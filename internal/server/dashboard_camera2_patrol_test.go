package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maas-box/internal/ai"
	"maas-box/internal/model"
)

func TestCamera2PatrolJobPromptCreatesPatrolEventIntegration(t *testing.T) {
	root := t.TempDir()
	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		engine := s.Engine()

		mockZLM := newPatrolSnapshotServer(t, buildPatrolTestPNG(t))
		defer mockZLM.Close()
		s.cfg.Server.ZLM.APIURL = mockZLM.URL

		recorder := &patrolAIRecorder{}
		mockAI := newPatrolAIServer(t, recorder, func(req ai.AnalyzeImageRequest) ai.AnalyzeImageResponse {
			taskCode := strings.TrimSpace(req.AlgorithmConfigs[0].TaskCode)
			algorithmID := strings.TrimSpace(req.AlgorithmConfigs[0].AlgorithmID)
			totalTokens := 123
			return ai.AnalyzeImageResponse{
				Success:          true,
				Message:          "ok",
				AlgorithmResults: []byte(fmt.Sprintf(`[{"algorithm_id":"%s","task_code":"%s","alarm":1,"reason":"发现人员"}]`, algorithmID, taskCode)),
				LLMResult: fmt.Sprintf(`{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["%s"]},"task_results":[{"task_code":"%s","task_name":"任务巡查","alarm":"1","reason":"发现人员","object_ids":["OBJ001"]}],"objects":[{"object_id":"OBJ001","task_code":"%s","bbox2d":[10,20,80,120],"label":"person","confidence":0.93}]}`,
					taskCode,
					taskCode,
					taskCode,
				),
				LLMUsage: &ai.LLMUsage{
					CallID:         "patrol-call-custom-hit",
					CallStatus:     model.LLMUsageStatusSuccess,
					UsageAvailable: true,
					TotalTokens:    &totalTokens,
					Model:          "config-test-model",
				},
			}
		})
		defer mockAI.Close()
		s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

		device := createPatrolTestDevice(t, s, "dev-patrol-custom-hit", "北门入口摄像头")
		token := loginToken(t, engine, "admin", "admin")
		promptText := "检查画面里是否有人跌倒"

		jobID := createPatrolJobViaAPI(t, engine, token, map[string]any{
			"device_ids": []string{device.ID},
			"prompt":     promptText,
		})
		snapshot := waitPatrolJobFinished(t, engine, token, jobID)

		if snapshot.Status != model.AlgorithmTestJobStatusCompleted {
			t.Fatalf("expected completed patrol job, got %+v", snapshot)
		}
		if snapshot.TotalCount != 1 || snapshot.SuccessCount != 1 || snapshot.FailedCount != 0 || snapshot.AlarmCount != 1 {
			t.Fatalf("unexpected patrol snapshot: %+v", snapshot)
		}
		if len(snapshot.Items) != 1 || strings.TrimSpace(snapshot.Items[0].EventID) == "" {
			t.Fatalf("expected patrol item to create event, got %+v", snapshot.Items)
		}

		lastReq := recorder.Last()
		if !strings.Contains(lastReq.LLMPrompt, promptText) {
			t.Fatalf("expected llm prompt contains custom patrol prompt, got=%s", lastReq.LLMPrompt)
		}
		if len(lastReq.AlgorithmConfigs) != 1 || lastReq.AlgorithmConfigs[0].DetectMode != model.AlgorithmDetectModeLLMOnly {
			t.Fatalf("expected llm-only patrol analyze request, got %+v", lastReq.AlgorithmConfigs)
		}

		var event model.AlarmEvent
		if err := s.db.Where("id = ?", snapshot.Items[0].EventID).First(&event).Error; err != nil {
			t.Fatalf("load patrol event failed: %v", err)
		}
		if event.EventSource != model.AlarmEventSourcePatrol {
			t.Fatalf("expected patrol event source, got=%s", event.EventSource)
		}
		if event.DisplayName != camera2PatrolDisplayName {
			t.Fatalf("expected patrol display name=%s, got=%s", camera2PatrolDisplayName, event.DisplayName)
		}
		if event.PromptText != promptText {
			t.Fatalf("expected prompt_text=%q, got=%q", promptText, event.PromptText)
		}
		if strings.TrimSpace(event.AlgorithmID) != "" {
			t.Fatalf("expected custom patrol algorithm_id empty, got=%s", event.AlgorithmID)
		}
		if strings.TrimSpace(event.SnapshotPath) == "" || event.NotifiedAt == nil {
			t.Fatalf("expected snapshot_path and notified_at written, got %+v", event)
		}

		var usage model.LLMUsageCall
		if err := s.db.Where("id = ?", "patrol-call-custom-hit").First(&usage).Error; err != nil {
			t.Fatalf("load patrol llm usage failed: %v", err)
		}
		if usage.Source != model.LLMUsageSourceDirectAnalyze {
			t.Fatalf("expected direct_analyze usage source, got=%s", usage.Source)
		}
		if strings.TrimSpace(usage.DeviceID) != device.ID {
			t.Fatalf("expected usage device_id=%s, got=%s", device.ID, usage.DeviceID)
		}
	})
}

func TestCamera2PatrolJobAlgorithmUsesActivePromptIntegration(t *testing.T) {
	root := t.TempDir()
	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		engine := s.Engine()

		mockZLM := newPatrolSnapshotServer(t, buildPatrolTestPNG(t))
		defer mockZLM.Close()
		s.cfg.Server.ZLM.APIURL = mockZLM.URL

		activePrompt := "检查是否有人翻越围栏"
		recorder := &patrolAIRecorder{}
		mockAI := newPatrolAIServer(t, recorder, func(req ai.AnalyzeImageRequest) ai.AnalyzeImageResponse {
			taskCode := strings.TrimSpace(req.AlgorithmConfigs[0].TaskCode)
			algorithmID := strings.TrimSpace(req.AlgorithmConfigs[0].AlgorithmID)
			return ai.AnalyzeImageResponse{
				Success:          true,
				Message:          "ok",
				AlgorithmResults: []byte(fmt.Sprintf(`[{"algorithm_id":"%s","task_code":"%s","alarm":1,"reason":"围栏异常"}]`, algorithmID, taskCode)),
				LLMResult: fmt.Sprintf(`{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["%s"]},"task_results":[{"task_code":"%s","task_name":"人员翻越围栏","alarm":"1","reason":"围栏异常","object_ids":["OBJ001"]}],"objects":[{"object_id":"OBJ001","task_code":"%s","bbox2d":[30,40,150,220],"label":"person","confidence":0.91}]}`,
					taskCode,
					taskCode,
					taskCode,
				),
			}
		})
		defer mockAI.Close()
		s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

		algorithm := model.Algorithm{
			ID:              "alg-patrol-active-prompt",
			Code:            "ALG_PATROL_ACTIVE_PROMPT",
			Name:            "人员翻越围栏",
			Mode:            model.AlgorithmModeLarge,
			DetectMode:      model.AlgorithmDetectModeLLMOnly,
			Enabled:         true,
			ModelProviderID: "provider-not-required-in-db-fixture",
		}
		if err := s.db.Create(&algorithm).Error; err != nil {
			t.Fatalf("create patrol algorithm failed: %v", err)
		}
		if err := s.db.Create(&model.AlgorithmPromptVersion{
			ID:          "prompt-patrol-active",
			AlgorithmID: algorithm.ID,
			Version:     "v1",
			Prompt:      activePrompt,
			IsActive:    true,
		}).Error; err != nil {
			t.Fatalf("create patrol active prompt failed: %v", err)
		}

		device := createPatrolTestDevice(t, s, "dev-patrol-alg-hit", "仓库通道摄像头")
		token := loginToken(t, engine, "admin", "admin")

		jobID := createPatrolJobViaAPI(t, engine, token, map[string]any{
			"device_ids":   []string{device.ID},
			"algorithm_id": algorithm.ID,
		})
		snapshot := waitPatrolJobFinished(t, engine, token, jobID)
		if snapshot.Status != model.AlgorithmTestJobStatusCompleted || snapshot.AlarmCount != 1 {
			t.Fatalf("unexpected algorithm patrol snapshot: %+v", snapshot)
		}

		lastReq := recorder.Last()
		if !strings.Contains(lastReq.LLMPrompt, activePrompt) {
			t.Fatalf("expected llm prompt contains active algorithm prompt, got=%s", lastReq.LLMPrompt)
		}

		var event model.AlarmEvent
		if err := s.db.Where("id = ?", snapshot.Items[0].EventID).First(&event).Error; err != nil {
			t.Fatalf("load algorithm patrol event failed: %v", err)
		}
		if event.EventSource != model.AlarmEventSourcePatrol {
			t.Fatalf("expected patrol event source, got=%s", event.EventSource)
		}
		if event.DisplayName != algorithm.Name {
			t.Fatalf("expected algorithm display name=%s, got=%s", algorithm.Name, event.DisplayName)
		}
		if event.PromptText != activePrompt {
			t.Fatalf("expected prompt_text=%q, got=%q", activePrompt, event.PromptText)
		}
		if event.AlgorithmID != algorithm.ID {
			t.Fatalf("expected algorithm_id=%s, got=%s", algorithm.ID, event.AlgorithmID)
		}
	})
}

func TestCamera2PatrolJobValidationAndMissIntegration(t *testing.T) {
	root := t.TempDir()
	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		engine := s.Engine()

		mockZLM := newPatrolSnapshotServer(t, buildPatrolTestPNG(t))
		defer mockZLM.Close()
		s.cfg.Server.ZLM.APIURL = mockZLM.URL

		mockAI := newPatrolAIServer(t, nil, func(req ai.AnalyzeImageRequest) ai.AnalyzeImageResponse {
			taskCode := strings.TrimSpace(req.AlgorithmConfigs[0].TaskCode)
			totalTokens := 45
			return ai.AnalyzeImageResponse{
				Success: true,
				Message: "ok",
				LLMResult: fmt.Sprintf(`{"version":"1.0","overall":{"alarm":"0","alarm_task_codes":[]},"task_results":[{"task_code":"%s","task_name":"任务巡查","alarm":"0","reason":"未发现异常","object_ids":[]}],"objects":[]}`,
					taskCode,
				),
				LLMUsage: &ai.LLMUsage{
					CallID:         "patrol-call-miss",
					CallStatus:     model.LLMUsageStatusSuccess,
					UsageAvailable: true,
					TotalTokens:    &totalTokens,
					Model:          "config-test-model",
				},
			}
		})
		defer mockAI.Close()
		s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

		token := loginToken(t, engine, "admin", "admin")

		emptyRec := performAuthedJSONRequest(t, engine, token, http.MethodPost, "/api/v1/dashboard/camera2/patrol-jobs", map[string]any{
			"device_ids": []string{},
			"prompt":     "检查空请求",
		})
		if emptyRec.Code != http.StatusBadRequest || !strings.Contains(emptyRec.Body.String(), "设备") {
			t.Fatalf("expected empty device list rejected, status=%d body=%s", emptyRec.Code, emptyRec.Body.String())
		}

		algorithm := model.Algorithm{
			ID:              "alg-patrol-no-active-prompt",
			Code:            "ALG_PATROL_NO_ACTIVE_PROMPT",
			Name:            "未配置提示词算法",
			Mode:            model.AlgorithmModeLarge,
			DetectMode:      model.AlgorithmDetectModeLLMOnly,
			Enabled:         true,
			ModelProviderID: "provider-not-required-in-db-fixture",
		}
		if err := s.db.Create(&algorithm).Error; err != nil {
			t.Fatalf("create no-prompt algorithm failed: %v", err)
		}
		noPromptRec := performAuthedJSONRequest(t, engine, token, http.MethodPost, "/api/v1/dashboard/camera2/patrol-jobs", map[string]any{
			"device_ids":   []string{"dev-missing-for-no-prompt"},
			"algorithm_id": algorithm.ID,
		})
		if noPromptRec.Code != http.StatusBadRequest || !strings.Contains(noPromptRec.Body.String(), "启用中的提示词") {
			t.Fatalf("expected missing active prompt rejected, status=%d body=%s", noPromptRec.Code, noPromptRec.Body.String())
		}

		missingJobID := createPatrolJobViaAPI(t, engine, token, map[string]any{
			"device_ids": []string{"dev-patrol-missing"},
			"prompt":     "检查离线设备",
		})
		missingSnapshot := waitPatrolJobFinished(t, engine, token, missingJobID)
		if missingSnapshot.Status != model.AlgorithmTestJobStatusFailed {
			t.Fatalf("expected failed patrol job for missing device, got %+v", missingSnapshot)
		}
		if missingSnapshot.SuccessCount != 0 || missingSnapshot.FailedCount != 1 || missingSnapshot.AlarmCount != 0 {
			t.Fatalf("unexpected missing-device snapshot: %+v", missingSnapshot)
		}
		if len(missingSnapshot.Items) != 1 || !strings.Contains(missingSnapshot.Items[0].Message, "设备不存在") {
			t.Fatalf("expected missing device failure item, got %+v", missingSnapshot.Items)
		}

		device := createPatrolTestDevice(t, s, "dev-patrol-miss", "办公区摄像头")
		missJobID := createPatrolJobViaAPI(t, engine, token, map[string]any{
			"device_ids": []string{device.ID},
			"prompt":     "检查画面里是否有人打电话",
		})
		missSnapshot := waitPatrolJobFinished(t, engine, token, missJobID)
		if missSnapshot.Status != model.AlgorithmTestJobStatusCompleted {
			t.Fatalf("expected completed miss patrol job, got %+v", missSnapshot)
		}
		if missSnapshot.SuccessCount != 1 || missSnapshot.FailedCount != 0 || missSnapshot.AlarmCount != 0 {
			t.Fatalf("unexpected miss snapshot: %+v", missSnapshot)
		}
		if len(missSnapshot.Items) != 1 || strings.TrimSpace(missSnapshot.Items[0].EventID) != "" {
			t.Fatalf("expected miss patrol item without event, got %+v", missSnapshot.Items)
		}

		var eventCount int64
		if err := s.db.Model(&model.AlarmEvent{}).Where("device_id = ? AND event_source = ?", device.ID, model.AlarmEventSourcePatrol).Count(&eventCount).Error; err != nil {
			t.Fatalf("count patrol events failed: %v", err)
		}
		if eventCount != 0 {
			t.Fatalf("expected no patrol event created on miss, got=%d", eventCount)
		}
	})
}

func TestCamera2PatrolJobRetriesRecoverableImageFailures(t *testing.T) {
	root := t.TempDir()
	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		s.cfg.Server.AI.AnalyzeImageFailureRetryCount = 2
		engine := s.Engine()

		mockZLM := newPatrolSnapshotServer(t, buildPatrolTestPNG(t))
		defer mockZLM.Close()
		s.cfg.Server.ZLM.APIURL = mockZLM.URL

		var attempts int32
		mockAI := newPatrolAIServer(t, nil, func(req ai.AnalyzeImageRequest) ai.AnalyzeImageResponse {
			attempt := atomic.AddInt32(&attempts, 1)
			taskCode := strings.TrimSpace(req.AlgorithmConfigs[0].TaskCode)
			if attempt < 3 {
				return ai.AnalyzeImageResponse{
					Success: true,
					Message: "ok",
					LLMUsage: &ai.LLMUsage{
						CallStatus:   model.LLMUsageStatusError,
						ErrorMessage: "Connection error.",
					},
				}
			}
			return ai.AnalyzeImageResponse{
				Success: true,
				Message: "ok",
				LLMResult: fmt.Sprintf(`{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["%s"]},"task_results":[{"task_code":"%s","task_name":"任务巡查","alarm":"1","reason":"发现人员","object_ids":["OBJ001"]}],"objects":[{"object_id":"OBJ001","task_code":"%s","bbox2d":[10,20,80,120],"label":"person","confidence":0.93}]}`,
					taskCode,
					taskCode,
					taskCode,
				),
				LLMUsage: &ai.LLMUsage{
					CallStatus: model.LLMUsageStatusSuccess,
				},
			}
		})
		defer mockAI.Close()
		s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

		device := createPatrolTestDevice(t, s, "dev-patrol-retry-hit", "巡查补跑摄像头")
		token := loginToken(t, engine, "admin", "admin")
		jobID := createPatrolJobViaAPI(t, engine, token, map[string]any{
			"device_ids": []string{device.ID},
			"prompt":     "检查画面里是否有人停留",
		})
		snapshot := waitPatrolJobFinished(t, engine, token, jobID)

		if snapshot.Status != model.AlgorithmTestJobStatusCompleted {
			t.Fatalf("expected completed patrol retry job, got %+v", snapshot)
		}
		if atomic.LoadInt32(&attempts) != 3 {
			t.Fatalf("expected patrol image to run 3 times, got %d", attempts)
		}
		if snapshot.SuccessCount != 1 || snapshot.FailedCount != 0 || snapshot.AlarmCount != 1 {
			t.Fatalf("unexpected patrol retry snapshot: %+v", snapshot)
		}
		if len(snapshot.Items) != 1 || strings.TrimSpace(snapshot.Items[0].EventID) == "" {
			t.Fatalf("expected patrol retry item to create a single event, got %+v", snapshot.Items)
		}

		var eventCount int64
		if err := s.db.Model(&model.AlarmEvent{}).Where("device_id = ? AND event_source = ?", device.ID, model.AlarmEventSourcePatrol).Count(&eventCount).Error; err != nil {
			t.Fatalf("count patrol retry events failed: %v", err)
		}
		if eventCount != 1 {
			t.Fatalf("expected single patrol event after retry success, got=%d", eventCount)
		}
	})
}

func TestCamera2PatrolJobDoesNotRetryNonRecoverableFailures(t *testing.T) {
	root := t.TempDir()
	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		s.cfg.Server.AI.AnalyzeImageFailureRetryCount = 2
		engine := s.Engine()

		mockZLM := newPatrolSnapshotServer(t, buildPatrolTestPNG(t))
		defer mockZLM.Close()
		s.cfg.Server.ZLM.APIURL = mockZLM.URL

		var attempts int32
		mockAI := newPatrolAIServer(t, nil, func(req ai.AnalyzeImageRequest) ai.AnalyzeImageResponse {
			atomic.AddInt32(&attempts, 1)
			return ai.AnalyzeImageResponse{
				Success: true,
				Message: "ok",
				LLMUsage: &ai.LLMUsage{
					CallStatus: model.LLMUsageStatusEmptyContent,
				},
			}
		})
		defer mockAI.Close()
		s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

		device := createPatrolTestDevice(t, s, "dev-patrol-no-retry", "巡查非补跑摄像头")
		token := loginToken(t, engine, "admin", "admin")
		jobID := createPatrolJobViaAPI(t, engine, token, map[string]any{
			"device_ids": []string{device.ID},
			"prompt":     "检查是否存在异常",
		})
		snapshot := waitPatrolJobFinished(t, engine, token, jobID)

		if snapshot.Status != model.AlgorithmTestJobStatusFailed {
			t.Fatalf("expected failed non-retryable patrol job, got %+v", snapshot)
		}
		if atomic.LoadInt32(&attempts) != 1 {
			t.Fatalf("expected non-retryable patrol failure to run once, got %d", attempts)
		}
		if snapshot.SuccessCount != 0 || snapshot.FailedCount != 1 || snapshot.AlarmCount != 0 {
			t.Fatalf("unexpected non-retryable patrol snapshot: %+v", snapshot)
		}
	})
}

func TestEventsSourceFilterAndPatrolDetailIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	area := model.Area{ID: "area-event-source", Name: "巡查区域", ParentID: model.RootAreaID, Sort: 1}
	if err := s.db.Create(&area).Error; err != nil {
		t.Fatalf("create area failed: %v", err)
	}
	device := model.Device{
		ID:            "dev-event-source",
		Name:          "巡查摄像头",
		AreaID:        area.ID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		App:           "live",
		StreamID:      "event_source",
		StreamURL:     "rtsp://127.0.0.1/live/event_source",
		Status:        "online",
		AIStatus:      model.DeviceAIStatusIdle,
		OutputConfig:  "{}",
		PlayWebRTCURL: "http://127.0.0.1/index/api/webrtc?app=live&stream=event_source&type=play",
		PlayWSFLVURL:  "ws://127.0.0.1/live/event_source.live.flv",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	algorithm := model.Algorithm{
		ID:              "alg-event-source-runtime",
		Code:            "ALG_EVENT_SOURCE_RUNTIME",
		Name:            "实时算法",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
		ModelProviderID: "provider-not-required-in-db-fixture",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	level := loadAlarmLevelBySeverity(t, s, 1)
	task := model.VideoTask{
		ID:              "task-event-source-runtime",
		Name:            "实时巡查任务",
		Status:          model.TaskStatusRunning,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:            task.ID,
		DeviceID:          device.ID,
		AlgorithmID:       algorithm.ID,
		AlarmLevelID:      level.ID,
		AlertCycleSeconds: 60,
	}).Error; err != nil {
		t.Fatalf("create runtime relation failed: %v", err)
	}

	now := time.Now()
	runtimeEvent := model.AlarmEvent{
		ID:             "event-source-runtime",
		TaskID:         task.ID,
		DeviceID:       device.ID,
		AlgorithmID:    algorithm.ID,
		EventSource:    model.AlarmEventSourceRuntime,
		AlarmLevelID:   level.ID,
		Status:         model.EventStatusPending,
		OccurredAt:     now.Add(-2 * time.Minute),
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        "{}",
		SourceCallback: "{}",
	}
	patrolEvent := model.AlarmEvent{
		ID:             "event-source-patrol",
		TaskID:         "",
		DeviceID:       device.ID,
		AlgorithmID:    "",
		EventSource:    model.AlarmEventSourcePatrol,
		DisplayName:    "任务巡查",
		PromptText:     "检查工位上是否有人睡觉",
		AlarmLevelID:   level.ID,
		Status:         model.EventStatusPending,
		OccurredAt:     now.Add(-time.Minute),
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        `{"overall":{"alarm":"1"}}`,
		SourceCallback: "{}",
	}
	if err := s.db.Create(&[]model.AlarmEvent{runtimeEvent, patrolEvent}).Error; err != nil {
		t.Fatalf("create source events failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")

	defaultItems := listEventIDsBySourceQuery(t, engine, token, "")
	if len(defaultItems) != 1 || defaultItems[0] != runtimeEvent.ID {
		t.Fatalf("expected default events list only runtime items, got=%v", defaultItems)
	}

	patrolItems := listEventIDsBySourceQuery(t, engine, token, "?source=patrol")
	if len(patrolItems) != 1 || patrolItems[0] != patrolEvent.ID {
		t.Fatalf("expected patrol source list only patrol items, got=%v", patrolItems)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/events/"+patrolEvent.ID, nil)
	detailReq.Header.Set("Authorization", "Bearer "+token)
	detailRec := httptest.NewRecorder()
	engine.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("patrol event detail failed: status=%d body=%s", detailRec.Code, detailRec.Body.String())
	}

	var detailResp struct {
		Code int `json:"code"`
		Data struct {
			ID          string `json:"id"`
			EventSource string `json:"event_source"`
			DisplayName string `json:"display_name"`
			PromptText  string `json:"prompt_text"`
		} `json:"data"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detailResp); err != nil {
		t.Fatalf("decode patrol event detail failed: %v", err)
	}
	if detailResp.Code != 0 {
		t.Fatalf("unexpected patrol event detail payload: %s", detailRec.Body.String())
	}
	if detailResp.Data.ID != patrolEvent.ID || detailResp.Data.EventSource != model.AlarmEventSourcePatrol {
		t.Fatalf("unexpected patrol event detail: %+v", detailResp.Data)
	}
	if detailResp.Data.DisplayName != patrolEvent.DisplayName || detailResp.Data.PromptText != patrolEvent.PromptText {
		t.Fatalf("expected patrol detail display/prompt returned, got %+v", detailResp.Data)
	}
}

func TestDashboardOverviewIgnoresPatrolEventsIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	area := model.Area{ID: "area-dashboard-patrol-ignore", Name: "大门区域", ParentID: model.RootAreaID, Sort: 1}
	if err := s.db.Create(&area).Error; err != nil {
		t.Fatalf("create area failed: %v", err)
	}
	device := model.Device{
		ID:            "dev-dashboard-patrol-ignore",
		Name:          "大门摄像头",
		AreaID:        area.ID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		App:           "live",
		StreamID:      "dashboard_patrol_ignore",
		StreamURL:     "rtsp://127.0.0.1/live/dashboard_patrol_ignore",
		Status:        "online",
		AIStatus:      model.DeviceAIStatusIdle,
		OutputConfig:  "{}",
		PlayWebRTCURL: "http://127.0.0.1/index/api/webrtc?app=live&stream=dashboard_patrol_ignore&type=play",
		PlayWSFLVURL:  "ws://127.0.0.1/live/dashboard_patrol_ignore.live.flv",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	algorithm := model.Algorithm{
		ID:              "alg-dashboard-patrol-ignore",
		Code:            "ALG_DASHBOARD_PATROL_IGNORE",
		Name:            "实时检测算法",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
		ModelProviderID: "provider-not-required-in-db-fixture",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	level := loadAlarmLevelBySeverity(t, s, 1)
	task := model.VideoTask{
		ID:              "task-dashboard-patrol-ignore",
		Name:            "实时任务",
		Status:          model.TaskStatusRunning,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:            task.ID,
		DeviceID:          device.ID,
		AlgorithmID:       algorithm.ID,
		AlarmLevelID:      level.ID,
		AlertCycleSeconds: 60,
	}).Error; err != nil {
		t.Fatalf("create task binding failed: %v", err)
	}

	now := time.Now()
	runtimeEvent := model.AlarmEvent{
		ID:             "event-dashboard-runtime-ignore",
		TaskID:         task.ID,
		DeviceID:       device.ID,
		AlgorithmID:    algorithm.ID,
		EventSource:    model.AlarmEventSourceRuntime,
		AlarmLevelID:   level.ID,
		Status:         model.EventStatusPending,
		OccurredAt:     now.Add(-2 * time.Minute),
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        "{}",
		SourceCallback: "{}",
	}
	patrolEvent := model.AlarmEvent{
		ID:             "event-dashboard-patrol-ignore",
		DeviceID:       device.ID,
		EventSource:    model.AlarmEventSourcePatrol,
		DisplayName:    "任务巡查",
		PromptText:     "检查大门是否有人停留",
		AlarmLevelID:   level.ID,
		Status:         model.EventStatusPending,
		OccurredAt:     now.Add(-10 * time.Second),
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        `{"overall":{"alarm":"1"}}`,
		SourceCallback: "{}",
	}
	if err := s.db.Create(&[]model.AlarmEvent{runtimeEvent, patrolEvent}).Error; err != nil {
		t.Fatalf("create dashboard source events failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/overview", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard overview failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Summary struct {
				AlarmingChannels int `json:"alarming_channels"`
			} `json:"summary"`
			Channels []struct {
				ID              string `json:"id"`
				Alarming60S     bool   `json:"alarming_60s"`
				TodayAlarmCount int64  `json:"today_alarm_count"`
				TotalAlarmCount int64  `json:"total_alarm_count"`
			} `json:"channels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode dashboard overview failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected dashboard overview payload: %s", rec.Body.String())
	}
	if resp.Data.Summary.AlarmingChannels != 0 {
		t.Fatalf("expected patrol events not to affect alarming_channels, got=%d", resp.Data.Summary.AlarmingChannels)
	}
	if len(resp.Data.Channels) != 1 {
		t.Fatalf("expected one channel in dashboard overview, got=%d", len(resp.Data.Channels))
	}
	if resp.Data.Channels[0].Alarming60S {
		t.Fatalf("expected patrol events not to affect alarming_60s")
	}
	if resp.Data.Channels[0].TodayAlarmCount != 1 || resp.Data.Channels[0].TotalAlarmCount != 1 {
		t.Fatalf("expected only runtime event counted in dashboard overview, got %+v", resp.Data.Channels[0])
	}
}

func TestDashboardCamera2OverviewIgnoresPatrolEventsIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	area := model.Area{ID: "area-camera2-patrol-ignore", Name: "车间区域", ParentID: model.RootAreaID, Sort: 1}
	if err := s.db.Create(&area).Error; err != nil {
		t.Fatalf("create area failed: %v", err)
	}
	device := model.Device{
		ID:            "dev-camera2-patrol-ignore",
		Name:          "车间摄像头",
		AreaID:        area.ID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		App:           "live",
		StreamID:      "camera2_patrol_ignore",
		StreamURL:     "rtsp://127.0.0.1/live/camera2_patrol_ignore",
		Status:        "online",
		AIStatus:      model.DeviceAIStatusRunning,
		OutputConfig:  "{}",
		PlayWebRTCURL: "http://127.0.0.1/index/api/webrtc?app=live&stream=camera2_patrol_ignore&type=play",
		PlayWSFLVURL:  "ws://127.0.0.1/live/camera2_patrol_ignore.live.flv",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	algorithm := model.Algorithm{
		ID:              "alg-camera2-patrol-ignore",
		Code:            "ALG_CAMERA2_PATROL_IGNORE",
		Name:            "车间烟火识别",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "fire",
		ModelProviderID: "provider-not-required-in-db-fixture",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	level := loadAlarmLevelBySeverity(t, s, 1)
	task := model.VideoTask{
		ID:              "task-camera2-patrol-ignore",
		Name:            "车间实时任务",
		Status:          model.TaskStatusRunning,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:            task.ID,
		DeviceID:          device.ID,
		AlgorithmID:       algorithm.ID,
		AlarmLevelID:      level.ID,
		AlertCycleSeconds: 60,
	}).Error; err != nil {
		t.Fatalf("create task binding failed: %v", err)
	}

	now := time.Now()
	runtimeEvent := model.AlarmEvent{
		ID:             "event-camera2-runtime-ignore",
		TaskID:         task.ID,
		DeviceID:       device.ID,
		AlgorithmID:    algorithm.ID,
		EventSource:    model.AlarmEventSourceRuntime,
		AlarmLevelID:   level.ID,
		Status:         model.EventStatusPending,
		OccurredAt:     now.Add(-90 * time.Second),
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        "{}",
		SourceCallback: "{}",
	}
	patrolEvent := model.AlarmEvent{
		ID:             "event-camera2-patrol-ignore",
		DeviceID:       device.ID,
		EventSource:    model.AlarmEventSourcePatrol,
		DisplayName:    "任务巡查",
		PromptText:     "检查车间地面是否有积水",
		AlarmLevelID:   level.ID,
		Status:         model.EventStatusPending,
		OccurredAt:     now.Add(-10 * time.Second),
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        `{"overall":{"alarm":"1"}}`,
		SourceCallback: "{}",
	}
	if err := s.db.Create(&[]model.AlarmEvent{runtimeEvent, patrolEvent}).Error; err != nil {
		t.Fatalf("create camera2 source events failed: %v", err)
	}

	resp := requestCamera2Overview(t, engine, "/api/v1/dashboard/camera2/overview?range=today")
	if resp.Data.AlarmStatistics.TotalAlarmCount != 1 {
		t.Fatalf("expected camera2 overview to count only runtime events, got=%d", resp.Data.AlarmStatistics.TotalAlarmCount)
	}
	if resp.Data.AlarmStatistics.PendingCount != 1 {
		t.Fatalf("expected pending_count=1, got=%d", resp.Data.AlarmStatistics.PendingCount)
	}
	if resp.Data.DeviceStatistics.AlarmDevices != 0 {
		t.Fatalf("expected patrol events not to affect alarming devices, got=%d", resp.Data.DeviceStatistics.AlarmDevices)
	}
	if len(resp.Data.DeviceStatistics.TopDevices) != 1 || resp.Data.DeviceStatistics.TopDevices[0].AlarmCount != 1 {
		t.Fatalf("expected top device count from runtime events only, got %+v", resp.Data.DeviceStatistics.TopDevices)
	}
}

type patrolAIRecorder struct {
	mu   sync.Mutex
	last ai.AnalyzeImageRequest
}

func (r *patrolAIRecorder) Store(req ai.AnalyzeImageRequest) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.last = req
	r.mu.Unlock()
}

func (r *patrolAIRecorder) Last() ai.AnalyzeImageRequest {
	if r == nil {
		return ai.AnalyzeImageRequest{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func newPatrolAIServer(t *testing.T, recorder *patrolAIRecorder, build func(req ai.AnalyzeImageRequest) ai.AnalyzeImageResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		var req ai.AnalyzeImageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if recorder != nil {
			recorder.Store(req)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(build(req))
	}))
}

func newPatrolSnapshotServer(t *testing.T, snapshotBody []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index/api/getSnap" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(snapshotBody)
	}))
}

func buildPatrolTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: uint8(40 + x*30), G: uint8(80 + y*20), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode patrol png failed: %v", err)
	}
	return buf.Bytes()
}

func createPatrolTestDevice(t *testing.T, s *Server, id, name string) model.Device {
	t.Helper()
	device := model.Device{
		ID:           id,
		Name:         name,
		AreaID:       model.RootAreaID,
		SourceType:   model.SourceTypePush,
		RowKind:      model.RowKindChannel,
		Protocol:     model.ProtocolRTSP,
		Transport:    "tcp",
		App:          "live",
		StreamID:     sanitizePathSegment(id),
		StreamURL:    "rtsp://127.0.0.1/live/" + sanitizePathSegment(id),
		Status:       "online",
		AIStatus:     model.DeviceAIStatusIdle,
		OutputConfig: "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create patrol test device failed: %v", err)
	}
	return device
}

func createPatrolJobViaAPI(t *testing.T, engine http.Handler, token string, payload map[string]any) string {
	t.Helper()
	rec := performAuthedJSONRequest(t, engine, token, http.MethodPost, "/api/v1/dashboard/camera2/patrol-jobs", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("create patrol job failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			JobID string `json:"job_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create patrol job response failed: %v", err)
	}
	if resp.Code != 0 || strings.TrimSpace(resp.Data.JobID) == "" {
		t.Fatalf("unexpected create patrol job payload: %s", rec.Body.String())
	}
	return resp.Data.JobID
}

func fetchPatrolJobViaAPI(t *testing.T, engine http.Handler, token, jobID string) camera2PatrolJobSnapshot {
	t.Helper()
	rec := performAuthedJSONRequest(t, engine, token, http.MethodGet, "/api/v1/dashboard/camera2/patrol-jobs/"+jobID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("fetch patrol job failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int                      `json:"code"`
		Data camera2PatrolJobSnapshot `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode patrol job snapshot failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected patrol job snapshot payload: %s", rec.Body.String())
	}
	return resp.Data
}

func waitPatrolJobFinished(t *testing.T, engine http.Handler, token, jobID string) camera2PatrolJobSnapshot {
	t.Helper()
	var snapshot camera2PatrolJobSnapshot
	waitForCondition(t, 5*time.Second, func() bool {
		snapshot = fetchPatrolJobViaAPI(t, engine, token, jobID)
		return snapshot.Status != model.AlgorithmTestJobStatusPending && snapshot.Status != model.AlgorithmTestJobStatusRunning
	})
	return snapshot
}

func listEventIDsBySourceQuery(t *testing.T, engine http.Handler, token, rawQuery string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events"+rawQuery, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list events failed: status=%d query=%s body=%s", rec.Code, rawQuery, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list events failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected list events payload: %s", rec.Body.String())
	}
	out := make([]string, 0, len(resp.Data.Items))
	for _, item := range resp.Data.Items {
		out = append(out, item.ID)
	}
	return out
}

func TestCamera2PatrolJobCreateBlockedWhenLLMTokenLimitReached(t *testing.T) {
	root := t.TempDir()
	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		s.cfg.Server.AI.DisableOnTokenLimitExceeded = true
		s.cfg.Server.AI.TotalTokenLimit = 100
		usage := makeLLMUsageCall("llm-patrol-quota-blocked", time.Now(), model.LLMUsageSourceDirectAnalyze, 100)
		if err := s.db.Create(&usage).Error; err != nil {
			t.Fatalf("create llm usage failed: %v", err)
		}

		engine := s.Engine()
		mockZLM := newPatrolSnapshotServer(t, buildPatrolTestPNG(t))
		defer mockZLM.Close()
		s.cfg.Server.ZLM.APIURL = mockZLM.URL

		analyzeCalls := 0
		mockAI := newPatrolAIServer(t, nil, func(req ai.AnalyzeImageRequest) ai.AnalyzeImageResponse {
			analyzeCalls++
			return ai.AnalyzeImageResponse{
				Success: true,
				Message: "ok",
			}
		})
		defer mockAI.Close()
		s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

		device := createPatrolTestDevice(t, s, "dev-patrol-quota-blocked", "配额拦截巡检摄像头")
		token := loginToken(t, engine, "admin", "admin")
		rec := performAuthedJSONRequest(t, engine, token, http.MethodPost, "/api/v1/dashboard/camera2/patrol-jobs", map[string]any{
			"device_ids": []string{device.ID},
			"prompt":     "检查画面是否存在异常",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected patrol job create blocked with status=400, got status=%d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), llmTokenLimitExceededMessage) {
			t.Fatalf("expected quota exceeded message, got body=%s", rec.Body.String())
		}
		if analyzeCalls != 0 {
			t.Fatalf("expected patrol analyze api not to be called, got %d", analyzeCalls)
		}
	})
}
