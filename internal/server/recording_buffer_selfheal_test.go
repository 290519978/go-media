package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"maas-box/internal/model"
)

type mockZLMRecordState struct {
	mu       sync.Mutex
	starts   int
	stops    int
	app      string
	stream   string
	lastPath string
}

func newMockZLMRecordServer(t *testing.T, state *mockZLMRecordState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/index/api/getMediaList":
			state.mu.Lock()
			app := state.app
			stream := state.stream
			state.mu.Unlock()
			items := []map[string]string{{
				"schema": "rtsp",
				"app":    app,
				"stream": stream,
			}}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": items,
			})
		case "/index/api/startRecord":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse startRecord form failed: %v", err)
			}
			state.mu.Lock()
			state.starts++
			state.lastPath = strings.TrimSpace(r.Form.Get("customized_path"))
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{},
			})
		case "/index/api/stopRecord":
			state.mu.Lock()
			state.stops++
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func createAlarmBufferPolicyFixture(t *testing.T, s *Server, source model.MediaSource, taskID string) {
	t.Helper()
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	task := model.VideoTask{
		ID:           taskID,
		Name:         taskID,
		Status:       model.TaskStatusRunning,
		AlarmLevelID: "level-buffer-selfheal",
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	profile := model.VideoTaskDeviceProfile{
		TaskID:           taskID,
		DeviceID:         source.ID,
		RecordingPolicy:  model.RecordingPolicyAlarmClip,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}
	if err := s.db.Create(&profile).Error; err != nil {
		t.Fatalf("create task profile failed: %v", err)
	}
}

func TestApplyRecordingPolicyForBufferedSourceRestartsStaleAlarmBuffer(t *testing.T) {
	s := newFocusedTestServer(t)
	bufferRoot := filepath.Join(t.TempDir(), "alarm-buffer")
	s.cfg.Server.Recording.AlarmClip.BufferDir = bufferRoot
	s.cfg.Server.Recording.AlarmClip.BufferSegmentSeconds = 5

	state := &mockZLMRecordState{app: "live", stream: "device_buffer_restart_1"}
	zlmServer := newMockZLMRecordServer(t, state)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	source := model.MediaSource{
		ID:              "buffer-restart-source-1",
		Name:            "buffer-restart-source-1",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "device_buffer_restart_1",
		StreamURL:       "rtsp://127.0.0.1:554/live/1",
		Status:          "online",
		AIStatus:        model.DeviceAIStatusRunning,
		RecordingStatus: recordingStatusBuffering,
		MediaServerID:   "local",
		OutputConfig:    "{}",
		ExtraJSON:       "{}",
	}
	createAlarmBufferPolicyFixture(t, s, source, "task-buffer-restart-1")

	if err := s.applyRecordingPolicyForSourceID(source.ID); err != nil {
		t.Fatalf("apply recording policy failed: %v", err)
	}

	state.mu.Lock()
	starts := state.starts
	stops := state.stops
	lastPath := state.lastPath
	state.mu.Unlock()
	if starts != 1 {
		t.Fatalf("expected startRecord once, got=%d", starts)
	}
	if stops != 1 {
		t.Fatalf("expected stopRecord once before restart, got=%d", stops)
	}
	if strings.TrimSpace(lastPath) == "" {
		t.Fatalf("expected customized_path to be passed to startRecord")
	}

	var updated model.MediaSource
	if err := s.db.Where("id = ?", source.ID).First(&updated).Error; err != nil {
		t.Fatalf("query updated source failed: %v", err)
	}
	if updated.RecordingStatus != recordingStatusBuffering {
		t.Fatalf("expected recording_status=buffering, got=%s", updated.RecordingStatus)
	}
}

func TestApplyRecordingPolicyForBufferedSourceKeepsHealthyAlarmBuffer(t *testing.T) {
	s := newFocusedTestServer(t)
	bufferRoot := filepath.Join(t.TempDir(), "alarm-buffer")
	s.cfg.Server.Recording.AlarmClip.BufferDir = bufferRoot
	s.cfg.Server.Recording.AlarmClip.BufferSegmentSeconds = 5

	state := &mockZLMRecordState{app: "live", stream: "device_buffer_healthy_1"}
	zlmServer := newMockZLMRecordServer(t, state)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	source := model.MediaSource{
		ID:              "buffer-healthy-source-1",
		Name:            "buffer-healthy-source-1",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "device_buffer_healthy_1",
		StreamURL:       "rtsp://127.0.0.1:554/live/2",
		Status:          "online",
		AIStatus:        model.DeviceAIStatusRunning,
		RecordingStatus: recordingStatusBuffering,
		MediaServerID:   "local",
		OutputConfig:    "{}",
		ExtraJSON:       "{}",
	}
	createAlarmBufferPolicyFixture(t, s, source, "task-buffer-healthy-1")

	dir, err := s.safeAlarmBufferDeviceDir(source.ID)
	if err != nil {
		t.Fatalf("resolve buffer dir failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "live"), 0o755); err != nil {
		t.Fatalf("create buffer dir failed: %v", err)
	}
	filePath := filepath.Join(dir, "live", "segment.mp4")
	if err := os.WriteFile(filePath, []byte("segment"), 0o644); err != nil {
		t.Fatalf("write buffer file failed: %v", err)
	}
	now := time.Now()
	if err := os.Chtimes(filePath, now, now); err != nil {
		t.Fatalf("update buffer file time failed: %v", err)
	}

	if err := s.applyRecordingPolicyForSourceID(source.ID); err != nil {
		t.Fatalf("apply recording policy failed: %v", err)
	}

	state.mu.Lock()
	starts := state.starts
	stops := state.stops
	state.mu.Unlock()
	if starts != 0 || stops != 0 {
		t.Fatalf("expected healthy buffer to avoid restart, starts=%d stops=%d", starts, stops)
	}
}
