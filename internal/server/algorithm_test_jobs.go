package server

import (
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"maas-box/internal/ai"
	"maas-box/internal/logutil"
	"maas-box/internal/model"
)

const (
	// 正式算法测试单个 job 的总并发上限，图片与视频共用该槽位池。
	algorithmTestJobTotalConcurrency = 5
	// 视频测试额外单独限流，避免同一批次并发抽帧/上传压垮 AI 服务。
	algorithmTestJobVideoConcurrency = 1
)

type algorithmTestJobSnapshot struct {
	JobID        string                    `json:"job_id"`
	BatchID      string                    `json:"batch_id"`
	AlgorithmID  string                    `json:"algorithm_id"`
	Status       string                    `json:"status"`
	TotalCount   int                       `json:"total_count"`
	SuccessCount int                       `json:"success_count"`
	FailedCount  int                       `json:"failed_count"`
	Items        []algorithmTestItemResult `json:"items"`
}

type algorithmTestJobLimiter struct {
	totalSlots chan struct{}
	videoSlots chan struct{}
}

type algorithmTestJobRetryImageItem struct {
	item model.AlgorithmTestJobItem
}

func newAlgorithmTestJobLimiter() *algorithmTestJobLimiter {
	return &algorithmTestJobLimiter{
		totalSlots: make(chan struct{}, algorithmTestJobTotalConcurrency),
		videoSlots: make(chan struct{}, algorithmTestJobVideoConcurrency),
	}
}

func (l *algorithmTestJobLimiter) acquire(mediaType algorithmTestMediaType) func() {
	if l == nil {
		return func() {}
	}
	l.totalSlots <- struct{}{}
	acquiredVideo := false
	if mediaType == algorithmTestMediaTypeVideo {
		l.videoSlots <- struct{}{}
		acquiredVideo = true
	}
	// 视频同时占用“总槽位 + 视频槽位”，确保混传时总请求数不会超过 5。
	return func() {
		if acquiredVideo {
			<-l.videoSlots
		}
		<-l.totalSlots
	}
}

func (s *Server) createAlgorithmTestJob(
	algorithm model.Algorithm,
	cameraID string,
	files []*multipart.FileHeader,
) (*model.AlgorithmTestJob, error) {
	batchID := uuid.NewString()
	job := &model.AlgorithmTestJob{
		ID:          uuid.NewString(),
		AlgorithmID: strings.TrimSpace(algorithm.ID),
		BatchID:     batchID,
		CameraID:    strings.TrimSpace(cameraID),
		Status:      model.AlgorithmTestJobStatusPending,
		TotalCount:  len(files),
	}

	items := make([]model.AlgorithmTestJobItem, 0, len(files))
	failedCount := 0
	pendingCount := 0
	for index, file := range files {
		fileName := strings.TrimSpace(file.Filename)
		item := model.AlgorithmTestJobItem{
			ID:               uuid.NewString(),
			JobID:            job.ID,
			AlgorithmID:      algorithm.ID,
			SortOrder:        index,
			FileName:         fileName,
			OriginalFileName: fileName,
			Status:           model.AlgorithmTestJobItemStatusPending,
		}

		saved, err := s.saveAlgorithmTestUpload(algorithm.ID, batchID, file)
		if err != nil {
			item.Status = model.AlgorithmTestJobItemStatusFailed
			item.Success = false
			item.Conclusion = "测试失败"
			item.Basis = err.Error()
			item.ErrorMessage = err.Error()
			failedCount++
		} else {
			item.MediaType = string(saved.MediaType)
			item.MediaPath = saved.RelativePath
			pendingCount++
		}
		items = append(items, item)
	}

	job.FailedCount = failedCount
	switch {
	case pendingCount == 0 && failedCount > 0:
		job.Status = model.AlgorithmTestJobStatusFailed
	case pendingCount > 0:
		job.Status = model.AlgorithmTestJobStatusPending
	default:
		job.Status = model.AlgorithmTestJobStatusCompleted
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		return tx.Create(&items).Error
	}); err != nil {
		return nil, fmt.Errorf("create algorithm test job failed: %w", err)
	}

	if pendingCount > 0 {
		go s.runAlgorithmTestJob(job.ID)
	}
	return job, nil
}

func (s *Server) runAlgorithmTestJob(jobID string) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return
	}

	var job model.AlgorithmTestJob
	if err := s.db.Where("id = ?", jobID).First(&job).Error; err != nil {
		logutil.Errorf("algorithm test job load failed: job_id=%s err=%v", jobID, err)
		return
	}

	var algorithm model.Algorithm
	if err := s.db.Where("id = ?", job.AlgorithmID).First(&algorithm).Error; err != nil {
		logutil.Errorf("algorithm test job algorithm load failed: job_id=%s algorithm_id=%s err=%v", job.ID, job.AlgorithmID, err)
		_ = s.failPendingAlgorithmTestJobItems(job.ID, "算法不存在")
		_, _ = s.refreshAlgorithmTestJobStatus(job.ID)
		return
	}

	var items []model.AlgorithmTestJobItem
	if err := s.db.Where("job_id = ?", job.ID).Order("sort_order asc").Find(&items).Error; err != nil {
		logutil.Errorf("algorithm test job items load failed: job_id=%s err=%v", job.ID, err)
		return
	}

	var hasPendingImage bool
	var hasPendingVideo bool
	for _, item := range items {
		if item.Status != model.AlgorithmTestJobItemStatusPending {
			continue
		}
		switch algorithmTestMediaType(strings.TrimSpace(item.MediaType)) {
		case algorithmTestMediaTypeImage:
			hasPendingImage = true
		case algorithmTestMediaTypeVideo:
			hasPendingVideo = true
		default:
			_ = s.updateAlgorithmTestJobItemFailure(item.ID, strings.TrimSpace(item.MediaPath), "测试失败", "unsupported media type")
		}
	}
	_, _ = s.refreshAlgorithmTestJobStatus(job.ID)

	if !hasPendingImage && !hasPendingVideo {
		return
	}

	algorithmConfig, imagePrompt, provider, err := s.buildAlgorithmTestConfig(algorithm)
	if err != nil {
		_ = s.failPendingAlgorithmTestJobItems(job.ID, err.Error())
		_, _ = s.refreshAlgorithmTestJobStatus(job.ID)
		return
	}

	videoPrompt := ""
	if hasPendingVideo {
		videoPrompt, err = s.buildAlgorithmVideoTestPrompt(algorithm)
		if err != nil {
			_ = s.failPendingAlgorithmTestJobItemsByMediaType(job.ID, string(algorithmTestMediaTypeVideo), err.Error())
			_, _ = s.refreshAlgorithmTestJobStatus(job.ID)
			hasPendingVideo = false
		}
	}

	var runnable []model.AlgorithmTestJobItem
	if err := s.db.Where("job_id = ? AND status = ?", job.ID, model.AlgorithmTestJobItemStatusPending).
		Order("sort_order asc").
		Find(&runnable).Error; err != nil {
		logutil.Errorf("algorithm test job runnable items load failed: job_id=%s err=%v", job.ID, err)
		return
	}
	if len(runnable) == 0 {
		_, _ = s.refreshAlgorithmTestJobStatus(job.ID)
		return
	}

	_ = s.db.Model(&model.AlgorithmTestJob{}).
		Where("id = ?", job.ID).
		Update("status", model.AlgorithmTestJobStatusRunning).Error

	retryLimit := s.analyzeImageFailureRetryCount()
	limiter := newAlgorithmTestJobLimiter()
	var wg sync.WaitGroup
	var retryMu sync.Mutex
	retryImages := make([]algorithmTestJobRetryImageItem, 0)
	for _, item := range runnable {
		current := item
		mediaType := algorithmTestMediaType(strings.TrimSpace(current.MediaType))
		switch mediaType {
		case algorithmTestMediaTypeImage, algorithmTestMediaTypeVideo:
		default:
			_ = s.updateAlgorithmTestJobItemFailure(current.ID, strings.TrimSpace(current.MediaPath), "测试失败", "unsupported media type")
			continue
		}

		releaseSlots := limiter.acquire(mediaType)
		if err := s.markAlgorithmTestJobItemRunning(current.ID); err != nil {
			releaseSlots()
			logutil.Errorf("algorithm test job item mark running failed: item_id=%s err=%v", current.ID, err)
			continue
		}

		wg.Add(1)
		go func() {
			defer func() {
				releaseSlots()
				wg.Done()
			}()

			media := algorithmTestSavedMedia{
				MediaType:    mediaType,
				RelativePath: strings.TrimSpace(current.MediaPath),
				FullPath:     s.algorithmTestMediaFullPath(strings.TrimSpace(current.MediaPath)),
			}
			fileName := strings.TrimSpace(current.OriginalFileName)
			if fileName == "" {
				fileName = strings.TrimSpace(current.FileName)
			}

			var result algorithmTestItemResult
			switch media.MediaType {
			case algorithmTestMediaTypeImage:
				result = s.runAlgorithmImageTest(context.Background(), algorithm, job.BatchID, job.ID, current.ID, job.CameraID, fileName, media, algorithmConfig, provider, imagePrompt, algorithmTestPersistenceOptions{
					PersistRecord:   false,
					PersistLLMUsage: true,
				})
				if retryLimit > 0 && shouldRetryAnalyzeImageFailure(result.Success, result.MediaType, result.AIRequestType, result.LLMCallStatus, result.ErrorMessage) {
					// 正式 job 的图片失败会统一补跑一轮；首轮只把 item 保持在 running，避免先落失败记录再补跑。
					if err := s.markAlgorithmTestJobItemRetrying(current.ID, media.RelativePath, result); err != nil {
						logutil.Errorf("algorithm test job item mark retrying failed: item_id=%s err=%v", current.ID, err)
						if err := s.persistFinalAlgorithmTestJobImageResult(algorithm.ID, job.BatchID, current, media.RelativePath, result); err != nil {
							logutil.Errorf("algorithm test job image final persist failed after retry mark error: item_id=%s err=%v", current.ID, err)
							return
						}
					} else {
						retryMu.Lock()
						retryImages = append(retryImages, algorithmTestJobRetryImageItem{item: current})
						retryMu.Unlock()
					}
				} else {
					if err := s.persistFinalAlgorithmTestJobImageResult(algorithm.ID, job.BatchID, current, media.RelativePath, result); err != nil {
						logutil.Errorf("algorithm test job image final persist failed: item_id=%s err=%v", current.ID, err)
						return
					}
				}
			case algorithmTestMediaTypeVideo:
				if strings.TrimSpace(videoPrompt) == "" {
					result = algorithmTestItemResult{
						FileName:        fileName,
						MediaType:       string(media.MediaType),
						Success:         false,
						Conclusion:      "测试失败",
						Basis:           "视频测试提示词未配置",
						MediaURL:        s.algorithmTestMediaURL(media.RelativePath),
						DurationSeconds: 0,
						ErrorMessage:    "视频测试提示词未配置",
					}
				} else {
					result = s.runAlgorithmVideoTest(context.Background(), algorithm, job.BatchID, job.ID, current.ID, job.CameraID, fileName, media, algorithmConfig, provider, videoPrompt, algorithmTestPersistenceOptions{
						PersistRecord:   true,
						PersistLLMUsage: true,
					})
				}
				if err := s.persistAlgorithmTestJobItemResult(current.ID, media.RelativePath, result); err != nil {
					logutil.Errorf("algorithm test job item persist failed: item_id=%s err=%v", current.ID, err)
					return
				}
			default:
				result = algorithmTestItemResult{
					FileName:     fileName,
					MediaType:    string(media.MediaType),
					Success:      false,
					Conclusion:   "测试失败",
					Basis:        "unsupported media type",
					MediaURL:     s.algorithmTestMediaURL(media.RelativePath),
					ErrorMessage: "unsupported media type",
				}
				if err := s.persistAlgorithmTestJobItemResult(current.ID, media.RelativePath, result); err != nil {
					logutil.Errorf("algorithm test job item persist failed: item_id=%s err=%v", current.ID, err)
					return
				}
			}
			if _, err := s.refreshAlgorithmTestJobStatus(job.ID); err != nil {
				logutil.Errorf("algorithm test job refresh failed: job_id=%s err=%v", job.ID, err)
			}
		}()
	}
	wg.Wait()

	retryMu.Lock()
	pendingRetryImages := append([]algorithmTestJobRetryImageItem(nil), retryImages...)
	retryMu.Unlock()
	sort.SliceStable(pendingRetryImages, func(i, j int) bool {
		return pendingRetryImages[i].item.SortOrder < pendingRetryImages[j].item.SortOrder
	})

	for retryRound := 1; retryRound <= retryLimit && len(pendingRetryImages) > 0; retryRound++ {
		logutil.Infof(
			"algorithm test job image retry round start: job_id=%s round=%d max_rounds=%d retry_count=%d",
			job.ID,
			retryRound,
			retryLimit,
			len(pendingRetryImages),
		)
		retryLimiter := newAlgorithmTestJobLimiter()
		var retryWG sync.WaitGroup
		var nextRetryMu sync.Mutex
		nextRetryImages := make([]algorithmTestJobRetryImageItem, 0)
		for _, retryItem := range pendingRetryImages {
			current := retryItem.item
			releaseSlots := retryLimiter.acquire(algorithmTestMediaTypeImage)
			retryWG.Add(1)
			go func() {
				defer func() {
					releaseSlots()
					retryWG.Done()
				}()

				media := algorithmTestSavedMedia{
					MediaType:    algorithmTestMediaTypeImage,
					RelativePath: strings.TrimSpace(current.MediaPath),
					FullPath:     s.algorithmTestMediaFullPath(strings.TrimSpace(current.MediaPath)),
				}
				fileName := strings.TrimSpace(current.OriginalFileName)
				if fileName == "" {
					fileName = strings.TrimSpace(current.FileName)
				}
				result := s.runAlgorithmImageTest(context.Background(), algorithm, job.BatchID, job.ID, current.ID, job.CameraID, fileName, media, algorithmConfig, provider, imagePrompt, algorithmTestPersistenceOptions{
					PersistRecord:   false,
					PersistLLMUsage: true,
				})
				if retryRound < retryLimit && shouldRetryAnalyzeImageFailure(result.Success, result.MediaType, result.AIRequestType, result.LLMCallStatus, result.ErrorMessage) {
					if err := s.markAlgorithmTestJobItemRetrying(current.ID, media.RelativePath, result); err != nil {
						logutil.Errorf("algorithm test job image retry mark retrying failed: item_id=%s round=%d err=%v", current.ID, retryRound, err)
						if err := s.persistFinalAlgorithmTestJobImageResult(algorithm.ID, job.BatchID, current, media.RelativePath, result); err != nil {
							logutil.Errorf("algorithm test job image retry final persist failed after mark error: item_id=%s round=%d err=%v", current.ID, retryRound, err)
							return
						}
					} else {
						nextRetryMu.Lock()
						nextRetryImages = append(nextRetryImages, algorithmTestJobRetryImageItem{item: current})
						nextRetryMu.Unlock()
					}
				} else {
					if err := s.persistFinalAlgorithmTestJobImageResult(algorithm.ID, job.BatchID, current, media.RelativePath, result); err != nil {
						logutil.Errorf("algorithm test job image retry persist failed: item_id=%s round=%d err=%v", current.ID, retryRound, err)
						return
					}
				}
				if _, err := s.refreshAlgorithmTestJobStatus(job.ID); err != nil {
					logutil.Errorf("algorithm test job refresh after image retry failed: job_id=%s round=%d err=%v", job.ID, retryRound, err)
				}
			}()
		}
		retryWG.Wait()
		pendingRetryImages = pendingRetryImages[:0]
		nextRetryMu.Lock()
		pendingRetryImages = append(pendingRetryImages, nextRetryImages...)
		nextRetryMu.Unlock()
		sort.SliceStable(pendingRetryImages, func(i, j int) bool {
			return pendingRetryImages[i].item.SortOrder < pendingRetryImages[j].item.SortOrder
		})
	}
	if snapshot, err := s.refreshAlgorithmTestJobStatus(job.ID); err == nil {
		logutil.Infof(
			"algorithm test job finished: job_id=%s status=%s success=%d failed=%d total=%d",
			job.ID,
			snapshot.Status,
			snapshot.SuccessCount,
			snapshot.FailedCount,
			snapshot.TotalCount,
		)
	}
}

func (s *Server) persistFinalAlgorithmTestJobImageResult(
	algorithmID string,
	batchID string,
	item model.AlgorithmTestJobItem,
	mediaPath string,
	result algorithmTestItemResult,
) error {
	result.MediaType = firstNonEmpty(strings.TrimSpace(result.MediaType), strings.TrimSpace(item.MediaType))
	if strings.TrimSpace(result.FileName) == "" {
		result.FileName = resolveAlgorithmTestJobItemFileName(item)
	}
	if strings.TrimSpace(result.MediaURL) == "" {
		result.MediaURL = s.algorithmTestMediaURL(strings.TrimSpace(mediaPath))
	}
	if shouldPersistAlgorithmTestJobImageRecord(result) {
		record := model.AlgorithmTestRecord{
			ID:               uuid.NewString(),
			AlgorithmID:      strings.TrimSpace(algorithmID),
			BatchID:          strings.TrimSpace(batchID),
			MediaType:        string(algorithmTestMediaTypeImage),
			MediaPath:        strings.TrimSpace(mediaPath),
			OriginalFileName: strings.TrimSpace(result.FileName),
			ImagePath:        strings.TrimSpace(mediaPath),
			RequestPayload:   strings.TrimSpace(result.RequestPayload),
			ResponsePayload:  strings.TrimSpace(result.ResponsePayload),
			Success:          result.Success,
		}
		if err := s.db.Create(&record).Error; err != nil {
			logutil.Errorf("algorithm test image record create failed: item_id=%s err=%v", item.ID, err)
		} else {
			result.RecordID = strings.TrimSpace(record.ID)
			result.Record = record
		}
	}
	return s.persistAlgorithmTestJobItemResult(item.ID, mediaPath, result)
}

func shouldPersistAlgorithmTestJobImageRecord(result algorithmTestItemResult) bool {
	return strings.TrimSpace(result.RequestPayload) != "" || strings.TrimSpace(result.ResponsePayload) != ""
}

func (s *Server) markAlgorithmTestJobItemRetrying(itemID, mediaPath string, result algorithmTestItemResult) error {
	// 这里显式保留 running，前端轮询时会把该分项持续显示为“自动重试中”，直到统一补跑结束。
	updates := map[string]any{
		"status":           model.AlgorithmTestJobItemStatusRunning,
		"success":          false,
		"record_id":        "",
		"conclusion":       "自动重试中",
		"basis":            buildAlgorithmTestJobImageRetryBasis(result),
		"normalized_boxes": "[]",
		"anomaly_times":    "[]",
		"duration_seconds": 0,
		"error_message":    "",
		"updated_at":       time.Now(),
	}
	if strings.TrimSpace(mediaPath) != "" {
		updates["media_path"] = strings.TrimSpace(mediaPath)
	}
	return s.db.Model(&model.AlgorithmTestJobItem{}).Where("id = ?", itemID).Updates(updates).Error
}

func buildAlgorithmTestJobImageRetryBasis(result algorithmTestItemResult) string {
	reason := strings.TrimSpace(firstNonEmpty(result.ErrorMessage, result.Basis))
	if reason == "" {
		reason = "图片分析失败"
	}
	return "首轮失败可自动补跑，正在统一重试一次：" + reason
}

func (s *Server) getAlgorithmTestJob(c *gin.Context) {
	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		s.fail(c, http.StatusBadRequest, "job id is required")
		return
	}
	snapshot, err := s.loadAlgorithmTestJobSnapshot(jobID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			s.fail(c, http.StatusNotFound, "test job not found")
			return
		}
		s.fail(c, http.StatusInternalServerError, "query test job failed")
		return
	}
	s.ok(c, snapshot)
}

func (s *Server) loadAlgorithmTestJobSnapshot(jobID string) (*algorithmTestJobSnapshot, error) {
	var job model.AlgorithmTestJob
	if err := s.db.Where("id = ?", jobID).First(&job).Error; err != nil {
		return nil, err
	}
	var items []model.AlgorithmTestJobItem
	if err := s.db.Where("job_id = ?", jobID).Order("sort_order asc").Find(&items).Error; err != nil {
		return nil, err
	}
	out := make([]algorithmTestItemResult, 0, len(items))
	for _, item := range items {
		out = append(out, s.buildAlgorithmTestJobItemResult(item))
	}
	return &algorithmTestJobSnapshot{
		JobID:        job.ID,
		BatchID:      job.BatchID,
		AlgorithmID:  job.AlgorithmID,
		Status:       job.Status,
		TotalCount:   job.TotalCount,
		SuccessCount: job.SuccessCount,
		FailedCount:  job.FailedCount,
		Items:        out,
	}, nil
}

func (s *Server) buildAlgorithmTestJobItemResult(item model.AlgorithmTestJobItem) algorithmTestItemResult {
	boxes := parseAlgorithmTestJobBoxes(item.NormalizedBoxes)
	anomalyTimes := parseAlgorithmTestJobAnomalyTimes(item.AnomalyTimes)
	result := algorithmTestItemResult{
		JobItemID:       item.ID,
		SortOrder:       item.SortOrder,
		Status:          strings.TrimSpace(item.Status),
		RecordID:        strings.TrimSpace(item.RecordID),
		FileName:        resolveAlgorithmTestJobItemFileName(item),
		MediaType:       strings.TrimSpace(item.MediaType),
		Success:         item.Success,
		Conclusion:      strings.TrimSpace(item.Conclusion),
		Basis:           strings.TrimSpace(item.Basis),
		MediaURL:        s.algorithmTestMediaURL(strings.TrimSpace(item.MediaPath)),
		NormalizedBoxes: boxes,
		AnomalyTimes:    anomalyTimes,
		DurationSeconds: item.DurationSeconds,
		ErrorMessage:    strings.TrimSpace(item.ErrorMessage),
	}
	if result.Success && result.Status != model.AlgorithmTestJobItemStatusFailed {
		result.ErrorMessage = ""
	}
	switch result.Status {
	case model.AlgorithmTestJobItemStatusPending:
		if result.Conclusion == "" {
			result.Conclusion = "排队中"
		}
		if result.Basis == "" {
			result.Basis = "等待开始分析"
		}
	case model.AlgorithmTestJobItemStatusRunning:
		if result.Conclusion == "" {
			result.Conclusion = "分析中"
		}
		if result.Basis == "" {
			result.Basis = "正在调用 AI 分析"
		}
	case model.AlgorithmTestJobItemStatusFailed:
		if result.Conclusion == "" {
			result.Conclusion = "测试失败"
		}
		if result.Basis == "" {
			result.Basis = firstNonEmpty(result.ErrorMessage, "分析失败")
		}
	}
	return result
}

func (s *Server) persistAlgorithmTestJobItemResult(itemID string, mediaPath string, result algorithmTestItemResult) error {
	normalizedBoxesJSON, _ := json.Marshal(result.NormalizedBoxes)
	if len(normalizedBoxesJSON) == 0 {
		normalizedBoxesJSON = []byte("[]")
	}
	anomalyTimesJSON, _ := json.Marshal(result.AnomalyTimes)
	if len(anomalyTimesJSON) == 0 {
		anomalyTimesJSON = []byte("[]")
	}

	status := model.AlgorithmTestJobItemStatusFailed
	errorMessage := strings.TrimSpace(firstNonEmpty(result.ErrorMessage, result.Basis))
	if result.Success {
		status = model.AlgorithmTestJobItemStatusSuccess
		errorMessage = ""
	}
	updates := map[string]any{
		"status":           status,
		"success":          result.Success,
		"record_id":        strings.TrimSpace(result.RecordID),
		"conclusion":       strings.TrimSpace(result.Conclusion),
		"basis":            strings.TrimSpace(result.Basis),
		"normalized_boxes": string(normalizedBoxesJSON),
		"anomaly_times":    string(anomalyTimesJSON),
		"duration_seconds": result.DurationSeconds,
		"error_message":    errorMessage,
		"updated_at":       time.Now(),
	}
	if strings.TrimSpace(mediaPath) != "" {
		updates["media_path"] = strings.TrimSpace(mediaPath)
	}
	return s.db.Model(&model.AlgorithmTestJobItem{}).Where("id = ?", itemID).Updates(updates).Error
}

func (s *Server) markAlgorithmTestJobItemRunning(itemID string) error {
	return s.db.Model(&model.AlgorithmTestJobItem{}).
		Where("id = ? AND status = ?", itemID, model.AlgorithmTestJobItemStatusPending).
		Updates(map[string]any{
			"status":     model.AlgorithmTestJobItemStatusRunning,
			"updated_at": time.Now(),
		}).Error
}

func (s *Server) failPendingAlgorithmTestJobItems(jobID string, basis string) error {
	return s.db.Model(&model.AlgorithmTestJobItem{}).
		Where("job_id = ? AND status IN ?", jobID, []string{model.AlgorithmTestJobItemStatusPending, model.AlgorithmTestJobItemStatusRunning}).
		Updates(map[string]any{
			"status":        model.AlgorithmTestJobItemStatusFailed,
			"success":       false,
			"conclusion":    "测试失败",
			"basis":         strings.TrimSpace(basis),
			"error_message": strings.TrimSpace(basis),
			"updated_at":    time.Now(),
		}).Error
}

func (s *Server) failPendingAlgorithmTestJobItemsByMediaType(jobID, mediaType, basis string) error {
	return s.db.Model(&model.AlgorithmTestJobItem{}).
		Where("job_id = ? AND media_type = ? AND status IN ?", jobID, strings.TrimSpace(mediaType), []string{model.AlgorithmTestJobItemStatusPending, model.AlgorithmTestJobItemStatusRunning}).
		Updates(map[string]any{
			"status":        model.AlgorithmTestJobItemStatusFailed,
			"success":       false,
			"conclusion":    "测试失败",
			"basis":         strings.TrimSpace(basis),
			"error_message": strings.TrimSpace(basis),
			"updated_at":    time.Now(),
		}).Error
}

func (s *Server) updateAlgorithmTestJobItemFailure(itemID, mediaPath, conclusion, basis string) error {
	updates := map[string]any{
		"status":        model.AlgorithmTestJobItemStatusFailed,
		"success":       false,
		"conclusion":    strings.TrimSpace(conclusion),
		"basis":         strings.TrimSpace(basis),
		"error_message": strings.TrimSpace(basis),
		"updated_at":    time.Now(),
	}
	if strings.TrimSpace(mediaPath) != "" {
		updates["media_path"] = strings.TrimSpace(mediaPath)
	}
	return s.db.Model(&model.AlgorithmTestJobItem{}).Where("id = ?", itemID).Updates(updates).Error
}

func (s *Server) refreshAlgorithmTestJobStatus(jobID string) (*algorithmTestJobSnapshot, error) {
	snapshot, err := s.loadAlgorithmTestJobSnapshot(jobID)
	if err != nil {
		return nil, err
	}

	pending := 0
	running := 0
	success := 0
	failed := 0
	for _, item := range snapshot.Items {
		switch item.Status {
		case model.AlgorithmTestJobItemStatusSuccess:
			success++
		case model.AlgorithmTestJobItemStatusFailed:
			failed++
		case model.AlgorithmTestJobItemStatusRunning:
			running++
		default:
			pending++
		}
	}

	status := model.AlgorithmTestJobStatusPending
	switch {
	case success+failed == snapshot.TotalCount && failed == 0:
		status = model.AlgorithmTestJobStatusCompleted
	case success+failed == snapshot.TotalCount && success == 0 && failed > 0:
		status = model.AlgorithmTestJobStatusFailed
	case success+failed == snapshot.TotalCount && failed > 0:
		status = model.AlgorithmTestJobStatusPartialFailed
	case running > 0 || success > 0 || failed > 0:
		status = model.AlgorithmTestJobStatusRunning
	default:
		status = model.AlgorithmTestJobStatusPending
	}

	if err := s.db.Model(&model.AlgorithmTestJob{}).Where("id = ?", jobID).Updates(map[string]any{
		"status":        status,
		"success_count": success,
		"failed_count":  failed,
		"updated_at":    time.Now(),
	}).Error; err != nil {
		return nil, err
	}

	snapshot.Status = status
	snapshot.SuccessCount = success
	snapshot.FailedCount = failed
	return snapshot, nil
}

func parseAlgorithmTestJobBoxes(raw string) []normalizedBox {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var boxes []normalizedBox
	if err := json.Unmarshal([]byte(trimmed), &boxes); err != nil {
		return nil
	}
	return boxes
}

func parseAlgorithmTestJobAnomalyTimes(raw string) []ai.SequenceAnomalyTime {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var times []ai.SequenceAnomalyTime
	if err := json.Unmarshal([]byte(trimmed), &times); err != nil {
		return nil
	}
	return times
}

func resolveAlgorithmTestJobItemFileName(item model.AlgorithmTestJobItem) string {
	if name := strings.TrimSpace(item.OriginalFileName); name != "" {
		return name
	}
	if name := strings.TrimSpace(item.FileName); name != "" {
		return name
	}
	if path := strings.TrimSpace(item.MediaPath); path != "" {
		parts := strings.Split(strings.ReplaceAll(path, "\\", "/"), "/")
		return parts[len(parts)-1]
	}
	return ""
}

func (s *Server) algorithmTestMediaFullPath(path string) string {
	normalized := normalizeAlgorithmTestMediaRelPath(path)
	if normalized == "" {
		return ""
	}
	return filepath.Join(algorithmTestMediaRootDir, filepath.FromSlash(normalized))
}
