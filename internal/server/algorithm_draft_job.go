package server

import (
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"maas-box/internal/ai"
	"maas-box/internal/model"
)

const draftAlgorithmTestTTL = 30 * time.Minute

type draftAlgorithmTestJob struct {
	mu           sync.RWMutex
	snapshot     algorithmTestJobSnapshot
	cameraID     string
	algorithm    model.Algorithm
	algorithmCfg ai.StartCameraAlgorithmConfig
	provider     model.ModelProvider
	imagePrompt  string
	videoPrompt  string
	items        []draftAlgorithmTestJobItem
	expiresAt    time.Time
}

type draftAlgorithmTestJobItem struct {
	ItemID   string
	FileName string
	Media    algorithmTestSavedMedia
}

func (s *Server) draftTestAlgorithm(c *gin.Context) {
	if err := c.Request.ParseMultipartForm(256 << 20); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid multipart payload")
		return
	}
	if err := s.ensureLLMTokenQuotaAvailable(); err != nil {
		if isLLMTokenLimitExceededError(err) {
			s.fail(c, http.StatusBadRequest, llmTokenLimitExceededMessage)
			return
		}
		s.fail(c, http.StatusInternalServerError, "check llm token quota failed")
		return
	}
	files := c.Request.MultipartForm.File["files"]
	if len(files) == 0 {
		s.fail(c, http.StatusBadRequest, "files is required")
		return
	}
	if err := s.validateAlgorithmTestFileCounts(files); err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		s.fail(c, http.StatusBadRequest, "算法名称不能为空")
		return
	}
	prompt := strings.TrimSpace(c.PostForm("prompt"))
	if prompt == "" {
		s.fail(c, http.StatusBadRequest, "提示词不能为空")
		return
	}
	detectMode := strings.TrimSpace(c.PostForm("detect_mode"))
	if detectMode == "" {
		detectMode = "2"
	}
	if detectMode != "2" {
		s.fail(c, http.StatusBadRequest, "草稿算法测试仅支持模式 2")
		return
	}

	job, err := s.createDraftAlgorithmTestJob(
		name,
		strings.TrimSpace(c.PostForm("description")),
		prompt,
		strings.TrimSpace(c.PostForm("camera_id")),
		files,
	)
	if err != nil {
		if isBadRequestError(err) {
			s.fail(c, http.StatusBadRequest, err.Error())
			return
		}
		s.fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	s.ok(c, gin.H{
		"job_id":       job.snapshot.JobID,
		"batch_id":     job.snapshot.BatchID,
		"algorithm_id": job.snapshot.AlgorithmID,
		"status":       job.snapshot.Status,
		"total_count":  job.snapshot.TotalCount,
	})
}

func (s *Server) getDraftAlgorithmTestJob(c *gin.Context) {
	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		s.fail(c, http.StatusBadRequest, "job id is required")
		return
	}
	snapshot, ok := s.loadDraftAlgorithmTestJob(jobID)
	if !ok {
		s.fail(c, http.StatusNotFound, "draft test job not found")
		return
	}
	s.ok(c, snapshot)
}

func (s *Server) createDraftAlgorithmTestJob(
	name string,
	description string,
	prompt string,
	cameraID string,
	files []*multipart.FileHeader,
) (*draftAlgorithmTestJob, error) {
	provider, err := s.getConfiguredLLMProvider()
	if err != nil {
		return nil, errBadRequest(err.Error())
	}
	algorithmCode := fmt.Sprintf("CAM2ALG_%s", strings.ToUpper(strings.ReplaceAll(uuid.NewString()[:8], "-", "")))
	algorithmID := "draft-" + uuid.NewString()
	imagePrompt, err := s.composeAlgorithmTestPrompt(name, "图片测试", prompt)
	if err != nil {
		return nil, err
	}
	videoPrompt, err := s.composeAlgorithmTestPrompt(name, "视频测试", prompt)
	if err != nil {
		return nil, err
	}

	algorithm := model.Algorithm{
		ID:                algorithmID,
		Code:              algorithmCode,
		Name:              name,
		Description:       description,
		Mode:              model.AlgorithmModeLarge,
		Enabled:           true,
		DetectMode:        model.AlgorithmDetectModeLLMOnly,
		YoloThreshold:     0.5,
		IOUThreshold:      0.8,
		LabelsTriggerMode: model.LabelsTriggerModeAny,
	}
	algorithmCfg := ai.StartCameraAlgorithmConfig{
		AlgorithmID:       algorithm.ID,
		TaskCode:          algorithmCode,
		DetectMode:        model.AlgorithmDetectModeLLMOnly,
		Labels:            []string{},
		YoloThreshold:     0.5,
		IOUThreshold:      0.8,
		LabelsTriggerMode: model.LabelsTriggerModeAny,
	}

	batchID := uuid.NewString()
	jobID := uuid.NewString()
	expiresAt := time.Now().Add(draftAlgorithmTestTTL)
	snapshot := algorithmTestJobSnapshot{
		JobID:       jobID,
		BatchID:     batchID,
		AlgorithmID: algorithm.ID,
		Status:      model.AlgorithmTestJobStatusPending,
		TotalCount:  len(files),
		Items:       make([]algorithmTestItemResult, 0, len(files)),
	}
	jobItems := make([]draftAlgorithmTestJobItem, 0, len(files))
	for idx, file := range files {
		itemID := uuid.NewString()
		fileName := strings.TrimSpace(file.Filename)
		saved, saveErr := s.saveAlgorithmTestUpload("draft", batchID, file)
		if saveErr != nil {
			snapshot.Items = append(snapshot.Items, algorithmTestItemResult{
				JobItemID:    itemID,
				SortOrder:    idx,
				Status:       model.AlgorithmTestJobItemStatusFailed,
				FileName:     fileName,
				Success:      false,
				Conclusion:   "测试失败",
				Basis:        saveErr.Error(),
				ErrorMessage: saveErr.Error(),
			})
			snapshot.FailedCount++
			continue
		}
		snapshot.Items = append(snapshot.Items, algorithmTestItemResult{
			JobItemID: itemID,
			SortOrder: idx,
			Status:    model.AlgorithmTestJobItemStatusPending,
			FileName:  fileName,
			MediaType: string(saved.MediaType),
			MediaURL:  s.algorithmTestMediaURL(saved.RelativePath),
		})
		jobItems = append(jobItems, draftAlgorithmTestJobItem{
			ItemID:   itemID,
			FileName: fileName,
			Media:    saved,
		})
	}
	if len(jobItems) == 0 && snapshot.FailedCount > 0 {
		snapshot.Status = model.AlgorithmTestJobStatusFailed
	}

	job := &draftAlgorithmTestJob{
		snapshot:     snapshot,
		cameraID:     cameraID,
		algorithm:    algorithm,
		algorithmCfg: algorithmCfg,
		provider:     provider,
		imagePrompt:  imagePrompt,
		videoPrompt:  videoPrompt,
		items:        jobItems,
		expiresAt:    expiresAt,
	}

	s.draftAlgorithmTestMu.Lock()
	if s.draftAlgorithmTestJobs == nil {
		s.draftAlgorithmTestJobs = make(map[string]*draftAlgorithmTestJob)
	}
	s.purgeExpiredDraftAlgorithmTestsLocked()
	s.draftAlgorithmTestJobs[job.snapshot.JobID] = job
	s.draftAlgorithmTestMu.Unlock()

	if len(jobItems) > 0 {
		go s.runDraftAlgorithmTestJob(job.snapshot.JobID)
	}
	return job, nil
}

func (s *Server) runDraftAlgorithmTestJob(jobID string) {
	job, ok := s.getDraftAlgorithmTestJobRef(jobID)
	if !ok {
		return
	}
	retryLimit := s.analyzeImageFailureRetryCount()
	job.mu.Lock()
	if len(job.items) > 0 {
		job.snapshot.Status = model.AlgorithmTestJobStatusRunning
	}
	job.mu.Unlock()

	pendingRetryImages := make([]draftAlgorithmTestJobItem, 0)
	for _, current := range job.items {
		job.mu.Lock()
		for idx := range job.snapshot.Items {
			if job.snapshot.Items[idx].JobItemID == current.ItemID {
				job.snapshot.Items[idx].Status = model.AlgorithmTestJobItemStatusRunning
				break
			}
		}
		job.mu.Unlock()

		var result algorithmTestItemResult
		switch current.Media.MediaType {
		case algorithmTestMediaTypeImage:
			result = s.runAlgorithmImageTest(
				context.Background(),
				job.algorithm,
				job.snapshot.BatchID,
				job.snapshot.JobID,
				current.ItemID,
				job.cameraID,
				current.FileName,
				current.Media,
				job.algorithmCfg,
				job.provider,
				job.imagePrompt,
				algorithmTestPersistenceOptions{
					PersistRecord:   false,
					PersistLLMUsage: true,
				},
			)
		case algorithmTestMediaTypeVideo:
			result = s.runAlgorithmVideoTest(
				context.Background(),
				job.algorithm,
				job.snapshot.BatchID,
				job.snapshot.JobID,
				current.ItemID,
				job.cameraID,
				current.FileName,
				current.Media,
				job.algorithmCfg,
				job.provider,
				job.videoPrompt,
				algorithmTestPersistenceOptions{
					PersistRecord:   false,
					PersistLLMUsage: true,
				},
			)
		default:
			result = algorithmTestItemResult{
				JobItemID:    current.ItemID,
				FileName:     current.FileName,
				MediaType:    string(current.Media.MediaType),
				Success:      false,
				Conclusion:   "测试失败",
				Basis:        "unsupported media type",
				MediaURL:     s.algorithmTestMediaURL(current.Media.RelativePath),
				ErrorMessage: "unsupported media type",
			}
		}

		job.mu.Lock()
		if retryLimit > 0 && shouldRetryAnalyzeImageFailure(result.Success, result.MediaType, result.AIRequestType, result.LLMCallStatus, result.ErrorMessage) {
			s.markDraftAlgorithmTestJobItemRetryingLocked(job, current, result, 1, retryLimit)
			pendingRetryImages = append(pendingRetryImages, current)
		} else {
			s.persistDraftAlgorithmTestJobItemResultLocked(job, current, result)
		}
		s.recomputeDraftAlgorithmTestJobLocked(job)
		job.mu.Unlock()
	}

	for retryRound := 1; retryRound <= retryLimit && len(pendingRetryImages) > 0; retryRound++ {
		nextRetryImages := make([]draftAlgorithmTestJobItem, 0)
		for _, current := range pendingRetryImages {
			result := s.runDraftAlgorithmTestItem(job, current)
			job.mu.Lock()
			if retryRound < retryLimit && shouldRetryAnalyzeImageFailure(result.Success, result.MediaType, result.AIRequestType, result.LLMCallStatus, result.ErrorMessage) {
				s.markDraftAlgorithmTestJobItemRetryingLocked(job, current, result, retryRound+1, retryLimit)
				nextRetryImages = append(nextRetryImages, current)
			} else {
				s.persistDraftAlgorithmTestJobItemResultLocked(job, current, result)
			}
			s.recomputeDraftAlgorithmTestJobLocked(job)
			job.mu.Unlock()
		}
		pendingRetryImages = nextRetryImages
	}
}

func (s *Server) runDraftAlgorithmTestItem(job *draftAlgorithmTestJob, current draftAlgorithmTestJobItem) algorithmTestItemResult {
	switch current.Media.MediaType {
	case algorithmTestMediaTypeImage:
		return s.runAlgorithmImageTest(
			context.Background(),
			job.algorithm,
			job.snapshot.BatchID,
			job.snapshot.JobID,
			current.ItemID,
			job.cameraID,
			current.FileName,
			current.Media,
			job.algorithmCfg,
			job.provider,
			job.imagePrompt,
			algorithmTestPersistenceOptions{
				PersistRecord:   false,
				PersistLLMUsage: true,
			},
		)
	case algorithmTestMediaTypeVideo:
		return s.runAlgorithmVideoTest(
			context.Background(),
			job.algorithm,
			job.snapshot.BatchID,
			job.snapshot.JobID,
			current.ItemID,
			job.cameraID,
			current.FileName,
			current.Media,
			job.algorithmCfg,
			job.provider,
			job.videoPrompt,
			algorithmTestPersistenceOptions{
				PersistRecord:   false,
				PersistLLMUsage: true,
			},
		)
	default:
		return algorithmTestItemResult{
			JobItemID:    current.ItemID,
			FileName:     current.FileName,
			MediaType:    string(current.Media.MediaType),
			Success:      false,
			Conclusion:   "测试失败",
			Basis:        "unsupported media type",
			MediaURL:     s.algorithmTestMediaURL(current.Media.RelativePath),
			ErrorMessage: "unsupported media type",
		}
	}
}

func (s *Server) persistDraftAlgorithmTestJobItemResultLocked(job *draftAlgorithmTestJob, current draftAlgorithmTestJobItem, result algorithmTestItemResult) {
	if job == nil {
		return
	}
	for idx := range job.snapshot.Items {
		if job.snapshot.Items[idx].JobItemID != current.ItemID {
			continue
		}
		result.JobItemID = current.ItemID
		result.SortOrder = job.snapshot.Items[idx].SortOrder
		if strings.TrimSpace(result.Status) == "" {
			if strings.TrimSpace(result.ErrorMessage) != "" {
				result.Status = model.AlgorithmTestJobItemStatusFailed
			} else {
				result.Status = model.AlgorithmTestJobItemStatusSuccess
			}
		}
		job.snapshot.Items[idx] = result
		return
	}
}

func (s *Server) markDraftAlgorithmTestJobItemRetryingLocked(job *draftAlgorithmTestJob, current draftAlgorithmTestJobItem, result algorithmTestItemResult, retryRound, maxRetryRounds int) {
	if job == nil {
		return
	}
	reason := strings.TrimSpace(firstNonEmpty(result.ErrorMessage, result.Basis))
	for idx := range job.snapshot.Items {
		if job.snapshot.Items[idx].JobItemID != current.ItemID {
			continue
		}
		item := job.snapshot.Items[idx]
		item.Status = model.AlgorithmTestJobItemStatusRunning
		item.Success = false
		item.RecordID = ""
		item.Conclusion = "自动重试中"
		item.Basis = buildAnalyzeImageRetryHint(reason, retryRound, maxRetryRounds)
		item.ErrorMessage = ""
		job.snapshot.Items[idx] = item
		return
	}
}

func (s *Server) recomputeDraftAlgorithmTestJobLocked(job *draftAlgorithmTestJob) {
	if job == nil {
		return
	}
	successCount := 0
	failedCount := 0
	runningCount := 0
	pendingCount := 0
	for idx := range job.snapshot.Items {
		status := strings.TrimSpace(job.snapshot.Items[idx].Status)
		switch status {
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

func (s *Server) getDraftAlgorithmTestJobRef(jobID string) (*draftAlgorithmTestJob, bool) {
	s.draftAlgorithmTestMu.RLock()
	job, ok := s.draftAlgorithmTestJobs[strings.TrimSpace(jobID)]
	s.draftAlgorithmTestMu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(job.expiresAt) {
		s.draftAlgorithmTestMu.Lock()
		delete(s.draftAlgorithmTestJobs, strings.TrimSpace(jobID))
		s.draftAlgorithmTestMu.Unlock()
		return nil, false
	}
	return job, true
}

func (s *Server) loadDraftAlgorithmTestJob(jobID string) (algorithmTestJobSnapshot, bool) {
	job, ok := s.getDraftAlgorithmTestJobRef(jobID)
	if !ok {
		return algorithmTestJobSnapshot{}, false
	}
	job.mu.RLock()
	defer job.mu.RUnlock()
	items := make([]algorithmTestItemResult, len(job.snapshot.Items))
	copy(items, job.snapshot.Items)
	snapshot := job.snapshot
	snapshot.Items = items
	return snapshot, true
}

func (s *Server) purgeExpiredDraftAlgorithmTestsLocked() {
	now := time.Now()
	for jobID, job := range s.draftAlgorithmTestJobs {
		if job == nil || now.After(job.expiresAt) {
			delete(s.draftAlgorithmTestJobs, jobID)
		}
	}
}
