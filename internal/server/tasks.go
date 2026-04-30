package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"maas-box/internal/ai"
	"maas-box/internal/logutil"
	"maas-box/internal/model"
)

type alarmLevelUpsertRequest struct {
	Name        string `json:"name"`
	Severity    int    `json:"severity"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type taskUpsertRequest struct {
	Name          string                   `json:"name"`
	DeviceConfigs []taskDeviceConfigUpsert `json:"device_configs"`
	Notes         string                   `json:"notes"`
}

type taskAlgorithmConfigUpsert struct {
	AlgorithmID       string `json:"algorithm_id"`
	AlarmLevelID      string `json:"alarm_level_id"`
	AlertCycleSeconds *int   `json:"alert_cycle_seconds"`
}

type taskDeviceConfigUpsert struct {
	DeviceID         string                      `json:"device_id"`
	AlgorithmConfigs []taskAlgorithmConfigUpsert `json:"algorithm_configs"`
	FrameRateMode    string                      `json:"frame_rate_mode"`
	FrameRateValue   int                         `json:"frame_rate_value"`
	RecordingPolicy  string                      `json:"recording_policy"`
	AlarmPreSeconds  int                         `json:"recording_pre_seconds"`
	AlarmPostSeconds int                         `json:"recording_post_seconds"`
}

type taskDeviceQuickConfigRequest struct {
	Name            string   `json:"name"`
	Notes           string   `json:"notes"`
	RecordingPolicy string   `json:"recording_policy"`
	AlgorithmIDs    []string `json:"algorithm_ids"`
}

type taskSummary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Notes       string    `json:"notes"`
	LastStartAt time.Time `json:"last_start_at"`
	LastStopAt  time.Time `json:"last_stop_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type taskDetail struct {
	Task          taskSummary              `json:"task"`
	DeviceConfigs []taskDeviceConfigDetail `json:"device_configs"`
}

type taskAlgorithmConfigDetail struct {
	AlgorithmID        string `json:"algorithm_id"`
	AlgorithmCode      string `json:"algorithm_code"`
	AlgorithmName      string `json:"algorithm_name"`
	AlarmLevelID       string `json:"alarm_level_id"`
	AlarmLevelName     string `json:"alarm_level_name"`
	AlarmLevelColor    string `json:"alarm_level_color"`
	AlarmLevelSeverity int    `json:"alarm_level_severity"`
	AlertCycleSeconds  int    `json:"alert_cycle_seconds"`
}

type taskDeviceConfigDetail struct {
	DeviceID         string                      `json:"device_id"`
	DeviceName       string                      `json:"device_name"`
	AlgorithmConfigs []taskAlgorithmConfigDetail `json:"algorithm_configs"`
	FrameRateMode    string                      `json:"frame_rate_mode"`
	FrameRateValue   int                         `json:"frame_rate_value"`
	RecordingPolicy  string                      `json:"recording_policy"`
	AlarmPreSeconds  int                         `json:"recording_pre_seconds"`
	AlarmPostSeconds int                         `json:"recording_post_seconds"`
}

type taskAlgorithmRuntime struct {
	Algorithm         model.Algorithm
	AlarmLevelID      string
	AlertCycleSeconds int
}

type taskDeviceRuntime struct {
	Profile          model.VideoTaskDeviceProfile
	Device           model.Device
	AlgorithmConfigs []taskAlgorithmRuntime
}

type llmPromptTask struct {
	TaskCode string `json:"task_code"`
	TaskName string `json:"task_name"`
	TaskMode string `json:"task_mode"`
	Goal     string `json:"goal"`
}

type llmPromptResult struct {
	Version string `json:"version"`
	Overall struct {
		Alarm          string   `json:"alarm"`
		AlarmTaskCodes []string `json:"alarm_task_codes"`
	} `json:"overall"`
	TaskResults []llmPromptTaskResult `json:"task_results"`
	Objects     []llmPromptObject     `json:"objects"`
}

type llmPromptTaskResult struct {
	TaskCode   string   `json:"task_code"`
	TaskName   string   `json:"task_name"`
	TaskMode   string   `json:"task_mode"`
	Alarm      any      `json:"alarm"`
	Reason     string   `json:"reason"`
	Excluded   []string `json:"excluded"`
	Suggestion string   `json:"suggestion"`
	ObjectIDs  []string `json:"object_ids"`
}

type llmPromptObject struct {
	ObjectID   string         `json:"object_id"`
	TaskCode   string         `json:"task_code"`
	BBox2D     []float64      `json:"bbox2d"`
	Label      string         `json:"label"`
	Confidence float64        `json:"confidence"`
	Attributes map[string]any `json:"attributes"`
}

type deviceLaunchPlan struct {
	Device           model.Device                    `json:"device"`
	Labels           []string                        `json:"labels"`
	Prompt           string                          `json:"prompt"`
	PromptTasks      []llmPromptTask                 `json:"prompt_tasks"`
	Provider         model.ModelProvider             `json:"provider"`
	Algorithms       []model.Algorithm               `json:"algorithms"`
	AlgorithmConfigs []ai.StartCameraAlgorithmConfig `json:"algorithm_configs"`
}

type taskActionResult struct {
	DeviceID string `json:"device_id"`
	Success  bool   `json:"success"`
	Message  string `json:"message"`
}

type taskDefaultsResponse struct {
	RecordingPolicyDefault      string   `json:"recording_policy_default"`
	AlarmClipEnabledDefault     bool     `json:"alarm_clip_enabled_default"`
	RecordingPreSecondsDefault  int      `json:"recording_pre_seconds_default"`
	RecordingPostSecondsDefault int      `json:"recording_post_seconds_default"`
	AlertCycleSecondsDefault    int      `json:"alert_cycle_seconds_default"`
	AlarmLevelIDDefault         string   `json:"alarm_level_id_default"`
	FrameRateModes              []string `json:"frame_rate_modes"`
	FrameRateModeDefault        string   `json:"frame_rate_mode_default"`
	FrameRateValueDefault       int      `json:"frame_rate_value_default"`
}

const (
	defaultAlertCycleSeconds = 60
	maxAlertCycleSeconds     = 86400
	aiStartCameraRetryLimit  = 20
)

const (
	builtinAlarmLevelID1 = "alarm_level_1"
	builtinAlarmLevelID2 = "alarm_level_2"
	builtinAlarmLevelID3 = "alarm_level_3"
)

type builtinAlarmLevelSpec struct {
	ID          string
	Name        string
	Severity    int
	Color       string
	Description string
}

var builtinAlarmLevels = []builtinAlarmLevelSpec{
	{ID: builtinAlarmLevelID1, Name: "低", Severity: 1, Color: "#52c41a", Description: "低风险"},
	{ID: builtinAlarmLevelID2, Name: "中", Severity: 2, Color: "#faad14", Description: "中风险"},
	{ID: builtinAlarmLevelID3, Name: "高", Severity: 3, Color: "#ff4d4f", Description: "高风险"},
}

func (s *Server) registerTaskRoutes(r gin.IRouter) {
	level := r.Group("/alarm-levels")
	level.GET("", s.listAlarmLevels)
	level.POST("", s.createAlarmLevel)
	level.PUT("/:id", s.updateAlarmLevel)
	level.DELETE("/:id", s.deleteAlarmLevel)

	task := r.Group("/tasks")
	task.GET("", s.listTasks)
	task.GET("/defaults", s.getTaskDefaults)
	task.GET("/:id", s.getTask)
	task.POST("", s.createTask)
	task.PUT("/:id", s.updateTask)
	task.PUT("/:id/devices/:device_id/quick-config", s.quickUpdateTaskDeviceConfig)
	task.DELETE("/:id", s.deleteTask)
	task.POST("/:id/start", s.startTask)
	task.POST("/:id/stop", s.stopTask)
	task.GET("/:id/sync-status", s.syncTaskStatus)
	task.GET("/:id/prompt-preview", s.taskPromptPreview)
}

func (s *Server) listAlarmLevels(c *gin.Context) {
	var items []model.AlarmLevel
	if err := s.db.
		Where("id IN ?", builtinAlarmLevelIDs()).
		Order("severity asc, created_at asc").
		Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query alarm levels failed")
		return
	}
	s.ok(c, gin.H{"items": items, "total": len(items)})
}

func (s *Server) getTaskDefaults(c *gin.Context) {
	s.ok(c, s.taskDefaultsPayload())
}

func (s *Server) createAlarmLevel(c *gin.Context) {
	s.fail(c, http.StatusBadRequest, "内置报警等级不支持新增")
}

func (s *Server) updateAlarmLevel(c *gin.Context) {
	id := c.Param("id")
	if !isBuiltinAlarmLevelID(id) {
		s.fail(c, http.StatusBadRequest, "仅支持编辑内置报警等级")
		return
	}
	var item model.AlarmLevel
	if err := s.db.Where("id = ?", id).First(&item).Error; err != nil {
		s.fail(c, http.StatusNotFound, "alarm level not found")
		return
	}
	var in alarmLevelUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		s.fail(c, http.StatusBadRequest, "name is required")
		return
	}
	item.Name = in.Name
	item.Color = strings.TrimSpace(in.Color)
	if item.Color == "" {
		item.Color = levelColorByID(id)
	}
	item.Description = strings.TrimSpace(in.Description)
	if err := s.db.Save(&item).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "update alarm level failed")
		return
	}
	s.ok(c, item)
}

func (s *Server) deleteAlarmLevel(c *gin.Context) {
	s.fail(c, http.StatusBadRequest, "内置报警等级不支持删除")
}

func (s *Server) listTasks(c *gin.Context) {
	var tasks []model.VideoTask
	if err := s.db.Order("created_at desc").Find(&tasks).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query tasks failed")
		return
	}
	items := make([]taskDetail, 0, len(tasks))
	for _, task := range tasks {
		detail, err := s.loadTaskDetail(task.ID)
		if err == nil {
			items = append(items, detail)
		}
	}
	s.ok(c, gin.H{"items": items, "total": len(items)})
}

func (s *Server) getTask(c *gin.Context) {
	id := c.Param("id")
	detail, err := s.loadTaskDetail(id)
	if err != nil {
		s.fail(c, http.StatusNotFound, "task not found")
		return
	}
	s.ok(c, detail)
}

func (s *Server) createTask(c *gin.Context) {
	var in taskUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	task, profiles, algorithmConfigsByDevice, err := s.validateTaskInput("", in)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	task.ID = uuid.NewString()
	task.Status = model.TaskStatusStopped

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		return s.saveTaskDeviceConfigs(tx, task.ID, profiles, algorithmConfigsByDevice)
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "create task failed")
		return
	}
	detail, _ := s.loadTaskDetail(task.ID)
	s.ok(c, detail)
}

func (s *Server) updateTask(c *gin.Context) {
	id := c.Param("id")
	var existing model.VideoTask
	if err := s.db.Where("id = ?", id).First(&existing).Error; err != nil {
		s.fail(c, http.StatusNotFound, "task not found")
		return
	}
	if existing.Status == model.TaskStatusRunning {
		s.fail(c, http.StatusBadRequest, "task is running and cannot be edited")
		return
	}

	var in taskUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	task, profiles, algorithmConfigsByDevice, err := s.validateTaskInput(id, in)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	task.ID = existing.ID
	task.Status = existing.Status
	task.CreatedAt = existing.CreatedAt
	task.LastStartAt = existing.LastStartAt
	task.LastStopAt = existing.LastStopAt

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&existing).Updates(task).Error; err != nil {
			return err
		}
		return s.saveTaskDeviceConfigs(tx, id, profiles, algorithmConfigsByDevice)
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "update task failed")
		return
	}
	detail, _ := s.loadTaskDetail(id)
	s.ok(c, detail)
}

func (s *Server) quickUpdateTaskDeviceConfig(c *gin.Context) {
	taskID := c.Param("id")
	deviceID := strings.TrimSpace(c.Param("device_id"))

	var task model.VideoTask
	if err := s.db.Where("id = ?", taskID).First(&task).Error; err != nil {
		s.fail(c, http.StatusNotFound, "task not found")
		return
	}
	if deviceID == "" {
		s.fail(c, http.StatusBadRequest, "device_id is required")
		return
	}

	var profile model.VideoTaskDeviceProfile
	if err := s.db.Where("task_id = ? AND device_id = ?", taskID, deviceID).First(&profile).Error; err != nil {
		s.fail(c, http.StatusNotFound, "task device not found")
		return
	}

	var in taskDeviceQuickConfigRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		s.fail(c, http.StatusBadRequest, "name is required")
		return
	}
	recordingPolicy, err := s.validateTaskRecordingPolicy(in.RecordingPolicy)
	if err != nil {
		s.fail(c, http.StatusBadRequest, "invalid recording_policy")
		return
	}

	algorithmIDs := uniqueStrings(in.AlgorithmIDs)
	if len(algorithmIDs) == 0 {
		s.fail(c, http.StatusBadRequest, "algorithm_ids is required")
		return
	}
	var algorithmCount int64
	if err := s.db.Model(&model.Algorithm{}).Where("id IN ?", algorithmIDs).Count(&algorithmCount).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query algorithms failed")
		return
	}
	if int(algorithmCount) != len(algorithmIDs) {
		s.fail(c, http.StatusBadRequest, "algorithm not found")
		return
	}

	defaults := s.taskDefaultsPayload()
	defaultAlarmLevelID := strings.TrimSpace(defaults.AlarmLevelIDDefault)
	if defaultAlarmLevelID == "" {
		defaultAlarmLevelID, err = s.defaultAlarmLevelID()
		if err != nil {
			s.fail(c, http.StatusInternalServerError, "resolve default alarm level failed")
			return
		}
	}
	defaultAlertCycle := s.normalizeAlertCycleSecondsPersisted(defaults.AlertCycleSecondsDefault)

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&task).Updates(map[string]any{
			"name":  name,
			"notes": strings.TrimSpace(in.Notes),
		}).Error; err != nil {
			return err
		}

		if err := tx.Model(&profile).Updates(map[string]any{
			"recording_policy": recordingPolicy,
		}).Error; err != nil {
			return err
		}

		var existingAlgorithms []model.VideoTaskDeviceAlgorithm
		if err := tx.Where("task_id = ? AND device_id = ?", taskID, deviceID).Find(&existingAlgorithms).Error; err != nil {
			return err
		}
		existingByAlgorithm := make(map[string]model.VideoTaskDeviceAlgorithm, len(existingAlgorithms))
		for _, item := range existingAlgorithms {
			existingByAlgorithm[strings.TrimSpace(item.AlgorithmID)] = item
		}

		if err := tx.Delete(&model.VideoTaskDeviceAlgorithm{}, "task_id = ? AND device_id = ?", taskID, deviceID).Error; err != nil {
			return err
		}

		// 大屏快速编辑只改当前设备的算法绑定：
		// 已存在算法沿用原报警周期/等级，新绑定算法补默认值，避免覆盖任务页里更细的高级配置。
		nextAlgorithms := make([]model.VideoTaskDeviceAlgorithm, 0, len(algorithmIDs))
		for _, algorithmID := range algorithmIDs {
			if current, ok := existingByAlgorithm[algorithmID]; ok {
				nextAlgorithms = append(nextAlgorithms, model.VideoTaskDeviceAlgorithm{
					TaskID:            taskID,
					DeviceID:          deviceID,
					AlgorithmID:       algorithmID,
					AlarmLevelID:      firstNonEmpty(strings.TrimSpace(current.AlarmLevelID), defaultAlarmLevelID),
					AlertCycleSeconds: s.normalizeAlertCycleSecondsPersisted(current.AlertCycleSeconds),
				})
				continue
			}
			nextAlgorithms = append(nextAlgorithms, model.VideoTaskDeviceAlgorithm{
				TaskID:            taskID,
				DeviceID:          deviceID,
				AlgorithmID:       algorithmID,
				AlarmLevelID:      defaultAlarmLevelID,
				AlertCycleSeconds: defaultAlertCycle,
			})
		}
		if len(nextAlgorithms) > 0 {
			if err := tx.Create(&nextAlgorithms).Error; err != nil {
				return err
			}
		}
		return s.rebuildTaskLegacyRelations(tx, taskID)
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "quick update task device failed")
		return
	}

	_, runtimes, err := s.loadTaskDeviceContexts(taskID)
	if err != nil {
		s.fail(c, http.StatusInternalServerError, "reload task runtime failed")
		return
	}

	onlyDeviceIDs := map[string]struct{}{deviceID: {}}
	runtimeResults := make([]taskActionResult, 0, 2)
	successCount := 0
	isDeviceRunning := false
	for _, item := range runtimes {
		if strings.TrimSpace(item.Device.ID) == deviceID {
			isDeviceRunning = item.Device.AIStatus == model.DeviceAIStatusRunning
			break
		}
	}

	if !s.cfg.Server.AI.Disabled {
		if err := s.ensureLLMTokenQuotaAvailable(); err != nil {
			if isLLMTokenLimitExceededError(err) {
				s.disableTaskAutoResumeForQuota(taskID)
				s.fail(c, http.StatusBadRequest, llmTokenLimitExceededMessage)
				return
			}
			s.fail(c, http.StatusInternalServerError, "check llm token quota failed")
			return
		}
	}

	if isDeviceRunning {
		stopResults := s.stopTaskRuntimes(c.Request.Context(), taskID, runtimes, onlyDeviceIDs, "quick_config")
		runtimeResults = append(runtimeResults, stopResults...)
	}

	if s.cfg.Server.AI.Disabled {
		nextStatus, err := s.updateTaskRuntimeStatus(taskID, runtimes, false)
		if err != nil {
			s.fail(c, http.StatusInternalServerError, "update task runtime status failed")
			return
		}
		detail, _ := s.loadTaskDetail(taskID)
		s.ok(c, gin.H{
			"task_id":   taskID,
			"device_id": deviceID,
			"status":    nextStatus,
			"message":   "配置已保存，但 AI 服务已禁用，未执行任务启动",
			"results":   runtimeResults,
			"detail":    detail,
		})
		return
	}

	startResults, successCount := s.startTaskRuntimes(c.Request.Context(), taskID, runtimes, onlyDeviceIDs, "quick_config")
	runtimeResults = append(runtimeResults, startResults...)
	nextStatus, err := s.updateTaskRuntimeStatus(taskID, runtimes, successCount > 0)
	if err != nil {
		s.fail(c, http.StatusInternalServerError, "update task runtime status failed")
		return
	}
	if successCount > 0 {
		if err := s.setTaskAutoResumeIntent(taskID, true); err != nil {
			s.fail(c, http.StatusInternalServerError, "update task auto resume failed")
			return
		}
	}

	messageText := "当前设备任务配置已保存"
	if isDeviceRunning {
		messageText = "当前设备任务配置已保存，并已重启设备任务"
	} else if successCount > 0 {
		messageText = "当前设备任务配置已保存，并已启动设备任务"
	}
	if successCount == 0 && len(startResults) > 0 {
		for _, item := range startResults {
			if strings.TrimSpace(item.Message) != "" {
				messageText = "当前设备任务配置已保存，设备任务启动失败：" + item.Message
				break
			}
		}
	}

	detail, _ := s.loadTaskDetail(taskID)
	s.ok(c, gin.H{
		"task_id":   taskID,
		"device_id": deviceID,
		"status":    nextStatus,
		"message":   messageText,
		"results":   runtimeResults,
		"detail":    detail,
	})
}

func (s *Server) deleteTask(c *gin.Context) {
	id := c.Param("id")
	var task model.VideoTask
	if err := s.db.Where("id = ?", id).First(&task).Error; err != nil {
		s.fail(c, http.StatusNotFound, "task not found")
		return
	}
	if task.Status == model.TaskStatusRunning {
		s.fail(c, http.StatusBadRequest, "running task cannot be deleted")
		return
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.VideoTaskDeviceProfile{}, "task_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.VideoTaskDeviceAlgorithm{}, "task_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.VideoTaskDevice{}, "task_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.VideoTaskAlgorithm{}, "task_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&model.VideoTask{}, "id = ?", id).Error
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "delete task failed")
		return
	}
	s.ok(c, gin.H{"deleted": id})
}

func (s *Server) taskPromptPreview(c *gin.Context) {
	id := c.Param("id")
	_, runtimes, err := s.loadTaskDeviceContexts(id)
	if err != nil {
		s.fail(c, http.StatusNotFound, "task not found")
		return
	}
	items := make([]gin.H, 0, len(runtimes))
	for _, item := range runtimes {
		algorithms := runtimeAlgorithms(item.AlgorithmConfigs)
		plan, err := s.buildDevicePlan(item.Device, algorithms)
		if err != nil {
			s.fail(c, http.StatusBadRequest, "build prompt failed: "+err.Error())
			return
		}
		algorithmConfigs := make([]gin.H, 0, len(item.AlgorithmConfigs))
		for _, cfg := range item.AlgorithmConfigs {
			detectMode := resolveAlgorithmDetectMode(cfg.Algorithm)
			labels := []string{}
			if detectMode == model.AlgorithmDetectModeSmallOnly || detectMode == model.AlgorithmDetectModeHybrid {
				labels = normalizeSmallModelLabels([]string{cfg.Algorithm.SmallModelLabel})
			}
			algorithmConfigs = append(algorithmConfigs, gin.H{
				"algorithm_id":        cfg.Algorithm.ID,
				"algorithm_code":      strings.TrimSpace(cfg.Algorithm.Code),
				"algorithm_name":      cfg.Algorithm.Name,
				"detect_mode":         detectMode,
				"labels":              labels,
				"yolo_threshold":      clamp(cfg.Algorithm.YoloThreshold, 0.01, 0.99, 0.5),
				"iou_threshold":       clamp(cfg.Algorithm.IOUThreshold, 0.1, 0.99, 0.8),
				"labels_trigger_mode": normalizeLabelsTriggerMode(cfg.Algorithm.LabelsTriggerMode),
				"alarm_level_id":      strings.TrimSpace(cfg.AlarmLevelID),
				"alert_cycle_seconds": cfg.AlertCycleSeconds,
			})
		}
		items = append(items, gin.H{
			"device_id":              item.Device.ID,
			"device_name":            item.Device.Name,
			"labels":                 plan.Labels,
			"prompt":                 plan.Prompt,
			"prompt_tasks":           plan.PromptTasks,
			"provider":               plan.Provider,
			"algorithm_configs":      algorithmConfigs,
			"frame_rate_mode":        item.Profile.FrameRateMode,
			"frame_rate_value":       item.Profile.FrameRateValue,
			"recording_policy":       item.Profile.RecordingPolicy,
			"recording_pre_seconds":  item.Profile.AlarmPreSeconds,
			"recording_post_seconds": item.Profile.AlarmPostSeconds,
		})
	}
	s.ok(c, gin.H{"items": items, "total": len(items)})
}

func (s *Server) startTask(c *gin.Context) {
	id := c.Param("id")
	task, runtimes, err := s.loadTaskDeviceContexts(id)
	if err != nil {
		s.fail(c, http.StatusNotFound, "task not found")
		return
	}
	if task.Status == model.TaskStatusRunning {
		s.ok(c, gin.H{"task_id": id, "status": task.Status, "message": "task already running"})
		return
	}
	if s.cfg.Server.AI.Disabled {
		s.fail(c, http.StatusBadRequest, "AI service is disabled")
		return
	}
	if err := s.ensureLLMTokenQuotaAvailable(); err != nil {
		if isLLMTokenLimitExceededError(err) {
			s.disableTaskAutoResumeForQuota(id)
			s.fail(c, http.StatusBadRequest, llmTokenLimitExceededMessage)
			return
		}
		s.fail(c, http.StatusInternalServerError, "check llm token quota failed")
		return
	}

	results, successCount := s.startTaskRuntimes(c.Request.Context(), id, runtimes, nil, "manual_start")
	newStatus, err := s.updateTaskRuntimeStatus(id, runtimes, true)
	if err != nil {
		s.fail(c, http.StatusInternalServerError, "update task runtime status failed")
		return
	}
	if successCount > 0 {
		if err := s.setTaskAutoResumeIntent(id, true); err != nil {
			s.fail(c, http.StatusInternalServerError, "update task auto resume failed")
			return
		}
		s.clearStartupResumePendingByTask(id)
		s.clearStartupTaskResumePendingByTask(id)
	}
	messageText := "任务启动失败"
	switch newStatus {
	case model.TaskStatusRunning:
		messageText = "任务已启动"
		for _, item := range results {
			if item.Success && strings.Contains(item.Message, "探测中") {
				messageText = "任务已启动，流信息探测中"
				break
			}
		}
	case model.TaskStatusPartialFail:
		messageText = fmt.Sprintf("任务部分启动成功：成功%d，失败%d", successCount, len(runtimes)-successCount)
	default:
		for _, item := range results {
			if !item.Success && strings.TrimSpace(item.Message) != "" {
				messageText = item.Message
				break
			}
		}
	}
	s.ok(c, gin.H{
		"task_id": id,
		"status":  newStatus,
		"message": messageText,
		"summary": gin.H{"total": len(runtimes), "success": successCount, "failed": len(runtimes) - successCount},
		"results": results,
	})
}

func (s *Server) stopTask(c *gin.Context) {
	id := c.Param("id")
	task, runtimes, err := s.loadTaskDeviceContexts(id)
	if err != nil {
		s.fail(c, http.StatusNotFound, "task not found")
		return
	}
	if err := s.setTaskAutoResumeIntent(id, false); err != nil {
		s.fail(c, http.StatusInternalServerError, "update task auto resume failed")
		return
	}
	s.clearStartupResumePendingByTask(id)
	s.clearStartupTaskResumePendingByTask(id)
	if task.Status == model.TaskStatusStopped {
		s.ok(c, gin.H{"task_id": id, "status": task.Status, "message": "task already stopped"})
		return
	}
	results := make([]taskActionResult, 0, len(runtimes))
	for _, item := range runtimes {
		device := item.Device
		req := ai.StopCameraRequest{CameraID: device.ID}
		logutil.Debugf("task stop request: task_id=%s device_id=%s ai_service_url=%s payload=%s", id, device.ID, strings.TrimSpace(s.cfg.Server.AI.ServiceURL), marshalJSONForLog(req))
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		resp, err := s.aiClient.StopCamera(ctx, req)
		cancel()
		if err != nil {
			logutil.Warnf("task stop response: task_id=%s device_id=%s error=%v", id, device.ID, err)
		} else {
			logutil.Debugf("task stop response: task_id=%s device_id=%s payload=%s", id, device.ID, marshalJSONForLog(resp))
		}
		if err != nil {
			results = append(results, taskActionResult{DeviceID: device.ID, Success: false, Message: err.Error()})
		} else {
			results = append(results, taskActionResult{DeviceID: device.ID, Success: resp.Success, Message: resp.Message})
		}
		_ = s.db.Model(&model.Device{}).Where("id = ?", device.ID).Update("ai_status", model.DeviceAIStatusStopped).Error
		_ = s.applyRecordingPolicyForSourceID(device.ID)
	}
	_ = s.db.Model(&model.VideoTask{}).Where("id = ?", id).
		Updates(map[string]any{"status": model.TaskStatusStopped, "last_stop_at": time.Now()}).Error
	s.ok(c, gin.H{"task_id": id, "status": model.TaskStatusStopped, "results": results})
}

func (s *Server) stopTaskRuntimes(ctx context.Context, taskID string, runtimes []taskDeviceRuntime, onlyDeviceIDs map[string]struct{}, reason string) []taskActionResult {
	results := make([]taskActionResult, 0, len(runtimes))
	if s == nil || s.db == nil || s.aiClient == nil {
		return results
	}
	for _, item := range runtimes {
		device := item.Device
		if len(onlyDeviceIDs) > 0 {
			if _, ok := onlyDeviceIDs[device.ID]; !ok {
				continue
			}
		}
		req := ai.StopCameraRequest{CameraID: device.ID}
		logutil.Debugf("task stop request: task_id=%s device_id=%s reason=%s ai_service_url=%s payload=%s", taskID, device.ID, strings.TrimSpace(reason), strings.TrimSpace(s.cfg.Server.AI.ServiceURL), marshalJSONForLog(req))
		callCtx := ctx
		if callCtx == nil {
			callCtx = context.Background()
		}
		callCtx, cancel := context.WithTimeout(callCtx, 8*time.Second)
		resp, err := s.aiClient.StopCamera(callCtx, req)
		cancel()
		if err != nil {
			logutil.Warnf("task stop response: task_id=%s device_id=%s reason=%s error=%v", taskID, device.ID, reason, err)
		} else {
			logutil.Debugf("task stop response: task_id=%s device_id=%s reason=%s payload=%s", taskID, device.ID, reason, marshalJSONForLog(resp))
		}
		if err != nil {
			results = append(results, taskActionResult{DeviceID: device.ID, Success: false, Message: err.Error()})
		} else {
			results = append(results, taskActionResult{DeviceID: device.ID, Success: resp.Success, Message: resp.Message})
		}
		_ = s.db.Model(&model.Device{}).Where("id = ?", device.ID).Update("ai_status", model.DeviceAIStatusStopped).Error
		_ = s.applyRecordingPolicyForSourceID(device.ID)
	}
	return results
}

func (s *Server) syncTaskStatus(c *gin.Context) {
	id := c.Param("id")
	resp, running, err := s.fetchAIRunningDevices(c.Request.Context())
	if err != nil {
		s.fail(c, http.StatusBadGateway, "fetch ai status failed: "+err.Error())
		return
	}
	nextTaskStatus, runningCount, totalDevices, err := s.syncTaskStatusFromRunningSet(id, running)
	if err != nil {
		s.fail(c, http.StatusNotFound, "task not found")
		return
	}

	s.ok(c, gin.H{
		"task_id":       id,
		"task_status":   nextTaskStatus,
		"running_count": runningCount,
		"total_devices": totalDevices,
		"ai_status":     resp,
	})
}

func (s *Server) validateTaskInput(taskID string, in taskUpsertRequest) (model.VideoTask, []model.VideoTaskDeviceProfile, map[string][]model.VideoTaskDeviceAlgorithm, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return model.VideoTask{}, nil, nil, errBadRequest("name is required")
	}
	if len(in.DeviceConfigs) == 0 {
		return model.VideoTask{}, nil, nil, errBadRequest("device_configs is required")
	}
	defaultAlarmLevelID, err := s.defaultAlarmLevelID()
	if err != nil {
		return model.VideoTask{}, nil, nil, err
	}
	alarmLevelMap, err := s.loadBuiltinAlarmLevelMap()
	if err != nil {
		return model.VideoTask{}, nil, nil, err
	}

	defaults := s.taskDefaultsPayload()
	deviceIDs := make([]string, 0, len(in.DeviceConfigs))
	allAlgorithmIDs := make([]string, 0, len(in.DeviceConfigs)*2)
	seenDevices := make(map[string]struct{}, len(in.DeviceConfigs))
	profiles := make([]model.VideoTaskDeviceProfile, 0, len(in.DeviceConfigs))
	algorithmConfigsByDevice := make(map[string][]model.VideoTaskDeviceAlgorithm, len(in.DeviceConfigs))
	for _, cfg := range in.DeviceConfigs {
		deviceID := strings.TrimSpace(cfg.DeviceID)
		if deviceID == "" {
			return model.VideoTask{}, nil, nil, errBadRequest("device_id is required")
		}
		if _, ok := seenDevices[deviceID]; ok {
			return model.VideoTask{}, nil, nil, errBadRequest(fmt.Sprintf("duplicate device id is not allowed: %s", deviceID))
		}
		seenDevices[deviceID] = struct{}{}
		algorithmConfigs := make([]model.VideoTaskDeviceAlgorithm, 0, len(cfg.AlgorithmConfigs))
		seenAlgorithms := make(map[string]struct{}, len(cfg.AlgorithmConfigs))
		for _, algorithmConfig := range cfg.AlgorithmConfigs {
			algorithmID := strings.TrimSpace(algorithmConfig.AlgorithmID)
			if algorithmID == "" {
				return model.VideoTask{}, nil, nil, errBadRequest(fmt.Sprintf("algorithm_id is required for device %s", deviceID))
			}
			alarmLevelID := strings.TrimSpace(algorithmConfig.AlarmLevelID)
			if alarmLevelID == "" {
				alarmLevelID = defaultAlarmLevelID
			}
			if _, exists := alarmLevelMap[alarmLevelID]; !exists {
				return model.VideoTask{}, nil, nil, errBadRequest(fmt.Sprintf("设备 %s 的算法 %s 报警等级无效", deviceID, algorithmID))
			}
			if _, exists := seenAlgorithms[algorithmID]; exists {
				return model.VideoTask{}, nil, nil, errBadRequest(fmt.Sprintf("duplicate algorithm id is not allowed for device %s", deviceID))
			}
			alertCycleSeconds, cycleErr := s.normalizeAlertCycleSecondsUpsert(algorithmConfig.AlertCycleSeconds)
			if cycleErr != nil {
				return model.VideoTask{}, nil, nil, errBadRequest(fmt.Sprintf("invalid alert_cycle_seconds for device %s algorithm %s: %v", deviceID, algorithmID, cycleErr))
			}
			seenAlgorithms[algorithmID] = struct{}{}
			algorithmConfigs = append(algorithmConfigs, model.VideoTaskDeviceAlgorithm{
				DeviceID:          deviceID,
				AlgorithmID:       algorithmID,
				AlarmLevelID:      alarmLevelID,
				AlertCycleSeconds: alertCycleSeconds,
			})
			allAlgorithmIDs = append(allAlgorithmIDs, algorithmID)
		}
		if len(algorithmConfigs) == 0 {
			return model.VideoTask{}, nil, nil, errBadRequest(fmt.Sprintf("algorithm_configs is required for device %s", deviceID))
		}
		frameRateMode, frameRateValue, frameRateErr := s.validateTaskFrameRate(cfg.FrameRateMode, cfg.FrameRateValue)
		if frameRateErr != nil {
			return model.VideoTask{}, nil, nil, errBadRequest(fmt.Sprintf("invalid frame rate for device %s: %v", deviceID, frameRateErr))
		}
		pre := cfg.AlarmPreSeconds
		post := cfg.AlarmPostSeconds
		if pre <= 0 {
			pre = defaults.RecordingPreSecondsDefault
		}
		if post <= 0 {
			post = defaults.RecordingPostSecondsDefault
		}
		recordingPolicy, policyErr := s.validateTaskRecordingPolicy(cfg.RecordingPolicy)
		if policyErr != nil {
			return model.VideoTask{}, nil, nil, errBadRequest(fmt.Sprintf("invalid recording_policy for device %s: %v", deviceID, policyErr))
		}
		profiles = append(profiles, model.VideoTaskDeviceProfile{
			DeviceID:         deviceID,
			FrameInterval:    frameRateValue,
			FrameRateMode:    frameRateMode,
			FrameRateValue:   frameRateValue,
			SmallConfidence:  0.5,
			LargeConfidence:  0.8,
			SmallIOU:         0.8,
			AlarmLevelID:     defaultAlarmLevelID,
			RecordingPolicy:  recordingPolicy,
			AlarmPreSeconds:  clampInt(pre, 1, 600),
			AlarmPostSeconds: clampInt(post, 1, 600),
		})
		algorithmConfigsByDevice[deviceID] = algorithmConfigs
		deviceIDs = append(deviceIDs, deviceID)
	}

	uniqDeviceIDs := uniqueStrings(deviceIDs)
	uniqAlgorithmIDs := uniqueStrings(allAlgorithmIDs)
	var deviceCount int64
	if err := s.db.Model(&model.Device{}).
		Where("id IN ?", uniqDeviceIDs).
		Where("row_kind = ?", model.RowKindChannel).
		Count(&deviceCount).Error; err != nil {
		return model.VideoTask{}, nil, nil, err
	}
	if int(deviceCount) != len(uniqDeviceIDs) {
		return model.VideoTask{}, nil, nil, errBadRequest("device not found or not a channel")
	}

	var algorithmCount int64
	if err := s.db.Model(&model.Algorithm{}).Where("id IN ?", uniqAlgorithmIDs).Count(&algorithmCount).Error; err != nil {
		return model.VideoTask{}, nil, nil, err
	}
	if int(algorithmCount) != len(uniqAlgorithmIDs) {
		return model.VideoTask{}, nil, nil, errBadRequest("algorithm not found")
	}

	var conflict []model.VideoTaskDeviceProfile
	conflictQuery := s.db.Where("device_id IN ?", uniqDeviceIDs)
	if taskID != "" {
		conflictQuery = conflictQuery.Where("task_id <> ?", taskID)
	}
	if err := conflictQuery.Find(&conflict).Error; err != nil {
		return model.VideoTask{}, nil, nil, err
	}
	if len(conflict) > 0 {
		ids := make([]string, 0, len(conflict))
		for _, item := range conflict {
			ids = append(ids, item.DeviceID)
		}
		return model.VideoTask{}, nil, nil, errBadRequest(fmt.Sprintf("devices already used by another task: %s", strings.Join(uniqueStrings(ids), ",")))
	}

	task := model.VideoTask{
		Name:            name,
		FrameInterval:   profiles[0].FrameRateValue,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.8,
		AlarmLevelID:    defaultAlarmLevelID,
		Notes:           strings.TrimSpace(in.Notes),
	}
	return task, profiles, algorithmConfigsByDevice, nil
}

func (s *Server) saveTaskDeviceConfigs(tx *gorm.DB, taskID string, profiles []model.VideoTaskDeviceProfile, algorithmConfigsByDevice map[string][]model.VideoTaskDeviceAlgorithm) error {
	if err := tx.Delete(&model.VideoTaskDeviceProfile{}, "task_id = ?", taskID).Error; err != nil {
		return err
	}
	if err := tx.Delete(&model.VideoTaskDeviceAlgorithm{}, "task_id = ?", taskID).Error; err != nil {
		return err
	}
	if err := tx.Delete(&model.VideoTaskDevice{}, "task_id = ?", taskID).Error; err != nil {
		return err
	}
	if err := tx.Delete(&model.VideoTaskAlgorithm{}, "task_id = ?", taskID).Error; err != nil {
		return err
	}
	if len(profiles) == 0 {
		return nil
	}

	toSaveProfiles := make([]model.VideoTaskDeviceProfile, 0, len(profiles))
	legacyDevices := make([]model.VideoTaskDevice, 0, len(profiles))
	legacyAlgorithmIDSet := make(map[string]struct{})
	deviceAlgorithms := make([]model.VideoTaskDeviceAlgorithm, 0, len(profiles)*2)
	defaultAlarmLevelID, err := s.defaultAlarmLevelID()
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		profile.TaskID = taskID
		toSaveProfiles = append(toSaveProfiles, profile)
		legacyDevices = append(legacyDevices, model.VideoTaskDevice{TaskID: taskID, DeviceID: profile.DeviceID})
		for _, algorithmConfig := range algorithmConfigsByDevice[profile.DeviceID] {
			algorithmID := strings.TrimSpace(algorithmConfig.AlgorithmID)
			if algorithmID == "" {
				continue
			}
			deviceAlgorithms = append(deviceAlgorithms, model.VideoTaskDeviceAlgorithm{
				TaskID:            taskID,
				DeviceID:          profile.DeviceID,
				AlgorithmID:       algorithmID,
				AlarmLevelID:      firstNonEmpty(strings.TrimSpace(algorithmConfig.AlarmLevelID), defaultAlarmLevelID),
				AlertCycleSeconds: s.normalizeAlertCycleSecondsPersisted(algorithmConfig.AlertCycleSeconds),
			})
			legacyAlgorithmIDSet[algorithmID] = struct{}{}
		}
	}
	if err := tx.Create(&toSaveProfiles).Error; err != nil {
		return err
	}
	if len(deviceAlgorithms) > 0 {
		if err := tx.Create(&deviceAlgorithms).Error; err != nil {
			return err
		}
	}
	if len(legacyDevices) > 0 {
		if err := tx.Create(&legacyDevices).Error; err != nil {
			return err
		}
	}
	legacyAlgorithms := make([]model.VideoTaskAlgorithm, 0, len(legacyAlgorithmIDSet))
	for algorithmID := range legacyAlgorithmIDSet {
		legacyAlgorithms = append(legacyAlgorithms, model.VideoTaskAlgorithm{
			TaskID:      taskID,
			AlgorithmID: algorithmID,
		})
	}
	if len(legacyAlgorithms) > 0 {
		if err := tx.Create(&legacyAlgorithms).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) rebuildTaskLegacyRelations(tx *gorm.DB, taskID string) error {
	if tx == nil {
		return nil
	}
	if err := tx.Delete(&model.VideoTaskDevice{}, "task_id = ?", taskID).Error; err != nil {
		return err
	}
	if err := tx.Delete(&model.VideoTaskAlgorithm{}, "task_id = ?", taskID).Error; err != nil {
		return err
	}

	var profiles []model.VideoTaskDeviceProfile
	if err := tx.Where("task_id = ?", taskID).Find(&profiles).Error; err != nil {
		return err
	}
	if len(profiles) > 0 {
		legacyDevices := make([]model.VideoTaskDevice, 0, len(profiles))
		for _, profile := range profiles {
			legacyDevices = append(legacyDevices, model.VideoTaskDevice{
				TaskID:   taskID,
				DeviceID: profile.DeviceID,
			})
		}
		if err := tx.Create(&legacyDevices).Error; err != nil {
			return err
		}
	}

	var relAlgorithms []model.VideoTaskDeviceAlgorithm
	if err := tx.Where("task_id = ?", taskID).Find(&relAlgorithms).Error; err != nil {
		return err
	}
	algorithmIDSet := make(map[string]struct{}, len(relAlgorithms))
	for _, item := range relAlgorithms {
		algorithmID := strings.TrimSpace(item.AlgorithmID)
		if algorithmID == "" {
			continue
		}
		algorithmIDSet[algorithmID] = struct{}{}
	}
	if len(algorithmIDSet) == 0 {
		return nil
	}

	legacyAlgorithms := make([]model.VideoTaskAlgorithm, 0, len(algorithmIDSet))
	for algorithmID := range algorithmIDSet {
		legacyAlgorithms = append(legacyAlgorithms, model.VideoTaskAlgorithm{
			TaskID:      taskID,
			AlgorithmID: algorithmID,
		})
	}
	return tx.Create(&legacyAlgorithms).Error
}

func (s *Server) loadTaskDetail(taskID string) (taskDetail, error) {
	task, runtimes, err := s.loadTaskDeviceContexts(taskID)
	if err != nil {
		return taskDetail{}, err
	}
	levelIDSet := make(map[string]struct{})
	for _, runtime := range runtimes {
		for _, cfg := range runtime.AlgorithmConfigs {
			levelID := strings.TrimSpace(cfg.AlarmLevelID)
			if levelID == "" {
				continue
			}
			levelIDSet[levelID] = struct{}{}
		}
	}
	levelMap := make(map[string]model.AlarmLevel, len(levelIDSet))
	if len(levelIDSet) > 0 {
		levelIDs := make([]string, 0, len(levelIDSet))
		for levelID := range levelIDSet {
			levelIDs = append(levelIDs, levelID)
		}
		var levels []model.AlarmLevel
		if err := s.db.Where("id IN ?", levelIDs).Find(&levels).Error; err == nil {
			for _, level := range levels {
				levelMap[level.ID] = level
			}
		}
	}
	deviceConfigs := make([]taskDeviceConfigDetail, 0, len(runtimes))
	for _, runtime := range runtimes {
		algorithmConfigs := make([]taskAlgorithmConfigDetail, 0, len(runtime.AlgorithmConfigs))
		for _, algorithmConfig := range runtime.AlgorithmConfigs {
			algorithm := algorithmConfig.Algorithm
			alarmLevelID := strings.TrimSpace(algorithmConfig.AlarmLevelID)
			alarmLevelName := ""
			alarmLevelColor := ""
			alarmLevelSeverity := 0
			if level, ok := levelMap[alarmLevelID]; ok {
				alarmLevelName = strings.TrimSpace(level.Name)
				alarmLevelColor = strings.TrimSpace(level.Color)
				alarmLevelSeverity = level.Severity
			}
			algorithmConfigs = append(algorithmConfigs, taskAlgorithmConfigDetail{
				AlgorithmID:        algorithm.ID,
				AlgorithmCode:      strings.TrimSpace(algorithm.Code),
				AlgorithmName:      algorithm.Name,
				AlarmLevelID:       alarmLevelID,
				AlarmLevelName:     alarmLevelName,
				AlarmLevelColor:    alarmLevelColor,
				AlarmLevelSeverity: alarmLevelSeverity,
				AlertCycleSeconds:  algorithmConfig.AlertCycleSeconds,
			})
		}
		deviceConfigs = append(deviceConfigs, taskDeviceConfigDetail{
			DeviceID:         runtime.Device.ID,
			DeviceName:       runtime.Device.Name,
			AlgorithmConfigs: algorithmConfigs,
			FrameRateMode:    runtime.Profile.FrameRateMode,
			FrameRateValue:   runtime.Profile.FrameRateValue,
			RecordingPolicy:  firstNonEmpty(normalizeTaskRecordingPolicy(runtime.Profile.RecordingPolicy), model.RecordingPolicyNone),
			AlarmPreSeconds:  runtime.Profile.AlarmPreSeconds,
			AlarmPostSeconds: runtime.Profile.AlarmPostSeconds,
		})
	}
	return taskDetail{
		Task:          toTaskSummary(task),
		DeviceConfigs: deviceConfigs,
	}, nil
}

func (s *Server) loadTaskDeviceContexts(taskID string) (model.VideoTask, []taskDeviceRuntime, error) {
	var task model.VideoTask
	if err := s.db.Where("id = ?", taskID).First(&task).Error; err != nil {
		return model.VideoTask{}, nil, err
	}
	defaultAlarmLevelID, err := s.defaultAlarmLevelID()
	if err != nil {
		return model.VideoTask{}, nil, err
	}
	if strings.TrimSpace(task.AlarmLevelID) == "" {
		task.AlarmLevelID = defaultAlarmLevelID
	}
	var profiles []model.VideoTaskDeviceProfile
	if err := s.db.Where("task_id = ?", taskID).Find(&profiles).Error; err != nil {
		return model.VideoTask{}, nil, err
	}
	if len(profiles) == 0 {
		var legacyDevices []model.VideoTaskDevice
		if err := s.db.Where("task_id = ?", taskID).Find(&legacyDevices).Error; err != nil {
			return model.VideoTask{}, nil, err
		}
		if len(legacyDevices) > 0 {
			defaultPre := s.alarmClipDefaultPreSeconds()
			defaultPost := s.alarmClipDefaultPostSeconds()
			defaultFrameRateMode := s.taskFrameRateDefaultMode()
			defaultFrameRateValue := s.taskFrameRateDefaultValue()
			defaultRecordingPolicy := s.taskRecordingPolicyDefault()
			profiles = make([]model.VideoTaskDeviceProfile, 0, len(legacyDevices))
			for _, rel := range legacyDevices {
				profiles = append(profiles, model.VideoTaskDeviceProfile{
					TaskID:           taskID,
					DeviceID:         rel.DeviceID,
					FrameInterval:    defaultFrameRateValue,
					FrameRateMode:    defaultFrameRateMode,
					FrameRateValue:   defaultFrameRateValue,
					SmallConfidence:  task.SmallConfidence,
					LargeConfidence:  task.LargeConfidence,
					SmallIOU:         task.SmallIOU,
					AlarmLevelID:     defaultAlarmLevelID,
					RecordingPolicy:  defaultRecordingPolicy,
					AlarmPreSeconds:  defaultPre,
					AlarmPostSeconds: defaultPost,
				})
			}
		}
	}
	if len(profiles) == 0 {
		return task, []taskDeviceRuntime{}, nil
	}
	for idx := range profiles {
		rawValue := profiles[idx].FrameRateValue
		if rawValue <= 0 {
			rawValue = profiles[idx].FrameInterval
		}
		mode, value, err := s.validateTaskFrameRate(profiles[idx].FrameRateMode, rawValue)
		if err != nil {
			mode = s.taskFrameRateDefaultMode()
			value = s.taskFrameRateDefaultValue()
		}
		profiles[idx].FrameRateMode = mode
		profiles[idx].FrameRateValue = value
		if profiles[idx].FrameInterval <= 0 {
			profiles[idx].FrameInterval = value
		}
		if strings.TrimSpace(profiles[idx].AlarmLevelID) == "" {
			profiles[idx].AlarmLevelID = defaultAlarmLevelID
		}
	}

	deviceIDs := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		deviceIDs = append(deviceIDs, profile.DeviceID)
	}
	var devices []model.Device
	if err := s.db.Where("id IN ?", uniqueStrings(deviceIDs)).Find(&devices).Error; err != nil {
		return model.VideoTask{}, nil, err
	}
	deviceByID := make(map[string]model.Device, len(devices))
	for _, item := range devices {
		deviceByID[item.ID] = item
	}

	var relAlgorithms []model.VideoTaskDeviceAlgorithm
	if err := s.db.Where("task_id = ?", taskID).Find(&relAlgorithms).Error; err != nil {
		return model.VideoTask{}, nil, err
	}
	algorithmConfigsByDevice := make(map[string][]model.VideoTaskDeviceAlgorithm, len(deviceIDs))
	allAlgorithmIDs := make([]string, 0, len(relAlgorithms))
	for _, item := range relAlgorithms {
		algorithmID := strings.TrimSpace(item.AlgorithmID)
		if algorithmID == "" {
			continue
		}
		alarmLevelID := strings.TrimSpace(item.AlarmLevelID)
		if alarmLevelID == "" || !isBuiltinAlarmLevelID(alarmLevelID) {
			alarmLevelID = defaultAlarmLevelID
		}
		algorithmConfigsByDevice[item.DeviceID] = append(algorithmConfigsByDevice[item.DeviceID], model.VideoTaskDeviceAlgorithm{
			TaskID:            item.TaskID,
			DeviceID:          item.DeviceID,
			AlgorithmID:       algorithmID,
			AlarmLevelID:      alarmLevelID,
			AlertCycleSeconds: s.normalizeAlertCycleSecondsPersisted(item.AlertCycleSeconds),
		})
		allAlgorithmIDs = append(allAlgorithmIDs, algorithmID)
	}
	if len(relAlgorithms) == 0 {
		var legacyAlgorithms []model.VideoTaskAlgorithm
		if err := s.db.Where("task_id = ?", taskID).Find(&legacyAlgorithms).Error; err != nil {
			return model.VideoTask{}, nil, err
		}
		legacyIDs := make([]string, 0, len(legacyAlgorithms))
		for _, item := range legacyAlgorithms {
			legacyIDs = append(legacyIDs, item.AlgorithmID)
		}
		legacyIDs = uniqueStrings(legacyIDs)
		for _, profile := range profiles {
			legacyConfigs := make([]model.VideoTaskDeviceAlgorithm, 0, len(legacyIDs))
			for _, legacyID := range legacyIDs {
				legacyConfigs = append(legacyConfigs, model.VideoTaskDeviceAlgorithm{
					TaskID:            taskID,
					DeviceID:          profile.DeviceID,
					AlgorithmID:       legacyID,
					AlarmLevelID:      defaultAlarmLevelID,
					AlertCycleSeconds: s.taskAlertCycleDefault(),
				})
				allAlgorithmIDs = append(allAlgorithmIDs, legacyID)
			}
			algorithmConfigsByDevice[profile.DeviceID] = legacyConfigs
		}
	}

	var algorithms []model.Algorithm
	if len(allAlgorithmIDs) > 0 {
		if err := s.db.Where("id IN ?", uniqueStrings(allAlgorithmIDs)).Find(&algorithms).Error; err != nil {
			return model.VideoTask{}, nil, err
		}
	}
	algorithmByID := make(map[string]model.Algorithm, len(algorithms))
	for _, item := range algorithms {
		algorithmByID[item.ID] = item
	}

	runtimes := make([]taskDeviceRuntime, 0, len(profiles))
	for _, profile := range profiles {
		device, ok := deviceByID[profile.DeviceID]
		if !ok {
			continue
		}
		runtimeAlgorithmConfigs := make([]taskAlgorithmRuntime, 0)
		seenAlgorithms := make(map[string]struct{})
		for _, algorithmConfig := range algorithmConfigsByDevice[profile.DeviceID] {
			algorithmID := strings.TrimSpace(algorithmConfig.AlgorithmID)
			if algorithmID == "" {
				continue
			}
			if _, exists := seenAlgorithms[algorithmID]; exists {
				continue
			}
			algorithm, exists := algorithmByID[algorithmID]
			if !exists {
				continue
			}
			seenAlgorithms[algorithmID] = struct{}{}
			runtimeAlgorithmConfigs = append(runtimeAlgorithmConfigs, taskAlgorithmRuntime{
				Algorithm:         algorithm,
				AlarmLevelID:      firstNonEmpty(strings.TrimSpace(algorithmConfig.AlarmLevelID), defaultAlarmLevelID),
				AlertCycleSeconds: s.normalizeAlertCycleSecondsPersisted(algorithmConfig.AlertCycleSeconds),
			})
		}
		sort.Slice(runtimeAlgorithmConfigs, func(i, j int) bool {
			return strings.TrimSpace(runtimeAlgorithmConfigs[i].Algorithm.Name) < strings.TrimSpace(runtimeAlgorithmConfigs[j].Algorithm.Name)
		})
		runtimes = append(runtimes, taskDeviceRuntime{
			Profile:          profile,
			Device:           device,
			AlgorithmConfigs: runtimeAlgorithmConfigs,
		})
	}
	sort.Slice(runtimes, func(i, j int) bool {
		left := strings.TrimSpace(runtimes[i].Device.Name)
		right := strings.TrimSpace(runtimes[j].Device.Name)
		if left == right {
			return strings.TrimSpace(runtimes[i].Device.ID) < strings.TrimSpace(runtimes[j].Device.ID)
		}
		return left < right
	})
	return task, runtimes, nil
}

func (s *Server) buildDevicePlan(device model.Device, algorithms []model.Algorithm) (deviceLaunchPlan, error) {
	labels := make([]string, 0)
	promptTasks := make([]llmPromptTask, 0)
	algorithmConfigs := make([]ai.StartCameraAlgorithmConfig, 0)

	for _, algorithm := range algorithms {
		if !algorithm.Enabled {
			continue
		}
		detectMode := resolveAlgorithmDetectMode(algorithm)
		algorithmLabels := []string{}
		if detectMode == model.AlgorithmDetectModeSmallOnly || detectMode == model.AlgorithmDetectModeHybrid {
			algorithmLabels = normalizeSmallModelLabels([]string{algorithm.SmallModelLabel})
			if len(algorithmLabels) == 0 {
				return deviceLaunchPlan{}, fmt.Errorf("small model labels missing for algorithm %s", algorithm.Name)
			}
		}
		algorithmCode := resolveAlgorithmTaskCode(algorithm)
		if algorithmCode == "" {
			return deviceLaunchPlan{}, fmt.Errorf("algorithm code missing for algorithm %s", algorithm.Name)
		}
		algorithmConfigs = append(algorithmConfigs, ai.StartCameraAlgorithmConfig{
			AlgorithmID:       algorithm.ID,
			TaskCode:          algorithmCode,
			DetectMode:        detectMode,
			Labels:            algorithmLabels,
			YoloThreshold:     clamp(algorithm.YoloThreshold, 0.01, 0.99, 0.5),
			IOUThreshold:      clamp(algorithm.IOUThreshold, 0.1, 0.99, 0.8),
			LabelsTriggerMode: normalizeLabelsTriggerMode(algorithm.LabelsTriggerMode),
		})
		if detectMode == model.AlgorithmDetectModeSmallOnly || detectMode == model.AlgorithmDetectModeHybrid {
			for _, label := range algorithmLabels {
				if !slices.Contains(labels, label) {
					labels = append(labels, label)
				}
			}
		}

		if detectMode == model.AlgorithmDetectModeLLMOnly || detectMode == model.AlgorithmDetectModeHybrid {
			prompt, err := s.getActivePrompt(algorithm.ID)
			if err != nil || prompt == nil {
				return deviceLaunchPlan{}, fmt.Errorf("active prompt missing for algorithm %s", algorithm.Name)
			}
			promptTasks = append(promptTasks, llmPromptTask{
				TaskCode: algorithmCode,
				TaskName: strings.TrimSpace(algorithm.Name),
				TaskMode: taskModeByAlgorithm(algorithm.Mode),
				Goal:     strings.TrimSpace(prompt.Prompt),
			})
		}
	}
	if len(algorithmConfigs) == 0 {
		return deviceLaunchPlan{}, fmt.Errorf("no enabled algorithm configured for device %s", strings.TrimSpace(device.Name))
	}
	sort.Slice(promptTasks, func(i, j int) bool {
		if promptTasks[i].TaskCode == promptTasks[j].TaskCode {
			return promptTasks[i].TaskName < promptTasks[j].TaskName
		}
		return promptTasks[i].TaskCode < promptTasks[j].TaskCode
	})
	sort.Slice(algorithmConfigs, func(i, j int) bool {
		if algorithmConfigs[i].TaskCode == algorithmConfigs[j].TaskCode {
			return algorithmConfigs[i].AlgorithmID < algorithmConfigs[j].AlgorithmID
		}
		return algorithmConfigs[i].TaskCode < algorithmConfigs[j].TaskCode
	})

	provider := model.ModelProvider{}
	if len(promptTasks) > 0 {
		configuredProvider, err := s.getConfiguredLLMProvider()
		if err != nil {
			return deviceLaunchPlan{}, err
		}
		provider = configuredProvider
	}

	prompt := ""
	if len(promptTasks) > 0 {
		role := strings.TrimSpace(s.getSetting("llm_role"))
		if role == "" {
			role = defaultLLMRoleText()
		}
		outputRequirement := strings.TrimSpace(s.getSetting("llm_output_requirement"))
		if outputRequirement == "" {
			outputRequirement = defaultLLMOutputRequirementText()
		}
		promptText, err := composeUnifiedLLMPrompt(role, outputRequirement, promptTasks)
		if err != nil {
			return deviceLaunchPlan{}, err
		}
		prompt = promptText
	}

	return deviceLaunchPlan{
		Device:           device,
		Labels:           labels,
		Prompt:           prompt,
		PromptTasks:      promptTasks,
		Provider:         provider,
		Algorithms:       algorithms,
		AlgorithmConfigs: algorithmConfigs,
	}, nil
}

func taskModeByAlgorithm(_ string) string {
	return "object"
}

func resolveAlgorithmTaskCode(algorithm model.Algorithm) string {
	code := normalizeAlgorithmCode(algorithm.Code)
	if code != "" {
		return code
	}
	fallback := strings.ToUpper(strings.TrimSpace(algorithm.ID))
	fallback = strings.ReplaceAll(fallback, "-", "_")
	fallback = strings.ReplaceAll(fallback, " ", "_")
	fallback = strings.Trim(fallback, "_")
	if fallback == "" {
		return ""
	}
	if !algorithmCodePattern.MatchString(fallback) {
		if len(fallback) > 28 {
			fallback = fallback[:28]
		}
		fallback = "ALG_" + fallback
	}
	if len(fallback) > 32 {
		fallback = fallback[:32]
	}
	if !algorithmCodePattern.MatchString(fallback) {
		return ""
	}
	return fallback
}

func defaultLLMRoleText() string {
	return "You are a multi-task video alarm analysis expert. Analyze tasks one by one and output strict JSON."
}

func defaultLLMOutputRequirementText() string {
	parts := []string{
		"Return valid JSON only, without markdown or extra text.",
		"Output must include: version, overall{alarm,alarm_task_codes}, task_results[], objects[].",
		"Each task_results item must include: task_code, task_name, alarm, reason, object_ids.",
		"Each objects item must include: object_id, task_code, bbox2d[x0,y0,x1,y1], label, confidence.",
		"bbox2d uses normalized 0..1000 coordinates and must satisfy x0<x1 and y0<y1.",
		"overall.alarm must be consistent with task_results where alarm=1.",
	}
	return strings.Join(parts, "\n")
}

func composeUnifiedLLMPrompt(role, outputRequirement string, promptTasks []llmPromptTask) (string, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		role = defaultLLMRoleText()
	}
	outputRequirement = strings.TrimSpace(outputRequirement)
	if outputRequirement == "" {
		outputRequirement = defaultLLMOutputRequirementText()
	}
	type promptTaskForLLM struct {
		TaskCode string `json:"task_code"`
		TaskName string `json:"task_name"`
		Goal     string `json:"goal"`
	}
	taskPayload := make([]promptTaskForLLM, 0, len(promptTasks))
	for _, item := range promptTasks {
		taskPayload = append(taskPayload, promptTaskForLLM{
			TaskCode: strings.TrimSpace(item.TaskCode),
			TaskName: strings.TrimSpace(item.TaskName),
			Goal:     strings.TrimSpace(item.Goal),
		})
	}
	taskJSON, err := json.MarshalIndent(taskPayload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal prompt tasks failed: %w", err)
	}
	prompt := strings.TrimSpace(fmt.Sprintf(
		"%s\n\n## [任务清单]\n%s\n\n## [统一输出JSON协议]\n%s",
		role,
		string(taskJSON),
		outputRequirement,
	))
	return prompt, nil
}

func toTaskSummary(task model.VideoTask) taskSummary {
	return taskSummary{
		ID:          task.ID,
		Name:        task.Name,
		Status:      task.Status,
		Notes:       task.Notes,
		LastStartAt: task.LastStartAt,
		LastStopAt:  task.LastStopAt,
		CreatedAt:   task.CreatedAt,
		UpdatedAt:   task.UpdatedAt,
	}
}

func runtimeAlgorithms(items []taskAlgorithmRuntime) []model.Algorithm {
	out := make([]model.Algorithm, 0, len(items))
	for _, item := range items {
		out = append(out, item.Algorithm)
	}
	return out
}

func (s *Server) normalizeAlertCycleSecondsUpsert(raw *int) (int, error) {
	if raw == nil {
		return s.taskAlertCycleDefault(), nil
	}
	value := *raw
	if value < 0 || value > maxAlertCycleSeconds {
		return 0, fmt.Errorf("alert_cycle_seconds must be in range 0..%d", maxAlertCycleSeconds)
	}
	return value, nil
}

func (s *Server) normalizeAlertCycleSecondsPersisted(raw int) int {
	if raw < 0 || raw > maxAlertCycleSeconds {
		return s.taskAlertCycleDefault()
	}
	return raw
}

func (s *Server) defaultAlarmLevelID() (string, error) {
	if s != nil && s.cfg != nil && isBuiltinAlarmLevelID(s.cfg.Server.TaskDefaults.Video.AlarmLevelIDDefault) {
		return strings.TrimSpace(s.cfg.Server.TaskDefaults.Video.AlarmLevelIDDefault), nil
	}
	return builtinAlarmLevelID1, nil
}

func (s *Server) taskAlertCycleDefault() int {
	if s != nil && s.cfg != nil {
		value := s.cfg.Server.TaskDefaults.Video.AlertCycleSecondsDefault
		if value >= 0 && value <= maxAlertCycleSeconds {
			return value
		}
	}
	return defaultAlertCycleSeconds
}

func (s *Server) taskFrameRateModes() []string {
	if s != nil && s.cfg != nil && len(s.cfg.Server.TaskDefaults.Video.FrameRateModes) > 0 {
		out := make([]string, 0, len(s.cfg.Server.TaskDefaults.Video.FrameRateModes))
		for _, item := range s.cfg.Server.TaskDefaults.Video.FrameRateModes {
			mode := normalizeTaskFrameRateMode(item)
			if mode == "" {
				continue
			}
			out = append(out, mode)
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{model.FrameRateModeInterval, model.FrameRateModeFPS}
}

func (s *Server) taskFrameRateDefaultMode() string {
	if s != nil && s.cfg != nil {
		mode := normalizeTaskFrameRateMode(s.cfg.Server.TaskDefaults.Video.FrameRateModeDefault)
		if mode != "" {
			for _, item := range s.taskFrameRateModes() {
				if item == mode {
					return mode
				}
			}
		}
	}
	return model.FrameRateModeInterval
}

func (s *Server) taskFrameRateDefaultValue() int {
	if s != nil && s.cfg != nil {
		value := s.cfg.Server.TaskDefaults.Video.FrameRateValueDefault
		if value >= 1 && value <= 60 {
			return value
		}
	}
	return 5
}

func (s *Server) taskRecordingPolicyDefault() string {
	if s != nil && s.cfg != nil && s.cfg.Server.Recording.AlarmClip.EnabledDefault {
		return model.RecordingPolicyAlarmClip
	}
	return model.RecordingPolicyNone
}

// 页面默认值拆成两段配置：录制窗口沿用 AlarmClip，任务侧参数走 TaskDefaults.Video。
func (s *Server) taskDefaultsPayload() taskDefaultsResponse {
	return taskDefaultsResponse{
		RecordingPolicyDefault:      s.taskRecordingPolicyDefault(),
		AlarmClipEnabledDefault:     s != nil && s.cfg != nil && s.cfg.Server.Recording.AlarmClip.EnabledDefault,
		RecordingPreSecondsDefault:  s.alarmClipDefaultPreSeconds(),
		RecordingPostSecondsDefault: s.alarmClipDefaultPostSeconds(),
		AlertCycleSecondsDefault:    s.taskAlertCycleDefault(),
		AlarmLevelIDDefault: func() string {
			levelID, _ := s.defaultAlarmLevelID()
			return levelID
		}(),
		FrameRateModes:        s.taskFrameRateModes(),
		FrameRateModeDefault:  s.taskFrameRateDefaultMode(),
		FrameRateValueDefault: s.taskFrameRateDefaultValue(),
	}
}

func builtinAlarmLevelIDs() []string {
	return []string{
		builtinAlarmLevelID1,
		builtinAlarmLevelID2,
		builtinAlarmLevelID3,
	}
}

func isBuiltinAlarmLevelID(id string) bool {
	normalized := strings.TrimSpace(id)
	for _, levelID := range builtinAlarmLevelIDs() {
		if normalized == levelID {
			return true
		}
	}
	return false
}

func levelColorByID(id string) string {
	for _, item := range builtinAlarmLevels {
		if strings.TrimSpace(id) == item.ID {
			return item.Color
		}
	}
	return "#faad14"
}

func builtinAlarmLevelIDBySeverity(severity int) string {
	if severity <= 2 {
		return builtinAlarmLevelID1
	}
	if severity == 3 {
		return builtinAlarmLevelID2
	}
	return builtinAlarmLevelID3
}

func shouldResetBuiltinAlarmLevelName(id, currentName string) bool {
	name := strings.TrimSpace(currentName)
	if name == "" {
		return true
	}
	switch strings.TrimSpace(id) {
	case builtinAlarmLevelID1:
		return name == "一级（低）"
	case builtinAlarmLevelID2:
		return name == "二级（较低）"
	case builtinAlarmLevelID3:
		return name == "三级（中）"
	default:
		return false
	}
}

func applyAlarmLevelIDMapping(tx *gorm.DB, tableModel any, mapping map[string]string) error {
	if tx == nil || len(mapping) == 0 {
		return nil
	}
	oldIDs := make([]string, 0, len(mapping))
	for oldID, newID := range mapping {
		if oldID == "" || newID == "" || oldID == newID {
			continue
		}
		oldIDs = append(oldIDs, oldID)
	}
	if len(oldIDs) == 0 {
		return nil
	}
	slices.Sort(oldIDs)

	caseParts := make([]string, 0, len(oldIDs))
	args := make([]any, 0, len(oldIDs)*2)
	for _, oldID := range oldIDs {
		caseParts = append(caseParts, "WHEN ? THEN ?")
		args = append(args, oldID, mapping[oldID])
	}
	caseExpr := "CASE alarm_level_id " + strings.Join(caseParts, " ") + " ELSE alarm_level_id END"

	return tx.
		Model(tableModel).
		Where("alarm_level_id IN ?", oldIDs).
		Update("alarm_level_id", gorm.Expr(caseExpr, args...)).
		Error
}

func (s *Server) loadBuiltinAlarmLevelMap() (map[string]model.AlarmLevel, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("database is not ready")
	}
	var levels []model.AlarmLevel
	if err := s.db.Where("id IN ?", builtinAlarmLevelIDs()).Find(&levels).Error; err != nil {
		return nil, err
	}
	out := make(map[string]model.AlarmLevel, len(levels))
	for _, level := range levels {
		out[strings.TrimSpace(level.ID)] = level
	}
	return out, nil
}

func (s *Server) normalizeBuiltinAlarmLevels() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("database is not ready")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var existing []model.AlarmLevel
		if err := tx.Order("severity asc, created_at asc").Find(&existing).Error; err != nil {
			return err
		}

		legacyFiveLevelMode := false
		for _, row := range existing {
			id := strings.TrimSpace(row.ID)
			name := strings.TrimSpace(row.Name)
			if id == "alarm_level_4" || id == "alarm_level_5" || row.Severity > 3 {
				legacyFiveLevelMode = true
				break
			}
			if (id == builtinAlarmLevelID2 && name == "二级（较低）") || (id == builtinAlarmLevelID3 && name == "三级（中）") {
				legacyFiveLevelMode = true
				break
			}
		}

		mapping := make(map[string]string, len(existing)+len(builtinAlarmLevels))
		for _, row := range existing {
			oldID := strings.TrimSpace(row.ID)
			if oldID == "" {
				continue
			}
			target := oldID
			if !isBuiltinAlarmLevelID(oldID) || legacyFiveLevelMode {
				target = builtinAlarmLevelIDBySeverity(row.Severity)
			}
			mapping[oldID] = target
		}
		for _, spec := range builtinAlarmLevels {
			if _, exists := mapping[spec.ID]; !exists {
				mapping[spec.ID] = spec.ID
			}
		}

		for _, spec := range builtinAlarmLevels {
			var level model.AlarmLevel
			err := tx.Where("id = ?", spec.ID).First(&level).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(&model.AlarmLevel{
					ID:          spec.ID,
					Name:        spec.Name,
					Severity:    spec.Severity,
					Color:       spec.Color,
					Description: spec.Description,
				}).Error; err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			updates := map[string]any{
				"severity": spec.Severity,
			}
			if shouldResetBuiltinAlarmLevelName(spec.ID, level.Name) {
				updates["name"] = spec.Name
			}
			if strings.TrimSpace(level.Color) == "" {
				updates["color"] = spec.Color
			}
			if strings.TrimSpace(level.Description) == "" {
				updates["description"] = spec.Description
			}
			if err := tx.Model(&model.AlarmLevel{}).Where("id = ?", spec.ID).Updates(updates).Error; err != nil {
				return err
			}
		}

		if err := applyAlarmLevelIDMapping(tx, &model.VideoTask{}, mapping); err != nil {
			return err
		}
		if err := applyAlarmLevelIDMapping(tx, &model.VideoTaskDeviceProfile{}, mapping); err != nil {
			return err
		}
		if err := applyAlarmLevelIDMapping(tx, &model.VideoTaskDeviceAlgorithm{}, mapping); err != nil {
			return err
		}
		if err := applyAlarmLevelIDMapping(tx, &model.AlarmEvent{}, mapping); err != nil {
			return err
		}

		defaultLevelID, err := s.defaultAlarmLevelID()
		if err != nil {
			return err
		}
		if err := tx.Model(&model.VideoTask{}).
			Where("alarm_level_id = '' OR alarm_level_id IS NULL").
			Update("alarm_level_id", defaultLevelID).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.VideoTaskDeviceProfile{}).
			Where("alarm_level_id = '' OR alarm_level_id IS NULL").
			Update("alarm_level_id", defaultLevelID).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.VideoTaskDeviceAlgorithm{}).
			Where("alarm_level_id = '' OR alarm_level_id IS NULL").
			Update("alarm_level_id", defaultLevelID).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AlarmEvent{}).
			Where("alarm_level_id = '' OR alarm_level_id IS NULL").
			Update("alarm_level_id", defaultLevelID).Error; err != nil {
			return err
		}

		if err := tx.Where("id NOT IN ?", builtinAlarmLevelIDs()).Delete(&model.AlarmLevel{}).Error; err != nil {
			return err
		}
		return nil
	})
}

const (
	taskAutoResumeEnabledValue  = "enabled"
	taskAutoResumeDisabledValue = "disabled"
)

func taskAutoResumeSettingKey(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	return "task_auto_resume:" + taskID
}

func (s *Server) setTaskAutoResumeIntent(taskID string, enabled bool) error {
	if s == nil || s.db == nil {
		return nil
	}
	key := taskAutoResumeSettingKey(taskID)
	if key == "" {
		return nil
	}
	value := taskAutoResumeDisabledValue
	if enabled {
		value = taskAutoResumeEnabledValue
	}
	return s.upsertSetting(key, value)
}

func (s *Server) isTaskAutoResumeEnabled(taskID string) bool {
	key := taskAutoResumeSettingKey(taskID)
	if key == "" {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(s.getSetting(key)))
	return value != taskAutoResumeDisabledValue
}

func (s *Server) startTaskRuntimes(ctx context.Context, taskID string, runtimes []taskDeviceRuntime, onlyDeviceIDs map[string]struct{}, reason string) ([]taskActionResult, int) {
	results := make([]taskActionResult, 0, len(runtimes))
	successCount := 0
	if s == nil || s.db == nil || s.aiClient == nil {
		return results, successCount
	}
	if err := s.ensureLLMTokenQuotaAvailable(); err != nil {
		if isLLMTokenLimitExceededError(err) {
			s.disableTaskAutoResumeForQuota(taskID)
			return s.buildQuotaBlockedTaskResults(runtimes, onlyDeviceIDs), 0
		}
		return append(results, taskActionResult{
			DeviceID: "",
			Success:  false,
			Message:  err.Error(),
		}), 0
	}
	for _, item := range runtimes {
		device := item.Device
		if len(onlyDeviceIDs) > 0 {
			if _, ok := onlyDeviceIDs[device.ID]; !ok {
				continue
			}
		}
		algorithms := runtimeAlgorithms(item.AlgorithmConfigs)
		plan, err := s.buildDevicePlan(device, algorithms)
		if err != nil {
			results = append(results, taskActionResult{DeviceID: device.ID, Success: false, Message: err.Error()})
			_ = s.db.Model(&model.Device{}).Where("id = ?", device.ID).Update("ai_status", model.DeviceAIStatusError).Error
			_ = s.applyRecordingPolicyForSourceID(device.ID)
			continue
		}
		rtspURL := pickDeviceRTSPURLForAI(device)
		if rtspURL == "" {
			results = append(results, taskActionResult{DeviceID: device.ID, Success: false, Message: "device has no valid rtsp input for ai"})
			_ = s.db.Model(&model.Device{}).Where("id = ?", device.ID).Update("ai_status", model.DeviceAIStatusError).Error
			_ = s.applyRecordingPolicyForSourceID(device.ID)
			continue
		}
		rtspURL = s.rewriteRTSPForAI(rtspURL)
		req := ai.StartCameraRequest{
			CameraID:         device.ID,
			RTSPURL:          rtspURL,
			CallbackURL:      s.cfg.Server.AI.CallbackURL,
			CallbackSecret:   s.cfg.Server.AI.CallbackToken,
			RetryLimit:       aiStartCameraRetryLimit,
			DetectRateMode:   item.Profile.FrameRateMode,
			DetectRateValue:  item.Profile.FrameRateValue,
			AlgorithmConfigs: plan.AlgorithmConfigs,
			LLMAPIURL:        plan.Provider.APIURL,
			LLMAPIKey:        plan.Provider.APIKey,
			LLMModel:         plan.Provider.Model,
			LLMPrompt:        plan.Prompt,
		}
		logutil.Debugf("task start request: task_id=%s device_id=%s reason=%s ai_service_url=%s payload=%s", taskID, device.ID, strings.TrimSpace(reason), strings.TrimSpace(s.cfg.Server.AI.ServiceURL), marshalJSONForLog(req))
		callCtx := ctx
		if callCtx == nil {
			callCtx = context.Background()
		}
		callCtx, cancel := context.WithTimeout(callCtx, 15*time.Second)
		resp, err := s.aiClient.StartCamera(callCtx, req)
		cancel()
		if err != nil {
			logutil.Warnf("task start response: task_id=%s device_id=%s reason=%s error=%v", taskID, device.ID, reason, err)
		} else {
			logutil.Debugf("task start response: task_id=%s device_id=%s reason=%s payload=%s", taskID, device.ID, reason, marshalJSONForLog(resp))
		}
		if err != nil || resp == nil || !resp.Success {
			msg := "ai start failed"
			if err != nil {
				msg = err.Error()
			} else if resp != nil && strings.TrimSpace(resp.Message) != "" {
				msg = resp.Message
			}
			results = append(results, taskActionResult{DeviceID: device.ID, Success: false, Message: msg})
			_ = s.db.Model(&model.Device{}).Where("id = ?", device.ID).Update("ai_status", model.DeviceAIStatusError).Error
			_ = s.applyRecordingPolicyForSourceID(device.ID)
			continue
		}
		successCount++
		_ = s.db.Model(&model.Device{}).Where("id = ?", device.ID).Updates(map[string]any{
			"ai_status":  model.DeviceAIStatusRunning,
			"updated_at": time.Now(),
		}).Error
		_ = s.applyRecordingPolicyForSourceID(device.ID)
		results = append(results, taskActionResult{DeviceID: device.ID, Success: true, Message: resp.Message})
		s.clearStartupResumePending(device.ID)
	}
	return results, successCount
}

func (s *Server) updateTaskRuntimeStatus(taskID string, runtimes []taskDeviceRuntime, updateLastStart bool) (string, error) {
	nextStatus, _, totalCount, err := s.computeTaskRuntimeStatus(runtimes)
	if err != nil {
		return model.TaskStatusStopped, err
	}
	updates := map[string]any{"status": nextStatus}
	if updateLastStart && totalCount > 0 {
		updates["last_start_at"] = time.Now()
	}
	return nextStatus, s.db.Model(&model.VideoTask{}).Where("id = ?", taskID).Updates(updates).Error
}

func (s *Server) computeTaskRuntimeStatus(runtimes []taskDeviceRuntime) (string, int, int, error) {
	if s == nil || s.db == nil {
		return model.TaskStatusStopped, 0, 0, nil
	}
	deviceIDs := make([]string, 0, len(runtimes))
	for _, item := range runtimes {
		if id := strings.TrimSpace(item.Device.ID); id != "" {
			deviceIDs = append(deviceIDs, id)
		}
	}
	if len(deviceIDs) == 0 {
		return model.TaskStatusStopped, 0, 0, nil
	}
	var devices []model.Device
	if err := s.db.Select("id", "ai_status").Where("id IN ?", uniqueStrings(deviceIDs)).Find(&devices).Error; err != nil {
		return model.TaskStatusStopped, 0, 0, err
	}
	runningCount := 0
	for _, item := range devices {
		if item.AIStatus == model.DeviceAIStatusRunning {
			runningCount++
		}
	}
	status := model.TaskStatusStopped
	if runningCount == len(deviceIDs) {
		status = model.TaskStatusRunning
	} else if runningCount > 0 {
		status = model.TaskStatusPartialFail
	}
	return status, runningCount, len(deviceIDs), nil
}

func (s *Server) fetchAIRunningDevices(ctx context.Context) (*ai.StatusResponse, map[string]struct{}, error) {
	resp, err := s.aiClient.Status(ctx)
	if err != nil {
		return nil, nil, err
	}
	running := make(map[string]struct{}, len(resp.Cameras))
	for _, camera := range resp.Cameras {
		if strings.EqualFold(camera.Status, "running") {
			running[camera.CameraID] = struct{}{}
		}
	}
	return resp, running, nil
}

func (s *Server) syncTaskStatusFromRunningSet(taskID string, running map[string]struct{}) (string, int, int, error) {
	_, runtimes, err := s.loadTaskDeviceContexts(taskID)
	if err != nil {
		return "", 0, 0, err
	}
	runningCount := 0
	for _, item := range runtimes {
		status := model.DeviceAIStatusStopped
		if _, ok := running[item.Device.ID]; ok {
			status = model.DeviceAIStatusRunning
			runningCount++
			s.clearStartupResumePending(item.Device.ID)
		}
		_ = s.db.Model(&model.Device{}).Where("id = ?", item.Device.ID).Update("ai_status", status).Error
		_ = s.applyRecordingPolicyForSourceID(item.Device.ID)
	}
	nextTaskStatus := model.TaskStatusStopped
	if runningCount == len(runtimes) && len(runtimes) > 0 {
		nextTaskStatus = model.TaskStatusRunning
	} else if runningCount > 0 {
		nextTaskStatus = model.TaskStatusPartialFail
	}
	if err := s.db.Model(&model.VideoTask{}).Where("id = ?", taskID).Update("status", nextTaskStatus).Error; err != nil {
		return "", 0, 0, err
	}
	return nextTaskStatus, runningCount, len(runtimes), nil
}

func (s *Server) syncAllTaskStatusesFromRunningSet(running map[string]struct{}) error {
	if s == nil || s.db == nil {
		return nil
	}
	var tasks []model.VideoTask
	if err := s.db.Select("id").Find(&tasks).Error; err != nil {
		return err
	}
	var firstErr error
	for _, task := range tasks {
		if _, _, _, err := s.syncTaskStatusFromRunningSet(task.ID, running); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Server) listAutoResumeCandidateTaskIDs() ([]string, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var tasks []model.VideoTask
	if err := s.db.Select("id", "status").Find(&tasks).Error; err != nil {
		return nil, err
	}
	type runningRow struct {
		TaskID string `gorm:"column:task_id"`
	}
	var runningRows []runningRow
	if err := s.db.Model(&model.VideoTaskDeviceProfile{}).
		Select("DISTINCT mb_video_task_device_profiles.task_id AS task_id").
		Joins("JOIN mb_media_sources ON mb_media_sources.id = mb_video_task_device_profiles.device_id").
		Where("mb_media_sources.ai_status = ?", model.DeviceAIStatusRunning).
		Find(&runningRows).Error; err != nil {
		return nil, err
	}
	runningTaskIDs := make(map[string]struct{}, len(runningRows))
	for _, row := range runningRows {
		if id := strings.TrimSpace(row.TaskID); id != "" {
			runningTaskIDs[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.ID)
		if taskID == "" || !s.isTaskAutoResumeEnabled(taskID) {
			continue
		}
		if task.Status == model.TaskStatusRunning || task.Status == model.TaskStatusPartialFail {
			out = append(out, taskID)
			continue
		}
		if _, ok := runningTaskIDs[taskID]; ok {
			out = append(out, taskID)
		}
	}
	return out, nil
}

func (s *Server) getSetting(key string) string {
	var setting model.SystemSetting
	if err := s.db.Where("key = ?", key).First(&setting).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(setting.Value)
}

func pickDeviceRTSPURLForAI(device model.Device) string {
	candidates := make([]string, 0, 4)
	candidates = append(candidates, strings.TrimSpace(device.PlayRTSPURL))
	if output, err := parseOutputConfigMap(device.OutputConfig); err == nil {
		candidates = append(candidates,
			strings.TrimSpace(output["rtsp"]),
			strings.TrimSpace(output["rtsp_url"]),
		)
	}
	candidates = append(candidates, strings.TrimSpace(device.StreamURL))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if strings.HasPrefix(strings.ToLower(candidate), "rtsp://") {
			return candidate
		}
	}
	return ""
}

func (s *Server) rewriteRTSPForAI(raw string) string {
	rtspURL := strings.TrimSpace(raw)
	if rtspURL == "" || s == nil || s.cfg == nil {
		return rtspURL
	}
	aiHost := normalizeHost(strings.TrimSpace(s.cfg.Server.ZLM.AIInputHost))
	if aiHost == "" {
		return rtspURL
	}
	u, err := url.Parse(rtspURL)
	if err != nil {
		return rtspURL
	}
	if !strings.EqualFold(strings.TrimSpace(u.Scheme), "rtsp") {
		return rtspURL
	}
	if !shouldRewriteRTSPHostForAI(u.Hostname(), s.cfg.Server.ZLM.PlayHost, aiHost) {
		return rtspURL
	}
	port := strings.TrimSpace(u.Port())
	u.Host = buildRTSPHostPort(aiHost, port)
	return u.String()
}

func shouldRewriteRTSPHostForAI(host, playHost, aiHost string) bool {
	normalized := strings.ToLower(strings.TrimSpace(normalizeHost(host)))
	if normalized == "" {
		return false
	}
	switch normalized {
	case "127.0.0.1", "localhost", "host.docker.internal", "::1":
		return true
	}
	normalizedPlayHost := strings.ToLower(strings.TrimSpace(normalizeHost(playHost)))
	if normalizedPlayHost == "" || normalized != normalizedPlayHost {
		return false
	}
	return !isLoopbackHost(aiHost)
}

func isLoopbackHost(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(normalizeHost(raw)))
	if normalized == "" {
		return false
	}
	switch normalized {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	ip := net.ParseIP(normalized)
	return ip != nil && ip.IsLoopback()
}

func buildRTSPHostPort(host, port string) string {
	host = strings.TrimSpace(normalizeHost(host))
	if host == "" {
		return ""
	}
	port = strings.TrimSpace(port)
	if port == "" {
		if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") && !strings.HasSuffix(host, "]") {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, port)
}

func marshalJSONForLog(v any) string {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"marshal_error":%q}`, err.Error())
	}
	return string(body)
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func clamp(v, low, high, fallback float64) float64 {
	if v <= 0 {
		return fallback
	}
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func parseLLMResult(raw string) llmPromptResult {
	out := llmPromptResult{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(raw, "```")
		raw = strings.TrimSpace(raw)
	}
	_ = json.Unmarshal([]byte(raw), &out)
	out.Overall.Alarm = normalizeAlarmFlag(out.Overall.Alarm)
	out.Overall.AlarmTaskCodes = uniqueStrings(out.Overall.AlarmTaskCodes)
	normalizedTaskResults := make([]llmPromptTaskResult, 0, len(out.TaskResults))
	for _, item := range out.TaskResults {
		item.TaskCode = normalizeAlgorithmCode(item.TaskCode)
		item.TaskName = strings.TrimSpace(item.TaskName)
		item.TaskMode = strings.ToLower(strings.TrimSpace(item.TaskMode))
		item.Reason = strings.TrimSpace(item.Reason)
		item.Suggestion = strings.TrimSpace(item.Suggestion)
		item.Excluded = normalizeStringSlice(item.Excluded)
		item.ObjectIDs = uniqueStrings(item.ObjectIDs)
		item.Alarm = normalizeAlarmValue(item.Alarm)
		if item.TaskCode == "" {
			continue
		}
		normalizedTaskResults = append(normalizedTaskResults, item)
	}
	out.TaskResults = normalizedTaskResults
	normalizedObjects := make([]llmPromptObject, 0, len(out.Objects))
	for _, item := range out.Objects {
		item.ObjectID = strings.TrimSpace(item.ObjectID)
		item.TaskCode = normalizeAlgorithmCode(item.TaskCode)
		item.Label = strings.TrimSpace(item.Label)
		if len(item.BBox2D) > 4 {
			item.BBox2D = item.BBox2D[:4]
		}
		if item.TaskCode == "" {
			continue
		}
		normalizedObjects = append(normalizedObjects, item)
	}
	out.Objects = normalizedObjects
	return out
}

func normalizeAlarmFlag(raw string) string {
	v := strings.TrimSpace(strings.ToLower(raw))
	switch v {
	case "1", "true", "yes":
		return "1"
	default:
		return "0"
	}
}

func normalizeAlarmValue(raw any) string {
	switch v := raw.(type) {
	case string:
		return normalizeAlarmFlag(v)
	case bool:
		if v {
			return "1"
		}
		return "0"
	case float64:
		if v != 0 {
			return "1"
		}
		return "0"
	case int:
		if v != 0 {
			return "1"
		}
		return "0"
	default:
		return "0"
	}
}

func normalizeStringSlice(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}
