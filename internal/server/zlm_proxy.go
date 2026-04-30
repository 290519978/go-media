package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"maas-box/internal/config"
	"maas-box/internal/model"
)

var zlmStreamIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

type zlmAPIResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type zlmMediaItem struct {
	Schema string `json:"schema"`
	App    string `json:"app"`
	Stream string `json:"stream"`
}

type zlmOpenRTPServerData struct {
	Port int `json:"port"`
}

type zlmOpenRTPServerResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Port int             `json:"port"`
	Data json.RawMessage `json:"data"`
}

type zlmOutputConfigLite struct {
	App          string `json:"zlm_app"`
	Stream       string `json:"zlm_stream"`
	PublishToken string `json:"publish_token"`
}

const defaultStreamProxyRetryCount = 1

func normalizeStreamProxyRetryCount(retryCount int) int {
	if retryCount <= 0 {
		return defaultStreamProxyRetryCount
	}
	return retryCount
}

func (s *Server) lockZLMProxySource(sourceID string) func() {
	if s == nil {
		return func() {}
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return func() {}
	}
	s.zlmProxySourceMu.Lock()
	if s.zlmProxySourceLocks == nil {
		s.zlmProxySourceLocks = make(map[string]*sync.Mutex)
	}
	locker, ok := s.zlmProxySourceLocks[sourceID]
	if !ok {
		locker = &sync.Mutex{}
		s.zlmProxySourceLocks[sourceID] = locker
	}
	s.zlmProxySourceMu.Unlock()
	locker.Lock()
	return locker.Unlock
}

func (s *Server) normalizeLegacyPullRetryCounts() error {
	if s == nil || s.db == nil {
		return nil
	}
	// 方案 B 下把历史默认值 3 收敛为 1，避免 ZLM 与 Go 长时间双重重试叠加。
	now := time.Now()
	result := s.db.Model(&model.StreamProxy{}).
		Where("retry_count = ?", 3).
		Updates(map[string]any{"retry_count": defaultStreamProxyRetryCount, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return s.db.Model(&model.StreamProxy{}).
			Where("retry_count <= 0").
			Updates(map[string]any{"retry_count": defaultStreamProxyRetryCount, "updated_at": now}).Error
	}
	return s.db.Model(&model.StreamProxy{}).
		Where("retry_count <= 0").
		Updates(map[string]any{"retry_count": defaultStreamProxyRetryCount, "updated_at": now}).Error
}

func (s *Server) ensureZLMProxy(deviceID, sourceURL, transport string, retryCount int, requestHost string) (map[string]string, error) {
	if s.cfg.Server.ZLM.Disabled {
		return nil, fmt.Errorf("zlm is disabled")
	}
	if err := validateRTSPStreamURL(sourceURL); err != nil {
		return nil, err
	}

	app := strings.TrimSpace(s.cfg.Server.ZLM.App)
	if app == "" {
		app = "live"
	}
	stream := buildZLMStreamID(deviceID)
	transport = strings.ToLower(strings.TrimSpace(transport))
	rtpType := "0"
	if transport == "udp" {
		rtpType = "1"
	}
	unlock := s.lockZLMProxySource(deviceID)
	defer unlock()

	// 预览/抓拍/缺流自愈/重启恢复都会走到这里，先按 source 串行后再查活跃流，避免同一路并发重复建代理。
	if active, err := s.isZLMStreamActive(app, stream, model.ProtocolRTSP, model.ProtocolRTMP); err == nil && active {
		return s.buildZLMOutputConfig(app, stream, requestHost), nil
	}

	form := url.Values{}
	form.Set("secret", strings.TrimSpace(s.cfg.Server.ZLM.Secret))
	form.Set("vhost", "__defaultVhost__")
	form.Set("app", app)
	form.Set("stream", stream)
	form.Set("url", strings.TrimSpace(sourceURL))
	form.Set("rtp_type", rtpType)
	form.Set("retry_count", strconv.Itoa(normalizeStreamProxyRetryCount(retryCount)))
	form.Set("enable_hls", boolToZLMFlag(s.cfg.Server.ZLM.Output.EnableHLS))
	form.Set("enable_hls_fmp4", boolToZLMFlag(s.cfg.Server.ZLM.Output.EnableHLS))
	form.Set("enable_rtsp", "1")
	form.Set("enable_rtmp", "1")
	form.Set("enable_mp4", "0")

	if _, err := s.callZLMAPI("/index/api/addStreamProxy", form); err != nil {
		// addStreamProxy 在“流已存在”或瞬时状态下可能返回错误。
		// 如果该 app/stream 已经活跃，则按成功处理，避免首次预览偶发 502。
		if active, _ := s.isZLMStreamActive(app, stream, model.ProtocolRTSP, model.ProtocolRTMP); active {
			return s.buildZLMOutputConfig(app, stream, requestHost), nil
		}
		return nil, err
	}
	return s.buildZLMOutputConfig(app, stream, requestHost), nil
}

func (s *Server) buildRTMPOutputConfig(streamURL, requestHost string, rawOutputConfig json.RawMessage) (map[string]string, error) {
	if err := validateRTMPStreamURL(streamURL); err != nil {
		return nil, err
	}
	app, stream, err := parseRTMPAppStream(streamURL)
	if err != nil {
		return nil, err
	}
	output := s.buildZLMOutputConfig(app, stream, requestHost)
	publishURL := strings.TrimSpace(streamURL)
	if publishURL == "" {
		publishURL = s.buildZLMRTMPPublishURL(app, stream, requestHost)
	}
	output["publish_url"] = publishURL
	if token := parsePublishTokenFromOutputConfig(rawOutputConfig); token != "" {
		output["publish_token"] = token
	} else if token := parsePublishTokenFromRTMPURL(streamURL); token != "" {
		output["publish_token"] = token
	}
	return output, nil
}

func (s *Server) callZLMAPI(apiPath string, form url.Values) (json.RawMessage, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(s.cfg.Server.ZLM.APIURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("zlm api url is empty")
	}
	apiPath = strings.TrimSpace(apiPath)
	if apiPath == "" {
		return nil, fmt.Errorf("zlm api path is empty")
	}
	endpoint := baseURL + apiPath

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request zlm failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("zlm api status=%d", resp.StatusCode)
	}

	var out zlmAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode zlm response failed: %w", err)
	}
	if out.Code != 0 {
		msg := strings.TrimSpace(out.Msg)
		if isZLMAlreadyExistsMessage(msg) {
			return out.Data, nil
		}
		if isZLMSourceConnRefused(msg) {
			return nil, fmt.Errorf("rtsp source connection refused, check stream_url reachability; if using host-local simulator, use host.docker.internal instead of 127.0.0.1")
		}
		if msg == "" {
			msg = "unknown zlm error"
		}
		return nil, fmt.Errorf("zlm error(code=%d): %s", out.Code, msg)
	}
	return out.Data, nil
}

func (s *Server) buildZLMOutputConfig(app, stream, requestHost string) map[string]string {
	host := normalizeHost(strings.TrimSpace(s.cfg.Server.ZLM.PlayHost))
	if host == "" {
		host = normalizeHost(requestHost)
	}
	if host == "" {
		host = "127.0.0.1"
	}

	httpPort := s.cfg.Server.ZLM.HTTPPort
	rtspPort := s.cfg.Server.ZLM.RTSPPort
	rtmpPort := s.cfg.Server.ZLM.RTMPPort

	httpPrefix := fmt.Sprintf("http://%s:%d", host, httpPort)
	wsPrefix := fmt.Sprintf("ws://%s:%d", host, httpPort)

	output := map[string]string{
		"webrtc":     fmt.Sprintf("%s/index/api/webrtc?app=%s&stream=%s&type=play", httpPrefix, app, stream),
		"ws_flv":     fmt.Sprintf("%s/%s/%s.live.flv", wsPrefix, app, stream),
		"http_flv":   fmt.Sprintf("%s/%s/%s.live.flv", httpPrefix, app, stream),
		"hls":        fmt.Sprintf("%s/%s/%s/hls.m3u8", httpPrefix, app, stream),
		"rtsp":       fmt.Sprintf("rtsp://%s:%d/%s/%s", host, rtspPort, app, stream),
		"rtmp":       fmt.Sprintf("rtmp://%s:%d/%s/%s", host, rtmpPort, app, stream),
		"zlm_app":    app,
		"zlm_stream": stream,
	}
	return applyZLMOutputPolicy(output, s.cfg.Server.ZLM.Output)
}

func (s *Server) buildZLMRTMPPublishURL(app, stream, requestHost string) string {
	host := normalizeHost(strings.TrimSpace(s.cfg.Server.ZLM.PlayHost))
	if host == "" {
		host = normalizeHost(requestHost)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("rtmp://%s:%d/%s/%s", host, s.cfg.Server.ZLM.RTMPPort, app, stream)
}

func applyZLMOutputPolicy(output map[string]string, policy config.ZLMOutputConfig) map[string]string {
	filtered := make(map[string]string, len(output))
	for key, value := range output {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		filtered[key] = value
	}
	deleteIfDisabled(filtered, "webrtc", policy.EnableWebRTC)
	deleteIfDisabled(filtered, "ws_flv", policy.EnableWSFLV)
	deleteIfDisabled(filtered, "http_flv", policy.EnableHTTPFLV)
	deleteIfDisabled(filtered, "hls", policy.EnableHLS)
	return filtered
}

func deleteIfDisabled(payload map[string]string, key string, enabled bool) {
	if enabled {
		return
	}
	delete(payload, strings.TrimSpace(key))
}

func boolToZLMFlag(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func normalizeHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, ",") {
		raw = strings.TrimSpace(strings.Split(raw, ",")[0])
	}
	if host, _, err := net.SplitHostPort(raw); err == nil && strings.TrimSpace(host) != "" {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(raw, "[]")
}

func buildZLMStreamID(deviceID string) string {
	id := strings.TrimSpace(deviceID)
	if id == "" {
		id = "unknown"
	}
	id = zlmStreamIDSanitizer.ReplaceAllString(id, "_")
	return "device_" + id
}

func isZLMAlreadyExistsMessage(msg string) bool {
	msg = strings.ToLower(strings.TrimSpace(msg))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "exist") ||
		strings.Contains(msg, "already") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "已存在") ||
		strings.Contains(msg, "重复")
}

func isZLMSourceConnRefused(msg string) bool {
	msg = strings.ToLower(strings.TrimSpace(msg))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "connection refused")
}

func buildZLMProxyKey(vhost, app, stream string) string {
	vhost = strings.TrimSpace(vhost)
	if vhost == "" {
		vhost = "__defaultVhost__"
	}
	app = strings.TrimSpace(app)
	if app == "" {
		app = "live"
	}
	stream = strings.TrimSpace(stream)
	return fmt.Sprintf("%s/%s/%s", vhost, app, stream)
}

func (s *Server) deleteZLMProxy(app, stream string) error {
	if s.cfg.Server.ZLM.Disabled {
		return nil
	}
	stream = strings.TrimSpace(stream)
	if stream == "" {
		return fmt.Errorf("zlm stream is empty")
	}
	app = strings.TrimSpace(app)
	if app == "" {
		app = strings.TrimSpace(s.cfg.Server.ZLM.App)
	}
	if app == "" {
		app = "live"
	}

	form := url.Values{}
	form.Set("secret", strings.TrimSpace(s.cfg.Server.ZLM.Secret))
	form.Set("key", buildZLMProxyKey("__defaultVhost__", app, stream))

	if _, err := s.callZLMAPI("/index/api/delStreamProxy", form); err != nil {
		return err
	}
	return nil
}

func (s *Server) closeZLMStreams(app, stream string) error {
	if s == nil || s.cfg == nil || s.cfg.Server.ZLM.Disabled {
		return nil
	}
	stream = strings.TrimSpace(stream)
	if stream == "" {
		return nil
	}
	app = strings.TrimSpace(app)
	if app == "" {
		app = strings.TrimSpace(s.cfg.Server.ZLM.App)
	}
	if app == "" {
		app = "live"
	}
	form := url.Values{}
	form.Set("secret", strings.TrimSpace(s.cfg.Server.ZLM.Secret))
	form.Set("vhost", "__defaultVhost__")
	form.Set("app", app)
	form.Set("stream", stream)
	form.Set("force", "1")
	_, err := s.callZLMAPI("/index/api/close_streams", form)
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no such") || strings.Contains(msg, "count_hit=0") {
			return nil
		}
		return err
	}
	return nil
}

func (s *Server) openZLMRTPServer(streamID string, port, tcpMode int) (int, error) {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return 0, fmt.Errorf("zlm stream id is empty")
	}
	if s.cfg.Server.ZLM.Disabled {
		return 0, fmt.Errorf("zlm is disabled")
	}

	open := func() (int, error) {
		form := url.Values{}
		form.Set("secret", strings.TrimSpace(s.cfg.Server.ZLM.Secret))
		form.Set("stream_id", streamID)
		form.Set("port", fmt.Sprintf("%d", port))
		form.Set("tcp_mode", fmt.Sprintf("%d", tcpMode))
		out, err := s.callZLMOpenRTPServer(form)
		if err != nil {
			return 0, err
		}
		if out.Port > 0 {
			return out.Port, nil
		}
		dataPort, ok := extractZLMPortFromRaw(out.Data)
		if ok && dataPort > 0 {
			return dataPort, nil
		}
		if port > 0 {
			return port, nil
		}
		return 0, fmt.Errorf(
			"zlm openRtpServer returned empty port (msg=%s,data=%s)",
			strings.TrimSpace(out.Msg),
			strings.TrimSpace(string(out.Data)),
		)
	}

	openedPort, err := open()
	if err == nil {
		return openedPort, nil
	}
	if !isZLMOpenRTPAlreadyExistsError(err) {
		return 0, err
	}
	lastErr := err
	for i := 0; i < 4; i++ {
		_ = s.closeZLMRTPServer(streamID)
		time.Sleep(time.Duration(80*(i+1)) * time.Millisecond)
		openedPort, err = open()
		if err == nil {
			return openedPort, nil
		}
		lastErr = err
		if !isZLMOpenRTPAlreadyExistsError(err) {
			return 0, err
		}
	}
	return 0, lastErr
}

func isZLMOpenRTPAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "exist") ||
		strings.Contains(msg, "already") ||
		strings.Contains(msg, "duplicate")
}

func (s *Server) closeZLMRTPServer(streamID string) error {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" || s.cfg.Server.ZLM.Disabled {
		return nil
	}
	form := url.Values{}
	form.Set("secret", strings.TrimSpace(s.cfg.Server.ZLM.Secret))
	form.Set("stream_id", streamID)
	_, err := s.callZLMAPI("/index/api/closeRtpServer", form)
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no such") {
			return nil
		}
		return err
	}
	return nil
}

func (s *Server) callZLMOpenRTPServer(form url.Values) (*zlmOpenRTPServerResponse, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(s.cfg.Server.ZLM.APIURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("zlm api url is empty")
	}
	endpoint := baseURL + "/index/api/openRtpServer"

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request zlm failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("zlm api status=%d", resp.StatusCode)
	}

	var out zlmOpenRTPServerResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode zlm openRtpServer response failed: %w", err)
	}
	if out.Code != 0 {
		msg := strings.TrimSpace(out.Msg)
		if msg == "" {
			msg = "unknown zlm error"
		}
		return nil, fmt.Errorf("zlm error(code=%d): %s", out.Code, msg)
	}
	return &out, nil
}

func extractZLMPortFromRaw(raw json.RawMessage) (int, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, false
	}

	var asInt int
	if err := json.Unmarshal(trimmed, &asInt); err == nil && asInt > 0 {
		return asInt, true
	}

	var asString string
	if err := json.Unmarshal(trimmed, &asString); err == nil {
		asString = strings.TrimSpace(asString)
		if asString == "" {
			return 0, false
		}
		if parsed, err := strconv.Atoi(asString); err == nil && parsed > 0 {
			return parsed, true
		}
	}

	var asObj struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(trimmed, &asObj); err == nil && asObj.Port > 0 {
		return asObj.Port, true
	}

	var asMap map[string]any
	if err := json.Unmarshal(trimmed, &asMap); err == nil {
		if value, ok := asMap["port"]; ok {
			switch v := value.(type) {
			case float64:
				if int(v) > 0 {
					return int(v), true
				}
			case string:
				v = strings.TrimSpace(v)
				if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
					return parsed, true
				}
			}
		}
	}

	return 0, false
}

func parseZLMAppStreamFromOutputConfig(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	var item zlmOutputConfigLite
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return "", ""
	}
	return strings.TrimSpace(item.App), strings.TrimSpace(item.Stream)
}

func parsePublishTokenFromOutputConfig(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return ""
	}
	rawToken, ok := payload["publish_token"]
	if !ok || rawToken == nil {
		return ""
	}
	switch value := rawToken.(type) {
	case string:
		return strings.TrimSpace(value)
	default:
		token := strings.TrimSpace(fmt.Sprintf("%v", value))
		if token == "<nil>" {
			return ""
		}
		return token
	}
}

func parseRTMPAppStream(streamURL string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(streamURL))
	if err != nil {
		return "", "", fmt.Errorf("invalid rtmp stream url")
	}
	if !strings.EqualFold(parsed.Scheme, model.ProtocolRTMP) {
		return "", "", fmt.Errorf("stream_url must start with rtmp://")
	}
	appStream := strings.Trim(parsed.EscapedPath(), "/")
	parts := strings.Split(appStream, "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("rtmp stream url must include app and stream path")
	}
	app := strings.TrimSpace(parts[0])
	stream := strings.TrimSpace(strings.Join(parts[1:], "/"))
	if app == "" || stream == "" {
		return "", "", fmt.Errorf("rtmp stream url must include app and stream path")
	}
	return app, stream, nil
}

func parseRTMPPublishToken(params string) string {
	params = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(params), "?"))
	values, err := url.ParseQuery(params)
	if err != nil {
		return ""
	}
	for _, key := range []string{"token", "key", "sign", "password"} {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func parsePublishTokenFromRTMPURL(streamURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(streamURL))
	if err != nil {
		return ""
	}
	for _, key := range []string{"token", "key", "sign", "password"} {
		if value := strings.TrimSpace(parsed.Query().Get(key)); value != "" {
			return value
		}
	}
	if parsed.User != nil {
		if pass, ok := parsed.User.Password(); ok {
			return strings.TrimSpace(pass)
		}
	}
	return ""
}

func buildZLMAppStreamKey(app, stream string) string {
	app = strings.ToLower(strings.TrimSpace(app))
	stream = strings.TrimSpace(stream)
	if app == "" {
		app = "live"
	}
	return app + "/" + stream
}

func parseDeviceZLMAppStream(protocol string, device model.Device, defaultApp string) (string, string) {
	defaultApp = strings.TrimSpace(defaultApp)
	if defaultApp == "" {
		defaultApp = "live"
	}
	app, stream := parseZLMAppStreamFromOutputConfig(device.OutputConfig)
	if app == "" {
		app = strings.TrimSpace(device.App)
	}
	if stream == "" {
		stream = strings.TrimSpace(device.StreamID)
	}
	if app == "" {
		app = defaultApp
	}
	if stream == "" {
		if protocol == model.ProtocolRTSP {
			stream = buildZLMStreamID(device.ID)
		}
		if protocol == model.ProtocolRTMP {
			parsedApp, parsedStream, err := parseRTMPAppStream(device.StreamURL)
			if err == nil {
				if app == "" {
					app = parsedApp
				}
				stream = parsedStream
			}
		}
	}
	return app, stream
}

func (s *Server) listZLMActiveStreams(schemas ...string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	if s.cfg.Server.ZLM.Disabled {
		return out, nil
	}

	form := url.Values{}
	form.Set("secret", strings.TrimSpace(s.cfg.Server.ZLM.Secret))

	data, err := s.callZLMAPI("/index/api/getMediaList", form)
	if err != nil {
		return out, err
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return out, nil
	}

	var items []zlmMediaItem
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return out, fmt.Errorf("decode zlm media list failed: %w", err)
	}

	schemaSet := make(map[string]struct{}, len(schemas))
	for _, schema := range schemas {
		schema = strings.ToLower(strings.TrimSpace(schema))
		if schema == "" {
			continue
		}
		schemaSet[schema] = struct{}{}
	}

	for _, item := range items {
		schema := strings.ToLower(strings.TrimSpace(item.Schema))
		if len(schemaSet) > 0 {
			if _, ok := schemaSet[schema]; !ok {
				continue
			}
		}
		app := strings.TrimSpace(item.App)
		stream := strings.TrimSpace(item.Stream)
		if stream == "" {
			continue
		}
		key := buildZLMAppStreamKey(app, stream)
		out[key] = struct{}{}
	}
	return out, nil
}

func (s *Server) isZLMStreamActive(app, stream string, schemas ...string) (bool, error) {
	app = strings.TrimSpace(app)
	stream = strings.TrimSpace(stream)
	if stream == "" {
		return false, nil
	}
	active, err := s.listZLMActiveStreams(schemas...)
	if err != nil {
		return false, err
	}
	_, ok := active[buildZLMAppStreamKey(app, stream)]
	return ok, nil
}
