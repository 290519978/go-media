package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maas-box/internal/ai"
	"maas-box/internal/model"
)

type algorithmTestJobCreateResponse struct {
	Code int `json:"code"`
	Data struct {
		JobID       string `json:"job_id"`
		BatchID     string `json:"batch_id"`
		AlgorithmID string `json:"algorithm_id"`
		Status      string `json:"status"`
		TotalCount  int    `json:"total_count"`
	} `json:"data"`
}

type algorithmTestJobPollResponse struct {
	Code int                      `json:"code"`
	Data algorithmTestJobSnapshot `json:"data"`
}

type draftAlgorithmTestJobCreateResponse struct {
	Code int `json:"code"`
	Data struct {
		JobID       string `json:"job_id"`
		BatchID     string `json:"batch_id"`
		AlgorithmID string `json:"algorithm_id"`
		Status      string `json:"status"`
		TotalCount  int    `json:"total_count"`
	} `json:"data"`
}

func recordAlgorithmTestPeak(peak *int32, current int32) {
	for {
		oldPeak := atomic.LoadInt32(peak)
		if current <= oldPeak {
			return
		}
		if atomic.CompareAndSwapInt32(peak, oldPeak, current) {
			return
		}
	}
}

func getAlgorithmTestJobSnapshot(t *testing.T, engine http.Handler, token, jobID string) algorithmTestJobSnapshot {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/test-jobs/"+jobID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get test job failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp algorithmTestJobPollResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode test job response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected test job payload: %s", rec.Body.String())
	}
	return resp.Data
}

func waitAlgorithmTestJob(t *testing.T, engine http.Handler, token, jobID string) algorithmTestJobSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := getAlgorithmTestJobSnapshot(t, engine, token, jobID)
		if snapshot.Status == model.AlgorithmTestJobStatusCompleted ||
			snapshot.Status == model.AlgorithmTestJobStatusPartialFailed ||
			snapshot.Status == model.AlgorithmTestJobStatusFailed {
			return snapshot
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("wait test job timeout: job_id=%s", jobID)
	return algorithmTestJobSnapshot{}
}

func waitDraftAlgorithmTestJob(t *testing.T, engine http.Handler, token, jobID string) algorithmTestJobSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/draft-test-jobs/"+jobID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get draft test job failed: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp algorithmTestJobPollResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode draft test job response failed: %v", err)
		}
		if resp.Code != 0 {
			t.Fatalf("unexpected draft test job payload: %s", rec.Body.String())
		}
		if resp.Data.Status == model.AlgorithmTestJobStatusCompleted ||
			resp.Data.Status == model.AlgorithmTestJobStatusPartialFailed ||
			resp.Data.Status == model.AlgorithmTestJobStatusFailed {
			return resp.Data
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("wait draft test job timeout: job_id=%s", jobID)
	return algorithmTestJobSnapshot{}
}

func TestAlgorithmTestMultipartImagesIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
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

	algorithm := model.Algorithm{
		ID:              "alg-test-media-image-1",
		Name:            "Multipart Image Test",
		Code:            "ALG_TEST_MEDIA_IMG_1",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range []string{"a.png", "b.png"} {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create form file failed: %v", err)
		}
		if _, err := part.Write(imageBytes); err != nil {
			t.Fatalf("write image failed: %v", err)
		}
	}
	_ = writer.WriteField("camera_id", "camera-test-1")
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			JobID      string `json:"job_id"`
			BatchID    string `json:"batch_id"`
			TotalCount int    `json:"total_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp.Code != 0 || strings.TrimSpace(resp.Data.JobID) == "" || resp.Data.TotalCount != 2 {
		t.Fatalf("unexpected multipart image response: %s", rec.Body.String())
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if len(jobSnapshot.Items) != 2 {
		t.Fatalf("expected 2 job items, got %+v", jobSnapshot)
	}
	for _, item := range jobSnapshot.Items {
		if item.MediaType != string(algorithmTestMediaTypeImage) {
			t.Fatalf("expected image media type, got %+v", item)
		}
		if !item.Success {
			t.Fatalf("expected success item, got %+v", item)
		}
		if strings.TrimSpace(item.MediaURL) == "" {
			t.Fatalf("expected media_url in response, got %+v", item)
		}
	}

	var records []model.AlgorithmTestRecord
	if err := s.db.Where("algorithm_id = ?", algorithm.ID).Find(&records).Error; err != nil {
		t.Fatalf("query records failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	for _, record := range records {
		if record.BatchID != resp.Data.BatchID {
			t.Fatalf("expected batch_id=%s, got %s", resp.Data.BatchID, record.BatchID)
		}
		if record.MediaType != string(algorithmTestMediaTypeImage) {
			t.Fatalf("expected image record, got %+v", record)
		}
		if strings.TrimSpace(record.MediaPath) == "" {
			t.Fatalf("expected media_path to be saved")
		}
	}
}

func TestAlgorithmTestMultipartImagesRunWithMaxFiveConcurrency(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	var inflight int32
	var peak int32
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		current := atomic.AddInt32(&inflight, 1)
		recordAlgorithmTestPeak(&peak, current)
		defer atomic.AddInt32(&inflight, -1)
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success: true,
			Message: "ok",
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:              "alg-test-media-image-concurrency-1",
		Name:            "Multipart Image Concurrency Test",
		Code:            "ALG_TEST_MEDIA_IMG_CONC_1",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png", "e.png"} {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create form file failed: %v", err)
		}
		if _, err := part.Write(imageBytes); err != nil {
			t.Fatalf("write image failed: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image concurrency test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if jobSnapshot.Status != model.AlgorithmTestJobStatusCompleted {
		t.Fatalf("expected completed job, got %+v", jobSnapshot)
	}
	if jobSnapshot.TotalCount != 5 || jobSnapshot.SuccessCount != 5 || jobSnapshot.FailedCount != 0 {
		t.Fatalf("unexpected job counters: %+v", jobSnapshot)
	}
	if len(jobSnapshot.Items) != 5 {
		t.Fatalf("expected 5 job items, got %+v", jobSnapshot)
	}
	if peak <= 1 || peak > algorithmTestJobTotalConcurrency {
		t.Fatalf("expected image concurrency in range (1,%d], got %d", algorithmTestJobTotalConcurrency, peak)
	}
	for _, item := range jobSnapshot.Items {
		if !item.Success || item.Status != model.AlgorithmTestJobItemStatusSuccess {
			t.Fatalf("expected success item, got %+v", item)
		}
	}
}

func TestAlgorithmTestMultipartMixedMediaSharesTotalConcurrencyLimit(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	s.cfg.Server.AI.AlgorithmTestVideoFPS = 2
	s.cfg.Server.AI.AlgorithmTestVideoMaxBytes = 100 * 1024 * 1024
	s.cfg.Server.AI.AlgorithmTestVideoMinSeconds = 2
	s.cfg.Server.AI.AlgorithmTestVideoMaxSeconds = 20 * 60

	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	oldProbe := algorithmTestProbeVideoRunner
	t.Cleanup(func() {
		algorithmTestProbeVideoRunner = oldProbe
	})
	algorithmTestProbeVideoRunner = func(videoPath string) (algorithmTestVideoMetadata, error) {
		return algorithmTestVideoMetadata{DurationSeconds: 12}, nil
	}

	var totalInflight int32
	var totalPeak int32
	var videoInflight int32
	var videoPeak int32
	var imageRequests int32
	var videoRequests int32
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/analyze_image":
			atomic.AddInt32(&imageRequests, 1)
			current := atomic.AddInt32(&totalInflight, 1)
			recordAlgorithmTestPeak(&totalPeak, current)
			defer atomic.AddInt32(&totalInflight, -1)
			time.Sleep(120 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
				Success:   true,
				Message:   "ok",
				LLMResult: `{"alarm":"0","conclusion":"正常","reason":"未发现异常","targets":[]}`,
			})
		case "/api/analyze_video_test":
			atomic.AddInt32(&videoRequests, 1)
			currentVideo := atomic.AddInt32(&videoInflight, 1)
			recordAlgorithmTestPeak(&videoPeak, currentVideo)
			currentTotal := atomic.AddInt32(&totalInflight, 1)
			recordAlgorithmTestPeak(&totalPeak, currentTotal)
			defer atomic.AddInt32(&videoInflight, -1)
			defer atomic.AddInt32(&totalInflight, -1)
			time.Sleep(150 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ai.AnalyzeVideoTestResponse{
				Success:   true,
				Message:   "ok",
				LLMResult: `{"alarm":"0","reason":"未发现异常","anomaly_times":[]}`,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-mixed-concurrency-1",
		Name:       "Multipart Mixed Concurrency Test",
		Code:       "ALG_TEST_MEDIA_MIXED_CONC_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-mixed-concurrency-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect mixed media anomaly",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	videoPart, err := writer.CreateFormFile("files", "test.mp4")
	if err != nil {
		t.Fatalf("create video form file failed: %v", err)
	}
	if _, err := videoPart.Write([]byte("fake-video-content")); err != nil {
		t.Fatalf("write video failed: %v", err)
	}
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png", "e.png"} {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create image form file failed: %v", err)
		}
		if _, err := part.Write(imageBytes); err != nil {
			t.Fatalf("write image failed: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart mixed test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if jobSnapshot.Status != model.AlgorithmTestJobStatusCompleted {
		t.Fatalf("expected completed mixed job, got %+v", jobSnapshot)
	}
	if jobSnapshot.TotalCount != 6 || jobSnapshot.SuccessCount != 6 || jobSnapshot.FailedCount != 0 {
		t.Fatalf("unexpected mixed job counters: %+v", jobSnapshot)
	}
	if len(jobSnapshot.Items) != 6 {
		t.Fatalf("expected 6 job items, got %+v", jobSnapshot)
	}
	if totalPeak <= 1 || totalPeak > algorithmTestJobTotalConcurrency {
		t.Fatalf("expected mixed total concurrency in range (1,%d], got %d", algorithmTestJobTotalConcurrency, totalPeak)
	}
	if videoPeak < 1 || videoPeak > algorithmTestJobVideoConcurrency {
		t.Fatalf("expected video concurrency in range [1,%d], got %d", algorithmTestJobVideoConcurrency, videoPeak)
	}
	if atomic.LoadInt32(&imageRequests) != 5 || atomic.LoadInt32(&videoRequests) != 1 {
		t.Fatalf("unexpected request distribution: images=%d videos=%d", imageRequests, videoRequests)
	}
}

func TestAlgorithmTestMultipartImagesRetryRecoverableFailuresOnce(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	var attemptsMu sync.Mutex
	attempts := make(map[string]int)
	retryStarted := make(chan struct{}, 1)
	releaseRetry := make(chan struct{})
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		var in ai.AnalyzeImageRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}

		fileName := ""
		switch {
		case strings.Contains(in.ImageRelPath, "timeout-a"):
			fileName = "timeout-a.png"
		case strings.Contains(in.ImageRelPath, "llm-b"):
			fileName = "llm-b.png"
		case strings.Contains(in.ImageRelPath, "ok-c"):
			fileName = "ok-c.png"
		case strings.Contains(in.ImageRelPath, "ok-d"):
			fileName = "ok-d.png"
		default:
			fileName = "ok-e.png"
		}

		attemptsMu.Lock()
		attempts[fileName]++
		attempt := attempts[fileName]
		attemptsMu.Unlock()

		switch fileName {
		case "timeout-a.png":
			if attempt == 1 {
				time.Sleep(180 * time.Millisecond)
			}
		case "llm-b.png":
			if attempt == 1 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
					Success: true,
					Message: "ok",
					LLMUsage: &ai.LLMUsage{
						CallStatus:   model.LLMUsageStatusError,
						ErrorMessage: "Connection error.",
					},
				})
				return
			}
			select {
			case retryStarted <- struct{}{}:
			default:
			}
			<-releaseRetry
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success:   true,
			Message:   "ok",
			LLMResult: `{"alarm":"0","conclusion":"正常","reason":"未发现异常","targets":[]}`,
			LLMUsage: &ai.LLMUsage{
				CallStatus: model.LLMUsageStatusSuccess,
			},
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 80*time.Millisecond)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-image-retry-once-1",
		Name:       "Multipart Image Retry Once Test",
		Code:       "ALG_TEST_MEDIA_IMAGE_RETRY_ONCE_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-image-retry-once-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect retryable image anomaly",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	expectedOrder := []string{"timeout-a.png", "llm-b.png", "ok-c.png", "ok-d.png", "ok-e.png"}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range expectedOrder {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create form file failed: %v", err)
		}
		if _, err := part.Write(imageBytes); err != nil {
			t.Fatalf("write image failed: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image retry test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	select {
	case <-retryStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected retry round to start")
	}

	retrySnapshot := getAlgorithmTestJobSnapshot(t, engine, token, resp.Data.JobID)
	if retrySnapshot.Status != model.AlgorithmTestJobStatusRunning {
		t.Fatalf("expected running snapshot during retry, got %+v", retrySnapshot)
	}
	if len(retrySnapshot.Items) != len(expectedOrder) {
		t.Fatalf("expected %d items during retry, got %+v", len(expectedOrder), retrySnapshot)
	}
	for index, item := range retrySnapshot.Items {
		if item.SortOrder != index {
			t.Fatalf("expected sort_order=%d during retry, got %+v", index, item)
		}
		if item.FileName != expectedOrder[index] {
			t.Fatalf("expected item order %v, got %+v", expectedOrder, retrySnapshot.Items)
		}
	}
	retryingItemFound := false
	for _, item := range retrySnapshot.Items {
		if item.FileName != "llm-b.png" {
			continue
		}
		retryingItemFound = true
		if item.Status != model.AlgorithmTestJobItemStatusRunning {
			t.Fatalf("expected retrying image to stay running, got %+v", item)
		}
		if item.Conclusion != "自动重试中" || !strings.Contains(item.Basis, "统一重试一次") {
			t.Fatalf("expected retry hint in snapshot, got %+v", item)
		}
	}
	if !retryingItemFound {
		t.Fatalf("expected llm-b retry item in snapshot, got %+v", retrySnapshot)
	}

	close(releaseRetry)
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if jobSnapshot.Status != model.AlgorithmTestJobStatusCompleted {
		t.Fatalf("expected completed job after retry, got %+v", jobSnapshot)
	}
	if jobSnapshot.TotalCount != 5 || jobSnapshot.SuccessCount != 5 || jobSnapshot.FailedCount != 0 {
		t.Fatalf("unexpected job counters after retry: %+v", jobSnapshot)
	}
	for _, item := range jobSnapshot.Items {
		if !item.Success || item.Status != model.AlgorithmTestJobItemStatusSuccess {
			t.Fatalf("expected successful final item, got %+v", item)
		}
		if strings.TrimSpace(item.RecordID) == "" {
			t.Fatalf("expected final record_id, got %+v", item)
		}
	}

	attemptsMu.Lock()
	defer attemptsMu.Unlock()
	if attempts["timeout-a.png"] != 2 || attempts["llm-b.png"] != 2 {
		t.Fatalf("expected retryable images to run twice, got %+v", attempts)
	}
	if attempts["ok-c.png"] != 1 || attempts["ok-d.png"] != 1 || attempts["ok-e.png"] != 1 {
		t.Fatalf("expected non-retry images to run once, got %+v", attempts)
	}

	var records []model.AlgorithmTestRecord
	if err := s.db.Where("algorithm_id = ?", algorithm.ID).Find(&records).Error; err != nil {
		t.Fatalf("query records failed: %v", err)
	}
	if len(records) != 5 {
		t.Fatalf("expected 5 final records after retry, got %d", len(records))
	}
	recordCountByFile := make(map[string]int)
	for _, record := range records {
		recordCountByFile[record.OriginalFileName]++
	}
	for _, name := range expectedOrder {
		if recordCountByFile[name] != 1 {
			t.Fatalf("expected single final record for %s, got %+v", name, recordCountByFile)
		}
	}
}

func TestAlgorithmTestMultipartImagesRetryRecoverableFailuresUsesConfiguredRetryCount(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.AnalyzeImageFailureRetryCount = 2
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	var attempts int32
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		attempt := atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "application/json")
		if attempt < 3 {
			_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
				Success: true,
				Message: "ok",
				LLMUsage: &ai.LLMUsage{
					CallStatus:   model.LLMUsageStatusError,
					ErrorMessage: "Connection error.",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success:   true,
			Message:   "ok",
			LLMResult: `{"alarm":"0","conclusion":"正常","reason":"未发现异常","targets":[]}`,
			LLMUsage: &ai.LLMUsage{
				CallStatus: model.LLMUsageStatusSuccess,
			},
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-image-retry-configured-1",
		Name:       "Multipart Image Retry Configured Test",
		Code:       "ALG_TEST_MEDIA_IMAGE_RETRY_CONFIGURED_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-image-retry-configured-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect configured retry image anomaly",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "retry-configured.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image configured retry test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if jobSnapshot.Status != model.AlgorithmTestJobStatusCompleted {
		t.Fatalf("expected completed job after configured retry, got %+v", jobSnapshot)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("expected image to run 3 times with retry_count=2, got %d", attempts)
	}
	if len(jobSnapshot.Items) != 1 || !jobSnapshot.Items[0].Success {
		t.Fatalf("expected successful configured retry item, got %+v", jobSnapshot.Items)
	}

	var recordCount int64
	if err := s.db.Model(&model.AlgorithmTestRecord{}).Where("algorithm_id = ?", algorithm.ID).Count(&recordCount).Error; err != nil {
		t.Fatalf("count algorithm test records failed: %v", err)
	}
	if recordCount != 1 {
		t.Fatalf("expected single final record after configured retry, got=%d", recordCount)
	}
}

func TestAlgorithmTestMultipartImagesRetryRecoverableFailuresCanBeDisabled(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.AnalyzeImageFailureRetryCount = 0
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	var attempts int32
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success: true,
			Message: "ok",
			LLMUsage: &ai.LLMUsage{
				CallStatus:   model.LLMUsageStatusError,
				ErrorMessage: "Connection error.",
			},
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-image-retry-disabled-1",
		Name:       "Multipart Image Retry Disabled Test",
		Code:       "ALG_TEST_MEDIA_IMAGE_RETRY_DISABLED_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-image-retry-disabled-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect disabled retry image anomaly",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "retry-disabled.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image retry disabled test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if jobSnapshot.Status != model.AlgorithmTestJobStatusFailed {
		t.Fatalf("expected failed job when retry disabled, got %+v", jobSnapshot)
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("expected image to run once with retry disabled, got %d", attempts)
	}
	if len(jobSnapshot.Items) != 1 || jobSnapshot.Items[0].Success {
		t.Fatalf("expected failed item when retry disabled, got %+v", jobSnapshot.Items)
	}
}

func TestAlgorithmTestMultipartImagesDoesNotRetryNonRecoverableFailures(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	var attempts int32
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success: true,
			Message: "ok",
			LLMUsage: &ai.LLMUsage{
				CallStatus: model.LLMUsageStatusEmptyContent,
			},
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-image-no-retry-1",
		Name:       "Multipart Image No Retry Test",
		Code:       "ALG_TEST_MEDIA_IMAGE_NO_RETRY_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-image-no-retry-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect non retryable image anomaly",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "empty.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image non-retry test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if jobSnapshot.Status != model.AlgorithmTestJobStatusFailed {
		t.Fatalf("expected failed job, got %+v", jobSnapshot)
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("expected non-retryable failure to run once, got %d", attempts)
	}
	if len(jobSnapshot.Items) != 1 {
		t.Fatalf("expected 1 item, got %+v", jobSnapshot)
	}
	item := jobSnapshot.Items[0]
	if item.Success || item.Status != model.AlgorithmTestJobItemStatusFailed {
		t.Fatalf("expected failed item, got %+v", item)
	}
	if !strings.Contains(item.Basis, "大模型未返回有效结果") {
		t.Fatalf("expected llm empty content basis, got %+v", item)
	}
	if strings.TrimSpace(item.RecordID) == "" {
		t.Fatalf("expected final failed record to be persisted, got %+v", item)
	}
}

func TestAlgorithmTestMultipartMixedMediaOnlyRetriesImages(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	s.cfg.Server.AI.AlgorithmTestVideoFPS = 2
	s.cfg.Server.AI.AlgorithmTestVideoMaxBytes = 100 * 1024 * 1024
	s.cfg.Server.AI.AlgorithmTestVideoMinSeconds = 2
	s.cfg.Server.AI.AlgorithmTestVideoMaxSeconds = 20 * 60

	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	oldProbe := algorithmTestProbeVideoRunner
	t.Cleanup(func() {
		algorithmTestProbeVideoRunner = oldProbe
	})
	algorithmTestProbeVideoRunner = func(videoPath string) (algorithmTestVideoMetadata, error) {
		return algorithmTestVideoMetadata{DurationSeconds: 12}, nil
	}

	var imageRequests int32
	var videoRequests int32
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/analyze_image":
			current := atomic.AddInt32(&imageRequests, 1)
			w.Header().Set("Content-Type", "application/json")
			if current == 1 {
				_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
					Success: true,
					Message: "ok",
					LLMUsage: &ai.LLMUsage{
						CallStatus:   model.LLMUsageStatusError,
						ErrorMessage: "Connection error.",
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
				Success:   true,
				Message:   "ok",
				LLMResult: `{"alarm":"0","conclusion":"正常","reason":"未发现异常","targets":[]}`,
				LLMUsage: &ai.LLMUsage{
					CallStatus: model.LLMUsageStatusSuccess,
				},
			})
		case "/api/analyze_video_test":
			atomic.AddInt32(&videoRequests, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ai.AnalyzeVideoTestResponse{
				Success:   true,
				Message:   "ok",
				LLMResult: `{"alarm":"0","reason":"未发现异常","anomaly_times":[]}`,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-mixed-retry-image-only-1",
		Name:       "Multipart Mixed Retry Image Only Test",
		Code:       "ALG_TEST_MEDIA_MIXED_RETRY_IMAGE_ONLY_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-mixed-retry-image-only-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect mixed media retry image only",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	videoPart, err := writer.CreateFormFile("files", "test.mp4")
	if err != nil {
		t.Fatalf("create video form file failed: %v", err)
	}
	if _, err := videoPart.Write([]byte("fake-video-content")); err != nil {
		t.Fatalf("write video failed: %v", err)
	}
	imagePart, err := writer.CreateFormFile("files", "retry.png")
	if err != nil {
		t.Fatalf("create image form file failed: %v", err)
	}
	if _, err := imagePart.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart mixed retry test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if jobSnapshot.Status != model.AlgorithmTestJobStatusCompleted {
		t.Fatalf("expected completed mixed retry job, got %+v", jobSnapshot)
	}
	if jobSnapshot.TotalCount != 2 || jobSnapshot.SuccessCount != 2 || jobSnapshot.FailedCount != 0 {
		t.Fatalf("unexpected mixed retry counters: %+v", jobSnapshot)
	}
	if atomic.LoadInt32(&imageRequests) != 2 {
		t.Fatalf("expected image request retried once, got %d", imageRequests)
	}
	if atomic.LoadInt32(&videoRequests) != 1 {
		t.Fatalf("expected video request to stay single-run, got %d", videoRequests)
	}
}

func TestAlgorithmTestJobSnapshotKeepsUploadOrderAfterConcurrentCompletion(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	var completionMu sync.Mutex
	completionOrder := make([]string, 0, 3)
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		var in ai.AnalyzeImageRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		switch {
		case strings.Contains(in.ImageRelPath, "slow-a.png"):
			time.Sleep(180 * time.Millisecond)
		case strings.Contains(in.ImageRelPath, "mid-b.png"):
			time.Sleep(90 * time.Millisecond)
		default:
			time.Sleep(20 * time.Millisecond)
		}
		completionMu.Lock()
		switch {
		case strings.Contains(in.ImageRelPath, "slow-a.png"):
			completionOrder = append(completionOrder, "slow-a.png")
		case strings.Contains(in.ImageRelPath, "mid-b.png"):
			completionOrder = append(completionOrder, "mid-b.png")
		default:
			completionOrder = append(completionOrder, "fast-c.png")
		}
		completionMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success: true,
			Message: "ok",
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:              "alg-test-media-image-order-1",
		Name:            "Multipart Image Order Test",
		Code:            "ALG_TEST_MEDIA_IMG_ORDER_1",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	expectedOrder := []string{"slow-a.png", "mid-b.png", "fast-c.png"}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range expectedOrder {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create form file failed: %v", err)
		}
		if _, err := part.Write(imageBytes); err != nil {
			t.Fatalf("write image failed: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image order test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if jobSnapshot.Status != model.AlgorithmTestJobStatusCompleted {
		t.Fatalf("expected completed order job, got %+v", jobSnapshot)
	}
	if jobSnapshot.TotalCount != 3 || jobSnapshot.SuccessCount != 3 || jobSnapshot.FailedCount != 0 {
		t.Fatalf("unexpected order job counters: %+v", jobSnapshot)
	}
	if len(jobSnapshot.Items) != len(expectedOrder) {
		t.Fatalf("expected %d job items, got %+v", len(expectedOrder), jobSnapshot)
	}
	for idx, item := range jobSnapshot.Items {
		if item.SortOrder != idx {
			t.Fatalf("expected sort_order=%d, got %+v", idx, item)
		}
		if item.FileName != expectedOrder[idx] {
			t.Fatalf("expected file_name=%s, got %+v", expectedOrder[idx], item)
		}
		if !item.Success || item.Status != model.AlgorithmTestJobItemStatusSuccess {
			t.Fatalf("expected success item, got %+v", item)
		}
	}

	completionMu.Lock()
	defer completionMu.Unlock()
	if strings.Join(completionOrder, ",") == strings.Join(expectedOrder, ",") {
		t.Fatalf("expected concurrent completion order to differ from upload order, got=%v", completionOrder)
	}
}

func TestAlgorithmDraftTestDoesNotPersistTestRecord(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success:   true,
			Message:   "ok",
			LLMResult: `{"alarm":"0","conclusion":"正常","reason":"未发现异常","targets":[]}`,
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "draft.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	_ = writer.WriteField("name", "草稿算法")
	_ = writer.WriteField("description", "仅用于页面测试")
	_ = writer.WriteField("prompt", "判断是否存在异常情况")
	_ = writer.WriteField("detect_mode", "2")
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/draft-test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("draft test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp draftAlgorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode draft test response failed: %v", err)
	}
	if resp.Code != 0 || strings.TrimSpace(resp.Data.JobID) == "" {
		t.Fatalf("unexpected draft test response: %s", rec.Body.String())
	}

	snapshot := waitDraftAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if snapshot.TotalCount != 1 || len(snapshot.Items) != 1 {
		t.Fatalf("unexpected draft snapshot: %+v", snapshot)
	}

	var recordCount int64
	if err := s.db.Model(&model.AlgorithmTestRecord{}).Count(&recordCount).Error; err != nil {
		t.Fatalf("count algorithm test records failed: %v", err)
	}
	if recordCount != 0 {
		t.Fatalf("draft test should not persist algorithm test records, got=%d", recordCount)
	}
}

func TestAlgorithmDraftTestRetriesRecoverableImageFailures(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.AnalyzeImageFailureRetryCount = 2
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	var attempts int32
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		attempt := atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "application/json")
		if attempt < 3 {
			_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
				Success: true,
				Message: "ok",
				LLMUsage: &ai.LLMUsage{
					CallStatus:   model.LLMUsageStatusError,
					ErrorMessage: "Connection error.",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success:   true,
			Message:   "ok",
			LLMResult: `{"alarm":"0","conclusion":"正常","reason":"未发现异常","targets":[]}`,
			LLMUsage: &ai.LLMUsage{
				CallStatus: model.LLMUsageStatusSuccess,
			},
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "draft-retry.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	_ = writer.WriteField("name", "草稿算法重试")
	_ = writer.WriteField("prompt", "判断是否存在异常情况")
	_ = writer.WriteField("detect_mode", "2")
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/draft-test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("draft retry test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp draftAlgorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode draft retry response failed: %v", err)
	}
	snapshot := waitDraftAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if snapshot.Status != model.AlgorithmTestJobStatusCompleted {
		t.Fatalf("expected completed draft retry job, got %+v", snapshot)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("expected draft retry image to run 3 times, got %d", attempts)
	}
	if len(snapshot.Items) != 1 || !snapshot.Items[0].Success {
		t.Fatalf("expected successful draft retry item, got %+v", snapshot.Items)
	}

	var recordCount int64
	if err := s.db.Model(&model.AlgorithmTestRecord{}).Count(&recordCount).Error; err != nil {
		t.Fatalf("count algorithm test records failed: %v", err)
	}
	if recordCount != 0 {
		t.Fatalf("draft retry should not persist algorithm test records, got=%d", recordCount)
	}
}

func TestAlgorithmTestMultipartRejectsTooManyImages(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.AlgorithmTestImageMaxCount = 1
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	algorithm := model.Algorithm{
		ID:          "alg-test-media-image-limit",
		Name:        "Multipart Image Limit Test",
		Code:        "ALG_TEST_MEDIA_IMG_LIMIT",
		Mode:        model.AlgorithmModeLarge,
		DetectMode:  model.AlgorithmDetectModeLLMOnly,
		Enabled:     true,
		Description: "limit test",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "prompt-img-limit",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "判断是否存在异常",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range []string{"a.png", "b.png"} {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create form file failed: %v", err)
		}
		if _, err := part.Write(imageBytes); err != nil {
			t.Fatalf("write image failed: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for too many images: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "测试图片最多上传 1 张") {
		t.Fatalf("unexpected too many images body: %s", rec.Body.String())
	}
}

func TestAlgorithmTestImageRetriesTransientAIError(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	attempts := 0
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		attempts++
		if attempts < 3 {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijacker unavailable", http.StatusInternalServerError)
				return
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				http.Error(w, "hijack failed", http.StatusInternalServerError)
				return
			}
			_ = conn.Close()
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

	algorithm := model.Algorithm{
		ID:              "alg-test-media-image-retry-1",
		Name:            "Multipart Image Retry Test",
		Code:            "ALG_TEST_MEDIA_IMG_RETRY_1",
		Mode:            model.AlgorithmModeSmall,
		DetectMode:      model.AlgorithmDetectModeSmallOnly,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "retry.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image retry test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if len(jobSnapshot.Items) != 1 {
		t.Fatalf("expected 1 job item, got %+v", jobSnapshot)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 AI attempts for transient failure, got %d", attempts)
	}
	item := jobSnapshot.Items[0]
	if !item.Success {
		t.Fatalf("expected retry item success, got %+v", item)
	}
}

func TestAlgorithmTestMultipartImageUsesSingleTaskPrompt(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	capturedPrompt := ""
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
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
			Success: true,
			Message: "ok",
			LLMResult: `{
				"alarm":"1",
				"result":"detected crowding",
				"reason":"center area contains multiple people",
				"targets":[
					{"label":"person","confidence":0.95,"bbox2d":[100,120,300,520]}
				]
			}`,
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-image-llm-1",
		Name:       "Multipart Image LLM Test",
		Code:       "ALG_TEST_MEDIA_IMG_LLM_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-image-llm-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect crowding",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "test.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image llm test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	promptDeadline := time.Now().Add(2 * time.Second)
	for capturedPrompt == "" && time.Now().Before(promptDeadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if strings.Contains(capturedPrompt, "## [浠诲姟娓呭崟]") {
		t.Fatalf("expected image test prompt without task list, got=%s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "detect crowding") {
		t.Fatalf("expected image test prompt to include active prompt, got=%s", capturedPrompt)
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp.Code != 0 || strings.TrimSpace(resp.Data.JobID) == "" {
		t.Fatalf("unexpected multipart image llm response: %s", rec.Body.String())
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if len(jobSnapshot.Items) != 1 {
		t.Fatalf("expected 1 job item, got %+v", jobSnapshot)
	}
	item := jobSnapshot.Items[0]
	if !item.Success {
		t.Fatalf("expected llm item success, got %+v", item)
	}
	if item.Conclusion != "detected crowding" {
		t.Fatalf("expected llm conclusion, got %+v", item)
	}
	if item.Basis != "center area contains multiple people" {
		t.Fatalf("expected llm basis, got %+v", item)
	}
	if len(item.NormalizedBoxes) != 1 || item.NormalizedBoxes[0].Label != "person" {
		t.Fatalf("expected normalized boxes from llm targets, got %+v", item)
	}
	if strings.TrimSpace(item.ErrorMessage) != "" {
		t.Fatalf("expected success item without error message, got %+v", item)
	}
	var jobItem model.AlgorithmTestJobItem
	if err := s.db.First(&jobItem, "id = ?", item.JobItemID).Error; err != nil {
		t.Fatalf("query job item failed: %v", err)
	}
	if strings.TrimSpace(jobItem.ErrorMessage) != "" {
		t.Fatalf("expected stored success job item without error message, got %+v", jobItem)
	}
}

func TestAlgorithmTestMultipartImageModes2And3UseOnlyLLMBoxes(t *testing.T) {
	testCases := []struct {
		name       string
		detectMode int
	}{
		{name: "mode2", detectMode: model.AlgorithmDetectModeLLMOnly},
		{name: "mode3", detectMode: model.AlgorithmDetectModeHybrid},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := newFocusedTestServer(t)
			engine := s.Engine()
			imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
			imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
			if err != nil {
				t.Fatalf("decode image failed: %v", err)
			}

			mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/analyze_image" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
					Success: true,
					Message: "ok",
					LLMResult: `{
						"alarm":"1",
                        "conclusion":"detected crowding",
                        "reason":"llm determined crowding is present",
						"targets":[]
					}`,
					Detections: json.RawMessage(`[
						{"label":"person","confidence":0.91,"box":{"x_min":100,"y_min":120,"x_max":360,"y_max":640}}
					]`),
				})
			}))
			defer mockAI.Close()
			s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

			algorithm := model.Algorithm{
				ID:         "alg-test-media-image-only-llm-" + tc.name,
				Name:       "Image Only LLM Boxes " + tc.name,
				Code:       "ALG_TEST_IMG_LLM_" + strings.ToUpper(tc.name),
				Mode:       model.AlgorithmModeHybrid,
				DetectMode: tc.detectMode,
				Enabled:    true,
			}
			if tc.detectMode == model.AlgorithmDetectModeHybrid {
				algorithm.SmallModelLabel = "person"
			}
			if err := s.db.Create(&algorithm).Error; err != nil {
				t.Fatalf("create algorithm failed: %v", err)
			}
			if err := s.db.Create(&model.AlgorithmPromptVersion{
				ID:          "alg-test-media-image-only-llm-prompt-" + tc.name,
				AlgorithmID: algorithm.ID,
				Version:     "v1",
				Prompt:      "detect crowding",
				IsActive:    true,
			}).Error; err != nil {
				t.Fatalf("create active prompt failed: %v", err)
			}

			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("files", "test.png")
			if err != nil {
				t.Fatalf("create form file failed: %v", err)
			}
			if _, err := part.Write(imageBytes); err != nil {
				t.Fatalf("write image failed: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("close writer failed: %v", err)
			}

			token := loginToken(t, engine, "admin", "admin")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("multipart image test failed: status=%d body=%s", rec.Code, rec.Body.String())
			}

			var resp algorithmTestJobCreateResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response failed: %v", err)
			}
			if resp.Code != 0 || strings.TrimSpace(resp.Data.JobID) == "" {
				t.Fatalf("unexpected multipart image response: %s", rec.Body.String())
			}
			jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
			if len(jobSnapshot.Items) != 1 {
				t.Fatalf("expected 1 job item, got %+v", jobSnapshot)
			}
			if len(jobSnapshot.Items[0].NormalizedBoxes) != 0 {
				t.Fatalf("expected no fallback boxes for detect_mode=%d, got %+v", tc.detectMode, jobSnapshot.Items[0].NormalizedBoxes)
			}

			historyReq := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/"+algorithm.ID+"/tests?page=1&page_size=10", nil)
			historyReq.Header.Set("Authorization", "Bearer "+token)
			historyRec := httptest.NewRecorder()
			engine.ServeHTTP(historyRec, historyReq)
			if historyRec.Code != http.StatusOK {
				t.Fatalf("list tests failed: status=%d body=%s", historyRec.Code, historyRec.Body.String())
			}

			var historyResp struct {
				Code int `json:"code"`
				Data struct {
					Items []struct {
						NormalizedBoxes []normalizedBox `json:"normalized_boxes"`
					} `json:"items"`
				} `json:"data"`
			}
			if err := json.Unmarshal(historyRec.Body.Bytes(), &historyResp); err != nil {
				t.Fatalf("decode history response failed: %v", err)
			}
			if historyResp.Code != 0 || len(historyResp.Data.Items) != 1 {
				t.Fatalf("unexpected history response: %s", historyRec.Body.String())
			}
			if len(historyResp.Data.Items[0].NormalizedBoxes) != 0 {
				t.Fatalf("expected history boxes to stay empty for detect_mode=%d, got %+v", tc.detectMode, historyResp.Data.Items[0].NormalizedBoxes)
			}
		})
	}
}

func TestAlgorithmTestMultipartHybridGateMissUsesChineseBasis(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success:    true,
			Message:    "ok",
			LLMResult:  "",
			Detections: json.RawMessage(`[]`),
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:              "alg-test-media-image-hybrid-gate-miss-1",
		Name:            "Hybrid Gate Miss Test",
		Code:            "ALG_TEST_IMG_HYBRID_GATE_MISS_1",
		Mode:            model.AlgorithmModeHybrid,
		DetectMode:      model.AlgorithmDetectModeHybrid,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-image-hybrid-gate-miss-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect crowding",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "test.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp.Code != 0 || strings.TrimSpace(resp.Data.JobID) == "" {
		t.Fatalf("unexpected multipart image response: %s", rec.Body.String())
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if len(jobSnapshot.Items) != 1 {
		t.Fatalf("expected 1 job item, got %+v", jobSnapshot)
	}
	if !jobSnapshot.Items[0].Success {
		t.Fatalf("expected hybrid gate miss item success, got %+v", jobSnapshot.Items[0])
	}
	if jobSnapshot.Items[0].Basis != "小模型未检出目标" {
		t.Fatalf("expected hybrid gate miss basis, got %+v", jobSnapshot.Items[0])
	}
	historyReq := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/"+algorithm.ID+"/tests?page=1&page_size=10", nil)
	historyReq.Header.Set("Authorization", "Bearer "+token)
	historyRec := httptest.NewRecorder()
	engine.ServeHTTP(historyRec, historyReq)
	if historyRec.Code != http.StatusOK {
		t.Fatalf("list tests failed: status=%d body=%s", historyRec.Code, historyRec.Body.String())
	}

	var historyResp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				Basis string `json:"basis"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(historyRec.Body.Bytes(), &historyResp); err != nil {
		t.Fatalf("decode history response failed: %v", err)
	}
	if historyResp.Code != 0 || len(historyResp.Data.Items) != 1 {
		t.Fatalf("unexpected history response: %s", historyRec.Body.String())
	}
	if historyResp.Data.Items[0].Basis != "小模型未检出目标" {
		t.Fatalf("expected history basis to avoid ok, got %+v", historyResp.Data.Items[0])
	}
}

func TestAlgorithmTestMultipartImageLLMFailureBecomesFailed(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success:    true,
			Message:    "ok",
			LLMResult:  "",
			Detections: json.RawMessage(`[{"label":"person","confidence":0.9}]`),
			LLMUsage: &ai.LLMUsage{
				CallStatus:   "error",
				ErrorMessage: "Connection error.",
			},
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:              "alg-test-media-image-llm-failure-1",
		Name:            "Image LLM Failure Test",
		Code:            "ALG_TEST_MEDIA_IMG_LLM_FAIL_1",
		Mode:            model.AlgorithmModeHybrid,
		DetectMode:      model.AlgorithmDetectModeHybrid,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-image-llm-failure-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect crowding",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "test.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image llm failure test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if len(jobSnapshot.Items) != 1 {
		t.Fatalf("expected 1 job item, got %+v", jobSnapshot)
	}
	item := jobSnapshot.Items[0]
	if item.Success || item.Status != model.AlgorithmTestJobItemStatusFailed {
		t.Fatalf("expected llm failure item to fail, got %+v", item)
	}
	if item.Conclusion != "大模型判定失败" {
		t.Fatalf("expected llm failure conclusion, got %+v", item)
	}
	if item.Basis != "大模型调用失败，未能完成最终判定" {
		t.Fatalf("expected llm failure basis, got %+v", item)
	}
	if item.ErrorMessage != "Connection error." {
		t.Fatalf("expected llm failure error message, got %+v", item)
	}
	if strings.Contains(item.Conclusion, "person") || strings.Contains(item.Basis, "person") {
		t.Fatalf("expected llm failure without yolo fallback summary, got %+v", item)
	}

	var jobItem model.AlgorithmTestJobItem
	if err := s.db.First(&jobItem, "id = ?", item.JobItemID).Error; err != nil {
		t.Fatalf("query job item failed: %v", err)
	}
	if jobItem.Success || jobItem.ErrorMessage != "Connection error." {
		t.Fatalf("expected stored failed job item with raw error, got %+v", jobItem)
	}

	var record model.AlgorithmTestRecord
	if err := s.db.First(&record, "id = ?", item.RecordID).Error; err != nil {
		t.Fatalf("query record failed: %v", err)
	}
	if record.Success {
		t.Fatalf("expected stored record success=false, got %+v", record)
	}
}

func TestAlgorithmTestMultipartImageLLMOnlyEmptyContentBecomesFailed(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	imageBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+2ZkAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatalf("decode image failed: %v", err)
	}

	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_image" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeImageResponse{
			Success:   true,
			Message:   "ok",
			LLMResult: "",
			LLMUsage: &ai.LLMUsage{
				CallStatus:   "empty_content",
				ErrorMessage: "LLM API returned empty message.content",
			},
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-image-llm-empty-1",
		Name:       "Image LLM Empty Content Test",
		Code:       "ALG_TEST_MEDIA_IMG_LLM_EMPTY_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-image-llm-empty-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect crowding",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "test.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatalf("write image failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart image llm empty test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if len(jobSnapshot.Items) != 1 {
		t.Fatalf("expected 1 job item, got %+v", jobSnapshot)
	}
	item := jobSnapshot.Items[0]
	if item.Success || item.Status != model.AlgorithmTestJobItemStatusFailed {
		t.Fatalf("expected llm empty item to fail, got %+v", item)
	}
	if item.Conclusion != "大模型判定失败" || item.Basis != "大模型未返回有效结果" {
		t.Fatalf("expected empty content failure semantics, got %+v", item)
	}
	if item.ErrorMessage != "LLM API returned empty message.content" {
		t.Fatalf("expected raw empty content error message, got %+v", item)
	}
}

func TestAlgorithmTestHistoryCoercesLegacyImageLLMFailure(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	algorithm := model.Algorithm{
		ID:              "alg-test-media-history-llm-failure-1",
		Name:            "History LLM Failure Test",
		Code:            "ALG_TEST_MEDIA_HISTORY_LLM_FAIL_1",
		Mode:            model.AlgorithmModeHybrid,
		DetectMode:      model.AlgorithmDetectModeHybrid,
		Enabled:         true,
		SmallModelLabel: "person",
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	responsePayload := `{
		"success": true,
		"message": "ok",
		"detections": [{"label":"person","confidence":0.9}],
		"llm_result": "",
		"llm_usage": {
			"call_status": "error",
			"error_message": "Connection error."
		}
	}`
	record := model.AlgorithmTestRecord{
		ID:               "alg-test-media-history-record-1",
		AlgorithmID:      algorithm.ID,
		BatchID:          "batch-history-1",
		MediaType:        "image",
		MediaPath:        "20260327/batch-history-1/test.png",
		ImagePath:        "20260327/batch-history-1/test.png",
		OriginalFileName: "test.png",
		ResponsePayload:  responsePayload,
		Success:          true,
	}
	if err := s.db.Create(&record).Error; err != nil {
		t.Fatalf("create record failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	historyReq := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/"+algorithm.ID+"/tests?page=1&page_size=10", nil)
	historyReq.Header.Set("Authorization", "Bearer "+token)
	historyRec := httptest.NewRecorder()
	engine.ServeHTTP(historyRec, historyReq)
	if historyRec.Code != http.StatusOK {
		t.Fatalf("list tests failed: status=%d body=%s", historyRec.Code, historyRec.Body.String())
	}

	var historyResp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				Success    bool   `json:"success"`
				Conclusion string `json:"conclusion"`
				Basis      string `json:"basis"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(historyRec.Body.Bytes(), &historyResp); err != nil {
		t.Fatalf("decode history response failed: %v", err)
	}
	if historyResp.Code != 0 || len(historyResp.Data.Items) != 1 {
		t.Fatalf("unexpected history response: %s", historyRec.Body.String())
	}
	item := historyResp.Data.Items[0]
	if item.Success {
		t.Fatalf("expected legacy history item coerced to failed, got %+v", item)
	}
	if item.Conclusion != "大模型判定失败" || item.Basis != "大模型调用失败，未能完成最终判定" {
		t.Fatalf("expected legacy history llm failure semantics, got %+v", item)
	}
	if strings.Contains(item.Conclusion, "person") || strings.Contains(item.Basis, "person") {
		t.Fatalf("expected legacy history without yolo fallback summary, got %+v", item)
	}
}

func TestAlgorithmTestMultipartVideoIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	s.cfg.Server.AI.AlgorithmTestVideoFPS = 2
	s.cfg.Server.AI.AlgorithmTestVideoMaxBytes = 100 * 1024 * 1024
	s.cfg.Server.AI.AlgorithmTestVideoMinSeconds = 2
	s.cfg.Server.AI.AlgorithmTestVideoMaxSeconds = 20 * 60

	oldProbe := algorithmTestProbeVideoRunner
	t.Cleanup(func() {
		algorithmTestProbeVideoRunner = oldProbe
	})
	algorithmTestProbeVideoRunner = func(videoPath string) (algorithmTestVideoMetadata, error) {
		return algorithmTestVideoMetadata{DurationSeconds: 12}, nil
	}

	capturedFPS := 0
	capturedPrompt := ""
	capturedVideoRelPath := ""
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_video_test" {
			http.NotFound(w, r)
			return
		}
		var in ai.AnalyzeVideoTestRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		capturedFPS = in.FPS
		capturedPrompt = in.LLMPrompt
		capturedVideoRelPath = in.VideoRelPath
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.AnalyzeVideoTestResponse{
			Success: true,
			Message: "ok",
			LLMResult: `{
				"alarm":"1",
				"reason":"abnormal target appears at 00:05",
				"anomaly_times":[
					{"timestamp_ms":5000,"timestamp_text":"00:05","reason":"abnormal target appears"}
				]
			}`,
		})
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-video-1",
		Name:       "Multipart Video Test",
		Code:       "ALG_TEST_MEDIA_VIDEO_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-video-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect video anomaly",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "test.mp4")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write([]byte("fake-video-content")); err != nil {
		t.Fatalf("write video failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart video test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	videoDeadline := time.Now().Add(2 * time.Second)
	for (capturedFPS == 0 || strings.TrimSpace(capturedVideoRelPath) == "" || strings.TrimSpace(capturedPrompt) == "") && time.Now().Before(videoDeadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(capturedPrompt, "时间顺序") || strings.Contains(capturedPrompt, "抽帧") {
		t.Fatalf("expected clean video prompt, got=%s", capturedPrompt)
	}
	if capturedFPS != 2 {
		t.Fatalf("expected fps=2 from config, got %d", capturedFPS)
	}
	if strings.TrimSpace(capturedVideoRelPath) == "" {
		t.Fatalf("expected non-empty video_rel_path")
	}
	if strings.Contains(capturedPrompt, "## [浠诲姟娓呭崟]") {
		t.Fatalf("expected video test prompt without task list, got=%s", capturedPrompt)
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp.Code != 0 || strings.TrimSpace(resp.Data.JobID) == "" {
		t.Fatalf("unexpected multipart video response: %s", rec.Body.String())
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if len(jobSnapshot.Items) != 1 {
		t.Fatalf("expected 1 job item, got %+v", jobSnapshot)
	}
	item := jobSnapshot.Items[0]
	if item.MediaType != string(algorithmTestMediaTypeVideo) {
		t.Fatalf("expected video media type, got %+v", item)
	}
	if !item.Success {
		t.Fatalf("expected successful video item, got %+v", item)
	}
	if len(item.AnomalyTimes) != 1 || item.AnomalyTimes[0].TimestampText != "00:05" {
		t.Fatalf("expected anomaly_times in response, got %+v", item)
	}

	var record model.AlgorithmTestRecord
	if err := s.db.Where("algorithm_id = ?", algorithm.ID).First(&record).Error; err != nil {
		t.Fatalf("query video record failed: %v", err)
	}
	if record.MediaType != string(algorithmTestMediaTypeVideo) {
		t.Fatalf("expected video record, got %+v", record)
	}
	var persisted map[string]any
	if err := json.Unmarshal([]byte(record.ResponsePayload), &persisted); err != nil {
		t.Fatalf("decode persisted response payload failed: %v payload=%s", err, record.ResponsePayload)
	}
	if _, ok := persisted["llm_result"]; !ok {
		t.Fatalf("expected llm_result in persisted response payload, got=%s", record.ResponsePayload)
	}
	if _, ok := persisted["basis"]; ok {
		t.Fatalf("expected basis removed from persisted response payload, got=%s", record.ResponsePayload)
	}
	if _, ok := persisted["conclusion"]; ok {
		t.Fatalf("expected conclusion removed from persisted response payload, got=%s", record.ResponsePayload)
	}
	if _, ok := persisted["anomaly_times"]; ok {
		t.Fatalf("expected anomaly_times removed from persisted response payload, got=%s", record.ResponsePayload)
	}
}

func TestAlgorithmTestVideoDoesNotRetryStatusError(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	s.cfg.Server.AI.AlgorithmTestVideoFPS = 2
	s.cfg.Server.AI.AlgorithmTestVideoMaxBytes = 100 * 1024 * 1024
	s.cfg.Server.AI.AlgorithmTestVideoMinSeconds = 2
	s.cfg.Server.AI.AlgorithmTestVideoMaxSeconds = 20 * 60

	oldProbe := algorithmTestProbeVideoRunner
	t.Cleanup(func() {
		algorithmTestProbeVideoRunner = oldProbe
	})
	algorithmTestProbeVideoRunner = func(videoPath string) (algorithmTestVideoMetadata, error) {
		return algorithmTestVideoMetadata{DurationSeconds: 12}, nil
	}

	attempts := 0
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyze_video_test" {
			http.NotFound(w, r)
			return
		}
		attempts++
		http.Error(w, "upstream status error", http.StatusBadGateway)
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-video-status-1",
		Name:       "Multipart Video Status Error Test",
		Code:       "ALG_TEST_MEDIA_VIDEO_STATUS_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-video-status-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect video anomaly",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "status.mp4")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write([]byte("fake-video-content")); err != nil {
		t.Fatalf("write video failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart video status test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if len(jobSnapshot.Items) != 1 {
		t.Fatalf("expected 1 job item, got %+v", jobSnapshot)
	}
	if attempts != 1 {
		t.Fatalf("expected status error to avoid retry, got %d attempts", attempts)
	}
	item := jobSnapshot.Items[0]
	if item.Success {
		t.Fatalf("expected failed item for status error, got %+v", item)
	}
	if !strings.Contains(item.Basis, "AI 视频分析失败") {
		t.Fatalf("expected video failure basis, got %+v", item)
	}
}

func TestAlgorithmTestMultipartVideoRejectsOversizedFile(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	s.cfg.Server.AI.AlgorithmTestVideoMaxBytes = 8
	s.cfg.Server.AI.AlgorithmTestVideoMinSeconds = 2
	s.cfg.Server.AI.AlgorithmTestVideoMaxSeconds = 20 * 60

	aiCalls := 0
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aiCalls++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	algorithm := model.Algorithm{
		ID:         "alg-test-media-video-size-1",
		Name:       "Multipart Video Size Limit Test",
		Code:       "ALG_TEST_MEDIA_VIDEO_SIZE_1",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-video-size-prompt-1",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect video anomaly",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "test.mp4")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write([]byte("fake-video-content")); err != nil {
		t.Fatalf("write video failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart video test failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp algorithmTestJobCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
	if len(jobSnapshot.Items) != 1 {
		t.Fatalf("expected 1 job item, got %+v", jobSnapshot)
	}
	item := jobSnapshot.Items[0]
	if item.Success {
		t.Fatalf("expected failed item for oversized video, got %+v", item)
	}
	if !strings.Contains(item.Basis, formatAlgorithmTestVideoMaxSize(s.cfg.Server.AI.AlgorithmTestVideoMaxBytes)) {
		t.Fatalf("expected max size basis, got %+v", item)
	}
	if aiCalls != 0 {
		t.Fatalf("expected AI not to be called, got %d", aiCalls)
	}
}

func TestAlgorithmTestMultipartVideoRejectsDurationOutOfRange(t *testing.T) {
	testCases := []struct {
		name     string
		duration float64
	}{
		{name: "too_short", duration: 1},
		{name: "too_long", duration: 1201},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := newFocusedTestServer(t)
			engine := s.Engine()
			s.cfg.Server.AI.AlgorithmTestVideoMaxBytes = 100 * 1024 * 1024
			s.cfg.Server.AI.AlgorithmTestVideoMinSeconds = 2
			s.cfg.Server.AI.AlgorithmTestVideoMaxSeconds = 20 * 60

			oldProbe := algorithmTestProbeVideoRunner
			t.Cleanup(func() {
				algorithmTestProbeVideoRunner = oldProbe
			})
			algorithmTestProbeVideoRunner = func(videoPath string) (algorithmTestVideoMetadata, error) {
				return algorithmTestVideoMetadata{DurationSeconds: tc.duration}, nil
			}

			aiCalls := 0
			mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				aiCalls++
				http.Error(w, "should not be called", http.StatusInternalServerError)
			}))
			defer mockAI.Close()
			s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

			algorithm := model.Algorithm{
				ID:         "alg-test-media-video-duration-" + tc.name,
				Name:       "Multipart Video Duration Limit Test " + tc.name,
				Code:       "ALG_TEST_MEDIA_VIDEO_DURATION_" + strings.ToUpper(tc.name),
				Mode:       model.AlgorithmModeLarge,
				DetectMode: model.AlgorithmDetectModeLLMOnly,
				Enabled:    true,
			}
			if err := s.db.Create(&algorithm).Error; err != nil {
				t.Fatalf("create algorithm failed: %v", err)
			}
			if err := s.db.Create(&model.AlgorithmPromptVersion{
				ID:          "alg-test-media-video-duration-prompt-" + tc.name,
				AlgorithmID: algorithm.ID,
				Version:     "v1",
				Prompt:      "detect video anomaly",
				IsActive:    true,
			}).Error; err != nil {
				t.Fatalf("create active prompt failed: %v", err)
			}

			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("files", "test.mp4")
			if err != nil {
				t.Fatalf("create form file failed: %v", err)
			}
			if _, err := part.Write([]byte("fake-video-content")); err != nil {
				t.Fatalf("write video failed: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("close writer failed: %v", err)
			}

			token := loginToken(t, engine, "admin", "admin")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("multipart video test failed: status=%d body=%s", rec.Code, rec.Body.String())
			}

			var resp algorithmTestJobCreateResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response failed: %v", err)
			}
			jobSnapshot := waitAlgorithmTestJob(t, engine, token, resp.Data.JobID)
			if len(jobSnapshot.Items) != 1 {
				t.Fatalf("expected 1 job item, got %+v", jobSnapshot)
			}
			item := jobSnapshot.Items[0]
			if item.Success {
				t.Fatalf("expected failed item for invalid duration, got %+v", item)
			}
			if !strings.Contains(item.Basis, "2 秒到 20 分钟") {
				t.Fatalf("expected duration range basis, got %+v", item)
			}
			if aiCalls != 0 {
				t.Fatalf("expected AI not to be called, got %d", aiCalls)
			}
		})
	}
}

func TestAlgorithmTestMultipartImageBlockedWhenLLMTokenLimitReached(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	s.cfg.Server.AI.DisableOnTokenLimitExceeded = true
	s.cfg.Server.AI.TotalTokenLimit = 100

	algorithm := model.Algorithm{
		ID:         "alg-test-media-image-quota-blocked",
		Name:       "LLM 配额拦截测试",
		Code:       "ALG_TEST_MEDIA_IMAGE_QUOTA_BLOCKED",
		Mode:       model.AlgorithmModeLarge,
		DetectMode: model.AlgorithmDetectModeLLMOnly,
		Enabled:    true,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	if err := s.db.Create(&model.AlgorithmPromptVersion{
		ID:          "alg-test-media-image-quota-blocked-prompt",
		AlgorithmID: algorithm.ID,
		Version:     "v1",
		Prompt:      "detect quota blocked",
		IsActive:    true,
	}).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}
	usage := makeLLMUsageCall("llm-test-media-image-quota-blocked", time.Now(), model.LLMUsageSourceTaskRuntime, 100)
	if err := s.db.Create(&usage).Error; err != nil {
		t.Fatalf("create llm usage failed: %v", err)
	}

	aiCalls := 0
	mockAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aiCalls++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer mockAI.Close()
	s.aiClient = ai.NewClient(mockAI.URL, 5*time.Second)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "test.png")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write([]byte("fake-image-content")); err != nil {
		t.Fatalf("write test image failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algorithm.ID+"/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status=400 when llm quota reached, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), llmTokenLimitExceededMessage) {
		t.Fatalf("expected quota exceeded message, got body=%s", rec.Body.String())
	}
	if aiCalls != 0 {
		t.Fatalf("expected AI not to be called, got %d", aiCalls)
	}
}
