package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"maas-box/internal/model"
)

func TestDashboardCamera2OverviewTodayIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.TotalTokenLimit = 5000
	engine := s.Engine()

	areaA := model.Area{ID: "area-camera2-a", Name: "北门", ParentID: model.RootAreaID, Sort: 1}
	areaB := model.Area{ID: "area-camera2-b", Name: "仓库区", ParentID: model.RootAreaID, Sort: 2}
	if err := s.db.Create(&[]model.Area{areaA, areaB}).Error; err != nil {
		t.Fatalf("create areas failed: %v", err)
	}

	devices := []model.Device{
		{
			ID:            "dev-camera2-1",
			Name:          "北门摄像头",
			AreaID:        areaA.ID,
			SourceType:    model.SourceTypePull,
			RowKind:       model.RowKindChannel,
			Protocol:      model.ProtocolRTSP,
			Transport:     "tcp",
			App:           "live",
			StreamID:      "camera2_1",
			StreamURL:     "rtsp://127.0.0.1/live/camera2_1",
			Status:        "online",
			AIStatus:      model.DeviceAIStatusRunning,
			OutputConfig:  "{}",
			PlayWebRTCURL: "http://127.0.0.1/index/api/webrtc?app=live&stream=camera2_1&type=play",
			PlayWSFLVURL:  "ws://127.0.0.1/live/camera2_1.live.flv",
		},
		{
			ID:            "dev-camera2-2",
			Name:          "仓库摄像头",
			AreaID:        areaB.ID,
			SourceType:    model.SourceTypePull,
			RowKind:       model.RowKindChannel,
			Protocol:      model.ProtocolRTSP,
			Transport:     "tcp",
			App:           "live",
			StreamID:      "camera2_2",
			StreamURL:     "rtsp://127.0.0.1/live/camera2_2",
			Status:        "online",
			AIStatus:      model.DeviceAIStatusRunning,
			OutputConfig:  "{}",
			PlayWebRTCURL: "http://127.0.0.1/index/api/webrtc?app=live&stream=camera2_2&type=play",
			PlayWSFLVURL:  "ws://127.0.0.1/live/camera2_2.live.flv",
		},
	}
	if err := s.db.Create(&devices).Error; err != nil {
		t.Fatalf("create devices failed: %v", err)
	}

	algorithms := []model.Algorithm{
		{ID: "alg-camera2-1", Name: "烟火识别", Mode: model.AlgorithmModeSmall, Enabled: true, SmallModelLabel: "fire"},
		{ID: "alg-camera2-2", Name: "人员闯入", Mode: model.AlgorithmModeSmall, Enabled: true, SmallModelLabel: "person"},
	}
	if err := s.db.Create(&algorithms).Error; err != nil {
		t.Fatalf("create algorithms failed: %v", err)
	}

	highLevel := loadAlarmLevelBySeverity(t, s, 1)
	mediumLevel := loadAlarmLevelBySeverity(t, s, 2)
	lowLevel := loadAlarmLevelBySeverity(t, s, 3)

	tasks := []model.VideoTask{
		{
			ID:              "task-camera2-1",
			Name:            "北门任务",
			Status:          model.TaskStatusRunning,
			FrameInterval:   5,
			SmallConfidence: 0.5,
			LargeConfidence: 0.8,
			SmallIOU:        0.8,
			AlarmLevelID:    highLevel.ID,
		},
		{
			ID:              "task-camera2-2",
			Name:            "仓库任务",
			Status:          model.TaskStatusRunning,
			FrameInterval:   5,
			SmallConfidence: 0.5,
			LargeConfidence: 0.8,
			SmallIOU:        0.8,
			AlarmLevelID:    mediumLevel.ID,
		},
	}
	if err := s.db.Create(&tasks).Error; err != nil {
		t.Fatalf("create tasks failed: %v", err)
	}

	if err := s.db.Create(&[]model.VideoTaskDeviceAlgorithm{
		{
			TaskID:            tasks[0].ID,
			DeviceID:          devices[0].ID,
			AlgorithmID:       algorithms[0].ID,
			AlarmLevelID:      highLevel.ID,
			AlertCycleSeconds: 60,
		},
	}).Error; err != nil {
		t.Fatalf("create modern bindings failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDevice{TaskID: tasks[1].ID, DeviceID: devices[1].ID}).Error; err != nil {
		t.Fatalf("create legacy task-device failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskAlgorithm{TaskID: tasks[1].ID, AlgorithmID: algorithms[1].ID}).Error; err != nil {
		t.Fatalf("create legacy task-algorithm failed: %v", err)
	}

	now := time.Now().In(time.Local)
	events := []model.AlarmEvent{
		{
			ID:             "event-camera2-1",
			TaskID:         tasks[0].ID,
			DeviceID:       devices[0].ID,
			AlgorithmID:    algorithms[0].ID,
			AlarmLevelID:   highLevel.ID,
			Status:         model.EventStatusPending,
			OccurredAt:     now.Add(-30 * time.Second),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
		{
			ID:             "event-camera2-2",
			TaskID:         tasks[0].ID,
			DeviceID:       devices[0].ID,
			AlgorithmID:    algorithms[0].ID,
			AlarmLevelID:   mediumLevel.ID,
			Status:         model.EventStatusValid,
			OccurredAt:     now.Add(-2 * time.Hour),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
		{
			ID:             "event-camera2-3",
			TaskID:         tasks[0].ID,
			DeviceID:       devices[0].ID,
			AlgorithmID:    algorithms[0].ID,
			AlarmLevelID:   highLevel.ID,
			Status:         model.EventStatusInvalid,
			OccurredAt:     now.Add(-4 * time.Hour),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
		{
			ID:             "event-camera2-4",
			TaskID:         tasks[1].ID,
			DeviceID:       devices[1].ID,
			AlgorithmID:    algorithms[1].ID,
			AlarmLevelID:   lowLevel.ID,
			Status:         model.EventStatusValid,
			OccurredAt:     now.AddDate(0, 0, -3),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
		{
			ID:             "event-camera2-5",
			TaskID:         tasks[1].ID,
			DeviceID:       devices[1].ID,
			AlgorithmID:    algorithms[1].ID,
			AlarmLevelID:   highLevel.ID,
			Status:         model.EventStatusValid,
			OccurredAt:     now.AddDate(0, 0, -10),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
		{
			ID:             "event-camera2-6",
			TaskID:         tasks[1].ID,
			DeviceID:       devices[1].ID,
			AlgorithmID:    algorithms[1].ID,
			AlarmLevelID:   mediumLevel.ID,
			Status:         model.EventStatusPending,
			OccurredAt:     now.AddDate(0, 0, -1),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
	}
	if err := s.db.Create(&events).Error; err != nil {
		t.Fatalf("create events failed: %v", err)
	}

	if err := s.db.Create(&[]model.LLMUsageCall{
		makeLLMUsageCall("llm-camera2-1", now.Add(-20*time.Minute), model.LLMUsageSourceTaskRuntime, 1000),
		makeLLMUsageCall("llm-camera2-2", now.Add(-1*time.Hour), model.LLMUsageSourceTaskRuntime, 500),
		makeLLMUsageCall("llm-camera2-3", now.AddDate(0, 0, -3), model.LLMUsageSourceTaskRuntime, 700),
		makeLLMUsageCall("llm-camera2-4", now.AddDate(0, 0, -10), model.LLMUsageSourceTaskRuntime, 900),
		makeLLMUsageCall("llm-camera2-5", now.Add(-10*time.Minute), model.LLMUsageSourceDirectAnalyze, 600),
	}).Error; err != nil {
		t.Fatalf("create llm usage failed: %v", err)
	}

	resp := requestCamera2Overview(t, engine, "/api/v1/dashboard/camera2/overview?range=today")

	if resp.Data.AlarmStatistics.TotalAlarmCount != 3 {
		t.Fatalf("expected total_alarm_count=3, got %d", resp.Data.AlarmStatistics.TotalAlarmCount)
	}
	if resp.Data.AlarmStatistics.PendingCount != 1 {
		t.Fatalf("expected pending_count=1, got %d", resp.Data.AlarmStatistics.PendingCount)
	}
	assertAlmostEqual(t, resp.Data.AlarmStatistics.HandlingRate, 66.6667, 0.05, "handling_rate")
	assertAlmostEqual(t, resp.Data.AlarmStatistics.FalseAlarmRate, 33.3333, 0.05, "false_alarm_rate")
	if resp.Data.AlarmStatistics.HighCount != 2 || resp.Data.AlarmStatistics.MediumCount != 1 || resp.Data.AlarmStatistics.LowCount != 0 {
		t.Fatalf("unexpected severity stats: %+v", resp.Data.AlarmStatistics)
	}

	if resp.Data.AlgorithmStatistics.DeployTotal != 2 {
		t.Fatalf("expected deploy_total=2, got %d", resp.Data.AlgorithmStatistics.DeployTotal)
	}
	if resp.Data.AlgorithmStatistics.RunningTotal != 2 {
		t.Fatalf("expected running_total=2, got %d", resp.Data.AlgorithmStatistics.RunningTotal)
	}
	assertAlmostEqual(t, resp.Data.AlgorithmStatistics.AverageAccuracy, 66.6667, 0.05, "average_accuracy")
	if resp.Data.AlgorithmStatistics.TodayCallCount != 2 {
		t.Fatalf("expected today_call_count=2, got %d", resp.Data.AlgorithmStatistics.TodayCallCount)
	}
	if len(resp.Data.AlgorithmStatistics.Items) == 0 {
		t.Fatalf("expected algorithm items not empty")
	}
	firstAlgorithm := resp.Data.AlgorithmStatistics.Items[0]
	if firstAlgorithm.AlgorithmID != algorithms[0].ID || firstAlgorithm.AlarmCount != 3 {
		t.Fatalf("unexpected first algorithm item: %+v", firstAlgorithm)
	}
	assertAlmostEqual(t, firstAlgorithm.Accuracy, 66.6667, 0.05, "algorithm_accuracy")

	if resp.Data.DeviceStatistics.TotalDevices != 2 || resp.Data.DeviceStatistics.AreaCount != 2 {
		t.Fatalf("unexpected device overview: %+v", resp.Data.DeviceStatistics)
	}
	if resp.Data.DeviceStatistics.OnlineDevices != 2 || resp.Data.DeviceStatistics.OfflineDevices != 0 {
		t.Fatalf("unexpected online/offline stats: %+v", resp.Data.DeviceStatistics)
	}
	assertAlmostEqual(t, resp.Data.DeviceStatistics.OnlineRate, 100, 0.01, "online_rate")
	if resp.Data.DeviceStatistics.AlarmDevices != 1 {
		t.Fatalf("expected alarm_devices=1, got %d", resp.Data.DeviceStatistics.AlarmDevices)
	}
	if len(resp.Data.DeviceStatistics.TopDevices) == 0 || resp.Data.DeviceStatistics.TopDevices[0].DeviceID != devices[0].ID {
		t.Fatalf("unexpected top_devices: %+v", resp.Data.DeviceStatistics.TopDevices)
	}

	if len(resp.Data.Analysis.AreaDistribution) == 0 || resp.Data.Analysis.AreaDistribution[0].Count != 3 {
		t.Fatalf("unexpected area distribution: %+v", resp.Data.Analysis.AreaDistribution)
	}
	if len(resp.Data.Analysis.TypeDistribution) == 0 || resp.Data.Analysis.TypeDistribution[0].Count != 3 {
		t.Fatalf("unexpected type distribution: %+v", resp.Data.Analysis.TypeDistribution)
	}
	var trendSum int64
	for _, item := range resp.Data.Analysis.Trend {
		trendSum += item.AlarmCount
	}
	if trendSum != 3 {
		t.Fatalf("expected trend sum=3, got %d", trendSum)
	}
	if resp.Data.Analysis.TrendUnit != camera2TrendUnitHour {
		t.Fatalf("expected trend_unit=hour, got %s", resp.Data.Analysis.TrendUnit)
	}

	if resp.Data.ResourceStatistics.TokenTotalLimit != 5000 {
		t.Fatalf("expected token_total_limit=5000, got %d", resp.Data.ResourceStatistics.TokenTotalLimit)
	}
	if resp.Data.ResourceStatistics.TokenUsed != 3700 {
		t.Fatalf("expected token_used=3700, got %d", resp.Data.ResourceStatistics.TokenUsed)
	}
	if resp.Data.ResourceStatistics.TokenRemaining != 1300 {
		t.Fatalf("expected token_remaining=1300, got %d", resp.Data.ResourceStatistics.TokenRemaining)
	}
	if resp.Data.ResourceStatistics.EstimatedRemainingDays == nil {
		t.Fatalf("expected estimated_remaining_days not nil")
	}
	assertAlmostEqual(t, *resp.Data.ResourceStatistics.EstimatedRemainingDays, 1300.0/(2800.0/7.0), 0.05, "estimated_remaining_days")
}

func TestDashboardCamera2OverviewCustomRangeWithoutTokenLimit(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	area := model.Area{ID: "area-camera2-custom", Name: "西门", ParentID: model.RootAreaID, Sort: 1}
	if err := s.db.Create(&area).Error; err != nil {
		t.Fatalf("create area failed: %v", err)
	}
	device := model.Device{
		ID:            "dev-camera2-custom",
		Name:          "西门摄像头",
		AreaID:        area.ID,
		SourceType:    model.SourceTypePull,
		RowKind:       model.RowKindChannel,
		Protocol:      model.ProtocolRTSP,
		Transport:     "tcp",
		App:           "live",
		StreamID:      "camera2_custom",
		StreamURL:     "rtsp://127.0.0.1/live/camera2_custom",
		Status:        "online",
		AIStatus:      model.DeviceAIStatusRunning,
		OutputConfig:  "{}",
		PlayWebRTCURL: "http://127.0.0.1/index/api/webrtc?app=live&stream=camera2_custom&type=play",
		PlayWSFLVURL:  "ws://127.0.0.1/live/camera2_custom.live.flv",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	algorithm := model.Algorithm{ID: "alg-camera2-custom", Name: "车辆识别", Mode: model.AlgorithmModeSmall, Enabled: true, SmallModelLabel: "car"}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	level := loadAlarmLevelBySeverity(t, s, 3)
	task := model.VideoTask{
		ID:              "task-camera2-custom",
		Name:            "西门任务",
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
		t.Fatalf("create binding failed: %v", err)
	}

	now := time.Now().In(time.Local)
	customStart := now.Add(-90 * time.Minute)
	customEnd := now.Add(-10 * time.Minute)
	if err := s.db.Create(&[]model.AlarmEvent{
		{
			ID:             "event-camera2-custom-1",
			TaskID:         task.ID,
			DeviceID:       device.ID,
			AlgorithmID:    algorithm.ID,
			AlarmLevelID:   level.ID,
			Status:         model.EventStatusPending,
			OccurredAt:     now.Add(-30 * time.Minute),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
		{
			ID:             "event-camera2-custom-2",
			TaskID:         task.ID,
			DeviceID:       device.ID,
			AlgorithmID:    algorithm.ID,
			AlarmLevelID:   level.ID,
			Status:         model.EventStatusPending,
			OccurredAt:     now.Add(-4 * time.Hour),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
	}).Error; err != nil {
		t.Fatalf("create custom events failed: %v", err)
	}

	url := fmt.Sprintf(
		"/api/v1/dashboard/camera2/overview?range=custom&start_at=%d&end_at=%d",
		customStart.UnixMilli(),
		customEnd.UnixMilli(),
	)
	resp := requestCamera2Overview(t, engine, url)

	if resp.Data.Range != camera2RangeCustom {
		t.Fatalf("expected range=custom, got %s", resp.Data.Range)
	}
	if resp.Data.AlarmStatistics.TotalAlarmCount != 1 {
		t.Fatalf("expected custom total_alarm_count=1, got %d", resp.Data.AlarmStatistics.TotalAlarmCount)
	}
	assertAlmostEqual(t, resp.Data.AlgorithmStatistics.AverageAccuracy, 100, 0.01, "custom_average_accuracy")
	if len(resp.Data.AlgorithmStatistics.Items) != 1 {
		t.Fatalf("expected custom algorithm items=1, got %d", len(resp.Data.AlgorithmStatistics.Items))
	}
	assertAlmostEqual(t, resp.Data.AlgorithmStatistics.Items[0].Accuracy, 100, 0.01, "custom_algorithm_accuracy")
	if resp.Data.Analysis.TrendUnit != camera2TrendUnitHour {
		t.Fatalf("expected custom trend_unit=hour, got %s", resp.Data.Analysis.TrendUnit)
	}
	if resp.Data.ResourceStatistics.TokenTotalLimit != 0 {
		t.Fatalf("expected token_total_limit=0, got %d", resp.Data.ResourceStatistics.TokenTotalLimit)
	}
	if resp.Data.ResourceStatistics.EstimatedRemainingDays != nil {
		t.Fatalf("expected estimated_remaining_days=nil, got %+v", resp.Data.ResourceStatistics.EstimatedRemainingDays)
	}
}

func requestCamera2Overview(t *testing.T, engine http.Handler, path string) camera2OverviewTestResponse {
	t.Helper()
	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("request camera2 overview failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp camera2OverviewTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode camera2 overview failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected response body: %s", rec.Body.String())
	}
	return resp
}

func loadAlarmLevelBySeverity(t *testing.T, s *Server, severity int) model.AlarmLevel {
	t.Helper()
	var level model.AlarmLevel
	if err := s.db.Where("severity = ?", severity).First(&level).Error; err != nil {
		t.Fatalf("load alarm level by severity=%d failed: %v", severity, err)
	}
	return level
}

func makeLLMUsageCall(id string, occurredAt time.Time, source string, totalTokens int) model.LLMUsageCall {
	tokens := totalTokens
	return model.LLMUsageCall{
		ID:             id,
		Source:         source,
		CallStatus:     model.LLMUsageStatusSuccess,
		UsageAvailable: true,
		TotalTokens:    &tokens,
		OccurredAt:     occurredAt,
	}
}

func assertAlmostEqual(t *testing.T, got float64, want float64, delta float64, field string) {
	t.Helper()
	if math.Abs(got-want) > delta {
		t.Fatalf("unexpected %s: got=%f want=%f delta=%f", field, got, want, delta)
	}
}

type camera2OverviewTestResponse struct {
	Code int `json:"code"`
	Data struct {
		Range           string `json:"range"`
		AlarmStatistics struct {
			TotalAlarmCount int64   `json:"total_alarm_count"`
			PendingCount    int64   `json:"pending_count"`
			HandlingRate    float64 `json:"handling_rate"`
			FalseAlarmRate  float64 `json:"false_alarm_rate"`
			HighCount       int64   `json:"high_count"`
			MediumCount     int64   `json:"medium_count"`
			LowCount        int64   `json:"low_count"`
		} `json:"alarm_statistics"`
		AlgorithmStatistics struct {
			DeployTotal     int64   `json:"deploy_total"`
			RunningTotal    int64   `json:"running_total"`
			AverageAccuracy float64 `json:"average_accuracy"`
			TodayCallCount  int64   `json:"today_call_count"`
			Items           []struct {
				AlgorithmID string  `json:"algorithm_id"`
				AlarmCount  int64   `json:"alarm_count"`
				Accuracy    float64 `json:"accuracy"`
			} `json:"items"`
		} `json:"algorithm_statistics"`
		DeviceStatistics struct {
			TotalDevices   int64   `json:"total_devices"`
			AreaCount      int64   `json:"area_count"`
			OnlineDevices  int64   `json:"online_devices"`
			OnlineRate     float64 `json:"online_rate"`
			AlarmDevices   int64   `json:"alarm_devices"`
			OfflineDevices int64   `json:"offline_devices"`
			TopDevices     []struct {
				DeviceID   string `json:"device_id"`
				AlarmCount int64  `json:"alarm_count"`
			} `json:"top_devices"`
		} `json:"device_statistics"`
		Analysis struct {
			TrendUnit        string `json:"trend_unit"`
			AreaDistribution []struct {
				Count int64 `json:"count"`
			} `json:"area_distribution"`
			TypeDistribution []struct {
				Count int64 `json:"count"`
			} `json:"type_distribution"`
			Trend []struct {
				AlarmCount int64 `json:"alarm_count"`
			} `json:"trend"`
		} `json:"analysis"`
		ResourceStatistics struct {
			TokenTotalLimit        int64    `json:"token_total_limit"`
			TokenUsed              int64    `json:"token_used"`
			TokenRemaining         int64    `json:"token_remaining"`
			EstimatedRemainingDays *float64 `json:"estimated_remaining_days"`
		} `json:"resource_statistics"`
	} `json:"data"`
}
