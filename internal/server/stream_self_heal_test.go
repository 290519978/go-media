package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"maas-box/internal/gb28181"
	"maas-box/internal/model"
)

type mockSelfHealZLM struct {
	mu          sync.Mutex
	active      map[string]bool
	addCalls    map[string]int
	retryCounts map[string]string
	addDelay    time.Duration
	autoActive  bool
}

func newMockSelfHealZLMServer(t *testing.T) (*httptest.Server, *mockSelfHealZLM) {
	t.Helper()
	state := &mockSelfHealZLM{
		active:      make(map[string]bool),
		addCalls:    make(map[string]int),
		retryCounts: make(map[string]string),
		autoActive:  true,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/index/api/addStreamProxy":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse addStreamProxy form failed: %v", err)
			}
			app := strings.TrimSpace(r.Form.Get("app"))
			stream := strings.TrimSpace(r.Form.Get("stream"))
			retryCount := strings.TrimSpace(r.Form.Get("retry_count"))
			key := buildZLMAppStreamKey(app, stream)
			state.mu.Lock()
			delay := state.addDelay
			state.mu.Unlock()
			if delay > 0 {
				time.Sleep(delay)
			}
			state.mu.Lock()
			state.addCalls[key]++
			state.retryCounts[key] = retryCount
			if state.autoActive {
				state.active[key] = true
			}
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{},
			})
		case "/index/api/getMediaList":
			state.mu.Lock()
			items := make([]map[string]string, 0, len(state.active))
			for key, active := range state.active {
				if !active {
					continue
				}
				parts := strings.SplitN(key, "/", 2)
				if len(parts) != 2 {
					continue
				}
				items = append(items, map[string]string{
					"schema": "rtsp",
					"app":    parts[0],
					"stream": parts[1],
				})
			}
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": items,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return server, state
}

func (m *mockSelfHealZLM) setAddDelay(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addDelay = delay
}

func (m *mockSelfHealZLM) setAutoActive(auto bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.autoActive = auto
}

func (m *mockSelfHealZLM) setActive(app, stream string, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active[buildZLMAppStreamKey(app, stream)] = active
}

func (m *mockSelfHealZLM) addCallCount(app, stream string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addCalls[buildZLMAppStreamKey(app, stream)]
}

func (m *mockSelfHealZLM) retryCount(app, stream string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.retryCounts[buildZLMAppStreamKey(app, stream)]
}

func TestPreviewPullDevicePassesStoredRetryCountToZLM(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	zlmServer, zlmState := newMockSelfHealZLMServer(t)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	sourceID := "pull-preview-retry-count-1"
	streamID := buildZLMStreamID(sourceID)
	source := model.MediaSource{
		ID:            sourceID,
		Name:          "拉流设备-重试次数",
		AreaID:        model.RootAreaID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		StreamURL:     "rtsp://192.168.10.30:554/stream0",
		Status:        "offline",
		AIStatus:      model.DeviceAIStatusIdle,
		App:           "live",
		StreamID:      streamID,
		MediaServerID: "local",
		OutputConfig:  "{}",
		ExtraJSON:     "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	if err := s.db.Create(&model.StreamProxy{
		SourceID:   sourceID,
		OriginURL:  source.StreamURL,
		Transport:  "tcp",
		Enable:     true,
		RetryCount: 7,
	}).Error; err != nil {
		t.Fatalf("create stream proxy failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+sourceID+"/preview", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview device failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := zlmState.retryCount("live", streamID); got != "7" {
		t.Fatalf("expected retry_count=7, got=%q", got)
	}
}

func TestPreviewPullDeviceUsesDefaultRetryCountOneWhenProxyMissing(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	zlmServer, zlmState := newMockSelfHealZLMServer(t)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	sourceID := "pull-preview-default-retry-1"
	streamID := buildZLMStreamID(sourceID)
	source := model.MediaSource{
		ID:            sourceID,
		Name:          "拉流设备-默认重试",
		AreaID:        model.RootAreaID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		StreamURL:     "rtsp://192.168.10.31:554/stream0",
		Status:        "offline",
		AIStatus:      model.DeviceAIStatusIdle,
		App:           "live",
		StreamID:      streamID,
		MediaServerID: "local",
		OutputConfig:  "{}",
		ExtraJSON:     "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create source failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+sourceID+"/preview", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview device failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := zlmState.retryCount("live", streamID); got != "1" {
		t.Fatalf("expected retry_count=1, got=%q", got)
	}
}

func TestZLMOnStreamNotFoundSchedulesPullAutoHeal(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	engine := s.Engine()
	zlmServer, zlmState := newMockSelfHealZLMServer(t)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	sourceID := "pull-not-found-heal-1"
	streamID := buildZLMStreamID(sourceID)
	source := model.MediaSource{
		ID:            sourceID,
		Name:          "拉流缺流自愈",
		AreaID:        model.RootAreaID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		StreamURL:     "rtsp://192.168.10.31:554/stream0",
		Status:        "offline",
		AIStatus:      model.DeviceAIStatusIdle,
		App:           "live",
		StreamID:      streamID,
		MediaServerID: "local",
		OutputConfig:  "{}",
		ExtraJSON:     "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	if err := s.db.Create(&model.StreamProxy{
		SourceID:   sourceID,
		OriginURL:  source.StreamURL,
		Transport:  "tcp",
		Enable:     true,
		RetryCount: 5,
	}).Error; err != nil {
		t.Fatalf("create stream proxy failed: %v", err)
	}

	resp := postJSON(t, engine, "/webhook/on_stream_not_found?secret=zlm-hook-secret", []byte(`{"app":"live","stream":"`+streamID+`","schema":"rtsp"}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", resp.Code, resp.Body.String())
	}
	var out zlmHookReply
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("expected code=0, got=%+v", out)
	}

	waitForCondition(t, 2*time.Second, func() bool {
		return zlmState.addCallCount("live", streamID) == 1
	})
	if got := zlmState.retryCount("live", streamID); got != "5" {
		t.Fatalf("expected retry_count=5, got=%q", got)
	}

	var updated model.MediaSource
	if err := s.db.Where("id = ?", sourceID).First(&updated).Error; err != nil {
		t.Fatalf("query updated source failed: %v", err)
	}
	if updated.Status != "offline" {
		t.Fatalf("expected status remain offline until on_stream_changed, got=%s", updated.Status)
	}
}

func TestZLMOnStreamNotFoundCoalescesDuplicatePullAutoHeal(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	engine := s.Engine()
	zlmServer, zlmState := newMockSelfHealZLMServer(t)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL
	zlmState.setAddDelay(150 * time.Millisecond)

	sourceID := "pull-not-found-coalesce-1"
	streamID := buildZLMStreamID(sourceID)
	source := model.MediaSource{
		ID:            sourceID,
		Name:          "拉流缺流合并",
		AreaID:        model.RootAreaID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		StreamURL:     "rtsp://192.168.10.32:554/stream0",
		Status:        "offline",
		AIStatus:      model.DeviceAIStatusIdle,
		App:           "live",
		StreamID:      streamID,
		MediaServerID: "local",
		OutputConfig:  "{}",
		ExtraJSON:     "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	if err := s.db.Create(&model.StreamProxy{
		SourceID:   sourceID,
		OriginURL:  source.StreamURL,
		Transport:  "tcp",
		Enable:     true,
		RetryCount: 4,
	}).Error; err != nil {
		t.Fatalf("create stream proxy failed: %v", err)
	}

	body := []byte(`{"app":"live","stream":"` + streamID + `","schema":"rtsp"}`)
	resp1 := postJSON(t, engine, "/webhook/on_stream_not_found?secret=zlm-hook-secret", body)
	resp2 := postJSON(t, engine, "/webhook/on_stream_not_found?secret=zlm-hook-secret", body)
	if resp1.Code != http.StatusOK || resp2.Code != http.StatusOK {
		t.Fatalf("unexpected status codes: %d %d", resp1.Code, resp2.Code)
	}

	waitForCondition(t, 2*time.Second, func() bool {
		s.pullHealMu.Lock()
		running := s.pullHealRunning[sourceID]
		pending := s.pullHealPending[sourceID]
		s.pullHealMu.Unlock()
		return !running && !pending
	})
	if got := zlmState.addCallCount("live", streamID); got != 1 {
		t.Fatalf("expected only one addStreamProxy call after duplicate hooks, got=%d", got)
	}
}

func TestPreviewAndPullAutoHealShareSingleProxyCreation(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	engine := s.Engine()
	zlmServer, zlmState := newMockSelfHealZLMServer(t)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL
	zlmState.setAddDelay(150 * time.Millisecond)

	sourceID := "pull-preview-heal-shared-1"
	streamID := buildZLMStreamID(sourceID)
	source := model.MediaSource{
		ID:            sourceID,
		Name:          "拉流设备-并发恢复",
		AreaID:        model.RootAreaID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		StreamURL:     "rtsp://192.168.10.33:554/stream0",
		Status:        "offline",
		AIStatus:      model.DeviceAIStatusIdle,
		App:           "live",
		StreamID:      streamID,
		MediaServerID: "local",
		OutputConfig:  "{}",
		ExtraJSON:     "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	if err := s.db.Create(&model.StreamProxy{
		SourceID:   sourceID,
		OriginURL:  source.StreamURL,
		Transport:  "tcp",
		Enable:     true,
		RetryCount: 1,
	}).Error; err != nil {
		t.Fatalf("create stream proxy failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	previewDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+sourceID+"/preview", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		previewDone <- rec
	}()

	time.Sleep(30 * time.Millisecond)
	body := []byte(`{"app":"live","stream":"` + streamID + `","schema":"rtsp"}`)
	resp := postJSON(t, engine, "/webhook/on_stream_not_found?secret=zlm-hook-secret", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected hook status=%d body=%s", resp.Code, resp.Body.String())
	}

	previewResp := <-previewDone
	if previewResp.Code != http.StatusOK {
		t.Fatalf("preview failed: status=%d body=%s", previewResp.Code, previewResp.Body.String())
	}
	waitForCondition(t, 2*time.Second, func() bool {
		s.pullHealMu.Lock()
		running := s.pullHealRunning[sourceID]
		pending := s.pullHealPending[sourceID]
		s.pullHealMu.Unlock()
		return !running && !pending
	})
	if got := zlmState.addCallCount("live", streamID); got != 1 {
		t.Fatalf("expected shared single addStreamProxy call, got=%d", got)
	}
}

func TestZLMOnStreamNotFoundQueuesOnlyMissingGBChannel(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.gbService = &gb28181.Service{}
	engine := s.Engine()

	deviceID := "34020000001110109991"
	channelID1 := "34020000001310009991"
	channelID2 := "34020000001310009992"
	streamID1 := buildGBChannelSourceStreamID(deviceID, channelID1)
	streamID2 := buildGBChannelSourceStreamID(deviceID, channelID2)
	source1 := model.MediaSource{
		ID:           "gb-source-missing-1",
		Name:         "GB 缺流通道 1",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypeGB28181,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolGB28181,
		Transport:    "udp",
		App:          "rtp",
		StreamID:     streamID1,
		StreamURL:    "gb28181://" + deviceID + "/" + channelID1,
		ParentID:     "gb-parent-1",
		Status:       "offline",
		AIStatus:     model.DeviceAIStatusIdle,
		OutputConfig: `{"gb_device_id":"` + deviceID + `","gb_channel_id":"` + channelID1 + `","zlm_app":"rtp","zlm_stream":"` + streamID1 + `"}`,
		ExtraJSON:    "{}",
	}
	source2 := model.MediaSource{
		ID:           "gb-source-missing-2",
		Name:         "GB 缺流通道 2",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypeGB28181,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolGB28181,
		Transport:    "udp",
		App:          "rtp",
		StreamID:     streamID2,
		StreamURL:    "gb28181://" + deviceID + "/" + channelID2,
		ParentID:     "gb-parent-1",
		Status:       "offline",
		AIStatus:     model.DeviceAIStatusIdle,
		OutputConfig: `{"gb_device_id":"` + deviceID + `","gb_channel_id":"` + channelID2 + `","zlm_app":"rtp","zlm_stream":"` + streamID2 + `"}`,
		ExtraJSON:    "{}",
	}
	for _, item := range []model.MediaSource{source1, source2} {
		if err := s.db.Create(&item).Error; err != nil {
			t.Fatalf("create gb source failed: %v", err)
		}
	}
	if err := s.db.Create(&model.GBDevice{
		DeviceID:       deviceID,
		SourceIDDevice: "gb-parent-1",
		Name:           "GB 缺流设备",
		AreaID:         model.RootAreaID,
		Enabled:        true,
		Status:         "online",
		Transport:      "udp",
		Expires:        3600,
	}).Error; err != nil {
		t.Fatalf("create gb device failed: %v", err)
	}
	for _, channelID := range []string{channelID1, channelID2} {
		if err := s.db.Create(&model.GBChannel{
			DeviceID:  deviceID,
			ChannelID: channelID,
			Name:      "GB 通道 " + channelID,
			Status:    "ON",
		}).Error; err != nil {
			t.Fatalf("create gb channel failed: %v", err)
		}
	}

	key1 := buildGBChannelInviteKey(deviceID, channelID1)
	key2 := buildGBChannelInviteKey(deviceID, channelID2)
	s.gbInviteChannelRunning = map[string]bool{key1: true}
	s.gbInviteChannelPending = map[string]bool{}

	resp := postJSON(t, engine, "/webhook/on_stream_not_found?secret=zlm-hook-secret", []byte(`{"app":"rtp","stream":"`+streamID1+`","schema":"rtsp"}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", resp.Code, resp.Body.String())
	}
	if !s.gbInviteChannelPending[key1] {
		t.Fatalf("expected missing channel queued for auto invite")
	}
	if s.gbInviteChannelPending[key2] {
		t.Fatalf("did not expect other channel queued")
	}
	if s.gbInvitePending[deviceID] {
		t.Fatalf("did not expect device-level auto invite queued")
	}
}

func TestInviteGBDeviceChannelOnceSkipsWhenStreamAlreadyActive(t *testing.T) {
	s := newFocusedTestServer(t)
	s.gbService = &gb28181.Service{}
	zlmServer, zlmState := newMockSelfHealZLMServer(t)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	deviceID := "34020000001110109993"
	channelID := "34020000001310009993"
	streamID := buildGBChannelSourceStreamID(deviceID, channelID)
	zlmState.setActive("rtp", streamID, true)

	source := model.MediaSource{
		ID:           "gb-active-skip-source",
		Name:         "GB 活跃通道",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypeGB28181,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolGB28181,
		Transport:    "udp",
		App:          "rtp",
		StreamID:     streamID,
		StreamURL:    "gb28181://" + deviceID + "/" + channelID,
		ParentID:     "gb-active-parent",
		Status:       "offline",
		AIStatus:     model.DeviceAIStatusIdle,
		OutputConfig: `{"gb_device_id":"` + deviceID + `","gb_channel_id":"` + channelID + `","zlm_app":"rtp","zlm_stream":"` + streamID + `"}`,
		ExtraJSON:    "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create gb source failed: %v", err)
	}
	if err := s.db.Create(&model.GBDevice{
		DeviceID:       deviceID,
		SourceIDDevice: "gb-active-parent",
		Name:           "GB 活跃设备",
		AreaID:         model.RootAreaID,
		Enabled:        true,
		Status:         "online",
		Transport:      "udp",
		Expires:        3600,
	}).Error; err != nil {
		t.Fatalf("create gb device failed: %v", err)
	}
	if err := s.db.Create(&model.GBChannel{
		DeviceID:        deviceID,
		ChannelID:       channelID,
		SourceIDChannel: source.ID,
		Name:            "GB 活跃通道",
		Status:          "ON",
	}).Error; err != nil {
		t.Fatalf("create gb channel failed: %v", err)
	}

	if err := s.inviteGBDeviceChannelOnce(deviceID, channelID, 1, "active_skip"); err != nil {
		t.Fatalf("expected active stream skip without error, got=%v", err)
	}
}

func TestZLMOnStreamNotFoundAcceptsInvalidPayloadForAutoHeal(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	engine := s.Engine()

	req := httptest.NewRequest(http.MethodPost, "/webhook/on_stream_not_found?secret=zlm-hook-secret", bytes.NewReader([]byte(`{"app":`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out zlmHookReply
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("expected code=0 for invalid payload, got=%+v", out)
	}
}
