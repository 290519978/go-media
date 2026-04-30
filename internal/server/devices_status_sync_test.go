package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"maas-box/internal/model"
)

type mockZLMActiveStream struct {
	mu     sync.Mutex
	app    string
	stream string
}

func newMockZLMActiveStreamServer(t *testing.T) (*httptest.Server, *mockZLMActiveStream) {
	t.Helper()
	state := &mockZLMActiveStream{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/index/api/addStreamProxy":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse addStreamProxy form failed: %v", err)
			}
			state.mu.Lock()
			state.app = strings.TrimSpace(r.Form.Get("app"))
			state.stream = strings.TrimSpace(r.Form.Get("stream"))
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{},
			})
		case "/index/api/getMediaList":
			state.mu.Lock()
			app := state.app
			stream := state.stream
			state.mu.Unlock()
			items := make([]map[string]string, 0, 1)
			if stream != "" {
				items = append(items, map[string]string{
					"schema": "rtsp",
					"app":    app,
					"stream": stream,
				})
			}
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

func TestCreatePullDeviceSetsOnlineWhenZLMStreamAlreadyActive(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	zlmServer, _ := newMockZLMActiveStreamServer(t)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	token := loginToken(t, engine, "admin", "admin")
	body := []byte(`{"name":"拉流设备-创建","area_id":"root","source_type":"pull","transport":"tcp","origin_url":"rtsp://192.168.10.20:554/stream0"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create device failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected create response: %s", rec.Body.String())
	}
	if resp.Data.Status != "online" {
		t.Fatalf("expected create response status=online, got=%s", resp.Data.Status)
	}

	var source model.MediaSource
	if err := s.db.Where("id = ?", resp.Data.ID).First(&source).Error; err != nil {
		t.Fatalf("query created source failed: %v", err)
	}
	if source.Status != "online" {
		t.Fatalf("expected created source status=online, got=%s", source.Status)
	}
}

func TestUpdatePullDeviceSetsOnlineWhenZLMStreamAlreadyActive(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	zlmServer, _ := newMockZLMActiveStreamServer(t)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	sourceID := "pull-update-online-1"
	source := model.MediaSource{
		ID:            sourceID,
		Name:          "拉流设备-编辑",
		AreaID:        model.RootAreaID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		StreamURL:     "rtsp://192.168.10.21:554/old",
		Status:        "offline",
		AIStatus:      model.DeviceAIStatusIdle,
		App:           "live",
		StreamID:      buildZLMStreamID(sourceID),
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
		RetryCount: 3,
	}).Error; err != nil {
		t.Fatalf("create stream proxy failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	body := []byte(`{"name":"拉流设备-编辑后","area_id":"root","transport":"tcp","origin_url":"rtsp://192.168.10.21:554/new"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/devices/"+sourceID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update device failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode update response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected update response: %s", rec.Body.String())
	}
	if resp.Data.Status != "online" {
		t.Fatalf("expected update response status=online, got=%s", resp.Data.Status)
	}

	var updated model.MediaSource
	if err := s.db.Where("id = ?", sourceID).First(&updated).Error; err != nil {
		t.Fatalf("query updated source failed: %v", err)
	}
	if updated.Status != "online" {
		t.Fatalf("expected updated source status=online, got=%s", updated.Status)
	}
}

func TestPreviewPullDeviceSetsOnlineWhenZLMStreamAlreadyActive(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	zlmServer, _ := newMockZLMActiveStreamServer(t)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	sourceID := "pull-preview-online-1"
	source := model.MediaSource{
		ID:            sourceID,
		Name:          "拉流设备-预览",
		AreaID:        model.RootAreaID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		StreamURL:     "rtsp://192.168.10.22:554/stream0",
		Status:        "offline",
		AIStatus:      model.DeviceAIStatusIdle,
		App:           "live",
		StreamID:      buildZLMStreamID(sourceID),
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
		RetryCount: 3,
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

	var resp struct {
		Code int `json:"code"`
		Data struct {
			PlayURL string `json:"play_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode preview response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected preview response: %s", rec.Body.String())
	}
	if strings.TrimSpace(resp.Data.PlayURL) == "" {
		t.Fatalf("expected preview play_url")
	}

	var updated model.MediaSource
	if err := s.db.Where("id = ?", sourceID).First(&updated).Error; err != nil {
		t.Fatalf("query preview source failed: %v", err)
	}
	if updated.Status != "online" {
		t.Fatalf("expected previewed source status=online, got=%s", updated.Status)
	}
}
