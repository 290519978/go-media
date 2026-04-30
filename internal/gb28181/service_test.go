package gb28181

import (
	"fmt"
	"strings"
	"testing"
	"time"

	sip "github.com/panjjo/gosip/sip"
)

type mockStore struct {
	authDevices map[string]AuthDevice
	states      map[string]DeviceState
	channels    map[string][]CatalogChannel
}

func newMockStore() *mockStore {
	return &mockStore{
		authDevices: map[string]AuthDevice{},
		states:      map[string]DeviceState{},
		channels:    map[string][]CatalogChannel{},
	}
}

func (m *mockStore) GetDeviceForAuth(deviceID string) (AuthDevice, error) {
	if item, ok := m.authDevices[deviceID]; ok {
		return item, nil
	}
	return AuthDevice{}, nil
}

func (m *mockStore) MarkRegistered(event RegisterEvent) error {
	item := m.states[event.DeviceID]
	item.DeviceID = event.DeviceID
	item.Status = "online"
	item.Expires = event.Expires
	item.LastRegisterAt = event.RegisteredAt
	m.states[event.DeviceID] = item
	return nil
}

func (m *mockStore) MarkOffline(deviceID string, offlineAt time.Time, _ string) error {
	item := m.states[deviceID]
	item.DeviceID = deviceID
	item.Status = "offline"
	item.LastKeepaliveAt = offlineAt
	m.states[deviceID] = item
	return nil
}

func (m *mockStore) MarkKeepalive(event KeepaliveEvent) error {
	item := m.states[event.DeviceID]
	item.DeviceID = event.DeviceID
	item.Status = "online"
	item.LastKeepaliveAt = event.KeepaliveAt
	m.states[event.DeviceID] = item
	return nil
}

func (m *mockStore) UpsertCatalog(event CatalogEvent) error {
	item := m.states[event.DeviceID]
	item.DeviceID = event.DeviceID
	item.Status = "online"
	item.LastKeepaliveAt = event.OccurredAt
	m.states[event.DeviceID] = item
	m.channels[event.DeviceID] = event.Channels
	return nil
}

func (m *mockStore) ListDeviceStates() ([]DeviceState, error) {
	out := make([]DeviceState, 0, len(m.states))
	for _, item := range m.states {
		out = append(out, item)
	}
	return out, nil
}

func TestIsValidGBDeviceID(t *testing.T) {
	if !isValidGBDeviceID("34020000001320000001") {
		t.Fatalf("expected device id valid")
	}
	if isValidGBDeviceID("3402000000132000000A") {
		t.Fatalf("expected invalid for alpha")
	}
	if isValidGBDeviceID("3402000000132000000") {
		t.Fatalf("expected invalid for short length")
	}
}

func TestVerifyDigestAuthorization(t *testing.T) {
	deviceID := "34020000001320000001"
	password := "123456"
	domain := "3402000000"
	uri := "sip:34020000002000000001@3402000000"
	nonce := "abcd1234"
	cnonce := "efgh5678"
	nc := "00000001"
	resp := sip.CalcResponse(deviceID, domain, password, "REGISTER", uri, nonce, "auth", cnonce, nc)

	raw := fmt.Sprintf("REGISTER %s SIP/2.0\r\nFrom: <sip:%s@%s>;tag=abc\r\nTo: <sip:34020000002000000001@%s>\r\nCall-ID: 1\r\nCSeq: 1 REGISTER\r\nVia: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK111\r\nAuthorization: Digest username=\"%s\",realm=\"%s\",nonce=\"%s\",uri=\"%s\",response=\"%s\",qop=auth,nc=%s,cnonce=\"%s\"\r\nContent-Length: 0\r\n\r\n",
		uri, deviceID, domain, domain, deviceID, domain, nonce, uri, resp, nc, cnonce)
	req, err := parseSIPRequest([]byte(raw))
	if err != nil {
		t.Fatalf("parse request failed: %v", err)
	}
	if !verifyDigestAuthorization(req, deviceID, password) {
		t.Fatalf("expected digest verify success")
	}
	if verifyDigestAuthorization(req, deviceID, "bad-password") {
		t.Fatalf("expected digest verify failure")
	}
}

func TestParseCatalogItems(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="GB2312"?>
<Response>
<CmdType>Catalog</CmdType>
<DeviceID>34020000001320000001</DeviceID>
<DeviceList Num="1">
<Item>
<DeviceID>34020000001320000011</DeviceID>
<Name>camera-1</Name>
<Manufacturer>HIK</Manufacturer>
<Model>DS-2CD</Model>
<Owner>admin</Owner>
<Status>ON</Status>
</Item>
</DeviceList>
</Response>`)
	items, err := parseCatalogItems(body)
	if err != nil {
		t.Fatalf("parse catalog failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 catalog item, got %d", len(items))
	}
	if items[0].ChannelID != "34020000001320000011" {
		t.Fatalf("unexpected channel id: %s", items[0].ChannelID)
	}
}

func TestShouldMarkOffline(t *testing.T) {
	now := time.Now()
	stateKeepaliveTimeout := DeviceState{
		DeviceID:        "34020000001320000001",
		Status:          "online",
		Expires:         3600,
		LastKeepaliveAt: now.Add(-181 * time.Second),
	}
	if !shouldMarkOffline(stateKeepaliveTimeout, now, 180, 30) {
		t.Fatalf("expected keepalive timeout")
	}

	stateRegisterTimeout := DeviceState{
		DeviceID:       "34020000001320000002",
		Status:         "online",
		Expires:        60,
		LastRegisterAt: now.Add(-95 * time.Second),
	}
	if !shouldMarkOffline(stateRegisterTimeout, now, 180, 30) {
		t.Fatalf("expected register grace timeout")
	}

	stateAlive := DeviceState{
		DeviceID:        "34020000001320000003",
		Status:          "online",
		Expires:         3600,
		LastKeepaliveAt: now.Add(-60 * time.Second),
	}
	if shouldMarkOffline(stateAlive, now, 180, 30) {
		t.Fatalf("expected device still online")
	}
}

func TestHandleRegisterAuthWithStoredDevice(t *testing.T) {
	deviceID := "34020000001320000001"
	store := newMockStore()
	store.authDevices[deviceID] = AuthDevice{
		DeviceID: deviceID,
		Password: "123456",
		Enabled:  true,
	}
	service, err := NewService(Config{
		Enabled:          true,
		ListenIP:         "127.0.0.1",
		Port:             15060,
		ServerID:         "34020000002000000001",
		Domain:           "3402000000",
		Password:         "",
		KeepaliveTimeout: 180,
		RegisterGrace:    30,
	}, store)
	if err != nil {
		t.Fatalf("new service failed: %v", err)
	}

	uri := "sip:34020000002000000001@3402000000"
	nonce := "abcd1234"
	cnonce := "efgh5678"
	nc := "00000001"
	resp := sip.CalcResponse(deviceID, "3402000000", "123456", "REGISTER", uri, nonce, "auth", cnonce, nc)
	authHeader := fmt.Sprintf("Digest username=\"%s\",realm=\"3402000000\",nonce=\"%s\",uri=\"%s\",response=\"%s\",qop=auth,nc=%s,cnonce=\"%s\"",
		deviceID, nonce, uri, resp, nc, cnonce)

	raw := fmt.Sprintf("REGISTER %s SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK111\r\nFrom: <sip:%s@3402000000>;tag=abc\r\nTo: <sip:34020000002000000001@3402000000>\r\nCall-ID: call-1\r\nCSeq: 1 REGISTER\r\nExpires: 3600\r\nAuthorization: %s\r\nContent-Length: 0\r\n\r\n", uri, deviceID, authHeader)
	req, err := parseSIPRequest([]byte(raw))
	if err != nil {
		t.Fatalf("parse register failed: %v", err)
	}
	out := ""
	writer := func(content string) error {
		out = content
		return nil
	}
	service.handleRegister(req, "udp", "127.0.0.1:5060", nil, writer)
	if !strings.Contains(out, "200 OK") {
		t.Fatalf("expected 200 OK, got: %s", out)
	}
	state := store.states[deviceID]
	if state.Status != "online" {
		t.Fatalf("expected online status after register")
	}
}

func TestHandleRegisterUnknownDeviceAutoCreate(t *testing.T) {
	service, err := NewService(Config{
		Enabled: true,
		Domain:  "3402000000",
	}, newMockStore())
	if err != nil {
		t.Fatalf("new service failed: %v", err)
	}
	raw := "REGISTER sip:34020000002000000001@3402000000 SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK111\r\nFrom: <sip:34020000001320000001@3402000000>;tag=abc\r\nTo: <sip:34020000002000000001@3402000000>\r\nCall-ID: call-1\r\nCSeq: 1 REGISTER\r\nExpires: 3600\r\nContent-Length: 0\r\n\r\n"
	req, err := parseSIPRequest([]byte(raw))
	if err != nil {
		t.Fatalf("parse register failed: %v", err)
	}
	out := ""
	service.handleRegister(req, "udp", "127.0.0.1:5060", nil, func(content string) error {
		out = content
		return nil
	})
	if !strings.Contains(out, "200 OK") {
		t.Fatalf("expected 200 OK, got: %s", out)
	}
	if store := service.store.(*mockStore); store.states["34020000001320000001"].Status != "online" {
		t.Fatalf("expected unknown device auto-created and online")
	}
}

func TestHandleMessageKeepaliveAndCatalog(t *testing.T) {
	deviceID := "34020000001320000001"
	store := newMockStore()
	store.authDevices[deviceID] = AuthDevice{
		DeviceID: deviceID,
		Enabled:  true,
	}
	service, err := NewService(Config{Enabled: true}, store)
	if err != nil {
		t.Fatalf("new service failed: %v", err)
	}

	keepaliveXML := fmt.Sprintf("<?xml version=\"1.0\"?><Notify><CmdType>Keepalive</CmdType><DeviceID>%s</DeviceID><Status>OK</Status></Notify>", deviceID)
	keepaliveMsg := fmt.Sprintf("MESSAGE sip:34020000002000000001@3402000000 SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK111\r\nFrom: <sip:%s@3402000000>;tag=abc\r\nTo: <sip:34020000002000000001@3402000000>\r\nCall-ID: call-2\r\nCSeq: 2 MESSAGE\r\nContent-Type: Application/MANSCDP+xml\r\nContent-Length: %d\r\n\r\n%s", deviceID, len([]byte(keepaliveXML)), keepaliveXML)
	keepaliveReq, err := parseSIPRequest([]byte(keepaliveMsg))
	if err != nil {
		t.Fatalf("parse keepalive message failed: %v", err)
	}
	out := ""
	service.handleMessage(keepaliveReq, "udp", "127.0.0.1:5060", nil, func(content string) error {
		out = content
		return nil
	})
	if !strings.Contains(out, "200 OK") {
		t.Fatalf("expected keepalive 200, got: %s", out)
	}
	if store.states[deviceID].LastKeepaliveAt.IsZero() {
		t.Fatalf("expected keepalive timestamp updated")
	}

	catalogXML := fmt.Sprintf(`<?xml version="1.0" encoding="GB2312"?>
<Response>
<CmdType>Catalog</CmdType>
<DeviceID>%s</DeviceID>
<DeviceList Num="1">
<Item>
<DeviceID>34020000001320000011</DeviceID>
<Name>camera-1</Name>
<Status>ON</Status>
</Item>
</DeviceList>
</Response>`, deviceID)
	catalogMsg := fmt.Sprintf("MESSAGE sip:34020000002000000001@3402000000 SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK112\r\nFrom: <sip:%s@3402000000>;tag=abc\r\nTo: <sip:34020000002000000001@3402000000>\r\nCall-ID: call-3\r\nCSeq: 3 MESSAGE\r\nContent-Type: Application/MANSCDP+xml\r\nContent-Length: %d\r\n\r\n%s",
		deviceID, len([]byte(catalogXML)), catalogXML)
	catalogReq, err := parseSIPRequest([]byte(catalogMsg))
	if err != nil {
		t.Fatalf("parse catalog message failed: %v", err)
	}
	service.handleMessage(catalogReq, "udp", "127.0.0.1:5060", nil, func(content string) error {
		out = content
		return nil
	})
	if !strings.Contains(out, "200 OK") {
		t.Fatalf("expected catalog 200, got: %s", out)
	}
	if len(store.channels[deviceID]) != 1 {
		t.Fatalf("expected 1 channel from catalog")
	}
}
