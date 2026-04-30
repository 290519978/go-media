package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"maas-box/internal/gb28181"
	"maas-box/internal/model"
)

func TestZLMOnPublishAllowRTMP(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.cfg.Server.ZLM.App = "live"

	device := model.Device{
		ID:              "dev-rtmp-allow",
		Name:            "RTMP Allow Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePush,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTMP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev-rtmp-allow",
		StreamURL:       "rtmp://127.0.0.1:11935/live/dev-rtmp-allow?token=tok-allow",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    `{"zlm_app":"live","zlm_stream":"dev-rtmp-allow","publish_token":"tok-allow"}`,
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	if err := s.db.Create(&model.StreamPush{SourceID: device.ID, PublishToken: "tok-allow"}).Error; err != nil {
		t.Fatalf("create stream push failed: %v", err)
	}

	engine := s.Engine()
	body := []byte(`{"app":"live","stream":"dev-rtmp-allow","schema":"rtmp","params":"token=tok-allow"}`)
	resp := postJSON(t, engine, "/webhook/on_publish?secret=zlm-hook-secret", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", resp.Code, resp.Body.String())
	}
	var out zlmHookReply
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("expected allow code=0, got=%+v", out)
	}

	var updated model.Device
	if err := s.db.Where("id = ?", device.ID).First(&updated).Error; err != nil {
		t.Fatalf("query updated device failed: %v", err)
	}
	if updated.Status != "offline" {
		t.Fatalf("expected status unchanged before on_stream_changed, got=%s", updated.Status)
	}
}

func TestZLMOnPublishRejectWrongToken(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.cfg.Server.ZLM.App = "live"

	device := model.Device{
		ID:              "dev-rtmp-deny",
		Name:            "RTMP Deny Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePush,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTMP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev-rtmp-deny",
		StreamURL:       "rtmp://127.0.0.1:11935/live/dev-rtmp-deny?token=tok-expected",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    `{"zlm_app":"live","zlm_stream":"dev-rtmp-deny","publish_token":"tok-expected"}`,
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	if err := s.db.Create(&model.StreamPush{SourceID: device.ID, PublishToken: "tok-expected"}).Error; err != nil {
		t.Fatalf("create stream push failed: %v", err)
	}

	engine := s.Engine()
	body := []byte(`{"app":"live","stream":"dev-rtmp-deny","schema":"rtmp","params":"token=tok-wrong"}`)
	resp := postJSON(t, engine, "/webhook/on_publish?secret=zlm-hook-secret", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", resp.Code, resp.Body.String())
	}
	var out zlmHookReply
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if out.Code == 0 {
		t.Fatalf("expected reject code!=0, got=%+v", out)
	}

	var updated model.Device
	if err := s.db.Where("id = ?", device.ID).First(&updated).Error; err != nil {
		t.Fatalf("query updated device failed: %v", err)
	}
	if updated.Status != "offline" {
		t.Fatalf("expected status=offline, got=%s", updated.Status)
	}
}

func TestZLMOnStreamChangedSetOffline(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.cfg.Server.ZLM.App = "live"

	device := model.Device{
		ID:              "dev-rtmp-offline",
		Name:            "RTMP Offline Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePush,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTMP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev-rtmp-offline",
		StreamURL:       "rtmp://127.0.0.1:11935/live/dev-rtmp-offline?token=tok",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "online",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    `{"zlm_app":"live","zlm_stream":"dev-rtmp-offline","publish_token":"tok"}`,
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	if err := s.db.Create(&model.StreamPush{SourceID: device.ID, PublishToken: "tok"}).Error; err != nil {
		t.Fatalf("create stream push failed: %v", err)
	}

	engine := s.Engine()
	body := []byte(`{"app":"live","stream":"dev-rtmp-offline","schema":"rtmp","regist":false}`)
	resp := postJSON(t, engine, "/webhook/on_stream_changed?secret=zlm-hook-secret", body)
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

	var updated model.Device
	if err := s.db.Where("id = ?", device.ID).First(&updated).Error; err != nil {
		t.Fatalf("query updated device failed: %v", err)
	}
	if updated.Status != "offline" {
		t.Fatalf("expected status=offline, got=%s", updated.Status)
	}
}

func TestZLMOnStreamChangedGBRegistFalseSetsOffline(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.cfg.Server.ZLM.App = "live"

	deviceID := "34020000001110103911"
	channelID := "34020000001310000001"
	streamID := deviceID + "_" + channelID
	source := model.MediaSource{
		ID:              "gb-stream-ch-1",
		Name:            "GB 通道 1",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypeGB28181,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolGB28181,
		Transport:       "udp",
		App:             "rtp",
		StreamID:        streamID,
		StreamURL:       "gb28181://" + deviceID + "/" + channelID,
		Status:          "online",
		AIStatus:        model.DeviceAIStatusIdle,
		EnableRecording: false,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		OutputConfig:    `{"gb_device_id":"` + deviceID + `","zlm_app":"rtp","zlm_stream":"` + streamID + `"}`,
		ExtraJSON:       "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create gb source failed: %v", err)
	}
	gbDevice := model.GBDevice{
		DeviceID:       deviceID,
		SourceIDDevice: "gb-device-row-1",
		Name:           "GB 设备 1",
		AreaID:         model.RootAreaID,
		Enabled:        true,
		Status:         "online",
		Transport:      "udp",
		Expires:        3600,
	}
	if err := s.db.Create(&gbDevice).Error; err != nil {
		t.Fatalf("create gb device failed: %v", err)
	}

	engine := s.Engine()
	body := []byte(`{"app":"rtp","stream":"` + streamID + `","schema":"rtsp","regist":false}`)
	resp := postJSON(t, engine, "/webhook/on_stream_changed?secret=zlm-hook-secret", body)
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

	var updated model.MediaSource
	if err := s.db.Where("id = ?", source.ID).First(&updated).Error; err != nil {
		t.Fatalf("query updated source failed: %v", err)
	}
	if updated.Status != "offline" {
		t.Fatalf("expected status=offline when regist=false, got=%s", updated.Status)
	}
}

func TestZLMOnStreamChangedGBRegistTrueSetsOnline(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.cfg.Server.ZLM.App = "live"

	deviceID := "34020000001110103919"
	channelID := "34020000001310000009"
	streamID := deviceID + "_" + channelID
	source := model.MediaSource{
		ID:              "gb-stream-ch-regist-true-1",
		Name:            "GB Channel Regist True",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypeGB28181,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolGB28181,
		Transport:       "udp",
		App:             "rtp",
		StreamID:        streamID,
		StreamURL:       "gb28181://" + deviceID + "/" + channelID,
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		EnableRecording: false,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		OutputConfig:    `{"gb_device_id":"` + deviceID + `","zlm_app":"rtp","zlm_stream":"` + streamID + `"}`,
		ExtraJSON:       "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create gb source failed: %v", err)
	}
	gbDevice := model.GBDevice{
		DeviceID:       deviceID,
		SourceIDDevice: "gb-device-row-regist-true-1",
		Name:           "GB Device Regist True",
		AreaID:         model.RootAreaID,
		Enabled:        true,
		Status:         "offline",
		Transport:      "udp",
		Expires:        3600,
	}
	if err := s.db.Create(&gbDevice).Error; err != nil {
		t.Fatalf("create gb device failed: %v", err)
	}

	engine := s.Engine()
	body := []byte(`{"app":"rtp","stream":"` + streamID + `","schema":"rtsp","regist":true}`)
	resp := postJSON(t, engine, "/webhook/on_stream_changed?secret=zlm-hook-secret", body)
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

	var updated model.MediaSource
	if err := s.db.Where("id = ?", source.ID).First(&updated).Error; err != nil {
		t.Fatalf("query updated source failed: %v", err)
	}
	if updated.Status != "online" {
		t.Fatalf("expected status=online when regist=true, got=%s", updated.Status)
	}
}

func TestZLMOnPublishAutoCreateUnknownRTMP(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.cfg.Server.ZLM.App = "live"
	s.cfg.Server.ZLM.PlayHost = "127.0.0.1"
	s.cfg.Server.ZLM.RTMPPort = 11935
	s.cfg.Server.ZLM.RTMPAutoPublishToken = "global-token"

	engine := s.Engine()
	body := []byte(`{"app":"live","stream":"auto-onboard-1","schema":"rtmp","params":"token=global-token","ip":"10.10.10.10"}`)
	resp := postJSON(t, engine, "/webhook/on_publish?secret=zlm-hook-secret", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", resp.Code, resp.Body.String())
	}

	var out zlmHookReply
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("expected allow code=0, got=%+v", out)
	}

	var source model.MediaSource
	if err := s.db.Where("source_type = ? AND protocol = ? AND app = ? AND stream_id = ?",
		model.SourceTypePush, model.ProtocolRTMP, "live", "auto-onboard-1").
		First(&source).Error; err != nil {
		t.Fatalf("query auto source failed: %v", err)
	}
	if source.Status != "offline" {
		t.Fatalf("expected offline source status before on_stream_changed, got=%s", source.Status)
	}
	if source.AreaID != model.RootAreaID {
		t.Fatalf("expected root area, got=%s", source.AreaID)
	}
	if source.RowKind != model.RowKindChannel {
		t.Fatalf("expected row_kind channel, got=%s", source.RowKind)
	}
	if source.StreamURL == "" {
		t.Fatalf("expected stream_url generated")
	}

	var push model.StreamPush
	if err := s.db.Where("source_id = ?", source.ID).First(&push).Error; err != nil {
		t.Fatalf("query stream push failed: %v", err)
	}
	if push.PublishToken != "global-token" {
		t.Fatalf("expected publish token persisted, got=%s", push.PublishToken)
	}
	if push.ClientIP != "10.10.10.10" {
		t.Fatalf("expected client_ip persisted, got=%s", push.ClientIP)
	}
}

func TestZLMOnPublishRejectUnknownRTMPWithoutGlobalTokenMatch(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.cfg.Server.ZLM.App = "live"
	s.cfg.Server.ZLM.RTMPAutoPublishToken = "global-token"

	engine := s.Engine()
	body := []byte(`{"app":"live","stream":"auto-onboard-deny","schema":"rtmp","params":"token=wrong-token"}`)
	resp := postJSON(t, engine, "/webhook/on_publish?secret=zlm-hook-secret", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", resp.Code, resp.Body.String())
	}

	var out zlmHookReply
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if out.Code == 0 {
		t.Fatalf("expected reject code!=0, got=%+v", out)
	}

	var count int64
	if err := s.db.Model(&model.MediaSource{}).Where(
		"source_type = ? AND protocol = ? AND app = ? AND stream_id = ?",
		model.SourceTypePush, model.ProtocolRTMP, "live", "auto-onboard-deny",
	).Count(&count).Error; err != nil {
		t.Fatalf("query source count failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no auto-created source on invalid token, got count=%d", count)
	}
}

func TestZLMOnPublishRejectBlockedStream(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.cfg.Server.ZLM.App = "live"
	s.cfg.Server.ZLM.RTMPAutoPublishToken = "global-token"

	if err := s.db.Create(&model.StreamBlock{
		App:      "live",
		StreamID: "blocked-stream",
		Reason:   "deleted by user",
	}).Error; err != nil {
		t.Fatalf("create stream block failed: %v", err)
	}

	engine := s.Engine()
	body := []byte(`{"app":"live","stream":"blocked-stream","schema":"rtmp","params":"token=global-token"}`)
	resp := postJSON(t, engine, "/webhook/on_publish?secret=zlm-hook-secret", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", resp.Code, resp.Body.String())
	}

	var out zlmHookReply
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if out.Code == 0 {
		t.Fatalf("expected reject code!=0, got=%+v", out)
	}
	if !strings.Contains(strings.ToLower(out.Msg), "blocked") {
		t.Fatalf("expected blocked msg, got=%s", out.Msg)
	}

	var count int64
	if err := s.db.Model(&model.MediaSource{}).Where(
		"source_type = ? AND protocol = ? AND app = ? AND stream_id = ?",
		model.SourceTypePush, model.ProtocolRTMP, "live", "blocked-stream",
	).Count(&count).Error; err != nil {
		t.Fatalf("query source count failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no auto-created source on blocked stream, got count=%d", count)
	}
}

func TestZLMOnStreamNotFoundAcceptsPayloadVariants(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	engine := s.Engine()

	cases := []struct {
		name    string
		payload string
	}{
		{
			name:    "app_stream",
			payload: `{"app":"rtp","stream":"missing-001","schema":"rtsp"}`,
		},
		{
			name:    "app_name_stream_name",
			payload: `{"app_name":"rtp","stream_name":"missing-002","schema":"rtsp"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, engine, "/webhook/on_stream_not_found?secret=zlm-hook-secret", []byte(tc.payload))
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
		})
	}
}

func TestZLMOnServerStartedSetsAllSourceOffline(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.cfg.Server.ZLM.Disabled = true

	items := []model.MediaSource{
		{
			ID:              "src-pull-1",
			Name:            "pull-1",
			AreaID:          model.RootAreaID,
			SourceType:      model.SourceTypePull,
			RowKind:         model.RowKindChannel,
			Protocol:        model.ProtocolRTSP,
			Transport:       "tcp",
			App:             "live",
			StreamID:        "src-pull-1",
			StreamURL:       "rtsp://127.0.0.1/test",
			Status:          "online",
			RecordingStatus: "recording",
			AIStatus:        model.DeviceAIStatusIdle,
			OutputConfig:    "{}",
			ExtraJSON:       "{}",
		},
		{
			ID:              "src-push-1",
			Name:            "push-1",
			AreaID:          model.RootAreaID,
			SourceType:      model.SourceTypePush,
			RowKind:         model.RowKindChannel,
			Protocol:        model.ProtocolRTMP,
			Transport:       "tcp",
			App:             "live",
			StreamID:        "src-push-1",
			StreamURL:       "rtmp://127.0.0.1/live/src-push-1",
			Status:          "online",
			RecordingStatus: "recording",
			AIStatus:        model.DeviceAIStatusIdle,
			OutputConfig:    "{}",
			ExtraJSON:       "{}",
		},
		{
			ID:              "src-gb-1",
			Name:            "gb-1",
			AreaID:          model.RootAreaID,
			SourceType:      model.SourceTypeGB28181,
			RowKind:         model.RowKindChannel,
			Protocol:        model.ProtocolGB28181,
			Transport:       "udp",
			App:             "rtp",
			StreamID:        "34020000001110100001_34020000001310000001",
			StreamURL:       "gb28181://34020000001110100001/34020000001310000001",
			Status:          "online",
			RecordingStatus: "recording",
			AIStatus:        model.DeviceAIStatusIdle,
			OutputConfig:    "{}",
			ExtraJSON:       "{}",
		},
	}
	for i := range items {
		if err := s.db.Create(&items[i]).Error; err != nil {
			t.Fatalf("create source failed: %v", err)
		}
	}

	engine := s.Engine()
	resp := postJSON(t, engine, "/webhook/on_server_started?secret=zlm-hook-secret", []byte(`{}`))
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

	var updated []model.MediaSource
	if err := s.db.Where("id IN ?", []string{"src-pull-1", "src-push-1", "src-gb-1"}).Find(&updated).Error; err != nil {
		t.Fatalf("query sources failed: %v", err)
	}
	if len(updated) != 3 {
		t.Fatalf("expected 3 sources, got %d", len(updated))
	}
	for i := range updated {
		if updated[i].Status != "offline" {
			t.Fatalf("expected offline status after on_server_started, id=%s got=%s", updated[i].ID, updated[i].Status)
		}
		if updated[i].RecordingStatus != "stopped" {
			t.Fatalf("expected stopped recording status after on_server_started, id=%s got=%s", updated[i].ID, updated[i].RecordingStatus)
		}
	}
}

func TestZLMOnServerStartedQueuesGBRecovery(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.gbService = &gb28181.Service{}
	deviceID := "34020000001110108888"
	s.gbInviteRunning = map[string]bool{deviceID: true}
	s.gbInvitePending = map[string]bool{}

	if err := s.db.Create(&model.GBDevice{
		DeviceID: deviceID,
		Name:     "GB-Restart-Recover",
		AreaID:   model.RootAreaID,
		Enabled:  true,
		Status:   "online",
	}).Error; err != nil {
		t.Fatalf("create gb device failed: %v", err)
	}

	engine := s.Engine()
	resp := postJSON(t, engine, "/webhook/on_server_started?secret=zlm-hook-secret", []byte(`{}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", resp.Code, resp.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.gbInviteMu.Lock()
		pending := s.gbInvitePending[deviceID]
		s.gbInviteMu.Unlock()
		if pending {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected gb auto invite pending after on_server_started")
}

func TestRecoverPullSourceAfterZLMRestartKeepsOfflineUntilStreamActive(t *testing.T) {
	s := newFocusedTestServer(t)
	zlmServer, zlmState := newMockSelfHealZLMServer(t)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL
	zlmState.setAutoActive(false)

	source := model.MediaSource{
		ID:            "pull-restart-pending-1",
		Name:          "拉流重启恢复待确认",
		AreaID:        model.RootAreaID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		StreamURL:     "rtsp://192.168.10.41:554/stream0",
		Status:        "offline",
		AIStatus:      model.DeviceAIStatusIdle,
		App:           "live",
		StreamID:      buildZLMStreamID("pull-restart-pending-1"),
		MediaServerID: "local",
		OutputConfig:  "{}",
		ExtraJSON:     "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	proxy := model.StreamProxy{
		SourceID:   source.ID,
		OriginURL:  source.StreamURL,
		Transport:  "tcp",
		Enable:     true,
		RetryCount: 1,
	}
	if err := s.db.Create(&proxy).Error; err != nil {
		t.Fatalf("create proxy failed: %v", err)
	}

	if err := s.recoverSinglePullSourceAfterZLMRestart(source, proxy, proxy.OriginURL, proxy.Transport, "test_recover"); err != nil {
		t.Fatalf("recover single pull source failed: %v", err)
	}

	var updated model.MediaSource
	if err := s.db.Where("id = ?", source.ID).First(&updated).Error; err != nil {
		t.Fatalf("query updated source failed: %v", err)
	}
	if updated.Status != "offline" {
		t.Fatalf("expected source remain offline before active confirm, got=%s", updated.Status)
	}
}

func postJSON(t *testing.T, engine http.Handler, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}
