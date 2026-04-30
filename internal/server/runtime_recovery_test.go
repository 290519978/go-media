package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"maas-box/internal/ai"
	"maas-box/internal/model"
)

type mockAIService struct {
	mu              sync.Mutex
	running         map[string]bool
	startCalls      []string
	stopCalls       []string
	statusAvailable bool
}

func newMockAIServiceServer(t *testing.T) (*mockAIService, *httptest.Server) {
	t.Helper()
	state := &mockAIService{
		running:         make(map[string]bool),
		statusAvailable: true,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			state.mu.Lock()
			available := state.statusAvailable
			cameras := make([]map[string]any, 0, len(state.running))
			for cameraID, running := range state.running {
				status := "stopped"
				if running {
					status = "running"
				}
				cameras = append(cameras, map[string]any{
					"camera_id":        cameraID,
					"status":           status,
					"frames_processed": 0,
					"retry_count":      0,
					"last_error":       "",
				})
			}
			state.mu.Unlock()
			if !available {
				http.Error(w, "ai warming up", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"is_ready": true,
				"cameras":  cameras,
				"stats":    map[string]any{},
			})
		case "/api/start_camera":
			var req ai.StartCameraRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			state.mu.Lock()
			state.startCalls = append(state.startCalls, req.CameraID)
			state.running[req.CameraID] = true
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success":   true,
				"message":   "ok",
				"camera_id": req.CameraID,
			})
		case "/api/stop_camera":
			var req ai.StopCameraRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			state.mu.Lock()
			state.stopCalls = append(state.stopCalls, req.CameraID)
			delete(state.running, req.CameraID)
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "stopped",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return state, srv
}

func (m *mockAIService) startCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.startCalls)
}

func (m *mockAIService) stopCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.stopCalls)
}

func (m *mockAIService) setStatusAvailable(available bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusAvailable = available
}

func configureMockAI(t *testing.T, s *Server) (*mockAIService, *httptest.Server) {
	t.Helper()
	state, srv := newMockAIServiceServer(t)
	s.cfg.Server.AI.Disabled = false
	s.aiClient = ai.NewClient(srv.URL, 3*time.Second)
	t.Cleanup(srv.Close)
	return state, srv
}

func seedRecoveryTask(t *testing.T, s *Server, device model.Device, taskStatus string) string {
	t.Helper()
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	algorithm := model.Algorithm{
		ID:                "alg-" + device.ID,
		Code:              "ALG_" + strings.ToUpper(strings.ReplaceAll(device.ID, "-", "_")),
		Name:              "Recovery Algorithm",
		Mode:              model.AlgorithmModeSmall,
		DetectMode:        model.AlgorithmDetectModeSmallOnly,
		Enabled:           true,
		SmallModelLabel:   "person",
		YoloThreshold:     0.5,
		IOUThreshold:      0.8,
		LabelsTriggerMode: model.LabelsTriggerModeAny,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	task := model.VideoTask{
		ID:           "task-" + device.ID,
		Name:         "Recovery Task " + device.ID,
		Status:       taskStatus,
		AlarmLevelID: builtinAlarmLevelID1,
		LastStartAt:  time.Now().Add(-time.Minute),
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	profile := model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    5,
		FrameRateMode:    model.FrameRateModeInterval,
		FrameRateValue:   5,
		AlarmLevelID:     builtinAlarmLevelID1,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}
	if err := s.db.Create(&profile).Error; err != nil {
		t.Fatalf("create task profile failed: %v", err)
	}
	rel := model.VideoTaskDeviceAlgorithm{
		TaskID:            task.ID,
		DeviceID:          device.ID,
		AlgorithmID:       algorithm.ID,
		AlarmLevelID:      builtinAlarmLevelID1,
		AlertCycleSeconds: defaultAlertCycleSeconds,
	}
	if err := s.db.Create(&rel).Error; err != nil {
		t.Fatalf("create task algorithm relation failed: %v", err)
	}
	return task.ID
}

func loadTaskAndDevice(t *testing.T, s *Server, taskID, deviceID string) (model.VideoTask, model.Device) {
	t.Helper()
	var task model.VideoTask
	if err := s.db.Where("id = ?", taskID).First(&task).Error; err != nil {
		t.Fatalf("query task failed: %v", err)
	}
	var device model.Device
	if err := s.db.Where("id = ?", deviceID).First(&device).Error; err != nil {
		t.Fatalf("query device failed: %v", err)
	}
	return task, device
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestNormalizeLegacyPullRetryCountsShrinksDefaultRetryToOne(t *testing.T) {
	s := newFocusedTestServer(t)
	proxy := model.StreamProxy{
		SourceID:   "legacy-retry-proxy-1",
		OriginURL:  "rtsp://192.168.10.40:554/stream0",
		Transport:  "tcp",
		Enable:     true,
		RetryCount: 3,
	}
	if err := s.db.Create(&proxy).Error; err != nil {
		t.Fatalf("create stream proxy failed: %v", err)
	}

	if err := s.normalizeLegacyPullRetryCounts(); err != nil {
		t.Fatalf("normalize legacy retry counts failed: %v", err)
	}

	var updated model.StreamProxy
	if err := s.db.Where("source_id = ?", proxy.SourceID).First(&updated).Error; err != nil {
		t.Fatalf("query updated stream proxy failed: %v", err)
	}
	if updated.RetryCount != 1 {
		t.Fatalf("expected retry_count=1 after normalization, got=%d", updated.RetryCount)
	}
}

func TestStopTaskDisablesAutoResumeIntent(t *testing.T) {
	s := newFocusedTestServer(t)
	_, _ = configureMockAI(t, s)
	device := model.Device{
		ID:           "dev-stop-auto-resume",
		Name:         "Stop Auto Resume Device",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypePush,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolRTMP,
		Transport:    "tcp",
		App:          "live",
		StreamID:     "dev-stop-auto-resume",
		StreamURL:    "rtmp://127.0.0.1/live/dev-stop-auto-resume",
		PlayRTSPURL:  "rtsp://127.0.0.1:1554/live/dev-stop-auto-resume",
		Status:       "online",
		AIStatus:     model.DeviceAIStatusRunning,
		OutputConfig: "{}",
	}
	taskID := seedRecoveryTask(t, s, device, model.TaskStatusRunning)
	engine := s.Engine()
	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/stop", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stop task failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := s.getSetting(taskAutoResumeSettingKey(taskID)); got != taskAutoResumeDisabledValue {
		t.Fatalf("expected auto resume disabled after manual stop, got=%q", got)
	}
}

func TestGoStartupRecoveryResumesEnabledTask(t *testing.T) {
	s := newFocusedTestServer(t)
	mockAI, _ := configureMockAI(t, s)
	s.cfg.Server.ZLM.Disabled = true
	device := model.Device{
		ID:           "dev-go-startup-enabled",
		Name:         "Go Startup Enabled Device",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypePush,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolRTMP,
		Transport:    "tcp",
		App:          "live",
		StreamID:     "dev-go-startup-enabled",
		StreamURL:    "rtmp://127.0.0.1/live/dev-go-startup-enabled",
		PlayRTSPURL:  "rtsp://127.0.0.1:1554/live/dev-go-startup-enabled",
		Status:       "online",
		AIStatus:     model.DeviceAIStatusRunning,
		OutputConfig: "{}",
	}
	taskID := seedRecoveryTask(t, s, device, model.TaskStatusRunning)

	s.runZLMRestartRecoveryOnce("go_startup")

	task, updatedDevice := loadTaskAndDevice(t, s, taskID, device.ID)
	if task.Status != model.TaskStatusRunning {
		t.Fatalf("expected task running after startup recovery, got=%s", task.Status)
	}
	if updatedDevice.AIStatus != model.DeviceAIStatusRunning {
		t.Fatalf("expected device ai status running after startup recovery, got=%s", updatedDevice.AIStatus)
	}
	if mockAI.startCallCount() != 1 {
		t.Fatalf("expected one ai start call, got=%d", mockAI.startCallCount())
	}
}

func TestGoStartupRecoveryRecoversPendingTaskOnAIKeepalive(t *testing.T) {
	s := newFocusedTestServer(t)
	mockAI, _ := configureMockAI(t, s)
	mockAI.setStatusAvailable(false)
	s.cfg.Server.ZLM.Disabled = true
	device := model.Device{
		ID:           "dev-go-startup-keepalive",
		Name:         "Go Startup Keepalive Device",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypePush,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolRTMP,
		Transport:    "tcp",
		App:          "live",
		StreamID:     "dev-go-startup-keepalive",
		StreamURL:    "rtmp://127.0.0.1/live/dev-go-startup-keepalive",
		PlayRTSPURL:  "rtsp://127.0.0.1:1554/live/dev-go-startup-keepalive",
		Status:       "online",
		AIStatus:     model.DeviceAIStatusRunning,
		OutputConfig: "{}",
	}
	taskID := seedRecoveryTask(t, s, device, model.TaskStatusRunning)

	s.runZLMRestartRecoveryOnce("go_startup")

	if !s.hasStartupRecoveryPending() {
		t.Fatalf("expected startup recovery pending after ai status failure")
	}
	if pending := s.pendingStartupTaskResumeIDs(); len(pending) != 1 || pending[0] != taskID {
		t.Fatalf("expected pending startup task %s, got=%v", taskID, pending)
	}
	if mockAI.startCallCount() != 0 {
		t.Fatalf("expected no ai start calls before keepalive, got=%d", mockAI.startCallCount())
	}

	mockAI.setStatusAvailable(true)
	engine := s.Engine()
	req := httptest.NewRequest(http.MethodPost, "/ai/keepalive", bytes.NewReader([]byte(`{"stats":{}}`)))
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(s.cfg.Server.AI.CallbackToken); token != "" {
		req.Header.Set("Authorization", token)
	}
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("ai keepalive failed: status=%d body=%s", resp.Code, resp.Body.String())
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return mockAI.startCallCount() == 1 && !s.hasStartupRecoveryPending()
	})

	task, updatedDevice := loadTaskAndDevice(t, s, taskID, device.ID)
	if task.Status != model.TaskStatusRunning {
		t.Fatalf("expected task running after ai keepalive recovery, got=%s", task.Status)
	}
	if updatedDevice.AIStatus != model.DeviceAIStatusRunning {
		t.Fatalf("expected device ai status running after ai keepalive recovery, got=%s", updatedDevice.AIStatus)
	}
}

func TestGoStartupRecoverySkipsDisabledTask(t *testing.T) {
	s := newFocusedTestServer(t)
	mockAI, _ := configureMockAI(t, s)
	s.cfg.Server.ZLM.Disabled = true
	device := model.Device{
		ID:           "dev-go-startup-disabled",
		Name:         "Go Startup Disabled Device",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypePush,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolRTMP,
		Transport:    "tcp",
		App:          "live",
		StreamID:     "dev-go-startup-disabled",
		StreamURL:    "rtmp://127.0.0.1/live/dev-go-startup-disabled",
		PlayRTSPURL:  "rtsp://127.0.0.1:1554/live/dev-go-startup-disabled",
		Status:       "online",
		AIStatus:     model.DeviceAIStatusRunning,
		OutputConfig: "{}",
	}
	taskID := seedRecoveryTask(t, s, device, model.TaskStatusRunning)
	if err := s.setTaskAutoResumeIntent(taskID, false); err != nil {
		t.Fatalf("disable auto resume failed: %v", err)
	}

	s.runZLMRestartRecoveryOnce("go_startup")

	task, updatedDevice := loadTaskAndDevice(t, s, taskID, device.ID)
	if task.Status != model.TaskStatusStopped {
		t.Fatalf("expected task stopped after startup recovery sync, got=%s", task.Status)
	}
	if updatedDevice.AIStatus != model.DeviceAIStatusStopped {
		t.Fatalf("expected device ai status stopped after sync, got=%s", updatedDevice.AIStatus)
	}
	if mockAI.startCallCount() != 0 {
		t.Fatalf("expected no ai start calls for disabled task, got=%d", mockAI.startCallCount())
	}
}

func TestGoStartupRecoveryRetryResumesWhenAIBecomesAvailable(t *testing.T) {
	oldDelays := startupTaskResumeRetryDelays
	oldWindow := startupTaskResumeRetryWindow
	startupTaskResumeRetryDelays = []time.Duration{20 * time.Millisecond, 30 * time.Millisecond}
	startupTaskResumeRetryWindow = 400 * time.Millisecond
	defer func() {
		startupTaskResumeRetryDelays = oldDelays
		startupTaskResumeRetryWindow = oldWindow
	}()

	s := newFocusedTestServer(t)
	mockAI, _ := configureMockAI(t, s)
	mockAI.setStatusAvailable(false)
	s.cfg.Server.ZLM.Disabled = true
	device := model.Device{
		ID:           "dev-go-startup-retry",
		Name:         "Go Startup Retry Device",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypePush,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolRTMP,
		Transport:    "tcp",
		App:          "live",
		StreamID:     "dev-go-startup-retry",
		StreamURL:    "rtmp://127.0.0.1/live/dev-go-startup-retry",
		PlayRTSPURL:  "rtsp://127.0.0.1:1554/live/dev-go-startup-retry",
		Status:       "online",
		AIStatus:     model.DeviceAIStatusRunning,
		OutputConfig: "{}",
	}
	taskID := seedRecoveryTask(t, s, device, model.TaskStatusRunning)

	s.runZLMRestartRecoveryOnce("go_startup")
	time.Sleep(40 * time.Millisecond)
	mockAI.setStatusAvailable(true)

	waitForCondition(t, 2*time.Second, func() bool {
		return mockAI.startCallCount() == 1 && !s.hasStartupRecoveryPending()
	})

	task, updatedDevice := loadTaskAndDevice(t, s, taskID, device.ID)
	if task.Status != model.TaskStatusRunning {
		t.Fatalf("expected task running after startup retry recovery, got=%s", task.Status)
	}
	if updatedDevice.AIStatus != model.DeviceAIStatusRunning {
		t.Fatalf("expected device ai status running after startup retry recovery, got=%s", updatedDevice.AIStatus)
	}
}

func TestGoStartupRecoveryRetrySyncsDisabledTaskWithoutResuming(t *testing.T) {
	oldDelays := startupTaskResumeRetryDelays
	oldWindow := startupTaskResumeRetryWindow
	startupTaskResumeRetryDelays = []time.Duration{20 * time.Millisecond, 30 * time.Millisecond}
	startupTaskResumeRetryWindow = 400 * time.Millisecond
	defer func() {
		startupTaskResumeRetryDelays = oldDelays
		startupTaskResumeRetryWindow = oldWindow
	}()

	s := newFocusedTestServer(t)
	mockAI, _ := configureMockAI(t, s)
	mockAI.setStatusAvailable(false)
	s.cfg.Server.ZLM.Disabled = true
	device := model.Device{
		ID:           "dev-go-startup-disabled-retry",
		Name:         "Go Startup Disabled Retry Device",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypePush,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolRTMP,
		Transport:    "tcp",
		App:          "live",
		StreamID:     "dev-go-startup-disabled-retry",
		StreamURL:    "rtmp://127.0.0.1/live/dev-go-startup-disabled-retry",
		PlayRTSPURL:  "rtsp://127.0.0.1:1554/live/dev-go-startup-disabled-retry",
		Status:       "online",
		AIStatus:     model.DeviceAIStatusRunning,
		OutputConfig: "{}",
	}
	taskID := seedRecoveryTask(t, s, device, model.TaskStatusRunning)
	if err := s.setTaskAutoResumeIntent(taskID, false); err != nil {
		t.Fatalf("disable auto resume failed: %v", err)
	}

	s.runZLMRestartRecoveryOnce("go_startup")
	time.Sleep(40 * time.Millisecond)
	mockAI.setStatusAvailable(true)

	waitForCondition(t, 2*time.Second, func() bool {
		task, updatedDevice := loadTaskAndDevice(t, s, taskID, device.ID)
		return !s.hasStartupRecoveryPending() && task.Status == model.TaskStatusStopped && updatedDevice.AIStatus == model.DeviceAIStatusStopped
	})

	if mockAI.startCallCount() != 0 {
		t.Fatalf("expected no ai start calls for disabled task during retry recovery, got=%d", mockAI.startCallCount())
	}
}

func TestGoStartupRecoveryResumesPendingGBTaskOnStreamChanged(t *testing.T) {
	s := newFocusedTestServer(t)
	mockAI, _ := configureMockAI(t, s)
	s.cfg.Server.ZLM.Secret = "zlm-hook-secret"
	s.cfg.Server.ZLM.Disabled = true
	deviceID := "34020000001110109999"
	channelID := "34020000001310009999"
	streamID := deviceID + "_" + channelID
	device := model.Device{
		ID:           "gb-recovery-source-1",
		Name:         "GB Recovery Channel",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypeGB28181,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolGB28181,
		Transport:    "udp",
		App:          "rtp",
		StreamID:     streamID,
		StreamURL:    "gb28181://" + deviceID + "/" + channelID,
		PlayRTSPURL:  "rtsp://127.0.0.1:1554/rtp/" + streamID,
		Status:       "online",
		AIStatus:     model.DeviceAIStatusRunning,
		OutputConfig: `{"zlm_app":"rtp","zlm_stream":"` + streamID + `"}`,
	}
	taskID := seedRecoveryTask(t, s, device, model.TaskStatusRunning)

	s.runZLMRestartRecoveryOnce("go_startup")

	task, updatedDevice := loadTaskAndDevice(t, s, taskID, device.ID)
	if task.Status != model.TaskStatusStopped {
		t.Fatalf("expected task stopped after initial go startup sync, got=%s", task.Status)
	}
	if updatedDevice.Status != "offline" {
		t.Fatalf("expected gb source offline after go startup recovery, got=%s", updatedDevice.Status)
	}
	if mockAI.startCallCount() != 0 {
		t.Fatalf("expected no ai start before stream comes back, got=%d", mockAI.startCallCount())
	}
	if pendingTaskID := s.pendingStartupResumeTaskID(device.ID); pendingTaskID != taskID {
		t.Fatalf("expected pending startup resume task=%s, got=%s", taskID, pendingTaskID)
	}

	engine := s.Engine()
	resp := postJSON(t, engine, "/webhook/on_stream_changed?secret=zlm-hook-secret", []byte(`{"app":"rtp","stream":"`+streamID+`","schema":"rtsp","regist":true}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("stream changed failed: status=%d body=%s", resp.Code, resp.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mockAI.startCallCount() == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if mockAI.startCallCount() != 1 {
		t.Fatalf("expected one ai start after gb stream online, got=%d", mockAI.startCallCount())
	}
	task, updatedDevice = loadTaskAndDevice(t, s, taskID, device.ID)
	if task.Status != model.TaskStatusRunning {
		t.Fatalf("expected task running after gb stream online, got=%s", task.Status)
	}
	if updatedDevice.AIStatus != model.DeviceAIStatusRunning {
		t.Fatalf("expected gb device ai status running after resume, got=%s", updatedDevice.AIStatus)
	}
	if s.pendingStartupResumeTaskID(device.ID) != "" {
		t.Fatalf("expected pending startup resume cleared after successful gb resume")
	}
}

func TestFetchAIRunningDevicesHelper(t *testing.T) {
	s := newFocusedTestServer(t)
	mockAI, srv := configureMockAI(t, s)
	mockAI.mu.Lock()
	mockAI.running["camera-1"] = true
	mockAI.running["camera-2"] = false
	mockAI.mu.Unlock()
	s.aiClient = ai.NewClient(srv.URL, 2*time.Second)

	_, running, err := s.fetchAIRunningDevices(context.Background())
	if err != nil {
		t.Fatalf("fetch ai running devices failed: %v", err)
	}
	if _, ok := running["camera-1"]; !ok {
		t.Fatalf("expected running camera-1 in running set")
	}
	if _, ok := running["camera-2"]; ok {
		t.Fatalf("did not expect stopped camera-2 in running set")
	}
}

func TestLLMTokenQuotaStopsRunningTaskAndRequiresManualRestart(t *testing.T) {
	s := newFocusedTestServer(t)
	mockAI, _ := configureMockAI(t, s)
	s.cfg.Server.AI.DisableOnTokenLimitExceeded = true
	s.cfg.Server.AI.TotalTokenLimit = 100

	device := model.Device{
		ID:           "dev-llm-token-limit-stop",
		Name:         "LLM Token Limit Stop Device",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypePush,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolRTMP,
		Transport:    "tcp",
		App:          "live",
		StreamID:     "dev-llm-token-limit-stop",
		StreamURL:    "rtmp://127.0.0.1/live/dev-llm-token-limit-stop",
		PlayRTSPURL:  "rtsp://127.0.0.1:1554/live/dev-llm-token-limit-stop",
		Status:       "online",
		AIStatus:     model.DeviceAIStatusRunning,
		OutputConfig: "{}",
	}
	taskID := seedRecoveryTask(t, s, device, model.TaskStatusRunning)

	mockAI.mu.Lock()
	mockAI.running[device.ID] = true
	mockAI.mu.Unlock()

	totalTokens := 100
	created, err := s.recordLLMUsage(s.db, llmUsagePersistRequest{
		Source:       model.LLMUsageSourceTaskRuntime,
		TaskID:       taskID,
		DeviceID:     device.ID,
		ProviderID:   "provider-test",
		ProviderName: "provider-test",
		Model:        "model-test",
		DetectMode:   model.AlgorithmDetectModeLLMOnly,
		OccurredAt:   time.Now(),
		Usage: &ai.LLMUsage{
			CallID:         "quota-stop-call",
			CallStatus:     model.LLMUsageStatusSuccess,
			UsageAvailable: true,
			TotalTokens:    &totalTokens,
		},
	})
	if err != nil {
		t.Fatalf("record llm usage failed: %v", err)
	}
	if !created {
		t.Fatalf("expected llm usage row created")
	}

	waitForCondition(t, 2*time.Second, func() bool {
		task, updatedDevice := loadTaskAndDevice(t, s, taskID, device.ID)
		return mockAI.stopCallCount() == 1 &&
			task.Status == model.TaskStatusStopped &&
			updatedDevice.AIStatus == model.DeviceAIStatusStopped
	})
	if got := s.getSetting(taskAutoResumeSettingKey(taskID)); got != taskAutoResumeDisabledValue {
		t.Fatalf("expected auto resume disabled after quota stop, got=%q", got)
	}

	s.cfg.Server.AI.TotalTokenLimit = 1000
	s.resumeAutoResumeTasks(context.Background(), []string{taskID}, "after_quota_lift")
	if mockAI.startCallCount() != 0 {
		t.Fatalf("expected no auto resume after quota lifted, got=%d", mockAI.startCallCount())
	}
}

func TestLLMTokenQuotaBlocksTaskStartIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	mockAI, _ := configureMockAI(t, s)
	s.cfg.Server.AI.DisableOnTokenLimitExceeded = true
	s.cfg.Server.AI.TotalTokenLimit = 100

	device := model.Device{
		ID:           "dev-llm-token-limit-start",
		Name:         "LLM Token Limit Start Device",
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypePush,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolRTMP,
		Transport:    "tcp",
		App:          "live",
		StreamID:     "dev-llm-token-limit-start",
		StreamURL:    "rtmp://127.0.0.1/live/dev-llm-token-limit-start",
		PlayRTSPURL:  "rtsp://127.0.0.1:1554/live/dev-llm-token-limit-start",
		Status:       "online",
		AIStatus:     model.DeviceAIStatusStopped,
		OutputConfig: "{}",
	}
	taskID := seedRecoveryTask(t, s, device, model.TaskStatusStopped)

	tokens := 100
	if err := s.db.Create(&model.LLMUsageCall{
		ID:             "quota-start-limit-call",
		Source:         model.LLMUsageSourceTaskRuntime,
		TaskID:         taskID,
		DeviceID:       device.ID,
		CallStatus:     model.LLMUsageStatusSuccess,
		UsageAvailable: true,
		TotalTokens:    &tokens,
		OccurredAt:     time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed llm usage failed: %v", err)
	}

	engine := s.Engine()
	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when task start blocked by quota: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), llmTokenLimitExceededMessage) {
		t.Fatalf("expected quota message, got=%s", rec.Body.String())
	}
	if mockAI.startCallCount() != 0 {
		t.Fatalf("expected no ai start call when quota blocks task start, got=%d", mockAI.startCallCount())
	}
}
