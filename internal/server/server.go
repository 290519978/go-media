package server

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"maas-box/internal/ai"
	"maas-box/internal/config"
	"maas-box/internal/gb28181"
	"maas-box/internal/logutil"
	"maas-box/internal/model"
	"maas-box/internal/ws"
)

type Server struct {
	cfg          *config.Config
	webFS        fs.FS
	db           *gorm.DB
	aiClient     *ai.Client
	wsHub        *ws.Hub
	gbService    *gb28181.Service
	jwtSecret    []byte
	version      string
	startedAt    time.Time
	yoloLabelsMu sync.RWMutex
	yoloLabels   []yoloLabelItem

	draftAlgorithmTestMu   sync.RWMutex
	draftAlgorithmTestJobs map[string]*draftAlgorithmTestJob
	camera2PatrolJobMu     sync.RWMutex
	camera2PatrolJobs      map[string]*camera2PatrolJob

	runtimeMetricsMu         sync.Mutex
	runtimeNetLastSampleAt   time.Time
	runtimeNetLastRXBytes    uint64
	runtimeNetLastTXBytes    uint64
	runtimeNetCurrentRXBPS   float64
	runtimeNetCurrentTXBPS   float64
	dashboardOverviewCache   dashboardOverviewPayload
	dashboardOverviewCacheAt time.Time
	dashboardOverviewMu      sync.Mutex

	recordingMu            sync.Mutex
	alarmRecordingSeq      map[string]uint64
	alarmRecordingTimers   map[string]*time.Timer
	alarmClipFinalizeMu    sync.Mutex
	alarmClipSessionMu     sync.Mutex
	alarmClipSessionSeq    map[string]uint64
	alarmClipSessionTimers map[string]*time.Timer

	gbInviteMu      sync.Mutex
	gbInviteRunning map[string]bool
	gbInvitePending map[string]bool

	gbInviteChannelMu      sync.Mutex
	gbInviteChannelRunning map[string]bool
	gbInviteChannelPending map[string]bool

	pullHealMu      sync.Mutex
	pullHealRunning map[string]bool
	pullHealPending map[string]bool

	zlmProxySourceMu    sync.Mutex
	zlmProxySourceLocks map[string]*sync.Mutex

	zlmRecoverMu      sync.Mutex
	zlmRecoverRunning bool
	zlmRecoverPending bool

	llmQuotaStopMu      sync.Mutex
	llmQuotaStopRunning bool
	llmQuotaNoticeMu    sync.Mutex

	startupResumeMu               sync.Mutex
	startupResumePending          map[string]string
	startupTaskResumePending      map[string]struct{}
	startupAISyncPending          bool
	startupTaskResumeRunning      bool
	startupTaskResumeQueued       bool
	startupTaskResumeRetryRunning bool
}

const (
	llmRoleMarkdownPath              = "./configs/llm/llm_role.md"
	llmOutputRequirementMarkdownPath = "./configs/llm/llm_output_requirement.md"
)

var startupTaskResumeRetryDelays = []time.Duration{
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	20 * time.Second,
	30 * time.Second,
}

var startupTaskResumeRetryWindow = 5 * time.Minute

func New(cfg *config.Config, opts ...Option) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	logutil.SetLevel(cfg.Log.Level)
	db, err := openDB(cfg)
	if err != nil {
		return nil, err
	}

	timeout, err := time.ParseDuration(cfg.Server.AI.RequestTimeout)
	if err != nil {
		timeout = 10 * time.Second
	}

	version := strings.TrimSpace(cfg.Server.Version)
	if version == "" {
		version = strings.TrimSpace(os.Getenv("MB_VERSION"))
	}
	if version == "" {
		version = "dev"
	}

	s := &Server{
		cfg:                      cfg,
		db:                       db,
		aiClient:                 ai.NewClient(cfg.Server.AI.ServiceURL, timeout),
		wsHub:                    ws.NewHub(),
		jwtSecret:                []byte(cfg.Server.HTTP.JwtSecret),
		version:                  version,
		startedAt:                time.Now(),
		draftAlgorithmTestJobs:   make(map[string]*draftAlgorithmTestJob),
		camera2PatrolJobs:        make(map[string]*camera2PatrolJob),
		alarmRecordingSeq:        make(map[string]uint64),
		alarmRecordingTimers:     make(map[string]*time.Timer),
		alarmClipSessionSeq:      make(map[string]uint64),
		alarmClipSessionTimers:   make(map[string]*time.Timer),
		gbInviteRunning:          make(map[string]bool),
		gbInvitePending:          make(map[string]bool),
		gbInviteChannelRunning:   make(map[string]bool),
		gbInviteChannelPending:   make(map[string]bool),
		pullHealRunning:          make(map[string]bool),
		pullHealPending:          make(map[string]bool),
		zlmProxySourceLocks:      make(map[string]*sync.Mutex),
		startupResumePending:     make(map[string]string),
		startupTaskResumePending: make(map[string]struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if err := s.loadYoloLabelsOnStartup(); err != nil {
		return nil, err
	}

	if err := s.autoMigrate(); err != nil {
		return nil, err
	}
	if err := s.normalizeLegacyPullRetryCounts(); err != nil {
		return nil, err
	}
	if err := s.normalizeBuiltinAlarmLevels(); err != nil {
		return nil, err
	}
	if err := s.backfillAlgorithmCodes(); err != nil {
		return nil, err
	}
	if err := s.migrateAlgorithmsToHybrid(); err != nil {
		return nil, err
	}
	if err := s.dropDeprecatedAlgorithmColumns(); err != nil {
		return nil, err
	}
	if err := s.backfillAlgorithmDecisionConfigs(); err != nil {
		return nil, err
	}
	if err := s.ensureAlgorithmCodeUniqueIndex(); err != nil {
		return nil, err
	}
	if err := s.ensurePromptVersionUniqueIndex(); err != nil {
		return nil, err
	}
	if err := s.backfillTaskDeviceProfiles(); err != nil {
		return nil, err
	}
	if err := s.migrateTaskRecordingPolicyContinuousToAlarmClip(); err != nil {
		return nil, err
	}
	if err := s.backfillTaskDeviceFrameRates(); err != nil {
		return nil, err
	}
	if err := s.seedDefaults(); err != nil {
		return nil, err
	}
	if err := s.backfillTaskDeviceAlgorithmAlertCycles(); err != nil {
		return nil, err
	}
	if err := s.backfillTaskDeviceProfileAlarmLevels(); err != nil {
		return nil, err
	}
	s.optimizeAlarmEventStorage()
	if err := s.startGB28181(); err != nil {
		return nil, err
	}
	if err := s.recoverAlarmClipSessionsOnStartup(); err != nil {
		return nil, err
	}
	s.startStorageJanitor()
	s.scheduleZLMRestartRecovery("go_startup")
	return s, nil
}

func (s *Server) Engine() *gin.Engine {
	if s.cfg.Server.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	r.GET("/healthz", func(c *gin.Context) {
		s.ok(c, gin.H{"status": "ok"})
	})
	r.GET("/ws/alerts", s.wsHub.Handle)

	// AI 闂佹悶鍎抽崑鐘绘儍閻旂厧绀傞柕澶堝劚缂?
	r.POST("/ai/events", s.handleAIDetectionEvent)
	r.POST("/ai/started", s.handleAIStarted)
	r.POST("/ai/stopped", s.handleAIStopped)
	r.POST("/ai/keepalive", s.handleAIKeepalive)
	s.registerWebhookRoutes(r)

	api := r.Group("/api/v1")
	api.POST("/auth/login", s.login)
	api.GET("/auth/me", s.authMiddleware(), s.me)

	protected := api.Group("")
	protected.Use(s.authMiddleware())

	playbackGroup := protected.Group("")
	s.registerPlaybackRoutes(playbackGroup)

	areaGroup := protected.Group("")
	areaGroup.Use(s.menuPermissionMiddleware("/areas"))
	s.registerAreaRoutes(areaGroup)

	deviceGroup := protected.Group("")
	deviceGroup.Use(s.menuPermissionMiddleware("/devices"))
	s.registerDeviceRoutes(deviceGroup)

	algorithmGroup := protected.Group("")
	algorithmGroup.Use(s.menuPermissionMiddleware("/algorithms"))
	s.registerAlgorithmRoutes(algorithmGroup)

	taskGroup := protected.Group("")
	taskGroup.Use(s.menuPermissionMiddleware("/tasks"))
	s.registerTaskRoutes(taskGroup)

	dashboardGroup := protected.Group("")
	dashboardGroup.Use(s.menuPermissionMiddleware("/dashboard"))
	s.registerDashboardRoutes(dashboardGroup)

	eventGroup := protected.Group("")
	eventGroup.Use(s.menuPermissionMiddleware("/events"), s.gzipEventsMiddleware())
	s.registerEventRoutes(eventGroup)

	systemGroup := protected.Group("")
	systemGroup.Use(s.menuPermissionMiddleware("/system"))
	s.registerSystemRoutes(systemGroup)
	s.registerEmbeddedWebRoutes(r)

	return r
}

func (s *Server) autoMigrate() error {
	if err := s.db.AutoMigrate(
		&model.Area{},
		&model.MediaSource{},
		&model.StreamProxy{},
		&model.StreamPush{},
		&model.GBDeviceBlock{},
		&model.StreamBlock{},
		&model.GBDevice{},
		&model.GBChannel{},
		&model.ModelProvider{},
		&model.Algorithm{},
		&model.AlgorithmPromptVersion{},
		&model.AlarmLevel{},
		&model.VideoTask{},
		&model.VideoTaskDevice{},
		&model.VideoTaskAlgorithm{},
		&model.VideoTaskDeviceProfile{},
		&model.VideoTaskDeviceAlgorithm{},
		&model.AlarmEvent{},
		&model.AlarmClipSession{},
		&model.AlarmClipSessionEvent{},
		&model.AlgorithmTestRecord{},
		&model.AlgorithmTestJob{},
		&model.AlgorithmTestJobItem{},
		&model.LLMUsageCall{},
		&model.LLMUsageHourly{},
		&model.LLMUsageDaily{},
		&model.User{},
		&model.Role{},
		&model.Menu{},
		&model.UserRole{},
		&model.RoleMenu{},
		&model.SystemSetting{},
	); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	return nil
}

func (s *Server) backfillTaskDeviceProfiles() error {
	if s == nil || s.db == nil {
		return nil
	}
	var relDevices []model.VideoTaskDevice
	if err := s.db.Find(&relDevices).Error; err != nil {
		return fmt.Errorf("load legacy task-device relations: %w", err)
	}
	if len(relDevices) == 0 {
		return nil
	}
	taskIDSet := make(map[string]struct{}, len(relDevices))
	deviceIDSet := make(map[string]struct{}, len(relDevices))
	for _, item := range relDevices {
		if id := strings.TrimSpace(item.TaskID); id != "" {
			taskIDSet[id] = struct{}{}
		}
		if id := strings.TrimSpace(item.DeviceID); id != "" {
			deviceIDSet[id] = struct{}{}
		}
	}
	taskIDs := make([]string, 0, len(taskIDSet))
	for id := range taskIDSet {
		taskIDs = append(taskIDs, id)
	}
	deviceIDs := make([]string, 0, len(deviceIDSet))
	for id := range deviceIDSet {
		deviceIDs = append(deviceIDs, id)
	}
	var tasks []model.VideoTask
	if len(taskIDs) > 0 {
		if err := s.db.Where("id IN ?", taskIDs).Find(&tasks).Error; err != nil {
			return fmt.Errorf("load legacy tasks: %w", err)
		}
	}
	taskByID := make(map[string]model.VideoTask, len(tasks))
	for _, item := range tasks {
		taskByID[item.ID] = item
	}
	var devices []model.MediaSource
	if len(deviceIDs) > 0 {
		if err := s.db.Where("id IN ?", deviceIDs).Find(&devices).Error; err != nil {
			return fmt.Errorf("load sources for legacy profiles: %w", err)
		}
	}
	deviceByID := make(map[string]model.MediaSource, len(devices))
	for _, item := range devices {
		deviceByID[item.ID] = item
	}
	var legacyAlgorithms []model.VideoTaskAlgorithm
	if len(taskIDs) > 0 {
		if err := s.db.Where("task_id IN ?", taskIDs).Find(&legacyAlgorithms).Error; err != nil {
			return fmt.Errorf("load legacy task-algorithm relations: %w", err)
		}
	}
	algorithmIDsByTask := make(map[string][]string, len(taskIDs))
	for _, item := range legacyAlgorithms {
		taskID := strings.TrimSpace(item.TaskID)
		algorithmID := strings.TrimSpace(item.AlgorithmID)
		if taskID == "" || algorithmID == "" {
			continue
		}
		algorithmIDsByTask[taskID] = append(algorithmIDsByTask[taskID], algorithmID)
	}

	defaultPre := s.alarmClipDefaultPreSeconds()
	defaultPost := s.alarmClipDefaultPostSeconds()
	defaultFrameRateMode := s.taskFrameRateDefaultMode()
	defaultFrameRateValue := s.taskFrameRateDefaultValue()
	defaultRecordingPolicy := s.taskRecordingPolicyDefault()
	defaultAlarmLevelID, err := s.defaultAlarmLevelID()
	if err != nil {
		return fmt.Errorf("resolve task default alarm level: %w", err)
	}
	defaultAlertCycle := s.taskAlertCycleDefault()

	profiles := make([]model.VideoTaskDeviceProfile, 0, len(relDevices))
	profileAlgorithms := make([]model.VideoTaskDeviceAlgorithm, 0, len(relDevices)*2)
	for _, rel := range relDevices {
		taskID := strings.TrimSpace(rel.TaskID)
		deviceID := strings.TrimSpace(rel.DeviceID)
		if taskID == "" || deviceID == "" {
			continue
		}
		task, ok := taskByID[taskID]
		if !ok {
			continue
		}
		recordingPolicy := defaultRecordingPolicy
		alarmPreSeconds := defaultPre
		alarmPostSeconds := defaultPost
		if device, exists := deviceByID[deviceID]; exists {
			if enabled, mode, err := resolveRecordingPolicyFromConfig(s.cfg, device.EnableRecording, device.RecordingMode); err == nil && enabled && mode == model.RecordingModeContinuous {
				recordingPolicy = model.RecordingPolicyAlarmClip
			} else if device.EnableAlarmClip {
				recordingPolicy = model.RecordingPolicyAlarmClip
			}
			if device.AlarmPreSeconds > 0 {
				alarmPreSeconds = device.AlarmPreSeconds
			}
			if device.AlarmPostSeconds > 0 {
				alarmPostSeconds = device.AlarmPostSeconds
			}
		}
		profiles = append(profiles, model.VideoTaskDeviceProfile{
			TaskID:           taskID,
			DeviceID:         deviceID,
			FrameInterval:    defaultFrameRateValue,
			FrameRateMode:    defaultFrameRateMode,
			FrameRateValue:   defaultFrameRateValue,
			SmallConfidence:  task.SmallConfidence,
			LargeConfidence:  task.LargeConfidence,
			SmallIOU:         task.SmallIOU,
			AlarmLevelID:     firstNonEmpty(strings.TrimSpace(task.AlarmLevelID), defaultAlarmLevelID),
			RecordingPolicy:  recordingPolicy,
			AlarmPreSeconds:  alarmPreSeconds,
			AlarmPostSeconds: alarmPostSeconds,
		})
		for _, algorithmID := range uniqueStrings(algorithmIDsByTask[taskID]) {
			profileAlgorithms = append(profileAlgorithms, model.VideoTaskDeviceAlgorithm{
				TaskID:            taskID,
				DeviceID:          deviceID,
				AlgorithmID:       algorithmID,
				AlarmLevelID:      firstNonEmpty(strings.TrimSpace(task.AlarmLevelID), defaultAlarmLevelID),
				AlertCycleSeconds: defaultAlertCycle,
			})
		}
	}
	if len(profiles) > 0 {
		if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&profiles).Error; err != nil {
			return fmt.Errorf("backfill task device profiles: %w", err)
		}
	}
	if len(profileAlgorithms) > 0 {
		if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&profileAlgorithms).Error; err != nil {
			return fmt.Errorf("backfill task device algorithms: %w", err)
		}
	}
	log.Printf("task device profile backfill completed: legacy_relations=%d profiles=%d algorithms=%d", len(relDevices), len(profiles), len(profileAlgorithms))
	return nil
}

func (s *Server) migrateTaskRecordingPolicyContinuousToAlarmClip() error {
	if s == nil || s.db == nil {
		return nil
	}
	now := time.Now()
	result := s.db.Exec(
		`UPDATE mb_video_task_device_profiles
SET recording_policy = ?,
    alarm_pre_seconds = CASE
        WHEN alarm_pre_seconds < 1 THEN 1
        WHEN alarm_pre_seconds > 600 THEN 600
        ELSE alarm_pre_seconds
    END,
    alarm_post_seconds = CASE
        WHEN alarm_post_seconds < 1 THEN 1
        WHEN alarm_post_seconds > 600 THEN 600
        ELSE alarm_post_seconds
    END,
    updated_at = ?
WHERE recording_policy = ?`,
		model.RecordingPolicyAlarmClip,
		now,
		model.RecordingPolicyContinuous,
	)
	if result.Error != nil {
		return fmt.Errorf("migrate task recording policy continuous->alarm_clip: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		log.Printf("task recording policy migration completed: migrated=%d", result.RowsAffected)
	}
	return nil
}

func (s *Server) backfillTaskDeviceFrameRates() error {
	if s == nil || s.db == nil {
		return nil
	}
	var profiles []model.VideoTaskDeviceProfile
	if err := s.db.Find(&profiles).Error; err != nil {
		return fmt.Errorf("load task device profiles for frame rate backfill: %w", err)
	}
	updated := 0
	for _, profile := range profiles {
		baseValue := profile.FrameRateValue
		if baseValue <= 0 {
			baseValue = profile.FrameInterval
		}
		mode, value, err := s.validateTaskFrameRate(profile.FrameRateMode, baseValue)
		if err != nil {
			mode = s.taskFrameRateDefaultMode()
			value = s.taskFrameRateDefaultValue()
		}
		legacyFrameInterval := profile.FrameInterval
		if legacyFrameInterval <= 0 {
			legacyFrameInterval = value
		}
		if profile.FrameRateMode == mode && profile.FrameRateValue == value && profile.FrameInterval == legacyFrameInterval {
			continue
		}
		if err := s.db.Model(&model.VideoTaskDeviceProfile{}).
			Where("task_id = ? AND device_id = ?", profile.TaskID, profile.DeviceID).
			Updates(map[string]any{
				"frame_rate_mode":  mode,
				"frame_rate_value": value,
				"frame_interval":   legacyFrameInterval,
			}).Error; err != nil {
			return fmt.Errorf("backfill task device frame rate (%s/%s): %w", profile.TaskID, profile.DeviceID, err)
		}
		updated++
	}
	if updated > 0 {
		log.Printf("task device frame rate backfill completed: updated=%d", updated)
	}
	return nil
}

func (s *Server) backfillTaskDeviceAlgorithmAlertCycles() error {
	if s == nil || s.db == nil {
		return nil
	}
	var rows []model.VideoTaskDeviceAlgorithm
	if err := s.db.Find(&rows).Error; err != nil {
		return fmt.Errorf("load task device algorithms for alert cycle backfill: %w", err)
	}
	updated := 0
	for _, row := range rows {
		normalized := s.normalizeAlertCycleSecondsPersisted(row.AlertCycleSeconds)
		if normalized == row.AlertCycleSeconds {
			continue
		}
		if err := s.db.Model(&model.VideoTaskDeviceAlgorithm{}).
			Where("task_id = ? AND device_id = ? AND algorithm_id = ?", row.TaskID, row.DeviceID, row.AlgorithmID).
			Update("alert_cycle_seconds", normalized).Error; err != nil {
			return fmt.Errorf("backfill task device algorithm alert cycle (%s/%s/%s): %w", row.TaskID, row.DeviceID, row.AlgorithmID, err)
		}
		updated++
	}
	if updated > 0 {
		log.Printf("task device algorithm alert cycle backfill completed: updated=%d", updated)
	}
	return nil
}

func (s *Server) backfillTaskDeviceProfileAlarmLevels() error {
	if s == nil || s.db == nil {
		return nil
	}
	defaultAlarmLevelID, err := s.defaultAlarmLevelID()
	if err != nil {
		return fmt.Errorf("resolve default alarm level for profile backfill: %w", err)
	}
	if err := s.db.Model(&model.VideoTaskDeviceProfile{}).
		Where("alarm_level_id = '' OR alarm_level_id IS NULL").
		Update("alarm_level_id", defaultAlarmLevelID).Error; err != nil {
		return fmt.Errorf("backfill task device profile alarm level: %w", err)
	}
	if err := s.db.Model(&model.VideoTask{}).
		Where("alarm_level_id = '' OR alarm_level_id IS NULL").
		Update("alarm_level_id", defaultAlarmLevelID).Error; err != nil {
		return fmt.Errorf("backfill task alarm level: %w", err)
	}
	if err := s.db.Model(&model.VideoTaskDeviceAlgorithm{}).
		Where("alarm_level_id = '' OR alarm_level_id IS NULL").
		Update("alarm_level_id", defaultAlarmLevelID).Error; err != nil {
		return fmt.Errorf("backfill task device algorithm alarm level: %w", err)
	}
	return nil
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.stopAllAlarmRecordingTimers()
	s.stopAllAlarmClipSessionTimers()
	s.closeAllActiveAlarmClipSessionsOnShutdown()
	if s.gbService == nil {
		return nil
	}
	return s.gbService.Close()
}

func (s *Server) seedDefaults() error {
	var areaCount int64
	if err := s.db.Model(&model.Area{}).Where("id = ?", model.RootAreaID).Count(&areaCount).Error; err != nil {
		return err
	}
	if areaCount == 0 {
		if err := s.db.Create(&model.Area{
			ID:       model.RootAreaID,
			Name:     "Root",
			ParentID: "",
			IsRoot:   true,
			Sort:     0,
		}).Error; err != nil {
			return err
		}
	}

	if err := s.upsertSetting("llm_role", s.readPromptMarkdown(llmRoleMarkdownPath)); err != nil {
		return err
	}
	if err := s.upsertSetting("llm_output_requirement", s.readPromptMarkdown(llmOutputRequirementMarkdownPath)); err != nil {
		return err
	}

	if err := s.normalizeBuiltinAlarmLevels(); err != nil {
		return err
	}

	var role model.Role
	if err := s.db.Where("name = ?", "admin").First(&role).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			role = model.Role{ID: uuid.NewString(), Name: "admin", Remark: "System administrator"}
			if err := s.db.Create(&role).Error; err != nil {
				return err
			}
		} else {
			return err
		}
	}

	var user model.User
	if err := s.db.Where("username = ?", s.cfg.Server.Username).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			hash, err := bcrypt.GenerateFromPassword([]byte(s.cfg.Server.Password), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			user = model.User{ID: uuid.NewString(), Username: s.cfg.Server.Username, PasswordHash: string(hash), Enabled: true}
			if err := s.db.Create(&user).Error; err != nil {
				return err
			}
		} else {
			return err
		}
	}

	var userRoleCount int64
	if err := s.db.Model(&model.UserRole{}).
		Where("user_id = ? AND role_id = ?", user.ID, role.ID).
		Count(&userRoleCount).Error; err != nil {
		return err
	}
	if userRoleCount == 0 {
		if err := s.db.Create(&model.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
			return err
		}
	}

	defaultMenus := []model.Menu{
		{ID: "menu_dashboard", Name: "数据看板", Path: "/dashboard", MenuType: "menu", ViewPath: "views/DashboardView.vue", Icon: "AppstoreOutlined", ParentID: "", Sort: 1},
		{ID: "menu_device_dir", Name: "设备管理", Path: "", MenuType: "directory", ViewPath: "", Icon: "CameraOutlined", ParentID: "", Sort: 2},
		{ID: "menu_devices", Name: "摄像头设备", Path: "/devices", MenuType: "menu", ViewPath: "views/devices/DeviceView.vue", Icon: "CameraOutlined", ParentID: "menu_device_dir", Sort: 1},
		{ID: "menu_areas", Name: "区域管理", Path: "/areas", MenuType: "menu", ViewPath: "views/areas/AreaView.vue", Icon: "ClusterOutlined", ParentID: "menu_device_dir", Sort: 2},
		{ID: "menu_algorithms_dir", Name: "算法中心", Path: "", MenuType: "directory", ViewPath: "", Icon: "NodeIndexOutlined", ParentID: "", Sort: 3},
		{ID: "menu_algorithms_manage", Name: "算法管理", Path: "/algorithms/manage", MenuType: "menu", ViewPath: "views/algorithms/AlgorithmManageView.vue", Icon: "NodeIndexOutlined", ParentID: "menu_algorithms_dir", Sort: 1},
		{ID: "menu_algorithms_llm_usage", Name: "LLM用量统计", Path: "/algorithms/llm-usage", MenuType: "menu", ViewPath: "views/algorithms/LLMUsageView.vue", Icon: "NodeIndexOutlined", ParentID: "menu_algorithms_dir", Sort: 2},
		{ID: "menu_tasks_dir", Name: "任务管理", Path: "", MenuType: "directory", ViewPath: "", Icon: "SafetyCertificateOutlined", ParentID: "", Sort: 4},
		{ID: "menu_tasks_video", Name: "视频任务", Path: "/tasks/video", MenuType: "menu", ViewPath: "views/tasks/TaskManageView.vue", Icon: "SafetyCertificateOutlined", ParentID: "menu_tasks_dir", Sort: 1},
		{ID: "menu_tasks_levels", Name: "报警等级", Path: "/tasks/levels", MenuType: "menu", ViewPath: "views/tasks/AlarmLevelView.vue", Icon: "SafetyCertificateOutlined", ParentID: "menu_tasks_dir", Sort: 2},
		{ID: "menu_events_dir", Name: "事件中心", Path: "", MenuType: "directory", ViewPath: "", Icon: "AlertOutlined", ParentID: "", Sort: 5},
		{ID: "menu_events", Name: "报警记录", Path: "/events", MenuType: "menu", ViewPath: "views/events/EventView.vue", Icon: "AlertOutlined", ParentID: "menu_events_dir", Sort: 1},
		{ID: "menu_system_dir", Name: "系统管理", Path: "", MenuType: "directory", ViewPath: "", Icon: "SettingOutlined", ParentID: "", Sort: 6},
		{ID: "menu_system_users", Name: "用户管理", Path: "/system/users", MenuType: "menu", ViewPath: "views/system/SystemUsersView.vue", Icon: "SettingOutlined", ParentID: "menu_system_dir", Sort: 1},
		{ID: "menu_system_roles", Name: "角色管理", Path: "/system/roles", MenuType: "menu", ViewPath: "views/system/SystemRolesView.vue", Icon: "SettingOutlined", ParentID: "menu_system_dir", Sort: 2},
		{ID: "menu_system_menus", Name: "菜单管理", Path: "/system/menus", MenuType: "menu", ViewPath: "views/system/SystemMenusView.vue", Icon: "SettingOutlined", ParentID: "menu_system_dir", Sort: 3},
	}

	for _, menu := range defaultMenus {
		var count int64
		if err := s.db.Model(&model.Menu{}).Where("id = ?", menu.ID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := s.db.Create(&menu).Error; err != nil {
				return err
			}
		}
		if err := s.db.Model(&model.Menu{}).Where("id = ?", menu.ID).Updates(map[string]any{
			"name":      menu.Name,
			"path":      menu.Path,
			"menu_type": menu.MenuType,
			"view_path": menu.ViewPath,
			"icon":      menu.Icon,
			"parent_id": menu.ParentID,
			"sort":      menu.Sort,
		}).Error; err != nil {
			return err
		}
	}
	if err := s.deleteDeprecatedSeedMenus([]string{
		"menu_algorithms_models",
		"menu_system_settings",
	}); err != nil {
		return err
	}

	for _, menu := range defaultMenus {
		var count int64
		if err := s.db.Model(&model.RoleMenu{}).Where("role_id = ? AND menu_id = ?", role.ID, menu.ID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := s.db.Create(&model.RoleMenu{RoleID: role.ID, MenuID: menu.ID}).Error; err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *Server) deleteDeprecatedSeedMenus(menuIDs []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	menuIDs = uniqueStrings(menuIDs)
	if len(menuIDs) == 0 {
		return nil
	}
	if err := s.db.Where("menu_id IN ?", menuIDs).Delete(&model.RoleMenu{}).Error; err != nil {
		return err
	}
	if err := s.db.Where("id IN ?", menuIDs).Delete(&model.Menu{}).Error; err != nil {
		return err
	}
	return nil
}

func (s *Server) migrateAlgorithmsToHybrid() error {
	if s == nil || s.db == nil {
		return nil
	}
	now := time.Now()
	result := s.db.Model(&model.Algorithm{}).
		Where("mode <> ? OR model_provider_id <> ''", model.AlgorithmModeHybrid).
		Updates(map[string]any{
			"mode":              model.AlgorithmModeHybrid,
			"model_provider_id": "",
			"updated_at":        now,
		})
	if result.Error != nil {
		return fmt.Errorf("migrate algorithms to hybrid failed: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		log.Printf("algorithm hybrid migration completed: updated=%d", result.RowsAffected)
	}
	return nil
}

func (s *Server) backfillAlgorithmDecisionConfigs() error {
	if s == nil || s.db == nil {
		return nil
	}
	now := time.Now()
	updates := []struct {
		where string
		args  []any
		data  map[string]any
	}{
		{
			where: "yolo_threshold <= 0 OR yolo_threshold > 1",
			data:  map[string]any{"yolo_threshold": 0.5, "updated_at": now},
		},
		{
			where: "iou_threshold <= 0 OR iou_threshold > 1",
			data:  map[string]any{"iou_threshold": 0.8, "updated_at": now},
		},
		{
			where: "labels_trigger_mode NOT IN (?, ?)",
			args:  []any{model.LabelsTriggerModeAny, model.LabelsTriggerModeAll},
			data:  map[string]any{"labels_trigger_mode": model.LabelsTriggerModeAny, "updated_at": now},
		},
		{
			where: "detect_mode NOT IN (?, ?, ?)",
			args: []any{
				model.AlgorithmDetectModeSmallOnly,
				model.AlgorithmDetectModeLLMOnly,
				model.AlgorithmDetectModeHybrid,
			},
			data: map[string]any{"detect_mode": model.AlgorithmDetectModeHybrid, "updated_at": now},
		},
	}
	for _, item := range updates {
		query := s.db.Model(&model.Algorithm{}).Where(item.where, item.args...)
		if err := query.Updates(item.data).Error; err != nil {
			return fmt.Errorf("backfill algorithm decision config failed (%s): %w", item.where, err)
		}
	}
	return nil
}

func (s *Server) dropDeprecatedAlgorithmColumns() error {
	if s == nil || s.db == nil {
		return nil
	}
	if s.db.Dialector.Name() != "sqlite" {
		return nil
	}
	algorithmModel := &model.Algorithm{}
	columns := []string{
		"llm_trigger_threshold",
		"mode3_small_behavior",
	}
	for _, column := range columns {
		if !s.db.Migrator().HasColumn(algorithmModel, column) {
			continue
		}
		if err := s.db.Migrator().DropColumn(algorithmModel, column); err != nil {
			return fmt.Errorf("drop deprecated algorithm column %s failed: %w", column, err)
		}
		log.Printf("deprecated algorithm column dropped: %s", column)
	}
	return nil
}

func (s *Server) backfillAlgorithmCodes() error {
	if s == nil || s.db == nil {
		return nil
	}
	var rows []model.Algorithm
	if err := s.db.Order("created_at asc").Order("id asc").Find(&rows).Error; err != nil {
		return fmt.Errorf("load algorithms for code backfill: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	validPattern := regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,31}$`)
	used := make(map[string]struct{}, len(rows))
	nextSeq := 1
	allocCode := func() string {
		for {
			candidate := fmt.Sprintf("ALG%03d", nextSeq)
			nextSeq++
			if _, exists := used[candidate]; !exists {
				used[candidate] = struct{}{}
				return candidate
			}
		}
	}
	updated := 0
	for _, row := range rows {
		code := strings.ToUpper(strings.TrimSpace(row.Code))
		desired := code
		if desired == "" || !validPattern.MatchString(desired) {
			desired = allocCode()
		} else {
			if _, exists := used[desired]; exists {
				desired = allocCode()
			} else {
				used[desired] = struct{}{}
			}
		}
		if desired == row.Code {
			continue
		}
		if err := s.db.Model(&model.Algorithm{}).Where("id = ?", row.ID).Update("code", desired).Error; err != nil {
			return fmt.Errorf("backfill algorithm code (%s): %w", row.ID, err)
		}
		updated++
	}
	if updated > 0 {
		log.Printf("algorithm code backfill completed: updated=%d", updated)
	}
	return nil
}

func (s *Server) ensureAlgorithmCodeUniqueIndex() error {
	if s == nil || s.db == nil {
		return nil
	}
	if s.db.Dialector.Name() == "sqlite" {
		if err := s.db.Exec(
			"CREATE UNIQUE INDEX IF NOT EXISTS idx_mb_algorithms_code_non_empty ON mb_algorithms(code) WHERE code <> ''",
		).Error; err != nil {
			return fmt.Errorf("create unique index for algorithm code: %w", err)
		}
	}
	return nil
}

func (s *Server) ensurePromptVersionUniqueIndex() error {
	if s == nil || s.db == nil {
		return nil
	}
	type duplicatePromptVersion struct {
		AlgorithmID string `gorm:"column:algorithm_id"`
		Version     string `gorm:"column:version"`
		DupCount    int64  `gorm:"column:dup_count"`
	}
	var duplicates []duplicatePromptVersion
	if err := s.db.Model(&model.AlgorithmPromptVersion{}).
		Select("algorithm_id, version, COUNT(1) AS dup_count").
		Group("algorithm_id, version").
		Having("COUNT(1) > 1").
		Scan(&duplicates).Error; err != nil {
		return fmt.Errorf("check duplicate prompt versions: %w", err)
	}
	if len(duplicates) > 0 {
		first := duplicates[0]
		return fmt.Errorf(
			"duplicate prompt version found: algorithm_id=%s version=%s count=%d",
			first.AlgorithmID,
			first.Version,
			first.DupCount,
		)
	}
	if s.db.Dialector.Name() == "sqlite" {
		if err := s.db.Exec(
			"CREATE UNIQUE INDEX IF NOT EXISTS uk_mb_algorithm_prompt_version ON mb_algorithm_prompts(algorithm_id, version)",
		).Error; err != nil {
			return fmt.Errorf("create unique index for prompt version: %w", err)
		}
	}
	return nil
}

func (s *Server) upsertSetting(key, value string) error {
	var setting model.SystemSetting
	err := s.db.Where("key = ?", key).First(&setting).Error
	if err == nil {
		setting.Value = value
		return s.db.Save(&setting).Error
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	return s.db.Create(&model.SystemSetting{Key: key, Value: value}).Error
}

func (s *Server) setStartupResumePending(taskID string, deviceIDs []string) {
	if s == nil {
		return
	}
	s.startupResumeMu.Lock()
	defer s.startupResumeMu.Unlock()
	if s.startupResumePending == nil {
		s.startupResumePending = make(map[string]string)
	}
	taskID = strings.TrimSpace(taskID)
	for _, deviceID := range deviceIDs {
		deviceID = strings.TrimSpace(deviceID)
		if deviceID == "" {
			continue
		}
		if taskID == "" {
			delete(s.startupResumePending, deviceID)
			continue
		}
		s.startupResumePending[deviceID] = taskID
	}
}

func (s *Server) clearStartupResumePending(deviceIDs ...string) {
	if s == nil {
		return
	}
	s.startupResumeMu.Lock()
	defer s.startupResumeMu.Unlock()
	for _, deviceID := range deviceIDs {
		deviceID = strings.TrimSpace(deviceID)
		if deviceID == "" {
			continue
		}
		delete(s.startupResumePending, deviceID)
	}
}

func (s *Server) clearStartupResumePendingByTask(taskID string) {
	if s == nil {
		return
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	s.startupResumeMu.Lock()
	defer s.startupResumeMu.Unlock()
	for deviceID, pendingTaskID := range s.startupResumePending {
		if strings.TrimSpace(pendingTaskID) == taskID {
			delete(s.startupResumePending, deviceID)
		}
	}
}

func (s *Server) pendingStartupResumeTaskID(deviceID string) string {
	if s == nil {
		return ""
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return ""
	}
	s.startupResumeMu.Lock()
	defer s.startupResumeMu.Unlock()
	return strings.TrimSpace(s.startupResumePending[deviceID])
}

func (s *Server) setStartupTaskResumePending(taskIDs []string) {
	if s == nil {
		return
	}
	s.startupResumeMu.Lock()
	defer s.startupResumeMu.Unlock()
	if s.startupTaskResumePending == nil {
		s.startupTaskResumePending = make(map[string]struct{})
	}
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		s.startupTaskResumePending[taskID] = struct{}{}
	}
}

func (s *Server) clearStartupTaskResumePending(taskIDs ...string) {
	if s == nil {
		return
	}
	s.startupResumeMu.Lock()
	defer s.startupResumeMu.Unlock()
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		delete(s.startupTaskResumePending, taskID)
	}
}

func (s *Server) clearStartupTaskResumePendingByTask(taskID string) {
	s.clearStartupTaskResumePending(taskID)
}

func (s *Server) pendingStartupTaskResumeIDs() []string {
	if s == nil {
		return nil
	}
	s.startupResumeMu.Lock()
	defer s.startupResumeMu.Unlock()
	if len(s.startupTaskResumePending) == 0 {
		return nil
	}
	taskIDs := make([]string, 0, len(s.startupTaskResumePending))
	for taskID := range s.startupTaskResumePending {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		taskIDs = append(taskIDs, taskID)
	}
	return taskIDs
}

func (s *Server) setStartupAISyncPending(pending bool) {
	if s == nil {
		return
	}
	s.startupResumeMu.Lock()
	defer s.startupResumeMu.Unlock()
	s.startupAISyncPending = pending
}

func (s *Server) hasStartupRecoveryPending() bool {
	if s == nil {
		return false
	}
	s.startupResumeMu.Lock()
	defer s.startupResumeMu.Unlock()
	return s.startupAISyncPending || len(s.startupTaskResumePending) > 0
}

func (s *Server) schedulePendingStartupRecovery(reason string) {
	if s == nil || !s.hasStartupRecoveryPending() {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "startup_pending_resume"
	}
	s.startupResumeMu.Lock()
	if s.startupTaskResumeRunning {
		s.startupTaskResumeQueued = true
		s.startupResumeMu.Unlock()
		return
	}
	s.startupTaskResumeRunning = true
	s.startupTaskResumeQueued = false
	s.startupResumeMu.Unlock()
	go s.runPendingStartupRecovery(reason)
}

func (s *Server) runPendingStartupRecovery(reason string) {
	for {
		s.attemptPendingStartupRecovery(reason)
		s.startupResumeMu.Lock()
		if s.startupTaskResumeQueued && s.hasStartupRecoveryPendingLocked() {
			s.startupTaskResumeQueued = false
			s.startupResumeMu.Unlock()
			reason = "coalesced_pending"
			continue
		}
		s.startupTaskResumeRunning = false
		s.startupTaskResumeQueued = false
		s.startupResumeMu.Unlock()
		return
	}
}

func (s *Server) scheduleStartupTaskResumeRetry() {
	if s == nil {
		return
	}
	s.startupResumeMu.Lock()
	if s.startupTaskResumeRetryRunning || !s.hasStartupRecoveryPendingLocked() {
		s.startupResumeMu.Unlock()
		return
	}
	s.startupTaskResumeRetryRunning = true
	s.startupResumeMu.Unlock()
	go s.runStartupTaskResumeRetry()
}

func (s *Server) runStartupTaskResumeRetry() {
	deadline := time.Now().Add(startupTaskResumeRetryWindow)
	delayIdx := 0
	for time.Now().Before(deadline) {
		if !s.hasStartupRecoveryPending() {
			break
		}
		delay := startupTaskResumeRetryDelays[len(startupTaskResumeRetryDelays)-1]
		if delayIdx < len(startupTaskResumeRetryDelays) {
			delay = startupTaskResumeRetryDelays[delayIdx]
			delayIdx++
		}
		time.Sleep(delay)
		if !s.hasStartupRecoveryPending() {
			break
		}
		s.schedulePendingStartupRecovery("startup_retry_resume")
	}
	if s.hasStartupRecoveryPending() {
		pendingTasks, aiSyncPending := s.pendingStartupRecoveryCounts()
		logutil.Warnf("startup pending recovery retry timed out: pending_tasks=%d ai_sync_pending=%t", pendingTasks, aiSyncPending)
	}
	s.startupResumeMu.Lock()
	s.startupTaskResumeRetryRunning = false
	s.startupResumeMu.Unlock()
}

func (s *Server) pendingStartupRecoveryCounts() (int, bool) {
	if s == nil {
		return 0, false
	}
	s.startupResumeMu.Lock()
	defer s.startupResumeMu.Unlock()
	return len(s.startupTaskResumePending), s.startupAISyncPending
}

func (s *Server) hasStartupRecoveryPendingLocked() bool {
	return s.startupAISyncPending || len(s.startupTaskResumePending) > 0
}

func (s *Server) readPromptMarkdown(path string) string {
	target := strings.TrimSpace(path)
	if target == "" {
		return ""
	}
	body, err := os.ReadFile(target)
	if err != nil {
		if fallback := resolvePromptMarkdownPath(target); fallback != "" && !samePath(target, fallback) {
			body, err = os.ReadFile(fallback)
			if err == nil {
				target = fallback
			}
		}
	}
	if err != nil {
		log.Printf("read prompt markdown failed: path=%s err=%v", target, err)
		return ""
	}
	text := strings.TrimPrefix(string(body), "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return text
}

// 测试和某些启动方式下，工作目录不一定是仓库根目录。
// 这里在相对路径读取失败时，回退到基于 server.go 所在位置推导出的项目根目录。
func resolvePromptMarkdownPath(target string) string {
	normalized := strings.TrimSpace(target)
	if normalized == "" || filepath.IsAbs(normalized) {
		return ""
	}
	normalized = strings.TrimPrefix(normalized, "./")
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok || strings.TrimSpace(currentFile) == "" {
		return ""
	}
	projectRoot := filepath.Dir(filepath.Dir(filepath.Dir(currentFile)))
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	projectRootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return ""
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	relToRoot, err := filepath.Rel(projectRootAbs, cwdAbs)
	if err != nil || strings.HasPrefix(relToRoot, "..") {
		return ""
	}
	return filepath.Join(projectRoot, filepath.FromSlash(normalized))
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.EqualFold(leftAbs, rightAbs)
}

func openDB(cfg *config.Config) (*gorm.DB, error) {
	logLevel := logger.Warn
	if strings.ToLower(cfg.Log.Level) == "debug" {
		logLevel = logger.Info
	}
	gormLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second,
			LogLevel:                  logLevel,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)
	db, err := gorm.Open(sqlite.Open(cfg.Data.Database.Dsn), &gorm.Config{Logger: gormLogger})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetConnMaxLifetime(6 * time.Hour)
	return db, nil
}

func (s *Server) ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func (s *Server) ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": data})
}

func (s *Server) fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"code": status, "msg": msg})
}
