package server

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gorm.io/gorm"
	"maas-box/internal/ai"
	"maas-box/internal/config"
	"maas-box/internal/model"
	"maas-box/internal/ws"
)

func newFocusedTestServer(t *testing.T) *Server {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Server: config.ServerConfig{
			Debug:    false,
			Username: "admin",
			Password: "admin",
			HTTP: config.HTTPConfig{
				Port:      15123,
				Timeout:   "1m",
				JwtSecret: "focused-test-jwt-secret",
			},
			AI: config.AIConfig{
				Disabled:                      true,
				RetainDays:                    7,
				ServiceURL:                    "http://127.0.0.1:50052",
				CallbackURL:                   "http://127.0.0.1:15123/ai",
				CallbackToken:                 "expected-callback-token",
				RequestTimeout:                "5s",
				AnalyzeImageFailureRetryCount: 1,
				LLMAPIURL:                     "https://llm.config.test/v1/chat/completions",
				LLMAPIKey:                     "config-test-key",
				LLMModel:                      "config-test-model",
			},
			ZLM: config.ZLMConfig{
				APIURL:   "http://127.0.0.1:11029",
				Secret:   "zlm-hook-secret",
				PlayHost: "127.0.0.1",
				HTTPPort: 11029,
				RTSPPort: 1554,
				RTMPPort: 11935,
				App:      "live",
				Output: config.ZLMOutputConfig{
					EnableWebRTC:  true,
					EnableWSFLV:   true,
					EnableHTTPFLV: false,
					EnableHLS:     false,
					WebFallback:   "ws_flv",
				},
			},
			Recording: config.RecordingConfig{
				Disabled:       false,
				StorageDir:     filepath.Join(tmp, "recordings"),
				RetainDays:     7,
				SegmentSeconds: 60,
				DiskThreshold:  95,
				AlarmClip: config.RecordingAlarmClipConfig{
					EnabledDefault: false,
					PreSeconds:     8,
					PostSeconds:    12,
				},
			},
			TaskDefaults: config.TaskDefaultsConfig{
				Video: config.VideoTaskDefaultsConfig{
					AlertCycleSecondsDefault: 60,
					AlarmLevelIDDefault:      "alarm_level_1",
					FrameRateModes:           []string{"interval", "fps"},
					FrameRateModeDefault:     "interval",
					FrameRateValueDefault:    5,
				},
			},
		},
		Data: config.DataConfig{
			Database: config.DatabaseConfig{Dsn: filepath.Join(tmp, "test.db")},
		},
		Log: config.LogConfig{
			Dir:   filepath.Join(tmp, "logs"),
			Level: "warn",
		},
	}
	db, err := openDB(cfg)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db failed: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	timeout := 5 * time.Second
	s := &Server{
		cfg:               cfg,
		db:                db,
		aiClient:          ai.NewClient(cfg.Server.AI.ServiceURL, timeout),
		wsHub:             ws.NewHub(),
		jwtSecret:         []byte(cfg.Server.HTTP.JwtSecret),
		camera2PatrolJobs: make(map[string]*camera2PatrolJob),
	}
	if err := s.autoMigrate(); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
	if err := s.seedDefaults(); err != nil {
		t.Fatalf("seed defaults failed: %v", err)
	}
	return s
}

func withWorkingDir(t *testing.T, dir string, run func()) {
	t.Helper()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to %s failed: %v", dir, err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()
	run()
}

func writePromptMarkdownFiles(t *testing.T, rootDir, roleText, requirementText string) {
	t.Helper()
	llmDir := filepath.Join(rootDir, "configs", "llm")
	if err := os.MkdirAll(llmDir, 0o755); err != nil {
		t.Fatalf("mkdir llm dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(llmDir, "llm_role.md"), []byte(roleText), 0o644); err != nil {
		t.Fatalf("write llm_role.md failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(llmDir, "llm_output_requirement.md"), []byte(requirementText), 0o644); err != nil {
		t.Fatalf("write llm_output_requirement.md failed: %v", err)
	}
}

func loginToken(t *testing.T, engine http.Handler, username, password string) string {
	t.Helper()
	payload := map[string]string{
		"username": username,
		"password": password,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login response failed: %v", err)
	}
	if resp.Code != 0 || strings.TrimSpace(resp.Data.Token) == "" {
		t.Fatalf("unexpected login response: %s", rec.Body.String())
	}
	return resp.Data.Token
}

func TestCallbackTokenRejectionIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	req := httptest.NewRequest(http.MethodPost, "/ai/events", strings.NewReader(`{"camera_id":"dev-1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "wrong-token")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid ai callback token") {
		t.Fatalf("expected token rejection message, got: %s", rec.Body.String())
	}
}

func TestSystemSettingsAPIRemovedIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	token := loginToken(t, engine, "admin", "admin")

	payload := map[string]any{
		"llm_role":               "ROLE_TEST_ONLY",
		"llm_output_requirement": "OUTPUT_TEST_ONLY",
		"ai_callback_token":      "callback-token-updated",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("system settings PUT should be removed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRec := httptest.NewRecorder()
	engine.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("system settings GET should be removed: status=%d body=%s", getRec.Code, getRec.Body.String())
	}
}

func TestSystemSettingsSeedFromMarkdownIntegration(t *testing.T) {
	root := t.TempDir()
	roleText := "ROLE_FROM_MD"
	requirementText := "OUTPUT_REQ_FROM_MD"
	writePromptMarkdownFiles(t, root, roleText, requirementText)

	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		if got := s.getSetting("llm_role"); got != roleText {
			t.Fatalf("unexpected llm_role from markdown: got=%q want=%q", got, roleText)
		}
		if got := s.getSetting("llm_output_requirement"); got != requirementText {
			t.Fatalf("unexpected llm_output_requirement from markdown: got=%q want=%q", got, requirementText)
		}
	})
}

func TestSystemSettingsSeedOverridesOnEveryStartIntegration(t *testing.T) {
	root := t.TempDir()
	roleText := "ROLE_FROM_MD_AGAIN"
	requirementText := "OUTPUT_REQ_FROM_MD_AGAIN"
	writePromptMarkdownFiles(t, root, roleText, requirementText)

	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		if err := s.upsertSetting("llm_role", "ROLE_MANUAL_CHANGED"); err != nil {
			t.Fatalf("manual llm_role update failed: %v", err)
		}
		if err := s.upsertSetting("llm_output_requirement", "OUTPUT_MANUAL_CHANGED"); err != nil {
			t.Fatalf("manual llm_output_requirement update failed: %v", err)
		}
		if got := s.getSetting("llm_role"); got != "ROLE_MANUAL_CHANGED" {
			t.Fatalf("manual llm_role update failed: got=%q", got)
		}
		if got := s.getSetting("llm_output_requirement"); got != "OUTPUT_MANUAL_CHANGED" {
			t.Fatalf("manual llm_output_requirement update failed: got=%q", got)
		}

		if err := s.seedDefaults(); err != nil {
			t.Fatalf("seedDefaults failed: %v", err)
		}
		if got := s.getSetting("llm_role"); got != roleText {
			t.Fatalf("llm_role should be overwritten by markdown each start: got=%q want=%q", got, roleText)
		}
		if got := s.getSetting("llm_output_requirement"); got != requirementText {
			t.Fatalf("llm_output_requirement should be overwritten by markdown each start: got=%q want=%q", got, requirementText)
		}
	})
}

func TestSystemSettingsSeedMissingMarkdownWritesEmptyIntegration(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "configs"), 0o755); err != nil {
		t.Fatalf("mkdir configs failed: %v", err)
	}

	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		if got := s.getSetting("llm_role"); got != "" {
			t.Fatalf("expected empty llm_role when markdown missing, got=%q", got)
		}
		if got := s.getSetting("llm_output_requirement"); got != "" {
			t.Fatalf("expected empty llm_output_requirement when markdown missing, got=%q", got)
		}
	})
}

func TestPromptMergePreviewIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-prompt-1",
		Name:            "Prompt Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_prompt_1",
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

	algSmall := model.Algorithm{
		ID:              "alg-small-1",
		Code:            "ALG_SMALL_1",
		Name:            "People Check",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person,car",
	}
	algLarge := model.Algorithm{
		ID:         "alg-large-1",
		Code:       "ALG_LARGE_1",
		Name:       "Intrusion Check",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algSmall).Error; err != nil {
		t.Fatalf("create small algorithm failed: %v", err)
	}
	if err := s.db.Create(&algLarge).Error; err != nil {
		t.Fatalf("create large algorithm failed: %v", err)
	}
	prompt := model.AlgorithmPromptVersion{
		ID:          "prompt-large-1",
		AlgorithmID: algLarge.ID,
		Version:     "v1",
		Prompt:      "Detect human intrusion in forbidden zone.",
		IsActive:    true,
	}
	if err := s.db.Create(&prompt).Error; err != nil {
		t.Fatalf("create prompt failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	task := model.VideoTask{
		ID:              "task-prompt-1",
		Name:            "task-prompt-preview",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    task.FrameInterval,
		SmallConfidence:  task.SmallConfidence,
		LargeConfidence:  task.LargeConfidence,
		SmallIOU:         task.SmallIOU,
		AlarmLevelID:     level.ID,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create task-device profile failed: %v", err)
	}
	if err := s.db.Create([]model.VideoTaskDeviceAlgorithm{
		{TaskID: task.ID, DeviceID: device.ID, AlgorithmID: algSmall.ID, AlarmLevelID: builtinAlarmLevelID1},
		{TaskID: task.ID, DeviceID: device.ID, AlgorithmID: algLarge.ID, AlarmLevelID: builtinAlarmLevelID1},
	}).Error; err != nil {
		t.Fatalf("create task-device algorithm relation failed: %v", err)
	}
	if err := s.upsertSetting("llm_output_requirement", ""); err != nil {
		t.Fatalf("clear llm_output_requirement failed: %v", err)
	}
	if err := s.upsertSetting("llm_return_suffix", "LEGACY_SUFFIX_SHOULD_NOT_APPEAR"); err != nil {
		t.Fatalf("set legacy llm_return_suffix failed: %v", err)
	}
	if err := s.upsertSetting("llm_color_correction", "LEGACY_COLOR_SHOULD_NOT_APPEAR"); err != nil {
		t.Fatalf("set legacy llm_color_correction failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+task.ID+"/prompt-preview", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("prompt preview failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				Labels      []string `json:"labels"`
				Prompt      string   `json:"prompt"`
				PromptTasks []struct {
					TaskCode string `json:"task_code"`
					TaskName string `json:"task_name"`
					TaskMode string `json:"task_mode"`
					Goal     string `json:"goal"`
				} `json:"prompt_tasks"`
				Provider struct {
					ID string `json:"id"`
				} `json:"provider"`
				AlgorithmConfigs []struct {
					AlgorithmID   string `json:"algorithm_id"`
					AlgorithmCode string `json:"algorithm_code"`
					DetectMode    int    `json:"detect_mode"`
				} `json:"algorithm_configs"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode prompt preview response failed: %v", err)
	}
	if resp.Code != 0 || len(resp.Data.Items) != 1 {
		t.Fatalf("unexpected prompt preview payload: %s", rec.Body.String())
	}

	item := resp.Data.Items[0]
	if len(item.Labels) != 2 || item.Labels[0] != "person" || item.Labels[1] != "car" {
		t.Fatalf("unexpected labels: %#v", item.Labels)
	}
	if item.Provider.ID != configLLMProviderID {
		t.Fatalf("unexpected provider id: %s", item.Provider.ID)
	}
	if !strings.Contains(item.Prompt, "\"task_code\"") {
		t.Fatalf("expected merged prompt with task_code schema, got: %s", item.Prompt)
	}
	if !strings.Contains(item.Prompt, algLarge.Code) {
		t.Fatalf("expected merged prompt includes large algorithm code, got: %s", item.Prompt)
	}
	if strings.Contains(item.Prompt, "LEGACY_SUFFIX_SHOULD_NOT_APPEAR") || strings.Contains(item.Prompt, "LEGACY_COLOR_SHOULD_NOT_APPEAR") {
		t.Fatalf("prompt should not include legacy llm settings, got: %s", item.Prompt)
	}
	if len(item.PromptTasks) != 1 {
		t.Fatalf("expected one prompt task, got %d", len(item.PromptTasks))
	}
	if item.PromptTasks[0].TaskCode != algLarge.Code || item.PromptTasks[0].TaskName != algLarge.Name || item.PromptTasks[0].TaskMode != "object" {
		t.Fatalf("unexpected prompt task: %+v", item.PromptTasks[0])
	}
	if len(item.AlgorithmConfigs) != 2 {
		t.Fatalf("expected two algorithm configs, got %d", len(item.AlgorithmConfigs))
	}
	var hasSmallCode bool
	var hasLargeCode bool
	for _, cfg := range item.AlgorithmConfigs {
		if cfg.AlgorithmID == algSmall.ID && cfg.AlgorithmCode == algSmall.Code && cfg.DetectMode == model.AlgorithmDetectModeSmallOnly {
			hasSmallCode = true
		}
		if cfg.AlgorithmID == algLarge.ID && cfg.AlgorithmCode == algLarge.Code && cfg.DetectMode == model.AlgorithmDetectModeLLMOnly {
			hasLargeCode = true
		}
	}
	if !hasSmallCode || !hasLargeCode {
		t.Fatalf("expected algorithm configs with codes, got: %+v", item.AlgorithmConfigs)
	}
}

func TestDeviceConflictConstraintIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-conflict-1",
		Name:            "Conflict Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_conflict_1",
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
		ID:              "alg-conflict-1",
		Name:            "Conflict Algorithm",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	usedTask := model.VideoTask{
		ID:              "task-used-1",
		Name:            "task-used-device",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&usedTask).Error; err != nil {
		t.Fatalf("create used task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           usedTask.ID,
		DeviceID:         device.ID,
		FrameInterval:    usedTask.FrameInterval,
		SmallConfidence:  usedTask.SmallConfidence,
		LargeConfidence:  usedTask.LargeConfidence,
		SmallIOU:         usedTask.SmallIOU,
		AlarmLevelID:     level.ID,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create used task-device profile failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	payload := map[string]any{
		"name": "task-conflict-check",
		"device_configs": []map[string]any{
			{
				"device_id":              device.ID,
				"algorithm_configs":      []map[string]any{{"algorithm_id": algorithm.ID, "alarm_level_id": builtinAlarmLevelID1, "alert_cycle_seconds": 60}},
				"frame_rate_mode":        "fps",
				"frame_rate_value":       5,
				"small_confidence":       0.5,
				"large_confidence":       0.8,
				"small_iou":              0.8,
				"recording_policy":       "none",
				"recording_pre_seconds":  8,
				"recording_post_seconds": 12,
			},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on device conflict, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "devices already used by another task") {
		t.Fatalf("expected conflict message, got: %s", rec.Body.String())
	}
}

func TestAlgorithmTestUsesUploadedImageForRecordIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	uploadedImageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	snapshotImageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII="
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST for analyze_image, got %s", r.Method)
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/api/analyze_image" {
			t.Errorf("expected /api/analyze_image, got %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var in ai.AnalyzeImageRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("decode ai request failed: %v", err)
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(in.ImageRelPath) == "" {
			t.Errorf("expected uploaded image relative path to be forwarded")
			http.Error(w, "bad image", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success: true,
			Message: "ok",
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)
	engine := s.Engine()

	algorithm := model.Algorithm{
		ID:              "alg-test-image-1",
		Name:            "Test Image Source",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	body, _ := json.Marshal(map[string]any{
		"image_base64": uploadedImageBase64,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("test algorithm failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Record struct {
				ImagePath string `json:"image_path"`
			} `json:"record"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode test response failed: %v", err)
	}
	if resp.Code != 0 || strings.TrimSpace(resp.Data.Record.ImagePath) == "" {
		t.Fatalf("unexpected test response: %s", rec.Body.String())
	}

	uploadedBytes, err := base64.StdEncoding.DecodeString(uploadedImageBase64)
	if err != nil {
		t.Fatalf("decode uploaded image failed: %v", err)
	}
	snapshotBytes, err := base64.StdEncoding.DecodeString(snapshotImageBase64)
	if err != nil {
		t.Fatalf("decode snapshot image failed: %v", err)
	}

	imagePath := filepath.Join(algorithmTestMediaRootDir, filepath.FromSlash(resp.Data.Record.ImagePath))
	savedBytes, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatalf("read saved test image failed: %v", err)
	}
	_ = os.Remove(imagePath)

	if !bytes.Equal(savedBytes, uploadedBytes) {
		t.Fatalf("saved image should match uploaded image")
	}
	if bytes.Equal(savedBytes, snapshotBytes) {
		t.Fatalf("saved image should not use ai snapshot")
	}

	var savedRecord model.AlgorithmTestRecord
	if err := s.db.Where("algorithm_id = ?", algorithm.ID).Order("created_at desc").First(&savedRecord).Error; err != nil {
		t.Fatalf("query algorithm test record failed: %v", err)
	}
	if strings.Contains(savedRecord.RequestPayload, "\"image_base64\"") {
		t.Fatalf("request_payload should not contain image_base64, got=%s", savedRecord.RequestPayload)
	}
	if strings.Contains(savedRecord.ResponsePayload, "\"snapshot\"") {
		t.Fatalf("response_payload should not contain snapshot, got=%s", savedRecord.ResponsePayload)
	}
	var reqPayload map[string]any
	if err := json.Unmarshal([]byte(savedRecord.RequestPayload), &reqPayload); err != nil {
		t.Fatalf("decode request_payload failed: %v", err)
	}
	if _, ok := reqPayload["algorithm_configs"]; !ok {
		t.Fatalf("request_payload should keep algorithm_configs, got=%s", savedRecord.RequestPayload)
	}
	var respPayload map[string]any
	if err := json.Unmarshal([]byte(savedRecord.ResponsePayload), &respPayload); err != nil {
		t.Fatalf("decode response_payload failed: %v", err)
	}
	if _, ok := respPayload["snapshot_width"]; ok {
		t.Fatalf("response_payload should remove snapshot_width, got=%s", savedRecord.ResponsePayload)
	}
	if _, ok := respPayload["snapshot_height"]; ok {
		t.Fatalf("response_payload should remove snapshot_height, got=%s", savedRecord.ResponsePayload)
	}
}

func TestAlgorithmTestLLMPromptUsesUnifiedTemplateIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	capturedPrompt := ""

	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		var in ai.AnalyzeImageRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		capturedPrompt = in.LLMPrompt
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success:   true,
			Message:   "ok",
			LLMResult: `{"overall":{"alarm":"0","alarm_task_codes":[]},"task_results":[],"objects":[]}`,
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)
	engine := s.Engine()

	if err := s.upsertSetting("llm_role", "ROLE_FOR_TEST_ONLY"); err != nil {
		t.Fatalf("set llm_role failed: %v", err)
	}
	if err := s.upsertSetting("llm_output_requirement", "OUTPUT_REQ_FOR_TEST_ONLY"); err != nil {
		t.Fatalf("set llm_output_requirement failed: %v", err)
	}
	if err := s.upsertSetting("llm_return_suffix", "LEGACY_SUFFIX_SHOULD_NOT_APPEAR"); err != nil {
		t.Fatalf("set legacy llm_return_suffix failed: %v", err)
	}
	if err := s.upsertSetting("llm_color_correction", "LEGACY_COLOR_SHOULD_NOT_APPEAR"); err != nil {
		t.Fatalf("set legacy llm_color_correction failed: %v", err)
	}

	algorithm := model.Algorithm{
		ID:         "alg-test-llm-1",
		Code:       "ALG_TEST_LLM_1",
		Name:       "Unified Prompt Algorithm",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	prompt := model.AlgorithmPromptVersion{
		ID:          "prompt-alg-test-llm-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "Detect unauthorized person in restricted area.",
		IsActive:    true,
	}
	if err := s.db.Create(&prompt).Error; err != nil {
		t.Fatalf("create prompt failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	body, _ := json.Marshal(map[string]any{"image_base64": imageBase64})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("test algorithm failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(capturedPrompt, "## [当前测试任务]") {
		t.Fatalf("llm_prompt should contain single-task section, got: %s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "任务名称：Unified Prompt Algorithm") {
		t.Fatalf("llm_prompt should contain task_name, got: %s", capturedPrompt)
	}
	if strings.Contains(capturedPrompt, "\"task_mode\"") {
		t.Fatalf("llm_prompt should not contain task_mode, got: %s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "Detect unauthorized person in restricted area.") {
		t.Fatalf("llm_prompt should contain active prompt goal, got: %s", capturedPrompt)
	}
	if strings.Contains(capturedPrompt, "## [任务清单]") {
		t.Fatalf("llm_prompt should not contain multi-task section, got: %s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "图片识别测试助手") {
		t.Fatalf("llm_prompt should contain image test role prompt, got: %s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "`bbox2d`") {
		t.Fatalf("llm_prompt should contain image output requirement, got: %s", capturedPrompt)
	}
	if strings.Contains(capturedPrompt, "LEGACY_SUFFIX_SHOULD_NOT_APPEAR") || strings.Contains(capturedPrompt, "LEGACY_COLOR_SHOULD_NOT_APPEAR") {
		t.Fatalf("llm_prompt should not include legacy settings, got: %s", capturedPrompt)
	}
}

func TestAlgorithmTestPersistsLLMUsageAndUsageAPIIntegration(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("UTC+8", 8*3600)
	t.Cleanup(func() {
		time.Local = oldLocal
	})

	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	promptTokens := 120
	completionTokens := 45
	totalTokens := 165

	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		var in ai.AnalyzeImageRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success:   true,
			Message:   "ok",
			LLMResult: `{"overall":{"alarm":"0","alarm_task_codes":[]},"task_results":[],"objects":[]}`,
			LLMUsage: &ai.LLMUsage{
				CallID:           "call-usage-test-1",
				CallStatus:       model.LLMUsageStatusSuccess,
				UsageAvailable:   true,
				PromptTokens:     &promptTokens,
				CompletionTokens: &completionTokens,
				TotalTokens:      &totalTokens,
				LatencyMS:        345.6,
				Model:            "qwen-test-model",
				ErrorMessage:     "",
				RequestContext:   "algorithm_test integration",
			},
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-llm-usage-1",
		Code:       "ALG_LLM_USAGE_1",
		Name:       "LLM Usage Algorithm",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	prompt := model.AlgorithmPromptVersion{
		ID:          "prompt-llm-usage-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "test prompt",
		IsActive:    true,
	}
	if err := s.db.Create(&prompt).Error; err != nil {
		t.Fatalf("create prompt failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	body, _ := json.Marshal(map[string]any{"image_base64": imageBase64})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("test algorithm failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var usageCall model.LLMUsageCall
	if err := s.db.Where("id = ?", "call-usage-test-1").First(&usageCall).Error; err != nil {
		t.Fatalf("query llm usage call failed: %v", err)
	}
	if usageCall.Source != model.LLMUsageSourceAlgorithmTest {
		t.Fatalf("unexpected llm usage source: %s", usageCall.Source)
	}
	if usageCall.ProviderID != configLLMProviderID {
		t.Fatalf("unexpected provider id: %s", usageCall.ProviderID)
	}
	if usageCall.TotalTokens == nil || *usageCall.TotalTokens != totalTokens {
		t.Fatalf("unexpected total tokens: %+v", usageCall.TotalTokens)
	}

	type summaryPayload struct {
		Summary struct {
			CallCount         int64   `json:"call_count"`
			PromptTokens      int64   `json:"prompt_tokens"`
			CompletionTokens  int64   `json:"completion_tokens"`
			TotalTokens       int64   `json:"total_tokens"`
			UsageMissingCount int64   `json:"usage_missing_count"`
			AverageTotal      float64 `json:"avg_total_tokens_per_call"`
		} `json:"summary"`
	}

	type hourlyPayload struct {
		Items []struct {
			CallCount         int64 `json:"call_count"`
			PromptTokens      int64 `json:"prompt_tokens"`
			CompletionTokens  int64 `json:"completion_tokens"`
			TotalTokens       int64 `json:"total_tokens"`
			UsageMissingCount int64 `json:"usage_missing_count"`
		} `json:"items"`
	}

	type callsPayload struct {
		Items []struct {
			ID             string `json:"id"`
			Source         string `json:"source"`
			ProviderID     string `json:"provider_id"`
			Model          string `json:"model"`
			CallStatus     string `json:"call_status"`
			UsageAvailable bool   `json:"usage_available"`
			TotalTokens    *int   `json:"total_tokens"`
		} `json:"items"`
		Total int64 `json:"total"`
	}

	assertAPI := func(path string, out any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %s failed: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		var resp struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s response failed: %v", path, err)
		}
		if resp.Code != 0 {
			t.Fatalf("unexpected %s response: %s", path, rec.Body.String())
		}
		if err := json.Unmarshal(resp.Data, out); err != nil {
			t.Fatalf("decode %s data failed: %v", path, err)
		}
	}

	var summaryResp summaryPayload
	assertAPI("/api/v1/llm-usage/summary", &summaryResp)
	if summaryResp.Summary.CallCount != 1 {
		t.Fatalf("unexpected summary call_count: %+v", summaryResp.Summary)
	}
	if summaryResp.Summary.PromptTokens != int64(promptTokens) || summaryResp.Summary.TotalTokens != int64(totalTokens) {
		t.Fatalf("unexpected summary tokens: %+v", summaryResp.Summary)
	}
	if summaryResp.Summary.UsageMissingCount != 0 {
		t.Fatalf("unexpected usage_missing_count: %+v", summaryResp.Summary)
	}

	var hourlyResp hourlyPayload
	assertAPI("/api/v1/llm-usage/hourly", &hourlyResp)
	if len(hourlyResp.Items) != 1 {
		t.Fatalf("expected 1 hourly bucket, got=%d", len(hourlyResp.Items))
	}
	if hourlyResp.Items[0].CallCount != 1 || hourlyResp.Items[0].TotalTokens != int64(totalTokens) {
		t.Fatalf("unexpected hourly payload: %+v", hourlyResp.Items[0])
	}

	var dailyResp hourlyPayload
	assertAPI("/api/v1/llm-usage/daily", &dailyResp)
	if len(dailyResp.Items) != 1 {
		t.Fatalf("expected 1 daily bucket, got=%d", len(dailyResp.Items))
	}
	if dailyResp.Items[0].CallCount != 1 || dailyResp.Items[0].PromptTokens != int64(promptTokens) {
		t.Fatalf("unexpected daily payload: %+v", dailyResp.Items[0])
	}

	var callsResp callsPayload
	assertAPI("/api/v1/llm-usage/calls?page=1&page_size=10", &callsResp)
	if callsResp.Total != 1 || len(callsResp.Items) != 1 {
		t.Fatalf("unexpected calls payload: %+v", callsResp)
	}
	if callsResp.Items[0].ID != "call-usage-test-1" {
		t.Fatalf("unexpected call id: %+v", callsResp.Items[0])
	}
	if callsResp.Items[0].Source != model.LLMUsageSourceAlgorithmTest || !callsResp.Items[0].UsageAvailable {
		t.Fatalf("unexpected call item: %+v", callsResp.Items[0])
	}
	if callsResp.Items[0].TotalTokens == nil || *callsResp.Items[0].TotalTokens != totalTokens {
		t.Fatalf("unexpected call total tokens: %+v", callsResp.Items[0].TotalTokens)
	}

	startAtRFC3339 := usageCall.OccurredAt.Add(-time.Minute).UTC().Format(time.RFC3339)
	endAtRFC3339 := usageCall.OccurredAt.Add(time.Minute).UTC().Format(time.RFC3339)
	windowQuery := "?start_at=" + url.QueryEscape(startAtRFC3339) + "&end_at=" + url.QueryEscape(endAtRFC3339)

	var summaryUTCResp summaryPayload
	assertAPI("/api/v1/llm-usage/summary"+windowQuery, &summaryUTCResp)
	if summaryUTCResp.Summary.CallCount != summaryResp.Summary.CallCount || summaryUTCResp.Summary.TotalTokens != summaryResp.Summary.TotalTokens {
		t.Fatalf("unexpected utc summary payload: %+v", summaryUTCResp.Summary)
	}

	var hourlyUTCResp hourlyPayload
	assertAPI("/api/v1/llm-usage/hourly"+windowQuery, &hourlyUTCResp)
	if len(hourlyUTCResp.Items) != 1 || hourlyUTCResp.Items[0].CallCount != 1 || hourlyUTCResp.Items[0].TotalTokens != int64(totalTokens) {
		t.Fatalf("unexpected utc hourly payload: %+v", hourlyUTCResp.Items)
	}

	var dailyUTCResp hourlyPayload
	assertAPI("/api/v1/llm-usage/daily"+windowQuery, &dailyUTCResp)
	if len(dailyUTCResp.Items) != 1 || dailyUTCResp.Items[0].CallCount != 1 || dailyUTCResp.Items[0].TotalTokens != int64(totalTokens) {
		t.Fatalf("unexpected utc daily payload: %+v", dailyUTCResp.Items)
	}

	var callsUTCResp callsPayload
	assertAPI("/api/v1/llm-usage/calls"+windowQuery+"&page=1&page_size=10", &callsUTCResp)
	if callsUTCResp.Total != 1 || len(callsUTCResp.Items) != 1 {
		t.Fatalf("unexpected utc calls payload: %+v", callsUTCResp)
	}
	if callsUTCResp.Items[0].ID != "call-usage-test-1" {
		t.Fatalf("unexpected utc call id: %+v", callsUTCResp.Items[0])
	}
}

func TestClearAlgorithmTestsIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	algorithm := model.Algorithm{
		ID:              "alg-clear-tests-1",
		Name:            "Clear Test Records",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	makeRecord := func(id, relPath string) {
		record := model.AlgorithmTestRecord{
			ID:              id,
			AlgorithmID:     algorithm.ID,
			ImagePath:       relPath,
			MediaPath:       relPath,
			RequestPayload:  `{"camera_id":"cam-1","detect_mode":1}`,
			ResponsePayload: `{"success":true,"snapshot_width":1920,"snapshot_height":1080}`,
			Success:         true,
		}
		if err := s.db.Create(&record).Error; err != nil {
			t.Fatalf("create algorithm test record failed: %v", err)
		}
		fullPath := filepath.Join(algorithmTestMediaRootDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir test image dir failed: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte("fake-image"), 0o644); err != nil {
			t.Fatalf("write test image failed: %v", err)
		}
	}

	relPath1 := filepath.ToSlash(filepath.Join("20991231", "alg-clear-tests-1_a.jpg"))
	relPath2 := filepath.ToSlash(filepath.Join("20991231", "alg-clear-tests-1_b.jpg"))
	makeRecord("alg-clear-record-1", relPath1)
	makeRecord("alg-clear-record-2", relPath2)

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/algorithms/"+algorithm.ID+"/tests", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear algorithm tests failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			DeletedRecords int `json:"deleted_records"`
			DeletedFiles   int `json:"deleted_files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode clear tests response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected clear tests response: %s", rec.Body.String())
	}
	if resp.Data.DeletedRecords != 2 {
		t.Fatalf("unexpected deleted_records: %d", resp.Data.DeletedRecords)
	}
	if resp.Data.DeletedFiles != 2 {
		t.Fatalf("unexpected deleted_files: %d", resp.Data.DeletedFiles)
	}

	var count int64
	if err := s.db.Model(&model.AlgorithmTestRecord{}).Where("algorithm_id = ?", algorithm.ID).Count(&count).Error; err != nil {
		t.Fatalf("count algorithm tests failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected all algorithm test records removed, got=%d", count)
	}

	fullPath1 := filepath.Join(algorithmTestMediaRootDir, filepath.FromSlash(relPath1))
	fullPath2 := filepath.Join(algorithmTestMediaRootDir, filepath.FromSlash(relPath2))
	if _, err := os.Stat(fullPath1); !os.IsNotExist(err) {
		t.Fatalf("expected image file removed: %s", fullPath1)
	}
	if _, err := os.Stat(fullPath2); !os.IsNotExist(err) {
		t.Fatalf("expected image file removed: %s", fullPath2)
	}
	_ = os.Remove(filepath.Join(algorithmTestMediaRootDir, "20991231"))
}

func TestEventAPIIncludesJoinedNamesIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-event-name-1",
		Name:            "Event Device Name",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_event_name_1",
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
		ID:              "alg-event-name-1",
		Name:            "Event Algorithm Name",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	task := model.VideoTask{
		ID:              "task-event-name-1",
		Name:            "Event Task Name",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	event := model.AlarmEvent{
		ID:             "event-name-1",
		TaskID:         task.ID,
		DeviceID:       device.ID,
		AlgorithmID:    algorithm.ID,
		AlarmLevelID:   level.ID,
		Status:         model.EventStatusPending,
		OccurredAt:     time.Now(),
		SnapshotPath:   "20260224/demo.jpg",
		SnapshotWidth:  1920,
		SnapshotHeight: 1080,
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        "{}",
		SourceCallback: "{}",
	}
	if err := s.db.Create(&event).Error; err != nil {
		t.Fatalf("create event failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	engine.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list events failed: status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	var listResp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				ID            string `json:"id"`
				TaskID        string `json:"task_id"`
				DeviceID      string `json:"device_id"`
				AlgorithmID   string `json:"algorithm_id"`
				TaskName      string `json:"task_name"`
				DeviceName    string `json:"device_name"`
				AlgorithmName string `json:"algorithm_name"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list events response failed: %v", err)
	}
	if listResp.Code != 0 || len(listResp.Data.Items) == 0 {
		t.Fatalf("unexpected list events response: %s", listRec.Body.String())
	}

	var listed *struct {
		ID            string `json:"id"`
		TaskID        string `json:"task_id"`
		DeviceID      string `json:"device_id"`
		AlgorithmID   string `json:"algorithm_id"`
		TaskName      string `json:"task_name"`
		DeviceName    string `json:"device_name"`
		AlgorithmName string `json:"algorithm_name"`
	}
	for i := range listResp.Data.Items {
		if listResp.Data.Items[i].ID == event.ID {
			listed = &listResp.Data.Items[i]
			break
		}
	}
	if listed == nil {
		t.Fatalf("expected event %s in list response", event.ID)
	}
	if listed.TaskName != task.Name || listed.DeviceName != device.Name || listed.AlgorithmName != algorithm.Name {
		t.Fatalf("unexpected joined names in list: %+v", *listed)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/events/"+event.ID, nil)
	detailReq.Header.Set("Authorization", "Bearer "+token)
	detailRec := httptest.NewRecorder()
	engine.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("event detail failed: status=%d body=%s", detailRec.Code, detailRec.Body.String())
	}

	var detailResp struct {
		Code int `json:"code"`
		Data struct {
			ID            string `json:"id"`
			TaskID        string `json:"task_id"`
			DeviceID      string `json:"device_id"`
			AlgorithmID   string `json:"algorithm_id"`
			TaskName      string `json:"task_name"`
			DeviceName    string `json:"device_name"`
			AlgorithmName string `json:"algorithm_name"`
			BoxesJSON     string `json:"boxes_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detailResp); err != nil {
		t.Fatalf("decode event detail response failed: %v", err)
	}
	if detailResp.Code != 0 {
		t.Fatalf("unexpected detail response: %s", detailRec.Body.String())
	}
	if detailResp.Data.ID != event.ID {
		t.Fatalf("unexpected detail id: got=%s want=%s", detailResp.Data.ID, event.ID)
	}
	if detailResp.Data.TaskName != task.Name || detailResp.Data.DeviceName != device.Name || detailResp.Data.AlgorithmName != algorithm.Name {
		t.Fatalf("unexpected joined names in detail: %+v", detailResp.Data)
	}
	if detailResp.Data.BoxesJSON != event.BoxesJSON {
		t.Fatalf("event core field regression: boxes_json mismatch")
	}
}

func TestEventsListSupportsNameFiltersIntegration(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("UTC+8", 8*3600)
	t.Cleanup(func() {
		time.Local = oldLocal
	})

	s := newFocusedTestServer(t)
	engine := s.Engine()

	areaSouth := model.Area{
		ID:       "area-event-filter-south",
		Name:     "South Area",
		ParentID: model.RootAreaID,
		IsRoot:   false,
		Sort:     10,
	}
	if err := s.db.Create(&areaSouth).Error; err != nil {
		t.Fatalf("create areaSouth failed: %v", err)
	}

	deviceA := model.Device{
		ID:              "dev-event-filter-a",
		Name:            "North Gate Camera",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_event_filter_a",
		StreamURL:       "rtsp://127.0.0.1:8554/live/a",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	deviceB := model.Device{
		ID:              "dev-event-filter-b",
		Name:            "South Yard Camera",
		AreaID:          areaSouth.ID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_event_filter_b",
		StreamURL:       "rtsp://127.0.0.1:8554/live/b",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&deviceA).Error; err != nil {
		t.Fatalf("create deviceA failed: %v", err)
	}
	if err := s.db.Create(&deviceB).Error; err != nil {
		t.Fatalf("create deviceB failed: %v", err)
	}

	algorithmA := model.Algorithm{
		ID:              "alg-event-filter-1",
		Name:            "North Intrusion",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	algorithmB := model.Algorithm{
		ID:              "alg-event-filter-2",
		Name:            "South Fire",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithmA).Error; err != nil {
		t.Fatalf("create algorithmA failed: %v", err)
	}
	if err := s.db.Create(&algorithmB).Error; err != nil {
		t.Fatalf("create algorithmB failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	taskA := model.VideoTask{
		ID:              "task-event-filter-a",
		Name:            "North Patrol Task",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	taskB := model.VideoTask{
		ID:              "task-event-filter-b",
		Name:            "South Patrol Task",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&taskA).Error; err != nil {
		t.Fatalf("create taskA failed: %v", err)
	}
	if err := s.db.Create(&taskB).Error; err != nil {
		t.Fatalf("create taskB failed: %v", err)
	}

	baseNow := time.Now()
	eventA := model.AlarmEvent{
		ID:             "event-filter-a",
		TaskID:         taskA.ID,
		DeviceID:       deviceA.ID,
		AlgorithmID:    algorithmA.ID,
		AlarmLevelID:   level.ID,
		Status:         model.EventStatusPending,
		OccurredAt:     baseNow,
		SnapshotPath:   "20260224/filter_a.jpg",
		SnapshotWidth:  1280,
		SnapshotHeight: 720,
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        "{}",
		SourceCallback: "{}",
	}
	eventB := model.AlarmEvent{
		ID:             "event-filter-b",
		TaskID:         taskB.ID,
		DeviceID:       deviceB.ID,
		AlgorithmID:    algorithmB.ID,
		AlarmLevelID:   level.ID,
		Status:         model.EventStatusInvalid,
		OccurredAt:     baseNow.Add(-time.Minute),
		SnapshotPath:   "20260224/filter_b.jpg",
		SnapshotWidth:  1280,
		SnapshotHeight: 720,
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        "{}",
		SourceCallback: "{}",
	}
	if err := s.db.Create(&eventA).Error; err != nil {
		t.Fatalf("create eventA failed: %v", err)
	}
	if err := s.db.Create(&eventB).Error; err != nil {
		t.Fatalf("create eventB failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")

	queryIDs := func(rawQuery string) []string {
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
			t.Fatalf("decode list events failed: query=%s err=%v", rawQuery, err)
		}
		if resp.Code != 0 {
			t.Fatalf("unexpected list events payload: query=%s body=%s", rawQuery, rec.Body.String())
		}
		out := make([]string, 0, len(resp.Data.Items))
		for _, item := range resp.Data.Items {
			out = append(out, item.ID)
		}
		return out
	}

	containsID := func(items []string, target string) bool {
		for _, item := range items {
			if item == target {
				return true
			}
		}
		return false
	}

	byTaskName := queryIDs("?task_name=" + url.QueryEscape("North Patrol"))
	if len(byTaskName) != 1 || !containsID(byTaskName, eventA.ID) {
		t.Fatalf("task_name filter mismatch: %v", byTaskName)
	}

	byDeviceName := queryIDs("?device_name=" + url.QueryEscape("South Yard"))
	if len(byDeviceName) != 1 || !containsID(byDeviceName, eventB.ID) {
		t.Fatalf("device_name filter mismatch: %v", byDeviceName)
	}

	byAlgorithmName := queryIDs("?algorithm_name=" + url.QueryEscape("South Fire"))
	if len(byAlgorithmName) != 1 || !containsID(byAlgorithmName, eventB.ID) {
		t.Fatalf("algorithm_name filter mismatch: %v", byAlgorithmName)
	}

	combined := queryIDs(
		"?task_name=" + url.QueryEscape("North Patrol") +
			"&device_name=" + url.QueryEscape("North Gate") +
			"&algorithm_name=" + url.QueryEscape("North Intrusion") +
			"&status=pending",
	)
	if len(combined) != 1 || !containsID(combined, eventA.ID) {
		t.Fatalf("combined name/status/algorithm filter mismatch: %v", combined)
	}

	none := queryIDs(
		"?task_name=" + url.QueryEscape("North Patrol") +
			"&device_name=" + url.QueryEscape("North Gate") +
			"&algorithm_name=" + url.QueryEscape("North Intrusion") +
			"&status=invalid",
	)
	if len(none) != 0 {
		t.Fatalf("expected empty result for unmatched status, got: %v", none)
	}

	byLegacyTaskID := queryIDs("?task_id=" + url.QueryEscape(taskA.ID))
	if len(byLegacyTaskID) != 1 || !containsID(byLegacyTaskID, eventA.ID) {
		t.Fatalf("legacy task_id filter mismatch: %v", byLegacyTaskID)
	}

	byLegacyDeviceID := queryIDs("?device_id=" + url.QueryEscape(deviceB.ID))
	if len(byLegacyDeviceID) != 1 || !containsID(byLegacyDeviceID, eventB.ID) {
		t.Fatalf("legacy device_id filter mismatch: %v", byLegacyDeviceID)
	}

	byAreaID := queryIDs("?area_id=" + url.QueryEscape(areaSouth.ID))
	if len(byAreaID) != 1 || !containsID(byAreaID, eventB.ID) {
		t.Fatalf("area_id filter mismatch: %v", byAreaID)
	}

	byAlgorithmID := queryIDs("?algorithm_id=" + url.QueryEscape(algorithmA.ID))
	if len(byAlgorithmID) != 1 || !containsID(byAlgorithmID, eventA.ID) {
		t.Fatalf("algorithm_id filter mismatch: %v", byAlgorithmID)
	}

	byAlarmLevelID := queryIDs("?alarm_level_id=" + url.QueryEscape(level.ID))
	if len(byAlarmLevelID) != 2 {
		t.Fatalf("alarm_level_id filter mismatch: %v", byAlarmLevelID)
	}

	startAt := strconv.FormatInt(baseNow.Add(-10*time.Second).UnixMilli(), 10)
	endAt := strconv.FormatInt(baseNow.Add(10*time.Second).UnixMilli(), 10)
	byTimeWindow := queryIDs("?start_at=" + url.QueryEscape(startAt) + "&end_at=" + url.QueryEscape(endAt))
	if len(byTimeWindow) != 1 || !containsID(byTimeWindow, eventA.ID) {
		t.Fatalf("time window filter mismatch: %v", byTimeWindow)
	}

	startAtRFC3339 := baseNow.Add(-10 * time.Second).UTC().Format(time.RFC3339)
	endAtRFC3339 := baseNow.Add(10 * time.Second).UTC().Format(time.RFC3339)
	byTimeWindowRFC3339 := queryIDs(
		"?start_at=" + url.QueryEscape(startAtRFC3339) +
			"&end_at=" + url.QueryEscape(endAtRFC3339),
	)
	if len(byTimeWindowRFC3339) != 1 || !containsID(byTimeWindowRFC3339, eventA.ID) {
		t.Fatalf("rfc3339 time window filter mismatch: %v", byTimeWindowRFC3339)
	}
}

func TestAlertWebSocketPayloadIncludesNamesIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-ws-name-1",
		Name:            "WS Device Name",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_ws_name_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live/ws",
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
		ID:              "alg-ws-name-1",
		Name:            "WS Algorithm Name",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	task := model.VideoTask{
		ID:              "task-ws-name-1",
		Name:            "WS Task Name",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    task.FrameInterval,
		SmallConfidence:  task.SmallConfidence,
		LargeConfidence:  task.LargeConfidence,
		SmallIOU:         task.SmallIOU,
		AlarmLevelID:     level.ID,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create task-device profile failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:       task.ID,
		DeviceID:     device.ID,
		AlgorithmID:  algorithm.ID,
		AlarmLevelID: builtinAlarmLevelID1,
	}).Error; err != nil {
		t.Fatalf("create task-device algorithm relation failed: %v", err)
	}

	httpSrv := httptest.NewServer(engine)
	defer httpSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/alerts"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	defer conn.Close()

	callbackBody, _ := json.Marshal(map[string]any{
		"camera_id":       device.ID,
		"timestamp":       time.Now().UnixMilli(),
		"detect_mode":     1,
		"snapshot":        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII=",
		"snapshot_width":  1920,
		"snapshot_height": 1080,
		"detections": []map[string]any{
			{
				"label":      "person",
				"confidence": 0.91,
				"box": map[string]any{
					"x_min": 100,
					"y_min": 120,
					"x_max": 220,
					"y_max": 280,
				},
			},
		},
	})
	req, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/ai/events", bytes.NewReader(callbackBody))
	if err != nil {
		t.Fatalf("build callback request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "expected-callback-token")
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post callback failed: %v", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected callback status: %d", httpResp.StatusCode)
	}
	var callbackResp struct {
		Code int `json:"code"`
		Data struct {
			CreatedEventIDs []string `json:"created_event_ids"`
		} `json:"data"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&callbackResp); err != nil {
		t.Fatalf("decode callback response failed: %v", err)
	}
	if callbackResp.Code != 0 || len(callbackResp.Data.CreatedEventIDs) == 0 {
		t.Fatalf("expected created_event_ids in callback response, got: %+v", callbackResp)
	}
	var savedEvent model.AlarmEvent
	if err := s.db.Where("id = ?", callbackResp.Data.CreatedEventIDs[0]).First(&savedEvent).Error; err != nil {
		t.Fatalf("query saved event failed: %v", err)
	}
	if strings.TrimSpace(savedEvent.SnapshotPath) != "" {
		snapshotPath := filepath.Join("configs", "events", filepath.FromSlash(savedEvent.SnapshotPath))
		t.Cleanup(func() {
			_ = os.Remove(snapshotPath)
			_ = os.Remove(filepath.Dir(snapshotPath))
		})
	}
	if strings.Contains(savedEvent.SourceCallback, "\"snapshot\"") {
		t.Fatalf("source_callback should not contain snapshot field, got=%s", savedEvent.SourceCallback)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message failed: %v", err)
	}
	var alarmPayload map[string]any
	if err := json.Unmarshal(msg, &alarmPayload); err != nil {
		t.Fatalf("decode websocket payload failed: %v", err)
	}
	payloadType, _ := alarmPayload["type"].(string)
	if strings.TrimSpace(payloadType) != "alarm" {
		t.Fatalf("unexpected websocket type: %+v", alarmPayload)
	}
	taskName, _ := alarmPayload["task_name"].(string)
	deviceName, _ := alarmPayload["device_name"].(string)
	algorithmName, _ := alarmPayload["algorithm_name"].(string)
	if strings.TrimSpace(taskName) != task.Name {
		t.Fatalf("unexpected task_name in websocket payload: %+v", alarmPayload)
	}
	if strings.TrimSpace(deviceName) != device.Name {
		t.Fatalf("unexpected device_name in websocket payload: %+v", alarmPayload)
	}
	if strings.TrimSpace(algorithmName) != algorithm.Name {
		t.Fatalf("unexpected algorithm_name in websocket payload: %+v", alarmPayload)
	}
}

func TestAlertCycleSuppressesRepeatedWebSocketButKeepsEventsIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-alert-cycle-1",
		Name:            "Alert Cycle Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_alert_cycle_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live/alert_cycle",
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
		ID:              "alg-alert-cycle-1",
		Name:            "Alert Cycle Algorithm",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	task := model.VideoTask{
		ID:              "task-alert-cycle-1",
		Name:            "Alert Cycle Task",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    task.FrameInterval,
		FrameRateMode:    model.FrameRateModeFPS,
		FrameRateValue:   5,
		SmallConfidence:  task.SmallConfidence,
		LargeConfidence:  task.LargeConfidence,
		SmallIOU:         task.SmallIOU,
		AlarmLevelID:     level.ID,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create task-device profile failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:            task.ID,
		DeviceID:          device.ID,
		AlgorithmID:       algorithm.ID,
		AlarmLevelID:      builtinAlarmLevelID1,
		AlertCycleSeconds: 30,
	}).Error; err != nil {
		t.Fatalf("create task-device algorithm relation failed: %v", err)
	}

	httpSrv := httptest.NewServer(engine)
	defer httpSrv.Close()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/alerts"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	defer conn.Close()

	sendCallback := func() {
		callbackBody, _ := json.Marshal(map[string]any{
			"camera_id":       device.ID,
			"timestamp":       time.Now().UnixMilli(),
			"detect_mode":     1,
			"snapshot":        "",
			"snapshot_width":  0,
			"snapshot_height": 0,
			"detections": []map[string]any{
				{
					"label":      "person",
					"confidence": 0.91,
					"box": map[string]any{
						"x_min": 10,
						"y_min": 10,
						"x_max": 60,
						"y_max": 90,
					},
				},
			},
		})
		req, buildErr := http.NewRequest(http.MethodPost, httpSrv.URL+"/ai/events", bytes.NewReader(callbackBody))
		if buildErr != nil {
			t.Fatalf("build callback request failed: %v", buildErr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "expected-callback-token")
		resp, postErr := http.DefaultClient.Do(req)
		if postErr != nil {
			t.Fatalf("post callback failed: %v", postErr)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("unexpected callback status: %d", resp.StatusCode)
		}
	}

	sendCallback()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("expected first websocket alarm message, got error: %v", err)
	}

	sendCallback()
	_ = conn.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected second callback to be suppressed by alert cycle")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("expected websocket timeout after suppression, got: %v", err)
	}

	var events []model.AlarmEvent
	if err := s.db.Where("task_id = ? AND device_id = ? AND algorithm_id = ?", task.ID, device.ID, algorithm.ID).
		Order("created_at asc").
		Find(&events).Error; err != nil {
		t.Fatalf("query events failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events persisted, got %d", len(events))
	}
	if events[0].NotifiedAt == nil {
		t.Fatalf("expected first event notified_at to be set")
	}
	if events[1].NotifiedAt != nil {
		t.Fatalf("expected second event notified_at to be empty due to suppression")
	}
}

func TestTaskStartRewriteRTSPForAIInputHostIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.Disabled = false
	s.cfg.Server.ZLM.AIInputHost = "zlm"
	s.cfg.Server.ZLM.PlayHost = "127.0.0.1"

	var startReq ai.StartCameraRequest
	var stopReq ai.StopCameraRequest
	startCalled := false
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/api/start_camera":
			startCalled = true
			if err := json.NewDecoder(r.Body).Decode(&startReq); err != nil {
				t.Errorf("decode start_camera payload failed: %v", err)
				http.Error(w, "bad payload", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ai.StartCameraResponse{
				Success:      true,
				Message:      "started",
				CameraID:     startReq.CameraID,
				SourceWidth:  1920,
				SourceHeight: 1080,
				SourceFPS:    25,
			})
		case "/api/stop_camera":
			if err := json.NewDecoder(r.Body).Decode(&stopReq); err != nil {
				t.Errorf("decode stop_camera payload failed: %v", err)
				http.Error(w, "bad payload", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ai.GenericResponse{
				Success: true,
				Message: "stopped",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-ai-input-host-1",
		Name:            "Pull Channel For AI Input Host",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "34020000001110852862_34020000001310000001",
		StreamURL:       "rtsp://61.150.94.14:20032/rtp/34020000001110852862_34020000001310000001?originTypeStr=rtp_push",
		PlayRTSPURL:     "rtsp://127.0.0.1:1554/rtp/34020000001110852862_34020000001310000001",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    `{"zlm_app":"live","zlm_stream":"34020000001110852862_34020000001310000001","rtsp":"rtsp://127.0.0.1:1554/rtp/output_fallback_should_not_be_used"}`,
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	algorithm := model.Algorithm{
		ID:              "alg-ai-input-host-1",
		Name:            "Small Mode For RTSP Rewrite",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	task := model.VideoTask{
		ID:              "task-ai-input-host-1",
		Name:            "task-ai-input-host",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    task.FrameInterval,
		SmallConfidence:  task.SmallConfidence,
		LargeConfidence:  task.LargeConfidence,
		SmallIOU:         task.SmallIOU,
		AlarmLevelID:     level.ID,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create task-device profile failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:       task.ID,
		DeviceID:     device.ID,
		AlgorithmID:  algorithm.ID,
		AlarmLevelID: builtinAlarmLevelID1,
	}).Error; err != nil {
		t.Fatalf("create task-device algorithm relation failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	startHTTPReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/start", nil)
	startHTTPReq.Header.Set("Authorization", "Bearer "+token)
	startRec := httptest.NewRecorder()
	engine.ServeHTTP(startRec, startHTTPReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("task start failed: status=%d body=%s", startRec.Code, startRec.Body.String())
	}
	if !startCalled {
		t.Fatalf("expected ai start request called")
	}
	expectedRTSP := "rtsp://zlm:1554/rtp/34020000001110852862_34020000001310000001"
	if strings.TrimSpace(startReq.RTSPURL) != expectedRTSP {
		t.Fatalf("unexpected rewritten rtsp url: got=%s want=%s", startReq.RTSPURL, expectedRTSP)
	}
	if strings.Contains(strings.TrimSpace(startReq.RTSPURL), "output_fallback_should_not_be_used") {
		t.Fatalf("expected play_rtsp_url to have higher priority than output_config.rtsp, got=%s", startReq.RTSPURL)
	}
	if strings.TrimSpace(startReq.CameraID) != device.ID {
		t.Fatalf("unexpected start camera id: %s", startReq.CameraID)
	}

	stopHTTPReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/stop", nil)
	stopHTTPReq.Header.Set("Authorization", "Bearer "+token)
	stopRec := httptest.NewRecorder()
	engine.ServeHTTP(stopRec, stopHTTPReq)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("task stop failed: status=%d body=%s", stopRec.Code, stopRec.Body.String())
	}
	if strings.TrimSpace(stopReq.CameraID) != device.ID {
		t.Fatalf("unexpected stop camera id: %s", stopReq.CameraID)
	}
}

func TestTaskStartKeepPlayHostWhenAIInputHostLoopbackIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.Disabled = false
	s.cfg.Server.ZLM.AIInputHost = "127.0.0.1"
	s.cfg.Server.ZLM.PlayHost = "61.150.94.14"

	var startReq ai.StartCameraRequest
	startCalled := false
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/api/start_camera" {
			http.NotFound(w, r)
			return
		}
		startCalled = true
		if err := json.NewDecoder(r.Body).Decode(&startReq); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.StartCameraResponse{
			Success:      true,
			Message:      "started",
			CameraID:     startReq.CameraID,
			SourceWidth:  1920,
			SourceHeight: 1080,
			SourceFPS:    25,
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)
	engine := s.Engine()

	streamID := "34020000001320000012_34020000001320000001"
	device := model.Device{
		ID:              "dev-ai-loopback-safe-rewrite-1",
		Name:            "Pull Channel Keep PlayHost For Loopback AIInputHost",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "rtp",
		StreamID:        streamID,
		StreamURL:       "rtsp://61.150.94.14:20032/rtp/" + streamID + "?originTypeStr=rtp_push",
		PlayRTSPURL:     "rtsp://61.150.94.14:1554/rtp/" + streamID,
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    `{"zlm_app":"rtp","zlm_stream":"` + streamID + `","rtsp":"rtsp://61.150.94.14:1554/rtp/` + streamID + `"}`,
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	algorithm := model.Algorithm{
		ID:              "alg-ai-loopback-safe-rewrite-1",
		Name:            "Loopback Safe Rewrite Algorithm",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	task := model.VideoTask{
		ID:              "task-ai-loopback-safe-rewrite-1",
		Name:            "task-ai-loopback-safe-rewrite",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    task.FrameInterval,
		SmallConfidence:  task.SmallConfidence,
		LargeConfidence:  task.LargeConfidence,
		SmallIOU:         task.SmallIOU,
		AlarmLevelID:     level.ID,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create task-device profile failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:       task.ID,
		DeviceID:     device.ID,
		AlgorithmID:  algorithm.ID,
		AlarmLevelID: builtinAlarmLevelID1,
	}).Error; err != nil {
		t.Fatalf("create task-device algorithm relation failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	startHTTPReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/start", nil)
	startHTTPReq.Header.Set("Authorization", "Bearer "+token)
	startRec := httptest.NewRecorder()
	engine.ServeHTTP(startRec, startHTTPReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("task start failed: status=%d body=%s", startRec.Code, startRec.Body.String())
	}
	if !startCalled {
		t.Fatalf("expected ai start request called")
	}
	expectedRTSP := "rtsp://61.150.94.14:1554/rtp/" + streamID
	if strings.TrimSpace(startReq.RTSPURL) != expectedRTSP {
		t.Fatalf("unexpected rewritten rtsp url: got=%s want=%s", startReq.RTSPURL, expectedRTSP)
	}
	if strings.Contains(strings.TrimSpace(startReq.RTSPURL), ":20032/") {
		t.Fatalf("expected ai rtsp input to prioritize zlm output rtsp, got=%s", startReq.RTSPURL)
	}
}

func TestTaskStartGBSignalOfflineKeepsSourceOfflineIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.Disabled = false

	startCalled := false
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/api/start_camera" {
			http.NotFound(w, r)
			return
		}
		startCalled = true
		var req ai.StartCameraRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.StartCameraResponse{
			Success:      true,
			Message:      "started",
			CameraID:     req.CameraID,
			SourceWidth:  1920,
			SourceHeight: 1080,
			SourceFPS:    25,
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)
	engine := s.Engine()

	gbDeviceID := "34020000001110852862"
	gbChannelID := "34020000001310000001"
	streamID := gbDeviceID + "_" + gbChannelID
	device := model.Device{
		ID:              "dev-task-start-gb-signal-offline-1",
		Name:            "GB Channel Offline",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypeGB28181,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolGB28181,
		Transport:       "udp",
		App:             "rtp",
		StreamID:        streamID,
		StreamURL:       "gb28181://" + gbDeviceID + "/" + gbChannelID,
		PlayRTSPURL:     "rtsp://127.0.0.1:1554/rtp/" + streamID,
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    `{"gb_device_id":"` + gbDeviceID + `","gb_channel_id":"` + gbChannelID + `","rtsp":"rtsp://127.0.0.1:1554/rtp/` + streamID + `"}`,
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	if err := s.db.Create(&model.GBDevice{
		DeviceID:       gbDeviceID,
		SourceIDDevice: "gb-device-task-start-offline-1",
		Name:           "GB Device Offline",
		AreaID:         model.RootAreaID,
		Enabled:        true,
		Status:         "offline",
		Transport:      "udp",
		Expires:        3600,
	}).Error; err != nil {
		t.Fatalf("create gb device failed: %v", err)
	}

	algorithm := model.Algorithm{
		ID:              "alg-task-start-gb-signal-offline-1",
		Name:            "GB Offline Algorithm",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}
	task := model.VideoTask{
		ID:              "task-start-gb-signal-offline-1",
		Name:            "task-start-gb-signal-offline",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    task.FrameInterval,
		SmallConfidence:  task.SmallConfidence,
		LargeConfidence:  task.LargeConfidence,
		SmallIOU:         task.SmallIOU,
		AlarmLevelID:     level.ID,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create task-device profile failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:       task.ID,
		DeviceID:     device.ID,
		AlgorithmID:  algorithm.ID,
		AlarmLevelID: builtinAlarmLevelID1,
	}).Error; err != nil {
		t.Fatalf("create task-device algorithm relation failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/start", nil)
	startReq.Header.Set("Authorization", "Bearer "+token)
	startRec := httptest.NewRecorder()
	engine.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("task start failed: status=%d body=%s", startRec.Code, startRec.Body.String())
	}
	if !startCalled {
		t.Fatalf("expected ai start request called even when gb signal is offline")
	}

	var updated model.Device
	if err := s.db.Where("id = ?", device.ID).First(&updated).Error; err != nil {
		t.Fatalf("query updated device failed: %v", err)
	}
	if updated.AIStatus != model.DeviceAIStatusRunning {
		t.Fatalf("expected ai_status=%s, got=%s", model.DeviceAIStatusRunning, updated.AIStatus)
	}
	if updated.Status != "offline" {
		t.Fatalf("expected status=offline when gb signal offline, got=%s", updated.Status)
	}
}

func TestTaskStartUsesDeviceProfileParamsIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.Disabled = false

	startReqs := make(map[string]ai.StartCameraRequest)
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/api/start_camera" {
			http.NotFound(w, r)
			return
		}
		var req ai.StartCameraRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		startReqs[req.CameraID] = req
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.StartCameraResponse{
			Success:      true,
			Message:      "started",
			CameraID:     req.CameraID,
			SourceWidth:  1920,
			SourceHeight: 1080,
			SourceFPS:    25,
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)
	engine := s.Engine()

	deviceA := model.Device{
		ID:              "dev-profile-a",
		Name:            "Profile Device A",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_profile_a",
		StreamURL:       "rtsp://172.16.1.10:554/live/a",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	deviceB := model.Device{
		ID:              "dev-profile-b",
		Name:            "Profile Device B",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_profile_b",
		StreamURL:       "rtsp://172.16.1.11:554/live/b",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&deviceA).Error; err != nil {
		t.Fatalf("create deviceA failed: %v", err)
	}
	if err := s.db.Create(&deviceB).Error; err != nil {
		t.Fatalf("create deviceB failed: %v", err)
	}

	algorithmA := model.Algorithm{
		ID:                "alg-profile-a",
		Name:              "Profile Alg A",
		Mode:              model.AlgorithmModeSmall,
		DetectMode:        model.AlgorithmDetectModeSmallOnly,
		Enabled:           true,
		SmallModelLabel:   "person",
		YoloThreshold:     0.35,
		IOUThreshold:      0.60,
		LabelsTriggerMode: model.LabelsTriggerModeAny,
	}
	algorithmB := model.Algorithm{
		ID:                "alg-profile-b",
		Name:              "Profile Alg B",
		Mode:              model.AlgorithmModeSmall,
		DetectMode:        model.AlgorithmDetectModeSmallOnly,
		Enabled:           true,
		SmallModelLabel:   "car",
		YoloThreshold:     0.55,
		IOUThreshold:      0.72,
		LabelsTriggerMode: model.LabelsTriggerModeAll,
	}
	if err := s.db.Create(&algorithmA).Error; err != nil {
		t.Fatalf("create algorithmA failed: %v", err)
	}
	if err := s.db.Create(&algorithmB).Error; err != nil {
		t.Fatalf("create algorithmB failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	task := model.VideoTask{
		ID:              "task-profile-params-1",
		Name:            "task-profile-params",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	if err := s.db.Create([]model.VideoTaskDeviceProfile{
		{
			TaskID:           task.ID,
			DeviceID:         deviceA.ID,
			FrameInterval:    3,
			FrameRateMode:    model.FrameRateModeFPS,
			FrameRateValue:   3,
			SmallConfidence:  0.35,
			LargeConfidence:  0.75,
			SmallIOU:         0.60,
			AlarmLevelID:     level.ID,
			RecordingPolicy:  model.RecordingPolicyAlarmClip,
			AlarmPreSeconds:  5,
			AlarmPostSeconds: 10,
		},
		{
			TaskID:           task.ID,
			DeviceID:         deviceB.ID,
			FrameInterval:    9,
			FrameRateMode:    model.FrameRateModeInterval,
			FrameRateValue:   9,
			SmallConfidence:  0.55,
			LargeConfidence:  0.88,
			SmallIOU:         0.72,
			AlarmLevelID:     level.ID,
			RecordingPolicy:  model.RecordingPolicyNone,
			AlarmPreSeconds:  8,
			AlarmPostSeconds: 12,
		},
	}).Error; err != nil {
		t.Fatalf("create task device profiles failed: %v", err)
	}
	if err := s.db.Create([]model.VideoTaskDeviceAlgorithm{
		{TaskID: task.ID, DeviceID: deviceA.ID, AlgorithmID: algorithmA.ID, AlarmLevelID: builtinAlarmLevelID1},
		{TaskID: task.ID, DeviceID: deviceB.ID, AlgorithmID: algorithmB.ID, AlarmLevelID: builtinAlarmLevelID1},
	}).Error; err != nil {
		t.Fatalf("create task device algorithms failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/start", nil)
	startReq.Header.Set("Authorization", "Bearer "+token)
	startRec := httptest.NewRecorder()
	engine.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("task start failed: status=%d body=%s", startRec.Code, startRec.Body.String())
	}

	reqA, okA := startReqs[deviceA.ID]
	reqB, okB := startReqs[deviceB.ID]
	if !okA || !okB {
		t.Fatalf("expected start requests for both devices, got=%d", len(startReqs))
	}
	if reqA.DetectRateMode != model.FrameRateModeFPS || reqA.DetectRateValue != 3 {
		t.Fatalf("device A profile params mismatch: %+v", reqA)
	}
	if reqB.DetectRateMode != model.FrameRateModeInterval || reqB.DetectRateValue != 9 {
		t.Fatalf("device B profile params mismatch: %+v", reqB)
	}
	if len(reqA.AlgorithmConfigs) != 1 {
		t.Fatalf("device A algorithm config mismatch: %+v", reqA.AlgorithmConfigs)
	}
	cfgA := reqA.AlgorithmConfigs[0]
	if cfgA.AlgorithmID != algorithmA.ID || cfgA.TaskCode == "" || len(cfgA.Labels) != 1 || cfgA.Labels[0] != "person" {
		t.Fatalf("device A algorithm config mismatch: %+v", cfgA)
	}
	if cfgA.YoloThreshold != 0.35 || cfgA.IOUThreshold != 0.60 {
		t.Fatalf("device A threshold mismatch: %+v", cfgA)
	}
	if cfgA.LabelsTriggerMode != model.LabelsTriggerModeAny {
		t.Fatalf("device A strategy mismatch: %+v", cfgA)
	}
	if len(reqB.AlgorithmConfigs) != 1 {
		t.Fatalf("device B algorithm config mismatch: %+v", reqB.AlgorithmConfigs)
	}
	cfgB := reqB.AlgorithmConfigs[0]
	if cfgB.AlgorithmID != algorithmB.ID || cfgB.TaskCode == "" || len(cfgB.Labels) != 1 || cfgB.Labels[0] != "car" {
		t.Fatalf("device B algorithm config mismatch: %+v", cfgB)
	}
	if cfgB.YoloThreshold != 0.55 || cfgB.IOUThreshold != 0.72 {
		t.Fatalf("device B threshold mismatch: %+v", cfgB)
	}
	if cfgB.LabelsTriggerMode != model.LabelsTriggerModeAll {
		t.Fatalf("device B strategy mismatch: %+v", cfgB)
	}
}

func TestTaskStartReturnsPendingMessageAndRetryLimitIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.Disabled = false

	var startReq ai.StartCameraRequest
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/api/start_camera" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&startReq); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.StartCameraResponse{
			Success:      true,
			Message:      "pending",
			CameraID:     startReq.CameraID,
			SourceWidth:  0,
			SourceHeight: 0,
			SourceFPS:    0,
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-start-pending-1",
		Name:            "Start Pending Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_start_pending_1",
		StreamURL:       "rtsp://172.16.1.20:554/live/pending",
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
		ID:              "alg-start-pending-1",
		Name:            "Start Pending Algorithm",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	task := model.VideoTask{
		ID:              "task-start-pending-1",
		Name:            "task-start-pending",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    task.FrameInterval,
		SmallConfidence:  task.SmallConfidence,
		LargeConfidence:  task.LargeConfidence,
		SmallIOU:         task.SmallIOU,
		AlarmLevelID:     level.ID,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create task-device profile failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:       task.ID,
		DeviceID:     device.ID,
		AlgorithmID:  algorithm.ID,
		AlarmLevelID: builtinAlarmLevelID1,
	}).Error; err != nil {
		t.Fatalf("create task-device algorithm relation failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("task start failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Summary struct {
				Total   int `json:"total"`
				Success int `json:"success"`
				Failed  int `json:"failed"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode task start response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected task start response: %s", rec.Body.String())
	}
	if resp.Data.Status != model.TaskStatusRunning {
		t.Fatalf("expected running status, got=%s body=%s", resp.Data.Status, rec.Body.String())
	}
	if strings.TrimSpace(resp.Data.Message) == "" {
		t.Fatalf("unexpected task start message: %s", resp.Data.Message)
	}
	if resp.Data.Summary.Total != 1 || resp.Data.Summary.Success != 1 || resp.Data.Summary.Failed != 0 {
		t.Fatalf("unexpected summary: %+v", resp.Data.Summary)
	}
	if startReq.RetryLimit != aiStartCameraRetryLimit {
		t.Fatalf("unexpected retry_limit: got=%d want=%d", startReq.RetryLimit, aiStartCameraRetryLimit)
	}
}

func TestTaskStartReturnsPartialFailMessageIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.Disabled = false

	startReqs := make(map[string]ai.StartCameraRequest)
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/api/start_camera" {
			http.NotFound(w, r)
			return
		}
		var req ai.StartCameraRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		startReqs[req.CameraID] = req
		w.Header().Set("Content-Type", "application/json")
		if req.CameraID == "dev-start-partial-b" {
			_ = json.NewEncoder(w).Encode(ai.StartCameraResponse{
				Success: false,
				Message: "camera offline",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(ai.StartCameraResponse{
			Success:      true,
			Message:      "ok",
			CameraID:     req.CameraID,
			SourceWidth:  1920,
			SourceHeight: 1080,
			SourceFPS:    25,
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)
	engine := s.Engine()

	deviceA := model.Device{
		ID:              "dev-start-partial-a",
		Name:            "Partial Device A",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_start_partial_a",
		StreamURL:       "rtsp://172.16.1.30:554/live/a",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	deviceB := model.Device{
		ID:              "dev-start-partial-b",
		Name:            "Partial Device B",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_start_partial_b",
		StreamURL:       "rtsp://172.16.1.31:554/live/b",
		EnableRecording: true,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&deviceA).Error; err != nil {
		t.Fatalf("create deviceA failed: %v", err)
	}
	if err := s.db.Create(&deviceB).Error; err != nil {
		t.Fatalf("create deviceB failed: %v", err)
	}

	algorithmA := model.Algorithm{
		ID:              "alg-start-partial-a",
		Name:            "Partial Algorithm A",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	algorithmB := model.Algorithm{
		ID:              "alg-start-partial-b",
		Name:            "Partial Algorithm B",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "car",
	}
	if err := s.db.Create(&algorithmA).Error; err != nil {
		t.Fatalf("create algorithmA failed: %v", err)
	}
	if err := s.db.Create(&algorithmB).Error; err != nil {
		t.Fatalf("create algorithmB failed: %v", err)
	}

	var level model.AlarmLevel
	if err := s.db.Order("severity asc").First(&level).Error; err != nil {
		t.Fatalf("load alarm level failed: %v", err)
	}

	task := model.VideoTask{
		ID:              "task-start-partial-1",
		Name:            "task-start-partial",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    level.ID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create([]model.VideoTaskDeviceProfile{
		{
			TaskID:           task.ID,
			DeviceID:         deviceA.ID,
			FrameInterval:    task.FrameInterval,
			SmallConfidence:  task.SmallConfidence,
			LargeConfidence:  task.LargeConfidence,
			SmallIOU:         task.SmallIOU,
			AlarmLevelID:     level.ID,
			RecordingPolicy:  model.RecordingPolicyNone,
			AlarmPreSeconds:  8,
			AlarmPostSeconds: 12,
		},
		{
			TaskID:           task.ID,
			DeviceID:         deviceB.ID,
			FrameInterval:    task.FrameInterval,
			SmallConfidence:  task.SmallConfidence,
			LargeConfidence:  task.LargeConfidence,
			SmallIOU:         task.SmallIOU,
			AlarmLevelID:     level.ID,
			RecordingPolicy:  model.RecordingPolicyNone,
			AlarmPreSeconds:  8,
			AlarmPostSeconds: 12,
		},
	}).Error; err != nil {
		t.Fatalf("create task device profiles failed: %v", err)
	}
	if err := s.db.Create([]model.VideoTaskDeviceAlgorithm{
		{TaskID: task.ID, DeviceID: deviceA.ID, AlgorithmID: algorithmA.ID, AlarmLevelID: builtinAlarmLevelID1},
		{TaskID: task.ID, DeviceID: deviceB.ID, AlgorithmID: algorithmB.ID, AlarmLevelID: builtinAlarmLevelID1},
	}).Error; err != nil {
		t.Fatalf("create task device algorithms failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("task start failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Summary struct {
				Total   int `json:"total"`
				Success int `json:"success"`
				Failed  int `json:"failed"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode partial task start response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected partial task start response: %s", rec.Body.String())
	}
	if resp.Data.Status != model.TaskStatusPartialFail {
		t.Fatalf("expected partial_fail status, got=%s body=%s", resp.Data.Status, rec.Body.String())
	}
	if strings.TrimSpace(resp.Data.Message) == "" {
		t.Fatalf("unexpected partial_fail message: %s", resp.Data.Message)
	}
	if resp.Data.Summary.Total != 2 || resp.Data.Summary.Success != 1 || resp.Data.Summary.Failed != 1 {
		t.Fatalf("unexpected partial summary: %+v", resp.Data.Summary)
	}
	if len(startReqs) != 2 {
		t.Fatalf("expected 2 start requests, got=%d", len(startReqs))
	}
}

func setupSingleAlgorithmEventRuntime(
	t *testing.T,
	s *Server,
	suffix string,
	algorithmMode string,
	algorithmCode string,
) (model.Device, model.Algorithm, model.VideoTask, string) {
	t.Helper()

	device := model.Device{
		ID:              fmt.Sprintf("dev-event-priority-%s", suffix),
		Name:            fmt.Sprintf("Event Priority Device %s", suffix),
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        fmt.Sprintf("dev_event_priority_%s", suffix),
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
		ID:              fmt.Sprintf("alg-event-priority-%s", suffix),
		Code:            algorithmCode,
		Name:            fmt.Sprintf("Event Priority Algorithm %s", suffix),
		Mode:            algorithmMode,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	levelLowID := builtinAlarmLevelID1
	levelHighID := builtinAlarmLevelID3
	task := model.VideoTask{
		ID:              fmt.Sprintf("task-event-priority-%s", suffix),
		Name:            fmt.Sprintf("task-event-priority-%s", suffix),
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    levelLowID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    5,
		SmallConfidence:  0.5,
		LargeConfidence:  0.8,
		SmallIOU:         0.8,
		AlarmLevelID:     levelLowID,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create task device profile failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:       task.ID,
		DeviceID:     device.ID,
		AlgorithmID:  algorithm.ID,
		AlarmLevelID: levelHighID,
	}).Error; err != nil {
		t.Fatalf("create task device algorithm failed: %v", err)
	}

	return device, algorithm, task, levelHighID
}

func postAIDetectionEventAndLoadFirst(
	t *testing.T,
	s *Server,
	engine http.Handler,
	payload map[string]any,
) model.AlarmEvent {
	t.Helper()

	createdIDs := postAIDetectionEvent(t, engine, payload)
	if len(createdIDs) == 0 {
		t.Fatalf("expected created_event_ids, got none")
	}

	var saved model.AlarmEvent
	if err := s.db.Where("id = ?", createdIDs[0]).First(&saved).Error; err != nil {
		t.Fatalf("query saved event failed: %v", err)
	}
	return saved
}

func postAIDetectionEvent(
	t *testing.T,
	engine http.Handler,
	payload map[string]any,
) []string {
	t.Helper()

	callbackBody, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/ai/events", bytes.NewReader(callbackBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "expected-callback-token")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var callbackResp struct {
		Code int `json:"code"`
		Data struct {
			CreatedEventIDs []string `json:"created_event_ids"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &callbackResp); err != nil {
		t.Fatalf("decode callback response failed: %v", err)
	}
	if callbackResp.Code != 0 {
		t.Fatalf("expected success callback response, got body=%s", rec.Body.String())
	}
	return callbackResp.Data.CreatedEventIDs
}

func decodeNormalizedBoxesForTest(t *testing.T, raw string) []normalizedBox {
	t.Helper()
	var boxes []normalizedBox
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &boxes); err != nil {
		t.Fatalf("decode boxes_json failed: raw=%s err=%v", raw, err)
	}
	return boxes
}

func TestAIDetectionUsesDeviceAlgorithmAlarmLevelIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-event-profile-level",
		Name:            "Event Profile Level Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_event_profile_level",
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
		ID:              "alg-event-profile-level",
		Name:            "Profile Level Algorithm",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	levelLowID := builtinAlarmLevelID1
	levelHighID := builtinAlarmLevelID3

	task := model.VideoTask{
		ID:              "task-event-profile-level",
		Name:            "task-event-profile-level",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    levelLowID,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    5,
		SmallConfidence:  0.5,
		LargeConfidence:  0.8,
		SmallIOU:         0.8,
		AlarmLevelID:     levelLowID,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create task device profile failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:       task.ID,
		DeviceID:     device.ID,
		AlgorithmID:  algorithm.ID,
		AlarmLevelID: levelHighID,
	}).Error; err != nil {
		t.Fatalf("create task device algorithm failed: %v", err)
	}

	callbackBody, _ := json.Marshal(map[string]any{
		"camera_id":       device.ID,
		"timestamp":       time.Now().UnixMilli(),
		"detect_mode":     1,
		"snapshot":        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII=",
		"snapshot_width":  1920,
		"snapshot_height": 1080,
		"detections": []map[string]any{
			{
				"label":      "person",
				"confidence": 0.91,
				"box": map[string]any{
					"x_min": 10,
					"y_min": 20,
					"x_max": 120,
					"y_max": 220,
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/ai/events", bytes.NewReader(callbackBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "expected-callback-token")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var saved []model.AlarmEvent
	if err := s.db.Where("task_id = ? AND device_id = ?", task.ID, device.ID).Find(&saved).Error; err != nil {
		t.Fatalf("query saved event failed: %v", err)
	}
	if len(saved) == 0 {
		t.Fatalf("expected at least one event")
	}
	if strings.TrimSpace(saved[0].SnapshotPath) != "" {
		snapshotPath := filepath.Join("configs", "events", filepath.FromSlash(saved[0].SnapshotPath))
		t.Cleanup(func() {
			_ = os.Remove(snapshotPath)
			_ = os.Remove(filepath.Dir(snapshotPath))
			_ = os.Remove(filepath.Join("configs", "events"))
			_ = os.Remove("configs")
		})
	}
	if saved[0].AlarmLevelID != levelHighID {
		t.Fatalf("expected event alarm level from task-device-algorithm: got=%s want=%s", saved[0].AlarmLevelID, levelHighID)
	}
}

func TestAIDetectionFallsBackToLLMWhenAlgorithmResultsEmptyIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-event-llm-fallback",
		Name:            "Event LLM Fallback Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_event_llm_fallback",
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
		ID:              "alg-event-llm-fallback",
		Code:            "ALG_FALLBACK",
		Name:            "LLM Fallback Algorithm",
		Mode:            model.AlgorithmModeHybrid,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	levelHighID := builtinAlarmLevelID3
	task := model.VideoTask{
		ID:              "task-event-llm-fallback",
		Name:            "task-event-llm-fallback",
		Status:          model.TaskStatusStopped,
		FrameInterval:   5,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    builtinAlarmLevelID1,
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceProfile{
		TaskID:           task.ID,
		DeviceID:         device.ID,
		FrameInterval:    5,
		SmallConfidence:  0.5,
		LargeConfidence:  0.8,
		SmallIOU:         0.8,
		AlarmLevelID:     builtinAlarmLevelID1,
		RecordingPolicy:  model.RecordingPolicyNone,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
	}).Error; err != nil {
		t.Fatalf("create task device profile failed: %v", err)
	}
	if err := s.db.Create(&model.VideoTaskDeviceAlgorithm{
		TaskID:       task.ID,
		DeviceID:     device.ID,
		AlgorithmID:  algorithm.ID,
		AlarmLevelID: levelHighID,
	}).Error; err != nil {
		t.Fatalf("create task device algorithm failed: %v", err)
	}

	llmResult := `{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["ALG_FALLBACK"]},"task_results":[{"task_code":"ALG_FALLBACK","task_name":"LLM Fallback Algorithm","alarm":"1","reason":"person detected","object_ids":["OBJ001"]}],"objects":[{"object_id":"OBJ001","task_code":"ALG_FALLBACK","bbox2d":[100,120,360,520],"label":"person","confidence":0.92}]}`
	callbackBody, _ := json.Marshal(map[string]any{
		"camera_id":         device.ID,
		"timestamp":         time.Now().UnixMilli(),
		"detect_mode":       3,
		"snapshot":          "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII=",
		"snapshot_width":    1920,
		"snapshot_height":   1080,
		"detections":        []map[string]any{},
		"algorithm_results": []map[string]any{},
		"llm_result":        llmResult,
	})
	req := httptest.NewRequest(http.MethodPost, "/ai/events", bytes.NewReader(callbackBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "expected-callback-token")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var callbackResp struct {
		Code int `json:"code"`
		Data struct {
			CreatedEventIDs []string `json:"created_event_ids"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &callbackResp); err != nil {
		t.Fatalf("decode callback response failed: %v", err)
	}
	if callbackResp.Code != 0 || len(callbackResp.Data.CreatedEventIDs) == 0 {
		t.Fatalf("expected created_event_ids with llm fallback, got body=%s", rec.Body.String())
	}

	var saved model.AlarmEvent
	if err := s.db.Where("id = ?", callbackResp.Data.CreatedEventIDs[0]).First(&saved).Error; err != nil {
		t.Fatalf("query saved event failed: %v", err)
	}
	if saved.AlgorithmID != algorithm.ID {
		t.Fatalf("unexpected algorithm id: got=%s want=%s", saved.AlgorithmID, algorithm.ID)
	}
	if saved.AlarmLevelID != levelHighID {
		t.Fatalf("expected event alarm level from task-device-algorithm: got=%s want=%s", saved.AlarmLevelID, levelHighID)
	}
	if saved.YoloJSON != "[]" {
		t.Fatalf("expected yolo_json to be empty in hybrid llm mode, got=%s", saved.YoloJSON)
	}
}

func TestAIDetectionLLMOnlyUsesObjectsAsAlarmTruthIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device, _, _, _ := setupSingleAlgorithmEventRuntime(
		t,
		s,
		"llm-only",
		model.AlgorithmModeLarge,
		"ALG_LLM_ONLY_OBJECT",
	)
	saved := postAIDetectionEventAndLoadFirst(t, s, engine, map[string]any{
		"camera_id":         device.ID,
		"timestamp":         time.Now().UnixMilli(),
		"detect_mode":       model.AlgorithmDetectModeLLMOnly,
		"snapshot":          "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII=",
		"snapshot_width":    1920,
		"snapshot_height":   1080,
		"detections":        []map[string]any{{"label": "person", "confidence": 0.95, "box": map[string]any{"x_min": 10, "y_min": 20, "x_max": 120, "y_max": 220}}},
		"algorithm_results": []map[string]any{{"task_code": "ALG_LLM_ONLY_OBJECT", "alarm": 1, "source": "small"}},
		"llm_result":        `{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["ALG_LLM_ONLY_OBJECT"]},"task_results":[{"task_code":"ALG_LLM_ONLY_OBJECT","task_name":"Event Priority Algorithm llm-only","alarm":"0","reason":"task_results alarm ignored","object_ids":["OBJ001"]}],"objects":[{"object_id":"OBJ001","task_code":"ALG_LLM_ONLY_OBJECT","bbox2d":[260,280,480,700],"label":"person","confidence":0.9}]}`,
	})

	boxes := decodeNormalizedBoxesForTest(t, saved.BoxesJSON)
	if len(boxes) != 1 {
		t.Fatalf("expected exactly 1 llm box, got=%d boxes=%+v", len(boxes), boxes)
	}
	if boxes[0].X <= 0.36 || boxes[0].X >= 0.38 {
		t.Fatalf("expected llm object box to drive event creation, got boxes=%+v", boxes)
	}
	if saved.YoloJSON != "[]" {
		t.Fatalf("expected yolo_json to be empty in llm-only mode, got=%s", saved.YoloJSON)
	}
}

func TestAIDetectionLLMOnlyDoesNotCreateEventWhenTaskResultAlarmHasNoObjectsIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device, _, _, _ := setupSingleAlgorithmEventRuntime(
		t,
		s,
		"llm-only-empty",
		model.AlgorithmModeLarge,
		"ALG_LLM_ONLY_EMPTY",
	)
	createdIDs := postAIDetectionEvent(t, engine, map[string]any{
		"camera_id":         device.ID,
		"timestamp":         time.Now().UnixMilli(),
		"detect_mode":       model.AlgorithmDetectModeLLMOnly,
		"snapshot":          "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII=",
		"snapshot_width":    1920,
		"snapshot_height":   1080,
		"detections":        []map[string]any{{"label": "person", "confidence": 0.95, "box": map[string]any{"x_min": 10, "y_min": 20, "x_max": 120, "y_max": 220}}},
		"algorithm_results": []map[string]any{{"task_code": "ALG_LLM_ONLY_EMPTY", "alarm": 1, "source": "llm"}},
		"llm_result":        `{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["ALG_LLM_ONLY_EMPTY"]},"task_results":[{"task_code":"ALG_LLM_ONLY_EMPTY","task_name":"Event Priority Algorithm llm-only-empty","alarm":"1","reason":"alarm without objects","object_ids":[]}],"objects":[]}`,
	})
	if len(createdIDs) != 0 {
		t.Fatalf("expected no event when llm task_result alarm has no objects, got ids=%v", createdIDs)
	}
}

func TestAIDetectionHybridIgnoresYoloResultsAndUsesLLMObjectsIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device, algorithm, _, _ := setupSingleAlgorithmEventRuntime(
		t,
		s,
		"both",
		model.AlgorithmModeHybrid,
		"ALG_COORD_PRIORITY_BOTH",
	)
	saved := postAIDetectionEventAndLoadFirst(t, s, engine, map[string]any{
		"camera_id":       device.ID,
		"timestamp":       time.Now().UnixMilli(),
		"detect_mode":     3,
		"snapshot":        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII=",
		"snapshot_width":  1920,
		"snapshot_height": 1080,
		"detections": []map[string]any{
			{
				"label":      "person",
				"confidence": 0.91,
				"box": map[string]any{
					"x_min": 10,
					"y_min": 20,
					"x_max": 120,
					"y_max": 220,
				},
			},
		},
		"algorithm_results": []map[string]any{
			{
				"algorithm_id": algorithm.ID,
				"task_code":    algorithm.Code,
				"alarm":        1,
				"source":       "small",
				"boxes": []map[string]any{
					{
						"label":      "person",
						"confidence": 0.91,
						"box": map[string]any{
							"x_min": 10,
							"y_min": 20,
							"x_max": 120,
							"y_max": 220,
						},
					},
				},
			},
			{
				"algorithm_id": algorithm.ID,
				"task_code":    algorithm.Code,
				"alarm":        1,
				"source":       "llm",
				"boxes": []map[string]any{
					{
						"label":      "person",
						"confidence": 0.88,
						"box": map[string]any{
							"x_min": 300,
							"y_min": 320,
							"x_max": 500,
							"y_max": 620,
						},
					},
				},
			},
		},
		"llm_result": `{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["ALG_COORD_PRIORITY_BOTH"]},"task_results":[{"task_code":"ALG_COORD_PRIORITY_BOTH","task_name":"Event Priority Algorithm both","alarm":"1","reason":"person detected","object_ids":["OBJ001"]}],"objects":[{"object_id":"OBJ001","task_code":"ALG_COORD_PRIORITY_BOTH","bbox2d":[300,320,500,620],"label":"person","confidence":0.88}]}`,
	})

	boxes := decodeNormalizedBoxesForTest(t, saved.BoxesJSON)
	if len(boxes) != 1 {
		t.Fatalf("expected exactly 1 selected box, got=%d boxes=%+v", len(boxes), boxes)
	}
	if boxes[0].X <= 0.39 || boxes[0].X >= 0.41 {
		t.Fatalf("expected llm box to be selected, got boxes=%+v", boxes)
	}
	if saved.YoloJSON != "[]" {
		t.Fatalf("expected yolo_json to be empty when hybrid mode ignores yolo results, got=%s", saved.YoloJSON)
	}
}

func TestAIDetectionHybridDoesNotFallbackToSmallBoxesWhenLLMObjectsMissingIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device, algorithm, _, _ := setupSingleAlgorithmEventRuntime(
		t,
		s,
		"llm-empty",
		model.AlgorithmModeHybrid,
		"ALG_COORD_PRIORITY_EMPTY",
	)
	createdIDs := postAIDetectionEvent(t, engine, map[string]any{
		"camera_id":       device.ID,
		"timestamp":       time.Now().UnixMilli(),
		"detect_mode":     3,
		"snapshot":        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII=",
		"snapshot_width":  1920,
		"snapshot_height": 1080,
		"detections": []map[string]any{
			{
				"label":      "person",
				"confidence": 0.92,
				"box": map[string]any{
					"x_min": 10,
					"y_min": 20,
					"x_max": 120,
					"y_max": 220,
				},
			},
		},
		"algorithm_results": []map[string]any{
			{
				"algorithm_id": algorithm.ID,
				"task_code":    algorithm.Code,
				"alarm":        1,
				"source":       "llm",
				"boxes":        []map[string]any{},
			},
		},
	})
	if len(createdIDs) != 0 {
		t.Fatalf("expected no event when llm objects are missing, got ids=%v", createdIDs)
	}
}

func TestAIDetectionFallbackPrefersLLMBoxesOverSmallIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device, _, _, _ := setupSingleAlgorithmEventRuntime(
		t,
		s,
		"fallback",
		model.AlgorithmModeHybrid,
		"ALG_COORD_PRIORITY_FALLBACK",
	)
	llmResult := `{"version":"1.0","overall":{"alarm":"1","alarm_task_codes":["ALG_COORD_PRIORITY_FALLBACK"]},"task_results":[{"task_code":"ALG_COORD_PRIORITY_FALLBACK","task_name":"Event Priority Algorithm fallback","alarm":"1","reason":"person detected","object_ids":["OBJ001"]}],"objects":[{"object_id":"OBJ001","task_code":"ALG_COORD_PRIORITY_FALLBACK","bbox2d":[200,200,400,500],"label":"person","confidence":0.92}]}`
	saved := postAIDetectionEventAndLoadFirst(t, s, engine, map[string]any{
		"camera_id":       device.ID,
		"timestamp":       time.Now().UnixMilli(),
		"detect_mode":     3,
		"snapshot":        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII=",
		"snapshot_width":  1920,
		"snapshot_height": 1080,
		"detections": []map[string]any{
			{
				"label":      "person",
				"confidence": 0.93,
				"box": map[string]any{
					"x_min": 10,
					"y_min": 20,
					"x_max": 120,
					"y_max": 220,
				},
			},
		},
		"llm_result": llmResult,
	})

	boxes := decodeNormalizedBoxesForTest(t, saved.BoxesJSON)
	if len(boxes) != 1 {
		t.Fatalf("expected llm-priority fallback box, got=%d boxes=%+v", len(boxes), boxes)
	}
	if boxes[0].X <= 0.29 || boxes[0].X >= 0.31 {
		t.Fatalf("expected llm box to be selected in fallback path, got boxes=%+v", boxes)
	}
	if saved.YoloJSON != "[]" {
		t.Fatalf("expected yolo_json to be empty in hybrid llm fallback path, got=%s", saved.YoloJSON)
	}
}

func TestEventClipFileEndpointPathSecurityIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-event-clip-1",
		Name:            "Event Clip Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_event_clip_1",
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

	eventID := "event-clip-route-1"
	clipRelPath := filepath.ToSlash(filepath.Join(alarmClipDirName, eventID, "001_demo.mp4"))
	deviceDir, err := s.safeRecordingDeviceDir(device.ID)
	if err != nil {
		t.Fatalf("resolve recording dir failed: %v", err)
	}
	clipAbs := filepath.Join(deviceDir, filepath.FromSlash(clipRelPath))
	if err := os.MkdirAll(filepath.Dir(clipAbs), 0o755); err != nil {
		t.Fatalf("mkdir clip dir failed: %v", err)
	}
	clipBody := []byte("fake mp4 data")
	if err := os.WriteFile(clipAbs, clipBody, 0o644); err != nil {
		t.Fatalf("write clip file failed: %v", err)
	}

	event := model.AlarmEvent{
		ID:             eventID,
		TaskID:         "task-clip-route-1",
		DeviceID:       device.ID,
		AlgorithmID:    "alg-clip-route-1",
		AlarmLevelID:   "level-clip-route-1",
		Status:         model.EventStatusPending,
		OccurredAt:     time.Now(),
		SourceCallback: "{}",
		ClipReady:      true,
		ClipPath:       filepath.ToSlash(filepath.Join(alarmClipDirName, eventID)),
		ClipFilesJSON:  `["` + clipRelPath + `"]`,
	}
	if err := s.db.Create(&event).Error; err != nil {
		t.Fatalf("create event failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")

	okReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/events/"+eventID+"/clips/file/"+clipRelPath,
		nil,
	)
	okReq.Header.Set("Authorization", "Bearer "+token)
	okRec := httptest.NewRecorder()
	engine.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("clip fetch failed: status=%d body=%s", okRec.Code, okRec.Body.String())
	}
	if !bytes.Equal(okRec.Body.Bytes(), clipBody) {
		t.Fatalf("clip file body mismatch")
	}

	badReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/events/"+eventID+"/clips/file/"+filepath.ToSlash(filepath.Join(alarmClipDirName, "other_event", "x.mp4")),
		nil,
	)
	badReq.Header.Set("Authorization", "Bearer "+token)
	badRec := httptest.NewRecorder()
	engine.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid clip path, got=%d body=%s", badRec.Code, badRec.Body.String())
	}
}

func TestDeviceRecordingsListExcludesAlarmClipsIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-recordings-filter-1",
		Name:            "Recording Filter Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_recordings_filter_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live",
		EnableRecording: true,
		RecordingMode:   "continuous",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	deviceDir, err := s.safeRecordingDeviceDir(device.ID)
	if err != nil {
		t.Fatalf("resolve recording dir failed: %v", err)
	}
	normalRel := filepath.ToSlash(filepath.Join("normal", "20260225_101010.mp4"))
	normalAbs := filepath.Join(deviceDir, filepath.FromSlash(normalRel))
	if err := os.MkdirAll(filepath.Dir(normalAbs), 0o755); err != nil {
		t.Fatalf("mkdir normal dir failed: %v", err)
	}
	if err := os.WriteFile(normalAbs, []byte("normal"), 0o644); err != nil {
		t.Fatalf("write normal recording failed: %v", err)
	}

	alarmRel := filepath.ToSlash(filepath.Join(alarmClipDirName, "event-x", "001_alarm.mp4"))
	alarmAbs := filepath.Join(deviceDir, filepath.FromSlash(alarmRel))
	if err := os.MkdirAll(filepath.Dir(alarmAbs), 0o755); err != nil {
		t.Fatalf("mkdir alarm dir failed: %v", err)
	}
	if err := os.WriteFile(alarmAbs, []byte("alarm"), 0o644); err != nil {
		t.Fatalf("write alarm recording failed: %v", err)
	}
	if err := os.Chtimes(alarmAbs, time.Now(), time.Now()); err != nil {
		t.Fatalf("set alarm recording mod time failed: %v", err)
	}

	alarmNewRel := filepath.ToSlash(filepath.Join(alarmClipDirName, "event-y", "001_alarm_new.mp4"))
	alarmNewAbs := filepath.Join(deviceDir, filepath.FromSlash(alarmNewRel))
	if err := os.MkdirAll(filepath.Dir(alarmNewAbs), 0o755); err != nil {
		t.Fatalf("mkdir alarm new dir failed: %v", err)
	}
	if err := os.WriteFile(alarmNewAbs, []byte("alarm-new"), 0o644); err != nil {
		t.Fatalf("write alarm new recording failed: %v", err)
	}
	if err := os.Chtimes(alarmNewAbs, time.Now().Add(-30*time.Minute), time.Now().Add(-30*time.Minute)); err != nil {
		t.Fatalf("set alarm new recording mod time failed: %v", err)
	}

	eventOld := model.AlarmEvent{
		ID:             "event-recording-order-old",
		TaskID:         "task-recording-order",
		DeviceID:       device.ID,
		AlgorithmID:    "alg-recording-order",
		AlarmLevelID:   "level-recording-order",
		Status:         model.EventStatusPending,
		OccurredAt:     time.Now().Add(-2 * time.Hour),
		SourceCallback: "{}",
		ClipReady:      true,
		ClipPath:       filepath.ToSlash(filepath.Join(alarmClipDirName, "event-x")),
		ClipFilesJSON:  `["` + alarmRel + `"]`,
	}
	eventNew := model.AlarmEvent{
		ID:             "event-recording-order-new",
		TaskID:         "task-recording-order",
		DeviceID:       device.ID,
		AlgorithmID:    "alg-recording-order",
		AlarmLevelID:   "level-recording-order",
		Status:         model.EventStatusPending,
		OccurredAt:     time.Now().Add(-1 * time.Hour),
		SourceCallback: "{}",
		ClipReady:      true,
		ClipPath:       filepath.ToSlash(filepath.Join(alarmClipDirName, "event-y")),
		ClipFilesJSON:  `["` + alarmNewRel + `"]`,
	}
	if err := s.db.Create(&eventOld).Error; err != nil {
		t.Fatalf("create old event failed: %v", err)
	}
	if err := s.db.Create(&eventNew).Error; err != nil {
		t.Fatalf("create new event failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+device.ID+"/recordings", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list recordings failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				Path string `json:"path"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode recording list failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected recording list response: %s", rec.Body.String())
	}
	if len(resp.Data.Items) == 0 {
		t.Fatalf("expected normal recording in list")
	}
	for _, item := range resp.Data.Items {
		if strings.HasPrefix(filepath.ToSlash(item.Path), alarmClipDirName+"/") {
			t.Fatalf("alarm clip file should be excluded from device recordings list: %s", item.Path)
		}
	}

	alarmReq := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+device.ID+"/recordings?kind=alarm", nil)
	alarmReq.Header.Set("Authorization", "Bearer "+token)
	alarmRec := httptest.NewRecorder()
	engine.ServeHTTP(alarmRec, alarmReq)
	if alarmRec.Code != http.StatusOK {
		t.Fatalf("list alarm recordings failed: status=%d body=%s", alarmRec.Code, alarmRec.Body.String())
	}

	var alarmResp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				Path            string     `json:"path"`
				Kind            string     `json:"kind"`
				EventID         string     `json:"event_id"`
				EventOccurredAt *time.Time `json:"event_occurred_at"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(alarmRec.Body.Bytes(), &alarmResp); err != nil {
		t.Fatalf("decode alarm recording list failed: %v", err)
	}
	if alarmResp.Code != 0 {
		t.Fatalf("unexpected alarm recording list response: %s", alarmRec.Body.String())
	}
	if len(alarmResp.Data.Items) != 2 {
		t.Fatalf("expected 2 alarm recordings, got %d", len(alarmResp.Data.Items))
	}
	if alarmResp.Data.Items[0].Path != alarmNewRel || alarmResp.Data.Items[1].Path != alarmRel {
		t.Fatalf("unexpected alarm recording order: %+v", alarmResp.Data.Items)
	}
	if alarmResp.Data.Items[0].Kind != recordingKindAlarm || alarmResp.Data.Items[1].Kind != recordingKindAlarm {
		t.Fatalf("unexpected alarm recording kind: %+v", alarmResp.Data.Items)
	}
	if alarmResp.Data.Items[0].EventID != eventNew.ID || alarmResp.Data.Items[1].EventID != eventOld.ID {
		t.Fatalf("unexpected alarm event mapping: %+v", alarmResp.Data.Items)
	}
	if alarmResp.Data.Items[0].EventOccurredAt == nil || alarmResp.Data.Items[1].EventOccurredAt == nil {
		t.Fatalf("expected event occurred_at for alarm recordings")
	}
	if !alarmResp.Data.Items[0].EventOccurredAt.After(*alarmResp.Data.Items[1].EventOccurredAt) {
		t.Fatalf("alarm recordings should be sorted by event_occurred_at desc")
	}
}

func TestDeleteRecordingsSyncsEventClipFieldsIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-recordings-delete-sync-1",
		Name:            "Recording Delete Sync Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_recordings_delete_sync_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live",
		EnableRecording: true,
		RecordingMode:   "continuous",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	deviceDir, err := s.safeRecordingDeviceDir(device.ID)
	if err != nil {
		t.Fatalf("resolve recording dir failed: %v", err)
	}
	alarmRel := filepath.ToSlash(filepath.Join(alarmClipDirName, "event-delete-sync", "001_alarm.mp4"))
	alarmAbs := filepath.Join(deviceDir, filepath.FromSlash(alarmRel))
	if err := os.MkdirAll(filepath.Dir(alarmAbs), 0o755); err != nil {
		t.Fatalf("mkdir alarm dir failed: %v", err)
	}
	if err := os.WriteFile(alarmAbs, []byte("alarm"), 0o644); err != nil {
		t.Fatalf("write alarm recording failed: %v", err)
	}

	event := model.AlarmEvent{
		ID:             "event-delete-sync-1",
		TaskID:         "task-delete-sync-1",
		DeviceID:       device.ID,
		AlgorithmID:    "alg-delete-sync-1",
		AlarmLevelID:   "level-delete-sync-1",
		Status:         model.EventStatusPending,
		OccurredAt:     time.Now(),
		SourceCallback: "{}",
		ClipReady:      true,
		ClipPath:       filepath.ToSlash(filepath.Join(alarmClipDirName, "event-delete-sync")),
		ClipFilesJSON:  `["` + alarmRel + `"]`,
	}
	if err := s.db.Create(&event).Error; err != nil {
		t.Fatalf("create event failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	payload := map[string]any{
		"paths": []string{alarmRel},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/"+device.ID+"/recordings", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete recordings failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var deleteResp struct {
		Code int `json:"code"`
		Data struct {
			Removed []string `json:"removed"`
			Summary struct {
				Removed int `json:"removed"`
				Failed  int `json:"failed"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &deleteResp); err != nil {
		t.Fatalf("decode delete recordings response failed: %v", err)
	}
	if deleteResp.Code != 0 {
		t.Fatalf("unexpected delete recordings response: %s", rec.Body.String())
	}
	if deleteResp.Data.Summary.Removed != 1 || deleteResp.Data.Summary.Failed != 0 {
		t.Fatalf("unexpected delete summary: %+v", deleteResp.Data.Summary)
	}
	if len(deleteResp.Data.Removed) != 1 || deleteResp.Data.Removed[0] != alarmRel {
		t.Fatalf("unexpected removed paths: %+v", deleteResp.Data.Removed)
	}

	var updated model.AlarmEvent
	if err := s.db.Where("id = ?", event.ID).First(&updated).Error; err != nil {
		t.Fatalf("query updated event failed: %v", err)
	}
	if strings.TrimSpace(updated.ClipFilesJSON) != "[]" {
		t.Fatalf("expected clip_files_json to be empty array, got %q", updated.ClipFilesJSON)
	}
	if strings.TrimSpace(updated.ClipPath) != "" {
		t.Fatalf("expected clip_path to be empty, got %q", updated.ClipPath)
	}
	if !updated.ClipReady {
		t.Fatalf("expected clip_ready to remain true")
	}
}

func TestExportRecordingsIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-recordings-export-1",
		Name:            "Recording Export Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_recordings_export_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live",
		EnableRecording: true,
		RecordingMode:   "continuous",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	deviceDir, err := s.safeRecordingDeviceDir(device.ID)
	if err != nil {
		t.Fatalf("resolve recording dir failed: %v", err)
	}
	relA := filepath.ToSlash(filepath.Join("normal", "a.mp4"))
	relB := filepath.ToSlash(filepath.Join("normal", "b.mp4"))
	absA := filepath.Join(deviceDir, filepath.FromSlash(relA))
	absB := filepath.Join(deviceDir, filepath.FromSlash(relB))
	if err := os.MkdirAll(filepath.Dir(absA), 0o755); err != nil {
		t.Fatalf("mkdir export dir failed: %v", err)
	}
	if err := os.WriteFile(absA, []byte("clip-a"), 0o644); err != nil {
		t.Fatalf("write file a failed: %v", err)
	}
	if err := os.WriteFile(absB, []byte("clip-b"), 0o644); err != nil {
		t.Fatalf("write file b failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	payload := map[string]any{
		"paths": []string{relA, relB},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+device.ID+"/recordings/export", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export recordings failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Header().Get("Content-Type")), "application/zip") {
		t.Fatalf("unexpected content type: %s", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(strings.ToLower(rec.Header().Get("Content-Disposition")), ".zip") {
		t.Fatalf("unexpected content disposition: %s", rec.Header().Get("Content-Disposition"))
	}

	zipReader, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("open zip response failed: %v", err)
	}
	if len(zipReader.File) != 2 {
		t.Fatalf("expected 2 zip entries, got %d", len(zipReader.File))
	}
	entrySet := make(map[string]struct{}, len(zipReader.File))
	for _, item := range zipReader.File {
		entrySet[filepath.ToSlash(item.Name)] = struct{}{}
	}
	if _, ok := entrySet[relA]; !ok {
		t.Fatalf("missing zip entry: %s", relA)
	}
	if _, ok := entrySet[relB]; !ok {
		t.Fatalf("missing zip entry: %s", relB)
	}
}

func TestExportRecordingsValidationIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	device := model.Device{
		ID:              "dev-recordings-export-validation-1",
		Name:            "Recording Export Validation Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_recordings_export_validation_1",
		StreamURL:       "rtsp://127.0.0.1:8554/live",
		EnableRecording: true,
		RecordingMode:   "continuous",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")

	emptyPayload := map[string]any{
		"paths": []string{},
	}
	emptyBody, _ := json.Marshal(emptyPayload)
	emptyReq := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+device.ID+"/recordings/export", bytes.NewReader(emptyBody))
	emptyReq.Header.Set("Authorization", "Bearer "+token)
	emptyReq.Header.Set("Content-Type", "application/json")
	emptyRec := httptest.NewRecorder()
	engine.ServeHTTP(emptyRec, emptyReq)
	if emptyRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty paths, got=%d body=%s", emptyRec.Code, emptyRec.Body.String())
	}

	invalidPayload := map[string]any{
		"paths": []string{"../outside.mp4"},
	}
	invalidBody, _ := json.Marshal(invalidPayload)
	invalidReq := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+device.ID+"/recordings/export", bytes.NewReader(invalidBody))
	invalidReq.Header.Set("Authorization", "Bearer "+token)
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidRec := httptest.NewRecorder()
	engine.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid path, got=%d body=%s", invalidRec.Code, invalidRec.Body.String())
	}

	tooMany := make([]string, 0, 201)
	for i := 0; i < 201; i++ {
		tooMany = append(tooMany, fmt.Sprintf("normal/f-%03d.mp4", i))
	}
	tooManyPayload := map[string]any{
		"paths": tooMany,
	}
	tooManyBody, _ := json.Marshal(tooManyPayload)
	tooManyReq := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+device.ID+"/recordings/export", bytes.NewReader(tooManyBody))
	tooManyReq.Header.Set("Authorization", "Bearer "+token)
	tooManyReq.Header.Set("Content-Type", "application/json")
	tooManyRec := httptest.NewRecorder()
	engine.ServeHTTP(tooManyRec, tooManyReq)
	if tooManyRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for too many paths, got=%d body=%s", tooManyRec.Code, tooManyRec.Body.String())
	}

	deviceDir, err := s.safeRecordingDeviceDir(device.ID)
	if err != nil {
		t.Fatalf("resolve recording dir failed: %v", err)
	}
	largeRel := filepath.ToSlash(filepath.Join("normal", "large-limit-test.mp4"))
	largeAbs := filepath.Join(deviceDir, filepath.FromSlash(largeRel))
	if err := os.MkdirAll(filepath.Dir(largeAbs), 0o755); err != nil {
		t.Fatalf("mkdir recording dir failed: %v", err)
	}
	largeFile, err := os.Create(largeAbs)
	if err != nil {
		t.Fatalf("create large test file failed: %v", err)
	}
	if err := largeFile.Truncate(recordingExportMaxSum + 1); err != nil {
		_ = largeFile.Close()
		t.Skipf("skip export size-limit validation: truncate large file failed: %v", err)
	}
	_ = largeFile.Close()
	largePayload := map[string]any{
		"paths": []string{largeRel},
	}
	largeBody, _ := json.Marshal(largePayload)
	largeReq := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+device.ID+"/recordings/export", bytes.NewReader(largeBody))
	largeReq.Header.Set("Authorization", "Bearer "+token)
	largeReq.Header.Set("Content-Type", "application/json")
	largeRec := httptest.NewRecorder()
	engine.ServeHTTP(largeRec, largeReq)
	if largeRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for total size limit, got=%d body=%s", largeRec.Code, largeRec.Body.String())
	}
}

func TestDeviceSnapshotCaptureSavesLocalFileAndUpdatesDBIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	snapshotImageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII="
	snapshotBody, err := base64.StdEncoding.DecodeString(snapshotImageBase64)
	if err != nil {
		t.Fatalf("decode snapshot image failed: %v", err)
	}

	var zlmServerURL string
	zlmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index/api/getSnap":
			if r.Method != http.MethodGet {
				t.Errorf("expected GET for getSnap, got %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse getSnap form failed: %v", err)
			}
			if strings.TrimSpace(r.Form.Get("url")) == "" {
				t.Errorf("expected getSnap form url")
			}
			if strings.TrimSpace(r.Form.Get("timeout_sec")) == "" {
				t.Errorf("expected getSnap timeout_sec")
			}
			if strings.TrimSpace(r.Form.Get("expire_sec")) == "" {
				t.Errorf("expected getSnap expire_sec")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": zlmServerURL + "/snap/mock_capture.jpg",
			})
		case "/snap/mock_capture.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(snapshotBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer zlmServer.Close()
	zlmServerURL = zlmServer.URL
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	device := model.Device{
		ID:              "dev-snapshot-capture-1",
		Name:            "Snapshot Capture Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePush,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTMP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_snapshot_capture_1",
		StreamURL:       "rtmp://127.0.0.1:11935/live/dev_snapshot_capture_1",
		EnableRecording: false,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		SnapshotURL:     "/api/v1/devices/snapshot/legacy_old.jpg",
		OutputConfig:    `{"rtsp":"rtsp://127.0.0.1:1554/live/dev_snapshot_capture_1"}`,
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}
	legacySnapshotPath := filepath.Join("configs", deviceSnapshotDirName, "legacy_old.jpg")
	if err := os.MkdirAll(filepath.Dir(legacySnapshotPath), 0o755); err != nil {
		t.Fatalf("prepare legacy snapshot dir failed: %v", err)
	}
	if err := os.WriteFile(legacySnapshotPath, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("prepare legacy snapshot file failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+device.ID+"/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("capture snapshot failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			DeviceID    string `json:"device_id"`
			SnapshotURL string `json:"snapshot_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode capture snapshot response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected capture snapshot response: %s", rec.Body.String())
	}
	expectedURL := "/api/v1/devices/snapshot/" + sanitizePathSegment(device.ID) + ".jpg"
	if resp.Data.SnapshotURL != expectedURL {
		t.Fatalf("unexpected snapshot url: got=%s want=%s", resp.Data.SnapshotURL, expectedURL)
	}

	var got model.Device
	if err := s.db.Where("id = ?", device.ID).First(&got).Error; err != nil {
		t.Fatalf("query device failed: %v", err)
	}
	if got.SnapshotURL != expectedURL {
		t.Fatalf("snapshot_url not updated in db: got=%s want=%s", got.SnapshotURL, expectedURL)
	}

	snapshotPath := filepath.Join("configs", deviceSnapshotDirName, sanitizePathSegment(device.ID)+".jpg")
	savedBody, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read saved snapshot failed: %v", err)
	}
	defer func() {
		_ = os.Remove(snapshotPath)
	}()
	if !bytes.Equal(savedBody, snapshotBody) {
		t.Fatalf("saved snapshot body mismatch")
	}
	if _, err := os.Stat(legacySnapshotPath); !os.IsNotExist(err) {
		if err == nil {
			_ = os.Remove(legacySnapshotPath)
		}
		t.Fatalf("legacy snapshot file should be removed, err=%v", err)
	}

	fileReq := httptest.NewRequest(http.MethodGet, resp.Data.SnapshotURL, nil)
	fileReq.Header.Set("Authorization", "Bearer "+token)
	fileRec := httptest.NewRecorder()
	engine.ServeHTTP(fileRec, fileReq)
	if fileRec.Code != http.StatusOK {
		t.Fatalf("get snapshot file failed: status=%d body=%s", fileRec.Code, fileRec.Body.String())
	}
	if !bytes.Equal(fileRec.Body.Bytes(), snapshotBody) {
		t.Fatalf("snapshot file api body mismatch")
	}
}

func TestDeviceSnapshotCaptureSupportsInlineImageFromGetSnapIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	snapshotImageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mNk+M8AAwUBAS8C/qkAAAAASUVORK5CYII="
	snapshotBody, err := base64.StdEncoding.DecodeString(snapshotImageBase64)
	if err != nil {
		t.Fatalf("decode snapshot image failed: %v", err)
	}

	zlmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index/api/getSnap":
			if r.Method != http.MethodGet {
				t.Errorf("expected GET for getSnap, got %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse getSnap form failed: %v", err)
			}
			if strings.TrimSpace(r.Form.Get("url")) == "" {
				t.Errorf("expected getSnap form url")
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(snapshotBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	device := model.Device{
		ID:              "dev-snapshot-inline-1",
		Name:            "Snapshot Inline Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePush,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTMP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_snapshot_inline_1",
		StreamURL:       "rtmp://127.0.0.1:11935/live/dev_snapshot_inline_1",
		EnableRecording: false,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    `{"rtsp":"rtsp://127.0.0.1:1554/live/dev_snapshot_inline_1"}`,
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+device.ID+"/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("capture snapshot failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			SnapshotURL string `json:"snapshot_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode capture snapshot response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected capture snapshot response: %s", rec.Body.String())
	}

	snapshotPath := filepath.Join("configs", deviceSnapshotDirName, sanitizePathSegment(device.ID)+".jpg")
	savedBody, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read saved snapshot failed: %v", err)
	}
	defer func() {
		_ = os.Remove(snapshotPath)
	}()
	if !bytes.Equal(savedBody, snapshotBody) {
		t.Fatalf("saved snapshot body mismatch for inline getSnap")
	}
}

func TestDeviceSnapshotCaptureRejectsDefaultFallbackFromGetSnapIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	zlmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index/api/getSnap":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": "./www/logo.png",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	device := model.Device{
		ID:              "dev-snapshot-default-fallback-1",
		Name:            "Snapshot Default Fallback Device",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePush,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTMP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "dev_snapshot_default_fallback_1",
		StreamURL:       "rtmp://127.0.0.1:11935/live/dev_snapshot_default_fallback_1",
		EnableRecording: false,
		RecordingMode:   "none",
		RecordingStatus: "stopped",
		Status:          "offline",
		AIStatus:        model.DeviceAIStatusIdle,
		OutputConfig:    `{"rtsp":"rtsp://127.0.0.1:1554/live/dev_snapshot_default_fallback_1"}`,
	}
	if err := s.db.Create(&device).Error; err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+device.ID+"/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected snapshot fallback rejection status=%d body=%s", rec.Code, rec.Body.String())
	}

	var got model.Device
	if err := s.db.Where("id = ?", device.ID).First(&got).Error; err != nil {
		t.Fatalf("query device failed: %v", err)
	}
	if strings.TrimSpace(got.SnapshotURL) != "" {
		t.Fatalf("snapshot_url should remain empty when fallback rejected, got=%s", got.SnapshotURL)
	}

	snapshotPath := filepath.Join("configs", deviceSnapshotDirName, sanitizePathSegment(device.ID)+".jpg")
	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		if err == nil {
			_ = os.Remove(snapshotPath)
		}
		t.Fatalf("snapshot file should not be generated when fallback rejected, err=%v", err)
	}
}

func TestDeviceSnapshotFileRejectsPathTraversalIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/snapshot/%2e%2e/%2e%2e/config.toml", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal, got=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTaskQuickUpdateDeviceConfigRestartsCurrentDeviceOnlyIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.Disabled = false

	startReqs := make([]ai.StartCameraRequest, 0, 2)
	stopReqs := make([]ai.StopCameraRequest, 0, 2)
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/api/start_camera":
			var req ai.StartCameraRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad payload", http.StatusBadRequest)
				return
			}
			startReqs = append(startReqs, req)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ai.StartCameraResponse{
				Success:      true,
				Message:      "started",
				CameraID:     req.CameraID,
				SourceWidth:  1920,
				SourceHeight: 1080,
				SourceFPS:    25,
			})
		case "/api/stop_camera":
			var req ai.StopCameraRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad payload", http.StatusBadRequest)
				return
			}
			stopReqs = append(stopReqs, req)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ai.GenericResponse{
				Success: true,
				Message: "stopped",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)
	engine := s.Engine()

	deviceA := model.Device{
		ID:              "dev-quick-config-a",
		Name:            "Quick Config Device A",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "quick_config_a",
		StreamURL:       "rtsp://172.16.10.10:554/live/a",
		EnableRecording: true,
		RecordingMode:   model.RecordingModeNone,
		RecordingStatus: "stopped",
		Status:          "online",
		AIStatus:        model.DeviceAIStatusRunning,
		OutputConfig:    "{}",
	}
	deviceB := model.Device{
		ID:              "dev-quick-config-b",
		Name:            "Quick Config Device B",
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "quick_config_b",
		StreamURL:       "rtsp://172.16.10.11:554/live/b",
		EnableRecording: true,
		RecordingMode:   model.RecordingModeNone,
		RecordingStatus: "stopped",
		Status:          "online",
		AIStatus:        model.DeviceAIStatusRunning,
		OutputConfig:    "{}",
	}
	if err := s.db.Create(&deviceA).Error; err != nil {
		t.Fatalf("create deviceA failed: %v", err)
	}
	if err := s.db.Create(&deviceB).Error; err != nil {
		t.Fatalf("create deviceB failed: %v", err)
	}

	algorithmKeep := model.Algorithm{
		ID:                "alg-quick-config-keep",
		Code:              "ALG_QUICK_KEEP",
		Name:              "Quick Keep Algorithm",
		Mode:              model.AlgorithmModeSmall,
		DetectMode:        model.AlgorithmDetectModeSmallOnly,
		Enabled:           true,
		SmallModelLabel:   "person",
		YoloThreshold:     0.35,
		IOUThreshold:      0.8,
		LabelsTriggerMode: model.LabelsTriggerModeAny,
	}
	algorithmPeer := model.Algorithm{
		ID:                "alg-quick-config-peer",
		Code:              "ALG_QUICK_PEER",
		Name:              "Quick Peer Algorithm",
		Mode:              model.AlgorithmModeSmall,
		DetectMode:        model.AlgorithmDetectModeSmallOnly,
		Enabled:           true,
		SmallModelLabel:   "vehicle",
		YoloThreshold:     0.4,
		IOUThreshold:      0.8,
		LabelsTriggerMode: model.LabelsTriggerModeAny,
	}
	algorithmNew := model.Algorithm{
		ID:                "alg-quick-config-new",
		Code:              "ALG_QUICK_NEW",
		Name:              "Quick New Algorithm",
		Mode:              model.AlgorithmModeSmall,
		DetectMode:        model.AlgorithmDetectModeSmallOnly,
		Enabled:           true,
		SmallModelLabel:   "smoke",
		YoloThreshold:     0.5,
		IOUThreshold:      0.8,
		LabelsTriggerMode: model.LabelsTriggerModeAny,
	}
	if err := s.db.Create([]model.Algorithm{algorithmKeep, algorithmPeer, algorithmNew}).Error; err != nil {
		t.Fatalf("create algorithms failed: %v", err)
	}

	task, profiles, algorithmConfigsByDevice, err := s.validateTaskInput("", taskUpsertRequest{
		Name:  "Quick Config Task",
		Notes: "原始备注",
		DeviceConfigs: []taskDeviceConfigUpsert{
			{
				DeviceID: deviceA.ID,
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{
						AlgorithmID:       algorithmKeep.ID,
						AlarmLevelID:      builtinAlarmLevelID3,
						AlertCycleSeconds: intPtr(123),
					},
				},
				FrameRateMode:    model.FrameRateModeInterval,
				FrameRateValue:   9,
				RecordingPolicy:  model.RecordingPolicyNone,
				AlarmPreSeconds:  16,
				AlarmPostSeconds: 24,
			},
			{
				DeviceID: deviceB.ID,
				AlgorithmConfigs: []taskAlgorithmConfigUpsert{
					{
						AlgorithmID:       algorithmPeer.ID,
						AlarmLevelID:      builtinAlarmLevelID2,
						AlertCycleSeconds: intPtr(45),
					},
				},
				FrameRateMode:    model.FrameRateModeFPS,
				FrameRateValue:   3,
				RecordingPolicy:  model.RecordingPolicyAlarmClip,
				AlarmPreSeconds:  10,
				AlarmPostSeconds: 14,
			},
		},
	})
	if err != nil {
		t.Fatalf("validate task input failed: %v", err)
	}
	task.ID = "task-quick-config-main"
	task.Status = model.TaskStatusRunning
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		return s.saveTaskDeviceConfigs(tx, task.ID, profiles, algorithmConfigsByDevice)
	}); err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	body, _ := json.Marshal(map[string]any{
		"name":             "Quick Config Task Updated",
		"notes":            "大屏快捷修改备注",
		"recording_policy": model.RecordingPolicyAlarmClip,
		"algorithm_ids":    []string{algorithmKeep.ID, algorithmNew.ID},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tasks/"+task.ID+"/devices/"+deviceA.ID+"/quick-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("quick update failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode quick update response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected response code: body=%s", rec.Body.String())
	}
	if resp.Data.Status != model.TaskStatusRunning {
		t.Fatalf("unexpected task status after quick update: got=%s want=%s", resp.Data.Status, model.TaskStatusRunning)
	}
	if !strings.Contains(resp.Data.Message, "重启设备任务") {
		t.Fatalf("unexpected response message: %s", resp.Data.Message)
	}

	if len(stopReqs) != 1 || stopReqs[0].CameraID != deviceA.ID {
		t.Fatalf("expected only current device stop request, got=%+v", stopReqs)
	}
	if len(startReqs) != 1 || startReqs[0].CameraID != deviceA.ID {
		t.Fatalf("expected only current device start request, got=%+v", startReqs)
	}
	if startReqs[0].DetectRateMode != model.FrameRateModeInterval || startReqs[0].DetectRateValue != 9 {
		t.Fatalf("quick update should preserve current device frame rate config, got mode=%s value=%d", startReqs[0].DetectRateMode, startReqs[0].DetectRateValue)
	}
	startAlgorithmIDs := make(map[string]struct{}, len(startReqs[0].AlgorithmConfigs))
	for _, item := range startReqs[0].AlgorithmConfigs {
		startAlgorithmIDs[item.AlgorithmID] = struct{}{}
	}
	if len(startAlgorithmIDs) != 2 {
		t.Fatalf("expected 2 algorithm configs in start request, got=%d", len(startAlgorithmIDs))
	}
	if _, ok := startAlgorithmIDs[algorithmKeep.ID]; !ok {
		t.Fatalf("existing algorithm missing from start request: %+v", startReqs[0].AlgorithmConfigs)
	}
	if _, ok := startAlgorithmIDs[algorithmNew.ID]; !ok {
		t.Fatalf("new algorithm missing from start request: %+v", startReqs[0].AlgorithmConfigs)
	}

	detail, err := s.loadTaskDetail(task.ID)
	if err != nil {
		t.Fatalf("load task detail failed: %v", err)
	}
	if detail.Task.Name != "Quick Config Task Updated" {
		t.Fatalf("unexpected task name: %s", detail.Task.Name)
	}
	if detail.Task.Notes != "大屏快捷修改备注" {
		t.Fatalf("unexpected task notes: %s", detail.Task.Notes)
	}
	if detail.Task.Status != model.TaskStatusRunning {
		t.Fatalf("unexpected stored task status: %s", detail.Task.Status)
	}

	var configA *taskDeviceConfigDetail
	var configB *taskDeviceConfigDetail
	for i := range detail.DeviceConfigs {
		switch detail.DeviceConfigs[i].DeviceID {
		case deviceA.ID:
			configA = &detail.DeviceConfigs[i]
		case deviceB.ID:
			configB = &detail.DeviceConfigs[i]
		}
	}
	if configA == nil || configB == nil {
		t.Fatalf("task detail device configs incomplete: %+v", detail.DeviceConfigs)
	}
	if configA.RecordingPolicy != model.RecordingPolicyAlarmClip {
		t.Fatalf("unexpected deviceA recording policy: %s", configA.RecordingPolicy)
	}
	if configA.FrameRateMode != model.FrameRateModeInterval || configA.FrameRateValue != 9 {
		t.Fatalf("deviceA frame rate config should stay unchanged, got mode=%s value=%d", configA.FrameRateMode, configA.FrameRateValue)
	}
	if configA.AlarmPreSeconds != 16 || configA.AlarmPostSeconds != 24 {
		t.Fatalf("deviceA recording window should stay unchanged, got pre=%d post=%d", configA.AlarmPreSeconds, configA.AlarmPostSeconds)
	}
	if len(configA.AlgorithmConfigs) != 2 {
		t.Fatalf("expected 2 algorithms on deviceA, got=%d", len(configA.AlgorithmConfigs))
	}
	configAAlgorithms := make(map[string]taskAlgorithmConfigDetail, len(configA.AlgorithmConfigs))
	for _, item := range configA.AlgorithmConfigs {
		configAAlgorithms[item.AlgorithmID] = item
	}
	if got := configAAlgorithms[algorithmKeep.ID]; got.AlertCycleSeconds != 123 || got.AlarmLevelID != builtinAlarmLevelID3 {
		t.Fatalf("existing algorithm config should be preserved, got=%+v", got)
	}
	if got := configAAlgorithms[algorithmNew.ID]; got.AlertCycleSeconds != 60 || got.AlarmLevelID != builtinAlarmLevelID1 {
		t.Fatalf("new algorithm config should use defaults, got=%+v", got)
	}

	if configB.RecordingPolicy != model.RecordingPolicyAlarmClip {
		t.Fatalf("deviceB recording policy should remain unchanged, got=%s", configB.RecordingPolicy)
	}
	if len(configB.AlgorithmConfigs) != 1 || configB.AlgorithmConfigs[0].AlgorithmID != algorithmPeer.ID {
		t.Fatalf("deviceB algorithm binding should remain unchanged, got=%+v", configB.AlgorithmConfigs)
	}
	if configB.AlgorithmConfigs[0].AlertCycleSeconds != 45 || configB.AlgorithmConfigs[0].AlarmLevelID != builtinAlarmLevelID2 {
		t.Fatalf("deviceB algorithm params should remain unchanged, got=%+v", configB.AlgorithmConfigs[0])
	}

	var refreshedDevices []model.Device
	if err := s.db.Where("id IN ?", []string{deviceA.ID, deviceB.ID}).Order("id asc").Find(&refreshedDevices).Error; err != nil {
		t.Fatalf("query refreshed devices failed: %v", err)
	}
	deviceStatus := make(map[string]string, len(refreshedDevices))
	for _, item := range refreshedDevices {
		deviceStatus[item.ID] = item.AIStatus
	}
	if deviceStatus[deviceA.ID] != model.DeviceAIStatusRunning {
		t.Fatalf("deviceA should be running after quick update, got=%s", deviceStatus[deviceA.ID])
	}
	if deviceStatus[deviceB.ID] != model.DeviceAIStatusRunning {
		t.Fatalf("deviceB should stay running, got=%s", deviceStatus[deviceB.ID])
	}
}

func intPtr(v int) *int {
	return &v
}
