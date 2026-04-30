package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"maas-box/internal/ai"
	"maas-box/internal/model"
)

const (
	camera2PatrolJobTTL          = 30 * time.Minute
	camera2PatrolDisplayName     = "任务巡查"
	camera2PatrolAnalyzeTimeout  = 20 * time.Second
	camera2PatrolSnapshotOwnerID = "camera2_patrol"
)

type camera2PatrolCreateRequest struct {
	DeviceIDs   []string `json:"device_ids"`
	AlgorithmID string   `json:"algorithm_id"`
	Prompt      string   `json:"prompt"`
}

type camera2PatrolJob struct {
	mu               sync.RWMutex
	snapshot         camera2PatrolJobSnapshot
	items            []camera2PatrolJobItem
	provider         model.ModelProvider
	algorithmCfg     ai.StartCameraAlgorithmConfig
	llmPrompt        string
	promptText       string
	displayName      string
	eventAlgorithmID string
	expiresAt        time.Time
}

type camera2PatrolJobItem struct {
	DeviceID   string
	DeviceName string
	AreaID     string
	AreaName   string
	Source     *model.MediaSource
}

type camera2PatrolJobSnapshot struct {
	JobID        string                       `json:"job_id"`
	Status       string                       `json:"status"`
	TotalCount   int                          `json:"total_count"`
	SuccessCount int                          `json:"success_count"`
	FailedCount  int                          `json:"failed_count"`
	AlarmCount   int                          `json:"alarm_count"`
	Items        []camera2PatrolJobItemResult `json:"items"`
}

type camera2PatrolJobItemResult struct {
	DeviceID      string `json:"device_id"`
	DeviceName    string `json:"device_name"`
	Status        string `json:"status"`
	Message       string `json:"message"`
	EventID       string `json:"event_id,omitempty"`
	LLMCallStatus string `json:"-"`
	AIRequestType string `json:"-"`
	ErrorMessage  string `json:"-"`
}

func (s *Server) createCamera2PatrolJob(c *gin.Context) {
	var in camera2PatrolCreateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	job, err := s.newCamera2PatrolJob(in)
	if err != nil {
		if isBadRequestError(err) {
			s.fail(c, http.StatusBadRequest, err.Error())
			return
		}
		s.fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.ok(c, gin.H{
		"job_id":      job.snapshot.JobID,
		"status":      job.snapshot.Status,
		"total_count": job.snapshot.TotalCount,
	})
}

func (s *Server) getCamera2PatrolJob(c *gin.Context) {
	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		s.fail(c, http.StatusBadRequest, "job id is required")
		return
	}
	snapshot, ok := s.loadCamera2PatrolJob(jobID)
	if !ok {
		s.fail(c, http.StatusNotFound, "patrol job not found")
		return
	}
	s.ok(c, snapshot)
}

func (s *Server) newCamera2PatrolJob(in camera2PatrolCreateRequest) (*camera2PatrolJob, error) {
	deviceIDs := uniqueStrings(trimmedStrings(in.DeviceIDs))
	if len(deviceIDs) == 0 {
		return nil, errBadRequest("请至少选择一个视频设备")
	}

	algorithmID := strings.TrimSpace(in.AlgorithmID)
	promptText := strings.TrimSpace(in.Prompt)
	if (algorithmID == "" && promptText == "") || (algorithmID != "" && promptText != "") {
		return nil, errBadRequest("算法和提示词必须二选一")
	}
	if err := s.ensureLLMTokenQuotaAvailable(); err != nil {
		if isLLMTokenLimitExceededError(err) {
			return nil, errBadRequest(llmTokenLimitExceededMessage)
		}
		return nil, err
	}

	provider, algorithmCfg, llmPrompt, displayName, actualPromptText, eventAlgorithmID, err := s.buildCamera2PatrolConfig(algorithmID, promptText)
	if err != nil {
		return nil, err
	}
	items, snapshotItems, err := s.buildCamera2PatrolItems(deviceIDs)
	if err != nil {
		return nil, err
	}

	job := &camera2PatrolJob{
		snapshot: camera2PatrolJobSnapshot{
			JobID:      uuid.NewString(),
			Status:     model.AlgorithmTestJobStatusPending,
			TotalCount: len(snapshotItems),
			Items:      snapshotItems,
		},
		items:            items,
		provider:         provider,
		algorithmCfg:     algorithmCfg,
		llmPrompt:        llmPrompt,
		promptText:       actualPromptText,
		displayName:      displayName,
		eventAlgorithmID: eventAlgorithmID,
		expiresAt:        time.Now().Add(camera2PatrolJobTTL),
	}

	s.camera2PatrolJobMu.Lock()
	s.purgeExpiredCamera2PatrolJobsLocked()
	s.camera2PatrolJobs[job.snapshot.JobID] = job
	s.camera2PatrolJobMu.Unlock()

	go s.runCamera2PatrolJob(job.snapshot.JobID)
	return job, nil
}

func (s *Server) buildCamera2PatrolConfig(
	algorithmID string,
	promptText string,
) (model.ModelProvider, ai.StartCameraAlgorithmConfig, string, string, string, string, error) {
	provider, err := s.getConfiguredLLMProvider()
	if err != nil {
		return model.ModelProvider{}, ai.StartCameraAlgorithmConfig{}, "", "", "", "", errBadRequest(err.Error())
	}

	if strings.TrimSpace(algorithmID) != "" {
		var algorithm model.Algorithm
		if err := s.db.Where("id = ?", strings.TrimSpace(algorithmID)).First(&algorithm).Error; err != nil {
			return model.ModelProvider{}, ai.StartCameraAlgorithmConfig{}, "", "", "", "", errBadRequest("算法不存在")
		}
		activePrompt, err := s.getActivePrompt(algorithm.ID)
		if err != nil || activePrompt == nil || strings.TrimSpace(activePrompt.Prompt) == "" {
			return model.ModelProvider{}, ai.StartCameraAlgorithmConfig{}, "", "", "", "", errBadRequest("所选算法缺少启用中的提示词")
		}
		taskCode := resolveAlgorithmTaskCode(algorithm)
		if taskCode == "" {
			return model.ModelProvider{}, ai.StartCameraAlgorithmConfig{}, "", "", "", "", errBadRequest("算法编码无效")
		}
		llmPrompt, err := s.composeAlgorithmTestPrompt(strings.TrimSpace(algorithm.Name), "图片测试", strings.TrimSpace(activePrompt.Prompt))
		if err != nil {
			return model.ModelProvider{}, ai.StartCameraAlgorithmConfig{}, "", "", "", "", err
		}
		return provider, ai.StartCameraAlgorithmConfig{
			AlgorithmID:       algorithm.ID,
			TaskCode:          taskCode,
			DetectMode:        model.AlgorithmDetectModeLLMOnly,
			Labels:            []string{},
			YoloThreshold:     0.5,
			IOUThreshold:      0.8,
			LabelsTriggerMode: model.LabelsTriggerModeAny,
		}, llmPrompt, strings.TrimSpace(algorithm.Name), strings.TrimSpace(activePrompt.Prompt), algorithm.ID, nil
	}

	syntheticAlgorithmID := "patrol-" + uuid.NewString()
	taskCode := "PATROL_" + strings.ToUpper(strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	llmPrompt, err := s.composeAlgorithmTestPrompt(camera2PatrolDisplayName, "图片测试", strings.TrimSpace(promptText))
	if err != nil {
		return model.ModelProvider{}, ai.StartCameraAlgorithmConfig{}, "", "", "", "", err
	}
	return provider, ai.StartCameraAlgorithmConfig{
		AlgorithmID:       syntheticAlgorithmID,
		TaskCode:          taskCode,
		DetectMode:        model.AlgorithmDetectModeLLMOnly,
		Labels:            []string{},
		YoloThreshold:     0.5,
		IOUThreshold:      0.8,
		LabelsTriggerMode: model.LabelsTriggerModeAny,
	}, llmPrompt, camera2PatrolDisplayName, strings.TrimSpace(promptText), "", nil
}

func (s *Server) buildCamera2PatrolItems(deviceIDs []string) ([]camera2PatrolJobItem, []camera2PatrolJobItemResult, error) {
	requestOrder := make([]string, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		deviceID = strings.TrimSpace(deviceID)
		if deviceID == "" {
			continue
		}
		requestOrder = append(requestOrder, deviceID)
	}
	if len(requestOrder) == 0 {
		return nil, nil, errBadRequest("请至少选择一个视频设备")
	}

	var sources []model.MediaSource
	if err := s.db.Where("id IN ?", requestOrder).Find(&sources).Error; err != nil {
		return nil, nil, err
	}
	sourceByID := make(map[string]model.MediaSource, len(sources))
	areaIDs := make([]string, 0, len(sources))
	areaSeen := make(map[string]struct{}, len(sources))
	for _, item := range sources {
		sourceByID[strings.TrimSpace(item.ID)] = item
		areaID := strings.TrimSpace(item.AreaID)
		if areaID == "" {
			continue
		}
		if _, exists := areaSeen[areaID]; exists {
			continue
		}
		areaSeen[areaID] = struct{}{}
		areaIDs = append(areaIDs, areaID)
	}

	areaNameByID := make(map[string]string, len(areaIDs))
	if len(areaIDs) > 0 {
		var areas []model.Area
		if err := s.db.Select("id", "name").Where("id IN ?", areaIDs).Find(&areas).Error; err != nil {
			return nil, nil, err
		}
		for _, item := range areas {
			areaNameByID[strings.TrimSpace(item.ID)] = strings.TrimSpace(item.Name)
		}
	}

	items := make([]camera2PatrolJobItem, 0, len(requestOrder))
	snapshotItems := make([]camera2PatrolJobItemResult, 0, len(requestOrder))
	for _, deviceID := range requestOrder {
		source, exists := sourceByID[deviceID]
		deviceName := deviceID
		areaID := ""
		areaName := "未分配区域"
		var sourcePtr *model.MediaSource
		if exists {
			deviceName = strings.TrimSpace(firstNonEmpty(source.Name, source.ID))
			areaID = strings.TrimSpace(source.AreaID)
			if resolvedAreaName := strings.TrimSpace(areaNameByID[areaID]); resolvedAreaName != "" {
				areaName = resolvedAreaName
			} else if areaID != "" {
				areaName = areaID
			}
			sourceCopy := source
			sourcePtr = &sourceCopy
		}
		items = append(items, camera2PatrolJobItem{
			DeviceID:   deviceID,
			DeviceName: deviceName,
			AreaID:     areaID,
			AreaName:   areaName,
			Source:     sourcePtr,
		})
		snapshotItems = append(snapshotItems, camera2PatrolJobItemResult{
			DeviceID:   deviceID,
			DeviceName: deviceName,
			Status:     model.AlgorithmTestJobItemStatusPending,
			Message:    "等待巡查",
		})
	}
	return items, snapshotItems, nil
}

func (s *Server) runCamera2PatrolJob(jobID string) {
	job, ok := s.getCamera2PatrolJobRef(jobID)
	if !ok {
		return
	}
	retryLimit := s.analyzeImageFailureRetryCount()
	job.mu.Lock()
	job.snapshot.Status = model.AlgorithmTestJobStatusRunning
	job.mu.Unlock()

	pendingRetryItems := make([]camera2PatrolJobItem, 0)
	for _, item := range job.items {
		job.mu.Lock()
		for idx := range job.snapshot.Items {
			if job.snapshot.Items[idx].DeviceID == item.DeviceID {
				job.snapshot.Items[idx].Status = model.AlgorithmTestJobItemStatusRunning
				job.snapshot.Items[idx].Message = "巡查中"
				break
			}
		}
		job.mu.Unlock()

		result := s.runCamera2PatrolItem(job, item)

		job.mu.Lock()
		if retryLimit > 0 && shouldRetryAnalyzeImageFailure(result.Status == model.AlgorithmTestJobItemStatusSuccess, string(algorithmTestMediaTypeImage), result.AIRequestType, result.LLMCallStatus, firstNonEmpty(result.ErrorMessage, result.Message)) {
			s.markCamera2PatrolJobItemRetryingLocked(job, item.DeviceID, result, 1, retryLimit)
			pendingRetryItems = append(pendingRetryItems, item)
		} else {
			s.persistCamera2PatrolJobItemResultLocked(job, item.DeviceID, result)
		}
		s.recomputeCamera2PatrolJobLocked(job)
		job.mu.Unlock()
	}

	for retryRound := 1; retryRound <= retryLimit && len(pendingRetryItems) > 0; retryRound++ {
		nextRetryItems := make([]camera2PatrolJobItem, 0)
		for _, item := range pendingRetryItems {
			result := s.runCamera2PatrolItem(job, item)
			job.mu.Lock()
			if retryRound < retryLimit && shouldRetryAnalyzeImageFailure(result.Status == model.AlgorithmTestJobItemStatusSuccess, string(algorithmTestMediaTypeImage), result.AIRequestType, result.LLMCallStatus, firstNonEmpty(result.ErrorMessage, result.Message)) {
				s.markCamera2PatrolJobItemRetryingLocked(job, item.DeviceID, result, retryRound+1, retryLimit)
				nextRetryItems = append(nextRetryItems, item)
			} else {
				s.persistCamera2PatrolJobItemResultLocked(job, item.DeviceID, result)
			}
			s.recomputeCamera2PatrolJobLocked(job)
			job.mu.Unlock()
		}
		pendingRetryItems = nextRetryItems
	}
}

func (s *Server) persistCamera2PatrolJobItemResultLocked(job *camera2PatrolJob, deviceID string, result camera2PatrolJobItemResult) {
	if job == nil {
		return
	}
	for idx := range job.snapshot.Items {
		if job.snapshot.Items[idx].DeviceID != deviceID {
			continue
		}
		job.snapshot.Items[idx] = result
		return
	}
}

func (s *Server) markCamera2PatrolJobItemRetryingLocked(job *camera2PatrolJob, deviceID string, result camera2PatrolJobItemResult, retryRound, maxRetryRounds int) {
	if job == nil {
		return
	}
	reason := strings.TrimSpace(firstNonEmpty(result.ErrorMessage, result.Message))
	for idx := range job.snapshot.Items {
		if job.snapshot.Items[idx].DeviceID != deviceID {
			continue
		}
		item := job.snapshot.Items[idx]
		item.Status = model.AlgorithmTestJobItemStatusRunning
		item.Message = buildAnalyzeImageRetryHint(reason, retryRound, maxRetryRounds)
		item.EventID = ""
		item.ErrorMessage = ""
		job.snapshot.Items[idx] = item
		return
	}
}

func (s *Server) runCamera2PatrolItem(job *camera2PatrolJob, item camera2PatrolJobItem) camera2PatrolJobItemResult {
	result := camera2PatrolJobItemResult{
		DeviceID:   item.DeviceID,
		DeviceName: item.DeviceName,
		Status:     model.AlgorithmTestJobItemStatusSuccess,
		Message:    "未发现异常",
	}
	if job == nil {
		result.Status = model.AlgorithmTestJobItemStatusFailed
		result.Message = "巡查任务不存在"
		return result
	}
	if item.Source == nil {
		result.Status = model.AlgorithmTestJobItemStatusFailed
		result.Message = "设备不存在"
		return result
	}

	snapshotBody, err := s.captureSnapshotBody(item.Source, "")
	if err != nil {
		result.Status = model.AlgorithmTestJobItemStatusFailed
		result.Message = err.Error()
		return result
	}
	mediaPath, err := s.saveAlgorithmTestImageBytes(camera2PatrolSnapshotOwnerID, snapshotBody)
	if err != nil {
		result.Status = model.AlgorithmTestJobItemStatusFailed
		result.Message = "保存巡查抓拍失败"
		return result
	}
	occurredAt := time.Now()
	imageWidth, imageHeight := readAlgorithmTestImageSize(s.algorithmTestMediaFullPath(mediaPath))
	req := ai.AnalyzeImageRequest{
		ImageRelPath:     mediaPath,
		AlgorithmConfigs: []ai.StartCameraAlgorithmConfig{job.algorithmCfg},
		LLMAPIURL:        strings.TrimSpace(job.provider.APIURL),
		LLMAPIKey:        strings.TrimSpace(job.provider.APIKey),
		LLMModel:         strings.TrimSpace(job.provider.Model),
		LLMPrompt:        strings.TrimSpace(job.llmPrompt),
	}
	if err := s.ensureLLMTokenQuotaAvailable(); err != nil {
		if isLLMTokenLimitExceededError(err) {
			result.Status = model.AlgorithmTestJobItemStatusFailed
			result.Message = llmTokenLimitExceededMessage
			return result
		}
		result.Status = model.AlgorithmTestJobItemStatusFailed
		result.Message = "检查 LLM token 限额失败: " + err.Error()
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), camera2PatrolAnalyzeTimeout)
	defer cancel()

	resp, err := s.aiClient.AnalyzeImage(ctx, req)
	result.AIRequestType = classifyAIRequestFailure(err)
	if resp != nil && resp.LLMUsage != nil {
		result.LLMCallStatus = strings.ToLower(strings.TrimSpace(resp.LLMUsage.CallStatus))
	}
	if resp != nil && resp.LLMUsage != nil {
		if _, usageErr := s.recordLLMUsage(s.db, llmUsagePersistRequest{
			Source:       model.LLMUsageSourceDirectAnalyze,
			DeviceID:     strings.TrimSpace(item.DeviceID),
			ProviderID:   strings.TrimSpace(job.provider.ID),
			ProviderName: strings.TrimSpace(job.provider.Name),
			Model:        strings.TrimSpace(job.provider.Model),
			DetectMode:   job.algorithmCfg.DetectMode,
			OccurredAt:   occurredAt,
			Usage:        resp.LLMUsage,
		}); usageErr != nil {
			log.Printf("record patrol llm usage failed: device_id=%s call_id=%s err=%v", item.DeviceID, resp.LLMUsage.CallID, usageErr)
		}
	}
	view := buildAlgorithmImageTestView(resp, err == nil && resp != nil && resp.Success, job.algorithmCfg, imageWidth, imageHeight)
	if err != nil {
		result.Status = model.AlgorithmTestJobItemStatusFailed
		result.Message = "AI 图片分析失败: " + err.Error()
		return result
	}
	if !view.Success {
		result.Status = model.AlgorithmTestJobItemStatusFailed
		result.Message = strings.TrimSpace(firstNonEmpty(view.ErrorMessage, view.Basis, view.Conclusion, "巡查分析失败"))
		return result
	}

	result.Message = strings.TrimSpace(firstNonEmpty(view.Conclusion, "未发现异常"))
	if !camera2PatrolAlarmTriggered(job.algorithmCfg, resp) {
		return result
	}

	snapshotPath, err := s.saveEventSnapshotBody(item.DeviceID, occurredAt.UnixMilli(), snapshotBody)
	if err != nil {
		result.Status = model.AlgorithmTestJobItemStatusFailed
		result.Message = "保存巡查截图失败"
		return result
	}

	eventID, err := s.createCamera2PatrolEvent(job, item, occurredAt, imageWidth, imageHeight, snapshotPath, resp, view)
	if err != nil {
		result.Status = model.AlgorithmTestJobItemStatusFailed
		result.Message = err.Error()
		return result
	}
	result.EventID = eventID
	return result
}

func (s *Server) createCamera2PatrolEvent(
	job *camera2PatrolJob,
	item camera2PatrolJobItem,
	occurredAt time.Time,
	imageWidth int,
	imageHeight int,
	snapshotPath string,
	resp *ai.AnalyzeImageResponse,
	view algorithmTestView,
) (string, error) {
	if job == nil {
		return "", errBadRequest("巡查任务不存在")
	}
	defaultAlarmLevelID, err := s.defaultAlarmLevelID()
	if err != nil {
		return "", err
	}

	llmJSON := "{}"
	if resp != nil && strings.TrimSpace(resp.LLMResult) != "" {
		llmJSON = strings.TrimSpace(resp.LLMResult)
	}
	yoloJSON := "[]"
	if resp != nil && len(resp.Detections) > 0 && !strings.EqualFold(strings.TrimSpace(string(resp.Detections)), "null") {
		yoloJSON = strings.TrimSpace(string(resp.Detections))
	}
	boxesJSONBytes, _ := json.Marshal(view.Boxes)
	sourceCallback := marshalJSONForLog(gin.H{
		"source":       "camera2_patrol",
		"job_id":       job.snapshot.JobID,
		"device_id":    item.DeviceID,
		"display_name": job.displayName,
	})
	notifiedAt := time.Now()
	event := model.AlarmEvent{
		ID:             uuid.NewString(),
		TaskID:         "",
		DeviceID:       item.DeviceID,
		AlgorithmID:    job.eventAlgorithmID,
		EventSource:    model.AlarmEventSourcePatrol,
		DisplayName:    job.displayName,
		PromptText:     job.promptText,
		AlarmLevelID:   defaultAlarmLevelID,
		Status:         model.EventStatusPending,
		OccurredAt:     occurredAt,
		SnapshotPath:   snapshotPath,
		SnapshotWidth:  imageWidth,
		SnapshotHeight: imageHeight,
		BoxesJSON:      string(boxesJSONBytes),
		YoloJSON:       yoloJSON,
		LLMJSON:        llmJSON,
		SourceCallback: sourceCallback,
		NotifiedAt:     &notifiedAt,
	}
	if err := s.db.Create(&event).Error; err != nil {
		return "", err
	}

	var level model.AlarmLevel
	if err := s.db.Select("id", "name", "color").Where("id = ?", defaultAlarmLevelID).First(&level).Error; err != nil {
		level = model.AlarmLevel{ID: defaultAlarmLevelID, Name: defaultAlarmLevelID}
	}
	s.wsHub.Broadcast(gin.H{
		"type":              "alarm",
		"event_id":          event.ID,
		"task_id":           "",
		"task_name":         "",
		"device_id":         event.DeviceID,
		"device_name":       item.DeviceName,
		"area_id":           item.AreaID,
		"area_name":         item.AreaName,
		"algorithm_id":      event.AlgorithmID,
		"algorithm_code":    "",
		"algorithm_name":    "",
		"display_name":      event.DisplayName,
		"alarm_level_id":    event.AlarmLevelID,
		"alarm_level_name":  strings.TrimSpace(firstNonEmpty(level.Name, level.ID)),
		"alarm_level_color": strings.TrimSpace(level.Color),
		"occurred_at":       event.OccurredAt.UnixMilli(),
		"notified_at":       event.NotifiedAt,
		"status":            event.Status,
		"source":            event.EventSource,
	})
	return event.ID, nil
}

func camera2PatrolAlarmTriggered(cfg ai.StartCameraAlgorithmConfig, resp *ai.AnalyzeImageResponse) bool {
	if resp == nil {
		return false
	}
	for _, item := range parseAlgorithmTestAlgorithmResults(resp) {
		if normalizeAlarmValue(item.Alarm) != "1" {
			continue
		}
		if strings.TrimSpace(cfg.AlgorithmID) != "" && strings.EqualFold(strings.TrimSpace(item.AlgorithmID), strings.TrimSpace(cfg.AlgorithmID)) {
			return true
		}
		if strings.TrimSpace(cfg.TaskCode) != "" && strings.EqualFold(strings.TrimSpace(item.TaskCode), strings.TrimSpace(cfg.TaskCode)) {
			return true
		}
	}

	llmPayload := parseAlgorithmTestImageLLMPayload(strings.TrimSpace(resp.LLMResult))
	if normalizeAlarmValue(llmPayload.Alarm) == "1" {
		return true
	}
	parsed := parseLLMResult(strings.TrimSpace(resp.LLMResult))
	for _, item := range parsed.TaskResults {
		if normalizeAlarmValue(item.Alarm) != "1" {
			continue
		}
		if strings.TrimSpace(cfg.TaskCode) == "" || strings.EqualFold(strings.TrimSpace(item.TaskCode), strings.TrimSpace(cfg.TaskCode)) {
			return true
		}
	}
	return false
}

func (s *Server) recomputeCamera2PatrolJobLocked(job *camera2PatrolJob) {
	if job == nil {
		return
	}
	successCount := 0
	failedCount := 0
	runningCount := 0
	pendingCount := 0
	alarmCount := 0
	for idx := range job.snapshot.Items {
		item := job.snapshot.Items[idx]
		if strings.TrimSpace(item.EventID) != "" {
			alarmCount++
		}
		switch strings.TrimSpace(item.Status) {
		case model.AlgorithmTestJobItemStatusPending:
			pendingCount++
		case model.AlgorithmTestJobItemStatusRunning:
			runningCount++
		case model.AlgorithmTestJobItemStatusFailed:
			failedCount++
		default:
			successCount++
		}
	}
	job.snapshot.SuccessCount = successCount
	job.snapshot.FailedCount = failedCount
	job.snapshot.AlarmCount = alarmCount
	switch {
	case pendingCount > 0:
		job.snapshot.Status = model.AlgorithmTestJobStatusPending
	case runningCount > 0:
		job.snapshot.Status = model.AlgorithmTestJobStatusRunning
	case failedCount > 0 && successCount > 0:
		job.snapshot.Status = model.AlgorithmTestJobStatusPartialFailed
	case failedCount > 0:
		job.snapshot.Status = model.AlgorithmTestJobStatusFailed
	default:
		job.snapshot.Status = model.AlgorithmTestJobStatusCompleted
	}
}

func (s *Server) getCamera2PatrolJobRef(jobID string) (*camera2PatrolJob, bool) {
	s.camera2PatrolJobMu.RLock()
	job, ok := s.camera2PatrolJobs[strings.TrimSpace(jobID)]
	s.camera2PatrolJobMu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(job.expiresAt) {
		s.camera2PatrolJobMu.Lock()
		delete(s.camera2PatrolJobs, strings.TrimSpace(jobID))
		s.camera2PatrolJobMu.Unlock()
		return nil, false
	}
	return job, true
}

func (s *Server) loadCamera2PatrolJob(jobID string) (camera2PatrolJobSnapshot, bool) {
	job, ok := s.getCamera2PatrolJobRef(jobID)
	if !ok {
		return camera2PatrolJobSnapshot{}, false
	}
	job.mu.RLock()
	defer job.mu.RUnlock()
	items := make([]camera2PatrolJobItemResult, len(job.snapshot.Items))
	copy(items, job.snapshot.Items)
	snapshot := job.snapshot
	snapshot.Items = items
	return snapshot, true
}

func (s *Server) purgeExpiredCamera2PatrolJobsLocked() {
	now := time.Now()
	for jobID, job := range s.camera2PatrolJobs {
		if job == nil || now.After(job.expiresAt) {
			delete(s.camera2PatrolJobs, jobID)
		}
	}
}

func trimmedStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if normalized := strings.TrimSpace(item); normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}
