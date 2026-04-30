package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"maas-box/internal/model"
)

func TestDashboardOverviewIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	area := model.Area{
		ID:       "area-dashboard-1",
		Name:     "North Gate",
		ParentID: model.RootAreaID,
		IsRoot:   false,
		Sort:     1,
	}
	if err := s.db.Create(&area).Error; err != nil {
		t.Fatalf("create area failed: %v", err)
	}

	devices := []model.Device{
		{
			ID:            "dev-dashboard-modern-running",
			Name:          "Camera Modern Running",
			AreaID:        area.ID,
			SourceType:    model.SourceTypePull,
			RowKind:       model.RowKindChannel,
			Protocol:      model.ProtocolRTSP,
			Transport:     "tcp",
			App:           "live",
			StreamID:      "dev_dashboard_modern_running",
			StreamURL:     "rtsp://127.0.0.1:8554/live/modern-running",
			Status:        "online",
			AIStatus:      model.DeviceAIStatusIdle,
			OutputConfig:  "{}",
			PlayWebRTCURL: "http://127.0.0.1/index/api/webrtc?app=live&stream=dev_dashboard_modern_running&type=play",
			PlayWSFLVURL:  "ws://127.0.0.1/live/dev_dashboard_modern_running.live.flv",
		},
		{
			ID:            "dev-dashboard-modern-stopped",
			Name:          "Camera Modern Stopped",
			AreaID:        area.ID,
			SourceType:    model.SourceTypePull,
			RowKind:       model.RowKindChannel,
			Protocol:      model.ProtocolRTSP,
			Transport:     "tcp",
			App:           "live",
			StreamID:      "dev_dashboard_modern_stopped",
			StreamURL:     "rtsp://127.0.0.1:8554/live/modern-stopped",
			Status:        "offline",
			AIStatus:      model.DeviceAIStatusIdle,
			OutputConfig:  "{}",
			PlayWebRTCURL: "http://127.0.0.1/index/api/webrtc?app=live&stream=dev_dashboard_modern_stopped&type=play",
			PlayWSFLVURL:  "ws://127.0.0.1/live/dev_dashboard_modern_stopped.live.flv",
		},
		{
			ID:            "dev-dashboard-legacy",
			Name:          "Camera Legacy",
			AreaID:        model.RootAreaID,
			SourceType:    model.SourceTypePull,
			RowKind:       model.RowKindChannel,
			Protocol:      model.ProtocolRTSP,
			Transport:     "tcp",
			App:           "live",
			StreamID:      "dev_dashboard_legacy",
			StreamURL:     "rtsp://127.0.0.1:8554/live/legacy",
			Status:        "online",
			AIStatus:      model.DeviceAIStatusIdle,
			OutputConfig:  "{}",
			PlayWebRTCURL: "http://127.0.0.1/index/api/webrtc?app=live&stream=dev_dashboard_legacy&type=play",
			PlayWSFLVURL:  "ws://127.0.0.1/live/dev_dashboard_legacy.live.flv",
		},
		{
			ID:            "dev-dashboard-no-algorithm",
			Name:          "Camera No Algorithm",
			AreaID:        model.RootAreaID,
			SourceType:    model.SourceTypePull,
			RowKind:       model.RowKindChannel,
			Protocol:      model.ProtocolRTSP,
			Transport:     "tcp",
			App:           "live",
			StreamID:      "dev_dashboard_no_algorithm",
			StreamURL:     "rtsp://127.0.0.1:8554/live/no-algorithm",
			Status:        "offline",
			AIStatus:      model.DeviceAIStatusIdle,
			OutputConfig:  "{}",
			PlayWebRTCURL: "http://127.0.0.1/index/api/webrtc?app=live&stream=dev_dashboard_no_algorithm&type=play",
			PlayWSFLVURL:  "ws://127.0.0.1/live/dev_dashboard_no_algorithm.live.flv",
		},
	}
	if err := s.db.Create(&devices).Error; err != nil {
		t.Fatalf("create devices failed: %v", err)
	}

	algorithms := []model.Algorithm{
		{
			ID:              "alg-dashboard-modern-running",
			Name:            "Intrusion Detection",
			Mode:            model.AlgorithmModeSmall,
			Enabled:         true,
			SmallModelLabel: "person",
		},
		{
			ID:              "alg-dashboard-modern-stopped",
			Name:            "Smoke Detection",
			Mode:            model.AlgorithmModeSmall,
			Enabled:         true,
			SmallModelLabel: "smoke",
		},
		{
			ID:              "alg-dashboard-legacy",
			Name:            "Helmet Check",
			Mode:            model.AlgorithmModeSmall,
			Enabled:         true,
			SmallModelLabel: "helmet",
		},
	}
	if err := s.db.Create(&algorithms).Error; err != nil {
		t.Fatalf("create algorithms failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	tasks := []model.VideoTask{
		{
			ID:              "task-dashboard-running",
			Name:            "Dashboard Running Task",
			Status:          model.TaskStatusRunning,
			FrameInterval:   5,
			SmallConfidence: 0.5,
			LargeConfidence: 0.8,
			SmallIOU:        0.8,
			AlarmLevelID:    level.ID,
		},
		{
			ID:              "task-dashboard-stopped",
			Name:            "Dashboard Stopped Task",
			Status:          model.TaskStatusStopped,
			FrameInterval:   5,
			SmallConfidence: 0.5,
			LargeConfidence: 0.8,
			SmallIOU:        0.8,
			AlarmLevelID:    level.ID,
		},
		{
			ID:              "task-dashboard-legacy",
			Name:            "Dashboard Legacy Task",
			Status:          model.TaskStatusStopped,
			FrameInterval:   5,
			SmallConfidence: 0.5,
			LargeConfidence: 0.8,
			SmallIOU:        0.8,
			AlarmLevelID:    level.ID,
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
			AlarmLevelID:      level.ID,
			AlertCycleSeconds: 60,
		},
		{
			TaskID:            tasks[1].ID,
			DeviceID:          devices[1].ID,
			AlgorithmID:       algorithms[1].ID,
			AlarmLevelID:      level.ID,
			AlertCycleSeconds: 60,
		},
	}).Error; err != nil {
		t.Fatalf("create task-device-algorithm relations failed: %v", err)
	}

	if err := s.db.Create(&model.VideoTaskDevice{
		TaskID:   tasks[2].ID,
		DeviceID: devices[2].ID,
	}).Error; err != nil {
		t.Fatalf("create legacy task-device relation failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskAlgorithm{
		TaskID:      tasks[2].ID,
		AlgorithmID: algorithms[2].ID,
	}).Error; err != nil {
		t.Fatalf("create legacy task-algorithm relation failed: %v", err)
	}

	now := time.Now()
	events := []model.AlarmEvent{
		{
			ID:             "event-dashboard-1",
			TaskID:         tasks[0].ID,
			DeviceID:       devices[0].ID,
			AlgorithmID:    algorithms[0].ID,
			AlarmLevelID:   level.ID,
			Status:         model.EventStatusPending,
			OccurredAt:     now.Add(-20 * time.Second),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
		{
			ID:             "event-dashboard-2",
			TaskID:         tasks[0].ID,
			DeviceID:       devices[0].ID,
			AlgorithmID:    algorithms[0].ID,
			AlarmLevelID:   level.ID,
			Status:         model.EventStatusValid,
			OccurredAt:     now.Add(-2 * time.Minute),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
		{
			ID:             "event-dashboard-3",
			TaskID:         tasks[1].ID,
			DeviceID:       devices[1].ID,
			AlgorithmID:    algorithms[1].ID,
			AlarmLevelID:   level.ID,
			Status:         model.EventStatusPending,
			OccurredAt:     now.Add(-3 * time.Minute),
			BoxesJSON:      "[]",
			YoloJSON:       "[]",
			LLMJSON:        "{}",
			SourceCallback: "{}",
		},
	}
	if err := s.db.Create(&events).Error; err != nil {
		t.Fatalf("create events failed: %v", err)
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
				TotalChannels    int `json:"total_channels"`
				OnlineChannels   int `json:"online_channels"`
				OfflineChannels  int `json:"offline_channels"`
				AlarmingChannels int `json:"alarming_channels"`
			} `json:"summary"`
			Channels []struct {
				ID              string   `json:"id"`
				Alarming60S     bool     `json:"alarming_60s"`
				TodayAlarmCount int64    `json:"today_alarm_count"`
				TotalAlarmCount int64    `json:"total_alarm_count"`
				Algorithms      []string `json:"algorithms"`
			} `json:"channels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected response body: %s", rec.Body.String())
	}

	if resp.Data.Summary.TotalChannels != 4 {
		t.Fatalf("expected total_channels=4, got %d", resp.Data.Summary.TotalChannels)
	}
	if resp.Data.Summary.OnlineChannels != 2 {
		t.Fatalf("expected online_channels=2, got %d", resp.Data.Summary.OnlineChannels)
	}
	if resp.Data.Summary.OfflineChannels != 2 {
		t.Fatalf("expected offline_channels=2, got %d", resp.Data.Summary.OfflineChannels)
	}
	if resp.Data.Summary.AlarmingChannels != 1 {
		t.Fatalf("expected alarming_channels=1, got %d", resp.Data.Summary.AlarmingChannels)
	}

	channelByID := make(map[string]struct {
		Alarming60S     bool
		TodayAlarmCount int64
		TotalAlarmCount int64
		Algorithms      []string
	}, len(resp.Data.Channels))
	for _, item := range resp.Data.Channels {
		channelByID[item.ID] = struct {
			Alarming60S     bool
			TodayAlarmCount int64
			TotalAlarmCount int64
			Algorithms      []string
		}{
			Alarming60S:     item.Alarming60S,
			TodayAlarmCount: item.TodayAlarmCount,
			TotalAlarmCount: item.TotalAlarmCount,
			Algorithms:      item.Algorithms,
		}
	}

	runningChannel, ok := channelByID[devices[0].ID]
	if !ok {
		t.Fatalf("modern running channel not found in overview")
	}
	if !runningChannel.Alarming60S {
		t.Fatalf("expected modern running channel alarming_60s=true")
	}
	if runningChannel.TodayAlarmCount < 2 {
		t.Fatalf("expected modern running channel today alarm count >= 2, got %d", runningChannel.TodayAlarmCount)
	}
	if runningChannel.TotalAlarmCount < 2 {
		t.Fatalf("expected modern running channel total alarm count >= 2, got %d", runningChannel.TotalAlarmCount)
	}
	if !containsDashboardAlgorithmName(runningChannel.Algorithms, algorithms[0].Name) {
		t.Fatalf("expected modern running channel includes configured algorithm")
	}

	stoppedChannel, ok := channelByID[devices[1].ID]
	if !ok {
		t.Fatalf("modern stopped channel not found in overview")
	}
	if !containsDashboardAlgorithmName(stoppedChannel.Algorithms, algorithms[1].Name) {
		t.Fatalf("expected modern stopped channel includes configured algorithm")
	}

	legacyChannel, ok := channelByID[devices[2].ID]
	if !ok {
		t.Fatalf("legacy channel not found in overview")
	}
	if !containsDashboardAlgorithmName(legacyChannel.Algorithms, algorithms[2].Name) {
		t.Fatalf("expected legacy channel includes configured algorithm")
	}

	noAlgorithmChannel, ok := channelByID[devices[3].ID]
	if !ok {
		t.Fatalf("no-algorithm channel not found in overview")
	}
	if noAlgorithmChannel.Algorithms == nil {
		t.Fatalf("expected no-algorithm channel to return empty array, got null")
	}
	if len(noAlgorithmChannel.Algorithms) != 0 {
		t.Fatalf("expected no-algorithm channel to return empty array, got %v", noAlgorithmChannel.Algorithms)
	}
}

func containsDashboardAlgorithmName(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
