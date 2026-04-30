package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"maas-box/internal/ai"
	"maas-box/internal/model"
)

type aiBoundingBox struct {
	XMin int `json:"x_min"`
	YMin int `json:"y_min"`
	XMax int `json:"x_max"`
	YMax int `json:"y_max"`
}

type aiDetection struct {
	Label      string        `json:"label"`
	Confidence float64       `json:"confidence"`
	Box        aiBoundingBox `json:"box"`
	Area       int           `json:"area,omitempty"`
}

type aiDetectionPayload struct {
	CameraID         string              `json:"camera_id"`
	Timestamp        int64               `json:"timestamp"`
	DetectMode       int                 `json:"detect_mode"`
	Detections       []aiDetection       `json:"detections"`
	AlgorithmResults []aiAlgorithmResult `json:"algorithm_results,omitempty"`
	LLMResult        string              `json:"llm_result,omitempty"`
	LLMUsage         *ai.LLMUsage        `json:"llm_usage,omitempty"`
	Snapshot         string              `json:"snapshot"`
	SnapshotWidth    int                 `json:"snapshot_width"`
	SnapshotHeight   int                 `json:"snapshot_height"`
}

type aiAlgorithmResult struct {
	AlgorithmID string        `json:"algorithm_id"`
	TaskCode    string        `json:"task_code"`
	Alarm       any           `json:"alarm"`
	Source      string        `json:"source"`
	Reason      string        `json:"reason"`
	Boxes       []aiDetection `json:"boxes"`
}

type aiStoppedPayload struct {
	CameraID  string `json:"camera_id"`
	Timestamp int64  `json:"timestamp"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
}

type aiStartedPayload struct {
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
}

type aiKeepalivePayload struct {
	Timestamp int64 `json:"timestamp"`
	Stats     struct {
		ActiveStreams   int `json:"active_streams"`
		TotalDetections int `json:"total_detections"`
		UptimeSeconds   int `json:"uptime_seconds"`
	} `json:"stats"`
	Message string `json:"message"`
}

type normalizedBox struct {
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	W          float64 `json:"w"`
	H          float64 `json:"h"`
}

type alarmEventOutput struct {
	model.AlarmEvent
	TaskName           string `json:"task_name" gorm:"column:task_name"`
	DeviceName         string `json:"device_name" gorm:"column:device_name"`
	AlgorithmName      string `json:"algorithm_name" gorm:"column:algorithm_name"`
	AlgorithmCode      string `json:"algorithm_code" gorm:"column:algorithm_code"`
	AlarmLevelName     string `json:"alarm_level_name" gorm:"column:alarm_level_name"`
	AlarmLevelColor    string `json:"alarm_level_color" gorm:"column:alarm_level_color"`
	AlarmLevelSeverity int    `json:"alarm_level_severity" gorm:"column:alarm_level_severity"`
	AreaID             string `json:"area_id" gorm:"column:area_id"`
	AreaName           string `json:"area_name" gorm:"column:area_name"`
}

type reviewEventRequest struct {
	Status     string `json:"status"`
	ReviewNote string `json:"review_note"`
}

func (s *Server) registerEventRoutes(r gin.IRouter) {
	g := r.Group("/events")
	g.GET("", s.listEvents)
	g.GET("/image/*path", s.getEventImage)
	g.GET("/:id/clips/file/*path", s.getEventClipFile)
	g.GET("/:id", s.getEvent)
	g.PUT("/:id/review", s.reviewEvent)
}

func (s *Server) handleAIDetectionEvent(c *gin.Context) {
	if !s.authorizeAI(c) {
		return
	}
	body, err := c.GetRawData()
	if err != nil {
		s.fail(c, http.StatusBadRequest, "read request body failed")
		return
	}
	var payload aiDetectionPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(payload.CameraID) == "" {
		s.fail(c, http.StatusBadRequest, "camera_id is required")
		return
	}

	task, _, algorithmConfigs, taskErr := s.findTaskByDevice(payload.CameraID)
	if taskErr != nil {
		s.ok(c, gin.H{"code": 0, "msg": "ignored: task not found"})
		return
	}
	deviceName := ""
	deviceAreaID := ""
	deviceAreaName := ""
	var callbackDevice model.Device
	if err := s.db.Select("name", "area_id").Where("id = ?", payload.CameraID).First(&callbackDevice).Error; err == nil {
		deviceName = strings.TrimSpace(callbackDevice.Name)
		deviceAreaID = strings.TrimSpace(callbackDevice.AreaID)
		if deviceAreaID != "" {
			var area model.Area
			if areaErr := s.db.Select("name").Where("id = ?", deviceAreaID).First(&area).Error; areaErr == nil {
				deviceAreaName = strings.TrimSpace(area.Name)
			}
		}
	}
	algorithmNameByID := make(map[string]string, len(algorithmConfigs))
	algorithmCodeByID := make(map[string]string, len(algorithmConfigs))
	algorithmIDByCode := make(map[string]string, len(algorithmConfigs))
	alarmLevelIDByAlgorithm := make(map[string]string, len(algorithmConfigs))
	alertCycleSecondsByAlgorithm := make(map[string]int, len(algorithmConfigs))
	for _, item := range algorithmConfigs {
		algorithmID := strings.TrimSpace(item.Algorithm.ID)
		algorithmCode := resolveAlgorithmTaskCode(item.Algorithm)
		algorithmNameByID[algorithmID] = strings.TrimSpace(item.Algorithm.Name)
		algorithmCodeByID[algorithmID] = algorithmCode
		defaultAlarmLevelID, _ := s.defaultAlarmLevelID()
		alarmLevelIDByAlgorithm[algorithmID] = firstNonEmpty(strings.TrimSpace(item.AlarmLevelID), defaultAlarmLevelID)
		if algorithmCode != "" {
			algorithmIDByCode[algorithmCode] = algorithmID
		}
		alertCycleSecondsByAlgorithm[algorithmID] = s.normalizeAlertCycleSecondsPersisted(item.AlertCycleSeconds)
	}
	alarmLevelInfoByID := make(map[string]model.AlarmLevel)
	alarmLevelIDs := make([]string, 0, len(alarmLevelIDByAlgorithm))
	alarmLevelSeen := make(map[string]struct{}, len(alarmLevelIDByAlgorithm))
	for _, levelID := range alarmLevelIDByAlgorithm {
		levelID = strings.TrimSpace(levelID)
		if levelID == "" {
			continue
		}
		if _, exists := alarmLevelSeen[levelID]; exists {
			continue
		}
		alarmLevelSeen[levelID] = struct{}{}
		alarmLevelIDs = append(alarmLevelIDs, levelID)
	}
	if len(alarmLevelIDs) > 0 {
		var levels []model.AlarmLevel
		if err := s.db.Select("id", "name", "color").Where("id IN ?", alarmLevelIDs).Find(&levels).Error; err == nil {
			for _, item := range levels {
				alarmLevelInfoByID[item.ID] = item
			}
		}
	}

	occurredAt := time.Now()
	if payload.Timestamp > 0 {
		occurredAt = time.UnixMilli(payload.Timestamp)
	}
	providerID := ""
	providerName := ""
	providerModel := ""
	if provider, ok := s.resolveTaskLLMProvider(algorithmConfigs); ok {
		providerID = strings.TrimSpace(provider.ID)
		providerName = strings.TrimSpace(provider.Name)
		providerModel = strings.TrimSpace(provider.Model)
	}
	if payload.LLMUsage != nil {
		if _, err := s.recordLLMUsage(s.db, llmUsagePersistRequest{
			Source:       model.LLMUsageSourceTaskRuntime,
			TaskID:       task.ID,
			DeviceID:     payload.CameraID,
			ProviderID:   providerID,
			ProviderName: providerName,
			Model:        providerModel,
			DetectMode:   payload.DetectMode,
			OccurredAt:   occurredAt,
			Usage:        payload.LLMUsage,
		}); err != nil {
			log.Printf(
				"record runtime llm usage failed: task_id=%s device_id=%s call_id=%s err=%v",
				task.ID,
				payload.CameraID,
				payload.LLMUsage.CallID,
				err,
			)
		}
	}
	llmParsed := parseLLMResult(payload.LLMResult)
	llmJSON := payload.LLMResult
	if strings.TrimSpace(llmJSON) == "" {
		llmJSON = "{}"
	}
	llmObjectsOnlyMode := payload.DetectMode == model.AlgorithmDetectModeLLMOnly || payload.DetectMode == model.AlgorithmDetectModeHybrid
	yoloJSONBytes, _ := json.Marshal(payload.Detections)
	yoloJSON := string(yoloJSONBytes)
	if llmObjectsOnlyMode {
		yoloJSON = "[]"
	}

	eventCandidates := make(map[string][]normalizedBox)
	llmBoxesByAlgorithm := make(map[string][]normalizedBox)
	smallBoxesByAlgorithm := make(map[string][]normalizedBox)
	alarmedAlgorithms := make(map[string]struct{})

	appendSmallBox := func(algorithmID string, box normalizedBox, markAlarm bool) {
		if strings.TrimSpace(algorithmID) == "" {
			return
		}
		smallBoxesByAlgorithm[algorithmID] = append(smallBoxesByAlgorithm[algorithmID], box)
		if markAlarm {
			alarmedAlgorithms[algorithmID] = struct{}{}
		}
	}
	appendLLMBox := func(algorithmID string, box normalizedBox, markAlarm bool) {
		if strings.TrimSpace(algorithmID) == "" {
			return
		}
		llmBoxesByAlgorithm[algorithmID] = append(llmBoxesByAlgorithm[algorithmID], box)
		if markAlarm {
			alarmedAlgorithms[algorithmID] = struct{}{}
		}
	}
	collectSmallBoxesFromDetections := func(onlyAlarmed bool, markAlarm bool) {
		for _, det := range payload.Detections {
			detLabel := strings.TrimSpace(det.Label)
			if detLabel == "" {
				continue
			}
			for _, algorithmConfig := range algorithmConfigs {
				alg := algorithmConfig.Algorithm
				if !alg.Enabled {
					continue
				}
				if alg.Mode != model.AlgorithmModeSmall && alg.Mode != model.AlgorithmModeHybrid {
					continue
				}
				if onlyAlarmed {
					if _, ok := alarmedAlgorithms[alg.ID]; !ok {
						continue
					}
				}
				matched := false
				for _, label := range normalizeSmallModelLabels([]string{alg.SmallModelLabel}) {
					if strings.EqualFold(label, detLabel) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
				appendSmallBox(alg.ID, normalizeBox(det, payload.SnapshotWidth, payload.SnapshotHeight), markAlarm)
			}
		}
	}
	logLLMResultConsistency(llmParsed, payload.CameraID)
	collectLLMBoxesFromParsed := func(onlyAlarmed bool, markAlarm bool) {
		type llmObjectHit struct {
			taskCode    string
			algorithmID string
			box         normalizedBox
		}
		validObjectCountsByTaskCode := make(map[string]int)
		objectHits := make([]llmObjectHit, 0, len(llmParsed.Objects))
		for _, object := range llmParsed.Objects {
			taskCode := normalizeAlgorithmCode(object.TaskCode)
			if taskCode == "" {
				continue
			}
			algID := strings.TrimSpace(algorithmIDByCode[taskCode])
			if algID == "" {
				log.Printf(
					"llm object ignored: camera_id=%s task_id=%s detect_mode=%d task_code=%s reason=algorithm_not_found",
					payload.CameraID,
					task.ID,
					payload.DetectMode,
					taskCode,
				)
				continue
			}
			box, ok := normalizedBoxFromLLMObject(object)
			if !ok {
				continue
			}
			validObjectCountsByTaskCode[taskCode]++
			objectHits = append(objectHits, llmObjectHit{
				taskCode:    taskCode,
				algorithmID: algID,
				box:         box,
			})
		}
		seenAlarmedTaskCodes := make(map[string]struct{}, len(llmParsed.TaskResults))
		for _, taskResult := range llmParsed.TaskResults {
			if normalizeAlarmValue(taskResult.Alarm) != "1" {
				continue
			}
			taskCode := normalizeAlgorithmCode(taskResult.TaskCode)
			if taskCode == "" {
				continue
			}
			if _, ok := seenAlarmedTaskCodes[taskCode]; ok {
				continue
			}
			seenAlarmedTaskCodes[taskCode] = struct{}{}
			if validObjectCountsByTaskCode[taskCode] > 0 {
				continue
			}
			log.Printf(
				"llm task_result alarm ignored without object: camera_id=%s task_id=%s detect_mode=%d task_code=%s",
				payload.CameraID,
				task.ID,
				payload.DetectMode,
				taskCode,
			)
		}
		for _, hit := range objectHits {
			if onlyAlarmed {
				if _, ok := alarmedAlgorithms[hit.algorithmID]; !ok {
					continue
				}
			}
			appendLLMBox(hit.algorithmID, hit.box, markAlarm)
		}
	}

	if llmObjectsOnlyMode {
		if payload.DetectMode == model.AlgorithmDetectModeHybrid {
			smallSourceCount := 0
			for _, item := range payload.AlgorithmResults {
				if strings.EqualFold(strings.TrimSpace(item.Source), "small") {
					smallSourceCount++
				}
			}
			if len(payload.Detections) > 0 || smallSourceCount > 0 {
				log.Printf(
					"hybrid callback ignored yolo inputs: camera_id=%s task_id=%s detect_mode=%d detections=%d small_algorithm_results=%d",
					payload.CameraID,
					task.ID,
					payload.DetectMode,
					len(payload.Detections),
					smallSourceCount,
				)
			}
		}
		collectLLMBoxesFromParsed(false, true)
	} else {
		useAlgorithmResults := len(payload.AlgorithmResults) > 0
		if useAlgorithmResults {
			for _, item := range payload.AlgorithmResults {
				if normalizeAlarmValue(item.Alarm) != "1" {
					continue
				}
				algorithmID := strings.TrimSpace(item.AlgorithmID)
				if algorithmID == "" {
					taskCode := normalizeAlgorithmCode(item.TaskCode)
					algorithmID = strings.TrimSpace(algorithmIDByCode[taskCode])
				}
				if algorithmID == "" {
					continue
				}
				alarmedAlgorithms[algorithmID] = struct{}{}
				isLLMSource := strings.EqualFold(strings.TrimSpace(item.Source), "llm")
				for _, box := range item.Boxes {
					normalized := normalizeBox(box, payload.SnapshotWidth, payload.SnapshotHeight)
					if isLLMSource {
						appendLLMBox(algorithmID, normalized, false)
						continue
					}
					appendSmallBox(algorithmID, normalized, false)
				}
			}
		}
		if !useAlgorithmResults || len(alarmedAlgorithms) == 0 {
			if useAlgorithmResults && len(alarmedAlgorithms) == 0 {
				log.Printf(
					"ai callback contains algorithm_results but no valid alarm hit; fallback to llm/yolo parse: camera_id=%s task_id=%s detect_mode=%d",
					payload.CameraID,
					task.ID,
					payload.DetectMode,
				)
			}
			collectSmallBoxesFromDetections(false, true)
			collectLLMBoxesFromParsed(false, true)
		} else {
			collectLLMBoxesFromParsed(true, false)
			collectSmallBoxesFromDetections(true, false)
		}
	}

	for algorithmID := range alarmedAlgorithms {
		llmBoxes := llmBoxesByAlgorithm[algorithmID]
		smallBoxes := smallBoxesByAlgorithm[algorithmID]
		switch {
		case len(llmBoxes) > 0:
			eventCandidates[algorithmID] = llmBoxes
			log.Printf(
				"event box source selected: task_id=%s device_id=%s algorithm_id=%s source=llm llm_boxes=%d small_boxes=%d",
				task.ID,
				payload.CameraID,
				algorithmID,
				len(llmBoxes),
				len(smallBoxes),
			)
		case len(smallBoxes) > 0:
			eventCandidates[algorithmID] = smallBoxes
			log.Printf(
				"event box source selected: task_id=%s device_id=%s algorithm_id=%s source=small llm_boxes=%d small_boxes=%d",
				task.ID,
				payload.CameraID,
				algorithmID,
				len(llmBoxes),
				len(smallBoxes),
			)
		default:
			eventCandidates[algorithmID] = []normalizedBox{}
			log.Printf(
				"event box source selected: task_id=%s device_id=%s algorithm_id=%s source=none llm_boxes=0 small_boxes=0",
				task.ID,
				payload.CameraID,
				algorithmID,
			)
		}
	}

	if len(eventCandidates) == 0 {
		s.ok(c, gin.H{"created_event_ids": []string{}})
		return
	}

	snapshotPath := ""
	if payload.Snapshot != "" {
		if p, err := s.saveEventSnapshot(payload.CameraID, payload.Timestamp, payload.Snapshot); err == nil {
			snapshotPath = p
		}
	}

	rawSource := sanitizeEventSourceCallback(body)
	createdIDs := make([]string, 0, len(eventCandidates))
	for algorithmID, boxes := range eventCandidates {
		defaultAlarmLevelID, _ := s.defaultAlarmLevelID()
		alarmLevelID := firstNonEmpty(strings.TrimSpace(alarmLevelIDByAlgorithm[algorithmID]), defaultAlarmLevelID)
		alertCycleSeconds := s.normalizeAlertCycleSecondsPersisted(alertCycleSecondsByAlgorithm[algorithmID])
		now := time.Now()
		shouldBroadcast, lastNotifiedAt, broadcastErr := s.shouldBroadcastAlarm(task.ID, payload.CameraID, algorithmID, alertCycleSeconds, now)
		if broadcastErr != nil {
			log.Printf(
				"alarm suppression check failed, fallback broadcast: task_id=%s device_id=%s algorithm_id=%s alert_cycle_seconds=%d err=%v",
				task.ID,
				payload.CameraID,
				algorithmID,
				alertCycleSeconds,
				broadcastErr,
			)
			shouldBroadcast = true
		}
		boxesJSONBytes, _ := json.Marshal(boxes)
		event := model.AlarmEvent{
			ID:             uuid.NewString(),
			TaskID:         task.ID,
			DeviceID:       payload.CameraID,
			AlgorithmID:    algorithmID,
			EventSource:    model.AlarmEventSourceRuntime,
			AlarmLevelID:   alarmLevelID,
			Status:         model.EventStatusPending,
			OccurredAt:     occurredAt,
			SnapshotPath:   snapshotPath,
			SnapshotWidth:  payload.SnapshotWidth,
			SnapshotHeight: payload.SnapshotHeight,
			BoxesJSON:      string(boxesJSONBytes),
			YoloJSON:       yoloJSON,
			LLMJSON:        llmJSON,
			SourceCallback: rawSource,
		}
		if shouldBroadcast {
			notifiedAt := now
			event.NotifiedAt = &notifiedAt
		}
		if err := s.db.Create(&event).Error; err != nil {
			continue
		}
		createdIDs = append(createdIDs, event.ID)
		if shouldBroadcast {
			algorithmName := algorithmNameByID[algorithmID]
			algorithmCode := algorithmCodeByID[algorithmID]
			displayName := strings.TrimSpace(firstNonEmpty(event.DisplayName, algorithmName, algorithmCode, algorithmID))
			alarmLevelInfo := alarmLevelInfoByID[alarmLevelID]
			alarmLevelName := strings.TrimSpace(alarmLevelInfo.Name)
			if alarmLevelName == "" {
				alarmLevelName = alarmLevelID
			}
			s.wsHub.Broadcast(gin.H{
				"type":              "alarm",
				"event_id":          event.ID,
				"task_id":           event.TaskID,
				"task_name":         strings.TrimSpace(task.Name),
				"device_id":         event.DeviceID,
				"device_name":       deviceName,
				"area_id":           deviceAreaID,
				"area_name":         deviceAreaName,
				"algorithm_id":      event.AlgorithmID,
				"algorithm_code":    algorithmCode,
				"algorithm_name":    algorithmName,
				"display_name":      displayName,
				"alarm_level_id":    alarmLevelID,
				"alarm_level_name":  alarmLevelName,
				"alarm_level_color": strings.TrimSpace(alarmLevelInfo.Color),
				"occurred_at":       event.OccurredAt.UnixMilli(),
				"notified_at":       event.NotifiedAt,
				"status":            event.Status,
				"source":            event.EventSource,
			})
			continue
		}
		lastNotifiedText := ""
		if lastNotifiedAt != nil {
			lastNotifiedText = lastNotifiedAt.Format(time.RFC3339Nano)
		}
		log.Printf(
			"alarm notification suppressed: task_id=%s device_id=%s algorithm_id=%s event_id=%s alert_cycle_seconds=%d last_notified_at=%s",
			task.ID,
			payload.CameraID,
			algorithmID,
			event.ID,
			alertCycleSeconds,
			lastNotifiedText,
		)
	}
	if len(createdIDs) > 0 {
		s.triggerAlarmClipBySourceID(payload.CameraID, occurredAt, createdIDs)
	}
	s.ok(c, gin.H{"created_event_ids": createdIDs})
}

func (s *Server) handleAIStopped(c *gin.Context) {
	if !s.authorizeAI(c) {
		return
	}
	var payload aiStoppedPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if payload.CameraID != "" {
		_ = s.db.Model(&model.Device{}).Where("id = ?", payload.CameraID).
			Update("ai_status", model.DeviceAIStatusStopped).Error
		_ = s.applyRecordingPolicyForSourceID(payload.CameraID)
		s.updateTaskStatusByDevice(payload.CameraID, "")
	}
	s.ok(c, gin.H{"received": true})
}

func (s *Server) handleAIStarted(c *gin.Context) {
	if !s.authorizeAI(c) {
		return
	}
	var payload aiStartedPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	s.ok(c, gin.H{"received": true, "timestamp": payload.Timestamp})
	s.schedulePendingStartupRecovery("ai_started_resume")
}

func (s *Server) handleAIKeepalive(c *gin.Context) {
	if !s.authorizeAI(c) {
		return
	}
	var payload aiKeepalivePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	s.ok(c, gin.H{"received": true, "stats": payload.Stats})
	s.schedulePendingStartupRecovery("ai_keepalive_resume")
}

func (s *Server) listEvents(c *gin.Context) {
	status := strings.TrimSpace(c.Query("status"))
	source := normalizeAlarmEventSource(c.Query("source"))
	taskID := strings.TrimSpace(c.Query("task_id"))
	deviceID := strings.TrimSpace(c.Query("device_id"))
	areaID := strings.TrimSpace(c.Query("area_id"))
	algorithmID := strings.TrimSpace(c.Query("algorithm_id"))
	alarmLevelID := strings.TrimSpace(c.Query("alarm_level_id"))
	startAtRaw := strings.TrimSpace(c.Query("start_at"))
	endAtRaw := strings.TrimSpace(c.Query("end_at"))
	taskName := strings.TrimSpace(c.Query("task_name"))
	deviceName := strings.TrimSpace(c.Query("device_name"))
	algorithmName := strings.TrimSpace(c.Query("algorithm_name"))
	startAt, err := parseEventQueryTime(startAtRaw)
	if err != nil {
		s.fail(c, http.StatusBadRequest, "start_at 鏍煎紡鏃犳晥")
		return
	}
	endAt, err := parseEventQueryTime(endAtRaw)
	if err != nil {
		s.fail(c, http.StatusBadRequest, "end_at 鏍煎紡鏃犳晥")
		return
	}
	if !startAt.IsZero() && !endAt.IsZero() && startAt.After(endAt) {
		s.fail(c, http.StatusBadRequest, "start_at 涓嶈兘鏅氫簬 end_at")
		return
	}
	page := parsePositiveInt(c.Query("page"), 1)
	pageSize := parsePositiveInt(c.Query("page_size"), 1000)
	if pageSize > 2000 {
		pageSize = 2000
	}
	offset := (page - 1) * pageSize

	queryStart := time.Now()
	baseQuery := s.db.Model(&model.AlarmEvent{}).
		Joins("LEFT JOIN mb_video_tasks t ON t.id = mb_alarm_events.task_id").
		Joins("LEFT JOIN mb_media_sources d ON d.id = mb_alarm_events.device_id").
		Joins("LEFT JOIN mb_algorithms a ON a.id = mb_alarm_events.algorithm_id").
		Joins("LEFT JOIN mb_alarm_levels l ON l.id = mb_alarm_events.alarm_level_id").
		Joins("LEFT JOIN mb_areas ar ON ar.id = d.area_id")
	baseQuery = applyAlarmEventSourceFilter(baseQuery, "mb_alarm_events.event_source", source)
	if status != "" {
		baseQuery = baseQuery.Where("mb_alarm_events.status = ?", status)
	}
	if taskID != "" {
		baseQuery = baseQuery.Where("mb_alarm_events.task_id = ?", taskID)
	}
	if deviceID != "" {
		baseQuery = baseQuery.Where("mb_alarm_events.device_id = ?", deviceID)
	}
	if areaID != "" {
		baseQuery = baseQuery.Where("d.area_id = ?", areaID)
	}
	if algorithmID != "" {
		baseQuery = baseQuery.Where("mb_alarm_events.algorithm_id = ?", algorithmID)
	}
	if alarmLevelID != "" {
		baseQuery = baseQuery.Where("mb_alarm_events.alarm_level_id = ?", alarmLevelID)
	}
	if taskName != "" {
		baseQuery = baseQuery.Where("t.name LIKE ?", "%"+taskName+"%")
	}
	if deviceName != "" {
		baseQuery = baseQuery.Where("d.name LIKE ?", "%"+deviceName+"%")
	}
	if algorithmName != "" {
		baseQuery = baseQuery.Where("a.name LIKE ?", "%"+algorithmName+"%")
	}
	if !startAt.IsZero() {
		baseQuery = baseQuery.Where("mb_alarm_events.occurred_at >= ?", startAt)
	}
	if !endAt.IsZero() {
		baseQuery = baseQuery.Where("mb_alarm_events.occurred_at <= ?", endAt)
	}

	var total int64
	if err := baseQuery.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "count events failed")
		return
	}

	var items []alarmEventOutput
	if err := baseQuery.
		Select(
			"mb_alarm_events.*, t.name AS task_name, d.name AS device_name, a.name AS algorithm_name, a.code AS algorithm_code, " +
				"l.name AS alarm_level_name, l.color AS alarm_level_color, l.severity AS alarm_level_severity, " +
				"d.area_id AS area_id, ar.name AS area_name",
		).
		Order("mb_alarm_events.occurred_at desc").
		Offset(offset).
		Limit(pageSize).
		Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query events failed")
		return
	}

	queryMS := time.Since(queryStart).Milliseconds()
	resp := gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}
	responseBytes := -1
	if body, err := json.Marshal(resp); err == nil {
		responseBytes = len(body)
	}
	log.Printf(
		"events list metrics: rows=%d total=%d page=%d page_size=%d query_ms=%d response_bytes=%d filters=%s",
		len(items),
		total,
		page,
		pageSize,
		queryMS,
		responseBytes,
		marshalJSONForLog(gin.H{
			"status":         status,
			"source":         source,
			"task_id":        taskID,
			"device_id":      deviceID,
			"area_id":        areaID,
			"algorithm_id":   algorithmID,
			"alarm_level_id": alarmLevelID,
			"start_at":       startAtRaw,
			"end_at":         endAtRaw,
			"task_name":      taskName,
			"device_name":    deviceName,
			"algorithm_name": algorithmName,
		}),
	)
	s.ok(c, resp)
}

func parseEventQueryTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
		// Support unix milliseconds (preferred) and unix seconds.
		if len(raw) >= 13 {
			return time.UnixMilli(ms).In(time.Local), nil
		}
		return time.Unix(ms, 0).In(time.Local), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		// Normalize to local zone so DB comparisons use one timezone semantics.
		return t.In(time.Local), nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, errors.New("invalid time format")
}

func (s *Server) getEvent(c *gin.Context) {
	id := c.Param("id")
	var item alarmEventOutput
	if err := s.db.Model(&model.AlarmEvent{}).
		Select(
			"mb_alarm_events.*, t.name AS task_name, d.name AS device_name, a.name AS algorithm_name, a.code AS algorithm_code, "+
				"l.name AS alarm_level_name, l.color AS alarm_level_color, l.severity AS alarm_level_severity, "+
				"d.area_id AS area_id, ar.name AS area_name",
		).
		Joins("LEFT JOIN mb_video_tasks t ON t.id = mb_alarm_events.task_id").
		Joins("LEFT JOIN mb_media_sources d ON d.id = mb_alarm_events.device_id").
		Joins("LEFT JOIN mb_algorithms a ON a.id = mb_alarm_events.algorithm_id").
		Joins("LEFT JOIN mb_alarm_levels l ON l.id = mb_alarm_events.alarm_level_id").
		Joins("LEFT JOIN mb_areas ar ON ar.id = d.area_id").
		Where("mb_alarm_events.id = ?", id).
		First(&item).Error; err != nil {
		s.fail(c, http.StatusNotFound, "event not found")
		return
	}
	s.ok(c, item)
}

func (s *Server) reviewEvent(c *gin.Context) {
	id := c.Param("id")
	var item model.AlarmEvent
	if err := s.db.Where("id = ?", id).First(&item).Error; err != nil {
		s.fail(c, http.StatusNotFound, "event not found")
		return
	}
	var in reviewEventRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	status := strings.TrimSpace(in.Status)
	if status != model.EventStatusValid && status != model.EventStatusInvalid && status != model.EventStatusPending {
		s.fail(c, http.StatusBadRequest, "status must be pending/valid/invalid")
		return
	}
	claims := s.mustClaims(c)
	item.Status = status
	item.ReviewNote = strings.TrimSpace(in.ReviewNote)
	item.ReviewedBy = claims.Username
	item.ReviewedAt = time.Now()
	if err := s.db.Save(&item).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "review event failed")
		return
	}
	s.ok(c, item)
}

func (s *Server) getEventImage(c *gin.Context) {
	rawPath := strings.TrimPrefix(c.Param("path"), "/")
	rawPath = filepath.Clean(rawPath)
	fullPath := filepath.Join("configs", "events", rawPath)
	absEventsDir, _ := filepath.Abs(filepath.Join("configs", "events"))
	absTarget, _ := filepath.Abs(fullPath)
	if !strings.HasPrefix(absTarget, absEventsDir) {
		s.fail(c, http.StatusBadRequest, "invalid image path")
		return
	}
	body, err := os.ReadFile(fullPath)
	if err != nil {
		s.fail(c, http.StatusNotFound, "image not found")
		return
	}
	c.Data(http.StatusOK, "image/jpeg", body)
}

func (s *Server) getEventClipFile(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		s.fail(c, http.StatusBadRequest, "invalid event id")
		return
	}
	rawPath := strings.TrimPrefix(c.Param("path"), "/")
	rawPath = filepath.ToSlash(filepath.Clean(rawPath))
	rawPath = strings.TrimPrefix(rawPath, "./")
	if rawPath == "" || rawPath == "." {
		s.fail(c, http.StatusBadRequest, "invalid clip path")
		return
	}

	var event model.AlarmEvent
	if err := s.db.Select("id", "device_id", "clip_files_json").Where("id = ?", id).First(&event).Error; err != nil {
		s.fail(c, http.StatusNotFound, "event not found")
		return
	}
	deviceID := strings.TrimSpace(event.DeviceID)
	if deviceID == "" {
		s.fail(c, http.StatusNotFound, "event clip not found")
		return
	}
	var files []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(event.ClipFilesJSON)), &files); err != nil || len(files) == 0 {
		s.fail(c, http.StatusBadRequest, "event has no clip files")
		return
	}
	matched := false
	for _, item := range files {
		if filepath.ToSlash(strings.TrimSpace(item)) == rawPath {
			matched = true
			break
		}
	}
	if !matched {
		s.fail(c, http.StatusBadRequest, "clip path not found in event")
		return
	}
	fullPath, err := s.safeRecordingFilePath(deviceID, rawPath)
	if err != nil {
		s.fail(c, http.StatusBadRequest, "invalid clip path")
		return
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			s.fail(c, http.StatusNotFound, "clip file not found")
			return
		}
		s.fail(c, http.StatusInternalServerError, "read clip file failed")
		return
	}
	if info.IsDir() {
		s.fail(c, http.StatusBadRequest, "clip path is a directory")
		return
	}
	c.Header("Content-Disposition", "inline")
	c.File(fullPath)
}

func (s *Server) authorizeAI(c *gin.Context) bool {
	expected := strings.TrimSpace(s.cfg.Server.AI.CallbackToken)
	if expected == "" {
		return true
	}
	given := strings.TrimSpace(c.GetHeader("Authorization"))
	if given != expected {
		s.fail(c, http.StatusUnauthorized, "invalid ai callback token")
		return false
	}
	return true
}

func (s *Server) findTaskByDevice(deviceID string) (model.VideoTask, model.VideoTaskDeviceProfile, []taskAlgorithmRuntime, error) {
	var profile model.VideoTaskDeviceProfile
	if err := s.db.Where("device_id = ?", deviceID).First(&profile).Error; err != nil {
		return model.VideoTask{}, model.VideoTaskDeviceProfile{}, nil, err
	}
	if strings.TrimSpace(profile.AlarmLevelID) == "" {
		defaultAlarmLevelID, levelErr := s.defaultAlarmLevelID()
		if levelErr != nil {
			return model.VideoTask{}, model.VideoTaskDeviceProfile{}, nil, levelErr
		}
		profile.AlarmLevelID = defaultAlarmLevelID
	}
	var task model.VideoTask
	if err := s.db.Where("id = ?", profile.TaskID).First(&task).Error; err != nil {
		return model.VideoTask{}, model.VideoTaskDeviceProfile{}, nil, err
	}
	var relAlgorithms []model.VideoTaskDeviceAlgorithm
	if err := s.db.Where("task_id = ? AND device_id = ?", profile.TaskID, deviceID).Find(&relAlgorithms).Error; err != nil {
		return model.VideoTask{}, model.VideoTaskDeviceProfile{}, nil, err
	}
	algorithmConfigByID := make(map[string]model.VideoTaskDeviceAlgorithm, len(relAlgorithms))
	for _, item := range relAlgorithms {
		algorithmID := strings.TrimSpace(item.AlgorithmID)
		if algorithmID == "" {
			continue
		}
		alarmLevelID := strings.TrimSpace(item.AlarmLevelID)
		if alarmLevelID == "" || !isBuiltinAlarmLevelID(alarmLevelID) {
			alarmLevelID, _ = s.defaultAlarmLevelID()
		}
		algorithmConfigByID[algorithmID] = model.VideoTaskDeviceAlgorithm{
			TaskID:            item.TaskID,
			DeviceID:          item.DeviceID,
			AlgorithmID:       algorithmID,
			AlarmLevelID:      alarmLevelID,
			AlertCycleSeconds: s.normalizeAlertCycleSecondsPersisted(item.AlertCycleSeconds),
		}
	}
	if len(algorithmConfigByID) == 0 {
		var legacyRel []model.VideoTaskAlgorithm
		if err := s.db.Where("task_id = ?", profile.TaskID).Find(&legacyRel).Error; err != nil {
			return model.VideoTask{}, model.VideoTaskDeviceProfile{}, nil, err
		}
		for _, item := range legacyRel {
			algorithmID := strings.TrimSpace(item.AlgorithmID)
			if algorithmID == "" {
				continue
			}
			defaultAlarmLevelID, _ := s.defaultAlarmLevelID()
			algorithmConfigByID[algorithmID] = model.VideoTaskDeviceAlgorithm{
				TaskID:            profile.TaskID,
				DeviceID:          deviceID,
				AlgorithmID:       algorithmID,
				AlarmLevelID:      defaultAlarmLevelID,
				AlertCycleSeconds: s.taskAlertCycleDefault(),
			}
		}
	}
	algorithmIDs := make([]string, 0, len(algorithmConfigByID))
	for algorithmID := range algorithmConfigByID {
		algorithmIDs = append(algorithmIDs, algorithmID)
	}
	algorithmIDs = uniqueStrings(algorithmIDs)
	algorithms := make([]model.Algorithm, 0, len(algorithmIDs))
	if len(algorithmIDs) > 0 {
		if err := s.db.Where("id IN ?", algorithmIDs).Find(&algorithms).Error; err != nil {
			return model.VideoTask{}, model.VideoTaskDeviceProfile{}, nil, err
		}
	}
	runtimeAlgorithms := make([]taskAlgorithmRuntime, 0, len(algorithms))
	for _, algorithm := range algorithms {
		algorithmConfig, exists := algorithmConfigByID[algorithm.ID]
		if !exists {
			continue
		}
		runtimeAlgorithms = append(runtimeAlgorithms, taskAlgorithmRuntime{
			Algorithm:         algorithm,
			AlarmLevelID:      firstNonEmpty(strings.TrimSpace(algorithmConfig.AlarmLevelID), profile.AlarmLevelID),
			AlertCycleSeconds: s.normalizeAlertCycleSecondsPersisted(algorithmConfig.AlertCycleSeconds),
		})
	}
	sort.Slice(runtimeAlgorithms, func(i, j int) bool {
		return strings.TrimSpace(runtimeAlgorithms[i].Algorithm.Name) < strings.TrimSpace(runtimeAlgorithms[j].Algorithm.Name)
	})
	return task, profile, runtimeAlgorithms, nil
}

func (s *Server) shouldBroadcastAlarm(taskID, deviceID, algorithmID string, alertCycleSeconds int, now time.Time) (bool, *time.Time, error) {
	alertCycleSeconds = s.normalizeAlertCycleSecondsPersisted(alertCycleSeconds)
	if alertCycleSeconds <= 0 {
		return true, nil, nil
	}
	taskID = strings.TrimSpace(taskID)
	deviceID = strings.TrimSpace(deviceID)
	algorithmID = strings.TrimSpace(algorithmID)
	if taskID == "" || deviceID == "" || algorithmID == "" {
		return true, nil, nil
	}
	var latest struct {
		NotifiedAt *time.Time `gorm:"column:notified_at"`
	}
	query := s.db.Model(&model.AlarmEvent{})
	query = applyAlarmEventSourceFilter(query, "mb_alarm_events.event_source", model.AlarmEventSourceRuntime)
	err := query.
		Select("notified_at").
		Where("task_id = ? AND device_id = ? AND algorithm_id = ? AND notified_at IS NOT NULL", taskID, deviceID, algorithmID).
		Order("notified_at desc").
		Limit(1).
		Take(&latest).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true, nil, nil
		}
		return true, nil, err
	}
	if latest.NotifiedAt == nil {
		return true, nil, nil
	}
	window := time.Duration(alertCycleSeconds) * time.Second
	if now.Sub(*latest.NotifiedAt) < window {
		return false, latest.NotifiedAt, nil
	}
	return true, latest.NotifiedAt, nil
}

func normalizeBox(det aiDetection, width, height int) normalizedBox {
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		height = 1
	}
	w := float64(det.Box.XMax-det.Box.XMin) / float64(width)
	h := float64(det.Box.YMax-det.Box.YMin) / float64(height)
	x := float64(det.Box.XMin+det.Box.XMax) / 2 / float64(width)
	y := float64(det.Box.YMin+det.Box.YMax) / 2 / float64(height)
	return normalizedBox{
		Label:      det.Label,
		Confidence: det.Confidence,
		X:          x,
		Y:          y,
		W:          w,
		H:          h,
	}
}

func normalizedBoxFromLLMObject(object llmPromptObject) (normalizedBox, bool) {
	if len(object.BBox2D) < 4 {
		return normalizedBox{}, false
	}
	x0 := clampBBox2DValue(object.BBox2D[0])
	y0 := clampBBox2DValue(object.BBox2D[1])
	x1 := clampBBox2DValue(object.BBox2D[2])
	y1 := clampBBox2DValue(object.BBox2D[3])
	if x1 <= x0 || y1 <= y0 {
		return normalizedBox{}, false
	}
	w := (x1 - x0) / 1000.0
	h := (y1 - y0) / 1000.0
	x := (x0 + x1) / 2000.0
	y := (y0 + y1) / 2000.0
	confidence := object.Confidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	return normalizedBox{
		Label:      strings.TrimSpace(object.Label),
		Confidence: confidence,
		X:          x,
		Y:          y,
		W:          w,
		H:          h,
	}, true
}

func clampBBox2DValue(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1000 {
		return 1000
	}
	return v
}

func logLLMResultConsistency(parsed llmPromptResult, cameraID string) {
	triggered := make([]string, 0, len(parsed.TaskResults))
	for _, item := range parsed.TaskResults {
		if normalizeAlarmValue(item.Alarm) != "1" {
			continue
		}
		code := normalizeAlgorithmCode(item.TaskCode)
		if code == "" {
			continue
		}
		triggered = append(triggered, code)
	}
	triggered = uniqueStrings(triggered)
	overallCodes := make([]string, 0, len(parsed.Overall.AlarmTaskCodes))
	for _, item := range parsed.Overall.AlarmTaskCodes {
		code := normalizeAlgorithmCode(item)
		if code == "" {
			continue
		}
		overallCodes = append(overallCodes, code)
	}
	overallCodes = uniqueStrings(overallCodes)
	triggeredSet := make(map[string]struct{}, len(triggered))
	for _, code := range triggered {
		triggeredSet[code] = struct{}{}
	}
	overallSet := make(map[string]struct{}, len(overallCodes))
	for _, code := range overallCodes {
		overallSet[code] = struct{}{}
	}
	mismatch := false
	if normalizeAlarmFlag(parsed.Overall.Alarm) == "0" && len(triggered) > 0 {
		mismatch = true
	}
	if normalizeAlarmFlag(parsed.Overall.Alarm) == "1" && len(triggered) == 0 {
		mismatch = true
	}
	if len(triggeredSet) != len(overallSet) {
		mismatch = true
	} else {
		for code := range triggeredSet {
			if _, ok := overallSet[code]; !ok {
				mismatch = true
				break
			}
		}
	}
	if mismatch {
		log.Printf(
			"llm result consistency mismatch: camera_id=%s overall_alarm=%s overall_codes=%v task_result_codes=%v",
			cameraID,
			normalizeAlarmFlag(parsed.Overall.Alarm),
			overallCodes,
			triggered,
		)
	}
}

var safeNameRegex = regexp.MustCompile(`[^a-zA-Z0-9_\-]+`)

func sanitizeEventSourceCallback(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}"
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return trimmed
	}
	if obj, ok := payload.(map[string]any); ok {
		delete(obj, "snapshot")
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return trimmed
	}
	return string(out)
}

func (s *Server) saveEventSnapshot(cameraID string, timestamp int64, snapshotBase64 string) (string, error) {
	body, err := base64.StdEncoding.DecodeString(snapshotBase64)
	if err != nil {
		return "", err
	}
	return s.saveEventSnapshotBody(cameraID, timestamp, body)
}

func (s *Server) saveEventSnapshotBody(cameraID string, timestamp int64, body []byte) (string, error) {
	t := time.Now()
	if timestamp > 0 {
		t = time.UnixMilli(timestamp)
	}
	dateDir := t.Format("20060102")
	dir := filepath.Join("configs", "events", dateDir)
	if err := s.ensureDir(dir); err != nil {
		return "", err
	}
	camera := safeNameRegex.ReplaceAllString(cameraID, "_")
	filename := camera + "_" + t.Format("150405.000") + ".jpg"
	full := filepath.Join(dir, filename)
	if err := os.WriteFile(full, body, 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(dateDir, filename)), nil
}

func (s *Server) updateTaskStatusByDevice(deviceID, taskStatus string) {
	var rel model.VideoTaskDeviceProfile
	if err := s.db.Where("device_id = ?", deviceID).First(&rel).Error; err != nil {
		return
	}
	if strings.TrimSpace(taskStatus) != "" {
		_ = s.db.Model(&model.VideoTask{}).Where("id = ?", rel.TaskID).Update("status", taskStatus).Error
		return
	}

	var relDevices []model.VideoTaskDeviceProfile
	if err := s.db.Where("task_id = ?", rel.TaskID).Find(&relDevices).Error; err != nil {
		return
	}
	if len(relDevices) == 0 {
		_ = s.db.Model(&model.VideoTask{}).Where("id = ?", rel.TaskID).
			Updates(map[string]any{"status": model.TaskStatusStopped, "last_stop_at": time.Now()}).Error
		return
	}

	deviceIDs := make([]string, 0, len(relDevices))
	for _, item := range relDevices {
		deviceIDs = append(deviceIDs, item.DeviceID)
	}
	var devices []model.Device
	if err := s.db.Where("id IN ?", deviceIDs).Find(&devices).Error; err != nil {
		return
	}

	runningCount := 0
	for _, device := range devices {
		if device.AIStatus == model.DeviceAIStatusRunning {
			runningCount++
		}
	}

	nextStatus := model.TaskStatusStopped
	if runningCount == len(deviceIDs) && len(deviceIDs) > 0 {
		nextStatus = model.TaskStatusRunning
	} else if runningCount > 0 {
		nextStatus = model.TaskStatusPartialFail
	}

	updates := map[string]any{"status": nextStatus}
	if nextStatus == model.TaskStatusStopped {
		updates["last_stop_at"] = time.Now()
	}
	_ = s.db.Model(&model.VideoTask{}).Where("id = ?", rel.TaskID).Updates(updates).Error
}

func (s *Server) withTransaction(fn func(tx *gorm.DB) error) error {
	return s.db.Transaction(fn)
}
