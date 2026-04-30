package server

import (
	"testing"
	"time"

	"maas-box/internal/config"
	"maas-box/internal/model"
)

func TestParseLLMResult(t *testing.T) {
	raw := `{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["ALG001"]},"task_results":[{"task_code":"ALG001","task_name":"intrusion","alarm":"1","reason":"person entered zone","object_ids":["O001"]}],"objects":[{"object_id":"O001","task_code":"ALG001","bbox2d":[100,100,300,400],"label":"person","confidence":0.91}]}`
	out := parseLLMResult(raw)
	if len(out.TaskResults) != 1 {
		t.Fatalf("expected 1 task_result, got %d", len(out.TaskResults))
	}
	if out.TaskResults[0].TaskCode != "ALG001" {
		t.Fatalf("unexpected task_code: %s", out.TaskResults[0].TaskCode)
	}
	if normalizeAlarmValue(out.TaskResults[0].Alarm) != "1" {
		t.Fatal("expected alarm=1")
	}
	if out.TaskResults[0].TaskMode != "" {
		t.Fatalf("expected omitted task_mode to stay empty, got %q", out.TaskResults[0].TaskMode)
	}
}

func TestParseLLMResultAcceptsGlobalTaskMode(t *testing.T) {
	raw := `{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["ALG001"]},"task_results":[{"task_code":"ALG001","task_name":"intrusion","task_mode":"global","alarm":"1","reason":"smoke found","object_ids":[]}],"objects":[]}`
	out := parseLLMResult(raw)
	if len(out.TaskResults) != 1 {
		t.Fatalf("expected 1 task_result, got %d", len(out.TaskResults))
	}
	if out.TaskResults[0].TaskMode != "global" {
		t.Fatalf("expected task_mode to remain parseable as global, got %s", out.TaskResults[0].TaskMode)
	}
}

func TestParseLLMResultAllowsMinimalRequiredFields(t *testing.T) {
	raw := `{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["ALG001"]},"task_results":[{"task_code":"ALG001","task_name":"intrusion","alarm":"1","reason":"person entered zone","object_ids":["O001"]},{"task_code":"ALG002","task_name":"smoke","alarm":"0","reason":"no smoke","object_ids":[]}],"objects":[{"object_id":"O001","task_code":"ALG001","bbox2d":[120,180,420,860],"label":"person","confidence":0.92}]}`
	out := parseLLMResult(raw)
	if len(out.TaskResults) != 2 {
		t.Fatalf("expected 2 task_results, got %d", len(out.TaskResults))
	}
	if len(out.Objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(out.Objects))
	}
	if out.TaskResults[0].Suggestion != "" {
		t.Fatalf("expected omitted suggestion to stay empty, got %q", out.TaskResults[0].Suggestion)
	}
	if len(out.TaskResults[0].Excluded) != 0 {
		t.Fatalf("expected omitted excluded to stay empty, got %+v", out.TaskResults[0].Excluded)
	}
	if out.Objects[0].ObjectID != "O001" || out.Objects[0].TaskCode != "ALG001" {
		t.Fatalf("unexpected parsed object: %+v", out.Objects[0])
	}
}

func TestClamp(t *testing.T) {
	if got := clamp(0, 0.1, 0.9, 0.5); got != 0.5 {
		t.Fatalf("expected fallback 0.5, got %v", got)
	}
	if got := clamp(0.01, 0.1, 0.9, 0.5); got != 0.1 {
		t.Fatalf("expected low bound 0.1, got %v", got)
	}
	if got := clamp(0.95, 0.1, 0.9, 0.5); got != 0.9 {
		t.Fatalf("expected high bound 0.9, got %v", got)
	}
	if got := clamp(0.6, 0.1, 0.9, 0.5); got != 0.6 {
		t.Fatalf("expected passthrough 0.6, got %v", got)
	}
}

func TestRewriteRTSPForAI(t *testing.T) {
	s := &Server{
		cfg: &config.Config{
			Server: config.ServerConfig{
				ZLM: config.ZLMConfig{
					PlayHost:    "172.16.200.41",
					AIInputHost: "zlm",
				},
			},
		},
	}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "replace 127.0.0.1",
			in:   "rtsp://127.0.0.1:1554/rtp/stream_1",
			want: "rtsp://zlm:1554/rtp/stream_1",
		},
		{
			name: "replace localhost with auth and query",
			in:   "rtsp://user:pass@localhost:1554/live/a?transport=tcp",
			want: "rtsp://user:pass@zlm:1554/live/a?transport=tcp",
		},
		{
			name: "replace host docker internal",
			in:   "rtsp://host.docker.internal:1554/live/b",
			want: "rtsp://zlm:1554/live/b",
		},
		{
			name: "replace configured play host",
			in:   "rtsp://172.16.200.41:1554/rtp/stream_2",
			want: "rtsp://zlm:1554/rtp/stream_2",
		},
		{
			name: "keep external host",
			in:   "rtsp://172.16.200.10:1554/live/external",
			want: "rtsp://172.16.200.10:1554/live/external",
		},
		{
			name: "keep non rtsp scheme",
			in:   "http://127.0.0.1:1554/live/external",
			want: "http://127.0.0.1:1554/live/external",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := s.rewriteRTSPForAI(tc.in)
			if got != tc.want {
				t.Fatalf("unexpected rewrite result: got=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestRewriteRTSPForAIKeepPlayHostWhenAIInputHostLoopback(t *testing.T) {
	s := &Server{
		cfg: &config.Config{
			Server: config.ServerConfig{
				ZLM: config.ZLMConfig{
					PlayHost:    "61.150.94.14",
					AIInputHost: "127.0.0.1",
				},
			},
		},
	}
	in := "rtsp://61.150.94.14:1554/rtp/34020000001320000012_34020000001320000001"
	got := s.rewriteRTSPForAI(in)
	if got != in {
		t.Fatalf("expected unchanged rtsp url, got=%s", got)
	}
}

func TestRewriteRTSPForAIEmptyInputHost(t *testing.T) {
	s := &Server{
		cfg: &config.Config{
			Server: config.ServerConfig{
				ZLM: config.ZLMConfig{
					PlayHost:    "172.16.200.41",
					AIInputHost: "",
				},
			},
		},
	}
	in := "rtsp://127.0.0.1:1554/rtp/stream_1"
	got := s.rewriteRTSPForAI(in)
	if got != in {
		t.Fatalf("expected unchanged rtsp url, got=%s", got)
	}
}

func TestPickDeviceRTSPURLForAI(t *testing.T) {
	tests := []struct {
		name   string
		device model.Device
		want   string
	}{
		{
			name: "prefer play rtsp url",
			device: model.Device{
				StreamURL:    "rtsp://origin.example.com:554/live/origin",
				PlayRTSPURL:  "rtsp://zlm:1554/live/play",
				OutputConfig: `{"rtsp":"rtsp://zlm:1554/live/output","rtsp_url":"rtsp://zlm:1554/live/output_alt"}`,
			},
			want: "rtsp://zlm:1554/live/play",
		},
		{
			name: "fallback to output rtsp",
			device: model.Device{
				StreamURL:    "rtsp://origin.example.com:554/live/origin",
				OutputConfig: `{"rtsp":"rtsp://zlm:1554/live/output","rtsp_url":"rtsp://zlm:1554/live/output_alt"}`,
			},
			want: "rtsp://zlm:1554/live/output",
		},
		{
			name: "fallback to output rtsp_url",
			device: model.Device{
				StreamURL:    "rtsp://origin.example.com:554/live/origin",
				OutputConfig: `{"rtsp_url":"rtsp://zlm:1554/live/output_alt"}`,
			},
			want: "rtsp://zlm:1554/live/output_alt",
		},
		{
			name: "fallback to stream url",
			device: model.Device{
				StreamURL: "rtsp://origin.example.com:554/live/origin",
			},
			want: "rtsp://origin.example.com:554/live/origin",
		},
		{
			name: "ignore invalid output config and fallback stream",
			device: model.Device{
				StreamURL:    "rtsp://origin.example.com:554/live/origin",
				OutputConfig: `{bad-json`,
			},
			want: "rtsp://origin.example.com:554/live/origin",
		},
		{
			name: "no valid rtsp input",
			device: model.Device{
				StreamURL:    "http://origin.example.com/live/origin",
				PlayRTSPURL:  "http://zlm/live/play",
				OutputConfig: `{"rtsp":"", "rtsp_url":"rtmp://zlm/live/output_alt"}`,
			},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pickDeviceRTSPURLForAI(tc.device)
			if got != tc.want {
				t.Fatalf("unexpected pick result: got=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestValidateTaskInputNormalizesDeviceConfig(t *testing.T) {
	s := newFocusedTestServer(t)

	device := model.Device{
		ID:              "dev-validate-task-1",
		Name:            "Validate Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_validate_task_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	algorithm := model.Algorithm{
		ID:              "alg-validate-task-1",
		Code:            "ALG_VALIDATE_TASK_1",
		Name:            "Validate Algorithm",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	task, profiles, algorithmByDevice, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-validate-normalize",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: device.ID,
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{
						AlgorithmID:  algorithm.ID,
						AlarmLevelID: builtinAlarmLevelID1,
					},
				},
				FrameRateMode:    "",
				FrameRateValue:   0,
				RecordingPolicy:  model.RecordingPolicyAlarmClip,
				AlarmPreSeconds:  0,
				AlarmPostSeconds: 9999,
			},
		},
	})
	if err != nil {
		t.Fatalf("validateTaskInput failed: %v", err)
	}
	if task.Name != "task-validate-normalize" {
		t.Fatalf("unexpected task name: %s", task.Name)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	profile := profiles[0]
	if profile.FrameInterval != 5 {
		t.Fatalf("expected default frame interval 5, got %d", profile.FrameInterval)
	}
	if profile.FrameRateMode != model.FrameRateModeInterval {
		t.Fatalf("expected default frame_rate_mode=interval, got %s", profile.FrameRateMode)
	}
	if profile.FrameRateValue != 5 {
		t.Fatalf("expected default frame_rate_value=5, got %d", profile.FrameRateValue)
	}
	if profile.SmallConfidence != 0.5 {
		t.Fatalf("expected default small confidence 0.5, got %v", profile.SmallConfidence)
	}
	if profile.LargeConfidence != 0.8 {
		t.Fatalf("expected default large confidence 0.8, got %v", profile.LargeConfidence)
	}
	if profile.SmallIOU != 0.8 {
		t.Fatalf("expected default iou 0.8, got %v", profile.SmallIOU)
	}
	if profile.RecordingPolicy != model.RecordingPolicyAlarmClip {
		t.Fatalf("expected recording_policy=alarm_clip, got %s", profile.RecordingPolicy)
	}
	if profile.AlarmPreSeconds != 8 {
		t.Fatalf("expected default recording_pre_seconds=8, got %d", profile.AlarmPreSeconds)
	}
	if profile.AlarmPostSeconds != 600 {
		t.Fatalf("expected clamped recording_post_seconds=600, got %d", profile.AlarmPostSeconds)
	}
	if len(algorithmByDevice[device.ID]) != 1 || algorithmByDevice[device.ID][0].AlgorithmID != algorithm.ID {
		t.Fatalf("unexpected algorithm ids by device: %+v", algorithmByDevice)
	}
	if algorithmByDevice[device.ID][0].AlertCycleSeconds != defaultAlertCycleSeconds {
		t.Fatalf("expected default alert_cycle_seconds=%d, got %d", defaultAlertCycleSeconds, algorithmByDevice[device.ID][0].AlertCycleSeconds)
	}
}

func TestValidateTaskInputRejectsContinuousRecordingPolicy(t *testing.T) {
	s := newFocusedTestServer(t)
	device := model.Device{
		ID:              "dev-task-policy-reject-continuous-1",
		Name:            "Policy Reject Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_task_policy_reject_continuous_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	algorithm := model.Algorithm{
		ID:              "alg-task-policy-reject-continuous-1",
		Code:            "ALG_TASK_POLICY_REJECT_CONTINUOUS_1",
		Name:            "Policy Reject Algorithm",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	_, _, _, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-policy-reject-continuous",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: device.ID,
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{AlgorithmID: algorithm.ID, AlarmLevelID: builtinAlarmLevelID1},
				},
				RecordingPolicy: model.RecordingPolicyContinuous,
			},
		},
	})
	if err == nil {
		t.Fatalf("expected continuous recording policy to be rejected")
	}
	if err.Error() != "invalid recording_policy for device "+device.ID+": recording_policy must be none/alarm_clip" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMigrateTaskRecordingPolicyContinuousToAlarmClip(t *testing.T) {
	s := newFocusedTestServer(t)
	row := model.VideoTaskDeviceProfile{
		TaskID:           "task-policy-migrate-1",
		DeviceID:         "device-policy-migrate-1",
		FrameInterval:    5,
		FrameRateMode:    model.FrameRateModeFPS,
		FrameRateValue:   5,
		SmallConfidence:  0.5,
		LargeConfidence:  0.8,
		SmallIOU:         0.8,
		AlarmLevelID:     builtinAlarmLevelID1,
		RecordingPolicy:  model.RecordingPolicyContinuous,
		AlarmPreSeconds:  -2,
		AlarmPostSeconds: 900,
	}
	if err := s.db.Create(&row).Error; err != nil {
		t.Fatalf("create profile failed: %v", err)
	}
	if err := s.migrateTaskRecordingPolicyContinuousToAlarmClip(); err != nil {
		t.Fatalf("run migrateTaskRecordingPolicyContinuousToAlarmClip failed: %v", err)
	}
	var updated model.VideoTaskDeviceProfile
	if err := s.db.Where("task_id = ? AND device_id = ?", row.TaskID, row.DeviceID).First(&updated).Error; err != nil {
		t.Fatalf("query migrated profile failed: %v", err)
	}
	if updated.RecordingPolicy != model.RecordingPolicyAlarmClip {
		t.Fatalf("expected recording_policy=%s, got=%s", model.RecordingPolicyAlarmClip, updated.RecordingPolicy)
	}
	if updated.AlarmPreSeconds != 1 {
		t.Fatalf("expected alarm_pre_seconds=1, got=%d", updated.AlarmPreSeconds)
	}
	if updated.AlarmPostSeconds != 600 {
		t.Fatalf("expected alarm_post_seconds=600, got=%d", updated.AlarmPostSeconds)
	}
}

func TestValidateTaskInputRejectsDuplicateDeviceConfig(t *testing.T) {
	s := newFocusedTestServer(t)
	_, _, _, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-duplicate-device",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: "dev-1",
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{AlgorithmID: "alg-1", AlarmLevelID: builtinAlarmLevelID1},
				},
			},
			{
				DeviceID: "dev-1",
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{AlgorithmID: "alg-2", AlarmLevelID: builtinAlarmLevelID1},
				},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected duplicate device config validation error")
	}
	if err.Error() != "duplicate device id is not allowed: dev-1" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskInputRejectsInvalidFrameRateMode(t *testing.T) {
	s := newFocusedTestServer(t)
	_, _, _, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-invalid-frame-rate-mode",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: "dev-1",
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{AlgorithmID: "alg-1", AlarmLevelID: builtinAlarmLevelID1},
				},
				FrameRateMode:  "bad_mode",
				FrameRateValue: 5,
			},
		},
	})
	if err == nil {
		t.Fatalf("expected invalid frame rate mode validation error")
	}
	if err.Error() != "invalid frame rate for device dev-1: frame_rate_mode must be one of: interval/fps" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskInputRejectsInvalidFrameRateValue(t *testing.T) {
	s := newFocusedTestServer(t)
	_, _, _, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-invalid-frame-rate-value",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: "dev-1",
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{AlgorithmID: "alg-1", AlarmLevelID: builtinAlarmLevelID1},
				},
				FrameRateMode:  model.FrameRateModeFPS,
				FrameRateValue: 61,
			},
		},
	})
	if err == nil {
		t.Fatalf("expected invalid frame rate value validation error")
	}
	if err.Error() != "invalid frame rate for device dev-1: frame_rate_value must be in range 1..60" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskInputAcceptsIntervalFrameRate(t *testing.T) {
	s := newFocusedTestServer(t)
	device := model.Device{
		ID:              "dev-interval-frame-1",
		Name:            "Interval Frame Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_interval_frame_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	algorithm := model.Algorithm{
		ID:              "alg-interval-frame-1",
		Code:            "ALG_INTERVAL_FRAME_1",
		Name:            "Interval Frame Algorithm",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	_, profiles, _, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-valid-interval-frame-rate",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: device.ID,
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{AlgorithmID: algorithm.ID, AlarmLevelID: builtinAlarmLevelID1},
				},
				FrameRateMode:  model.FrameRateModeInterval,
				FrameRateValue: 5,
			},
		},
	})
	if err != nil {
		t.Fatalf("expected interval frame rate to pass validation, got error: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected one profile, got %d", len(profiles))
	}
	if profiles[0].FrameRateMode != model.FrameRateModeInterval {
		t.Fatalf("expected frame_rate_mode=interval, got %s", profiles[0].FrameRateMode)
	}
	if profiles[0].FrameRateValue != 5 {
		t.Fatalf("expected frame_rate_value=5, got %d", profiles[0].FrameRateValue)
	}
}

func TestValidateTaskInputAcceptsAlertCycleZero(t *testing.T) {
	s := newFocusedTestServer(t)
	device := model.Device{
		ID:              "dev-alert-cycle-zero-1",
		Name:            "Alert Cycle Zero Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_alert_cycle_zero_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	algorithm := model.Algorithm{
		ID:              "alg-alert-cycle-zero-1",
		Code:            "ALG_ALERT_CYCLE_ZERO_1",
		Name:            "Alert Cycle Zero Algorithm",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	cycle := 0
	_, _, algorithmByDevice, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-valid-alert-cycle-zero",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: device.ID,
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{
						AlgorithmID:       algorithm.ID,
						AlarmLevelID:      builtinAlarmLevelID1,
						AlertCycleSeconds: &cycle,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected alert_cycle_seconds=0 to pass validation, got error: %v", err)
	}
	if len(algorithmByDevice[device.ID]) != 1 {
		t.Fatalf("expected one algorithm config, got %d", len(algorithmByDevice[device.ID]))
	}
	if algorithmByDevice[device.ID][0].AlertCycleSeconds != 0 {
		t.Fatalf("expected alert_cycle_seconds=0, got %d", algorithmByDevice[device.ID][0].AlertCycleSeconds)
	}
}

func TestValidateTaskInputRejectsInvalidAlertCycle(t *testing.T) {
	s := newFocusedTestServer(t)
	cycle := 86401
	_, _, _, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-invalid-alert-cycle",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: "dev-1",
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{
						AlgorithmID:       "alg-1",
						AlarmLevelID:      builtinAlarmLevelID1,
						AlertCycleSeconds: &cycle,
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected invalid alert cycle validation error")
	}
	if err.Error() != "invalid alert_cycle_seconds for device dev-1 algorithm alg-1: alert_cycle_seconds must be in range 0..86400" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskInputDefaultsMissingAlgorithmAlarmLevel(t *testing.T) {
	s := newFocusedTestServer(t)
	device := model.Device{
		ID:              "dev-task-default-alarm-level-1",
		Name:            "Default Alarm Level Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_task_default_alarm_level_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	algorithm := model.Algorithm{
		ID:              "alg-task-default-alarm-level-1",
		Code:            "ALG_TASK_DEFAULT_ALARM_LEVEL_1",
		Name:            "Default Alarm Level Algorithm",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	_, _, algorithmByDevice, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-missing-algorithm-alarm-level",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: device.ID,
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{
						AlgorithmID: algorithm.ID,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected missing alarm_level_id to fallback, got error: %v", err)
	}
	if algorithmByDevice[device.ID][0].AlarmLevelID != builtinAlarmLevelID1 {
		t.Fatalf("expected default alarm level %s, got %s", builtinAlarmLevelID1, algorithmByDevice[device.ID][0].AlarmLevelID)
	}
}

func TestTaskDefaultsPayloadUsesConfiguredValues(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.EnabledDefault = true
	s.cfg.Server.Recording.AlarmClip.PreSeconds = 11
	s.cfg.Server.Recording.AlarmClip.PostSeconds = 13
	s.cfg.Server.TaskDefaults.Video = config.VideoTaskDefaultsConfig{
		AlertCycleSecondsDefault: 15,
		AlarmLevelIDDefault:      builtinAlarmLevelID2,
		FrameRateModes:           []string{model.FrameRateModeFPS},
		FrameRateModeDefault:     model.FrameRateModeFPS,
		FrameRateValueDefault:    3,
	}
	got := s.taskDefaultsPayload()
	if got.RecordingPolicyDefault != model.RecordingPolicyAlarmClip {
		t.Fatalf("expected recording policy default alarm_clip, got %s", got.RecordingPolicyDefault)
	}
	if !got.AlarmClipEnabledDefault || got.RecordingPreSecondsDefault != 11 || got.RecordingPostSecondsDefault != 13 {
		t.Fatalf("unexpected alarm clip defaults: %+v", got)
	}
	if got.AlertCycleSecondsDefault != 15 {
		t.Fatalf("expected alert cycle default 15, got %d", got.AlertCycleSecondsDefault)
	}
	if got.AlarmLevelIDDefault != builtinAlarmLevelID2 {
		t.Fatalf("expected default alarm level %s, got %s", builtinAlarmLevelID2, got.AlarmLevelIDDefault)
	}
	if len(got.FrameRateModes) != 1 || got.FrameRateModes[0] != model.FrameRateModeFPS {
		t.Fatalf("unexpected frame rate modes: %+v", got.FrameRateModes)
	}
	if got.FrameRateModeDefault != model.FrameRateModeFPS || got.FrameRateValueDefault != 3 {
		t.Fatalf("unexpected frame rate defaults: mode=%s value=%d", got.FrameRateModeDefault, got.FrameRateValueDefault)
	}
}

func TestValidateTaskInputUsesConfiguredTaskDefaults(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.EnabledDefault = true
	s.cfg.Server.Recording.AlarmClip.PreSeconds = 11
	s.cfg.Server.Recording.AlarmClip.PostSeconds = 13
	s.cfg.Server.TaskDefaults.Video = config.VideoTaskDefaultsConfig{
		AlertCycleSecondsDefault: 15,
		AlarmLevelIDDefault:      builtinAlarmLevelID2,
		FrameRateModes:           []string{model.FrameRateModeFPS},
		FrameRateModeDefault:     model.FrameRateModeFPS,
		FrameRateValueDefault:    3,
	}
	device := model.Device{
		ID:              "dev-configured-defaults-1",
		Name:            "Configured Defaults Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_configured_defaults_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	algorithm := model.Algorithm{
		ID:              "alg-configured-defaults-1",
		Code:            "ALG_CONFIGURED_DEFAULTS_1",
		Name:            "Configured Defaults Algorithm",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	_, profiles, algorithmByDevice, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-configured-defaults",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: device.ID,
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{
						AlgorithmID: algorithm.ID,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("validateTaskInput failed: %v", err)
	}
	if profiles[0].FrameRateMode != model.FrameRateModeFPS || profiles[0].FrameRateValue != 3 {
		t.Fatalf("expected configured frame rate defaults, got mode=%s value=%d", profiles[0].FrameRateMode, profiles[0].FrameRateValue)
	}
	if profiles[0].RecordingPolicy != model.RecordingPolicyAlarmClip {
		t.Fatalf("expected configured recording policy default alarm_clip, got %s", profiles[0].RecordingPolicy)
	}
	if profiles[0].AlarmPreSeconds != 11 || profiles[0].AlarmPostSeconds != 13 {
		t.Fatalf("expected configured alarm clip defaults, got pre=%d post=%d", profiles[0].AlarmPreSeconds, profiles[0].AlarmPostSeconds)
	}
	if profiles[0].AlarmLevelID != builtinAlarmLevelID2 {
		t.Fatalf("expected configured default alarm level %s, got %s", builtinAlarmLevelID2, profiles[0].AlarmLevelID)
	}
	if algorithmByDevice[device.ID][0].AlarmLevelID != builtinAlarmLevelID2 {
		t.Fatalf("expected configured algorithm alarm level %s, got %s", builtinAlarmLevelID2, algorithmByDevice[device.ID][0].AlarmLevelID)
	}
	if algorithmByDevice[device.ID][0].AlertCycleSeconds != 15 {
		t.Fatalf("expected configured alert cycle default 15, got %d", algorithmByDevice[device.ID][0].AlertCycleSeconds)
	}
}

func TestValidateTaskInputRejectsConfiguredDisallowedFrameRateMode(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.TaskDefaults.Video = config.VideoTaskDefaultsConfig{
		AlertCycleSecondsDefault: 60,
		AlarmLevelIDDefault:      builtinAlarmLevelID1,
		FrameRateModes:           []string{model.FrameRateModeInterval},
		FrameRateModeDefault:     model.FrameRateModeInterval,
		FrameRateValueDefault:    5,
	}
	_, _, _, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-disallowed-frame-rate-mode",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: "dev-1",
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{AlgorithmID: "alg-1", AlarmLevelID: builtinAlarmLevelID1},
				},
				FrameRateMode:  model.FrameRateModeFPS,
				FrameRateValue: 5,
			},
		},
	})
	if err == nil {
		t.Fatalf("expected configured disallowed frame rate mode validation error")
	}
	if err.Error() != "invalid frame rate for device dev-1: frame_rate_mode must be one of: interval" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskInputRejectsInvalidAlgorithmAlarmLevel(t *testing.T) {
	s := newFocusedTestServer(t)
	_, _, _, err := s.validateTaskInput("", taskUpsertRequest{
		Name: "task-invalid-algorithm-alarm-level",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: "dev-1",
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{
						AlgorithmID:  "alg-1",
						AlarmLevelID: "level-x",
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected invalid alarm_level_id validation error")
	}
	if err.Error() != "设备 dev-1 的算法 alg-1 报警等级无效" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskInputRejectsLegacyBuiltinAlarmLevelIDs(t *testing.T) {
	s := newFocusedTestServer(t)
	for _, levelID := range []string{"alarm_level_4", "alarm_level_5"} {
		_, _, _, err := s.validateTaskInput("", taskUpsertRequest{
			Name: "task-invalid-legacy-level-" + levelID,
			DeviceConfigs: []taskDeviceConfigUpsert{
				{
					DeviceID: "dev-1",
					AlgorithmConfigs: []taskAlgorithmConfigUpsert{
						{
							AlgorithmID:  "alg-1",
							AlarmLevelID: levelID,
						},
					},
				},
			},
		})
		if err == nil {
			t.Fatalf("expected invalid alarm_level_id validation error for %s", levelID)
		}
		if err.Error() != "设备 dev-1 的算法 alg-1 报警等级无效" {
			t.Fatalf("unexpected error for %s: %v", levelID, err)
		}
	}
}

func TestNormalizeBuiltinAlarmLevelsMigratesLegacyFiveToThree(t *testing.T) {
	s := newFocusedTestServer(t)

	seedLegacy := []struct {
		id          string
		name        string
		severity    int
		color       string
		description string
	}{
		{builtinAlarmLevelID1, "一级（低）", 1, "#52c41a", "低风险"},
		{builtinAlarmLevelID2, "二级（较低）", 2, "#73d13d", "较低风险"},
		{builtinAlarmLevelID3, "三级（中）", 3, "#faad14", "中风险"},
		{"alarm_level_4", "四级（较高）", 4, "#fa8c16", "较高风险"},
		{"alarm_level_5", "五级（高）", 5, "#ff4d4f", "高风险"},
	}
	for _, item := range seedLegacy {
		var count int64
		if err := s.db.Model(&model.AlarmLevel{}).Where("id = ?", item.id).Count(&count).Error; err != nil {
			t.Fatalf("count alarm level %s failed: %v", item.id, err)
		}
		if count == 0 {
			if err := s.db.Create(&model.AlarmLevel{
				ID:          item.id,
				Name:        item.name,
				Severity:    item.severity,
				Color:       item.color,
				Description: item.description,
			}).Error; err != nil {
				t.Fatalf("create alarm level %s failed: %v", item.id, err)
			}
			continue
		}
		if err := s.db.Model(&model.AlarmLevel{}).Where("id = ?", item.id).Updates(map[string]any{
			"name":        item.name,
			"severity":    item.severity,
			"color":       item.color,
			"description": item.description,
		}).Error; err != nil {
			t.Fatalf("update alarm level %s failed: %v", item.id, err)
		}
	}

	task := model.VideoTask{
		ID:              "task-legacy-level-migrate",
		Name:            "task-legacy-level-migrate",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    builtinAlarmLevelID2,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         "dev-legacy-level-migrate",
		FrameInterval:    5,
		FrameRateMode:    model.FrameRateModeFPS,
		FrameRateValue:   5,
		SmallConfidence:  0.5,
		LargeConfidence:  0.8,
		SmallIOU:         0.8,
		AlarmLevelID:     builtinAlarmLevelID3,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create profile failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:       task.ID,
		DeviceID:     "dev-legacy-level-migrate",
		AlgorithmID:  "alg-legacy-level-migrate",
		AlarmLevelID: "alarm_level_4",
	}).Error; err != nil {
		t.Fatalf("create task device algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlarmEvent{
		ID:             "evt-legacy-level-migrate",
		TaskID:         task.ID,
		DeviceID:       "dev-legacy-level-migrate",
		AlgorithmID:    "alg-legacy-level-migrate",
		AlarmLevelID:   "alarm_level_5",
		Status:         model.EventStatusPending,
		OccurredAt:     time.Now(),
		SourceCallback: "{}",
	}).Error; err != nil {
		t.Fatalf("create alarm event failed: %v", err)
	}

	if err := s.normalizeBuiltinAlarmLevels(); err != nil {
		t.Fatalf("normalize builtin alarm levels failed: %v", err)
	}

	var levels []model.AlarmLevel
	if err := s.db.Order("severity asc").Find(&levels).Error; err != nil {
		t.Fatalf("query alarm levels failed: %v", err)
	}
	if len(levels) != 3 {
		t.Fatalf("expected 3 builtin alarm levels, got %d", len(levels))
	}
	for idx, level := range levels {
		if level.ID != builtinAlarmLevelIDs()[idx] {
			t.Fatalf("unexpected level id order: got=%s want=%s", level.ID, builtinAlarmLevelIDs()[idx])
		}
	}
	if levels[0].Name != "低" || levels[1].Name != "中" || levels[2].Name != "高" {
		t.Fatalf("expected names [低 中 高], got [%s %s %s]", levels[0].Name, levels[1].Name, levels[2].Name)
	}

	var reloadedTask model.VideoTask
	if err := s.db.Where("id = ?", task.ID).First(&reloadedTask).Error; err != nil {
		t.Fatalf("reload task failed: %v", err)
	}
	if reloadedTask.AlarmLevelID != builtinAlarmLevelID1 {
		t.Fatalf("expected task level migrate to %s, got %s", builtinAlarmLevelID1, reloadedTask.AlarmLevelID)
	}

	var reloadedProfile model.VideoTaskDeviceProfile
	if err := s.db.Where("task_id = ? AND device_id = ?", task.ID, "dev-legacy-level-migrate").First(&reloadedProfile).Error; err != nil {
		t.Fatalf("reload profile failed: %v", err)
	}
	if reloadedProfile.AlarmLevelID != builtinAlarmLevelID2 {
		t.Fatalf("expected profile level migrate to %s, got %s", builtinAlarmLevelID2, reloadedProfile.AlarmLevelID)
	}

	var reloadedAlgo model.VideoTaskDeviceAlgorithm
	if err := s.db.Where("task_id = ? AND device_id = ? AND algorithm_id = ?", task.ID, "dev-legacy-level-migrate", "alg-legacy-level-migrate").First(&reloadedAlgo).Error; err != nil {
		t.Fatalf("reload device algorithm failed: %v", err)
	}
	if reloadedAlgo.AlarmLevelID != builtinAlarmLevelID3 {
		t.Fatalf("expected algorithm level migrate to %s, got %s", builtinAlarmLevelID3, reloadedAlgo.AlarmLevelID)
	}

	var reloadedEvent model.AlarmEvent
	if err := s.db.Where("id = ?", "evt-legacy-level-migrate").First(&reloadedEvent).Error; err != nil {
		t.Fatalf("reload alarm event failed: %v", err)
	}
	if reloadedEvent.AlarmLevelID != builtinAlarmLevelID3 {
		t.Fatalf("expected event level migrate to %s, got %s", builtinAlarmLevelID3, reloadedEvent.AlarmLevelID)
	}

	var legacyCount int64
	if err := s.db.Model(&model.AlarmLevel{}).Where("id IN ?", []string{"alarm_level_4", "alarm_level_5"}).Count(&legacyCount).Error; err != nil {
		t.Fatalf("count legacy levels failed: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("expected legacy levels removed, found %d", legacyCount)
	}
}
