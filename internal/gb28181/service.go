package gb28181

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	sip "github.com/panjjo/gosip/sip"
	"golang.org/x/net/html/charset"
)

var (
	gbDeviceIDRegexp = regexp.MustCompile(`^\d{20}$`)
	deviceIDInSIPURI = regexp.MustCompile(`sip:(\d{20})@`)
)

type Config struct {
	Enabled          bool
	ListenIP         string
	Port             int
	ServerID         string
	Domain           string
	Password         string
	KeepaliveTimeout int
	RegisterGrace    int
}

type AuthDevice struct {
	DeviceID string
	Password string
	Enabled  bool
}

type RegisterEvent struct {
	DeviceID     string
	Transport    string
	SourceAddr   string
	Expires      int
	RegisteredAt time.Time
}

type KeepaliveEvent struct {
	DeviceID    string
	Transport   string
	SourceAddr  string
	KeepaliveAt time.Time
}

type CatalogChannel struct {
	ChannelID    string
	Name         string
	Manufacturer string
	Model        string
	Owner        string
	Status       string
}

type CatalogEvent struct {
	DeviceID   string
	RawXML     string
	Channels   []CatalogChannel
	OccurredAt time.Time
}

type DeviceState struct {
	DeviceID        string
	Status          string
	Expires         int
	LastRegisterAt  time.Time
	LastKeepaliveAt time.Time
}

type Store interface {
	GetDeviceForAuth(deviceID string) (AuthDevice, error)
	MarkRegistered(event RegisterEvent) error
	MarkOffline(deviceID string, offlineAt time.Time, reason string) error
	MarkKeepalive(event KeepaliveEvent) error
	UpsertCatalog(event CatalogEvent) error
	ListDeviceStates() ([]DeviceState, error)
}

type session struct {
	DeviceID   string
	Transport  string
	SourceAddr string
	Conn       net.Conn
	LastSeenAt time.Time
}

type playDialog struct {
	DeviceID  string
	ChannelID string
	Transport string
	Host      string
	Port      int
	CallID    string
	FromTag   string
	ToHeader  string
	CSeq      int
	StreamID  string
	StartedAt time.Time
}

type Service struct {
	cfg    Config
	store  Store
	logger *log.Logger

	udpConn net.PacketConn
	tcpLn   net.Listener

	mu       sync.RWMutex
	sessions map[string]*session

	pendingMu        sync.Mutex
	pendingResponses map[string]chan *sipResponse

	playMu      sync.RWMutex
	playDialogs map[string]*playDialog

	startOnce sync.Once
	closeOnce sync.Once
	wg        sync.WaitGroup
	closeCh   chan struct{}
}

type sipRequest struct {
	Method  string
	URI     string
	Version string
	Headers map[string]string
	Body    []byte
}

type sipResponse struct {
	Version    string
	StatusCode int
	Reason     string
	Headers    map[string]string
	Body       []byte
}

type responseWriter func(content string) error

func NewService(cfg Config, store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("gb28181 store is nil")
	}
	normalizeConfig(&cfg)
	return &Service{
		cfg:              cfg,
		store:            store,
		logger:           log.Default(),
		sessions:         make(map[string]*session),
		pendingResponses: make(map[string]chan *sipResponse),
		playDialogs:      make(map[string]*playDialog),
		closeCh:          make(chan struct{}),
	}, nil
}

func normalizeConfig(cfg *Config) {
	if cfg == nil {
		return
	}
	if strings.TrimSpace(cfg.ListenIP) == "" {
		cfg.ListenIP = "0.0.0.0"
	}
	if cfg.Port <= 0 {
		cfg.Port = 15060
	}
	if strings.TrimSpace(cfg.ServerID) == "" {
		cfg.ServerID = "34020000002000000001"
	}
	if strings.TrimSpace(cfg.Domain) == "" {
		cfg.Domain = "3402000000"
	}
	if cfg.KeepaliveTimeout <= 0 {
		cfg.KeepaliveTimeout = 180
	}
	if cfg.RegisterGrace <= 0 {
		cfg.RegisterGrace = 30
	}
}

func (s *Service) Start() error {
	if !s.cfg.Enabled {
		return nil
	}
	var startErr error
	s.startOnce.Do(func() {
		addr := net.JoinHostPort(s.cfg.ListenIP, strconv.Itoa(s.cfg.Port))
		udpConn, err := net.ListenPacket("udp", addr)
		if err != nil {
			startErr = fmt.Errorf("listen udp failed: %w", err)
			return
		}
		tcpLn, err := net.Listen("tcp", addr)
		if err != nil {
			_ = udpConn.Close()
			startErr = fmt.Errorf("listen tcp failed: %w", err)
			return
		}
		s.udpConn = udpConn
		s.tcpLn = tcpLn

		s.logger.Printf("GB28181 SIP service started on %s (udp+tcp)", addr)

		s.wg.Add(3)
		go s.serveUDP()
		go s.serveTCP()
		go s.scanOfflineLoop()
	})
	return startErr
}

func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		close(s.closeCh)
		if s.udpConn != nil {
			_ = s.udpConn.Close()
		}
		if s.tcpLn != nil {
			_ = s.tcpLn.Close()
		}
		s.mu.Lock()
		for _, item := range s.sessions {
			if item != nil && item.Conn != nil {
				_ = item.Conn.Close()
			}
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	return nil
}

func (s *Service) serveUDP() {
	defer s.wg.Done()
	buf := make([]byte, 64*1024)
	for {
		n, addr, err := s.udpConn.ReadFrom(buf)
		if err != nil {
			select {
			case <-s.closeCh:
				return
			default:
			}
			s.logger.Printf("GB28181 udp read failed: %v", err)
			continue
		}
		raw := make([]byte, n)
		copy(raw, buf[:n])
		writer := func(content string) error {
			_, werr := s.udpConn.WriteTo([]byte(content), addr)
			return werr
		}
		if isSIPResponsePacket(raw) {
			s.handleIncomingResponse(raw)
			continue
		}
		s.handleIncoming(raw, "udp", addr.String(), nil, writer)
	}
}

func (s *Service) serveTCP() {
	defer s.wg.Done()
	for {
		conn, err := s.tcpLn.Accept()
		if err != nil {
			select {
			case <-s.closeCh:
				return
			default:
			}
			s.logger.Printf("GB28181 tcp accept failed: %v", err)
			continue
		}
		s.wg.Add(1)
		go s.handleTCPConn(conn)
	}
}

func (s *Service) handleTCPConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for {
		packet, err := readSIPPacket(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			s.logger.Printf("GB28181 tcp read packet failed: %v", err)
			return
		}

		writer := func(content string) error {
			_, werr := io.WriteString(conn, content)
			return werr
		}
		if isSIPResponsePacket(packet) {
			s.handleIncomingResponse(packet)
			continue
		}
		s.handleIncoming(packet, "tcp", conn.RemoteAddr().String(), conn, writer)
	}
}

func (s *Service) handleIncoming(raw []byte, transport, source string, conn net.Conn, writer responseWriter) {
	req, err := parseSIPRequest(raw)
	if err != nil {
		s.logger.Printf("GB28181 parse request failed from %s: %v", source, err)
		return
	}
	switch strings.ToUpper(req.Method) {
	case string(sip.REGISTER):
		s.handleRegister(req, transport, source, conn, writer)
	case string(sip.MESSAGE):
		s.handleMessage(req, transport, source, conn, writer)
	case string(sip.OPTIONS):
		_ = writer(buildSIPResponse(req, 200, "OK", nil))
	default:
		_ = writer(buildSIPResponse(req, 405, "Method Not Allowed", nil))
	}
}

func (s *Service) handleRegister(req *sipRequest, transport, source string, conn net.Conn, writer responseWriter) {
	deviceID := firstNonEmpty(
		extractDeviceID(req.headersGet("from")),
		extractDeviceID(req.headersGet("to")),
		extractDeviceID(req.headersGet("contact")),
	)
	if !isValidGBDeviceID(deviceID) {
		_ = writer(buildSIPResponse(req, 400, "Bad Request", nil))
		return
	}

	authDevice, err := s.store.GetDeviceForAuth(deviceID)
	if err != nil {
		s.logger.Printf("GB28181 load device auth failed: %v", err)
		_ = writer(buildSIPResponse(req, 500, "Server Internal Error", nil))
		return
	}
	if authDevice.DeviceID != "" && !authDevice.Enabled {
		_ = writer(buildSIPResponse(req, 403, "Forbidden", nil))
		return
	}

	password := strings.TrimSpace(authDevice.Password)
	if password == "" {
		password = strings.TrimSpace(s.cfg.Password)
	}
	if password != "" {
		authHeader := strings.TrimSpace(req.headersGet("authorization"))
		if authHeader == "" {
			_ = writer(buildSIPResponse(req, 401, "Unauthorized", map[string]string{
				"WWW-Authenticate": s.buildDigestChallenge(),
			}))
			return
		}
		if !verifyDigestAuthorization(req, deviceID, password) {
			_ = writer(buildSIPResponse(req, 401, "Unauthorized", map[string]string{
				"WWW-Authenticate": s.buildDigestChallenge(),
			}))
			return
		}
	}

	expires := parseRegisterExpires(req)
	now := time.Now()
	if expires == 0 {
		if err := s.store.MarkOffline(deviceID, now, "unregister"); err != nil {
			s.logger.Printf("GB28181 mark offline failed: %v", err)
		}
		s.dropSession(deviceID)
		_ = writer(buildSIPResponse(req, 200, "OK", map[string]string{
			"Date": now.Format("2006-01-02T15:04:05.000"),
		}))
		return
	}
	if expires < 0 {
		expires = 3600
	}

	if err := s.store.MarkRegistered(RegisterEvent{
		DeviceID:     deviceID,
		Transport:    strings.ToLower(strings.TrimSpace(transport)),
		SourceAddr:   source,
		Expires:      expires,
		RegisteredAt: now,
	}); err != nil {
		s.logger.Printf("GB28181 mark register failed: %v", err)
		_ = writer(buildSIPResponse(req, 500, "Server Internal Error", nil))
		return
	}

	s.upsertSession(deviceID, strings.ToLower(strings.TrimSpace(transport)), source, conn, now)
	_ = writer(buildSIPResponse(req, 200, "OK", map[string]string{
		"Date": now.Format("2006-01-02T15:04:05.000"),
	}))

	go s.autoQueryCatalog(deviceID)
}

// 注册成功后进行多次目录查询，降低首包丢失导致“仅设备在线、无通道”的概率。
func (s *Service) autoQueryCatalog(deviceID string) {
	deviceID = strings.TrimSpace(deviceID)
	if !isValidGBDeviceID(deviceID) {
		return
	}
	const attempts = 3
	for i := 0; i < attempts; i++ {
		var delay time.Duration
		if i == 0 {
			delay = 300 * time.Millisecond
		} else {
			delay = 2 * time.Second
		}
		select {
		case <-s.closeCh:
			return
		case <-time.After(delay):
		}
		if err := s.QueryCatalog(deviceID); err != nil {
			s.logger.Printf("GB28181 auto query catalog failed: device=%s attempt=%d err=%v", deviceID, i+1, err)
		}
	}
}

func (s *Service) handleMessage(req *sipRequest, transport, source string, conn net.Conn, writer responseWriter) {
	cmdType, deviceID, err := parseMessageCmdType(req.Body)
	if err != nil {
		s.logger.Printf("GB28181 parse message cmd failed: %v", err)
		_ = writer(buildSIPResponse(req, 400, "Bad Request", nil))
		return
	}
	if deviceID == "" {
		deviceID = extractDeviceID(req.headersGet("from"))
	}
	if !isValidGBDeviceID(deviceID) {
		_ = writer(buildSIPResponse(req, 400, "Bad Request", nil))
		return
	}
	authDevice, err := s.store.GetDeviceForAuth(deviceID)
	if err != nil {
		s.logger.Printf("GB28181 load device auth failed: %v", err)
		_ = writer(buildSIPResponse(req, 500, "Server Internal Error", nil))
		return
	}
	if authDevice.DeviceID != "" && !authDevice.Enabled {
		_ = writer(buildSIPResponse(req, 403, "Forbidden", nil))
		return
	}

	now := time.Now()
	s.upsertSession(deviceID, strings.ToLower(strings.TrimSpace(transport)), source, conn, now)

	switch strings.ToLower(cmdType) {
	case "keepalive":
		if err := s.store.MarkKeepalive(KeepaliveEvent{
			DeviceID:    deviceID,
			Transport:   strings.ToLower(strings.TrimSpace(transport)),
			SourceAddr:  source,
			KeepaliveAt: now,
		}); err != nil {
			s.logger.Printf("GB28181 mark keepalive failed: %v", err)
			_ = writer(buildSIPResponse(req, 500, "Server Internal Error", nil))
			return
		}
	case "catalog":
		channels, perr := parseCatalogItems(req.Body)
		if perr != nil {
			s.logger.Printf("GB28181 parse catalog failed: %v", perr)
		}
		if err := s.store.UpsertCatalog(CatalogEvent{
			DeviceID:   deviceID,
			RawXML:     string(req.Body),
			Channels:   channels,
			OccurredAt: now,
		}); err != nil {
			s.logger.Printf("GB28181 upsert catalog failed: %v", err)
			_ = writer(buildSIPResponse(req, 500, "Server Internal Error", nil))
			return
		}
	default:
	}
	_ = writer(buildSIPResponse(req, 200, "OK", nil))
}

func (s *Service) QueryCatalog(deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if !isValidGBDeviceID(deviceID) {
		return errors.New("invalid gb device id")
	}
	s.mu.RLock()
	sess := s.sessions[deviceID]
	s.mu.RUnlock()
	if sess == nil {
		return errors.New("device session not found")
	}

	host, port, err := splitHostPort(sess.SourceAddr)
	if err != nil {
		return err
	}
	body := buildCatalogBody(deviceID)
	content := buildCatalogRequest(s.cfg, sess.Transport, deviceID, host, port, body)

	switch strings.ToLower(strings.TrimSpace(sess.Transport)) {
	case "tcp":
		if sess.Conn == nil {
			return errors.New("tcp session connection not found")
		}
		_, err = io.WriteString(sess.Conn, content)
		return err
	default:
		if s.udpConn == nil {
			return errors.New("udp listener not started")
		}
		addr, rerr := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
		if rerr != nil {
			return rerr
		}
		_, err = s.udpConn.WriteTo([]byte(content), addr)
		return err
	}
}

func (s *Service) InvitePlay(deviceID, channelID, mediaIP string, mediaPort int, streamID string) error {
	deviceID = strings.TrimSpace(deviceID)
	channelID = strings.TrimSpace(channelID)
	mediaIP = strings.TrimSpace(mediaIP)
	streamID = strings.TrimSpace(streamID)
	if !isValidGBDeviceID(deviceID) {
		return errors.New("invalid gb device id")
	}
	if channelID == "" {
		channelID = deviceID
	}
	if mediaIP == "" {
		return errors.New("media ip is empty")
	}
	if mediaPort <= 0 {
		return errors.New("media port is invalid")
	}
	if streamID == "" {
		streamID = channelID
	}
	_ = s.StopPlay(deviceID, channelID)

	s.mu.RLock()
	sess := s.sessions[deviceID]
	s.mu.RUnlock()
	if sess == nil {
		return errors.New("device session not found")
	}

	host, port, err := splitHostPort(sess.SourceAddr)
	if err != nil {
		return err
	}

	callID := randomToken(24)
	fromTag := randomToken(10)
	branch := "z9hG4bK" + randomToken(12)
	cseq := rand.Intn(90000) + 10000
	ssrc := buildPlaySSRC()

	body := buildInvitePlayBody(s.cfg.ServerID, mediaIP, mediaPort, ssrc)
	invite := buildInvitePlayRequest(s.cfg, strings.TrimSpace(sess.Transport), deviceID, channelID, host, port, body, callID, fromTag, branch, cseq, streamID, ssrc)

	waitCh := s.registerPendingResponse(callID)
	defer s.unregisterPendingResponse(callID)

	if err := s.writeSIPPacket(sess, host, port, invite); err != nil {
		return err
	}

	timeout := time.NewTimer(8 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-s.closeCh:
			return errors.New("service closed")
		case <-timeout.C:
			return errors.New("invite timeout")
		case resp := <-waitCh:
			if resp == nil {
				continue
			}
			if resp.StatusCode < 200 {
				continue
			}
			if resp.StatusCode >= 300 {
				return fmt.Errorf("invite rejected: %d %s", resp.StatusCode, strings.TrimSpace(resp.Reason))
			}
			ack := buildInviteACKRequest(s.cfg, strings.TrimSpace(sess.Transport), channelID, host, port, callID, fromTag, resp.headersGet("to"), cseq)
			if err := s.writeSIPPacket(sess, host, port, ack); err != nil {
				return fmt.Errorf("send ack failed: %w", err)
			}
			s.upsertPlayDialog(playDialog{
				DeviceID:  deviceID,
				ChannelID: channelID,
				Transport: strings.ToLower(strings.TrimSpace(sess.Transport)),
				Host:      host,
				Port:      port,
				CallID:    callID,
				FromTag:   fromTag,
				ToHeader:  strings.TrimSpace(resp.headersGet("to")),
				CSeq:      cseq,
				StreamID:  streamID,
				StartedAt: time.Now(),
			})
			return nil
		}
	}
}

func (s *Service) StopPlay(deviceID, channelID string) error {
	deviceID = strings.TrimSpace(deviceID)
	channelID = strings.TrimSpace(channelID)
	if deviceID == "" || channelID == "" {
		return nil
	}
	dialog := s.getPlayDialog(deviceID, channelID)
	if dialog == nil {
		return nil
	}
	return s.stopPlayDialog(dialog)
}

func (s *Service) StopAllPlays(deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	dialogs := s.listPlayDialogsByDevice(deviceID)
	if len(dialogs) == 0 {
		return nil
	}
	errs := make([]string, 0)
	for _, dialog := range dialogs {
		if err := s.stopPlayDialog(dialog); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "; "))
}

func (s *Service) stopPlayDialog(dialog *playDialog) error {
	if dialog == nil {
		return nil
	}
	deviceID := strings.TrimSpace(dialog.DeviceID)
	channelID := strings.TrimSpace(dialog.ChannelID)
	if deviceID == "" || channelID == "" {
		return nil
	}
	defer s.deletePlayDialog(deviceID, channelID)

	host := strings.TrimSpace(dialog.Host)
	port := dialog.Port
	if host == "" || port <= 0 {
		return errors.New("invalid play dialog address")
	}
	callID := strings.TrimSpace(dialog.CallID)
	if callID == "" {
		return errors.New("invalid play dialog call-id")
	}
	transport := strings.ToLower(strings.TrimSpace(dialog.Transport))
	if transport == "" {
		transport = "udp"
	}

	s.mu.RLock()
	sess := s.sessions[deviceID]
	s.mu.RUnlock()
	if sess == nil {
		sess = &session{
			DeviceID:   deviceID,
			Transport:  transport,
			SourceAddr: net.JoinHostPort(host, strconv.Itoa(port)),
		}
	} else if strings.ToLower(strings.TrimSpace(sess.Transport)) == "tcp" && sess.Conn == nil {
		return errors.New("tcp session connection not found")
	}

	byeCSeq := dialog.CSeq + 1
	if byeCSeq <= 0 {
		byeCSeq = rand.Intn(90000) + 10000
	}
	bye := buildInviteBYERequest(
		s.cfg,
		transport,
		channelID,
		host,
		port,
		callID,
		strings.TrimSpace(dialog.FromTag),
		strings.TrimSpace(dialog.ToHeader),
		byeCSeq,
	)

	waitCh := s.registerPendingResponse(callID)
	defer s.unregisterPendingResponse(callID)
	if err := s.writeSIPPacket(sess, host, port, bye); err != nil {
		return err
	}
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-s.closeCh:
			return errors.New("service closed")
		case <-timeout.C:
			return errors.New("bye timeout")
		case resp := <-waitCh:
			if resp == nil {
				continue
			}
			if resp.StatusCode < 200 {
				continue
			}
			if resp.StatusCode >= 300 {
				return fmt.Errorf("bye rejected: %d %s", resp.StatusCode, strings.TrimSpace(resp.Reason))
			}
			return nil
		}
	}
}

func (s *Service) writeSIPPacket(sess *session, host string, port int, content string) error {
	if sess == nil {
		return errors.New("nil sip session")
	}
	switch strings.ToLower(strings.TrimSpace(sess.Transport)) {
	case "tcp":
		if sess.Conn == nil {
			return errors.New("tcp session connection not found")
		}
		_, err := io.WriteString(sess.Conn, content)
		return err
	default:
		if s.udpConn == nil {
			return errors.New("udp listener not started")
		}
		addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return err
		}
		_, err = s.udpConn.WriteTo([]byte(content), addr)
		return err
	}
}

func (s *Service) registerPendingResponse(callID string) chan *sipResponse {
	callID = strings.TrimSpace(callID)
	ch := make(chan *sipResponse, 4)
	if callID == "" {
		return ch
	}
	s.pendingMu.Lock()
	s.pendingResponses[callID] = ch
	s.pendingMu.Unlock()
	return ch
}

func (s *Service) unregisterPendingResponse(callID string) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	s.pendingMu.Lock()
	if ch, ok := s.pendingResponses[callID]; ok {
		delete(s.pendingResponses, callID)
		close(ch)
	}
	s.pendingMu.Unlock()
}

func (s *Service) handleIncomingResponse(raw []byte) {
	resp, err := parseSIPResponse(raw)
	if err != nil {
		return
	}
	callID := strings.TrimSpace(resp.headersGet("call-id"))
	if callID == "" {
		return
	}
	s.pendingMu.Lock()
	ch := s.pendingResponses[callID]
	s.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

func (s *Service) buildDigestChallenge() string {
	nonce := randomToken(16)
	return fmt.Sprintf(`Digest realm="%s",nonce="%s",algorithm=MD5,qop="auth"`, s.cfg.Domain, nonce)
}

func (s *Service) upsertSession(deviceID, transport, source string, conn net.Conn, seenAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[deviceID] = &session{
		DeviceID:   deviceID,
		Transport:  transport,
		SourceAddr: source,
		Conn:       conn,
		LastSeenAt: seenAt,
	}
}

func buildPlayDialogKey(deviceID, channelID string) string {
	deviceID = strings.TrimSpace(deviceID)
	channelID = strings.TrimSpace(channelID)
	return deviceID + "|" + channelID
}

func (s *Service) upsertPlayDialog(item playDialog) {
	deviceID := strings.TrimSpace(item.DeviceID)
	channelID := strings.TrimSpace(item.ChannelID)
	if deviceID == "" || channelID == "" {
		return
	}
	key := buildPlayDialogKey(deviceID, channelID)
	item.DeviceID = deviceID
	item.ChannelID = channelID
	cp := item
	s.playMu.Lock()
	s.playDialogs[key] = &cp
	s.playMu.Unlock()
}

func (s *Service) getPlayDialog(deviceID, channelID string) *playDialog {
	deviceID = strings.TrimSpace(deviceID)
	channelID = strings.TrimSpace(channelID)
	if deviceID == "" || channelID == "" {
		return nil
	}
	key := buildPlayDialogKey(deviceID, channelID)
	s.playMu.RLock()
	item := s.playDialogs[key]
	s.playMu.RUnlock()
	if item == nil {
		return nil
	}
	cp := *item
	return &cp
}

func (s *Service) listPlayDialogsByDevice(deviceID string) []*playDialog {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	prefix := deviceID + "|"
	s.playMu.RLock()
	defer s.playMu.RUnlock()
	out := make([]*playDialog, 0)
	for key, item := range s.playDialogs {
		if !strings.HasPrefix(key, prefix) || item == nil {
			continue
		}
		cp := *item
		out = append(out, &cp)
	}
	return out
}

func (s *Service) deletePlayDialog(deviceID, channelID string) {
	deviceID = strings.TrimSpace(deviceID)
	channelID = strings.TrimSpace(channelID)
	if deviceID == "" || channelID == "" {
		return
	}
	key := buildPlayDialogKey(deviceID, channelID)
	s.playMu.Lock()
	delete(s.playDialogs, key)
	s.playMu.Unlock()
}

func (s *Service) clearPlayDialogsByDevice(deviceID string) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return
	}
	prefix := deviceID + "|"
	s.playMu.Lock()
	for key := range s.playDialogs {
		if strings.HasPrefix(key, prefix) {
			delete(s.playDialogs, key)
		}
	}
	s.playMu.Unlock()
}

func (s *Service) dropSession(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, deviceID)
	s.clearPlayDialogsByDevice(deviceID)
}

func (s *Service) ForgetDevice(deviceID string) {
	s.dropSession(strings.TrimSpace(deviceID))
}

func (s *Service) scanOfflineLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.closeCh:
			return
		case <-ticker.C:
			s.scanOffline()
		}
	}
}

func (s *Service) scanOffline() {
	states, err := s.store.ListDeviceStates()
	if err != nil {
		s.logger.Printf("GB28181 list device states failed: %v", err)
		return
	}
	now := time.Now()
	for _, item := range states {
		if !shouldMarkOffline(item, now, s.cfg.KeepaliveTimeout, s.cfg.RegisterGrace) {
			continue
		}
		if err := s.store.MarkOffline(item.DeviceID, now, "timeout"); err != nil {
			s.logger.Printf("GB28181 mark timeout offline failed: %v", err)
			continue
		}
		s.dropSession(item.DeviceID)
	}
}

func shouldMarkOffline(state DeviceState, now time.Time, keepaliveTimeoutSec, registerGraceSec int) bool {
	if !strings.EqualFold(strings.TrimSpace(state.Status), "online") {
		return false
	}
	if keepaliveTimeoutSec <= 0 {
		keepaliveTimeoutSec = 180
	}
	if registerGraceSec <= 0 {
		registerGraceSec = 30
	}

	if !state.LastKeepaliveAt.IsZero() {
		return now.Sub(state.LastKeepaliveAt) > time.Duration(keepaliveTimeoutSec)*time.Second
	}
	if state.LastRegisterAt.IsZero() {
		return false
	}
	expires := state.Expires
	if expires <= 0 {
		expires = 3600
	}
	timeout := time.Duration(expires+registerGraceSec) * time.Second
	return now.Sub(state.LastRegisterAt) > timeout
}

func parseSIPRequest(raw []byte) (*sipRequest, error) {
	headerPart, bodyPart, err := splitSIPHeaderBody(raw)
	if err != nil {
		return nil, err
	}
	lines := splitSIPLines(headerPart)
	if len(lines) == 0 {
		return nil, errors.New("empty sip packet")
	}
	first := strings.TrimSpace(lines[0])
	parts := strings.SplitN(first, " ", 3)
	if len(parts) != 3 {
		return nil, errors.New("invalid sip request line")
	}
	headers := make(map[string]string)
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		val := strings.TrimSpace(line[idx+1:])
		if key == "" {
			continue
		}
		if old, ok := headers[key]; ok && old != "" {
			headers[key] = old + "," + val
		} else {
			headers[key] = val
		}
	}

	if clRaw := strings.TrimSpace(headers["content-length"]); clRaw != "" {
		if cl, err := strconv.Atoi(clRaw); err == nil && cl >= 0 {
			if cl <= len(bodyPart) {
				bodyPart = bodyPart[:cl]
			}
		}
	}

	return &sipRequest{
		Method:  strings.ToUpper(strings.TrimSpace(parts[0])),
		URI:     strings.TrimSpace(parts[1]),
		Version: strings.TrimSpace(parts[2]),
		Headers: headers,
		Body:    bodyPart,
	}, nil
}

func parseSIPResponse(raw []byte) (*sipResponse, error) {
	headerPart, bodyPart, err := splitSIPHeaderBody(raw)
	if err != nil {
		return nil, err
	}
	lines := splitSIPLines(headerPart)
	if len(lines) == 0 {
		return nil, errors.New("empty sip packet")
	}
	first := strings.TrimSpace(lines[0])
	parts := strings.SplitN(first, " ", 3)
	if len(parts) < 2 {
		return nil, errors.New("invalid sip response line")
	}
	statusCode, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return nil, errors.New("invalid sip response status code")
	}
	reason := ""
	if len(parts) >= 3 {
		reason = strings.TrimSpace(parts[2])
	}

	headers := make(map[string]string)
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		val := strings.TrimSpace(line[idx+1:])
		if key == "" {
			continue
		}
		if old, ok := headers[key]; ok && old != "" {
			headers[key] = old + "," + val
		} else {
			headers[key] = val
		}
	}

	if clRaw := strings.TrimSpace(headers["content-length"]); clRaw != "" {
		if cl, err := strconv.Atoi(clRaw); err == nil && cl >= 0 && cl <= len(bodyPart) {
			bodyPart = bodyPart[:cl]
		}
	}

	return &sipResponse{
		Version:    strings.TrimSpace(parts[0]),
		StatusCode: statusCode,
		Reason:     reason,
		Headers:    headers,
		Body:       bodyPart,
	}, nil
}

func splitSIPHeaderBody(raw []byte) ([]byte, []byte, error) {
	if len(raw) == 0 {
		return nil, nil, errors.New("empty packet")
	}
	if idx := bytes.Index(raw, []byte("\r\n\r\n")); idx >= 0 {
		return raw[:idx], raw[idx+4:], nil
	}
	if idx := bytes.Index(raw, []byte("\n\n")); idx >= 0 {
		return raw[:idx], raw[idx+2:], nil
	}
	return nil, nil, errors.New("invalid sip packet delimiter")
}

func isSIPResponsePacket(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	firstLine := raw
	if idx := bytes.IndexByte(raw, '\n'); idx >= 0 {
		firstLine = raw[:idx]
	}
	line := strings.ToUpper(strings.TrimSpace(string(firstLine)))
	return strings.HasPrefix(line, "SIP/2.0 ")
}

func splitSIPLines(raw []byte) []string {
	text := string(raw)
	if strings.Contains(text, "\r\n") {
		return strings.Split(text, "\r\n")
	}
	return strings.Split(text, "\n")
}

func (s *sipRequest) headersGet(key string) string {
	if s == nil {
		return ""
	}
	return s.Headers[strings.ToLower(strings.TrimSpace(key))]
}

func (s *sipResponse) headersGet(key string) string {
	if s == nil {
		return ""
	}
	return s.Headers[strings.ToLower(strings.TrimSpace(key))]
}

func parseRegisterExpires(req *sipRequest) int {
	if req == nil {
		return 3600
	}
	if v, err := strconv.Atoi(strings.TrimSpace(req.headersGet("expires"))); err == nil {
		return v
	}
	contact := req.headersGet("contact")
	for _, part := range strings.Split(contact, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "expires=") {
			raw := strings.TrimSpace(part[8:])
			if v, err := strconv.Atoi(raw); err == nil {
				return v
			}
		}
	}
	return 3600
}

func isValidGBDeviceID(v string) bool {
	return gbDeviceIDRegexp.MatchString(strings.TrimSpace(v))
}

func extractDeviceID(headerValue string) string {
	headerValue = strings.TrimSpace(headerValue)
	if headerValue == "" {
		return ""
	}
	if groups := deviceIDInSIPURI.FindStringSubmatch(headerValue); len(groups) > 1 {
		return groups[1]
	}
	if groups := gbDeviceIDRegexp.FindStringSubmatch(headerValue); len(groups) > 0 {
		return groups[0]
	}
	return ""
}

func verifyDigestAuthorization(req *sipRequest, deviceID, password string) bool {
	authHeader := strings.TrimSpace(req.headersGet("authorization"))
	if authHeader == "" {
		return false
	}
	auth := sip.AuthFromValue(authHeader)
	uri := strings.TrimSpace(auth.Get("uri"))
	if uri == "" {
		uri = req.URI
	}
	auth.SetUsername(deviceID).SetPassword(password).SetMethod(strings.ToUpper(strings.TrimSpace(req.Method))).SetURI(uri)
	actual := strings.ToLower(strings.TrimSpace(auth.Get("response")))
	expected := strings.ToLower(strings.TrimSpace(auth.CalcResponse()))
	return actual != "" && expected != "" && actual == expected
}

func buildSIPResponse(req *sipRequest, code int, reason string, extraHeaders map[string]string) string {
	if reason == "" {
		reason = "Unknown"
	}
	lines := []string{fmt.Sprintf("SIP/2.0 %d %s", code, reason)}
	if via := strings.TrimSpace(req.headersGet("via")); via != "" {
		lines = append(lines, "Via: "+via)
	}
	if from := strings.TrimSpace(req.headersGet("from")); from != "" {
		lines = append(lines, "From: "+from)
	}
	if to := strings.TrimSpace(req.headersGet("to")); to != "" {
		lines = append(lines, "To: "+ensureSIPToTag(to))
	}
	if callID := strings.TrimSpace(req.headersGet("call-id")); callID != "" {
		lines = append(lines, "Call-ID: "+callID)
	}
	if cseq := strings.TrimSpace(req.headersGet("cseq")); cseq != "" {
		lines = append(lines, "CSeq: "+cseq)
	}
	lines = append(lines, "User-Agent: maas-box")
	for _, key := range []string{"WWW-Authenticate", "Date"} {
		if value := strings.TrimSpace(extraHeaders[key]); value != "" {
			lines = append(lines, fmt.Sprintf("%s: %s", key, value))
		}
	}
	lines = append(lines, "Content-Length: 0", "", "")
	return strings.Join(lines, "\r\n")
}

func ensureSIPToTag(to string) string {
	lower := strings.ToLower(to)
	if strings.Contains(lower, ";tag=") {
		return to
	}
	return fmt.Sprintf("%s;tag=%s", to, randomToken(8))
}

func parseMessageCmdType(body []byte) (cmdType string, deviceID string, err error) {
	var base struct {
		CmdType  string `xml:"CmdType"`
		DeviceID string `xml:"DeviceID"`
	}
	if derr := decodeGBXML(body, &base); derr != nil {
		return "", "", derr
	}
	return strings.TrimSpace(base.CmdType), strings.TrimSpace(base.DeviceID), nil
}

func parseCatalogItems(body []byte) ([]CatalogChannel, error) {
	var payload struct {
		DeviceList struct {
			Items []struct {
				DeviceID     string `xml:"DeviceID"`
				Name         string `xml:"Name"`
				Manufacturer string `xml:"Manufacturer"`
				Model        string `xml:"Model"`
				Owner        string `xml:"Owner"`
				Status       string `xml:"Status"`
			} `xml:"Item"`
		} `xml:"DeviceList"`
	}
	if err := decodeGBXML(body, &payload); err != nil {
		return nil, err
	}
	out := make([]CatalogChannel, 0, len(payload.DeviceList.Items))
	for _, item := range payload.DeviceList.Items {
		chID := strings.TrimSpace(item.DeviceID)
		if chID == "" {
			continue
		}
		out = append(out, CatalogChannel{
			ChannelID:    chID,
			Name:         strings.TrimSpace(item.Name),
			Manufacturer: strings.TrimSpace(item.Manufacturer),
			Model:        strings.TrimSpace(item.Model),
			Owner:        strings.TrimSpace(item.Owner),
			Status:       strings.TrimSpace(item.Status),
		})
	}
	return out, nil
}

func decodeGBXML(body []byte, out any) error {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.CharsetReader = charset.NewReaderLabel
	return decoder.Decode(out)
}

func buildCatalogBody(deviceID string) string {
	sn := time.Now().UnixNano()%900000 + 100000
	return fmt.Sprintf(`<?xml version="1.0" encoding="GB2312"?>
<Query>
<CmdType>Catalog</CmdType>
<SN>%d</SN>
<DeviceID>%s</DeviceID>
</Query>`, sn, deviceID)
}

func buildCatalogRequest(cfg Config, transport, deviceID, host string, port int, body string) string {
	transportUpper := strings.ToUpper(strings.TrimSpace(transport))
	if transportUpper == "" {
		transportUpper = "UDP"
	}
	viaHost := strings.TrimSpace(cfg.ListenIP)
	if viaHost == "" || viaHost == "0.0.0.0" || viaHost == "::" {
		viaHost = "127.0.0.1"
	}
	callID := randomToken(24)
	branch := "z9hG4bK" + randomToken(12)
	cseq := rand.Intn(90000) + 10000

	lines := []string{
		fmt.Sprintf("MESSAGE sip:%s@%s:%d SIP/2.0", deviceID, host, port),
		fmt.Sprintf("Via: SIP/2.0/%s %s:%d;branch=%s;rport", transportUpper, viaHost, cfg.Port, branch),
		fmt.Sprintf("From: <sip:%s@%s>;tag=%s", cfg.ServerID, cfg.Domain, randomToken(10)),
		fmt.Sprintf("To: <sip:%s@%s>", deviceID, cfg.Domain),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %d MESSAGE", cseq),
		"Max-Forwards: 70",
		"Content-Type: Application/MANSCDP+xml",
		"User-Agent: maas-box",
		fmt.Sprintf("Content-Length: %d", len([]byte(body))),
		"",
		body,
	}
	return strings.Join(lines, "\r\n")
}

func buildPlaySSRC() string {
	return fmt.Sprintf("0%09d", rand.Intn(1000000000))
}

func buildInvitePlayBody(serverID, mediaIP string, mediaPort int, ssrc string) string {
	if strings.TrimSpace(ssrc) == "" {
		ssrc = buildPlaySSRC()
	}
	return fmt.Sprintf(`v=0
o=%s 0 0 IN IP4 %s
s=Play
c=IN IP4 %s
t=0 0
m=video %d RTP/AVP 96 97 98
a=recvonly
a=rtpmap:96 PS/90000
a=rtpmap:97 MPEG4/90000
a=rtpmap:98 H264/90000
y=%s
f=
`, strings.TrimSpace(serverID), strings.TrimSpace(mediaIP), strings.TrimSpace(mediaIP), mediaPort, strings.TrimSpace(ssrc))
}

func buildInvitePlayRequest(cfg Config, transport, deviceID, channelID, host string, port int, body, callID, fromTag, branch string, cseq int, streamID, ssrc string) string {
	transportUpper := strings.ToUpper(strings.TrimSpace(transport))
	if transportUpper == "" {
		transportUpper = "UDP"
	}
	viaHost := strings.TrimSpace(cfg.ListenIP)
	if viaHost == "" || viaHost == "0.0.0.0" || viaHost == "::" {
		viaHost = "127.0.0.1"
	}
	if strings.TrimSpace(streamID) == "" {
		streamID = channelID
	}
	if strings.TrimSpace(ssrc) == "" {
		ssrc = buildPlaySSRC()
	}
	lines := []string{
		fmt.Sprintf("INVITE sip:%s@%s:%d SIP/2.0", channelID, host, port),
		fmt.Sprintf("Via: SIP/2.0/%s %s:%d;branch=%s;rport", transportUpper, viaHost, cfg.Port, branch),
		fmt.Sprintf("From: <sip:%s@%s>;tag=%s", cfg.ServerID, cfg.Domain, fromTag),
		fmt.Sprintf("To: <sip:%s@%s>", channelID, cfg.Domain),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %d INVITE", cseq),
		"Max-Forwards: 70",
		fmt.Sprintf("Contact: <sip:%s@%s:%d>", cfg.ServerID, viaHost, cfg.Port),
		fmt.Sprintf("Subject: %s:%s,%s:%s", channelID, streamID, deviceID, streamID),
		"Content-Type: Application/SDP",
		"User-Agent: maas-box",
		fmt.Sprintf("Content-Length: %d", len([]byte(body))),
		"",
		body,
	}
	return strings.Join(lines, "\r\n")
}

func buildInviteACKRequest(cfg Config, transport, channelID, host string, port int, callID, fromTag, toHeader string, cseq int) string {
	transportUpper := strings.ToUpper(strings.TrimSpace(transport))
	if transportUpper == "" {
		transportUpper = "UDP"
	}
	viaHost := strings.TrimSpace(cfg.ListenIP)
	if viaHost == "" || viaHost == "0.0.0.0" || viaHost == "::" {
		viaHost = "127.0.0.1"
	}
	branch := "z9hG4bK" + randomToken(12)
	toHeader = strings.TrimSpace(toHeader)
	if toHeader == "" {
		toHeader = fmt.Sprintf("<sip:%s@%s>", channelID, cfg.Domain)
	}
	lines := []string{
		fmt.Sprintf("ACK sip:%s@%s:%d SIP/2.0", channelID, host, port),
		fmt.Sprintf("Via: SIP/2.0/%s %s:%d;branch=%s;rport", transportUpper, viaHost, cfg.Port, branch),
		fmt.Sprintf("From: <sip:%s@%s>;tag=%s", cfg.ServerID, cfg.Domain, fromTag),
		fmt.Sprintf("To: %s", toHeader),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %d ACK", cseq),
		"Max-Forwards: 70",
		"User-Agent: maas-box",
		"Content-Length: 0",
		"",
		"",
	}
	return strings.Join(lines, "\r\n")
}

func buildInviteBYERequest(cfg Config, transport, channelID, host string, port int, callID, fromTag, toHeader string, cseq int) string {
	transportUpper := strings.ToUpper(strings.TrimSpace(transport))
	if transportUpper == "" {
		transportUpper = "UDP"
	}
	viaHost := strings.TrimSpace(cfg.ListenIP)
	if viaHost == "" || viaHost == "0.0.0.0" || viaHost == "::" {
		viaHost = "127.0.0.1"
	}
	if strings.TrimSpace(fromTag) == "" {
		fromTag = randomToken(10)
	}
	branch := "z9hG4bK" + randomToken(12)
	toHeader = strings.TrimSpace(toHeader)
	if toHeader == "" {
		toHeader = fmt.Sprintf("<sip:%s@%s>", channelID, cfg.Domain)
	}
	if cseq <= 0 {
		cseq = rand.Intn(90000) + 10000
	}
	lines := []string{
		fmt.Sprintf("BYE sip:%s@%s:%d SIP/2.0", channelID, host, port),
		fmt.Sprintf("Via: SIP/2.0/%s %s:%d;branch=%s;rport", transportUpper, viaHost, cfg.Port, branch),
		fmt.Sprintf("From: <sip:%s@%s>;tag=%s", cfg.ServerID, cfg.Domain, fromTag),
		fmt.Sprintf("To: %s", toHeader),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %d BYE", cseq),
		"Max-Forwards: 70",
		"User-Agent: maas-box",
		"Content-Length: 0",
		"",
		"",
	}
	return strings.Join(lines, "\r\n")
}

func readSIPPacket(reader *bufio.Reader) ([]byte, error) {
	var buffer bytes.Buffer
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	buffer.WriteString(line)

	contentLength := 0
	for {
		ln, rerr := reader.ReadString('\n')
		if rerr != nil {
			return nil, rerr
		}
		buffer.WriteString(ln)
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(strings.ToLower(trimmed), "content-length:") {
			raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "Content-Length:"))
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "content-length:"))
			if v, perr := strconv.Atoi(raw); perr == nil && v >= 0 {
				contentLength = v
			}
		}
		if trimmed == "" {
			break
		}
	}
	if contentLength > 0 {
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			return nil, err
		}
		buffer.Write(body)
	}
	return buffer.Bytes(), nil
}

func randomToken(size int) string {
	if size <= 0 {
		size = 8
	}
	const alphabet = "0123456789abcdef"
	var builder strings.Builder
	builder.Grow(size)
	for i := 0; i < size; i++ {
		builder.WriteByte(alphabet[rand.Intn(len(alphabet))])
	}
	return builder.String()
}

func splitHostPort(addr string) (string, int, error) {
	host, portRaw, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
