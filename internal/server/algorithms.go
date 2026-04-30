package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
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
	"maas-box/internal/logutil"
	"maas-box/internal/model"
)

type algorithmUpsertRequest struct {
	Code              string     `json:"code"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	ImageURL          string     `json:"image_url"`
	Scene             string     `json:"scene"`
	Category          string     `json:"category"`
	Mode              string     `json:"mode"`
	Enabled           bool       `json:"enabled"`
	SmallModelLabel   stringList `json:"small_model_label"`
	DetectMode        int        `json:"detect_mode"`
	ModelProviderID   string     `json:"model_provider_id"`
	YoloThreshold     float64    `json:"yolo_threshold"`
	IOUThreshold      float64    `json:"iou_threshold"`
	LabelsTriggerMode string     `json:"labels_trigger_mode"`
	Prompt            string     `json:"prompt"`
	PromptVersion     string     `json:"prompt_version"`
	ActivatePrompt    *bool      `json:"activate_prompt"`
}

type algorithmImportItemError struct {
	Index   int    `json:"index"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type stringList []string

func (s *stringList) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*s = nil
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var items []string
		if err := json.Unmarshal(data, &items); err != nil {
			return err
		}
		*s = items
		return nil
	}
	var item string
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	*s = []string{item}
	return nil
}

type promptUpsertRequest struct {
	Version  string `json:"version"`
	Prompt   string `json:"prompt"`
	IsActive bool   `json:"is_active"`
}

type modelProviderUpsertRequest struct {
	Name    string `json:"name"`
	APIURL  string `json:"api_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	Enabled bool   `json:"enabled"`
}

type yoloLabelItem struct {
	Label string `json:"label"`
	Name  string `json:"name"`
}

type testAlgorithmRequest struct {
	CameraID    string `json:"camera_id"`
	ImageBase64 string `json:"image_base64"`
}

type algorithmTestMediaType string

const (
	algorithmTestMediaTypeImage algorithmTestMediaType = "image"
	algorithmTestMediaTypeVideo algorithmTestMediaType = "video"
)

type algorithmTestItemResult struct {
	JobItemID       string                    `json:"job_item_id,omitempty"`
	SortOrder       int                       `json:"sort_order,omitempty"`
	Status          string                    `json:"status,omitempty"`
	RecordID        string                    `json:"record_id"`
	FileName        string                    `json:"file_name"`
	MediaType       string                    `json:"media_type"`
	Success         bool                      `json:"success"`
	Conclusion      string                    `json:"conclusion"`
	Basis           string                    `json:"basis"`
	MediaURL        string                    `json:"media_url"`
	NormalizedBoxes []normalizedBox           `json:"normalized_boxes,omitempty"`
	AnomalyTimes    []ai.SequenceAnomalyTime  `json:"anomaly_times,omitempty"`
	DurationSeconds float64                   `json:"duration_seconds,omitempty"`
	ErrorMessage    string                    `json:"error_message,omitempty"`
	Record          model.AlgorithmTestRecord `json:"record"`
	LLMCallStatus   string                    `json:"-"`
	AIRequestType   string                    `json:"-"`
	RequestPayload  string                    `json:"-"`
	ResponsePayload string                    `json:"-"`
}

type algorithmTestVideoMetadata struct {
	DurationSeconds float64 `json:"duration_seconds"`
}

type aiTestDetection struct {
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
	NormBox    struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		W float64 `json:"w"`
		H float64 `json:"h"`
	} `json:"norm_box"`
	Box struct {
		XMin int `json:"x_min"`
		YMin int `json:"y_min"`
		XMax int `json:"x_max"`
		YMax int `json:"y_max"`
	} `json:"box"`
}

type algorithmTestImageLLMTarget struct {
	Label      string    `json:"label"`
	Confidence float64   `json:"confidence"`
	BBox2D     []float64 `json:"bbox2d"`
}

type algorithmTestImageLLMPayload struct {
	Alarm      string                        `json:"alarm"`
	Conclusion string                        `json:"conclusion"`
	Result     string                        `json:"result"`
	Reason     string                        `json:"reason"`
	Targets    []algorithmTestImageLLMTarget `json:"targets"`
}

type algorithmTestPromptSpec struct {
	Name string
	Mode string
	Goal string
}

type algorithmTestPersistenceOptions struct {
	PersistRecord   bool
	PersistLLMUsage bool
}

const maxAlgorithmCoverSize = 5 * 1024 * 1024
const yoloLabelsConfigPath = "./configs/yolo-label.json"
const algorithmTestMediaRootDir = "configs/test"
const algorithmTestAIRequestMaxAttempts = 3
const algorithmTestAIRequestRetryBaseDelay = time.Second

var algorithmCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,31}$`)
var algorithmTestProbeVideoRunner = probeAlgorithmTestVideo

func formatAlgorithmTestVideoMaxSize(bytes int64) string {
	if bytes <= 0 {
		return "100MB"
	}
	if bytes >= 1024*1024 {
		return fmt.Sprintf("%dMB", bytes/(1024*1024))
	}
	if bytes >= 1024 {
		return fmt.Sprintf("%dKB", bytes/1024)
	}
	return fmt.Sprintf("%dB", bytes)
}

func formatAlgorithmTestVideoDurationRange(minSeconds, maxSeconds int) string {
	if minSeconds < 2 {
		minSeconds = 2
	}
	if maxSeconds < minSeconds {
		maxSeconds = 20 * 60
	}
	if maxSeconds%60 == 0 {
		return fmt.Sprintf("%d 秒到 %d 分钟", minSeconds, maxSeconds/60)
	}
	return fmt.Sprintf("%d 秒到 %d 秒", minSeconds, maxSeconds)
}

func (s *Server) registerAlgorithmRoutes(r gin.IRouter) {
	alg := r.Group("/algorithms")
	alg.GET("/test-jobs/:job_id", s.getAlgorithmTestJob)
	alg.GET("/draft-test-jobs/:job_id", s.getDraftAlgorithmTestJob)
	alg.GET("/test-limits", s.getAlgorithmTestLimits)
	alg.GET("", s.listAlgorithms)
	alg.POST("/draft-test", s.draftTestAlgorithm)
	alg.POST("", s.createAlgorithm)
	alg.POST("/import", s.importAlgorithms)
	alg.GET("/:id", s.getAlgorithm)
	alg.PUT("/:id", s.updateAlgorithm)
	alg.DELETE("/:id", s.deleteAlgorithm)

	alg.GET("/:id/prompts", s.listAlgorithmPrompts)
	alg.POST("/:id/prompts", s.createAlgorithmPrompt)
	alg.PUT("/:id/prompts/:prompt_id", s.updateAlgorithmPrompt)
	alg.DELETE("/:id/prompts/:prompt_id", s.deleteAlgorithmPrompt)
	alg.POST("/:id/prompts/:prompt_id/activate", s.activateAlgorithmPrompt)
	alg.POST("/:id/test", s.testAlgorithm)
	alg.GET("/:id/tests", s.listAlgorithmTests)
	alg.DELETE("/:id/tests", s.clearAlgorithmTests)
	alg.GET("/test-media/*path", s.getAlgorithmTestMedia)
	alg.GET("/test-image/*path", s.getAlgorithmTestImage)
	alg.POST("/cover", s.uploadAlgorithmCover)
	alg.GET("/cover/*path", s.getAlgorithmCoverImage)

	labels := r.Group("/yolo-labels")
	labels.GET("", s.listYoloLabels)

	s.registerLLMUsageRoutes(r)
}

func (s *Server) listAlgorithms(c *gin.Context) {
	var items []model.Algorithm
	if err := s.db.Order("created_at desc").Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query algorithms failed")
		return
	}
	type outItem struct {
		model.Algorithm
		ActivePrompt *model.AlgorithmPromptVersion `json:"active_prompt"`
	}
	out := make([]outItem, 0, len(items))
	for _, item := range items {
		prompt, _ := s.getActivePrompt(item.ID)
		out = append(out, outItem{Algorithm: item, ActivePrompt: prompt})
	}
	s.ok(c, gin.H{"items": out, "total": len(out)})
}

func (s *Server) getAlgorithm(c *gin.Context) {
	id := c.Param("id")
	var item model.Algorithm
	if err := s.db.Where("id = ?", id).First(&item).Error; err != nil {
		s.fail(c, http.StatusNotFound, "algorithm not found")
		return
	}
	prompt, _ := s.getActivePrompt(item.ID)
	s.ok(c, gin.H{"algorithm": item, "active_prompt": prompt})
}

func (s *Server) createAlgorithm(c *gin.Context) {
	var in algorithmUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(in.Code) == "" {
		generatedCode, err := s.generateCamera2AlgorithmCode()
		if err != nil {
			s.fail(c, http.StatusInternalServerError, "generate algorithm code failed")
			return
		}
		in.Code = generatedCode
	}
	algorithm, err := s.buildAlgorithmModel("", in)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.ensureAlgorithmCodeUnique(algorithm.Code, ""); err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.ensureAlgorithmNameUnique(algorithm.Name, ""); err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	algorithm.ID = uuid.NewString()
	activePrompt, err := s.createAlgorithmPromptIfNeeded(algorithm.ID, in)
	if err != nil {
		if isBadRequestError(err) {
			s.fail(c, http.StatusBadRequest, err.Error())
			return
		}
		s.fail(c, http.StatusInternalServerError, "create prompt failed")
		return
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&algorithm).Error; err != nil {
			return err
		}
		if activePrompt != nil {
			return tx.Create(activePrompt).Error
		}
		return nil
	}); err != nil {
		if isAlgorithmNameConflictError(err) {
			s.fail(c, http.StatusBadRequest, "算法名称不能重复")
			return
		}
		if isPromptVersionConflictError(err) {
			s.fail(c, http.StatusBadRequest, "提示词版本已存在")
			return
		}
		s.fail(c, http.StatusInternalServerError, "create algorithm failed")
		return
	}
	s.ok(c, gin.H{"algorithm": algorithm, "active_prompt": activePrompt})
}

func (s *Server) updateAlgorithm(c *gin.Context) {
	id := c.Param("id")
	var existing model.Algorithm
	if err := s.db.Where("id = ?", id).First(&existing).Error; err != nil {
		s.fail(c, http.StatusNotFound, "algorithm not found")
		return
	}
	var in algorithmUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	algorithm, err := s.buildAlgorithmModel(id, in)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.ensureAlgorithmCodeUnique(algorithm.Code, existing.ID); err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.ensureAlgorithmNameUnique(algorithm.Name, existing.ID); err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	oldImageURL := existing.ImageURL
	algorithm.ID = existing.ID
	algorithm.CreatedAt = existing.CreatedAt
	if err := s.db.Model(&existing).Updates(algorithm).Error; err != nil {
		if isAlgorithmNameConflictError(err) {
			s.fail(c, http.StatusBadRequest, "算法名称不能重复")
			return
		}
		s.fail(c, http.StatusInternalServerError, "update algorithm failed")
		return
	}
	s.tryDeleteReplacedAlgorithmCover(oldImageURL, algorithm.ImageURL)
	s.ok(c, algorithm)
}

func (s *Server) importAlgorithms(c *gin.Context) {
	var items []algorithmUpsertRequest
	if err := c.ShouldBindJSON(&items); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if len(items) == 0 {
		s.fail(c, http.StatusBadRequest, "import payload is empty")
		return
	}

	created := 0
	updated := 0
	itemErrors := make([]algorithmImportItemError, 0)

	appendImportError := func(index int, code string, err error) {
		if err == nil {
			return
		}
		itemErrors = append(itemErrors, algorithmImportItemError{
			Index:   index,
			Code:    normalizeAlgorithmCode(code),
			Message: err.Error(),
		})
	}

	for i, item := range items {
		rowIndex := i + 1
		code := normalizeAlgorithmCode(item.Code)
		var existing model.Algorithm
		hasExisting := false

		if code != "" {
			err := s.db.Where("code = ?", code).First(&existing).Error
			if err == nil {
				hasExisting = true
			} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				appendImportError(rowIndex, code, errBadRequest("query algorithm failed"))
				continue
			}
		}

		modelID := ""
		excludeID := ""
		if hasExisting {
			modelID = existing.ID
			excludeID = existing.ID
		}

		algorithm, err := s.buildAlgorithmModel(modelID, item)
		if err != nil {
			appendImportError(rowIndex, code, err)
			continue
		}
		if err := s.ensureAlgorithmCodeUnique(algorithm.Code, excludeID); err != nil {
			appendImportError(rowIndex, algorithm.Code, err)
			continue
		}

		if hasExisting {
			oldImageURL := existing.ImageURL
			algorithm.ID = existing.ID
			algorithm.CreatedAt = existing.CreatedAt
			if err := s.db.Model(&existing).Select("*").Omit("id", "created_at").Updates(algorithm).Error; err != nil {
				appendImportError(rowIndex, algorithm.Code, errBadRequest("update algorithm failed"))
				continue
			}
			s.tryDeleteReplacedAlgorithmCover(oldImageURL, algorithm.ImageURL)
			updated++
			continue
		}

		algorithm.ID = uuid.NewString()
		if err := s.db.Create(&algorithm).Error; err != nil {
			appendImportError(rowIndex, algorithm.Code, errBadRequest("create algorithm failed"))
			continue
		}
		created++
	}

	s.ok(c, gin.H{
		"total":   len(items),
		"created": created,
		"updated": updated,
		"failed":  len(itemErrors),
		"errors":  itemErrors,
	})
}

func (s *Server) deleteAlgorithm(c *gin.Context) {
	id := c.Param("id")
	var count int64
	if err := s.db.Model(&model.VideoTaskDeviceAlgorithm{}).Where("algorithm_id = ?", id).Count(&count).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query task relation failed")
		return
	}
	if count > 0 {
		s.fail(c, http.StatusBadRequest, "algorithm used by task, remove relation first")
		return
	}
	if err := s.db.Delete(&model.Algorithm{}, "id = ?", id).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "delete algorithm failed")
		return
	}
	s.ok(c, gin.H{"deleted": id})
}

func (s *Server) listAlgorithmPrompts(c *gin.Context) {
	algorithmID := c.Param("id")
	var items []model.AlgorithmPromptVersion
	if err := s.db.Where("algorithm_id = ?", algorithmID).Order("created_at desc").Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query prompts failed")
		return
	}
	s.ok(c, gin.H{"items": items, "total": len(items)})
}

func (s *Server) createAlgorithmPrompt(c *gin.Context) {
	algorithmID := c.Param("id")
	var in promptUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	in.Version = normalizePromptVersion(in.Version)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Version == "" || in.Prompt == "" {
		s.fail(c, http.StatusBadRequest, "version and prompt are required")
		return
	}

	prompt := model.AlgorithmPromptVersion{
		ID:          uuid.NewString(),
		AlgorithmID: algorithmID,
		Version:     in.Version,
		Prompt:      in.Prompt,
		IsActive:    in.IsActive,
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.ensurePromptVersionUnique(tx, algorithmID, in.Version, ""); err != nil {
			return err
		}
		if in.IsActive {
			if err := tx.Model(&model.AlgorithmPromptVersion{}).
				Where("algorithm_id = ?", algorithmID).
				Update("is_active", false).Error; err != nil {
				return err
			}
		}
		return tx.Create(&prompt).Error
	})
	if err != nil {
		if isPromptVersionConflictError(err) {
			s.fail(c, http.StatusBadRequest, "version already exists in this algorithm")
			return
		}
		if isBadRequestError(err) {
			s.fail(c, http.StatusBadRequest, err.Error())
			return
		}
		s.fail(c, http.StatusInternalServerError, "create prompt failed")
		return
	}
	s.ok(c, prompt)
}

func (s *Server) updateAlgorithmPrompt(c *gin.Context) {
	algorithmID := c.Param("id")
	promptID := c.Param("prompt_id")
	var existing model.AlgorithmPromptVersion
	if err := s.db.Where("id = ? AND algorithm_id = ?", promptID, algorithmID).First(&existing).Error; err != nil {
		s.fail(c, http.StatusNotFound, "prompt not found")
		return
	}
	var in promptUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	in.Version = normalizePromptVersion(in.Version)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Version == "" || in.Prompt == "" {
		s.fail(c, http.StatusBadRequest, "version and prompt are required")
		return
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.ensurePromptVersionUnique(tx, algorithmID, in.Version, existing.ID); err != nil {
			return err
		}
		if in.IsActive {
			if err := tx.Model(&model.AlgorithmPromptVersion{}).
				Where("algorithm_id = ?", algorithmID).
				Update("is_active", false).Error; err != nil {
				return err
			}
		}
		existing.Version = in.Version
		existing.Prompt = in.Prompt
		existing.IsActive = in.IsActive
		return tx.Save(&existing).Error
	})
	if err != nil {
		if isPromptVersionConflictError(err) {
			s.fail(c, http.StatusBadRequest, "version already exists in this algorithm")
			return
		}
		if isBadRequestError(err) {
			s.fail(c, http.StatusBadRequest, err.Error())
			return
		}
		s.fail(c, http.StatusInternalServerError, "update prompt failed")
		return
	}
	s.ok(c, existing)
}

func (s *Server) deleteAlgorithmPrompt(c *gin.Context) {
	algorithmID := c.Param("id")
	promptID := c.Param("prompt_id")
	var existing model.AlgorithmPromptVersion
	if err := s.db.Where("id = ? AND algorithm_id = ?", promptID, algorithmID).First(&existing).Error; err != nil {
		s.fail(c, http.StatusNotFound, "prompt not found")
		return
	}
	if existing.IsActive {
		s.fail(c, http.StatusBadRequest, "active prompt version cannot be deleted")
		return
	}
	if err := s.db.Delete(&existing).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "delete prompt failed")
		return
	}
	s.ok(c, gin.H{"deleted": promptID})
}

func (s *Server) activateAlgorithmPrompt(c *gin.Context) {
	algorithmID := c.Param("id")
	promptID := c.Param("prompt_id")
	var existing model.AlgorithmPromptVersion
	if err := s.db.Where("id = ? AND algorithm_id = ?", promptID, algorithmID).First(&existing).Error; err != nil {
		s.fail(c, http.StatusNotFound, "prompt not found")
		return
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.AlgorithmPromptVersion{}).
			Where("algorithm_id = ?", algorithmID).Update("is_active", false).Error; err != nil {
			return err
		}
		return tx.Model(&model.AlgorithmPromptVersion{}).
			Where("id = ?", promptID).Update("is_active", true).Error
	})
	if err != nil {
		s.fail(c, http.StatusInternalServerError, "activate prompt failed")
		return
	}
	s.ok(c, gin.H{"activated": promptID})
}

func (s *Server) listModelProviders(c *gin.Context) {
	var items []model.ModelProvider
	if err := s.db.Order("created_at desc").Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query model providers failed")
		return
	}
	s.ok(c, gin.H{"items": items, "total": len(items)})
}

func (s *Server) createModelProvider(c *gin.Context) {
	var in modelProviderUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.APIURL) == "" || strings.TrimSpace(in.Model) == "" {
		s.fail(c, http.StatusBadRequest, "name, api_url, model are required")
		return
	}
	item := model.ModelProvider{
		ID:      uuid.NewString(),
		Name:    strings.TrimSpace(in.Name),
		APIURL:  strings.TrimSpace(in.APIURL),
		APIKey:  strings.TrimSpace(in.APIKey),
		Model:   strings.TrimSpace(in.Model),
		Enabled: in.Enabled,
	}
	if err := s.db.Create(&item).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "create model provider failed")
		return
	}
	s.ok(c, item)
}

func (s *Server) updateModelProvider(c *gin.Context) {
	id := c.Param("id")
	var item model.ModelProvider
	if err := s.db.Where("id = ?", id).First(&item).Error; err != nil {
		s.fail(c, http.StatusNotFound, "model provider not found")
		return
	}
	var in modelProviderUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	item.Name = strings.TrimSpace(in.Name)
	item.APIURL = strings.TrimSpace(in.APIURL)
	item.APIKey = strings.TrimSpace(in.APIKey)
	item.Model = strings.TrimSpace(in.Model)
	item.Enabled = in.Enabled
	if item.Name == "" || item.APIURL == "" || item.Model == "" {
		s.fail(c, http.StatusBadRequest, "name, api_url, model are required")
		return
	}
	if err := s.db.Save(&item).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "update model provider failed")
		return
	}
	s.ok(c, item)
}

func (s *Server) deleteModelProvider(c *gin.Context) {
	id := c.Param("id")
	var count int64
	if err := s.db.Model(&model.Algorithm{}).Where("model_provider_id = ?", id).Count(&count).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query relation failed")
		return
	}
	if count > 0 {
		s.fail(c, http.StatusBadRequest, "model provider is used by algorithm")
		return
	}
	if err := s.db.Delete(&model.ModelProvider{}, "id = ?", id).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "delete model provider failed")
		return
	}
	s.ok(c, gin.H{"deleted": id})
}

func (s *Server) listYoloLabels(c *gin.Context) {
	items := s.getYoloLabelsSnapshot()
	if len(items) == 0 {
		s.fail(c, http.StatusInternalServerError, "yolo labels are not loaded")
		return
	}
	s.ok(c, gin.H{"items": items, "total": len(items)})
}

func (s *Server) loadYoloLabelsOnStartup() error {
	return s.loadYoloLabelsFromPath(resolveYoloLabelsConfigPath())
}

func (s *Server) loadYoloLabelsFromPath(path string) error {
	items, err := loadYoloLabelsFromFile(path)
	if err != nil {
		return fmt.Errorf("load yolo labels from %s: %w", path, err)
	}
	s.yoloLabelsMu.Lock()
	defer s.yoloLabelsMu.Unlock()
	s.yoloLabels = make([]yoloLabelItem, len(items))
	copy(s.yoloLabels, items)
	return nil
}

func (s *Server) getYoloLabelsSnapshot() []yoloLabelItem {
	s.yoloLabelsMu.RLock()
	defer s.yoloLabelsMu.RUnlock()
	if len(s.yoloLabels) == 0 {
		return nil
	}
	items := make([]yoloLabelItem, len(s.yoloLabels))
	copy(items, s.yoloLabels)
	return items
}

func resolveYoloLabelsConfigPath() string {
	candidates := []string{
		filepath.Clean(yoloLabelsConfigPath),
		filepath.Clean(filepath.Join("..", yoloLabelsConfigPath)),
		filepath.Clean(filepath.Join("..", "..", yoloLabelsConfigPath)),
		filepath.Clean(filepath.Join("..", "..", "..", yoloLabelsConfigPath)),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Clean(yoloLabelsConfigPath)
}

func (s *Server) uploadAlgorithmCover(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		s.fail(c, http.StatusBadRequest, "image file is required")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		s.fail(c, http.StatusBadRequest, "open upload file failed")
		return
	}
	defer file.Close()

	body, err := io.ReadAll(io.LimitReader(file, maxAlgorithmCoverSize+1))
	if err != nil {
		s.fail(c, http.StatusBadRequest, "read upload file failed")
		return
	}
	if len(body) == 0 {
		s.fail(c, http.StatusBadRequest, "image file is empty")
		return
	}
	if len(body) > maxAlgorithmCoverSize {
		s.fail(c, http.StatusBadRequest, "image file is too large (max 5MB)")
		return
	}

	mimeType := http.DetectContentType(body)
	ext, ok := imageExtensionByMIME(mimeType)
	if !ok {
		s.fail(c, http.StatusBadRequest, "unsupported image type")
		return
	}

	now := time.Now()
	dateDir := now.Format("20060102")
	dir := filepath.Join("configs", "cover", dateDir)
	if err := s.ensureDir(dir); err != nil {
		s.fail(c, http.StatusInternalServerError, "create cover directory failed")
		return
	}
	filename := uuid.NewString() + ext
	fullPath := filepath.Join(dir, filename)
	if err := os.WriteFile(fullPath, body, 0o644); err != nil {
		s.fail(c, http.StatusInternalServerError, "save cover file failed")
		return
	}

	relPath := filepath.ToSlash(filepath.Join(dateDir, filename))
	s.ok(c, gin.H{
		"path": relPath,
		"url":  "/api/v1/algorithms/cover/" + relPath,
	})
}

func loadYoloLabelsFromFile(path string) ([]yoloLabelItem, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var rawItems []yoloLabelItem
	if err := json.Unmarshal(body, &rawItems); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	if len(rawItems) == 0 {
		return nil, fmt.Errorf("empty label list")
	}

	seen := make(map[string]struct{}, len(rawItems))
	items := make([]yoloLabelItem, 0, len(rawItems))
	for i, item := range rawItems {
		label := strings.TrimSpace(item.Label)
		if label == "" {
			return nil, fmt.Errorf("label is required at index %d", i)
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return nil, fmt.Errorf("name is required at index %d", i)
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate label: %s", label)
		}
		seen[key] = struct{}{}
		items = append(items, yoloLabelItem{
			Label: label,
			Name:  name,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
	})
	return items, nil
}

func (s *Server) getAlgorithmTestLimits(c *gin.Context) {
	s.ok(c, gin.H{
		"image_max_count": s.algorithmTestImageMaxCount(),
		"video_max_count": s.algorithmTestVideoMaxCount(),
		"video_max_bytes": s.algorithmTestVideoMaxBytes(),
	})
}

func (s *Server) algorithmTestImageMaxCount() int {
	if s == nil || s.cfg == nil || s.cfg.Server.AI.AlgorithmTestImageMaxCount <= 0 {
		return 5
	}
	return s.cfg.Server.AI.AlgorithmTestImageMaxCount
}

func (s *Server) algorithmTestVideoMaxCount() int {
	if s == nil || s.cfg == nil || s.cfg.Server.AI.AlgorithmTestVideoMaxCount <= 0 {
		return 1
	}
	return s.cfg.Server.AI.AlgorithmTestVideoMaxCount
}

func (s *Server) algorithmTestVideoMaxBytes() int64 {
	if s == nil || s.cfg == nil || s.cfg.Server.AI.AlgorithmTestVideoMaxBytes <= 0 {
		return 100 * 1024 * 1024
	}
	return s.cfg.Server.AI.AlgorithmTestVideoMaxBytes
}

func (s *Server) testAlgorithm(c *gin.Context) {
	algorithmID := c.Param("id")
	var algorithm model.Algorithm
	if err := s.db.Where("id = ?", algorithmID).First(&algorithm).Error; err != nil {
		s.fail(c, http.StatusNotFound, "algorithm not found")
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(c.GetHeader("Content-Type")))
	if strings.Contains(contentType, "application/json") {
		s.testAlgorithmJSON(c, algorithm)
		return
	}
	s.testAlgorithmMultipart(c, algorithm)
}

func (s *Server) testAlgorithmJSON(c *gin.Context, algorithm model.Algorithm) {
	var in testAlgorithmRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
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
	in.ImageBase64 = normalizeImageBase64(in.ImageBase64)
	if in.ImageBase64 == "" {
		s.fail(c, http.StatusBadRequest, "image_base64 is required")
		return
	}
	if _, err := decodeImageBase64(in.ImageBase64); err != nil {
		s.fail(c, http.StatusBadRequest, "image_base64 is invalid")
		return
	}
	uploadedImagePath, saveUploadErr := s.saveTestSnapshot(algorithm.ID, in.ImageBase64)
	if saveUploadErr != nil {
		s.fail(c, http.StatusInternalServerError, "save upload image failed")
		return
	}
	batchID := uuid.NewString()

	algorithmConfig, llmPrompt, provider, err := s.buildAlgorithmTestConfig(algorithm)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}

	req := ai.AnalyzeImageRequest{
		ImageRelPath:     uploadedImagePath,
		AlgorithmConfigs: []ai.StartCameraAlgorithmConfig{algorithmConfig},
		LLMAPIURL:        provider.APIURL,
		LLMAPIKey:        provider.APIKey,
		LLMModel:         provider.Model,
		LLMPrompt:        llmPrompt,
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	resp, err := s.aiClient.AnalyzeImage(ctx, req)

	reqJSON, _ := json.Marshal(req)
	respJSON, _ := json.Marshal(resp)
	imageWidth, imageHeight := readAlgorithmTestImageSize(s.algorithmTestMediaFullPath(uploadedImagePath))
	view := buildAlgorithmImageTestView(resp, err == nil && resp != nil && resp.Success, algorithmConfig, imageWidth, imageHeight)
	record := model.AlgorithmTestRecord{
		ID:               uuid.NewString(),
		AlgorithmID:      algorithm.ID,
		BatchID:          batchID,
		MediaType:        string(algorithmTestMediaTypeImage),
		MediaPath:        uploadedImagePath,
		OriginalFileName: "",
		ImagePath:        uploadedImagePath,
		RequestPayload:   sanitizeAlgorithmTestRequestPayload(reqJSON),
		ResponsePayload:  sanitizeAlgorithmTestResponsePayload(respJSON),
		Success:          view.Success,
	}
	_ = s.db.Create(&record).Error
	if err == nil && resp != nil && resp.LLMUsage != nil {
		s.persistAlgorithmTestLLMUsage(in.CameraID, provider, algorithmConfig, resp.LLMUsage, algorithm.ID)
	}

	if err != nil {
		s.fail(c, http.StatusBadGateway, "ai analyze failed: "+err.Error())
		return
	}
	s.ok(c, gin.H{
		"result":           resp,
		"record":           record,
		"conclusion":       view.Conclusion,
		"basis":            view.Basis,
		"normalized_boxes": view.Boxes,
		"media_url":        s.algorithmTestMediaURL(uploadedImagePath),
	})
}

func (s *Server) testAlgorithmMultipart(c *gin.Context, algorithm model.Algorithm) {
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

	cameraID := strings.TrimSpace(c.PostForm("camera_id"))
	job, err := s.createAlgorithmTestJob(algorithm, cameraID, files)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	s.ok(c, gin.H{
		"job_id":       job.ID,
		"batch_id":     job.BatchID,
		"algorithm_id": job.AlgorithmID,
		"status":       job.Status,
		"total_count":  job.TotalCount,
	})
}

func (s *Server) validateAlgorithmTestFileCounts(files []*multipart.FileHeader) error {
	if len(files) == 0 {
		return errBadRequest("files is required")
	}
	imageCount := 0
	videoCount := 0
	for _, file := range files {
		src, err := file.Open()
		if err != nil {
			return errBadRequest("读取上传文件失败")
		}
		head := make([]byte, 512)
		n, _ := io.ReadFull(src, head)
		_ = src.Close()
		head = head[:n]
		mimeType := http.DetectContentType(head)
		mediaType, _, classifyErr := classifyAlgorithmTestUpload(file.Filename, mimeType)
		if classifyErr != nil {
			return errBadRequest(classifyErr.Error())
		}
		switch mediaType {
		case algorithmTestMediaTypeImage:
			imageCount++
		case algorithmTestMediaTypeVideo:
			videoCount++
		}
	}
	if imageCount > s.algorithmTestImageMaxCount() {
		return errBadRequest(fmt.Sprintf("测试图片最多上传 %d 张", s.algorithmTestImageMaxCount()))
	}
	if videoCount > s.algorithmTestVideoMaxCount() {
		return errBadRequest(fmt.Sprintf("测试视频最多上传 %d 个", s.algorithmTestVideoMaxCount()))
	}
	return nil
}

func (s *Server) processAlgorithmTestUpload(
	ctx context.Context,
	algorithm model.Algorithm,
	batchID string,
	cameraID string,
	file *multipart.FileHeader,
	algorithmConfig ai.StartCameraAlgorithmConfig,
	provider model.ModelProvider,
	imagePrompt string,
	videoPrompt string,
) algorithmTestItemResult {
	fileName := strings.TrimSpace(file.Filename)
	saved, err := s.saveAlgorithmTestUpload(algorithm.ID, batchID, file)
	if err != nil {
		return algorithmTestItemResult{
			FileName:   fileName,
			MediaType:  "",
			Success:    false,
			Conclusion: "测试失败",
			Basis:      err.Error(),
		}
	}

	switch saved.MediaType {
	case algorithmTestMediaTypeImage:
		return s.runAlgorithmImageTest(ctx, algorithm, batchID, "", "", cameraID, fileName, saved, algorithmConfig, provider, imagePrompt, algorithmTestPersistenceOptions{
			PersistRecord:   true,
			PersistLLMUsage: true,
		})
	case algorithmTestMediaTypeVideo:
		return s.runAlgorithmVideoTest(ctx, algorithm, batchID, "", "", cameraID, fileName, saved, algorithmConfig, provider, videoPrompt, algorithmTestPersistenceOptions{
			PersistRecord:   true,
			PersistLLMUsage: true,
		})
	default:
		return algorithmTestItemResult{
			FileName:   fileName,
			MediaType:  string(saved.MediaType),
			Success:    false,
			Conclusion: "测试失败",
			Basis:      "unsupported media type",
			MediaURL:   s.algorithmTestMediaURL(saved.RelativePath),
		}
	}
}

func (s *Server) runAlgorithmImageTest(
	ctx context.Context,
	algorithm model.Algorithm,
	batchID string,
	jobID string,
	itemID string,
	cameraID string,
	fileName string,
	media algorithmTestSavedMedia,
	algorithmConfig ai.StartCameraAlgorithmConfig,
	provider model.ModelProvider,
	llmPrompt string,
	options algorithmTestPersistenceOptions,
) algorithmTestItemResult {
	if err := s.ensureLLMTokenQuotaAvailable(); err != nil {
		message := llmTokenLimitExceededMessage
		if !isLLMTokenLimitExceededError(err) {
			message = "检查 LLM token 限额失败: " + err.Error()
		}
		return algorithmTestItemResult{
			JobItemID:    itemID,
			FileName:     fileName,
			MediaType:    string(media.MediaType),
			Success:      false,
			Conclusion:   "测试失败",
			Basis:        message,
			MediaURL:     s.algorithmTestMediaURL(media.RelativePath),
			ErrorMessage: message,
		}
	}
	if _, err := os.ReadFile(media.FullPath); err != nil {
		return algorithmTestItemResult{
			FileName:   fileName,
			MediaType:  string(media.MediaType),
			Success:    false,
			Conclusion: "测试失败",
			Basis:      "read image failed",
			MediaURL:   s.algorithmTestMediaURL(media.RelativePath),
		}
	}
	imageWidth, imageHeight := readAlgorithmTestImageSize(media.FullPath)
	req := ai.AnalyzeImageRequest{
		ImageRelPath:     media.RelativePath,
		AlgorithmConfigs: []ai.StartCameraAlgorithmConfig{algorithmConfig},
		LLMAPIURL:        provider.APIURL,
		LLMAPIKey:        provider.APIKey,
		LLMModel:         provider.Model,
		LLMPrompt:        llmPrompt,
	}
	logutil.Infof(
		"algorithm test image ai request start: algorithm_id=%s batch_id=%s job_id=%s item_id=%s camera_id=%s media_path=%s file_name=%s max_attempts=%d",
		algorithm.ID,
		batchID,
		strings.TrimSpace(jobID),
		strings.TrimSpace(itemID),
		cameraID,
		media.RelativePath,
		fileName,
		algorithmTestAIRequestMaxAttempts,
	)
	var resp *ai.AnalyzeImageResponse
	attempts, callErr := s.executeAlgorithmTestAIRequestWithRetry(
		ctx,
		20*time.Second,
		algorithmTestMediaTypeImage,
		algorithm.ID,
		batchID,
		jobID,
		itemID,
		cameraID,
		media.RelativePath,
		fileName,
		func(attemptCtx context.Context) error {
			var err error
			resp, err = s.aiClient.AnalyzeImage(attemptCtx, req)
			return err
		},
	)
	if callErr == nil {
		logutil.Infof(
			"algorithm test image ai request finished: algorithm_id=%s batch_id=%s job_id=%s item_id=%s camera_id=%s media_path=%s success=%v attempt=%d max_attempts=%d",
			algorithm.ID,
			batchID,
			strings.TrimSpace(jobID),
			strings.TrimSpace(itemID),
			cameraID,
			media.RelativePath,
			resp != nil && resp.Success,
			attempts,
			algorithmTestAIRequestMaxAttempts,
		)
	}
	callFailureType := classifyAIRequestFailure(callErr)
	llmCallStatus := ""
	if resp != nil && resp.LLMUsage != nil {
		llmCallStatus = strings.ToLower(strings.TrimSpace(resp.LLMUsage.CallStatus))
	}

	reqJSON, _ := json.Marshal(map[string]any{
		"algorithm_configs": req.AlgorithmConfigs,
		"llm_api_url":       req.LLMAPIURL,
		"llm_model":         req.LLMModel,
		"llm_prompt":        req.LLMPrompt,
		"media_type":        media.MediaType,
		"file_name":         fileName,
		"batch_id":          batchID,
	})
	respJSON, _ := json.Marshal(resp)
	requestPayload := sanitizeAlgorithmTestRequestPayload(reqJSON)
	responsePayload := sanitizeAlgorithmTestResponsePayload(respJSON)
	view := buildAlgorithmImageTestView(resp, callErr == nil && resp != nil && resp.Success, algorithmConfig, imageWidth, imageHeight)
	if callErr != nil {
		view.Success = false
		view.Conclusion = "测试失败"
		view.Basis = "AI 图片分析失败: " + callErr.Error()
		view.ErrorMessage = view.Basis
	}
	record := model.AlgorithmTestRecord{}
	if options.PersistRecord {
		record = model.AlgorithmTestRecord{
			ID:               uuid.NewString(),
			AlgorithmID:      algorithm.ID,
			BatchID:          batchID,
			MediaType:        string(media.MediaType),
			MediaPath:        media.RelativePath,
			OriginalFileName: fileName,
			ImagePath:        media.RelativePath,
			RequestPayload:   requestPayload,
			ResponsePayload:  responsePayload,
			Success:          view.Success,
		}
		_ = s.db.Create(&record).Error
	}
	if options.PersistLLMUsage && callErr == nil && resp != nil && resp.LLMUsage != nil {
		s.persistAlgorithmTestLLMUsage(cameraID, provider, algorithmConfig, resp.LLMUsage, algorithm.ID)
	}
	return algorithmTestItemResult{
		RecordID:        strings.TrimSpace(record.ID),
		FileName:        fileName,
		MediaType:       string(media.MediaType),
		Success:         view.Success,
		Conclusion:      view.Conclusion,
		Basis:           view.Basis,
		MediaURL:        s.algorithmTestMediaURL(media.RelativePath),
		NormalizedBoxes: view.Boxes,
		ErrorMessage:    view.ErrorMessage,
		Record:          record,
		LLMCallStatus:   llmCallStatus,
		AIRequestType:   callFailureType,
		RequestPayload:  requestPayload,
		ResponsePayload: responsePayload,
	}
}

func (s *Server) runAlgorithmVideoTest(
	ctx context.Context,
	algorithm model.Algorithm,
	batchID string,
	jobID string,
	itemID string,
	cameraID string,
	fileName string,
	media algorithmTestSavedMedia,
	algorithmConfig ai.StartCameraAlgorithmConfig,
	provider model.ModelProvider,
	llmPrompt string,
	options algorithmTestPersistenceOptions,
) algorithmTestItemResult {
	if err := s.ensureLLMTokenQuotaAvailable(); err != nil {
		message := llmTokenLimitExceededMessage
		if !isLLMTokenLimitExceededError(err) {
			message = "检查 LLM token 限额失败: " + err.Error()
		}
		return algorithmTestItemResult{
			JobItemID:       itemID,
			FileName:        fileName,
			MediaType:       string(media.MediaType),
			Success:         false,
			Conclusion:      "测试失败",
			Basis:           message,
			MediaURL:        s.algorithmTestMediaURL(media.RelativePath),
			ErrorMessage:    message,
			DurationSeconds: 0,
		}
	}
	videoInfo, err := os.Stat(media.FullPath)
	if err != nil {
		return algorithmTestItemResult{
			FileName:   fileName,
			MediaType:  string(media.MediaType),
			Success:    false,
			Conclusion: "测试失败",
			Basis:      "视频文件读取失败: " + err.Error(),
			MediaURL:   s.algorithmTestMediaURL(media.RelativePath),
		}
	}
	if videoInfo.Size() > s.algorithmTestVideoMaxBytes() {
		return algorithmTestItemResult{
			FileName:   fileName,
			MediaType:  string(media.MediaType),
			Success:    false,
			Conclusion: "测试失败",
			Basis:      fmt.Sprintf("视频大小超过限制，当前上限为 %s", formatAlgorithmTestVideoMaxSize(s.algorithmTestVideoMaxBytes())),
			MediaURL:   s.algorithmTestMediaURL(media.RelativePath),
		}
	}

	meta, err := algorithmTestProbeVideoRunner(media.FullPath)
	if err != nil {
		return algorithmTestItemResult{
			FileName:   fileName,
			MediaType:  string(media.MediaType),
			Success:    false,
			Conclusion: "测试失败",
			Basis:      "视频信息读取失败: " + err.Error(),
			MediaURL:   s.algorithmTestMediaURL(media.RelativePath),
		}
	}
	if meta.DurationSeconds <= 0 {
		return algorithmTestItemResult{
			FileName:        fileName,
			MediaType:       string(media.MediaType),
			Success:         false,
			Conclusion:      "测试失败",
			Basis:           "视频时长无效",
			MediaURL:        s.algorithmTestMediaURL(media.RelativePath),
			DurationSeconds: meta.DurationSeconds,
		}
	}
	if meta.DurationSeconds < float64(s.cfg.Server.AI.AlgorithmTestVideoMinSeconds) ||
		meta.DurationSeconds > float64(s.cfg.Server.AI.AlgorithmTestVideoMaxSeconds) {
		return algorithmTestItemResult{
			FileName:        fileName,
			MediaType:       string(media.MediaType),
			Success:         false,
			Conclusion:      "测试失败",
			Basis:           fmt.Sprintf("视频时长超出限制，当前支持区间为 %s", formatAlgorithmTestVideoDurationRange(s.cfg.Server.AI.AlgorithmTestVideoMinSeconds, s.cfg.Server.AI.AlgorithmTestVideoMaxSeconds)),
			MediaURL:        s.algorithmTestMediaURL(media.RelativePath),
			DurationSeconds: meta.DurationSeconds,
		}
	}
	if strings.TrimSpace(provider.APIURL) == "" || strings.TrimSpace(provider.Model) == "" || strings.TrimSpace(llmPrompt) == "" {
		return algorithmTestItemResult{
			FileName:        fileName,
			MediaType:       string(media.MediaType),
			Success:         false,
			Conclusion:      "测试失败",
			Basis:           "视频测试仅支持已配置大模型提示词的算法",
			MediaURL:        s.algorithmTestMediaURL(media.RelativePath),
			DurationSeconds: meta.DurationSeconds,
		}
	}

	if _, err := os.ReadFile(media.FullPath); err != nil {
		return algorithmTestItemResult{
			FileName:        fileName,
			MediaType:       string(media.MediaType),
			Success:         false,
			Conclusion:      "测试失败",
			Basis:           "视频读取失败: " + err.Error(),
			MediaURL:        s.algorithmTestMediaURL(media.RelativePath),
			DurationSeconds: meta.DurationSeconds,
		}
	}
	req := ai.AnalyzeVideoTestRequest{
		VideoRelPath:     media.RelativePath,
		FPS:              s.cfg.Server.AI.AlgorithmTestVideoFPS,
		AlgorithmConfigs: []ai.StartCameraAlgorithmConfig{algorithmConfig},
		LLMAPIURL:        provider.APIURL,
		LLMAPIKey:        provider.APIKey,
		LLMModel:         provider.Model,
		LLMPrompt:        llmPrompt,
	}
	logutil.Infof(
		"algorithm test video ai request start: algorithm_id=%s batch_id=%s job_id=%s item_id=%s camera_id=%s media_path=%s file_name=%s duration_seconds=%.3f fps=%d max_attempts=%d",
		algorithm.ID,
		batchID,
		strings.TrimSpace(jobID),
		strings.TrimSpace(itemID),
		cameraID,
		media.RelativePath,
		fileName,
		meta.DurationSeconds,
		req.FPS,
		algorithmTestAIRequestMaxAttempts,
	)
	var resp *ai.AnalyzeVideoTestResponse
	attempts, callErr := s.executeAlgorithmTestAIRequestWithRetry(
		ctx,
		3*time.Minute,
		algorithmTestMediaTypeVideo,
		algorithm.ID,
		batchID,
		jobID,
		itemID,
		cameraID,
		media.RelativePath,
		fileName,
		func(attemptCtx context.Context) error {
			var err error
			resp, err = s.aiClient.AnalyzeVideoTest(attemptCtx, req)
			return err
		},
	)
	if callErr == nil {
		logutil.Infof(
			"algorithm test video ai request finished: algorithm_id=%s batch_id=%s job_id=%s item_id=%s camera_id=%s media_path=%s success=%v attempt=%d max_attempts=%d",
			algorithm.ID,
			batchID,
			strings.TrimSpace(jobID),
			strings.TrimSpace(itemID),
			cameraID,
			media.RelativePath,
			resp != nil && resp.Success,
			attempts,
			algorithmTestAIRequestMaxAttempts,
		)
	}

	reqJSON, _ := json.Marshal(map[string]any{
		"algorithm_configs": req.AlgorithmConfigs,
		"llm_api_url":       req.LLMAPIURL,
		"llm_model":         req.LLMModel,
		"llm_prompt":        req.LLMPrompt,
		"media_type":        media.MediaType,
		"file_name":         fileName,
		"batch_id":          batchID,
		"fps":               req.FPS,
	})
	respJSON, _ := json.Marshal(resp)
	conclusion := "测试失败"
	basis := ""
	anomalyTimes := []ai.SequenceAnomalyTime(nil)
	success := callErr == nil && resp != nil && resp.Success
	if callErr != nil {
		basis = "AI 视频分析失败: " + callErr.Error()
	} else if resp != nil {
		view := buildAlgorithmVideoTestViewFromPayload(map[string]any{
			"llm_result": resp.LLMResult,
			"message":    resp.Message,
		}, true)
		conclusion = strings.TrimSpace(view.Conclusion)
		if conclusion == "" {
			conclusion = "视频分析完成"
		}
		basis = strings.TrimSpace(view.Basis)
		anomalyTimes = view.AnomalyTimes
	}
	record := model.AlgorithmTestRecord{}
	if options.PersistRecord {
		record = model.AlgorithmTestRecord{
			ID:               uuid.NewString(),
			AlgorithmID:      algorithm.ID,
			BatchID:          batchID,
			MediaType:        string(media.MediaType),
			MediaPath:        media.RelativePath,
			OriginalFileName: fileName,
			RequestPayload:   sanitizeAlgorithmTestVideoRequestPayload(reqJSON),
			ResponsePayload:  sanitizeAlgorithmTestVideoResponsePayload(respJSON),
			Success:          success,
		}
		_ = s.db.Create(&record).Error
	}
	if options.PersistLLMUsage && success && resp != nil && resp.LLMUsage != nil {
		s.persistAlgorithmTestLLMUsage(cameraID, provider, algorithmConfig, resp.LLMUsage, algorithm.ID)
	}
	return algorithmTestItemResult{
		RecordID:        strings.TrimSpace(record.ID),
		FileName:        fileName,
		MediaType:       string(media.MediaType),
		Success:         success,
		Conclusion:      conclusion,
		Basis:           basis,
		MediaURL:        s.algorithmTestMediaURL(media.RelativePath),
		AnomalyTimes:    anomalyTimes,
		DurationSeconds: meta.DurationSeconds,
		Record:          record,
	}
}

func (s *Server) executeAlgorithmTestAIRequestWithRetry(
	ctx context.Context,
	timeout time.Duration,
	mediaType algorithmTestMediaType,
	algorithmID string,
	batchID string,
	jobID string,
	itemID string,
	cameraID string,
	mediaPath string,
	fileName string,
	call func(context.Context) error,
) (int, error) {
	if call == nil {
		return 0, errors.New("algorithm test ai call is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := algorithmTestAIRequestMaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return attempt - 1, err
		}
		attemptCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		err := call(attemptCtx)
		cancel()
		if err == nil {
			return attempt, nil
		}
		failureType := classifyAIRequestFailure(err)
		if !shouldRetryAlgorithmTestAIRequest(err) || attempt >= attempts {
			logutil.Warnf(
				"algorithm test %s ai request failed: algorithm_id=%s batch_id=%s job_id=%s item_id=%s camera_id=%s media_path=%s file_name=%s attempt=%d max_attempts=%d failure_type=%s err=%v",
				string(mediaType),
				algorithmID,
				batchID,
				strings.TrimSpace(jobID),
				strings.TrimSpace(itemID),
				cameraID,
				mediaPath,
				fileName,
				attempt,
				attempts,
				failureType,
				err,
			)
			return attempt, err
		}
		retryDelay := time.Duration(attempt) * algorithmTestAIRequestRetryBaseDelay
		logutil.Warnf(
			"algorithm test %s ai request retrying: algorithm_id=%s batch_id=%s job_id=%s item_id=%s camera_id=%s media_path=%s file_name=%s attempt=%d max_attempts=%d failure_type=%s retry_in=%s err=%v",
			string(mediaType),
			algorithmID,
			batchID,
			strings.TrimSpace(jobID),
			strings.TrimSpace(itemID),
			cameraID,
			mediaPath,
			fileName,
			attempt,
			attempts,
			failureType,
			retryDelay,
			err,
		)
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return attempt, ctx.Err()
		case <-timer.C:
		}
	}
	return attempts, errors.New("algorithm test ai retry exhausted")
}

func shouldRetryAlgorithmTestAIRequest(err error) bool {
	switch classifyAIRequestFailure(err) {
	case "connect", "read":
		return true
	default:
		return false
	}
}

func classifyAIRequestFailure(err error) string {
	if err == nil {
		return "none"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "connect refused"), strings.Contains(message, "actively refused"):
		return "connect"
	case strings.Contains(message, "context deadline exceeded"),
		strings.Contains(message, "client.timeout exceeded"),
		strings.Contains(message, "timed out"),
		strings.Contains(message, "timeout"):
		return "timeout"
	case strings.Contains(message, "read ai response failed"), strings.Contains(message, "connection reset"), strings.Contains(message, "forcibly closed"), strings.Contains(message, "unexpected eof"), message == "eof", strings.Contains(message, ": eof"):
		return "read"
	case strings.Contains(message, "ai request failed ["):
		return "status"
	case strings.Contains(message, "decode ai response failed"), strings.Contains(message, "decode ai status failed"):
		return "decode"
	default:
		return "unknown"
	}
}

// 图片类分析的 job/item 级补跑统一走这套判定，避免正式测试、草稿测试和巡查三处分叉。
func shouldRetryAnalyzeImageFailure(success bool, mediaType, aiRequestType, llmCallStatus, errorMessage string) bool {
	if success || !strings.EqualFold(strings.TrimSpace(mediaType), string(algorithmTestMediaTypeImage)) {
		return false
	}
	if isRetryableAnalyzeImageTransportFailure(strings.TrimSpace(aiRequestType)) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(llmCallStatus), model.LLMUsageStatusError) {
		return false
	}
	return isRetryableAnalyzeImageErrorMessage(errorMessage)
}

func shouldRetryAlgorithmTestImageJobResult(result algorithmTestItemResult) bool {
	return shouldRetryAnalyzeImageFailure(
		result.Success,
		result.MediaType,
		result.AIRequestType,
		result.LLMCallStatus,
		result.ErrorMessage,
	)
}

func isRetryableAlgorithmTestImageTransportFailure(failureType string) bool {
	switch strings.ToLower(strings.TrimSpace(failureType)) {
	case "connect", "read", "timeout":
		return true
	default:
		return false
	}
}

func isRetryableAnalyzeImageTransportFailure(failureType string) bool {
	return isRetryableAlgorithmTestImageTransportFailure(failureType)
}

func isRetryableAnalyzeImageErrorMessage(errorMessage string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(errorMessage)), "connection error")
}

func buildAnalyzeImageRetryHint(reason string, retryRound, maxRetryRounds int) string {
	if retryRound < 1 {
		retryRound = 1
	}
	if maxRetryRounds < retryRound {
		maxRetryRounds = retryRound
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "图片分析失败"
	}
	return fmt.Sprintf("第 %d/%d 轮自动重试中，上一轮失败原因：%s", retryRound, maxRetryRounds, reason)
}

func (s *Server) analyzeImageFailureRetryCount() int {
	if s == nil || s.cfg == nil {
		return 1
	}
	if s.cfg.Server.AI.AnalyzeImageFailureRetryCount < 0 {
		return 0
	}
	return s.cfg.Server.AI.AnalyzeImageFailureRetryCount
}

func (s *Server) persistAlgorithmTestLLMUsage(
	cameraID string,
	provider model.ModelProvider,
	algorithmConfig ai.StartCameraAlgorithmConfig,
	usage *ai.LLMUsage,
	algorithmID string,
) {
	if usage == nil {
		return
	}
	if _, usageErr := s.recordLLMUsage(s.db, llmUsagePersistRequest{
		Source:       model.LLMUsageSourceAlgorithmTest,
		DeviceID:     strings.TrimSpace(cameraID),
		ProviderID:   strings.TrimSpace(provider.ID),
		ProviderName: strings.TrimSpace(provider.Name),
		Model:        strings.TrimSpace(provider.Model),
		DetectMode:   algorithmConfig.DetectMode,
		OccurredAt:   time.Now(),
		Usage:        usage,
	}); usageErr != nil {
		log.Printf("record algorithm test llm usage failed: algorithm_id=%s call_id=%s err=%v", algorithmID, usage.CallID, usageErr)
	}
}

func (s *Server) listAlgorithmTests(c *gin.Context) {
	algorithmID := c.Param("id")
	page := parsePositiveInt(c.Query("page"), 1)
	pageSize := parsePositiveInt(c.Query("page_size"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if page < 1 {
		page = 1
	}

	var algorithm model.Algorithm
	if err := s.db.Select("id", "detect_mode").Where("id = ?", algorithmID).First(&algorithm).Error; err != nil {
		s.fail(c, http.StatusNotFound, "algorithm not found")
		return
	}

	var total int64
	if err := s.db.Model(&model.AlgorithmTestRecord{}).Where("algorithm_id = ?", algorithmID).Count(&total).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "count algorithm tests failed")
		return
	}
	offset := (page - 1) * pageSize
	var items []model.AlgorithmTestRecord
	if err := s.db.Where("algorithm_id = ?", algorithmID).
		Order("created_at desc").
		Limit(pageSize).
		Offset(offset).
		Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query algorithm tests failed")
		return
	}
	type outItem struct {
		model.AlgorithmTestRecord
		FileName        string                   `json:"file_name"`
		MediaURL        string                   `json:"media_url"`
		Basis           string                   `json:"basis"`
		Conclusion      string                   `json:"conclusion"`
		NormalizedBoxes []normalizedBox          `json:"normalized_boxes"`
		AnomalyTimes    []ai.SequenceAnomalyTime `json:"anomaly_times"`
		DurationSeconds float64                  `json:"duration_seconds"`
	}
	out := make([]outItem, 0, len(items))
	for _, item := range items {
		view := buildAlgorithmTestListView(item, algorithm.DetectMode)
		item.Success = view.Success
		out = append(out, outItem{
			AlgorithmTestRecord: item,
			FileName:            resolveAlgorithmTestRecordFileName(item),
			MediaURL:            s.algorithmTestMediaURL(resolveAlgorithmTestRecordMediaPath(item)),
			Basis:               view.Basis,
			Conclusion:          view.Conclusion,
			NormalizedBoxes:     view.Boxes,
			AnomalyTimes:        view.AnomalyTimes,
			DurationSeconds:     view.DurationSeconds,
		})
	}

	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	s.ok(c, gin.H{
		"items":       out,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	})
}

func (s *Server) clearAlgorithmTests(c *gin.Context) {
	algorithmID := strings.TrimSpace(c.Param("id"))
	if algorithmID == "" {
		s.fail(c, http.StatusBadRequest, "algorithm id is required")
		return
	}

	var algorithm model.Algorithm
	if err := s.db.Select("id").Where("id = ?", algorithmID).First(&algorithm).Error; err != nil {
		s.fail(c, http.StatusNotFound, "algorithm not found")
		return
	}

	var records []model.AlgorithmTestRecord
	if err := s.db.Select("id", "image_path", "media_path").Where("algorithm_id = ?", algorithmID).Find(&records).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query algorithm tests failed")
		return
	}
	if len(records) == 0 {
		s.ok(c, gin.H{
			"algorithm_id":    algorithmID,
			"deleted_records": 0,
			"deleted_files":   0,
		})
		return
	}

	if err := s.db.Where("algorithm_id = ?", algorithmID).Delete(&model.AlgorithmTestRecord{}).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "clear algorithm tests failed")
		return
	}

	deletedFiles := 0
	for _, record := range records {
		mediaPath := resolveAlgorithmTestRecordMediaPath(record)
		if deleted, err := s.removeAlgorithmTestMedia(mediaPath); err == nil && deleted {
			deletedFiles++
		}
	}

	s.ok(c, gin.H{
		"algorithm_id":    algorithmID,
		"deleted_records": len(records),
		"deleted_files":   deletedFiles,
	})
}

func (s *Server) getAlgorithmTestMedia(c *gin.Context) {
	rawPath := strings.TrimPrefix(c.Param("path"), "/")
	rawPath = filepath.Clean(rawPath)
	fullPath := filepath.Join(algorithmTestMediaRootDir, rawPath)
	absDir, _ := filepath.Abs(algorithmTestMediaRootDir)
	absTarget, _ := filepath.Abs(fullPath)
	if !strings.HasPrefix(absTarget, absDir) {
		s.fail(c, http.StatusBadRequest, "invalid media path")
		return
	}
	body, err := os.ReadFile(fullPath)
	if err != nil {
		s.fail(c, http.StatusNotFound, "media not found")
		return
	}
	mimeType := http.DetectContentType(body)
	if mimeType == "application/octet-stream" {
		mimeType = mime.TypeByExtension(filepath.Ext(fullPath))
		if strings.TrimSpace(mimeType) == "" {
			mimeType = "application/octet-stream"
		}
	}
	c.Data(http.StatusOK, mimeType, body)
}

func (s *Server) getAlgorithmTestImage(c *gin.Context) {
	s.getAlgorithmTestMedia(c)
}

func (s *Server) removeAlgorithmTestImage(rawPath string) (bool, error) {
	return s.removeAlgorithmTestMedia(rawPath)
}

func (s *Server) removeAlgorithmTestMedia(rawPath string) (bool, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return false, nil
	}
	cleanPath := filepath.Clean(rawPath)
	if cleanPath == "." || cleanPath == "" {
		return false, nil
	}

	baseDir := algorithmTestMediaRootDir
	fullPath := filepath.Join(baseDir, cleanPath)
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return false, err
	}
	absTarget, err := filepath.Abs(fullPath)
	if err != nil {
		return false, err
	}
	if absTarget != absBase && !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) {
		return false, nil
	}

	if err := os.Remove(absTarget); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	for parent := filepath.Dir(absTarget); parent != "" && parent != absBase; parent = filepath.Dir(parent) {
		if err := os.Remove(parent); err != nil {
			break
		}
	}
	return true, nil
}

func (s *Server) tryDeleteReplacedAlgorithmCover(oldImageURL, newImageURL string) {
	oldRel := normalizeCoverImageRelPath(oldImageURL)
	if oldRel == "" {
		return
	}
	newRel := normalizeCoverImageRelPath(newImageURL)
	if oldRel == newRel {
		return
	}
	referenced, err := s.loadReferencedCoverPaths()
	if err != nil {
		log.Printf("cleanup replaced cover skipped: load references failed old=%s err=%v", oldRel, err)
		return
	}
	if _, ok := referenced[oldRel]; ok {
		return
	}
	if _, err := s.removeAlgorithmCoverImage(oldRel); err != nil {
		log.Printf("cleanup replaced cover failed: old=%s err=%v", oldRel, err)
	}
}

func (s *Server) removeAlgorithmCoverImage(rawPath string) (bool, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return false, nil
	}
	cleanPath := filepath.Clean(rawPath)
	if cleanPath == "." || cleanPath == "" {
		return false, nil
	}

	baseDir := filepath.Join("configs", "cover")
	fullPath := filepath.Join(baseDir, cleanPath)
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return false, err
	}
	absTarget, err := filepath.Abs(fullPath)
	if err != nil {
		return false, err
	}
	if absTarget != absBase && !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) {
		return false, nil
	}

	if err := os.Remove(absTarget); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	parent := filepath.Dir(absTarget)
	if parent != "" && parent != absBase {
		_ = os.Remove(parent)
	}
	return true, nil
}

func (s *Server) getAlgorithmCoverImage(c *gin.Context) {
	rawPath := strings.TrimPrefix(c.Param("path"), "/")
	rawPath = filepath.Clean(rawPath)
	fullPath := filepath.Join("configs", "cover", rawPath)
	absDir, _ := filepath.Abs(filepath.Join("configs", "cover"))
	absTarget, _ := filepath.Abs(fullPath)
	if absTarget != absDir && !strings.HasPrefix(absTarget, absDir+string(filepath.Separator)) {
		s.fail(c, http.StatusBadRequest, "invalid image path")
		return
	}
	body, err := os.ReadFile(fullPath)
	if err != nil {
		s.fail(c, http.StatusNotFound, "image not found")
		return
	}
	mimeType := http.DetectContentType(body)
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "application/octet-stream"
	}
	c.Data(http.StatusOK, mimeType, body)
}

func imageExtensionByMIME(mimeType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/gif":
		return ".gif", true
	case "image/webp":
		return ".webp", true
	case "image/bmp":
		return ".bmp", true
	default:
		return "", false
	}
}

func (s *Server) buildAlgorithmModel(id string, in algorithmUpsertRequest) (model.Algorithm, error) {
	m := model.AlgorithmModeHybrid
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return model.Algorithm{}, errBadRequest("name is required")
	}
	code := normalizeAlgorithmCode(in.Code)
	if code == "" {
		return model.Algorithm{}, errBadRequest("code is required")
	}
	if !algorithmCodePattern.MatchString(code) {
		return model.Algorithm{}, errBadRequest("code format invalid: use ^[A-Z][A-Z0-9_]{1,31}$")
	}
	detectMode := normalizeAlgorithmDetectMode(in.DetectMode)
	labels := normalizeSmallModelLabels([]string(in.SmallModelLabel))
	if detectMode == model.AlgorithmDetectModeSmallOnly || detectMode == model.AlgorithmDetectModeHybrid {
		if len(labels) == 0 {
			return model.Algorithm{}, errBadRequest("small_model_label is required")
		}
	}
	if detectMode == model.AlgorithmDetectModeLLMOnly {
		labels = []string{}
	}
	yoloThreshold := clamp(in.YoloThreshold, 0.01, 0.99, 0.5)
	iouThreshold := clamp(in.IOUThreshold, 0.1, 0.99, 0.8)
	return model.Algorithm{
		ID:                id,
		Code:              code,
		Name:              name,
		Description:       strings.TrimSpace(in.Description),
		ImageURL:          strings.TrimSpace(in.ImageURL),
		Scene:             strings.TrimSpace(in.Scene),
		Category:          strings.TrimSpace(in.Category),
		Mode:              m,
		Enabled:           in.Enabled,
		SmallModelLabel:   strings.Join(labels, ","),
		DetectMode:        detectMode,
		YoloThreshold:     yoloThreshold,
		IOUThreshold:      iouThreshold,
		LabelsTriggerMode: normalizeLabelsTriggerMode(in.LabelsTriggerMode),
		ModelProviderID:   "",
	}, nil
}

func normalizeAlgorithmCode(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func normalizeLabelsTriggerMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case model.LabelsTriggerModeAll:
		return model.LabelsTriggerModeAll
	default:
		return model.LabelsTriggerModeAny
	}
}

func normalizeAlgorithmDetectMode(raw int) int {
	switch raw {
	case model.AlgorithmDetectModeSmallOnly:
		return model.AlgorithmDetectModeSmallOnly
	case model.AlgorithmDetectModeLLMOnly:
		return model.AlgorithmDetectModeLLMOnly
	case model.AlgorithmDetectModeHybrid:
		return model.AlgorithmDetectModeHybrid
	default:
		return model.AlgorithmDetectModeHybrid
	}
}

func resolveAlgorithmDetectMode(algorithm model.Algorithm) int {
	mode := normalizeAlgorithmDetectMode(algorithm.DetectMode)
	if algorithm.DetectMode != mode {
		switch strings.ToLower(strings.TrimSpace(algorithm.Mode)) {
		case model.AlgorithmModeSmall:
			return model.AlgorithmDetectModeSmallOnly
		case model.AlgorithmModeLarge:
			return model.AlgorithmDetectModeLLMOnly
		case model.AlgorithmModeHybrid:
			return model.AlgorithmDetectModeHybrid
		}
	}
	return mode
}

func normalizePromptVersion(raw string) string {
	return strings.TrimSpace(raw)
}

func (s *Server) ensureAlgorithmNameUnique(name, excludeID string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errBadRequest("name is required")
	}
	query := s.db.Model(&model.Algorithm{}).Where("name = ?", name)
	excludeID = strings.TrimSpace(excludeID)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errBadRequest("算法名称不能重复")
	}
	return nil
}

func (s *Server) ensureAlgorithmCodeUnique(code, excludeID string) error {
	code = normalizeAlgorithmCode(code)
	if code == "" {
		return errBadRequest("code is required")
	}
	query := s.db.Model(&model.Algorithm{}).Where("code = ?", code)
	excludeID = strings.TrimSpace(excludeID)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errBadRequest("code already exists")
	}
	return nil
}

func (s *Server) generateCamera2AlgorithmCode() (string, error) {
	const prefix = "CAM2ALG_"
	for attempt := 0; attempt < 10; attempt++ {
		candidate := fmt.Sprintf("%s%s", prefix, strings.ToUpper(uuid.NewString()[:8]))
		if len(candidate) > 32 {
			candidate = candidate[:32]
		}
		if !algorithmCodePattern.MatchString(candidate) {
			continue
		}
		if err := s.ensureAlgorithmCodeUnique(candidate, ""); err == nil {
			return candidate, nil
		} else if !isBadRequestError(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("generate unique algorithm code exhausted")
}

func (s *Server) createAlgorithmPromptIfNeeded(algorithmID string, in algorithmUpsertRequest) (*model.AlgorithmPromptVersion, error) {
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return nil, nil
	}
	version := normalizePromptVersion(in.PromptVersion)
	if version == "" {
		version = "v1"
	}
	isActive := true
	if in.ActivatePrompt != nil {
		isActive = *in.ActivatePrompt
	}
	if err := s.ensurePromptVersionUnique(nil, algorithmID, version, ""); err != nil {
		return nil, err
	}
	return &model.AlgorithmPromptVersion{
		ID:          uuid.NewString(),
		AlgorithmID: strings.TrimSpace(algorithmID),
		Version:     version,
		Prompt:      prompt,
		IsActive:    isActive,
	}, nil
}

func (s *Server) ensurePromptVersionUnique(tx *gorm.DB, algorithmID, version, excludePromptID string) error {
	if tx == nil {
		tx = s.db
	}
	algorithmID = strings.TrimSpace(algorithmID)
	version = normalizePromptVersion(version)
	if algorithmID == "" {
		return errBadRequest("algorithm id is required")
	}
	if version == "" {
		return errBadRequest("version and prompt are required")
	}
	query := tx.Model(&model.AlgorithmPromptVersion{}).
		Where("algorithm_id = ? AND version = ?", algorithmID, version)
	excludePromptID = strings.TrimSpace(excludePromptID)
	if excludePromptID != "" {
		query = query.Where("id <> ?", excludePromptID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errBadRequest("version already exists in this algorithm")
	}
	return nil
}

func (s *Server) buildAlgorithmTestConfig(algorithm model.Algorithm) (ai.StartCameraAlgorithmConfig, string, model.ModelProvider, error) {
	mode := resolveAlgorithmDetectMode(algorithm)
	labels := []string{}
	if mode == model.AlgorithmDetectModeSmallOnly || mode == model.AlgorithmDetectModeHybrid {
		labels = normalizeSmallModelLabels([]string{algorithm.SmallModelLabel})
		if len(labels) == 0 {
			return ai.StartCameraAlgorithmConfig{}, "", model.ModelProvider{}, errBadRequest("small_model_label is required")
		}
	}
	algorithmCode := resolveAlgorithmTaskCode(algorithm)
	if algorithmCode == "" {
		return ai.StartCameraAlgorithmConfig{}, "", model.ModelProvider{}, errBadRequest("algorithm code is required")
	}
	algorithmConfig := ai.StartCameraAlgorithmConfig{
		AlgorithmID:       algorithm.ID,
		TaskCode:          algorithmCode,
		DetectMode:        mode,
		Labels:            labels,
		YoloThreshold:     clamp(algorithm.YoloThreshold, 0.01, 0.99, 0.5),
		IOUThreshold:      clamp(algorithm.IOUThreshold, 0.1, 0.99, 0.8),
		LabelsTriggerMode: normalizeLabelsTriggerMode(algorithm.LabelsTriggerMode),
	}

	provider := model.ModelProvider{}
	prompt := ""
	if mode == model.AlgorithmDetectModeLLMOnly || mode == model.AlgorithmDetectModeHybrid {
		configuredProvider, err := s.getConfiguredLLMProvider()
		if err != nil {
			return ai.StartCameraAlgorithmConfig{}, "", model.ModelProvider{}, errBadRequest(err.Error())
		}
		provider = configuredProvider
		prompt, err = s.buildAlgorithmImageTestPrompt(algorithm)
		if err != nil {
			return ai.StartCameraAlgorithmConfig{}, "", model.ModelProvider{}, err
		}
	}
	return algorithmConfig, prompt, provider, nil
}

func (s *Server) buildAlgorithmImageTestPrompt(algorithm model.Algorithm) (string, error) {
	mode := resolveAlgorithmDetectMode(algorithm)
	if mode != model.AlgorithmDetectModeLLMOnly && mode != model.AlgorithmDetectModeHybrid {
		return "", nil
	}
	activePrompt, err := s.getActivePrompt(algorithm.ID)
	if err != nil || activePrompt == nil {
		return "", errBadRequest("active prompt not found")
	}
	return s.composeAlgorithmTestPrompt(strings.TrimSpace(algorithm.Name), "图片测试", strings.TrimSpace(activePrompt.Prompt))
}

func (s *Server) composeAlgorithmTestPrompt(name, mode, goal string) (string, error) {
	role := strings.TrimSpace(s.readPromptMarkdown("./configs/llm/algorithm_test_image_role.md"))
	if strings.TrimSpace(mode) == "视频测试" {
		role = strings.TrimSpace(s.readPromptMarkdown("./configs/llm/algorithm_test_video_role.md"))
	}
	if role == "" {
		role = strings.TrimSpace(s.getSetting("llm_role"))
	}
	if role == "" {
		role = defaultLLMRoleText()
	}
	outputRequirement := strings.TrimSpace(s.readPromptMarkdown("./configs/llm/algorithm_test_image_output_requirement.md"))
	if strings.TrimSpace(mode) == "视频测试" {
		outputRequirement = strings.TrimSpace(s.readPromptMarkdown("./configs/llm/algorithm_test_video_output_requirement.md"))
	}
	if outputRequirement == "" {
		outputRequirement = strings.TrimSpace(s.getSetting("llm_output_requirement"))
	}
	if outputRequirement == "" {
		outputRequirement = defaultLLMOutputRequirementText()
	}
	return composeAlgorithmSingleTaskPrompt(role, outputRequirement, algorithmTestPromptSpec{
		Name: strings.TrimSpace(name),
		Mode: strings.TrimSpace(mode),
		Goal: strings.TrimSpace(goal),
	})
}

func (s *Server) buildAlgorithmVideoTestPrompt(algorithm model.Algorithm) (string, error) {
	mode := resolveAlgorithmDetectMode(algorithm)
	if mode != model.AlgorithmDetectModeLLMOnly && mode != model.AlgorithmDetectModeHybrid {
		return "", nil
	}
	activePrompt, err := s.getActivePrompt(algorithm.ID)
	if err != nil || activePrompt == nil {
		return "", errBadRequest("active prompt not found")
	}
	return s.composeAlgorithmTestPrompt(strings.TrimSpace(algorithm.Name), "视频测试", strings.TrimSpace(activePrompt.Prompt))
}

func composeAlgorithmSingleTaskPrompt(role, outputRequirement string, spec algorithmTestPromptSpec) (string, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		role = defaultLLMRoleText()
	}
	outputRequirement = strings.TrimSpace(outputRequirement)
	if outputRequirement == "" {
		outputRequirement = defaultLLMOutputRequirementText()
	}
	taskName := strings.TrimSpace(spec.Name)
	if taskName == "" {
		taskName = "未命名任务"
	}
	taskMode := strings.TrimSpace(spec.Mode)
	if taskMode == "" {
		taskMode = "单任务测试"
	}
	taskGoal := strings.TrimSpace(spec.Goal)
	if taskGoal == "" {
		return "", errBadRequest("active prompt not found")
	}
	prompt := strings.TrimSpace(fmt.Sprintf(
		"%s\n\n## [当前测试任务]\n任务名称：%s\n任务场景：%s\n识别目标：%s\n\n## [输出JSON协议]\n%s",
		role,
		taskName,
		taskMode,
		taskGoal,
		outputRequirement,
	))
	return prompt, nil
}

type algorithmTestView struct {
	Success         bool
	Conclusion      string
	Basis           string
	ErrorMessage    string
	Boxes           []normalizedBox
	AnomalyTimes    []ai.SequenceAnomalyTime
	DurationSeconds float64
}

func normalizeSmallModelLabels(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		parts := strings.Split(item, ",")
		for _, part := range parts {
			v := strings.TrimSpace(part)
			if v == "" {
				continue
			}
			key := strings.ToLower(v)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func buildAlgorithmTestInsight(resp *ai.AnalyzeImageResponse, success bool) (string, []normalizedBox, int, int) {
	view := buildAlgorithmImageTestView(resp, success, ai.StartCameraAlgorithmConfig{}, 0, 0)
	return view.Conclusion, view.Boxes, 0, 0
}

func buildAlgorithmTestInsightFromPayload(raw string, success bool) (string, []normalizedBox, int, int) {
	payload := strings.TrimSpace(raw)
	if payload == "" || strings.EqualFold(payload, "null") {
		if success {
			return "分析成功", nil, 0, 0
		}
		return "分析失败", nil, 0, 0
	}
	var resp ai.AnalyzeImageResponse
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		if success {
			return "分析成功", nil, 0, 0
		}
		return "分析失败", nil, 0, 0
	}
	return buildAlgorithmTestInsight(&resp, success)
}

func buildAlgorithmImageTestView(resp *ai.AnalyzeImageResponse, success bool, cfg ai.StartCameraAlgorithmConfig, imageWidth, imageHeight int) algorithmTestView {
	if resp == nil {
		if success {
			return algorithmTestView{Success: true, Conclusion: "分析成功"}
		}
		basis := "AI 未返回有效结果"
		return algorithmTestView{Success: false, Conclusion: "分析失败", Basis: basis, ErrorMessage: basis}
	}
	llmPayload := parseAlgorithmTestImageLLMPayload(strings.TrimSpace(resp.LLMResult))
	detections := parseAlgorithmTestDetections(resp.Detections)
	boxes := selectAlgorithmTestImageBoxes(llmPayload.toBoxes(), detections, imageWidth, imageHeight, cfg.DetectMode)
	view := resolveAlgorithmImageTestOutcome(resp, success, cfg, detections, llmPayload)
	view.Boxes = boxes
	return view
}

func buildAlgorithmTestViewFromPayload(raw string, success bool, detectMode int, mediaType string) algorithmTestView {
	payload := strings.TrimSpace(raw)
	if payload == "" || strings.EqualFold(payload, "null") {
		if success {
			return algorithmTestView{Success: true, Conclusion: "分析成功"}
		}
		return algorithmTestView{Success: false, Conclusion: "分析失败"}
	}

	var rawObj map[string]any
	imageWidth := 0
	imageHeight := 0
	if err := json.Unmarshal([]byte(payload), &rawObj); err == nil {
		normalizedMediaType := strings.ToLower(strings.TrimSpace(mediaType))
		if normalizedMediaType == string(algorithmTestMediaTypeVideo) {
			return buildAlgorithmVideoTestViewFromPayload(rawObj, success)
		}
		if normalizedMediaType == string(algorithmTestMediaTypeImage) || normalizedMediaType == "" {
			imageWidth = int(floatValue(rawObj["snapshot_width"]))
			imageHeight = int(floatValue(rawObj["snapshot_height"]))
		}
	}

	var resp ai.AnalyzeImageResponse
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		if success {
			return algorithmTestView{Success: true, Conclusion: "分析成功"}
		}
		return algorithmTestView{Success: false, Conclusion: "分析失败"}
	}
	return buildAlgorithmImageTestView(&resp, success, ai.StartCameraAlgorithmConfig{DetectMode: detectMode}, imageWidth, imageHeight)
}

func selectAlgorithmTestImageBoxes(llmBoxes []normalizedBox, detections []aiTestDetection, width int, height int, detectMode int) []normalizedBox {
	switch detectMode {
	case model.AlgorithmDetectModeLLMOnly, model.AlgorithmDetectModeHybrid:
		return llmBoxes
	default:
		if len(llmBoxes) > 0 {
			return llmBoxes
		}
		return normalizeAlgorithmTestDetections(detections, width, height)
	}
}

func parseAlgorithmTestDetections(raw json.RawMessage) []aiTestDetection {
	if len(raw) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return nil
	}
	var out []aiTestDetection
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func parseAlgorithmTestImageLLMPayload(raw string) algorithmTestImageLLMPayload {
	out := algorithmTestImageLLMPayload{Alarm: "0"}
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

	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err == nil {
		out.Alarm = normalizeAlarmValue(payload["alarm"])
		out.Conclusion = strings.TrimSpace(stringValue(payload["conclusion"]))
		out.Result = strings.TrimSpace(stringValue(payload["result"]))
		out.Reason = strings.TrimSpace(stringValue(payload["reason"]))
		out.Targets = parseAlgorithmTestImageTargets(payload["targets"])
		if len(out.Targets) == 0 {
			out.Targets = parseAlgorithmTestImageTargets(payload["objects"])
		}
	}

	legacy := parseLLMResult(raw)
	if out.Alarm == "0" {
		for _, item := range legacy.TaskResults {
			if normalizeAlarmValue(item.Alarm) == "1" {
				out.Alarm = "1"
				break
			}
		}
	}
	if out.Reason == "" {
		for _, item := range legacy.TaskResults {
			if strings.TrimSpace(item.Reason) != "" {
				out.Reason = strings.TrimSpace(item.Reason)
				break
			}
		}
	}
	if len(out.Targets) == 0 && len(legacy.Objects) > 0 {
		out.Targets = make([]algorithmTestImageLLMTarget, 0, len(legacy.Objects))
		for _, item := range legacy.Objects {
			if len(item.BBox2D) < 4 {
				continue
			}
			out.Targets = append(out.Targets, algorithmTestImageLLMTarget{
				Label:      strings.TrimSpace(item.Label),
				Confidence: item.Confidence,
				BBox2D: []float64{
					item.BBox2D[0],
					item.BBox2D[1],
					item.BBox2D[2],
					item.BBox2D[3],
				},
			})
		}
	}
	if out.Conclusion == "" {
		out.Conclusion = strings.TrimSpace(out.Result)
	}
	if out.Conclusion == "" {
		if out.Alarm == "1" {
			out.Conclusion = "图片判定触发异常"
		} else {
			out.Conclusion = "图片判定未触发"
		}
	}
	return out
}

type algorithmTestVideoLLMPayload struct {
	Alarm        string
	Reason       string
	AnomalyTimes []ai.SequenceAnomalyTime
}

func parseAlgorithmTestVideoLLMPayload(raw string) algorithmTestVideoLLMPayload {
	out := algorithmTestVideoLLMPayload{Alarm: "0"}
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

	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return out
	}
	out.Alarm = normalizeAlarmValue(payload["alarm"])
	out.Reason = strings.TrimSpace(stringValue(payload["reason"]))
	out.AnomalyTimes = parseAlgorithmTestAnomalyTimes(payload["anomaly_times"])
	sort.SliceStable(out.AnomalyTimes, func(i, j int) bool {
		return out.AnomalyTimes[i].TimestampMS < out.AnomalyTimes[j].TimestampMS
	})
	if out.Alarm != "1" && len(out.AnomalyTimes) > 0 {
		out.Alarm = "1"
	}
	return out
}

func parseAlgorithmTestImageTargets(raw any) []algorithmTestImageLLMTarget {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	out := make([]algorithmTestImageLLMTarget, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		target := algorithmTestImageLLMTarget{
			Label:      strings.TrimSpace(stringValue(row["label"])),
			Confidence: floatValue(row["confidence"]),
			BBox2D:     parseBBox2D(row["bbox2d"]),
		}
		if len(target.BBox2D) < 4 {
			continue
		}
		out = append(out, target)
	}
	return out
}

func parseBBox2D(raw any) []float64 {
	items, ok := raw.([]any)
	if !ok || len(items) < 4 {
		return nil
	}
	out := make([]float64, 0, 4)
	for _, item := range items[:4] {
		out = append(out, floatValue(item))
	}
	return out
}

func (p algorithmTestImageLLMPayload) toBoxes() []normalizedBox {
	if len(p.Targets) == 0 {
		return nil
	}
	out := make([]normalizedBox, 0, len(p.Targets))
	for _, item := range p.Targets {
		box, ok := normalizeAlgorithmTestBBox2D(item)
		if !ok {
			continue
		}
		out = append(out, box)
	}
	return out
}

func normalizeAlgorithmTestBBox2D(item algorithmTestImageLLMTarget) (normalizedBox, bool) {
	if len(item.BBox2D) < 4 {
		return normalizedBox{}, false
	}
	scale := 1000.0
	maxCoord := 0.0
	for _, value := range item.BBox2D[:4] {
		if value > maxCoord {
			maxCoord = value
		}
	}
	if maxCoord <= 1.0 {
		scale = 1.0
	}
	x0 := clamp01(item.BBox2D[0] / scale)
	y0 := clamp01(item.BBox2D[1] / scale)
	x1 := clamp01(item.BBox2D[2] / scale)
	y1 := clamp01(item.BBox2D[3] / scale)
	if x1 <= x0 || y1 <= y0 {
		return normalizedBox{}, false
	}
	confidence := item.Confidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	label := strings.TrimSpace(item.Label)
	if label == "" {
		label = "target"
	}
	return normalizedBox{
		Label:      label,
		Confidence: confidence,
		X:          (x0 + x1) / 2,
		Y:          (y0 + y1) / 2,
		W:          x1 - x0,
		H:          y1 - y0,
	}, true
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func readAlgorithmTestImageSize(imagePath string) (int, int) {
	imagePath = strings.TrimSpace(imagePath)
	if imagePath == "" {
		return 0, 0
	}
	fp, err := os.Open(imagePath)
	if err != nil {
		return 0, 0
	}
	defer fp.Close()
	cfg, _, err := image.DecodeConfig(fp)
	if err != nil {
		return 0, 0
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func normalizeAlgorithmTestDetections(items []aiTestDetection, width, height int) []normalizedBox {
	if len(items) == 0 {
		return nil
	}
	out := make([]normalizedBox, 0, len(items))
	for _, item := range items {
		w := item.NormBox.W
		h := item.NormBox.H
		x := item.NormBox.X
		y := item.NormBox.Y
		if !(w > 0 && h > 0) {
			if width <= 0 || height <= 0 {
				continue
			}
			w = float64(item.Box.XMax-item.Box.XMin) / float64(width)
			h = float64(item.Box.YMax-item.Box.YMin) / float64(height)
			x = float64(item.Box.XMin+item.Box.XMax) / 2 / float64(width)
			y = float64(item.Box.YMin+item.Box.YMax) / 2 / float64(height)
		}
		out = append(out, normalizedBox{
			Label:      strings.TrimSpace(item.Label),
			Confidence: item.Confidence,
			X:          clamp01(x),
			Y:          clamp01(y),
			W:          clamp01(w),
			H:          clamp01(h),
		})
	}
	return out
}

func summarizeAlgorithmTestConclusion(success bool, resp *ai.AnalyzeImageResponse, detections []aiTestDetection, llmPayload algorithmTestImageLLMPayload) string {
	if !success {
		msg := ""
		if resp != nil {
			msg = strings.TrimSpace(resp.Message)
		}
		if msg != "" {
			return "分析失败: " + msg
		}
		return "分析失败"
	}
	if strings.TrimSpace(llmPayload.Conclusion) != "" {
		return strings.TrimSpace(llmPayload.Conclusion)
	}
	if len(detections) > 0 {
		countByLabel := make(map[string]int, len(detections))
		labelOrder := make([]string, 0, len(detections))
		for _, det := range detections {
			label := strings.TrimSpace(det.Label)
			if label == "" {
				label = "unknown"
			}
			if _, ok := countByLabel[label]; !ok {
				labelOrder = append(labelOrder, label)
			}
			countByLabel[label]++
		}
		parts := make([]string, 0, len(labelOrder))
		for _, label := range labelOrder {
			parts = append(parts, fmt.Sprintf("%s x%d", label, countByLabel[label]))
		}
		return fmt.Sprintf("检测到 %d 个目标: %s", len(detections), strings.Join(parts, ", "))
	}
	llm := ""
	if resp != nil {
		llm = strings.TrimSpace(resp.LLMResult)
	}
	if llm != "" {
		parsed := parseLLMResult(llm)
		if len(parsed.TaskResults) > 0 {
			triggered := 0
			for _, item := range parsed.TaskResults {
				if normalizeAlarmValue(item.Alarm) == "1" {
					triggered++
				}
			}
			if triggered > 0 {
				return fmt.Sprintf("大模型判定触发: %d/%d 项", triggered, len(parsed.TaskResults))
			}
			return fmt.Sprintf("大模型判定未触发: %d 项", len(parsed.TaskResults))
		}
		return "大模型已返回结果"
	}
	return "未检测到目标"
}

func buildAlgorithmTestVideoViewLegacy(payload map[string]any, success bool) algorithmTestView {
	conclusion := strings.TrimSpace(stringValue(payload["conclusion"]))
	basis := strings.TrimSpace(stringValue(payload["basis"]))
	anomalyTimes := parseAlgorithmTestAnomalyTimes(payload["anomaly_times"])
	if basis == "" && len(anomalyTimes) == 0 {
		llm := parseAlgorithmTestVideoLLMPayload(strings.TrimSpace(stringValue(payload["llm_result"])))
		if llm.Alarm != "1" && len(llm.AnomalyTimes) > 0 {
			llm.Alarm = "1"
		}
		basis = strings.TrimSpace(llm.Reason)
		if basis == "" {
			if len(llm.AnomalyTimes) > 0 {
				basis = strings.TrimSpace(llm.AnomalyTimes[0].Reason)
			} else {
				basis = "未检测到异常"
			}
		}
		anomalyTimes = llm.AnomalyTimes
		if conclusion == "" {
			if llm.Alarm == "1" {
				conclusion = "视频判定触发异常"
			} else {
				conclusion = "视频判定未触发"
			}
		}
	}
	if conclusion == "" {
		if success {
			conclusion = "视频分析完成"
		} else {
			conclusion = "分析失败"
		}
	}
	return algorithmTestView{
		Success:         success,
		Conclusion:      conclusion,
		Basis:           basis,
		AnomalyTimes:    anomalyTimes,
		DurationSeconds: floatValue(payload["duration_seconds"]),
	}
}

func buildAlgorithmVideoTestViewFromPayload(payload map[string]any, success bool) algorithmTestView {
	legacy := buildAlgorithmTestVideoViewLegacy(payload, success)
	llmRaw := strings.TrimSpace(stringValue(payload["llm_result"]))
	if llmRaw == "" {
		return legacy
	}
	if strings.TrimSpace(legacy.Basis) != "" || len(legacy.AnomalyTimes) > 0 {
		return legacy
	}

	llm := parseAlgorithmTestVideoLLMPayload(llmRaw)
	if llm.Alarm != "1" && len(llm.AnomalyTimes) > 0 {
		llm.Alarm = "1"
	}
	basis := strings.TrimSpace(llm.Reason)
	if basis == "" {
		if len(llm.AnomalyTimes) > 0 {
			basis = strings.TrimSpace(llm.AnomalyTimes[0].Reason)
		} else {
			basis = "未检测到异常"
		}
	}
	conclusion := strings.TrimSpace(legacy.Conclusion)
	if conclusion == "" {
		if !success {
			conclusion = "分析失败"
		} else if llm.Alarm == "1" {
			conclusion = "视频判定触发异常"
		} else {
			conclusion = "视频判定未触发"
		}
	}
	legacy.Conclusion = conclusion
	legacy.Basis = basis
	legacy.AnomalyTimes = llm.AnomalyTimes
	return legacy
}

// 图片算法测试要按“最终判定是否完成”收敛结果，避免 LLM 失败时误展示成 YOLO 摘要。
func resolveAlgorithmImageTestOutcome(
	resp *ai.AnalyzeImageResponse,
	success bool,
	cfg ai.StartCameraAlgorithmConfig,
	detections []aiTestDetection,
	llmPayload algorithmTestImageLLMPayload,
) algorithmTestView {
	if resp == nil {
		if success {
			return algorithmTestView{Success: true, Conclusion: "分析成功"}
		}
		basis := "AI 未返回有效结果"
		return algorithmTestView{Success: false, Conclusion: "分析失败", Basis: basis, ErrorMessage: basis}
	}
	if !success {
		basis := "AI 未返回有效结果"
		conclusion := "分析失败"
		if msg := strings.TrimSpace(resp.Message); msg != "" {
			basis = msg
			conclusion = "分析失败: " + msg
		}
		return algorithmTestView{
			Success:      false,
			Conclusion:   conclusion,
			Basis:        basis,
			ErrorMessage: basis,
		}
	}
	if failure, ok := buildAlgorithmImageLLMFailureView(resp, cfg, detections, llmPayload); ok {
		return failure
	}
	return algorithmTestView{
		Success:    true,
		Conclusion: summarizeAlgorithmTestConclusion(success, resp, detections, llmPayload),
		Basis:      summarizeAlgorithmTestBasis(success, resp, cfg, detections, llmPayload),
	}
}

func buildAlgorithmImageLLMFailureView(
	resp *ai.AnalyzeImageResponse,
	cfg ai.StartCameraAlgorithmConfig,
	detections []aiTestDetection,
	llmPayload algorithmTestImageLLMPayload,
) (algorithmTestView, bool) {
	callStatus := ""
	errorMessage := ""
	if resp != nil && resp.LLMUsage != nil {
		callStatus = strings.ToLower(strings.TrimSpace(resp.LLMUsage.CallStatus))
		errorMessage = strings.TrimSpace(resp.LLMUsage.ErrorMessage)
	}
	if !shouldFailAlgorithmImageOnLLMResult(cfg, resp, detections, llmPayload, callStatus, errorMessage) {
		return algorithmTestView{}, false
	}
	basis := "大模型未返回有效结果"
	if callStatus == "error" {
		basis = "大模型调用失败，未能完成最终判定"
	}
	return algorithmTestView{
		Success:      false,
		Conclusion:   "大模型判定失败",
		Basis:        basis,
		ErrorMessage: normalizeAlgorithmImageLLMErrorMessage(errorMessage, basis),
	}, true
}

func shouldFailAlgorithmImageOnLLMResult(
	cfg ai.StartCameraAlgorithmConfig,
	resp *ai.AnalyzeImageResponse,
	detections []aiTestDetection,
	llmPayload algorithmTestImageLLMPayload,
	callStatus string,
	errorMessage string,
) bool {
	if cfg.DetectMode != model.AlgorithmDetectModeLLMOnly && cfg.DetectMode != model.AlgorithmDetectModeHybrid {
		return false
	}
	algorithmResults := parseAlgorithmTestAlgorithmResults(resp)
	if shouldUseHybridGateMissBasis(cfg, resp, detections, llmPayload, algorithmResults) {
		return false
	}
	if callStatus == "error" || callStatus == "empty_content" {
		return true
	}
	if strings.TrimSpace(errorMessage) != "" {
		return true
	}
	if resp == nil || strings.TrimSpace(resp.LLMResult) != "" {
		return false
	}
	if cfg.DetectMode == model.AlgorithmDetectModeLLMOnly {
		return true
	}
	return len(detections) > 0 ||
		len(algorithmResults) > 0 ||
		strings.TrimSpace(llmPayload.Conclusion) != "" ||
		strings.TrimSpace(llmPayload.Reason) != "" ||
		len(llmPayload.Targets) > 0
}

func normalizeAlgorithmImageLLMErrorMessage(raw string, fallback string) string {
	if msg := strings.TrimSpace(raw); msg != "" {
		return msg
	}
	return strings.TrimSpace(fallback)
}

func summarizeAlgorithmTestBasis(success bool, resp *ai.AnalyzeImageResponse, cfg ai.StartCameraAlgorithmConfig, detections []aiTestDetection, llmPayload algorithmTestImageLLMPayload) string {
	if !success {
		if resp != nil && strings.TrimSpace(resp.Message) != "" {
			return strings.TrimSpace(resp.Message)
		}
		return "AI 未返回有效结果"
	}
	if strings.TrimSpace(llmPayload.Reason) != "" {
		return strings.TrimSpace(llmPayload.Reason)
	}

	algorithmResults := parseAlgorithmTestAlgorithmResults(resp)
	for _, item := range algorithmResults {
		if strings.TrimSpace(cfg.AlgorithmID) == "" || strings.EqualFold(strings.TrimSpace(item.AlgorithmID), strings.TrimSpace(cfg.AlgorithmID)) {
			if reason := strings.TrimSpace(item.Reason); reason != "" {
				return reason
			}
		}
	}

	if resp != nil {
		llm := parseLLMResult(strings.TrimSpace(resp.LLMResult))
		for _, item := range llm.TaskResults {
			if strings.TrimSpace(cfg.TaskCode) == "" || strings.EqualFold(item.TaskCode, strings.TrimSpace(cfg.TaskCode)) {
				if strings.TrimSpace(item.Reason) != "" {
					return strings.TrimSpace(item.Reason)
				}
			}
		}
	}

	if len(detections) > 0 {
		parts := make([]string, 0, len(detections))
		for _, det := range detections {
			label := strings.TrimSpace(det.Label)
			if label == "" {
				label = "unknown"
			}
			parts = append(parts, fmt.Sprintf("%s(%.2f)", label, det.Confidence))
		}
		return "检测目标: " + strings.Join(parts, ", ")
	}
	if resp != nil && strings.TrimSpace(resp.LLMResult) != "" {
		return strings.TrimSpace(resp.LLMResult)
	}
	if shouldUseHybridGateMissBasis(cfg, resp, detections, llmPayload, algorithmResults) {
		return "小模型未检出目标"
	}
	if resp != nil && strings.TrimSpace(resp.Message) != "" {
		return strings.TrimSpace(resp.Message)
	}
	return "未检测到异常目标"
}

func shouldUseHybridGateMissBasis(
	cfg ai.StartCameraAlgorithmConfig,
	resp *ai.AnalyzeImageResponse,
	detections []aiTestDetection,
	llmPayload algorithmTestImageLLMPayload,
	algorithmResults []algorithmTestResultItem,
) bool {
	if cfg.DetectMode != model.AlgorithmDetectModeHybrid {
		return false
	}
	if resp == nil {
		return false
	}
	if len(detections) > 0 || len(algorithmResults) > 0 {
		return false
	}
	if strings.TrimSpace(llmPayload.Reason) != "" || strings.TrimSpace(resp.LLMResult) != "" {
		return false
	}
	return isGenericAISuccessMessage(resp.Message)
}

func isGenericAISuccessMessage(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ok", "success":
		return true
	default:
		return false
	}
}

type algorithmTestResultItem struct {
	AlgorithmID string `json:"algorithm_id"`
	TaskCode    string `json:"task_code"`
	Alarm       any    `json:"alarm"`
	Source      string `json:"source"`
	Reason      string `json:"reason"`
}

func parseAlgorithmTestAlgorithmResults(resp *ai.AnalyzeImageResponse) []algorithmTestResultItem {
	if resp == nil || len(resp.AlgorithmResults) == 0 || strings.EqualFold(strings.TrimSpace(string(resp.AlgorithmResults)), "null") {
		return nil
	}
	var out []algorithmTestResultItem
	if err := json.Unmarshal(resp.AlgorithmResults, &out); err != nil {
		return nil
	}
	return out
}

func (s *Server) getActivePrompt(algorithmID string) (*model.AlgorithmPromptVersion, error) {
	var prompt model.AlgorithmPromptVersion
	if err := s.db.
		Where("algorithm_id = ? AND is_active = ?", algorithmID, true).
		Order("updated_at desc").
		First(&prompt).Error; err != nil {
		return nil, err
	}
	return &prompt, nil
}

type algorithmTestSavedMedia struct {
	MediaType    algorithmTestMediaType
	RelativePath string
	FullPath     string
}

func buildAlgorithmTestListView(record model.AlgorithmTestRecord, detectMode int) algorithmTestView {
	return buildAlgorithmTestViewFromPayload(record.ResponsePayload, record.Success, detectMode, strings.TrimSpace(record.MediaType))
}

func resolveAlgorithmTestRecordMediaPath(record model.AlgorithmTestRecord) string {
	if path := strings.TrimSpace(record.MediaPath); path != "" {
		return path
	}
	return strings.TrimSpace(record.ImagePath)
}

func resolveAlgorithmTestRecordFileName(record model.AlgorithmTestRecord) string {
	if name := strings.TrimSpace(record.OriginalFileName); name != "" {
		return name
	}
	path := resolveAlgorithmTestRecordMediaPath(record)
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

func (s *Server) algorithmTestMediaURL(path string) string {
	normalized := normalizeAlgorithmTestMediaRelPath(path)
	if normalized == "" {
		return ""
	}
	return "/api/v1/algorithms/test-media/" + normalized
}

func normalizeAlgorithmTestMediaRelPath(path string) string {
	return strings.Trim(strings.ReplaceAll(filepath.ToSlash(strings.TrimSpace(path)), "\\", "/"), "/")
}

func (s *Server) saveAlgorithmTestUpload(algorithmID, batchID string, file *multipart.FileHeader) (algorithmTestSavedMedia, error) {
	src, err := file.Open()
	if err != nil {
		return algorithmTestSavedMedia{}, err
	}
	defer src.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(src, head)
	head = head[:n]
	mimeType := http.DetectContentType(head)
	mediaType, ext, err := classifyAlgorithmTestUpload(file.Filename, mimeType)
	if err != nil {
		return algorithmTestSavedMedia{}, err
	}

	now := time.Now()
	datePart := now.Format("20060102")
	dir := filepath.Join(algorithmTestMediaRootDir, datePart, batchID)
	if err := s.ensureDir(dir); err != nil {
		return algorithmTestSavedMedia{}, err
	}
	base := sanitizeAlgorithmTestFileName(strings.TrimSuffix(file.Filename, filepath.Ext(file.Filename)))
	if base == "" {
		base = string(mediaType)
	}
	filename := fmt.Sprintf("%s_%s_%s%s", algorithmID, base, uuid.NewString(), ext)
	fullPath := filepath.Join(dir, filename)
	reader := io.MultiReader(bytes.NewReader(head), src)
	target, err := os.Create(fullPath)
	if err != nil {
		return algorithmTestSavedMedia{}, err
	}
	defer target.Close()
	if _, err := io.Copy(target, reader); err != nil {
		return algorithmTestSavedMedia{}, err
	}
	relPath := filepath.ToSlash(filepath.Join(datePart, batchID, filename))
	return algorithmTestSavedMedia{
		MediaType:    mediaType,
		RelativePath: relPath,
		FullPath:     fullPath,
	}, nil
}

func sanitizeAlgorithmTestFileName(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	value = regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(value, "_")
	value = strings.Trim(value, "._-")
	if len(value) > 48 {
		value = value[:48]
	}
	return value
}

func classifyAlgorithmTestUpload(fileName, mimeType string) (algorithmTestMediaType, string, error) {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(fileName)))
	if strings.HasPrefix(strings.TrimSpace(mimeType), "image/") {
		if ext == "" {
			ext = extensionByMIME(mimeType, ".jpg")
		}
		return algorithmTestMediaTypeImage, ext, nil
	}
	if strings.HasPrefix(strings.TrimSpace(mimeType), "video/") {
		if ext == "" {
			ext = extensionByMIME(mimeType, ".mp4")
		}
		return algorithmTestMediaTypeVideo, ext, nil
	}
	if ext != "" {
		switch ext {
		case ".jpg", ".jpeg", ".png", ".bmp", ".webp":
			return algorithmTestMediaTypeImage, ext, nil
		case ".mp4", ".mov", ".avi", ".mkv", ".webm":
			return algorithmTestMediaTypeVideo, ext, nil
		}
	}
	return "", "", fmt.Errorf("unsupported file type: %s", strings.TrimSpace(fileName))
}

func extensionByMIME(mimeType, fallback string) string {
	exts, err := mime.ExtensionsByType(mimeType)
	if err == nil && len(exts) > 0 {
		return exts[0]
	}
	return fallback
}

func detectAlgorithmTestVideoMIME(fileName string, fullPath string) string {
	mimeType := ""
	if strings.TrimSpace(fullPath) != "" {
		if file, err := os.Open(fullPath); err == nil {
			defer file.Close()
			head := make([]byte, 512)
			if n, readErr := file.Read(head); readErr == nil || readErr == io.EOF {
				mimeType = strings.TrimSpace(http.DetectContentType(head[:n]))
			}
		}
	}
	if strings.HasPrefix(mimeType, "video/") {
		return mimeType
	}
	if guessed := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName))); strings.HasPrefix(strings.TrimSpace(guessed), "video/") {
		return guessed
	}
	return "video/mp4"
}

func probeAlgorithmTestVideo(videoPath string) (algorithmTestVideoMetadata, error) {
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return algorithmTestVideoMetadata{}, fmt.Errorf("ffprobe failed: %s", strings.TrimSpace(string(output)))
	}
	value := strings.TrimSpace(string(output))
	duration, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return algorithmTestVideoMetadata{}, fmt.Errorf("parse video duration failed: %w", err)
	}
	return algorithmTestVideoMetadata{DurationSeconds: duration}, nil
}

func parseAlgorithmTestAnomalyTimes(raw any) []ai.SequenceAnomalyTime {
	if raw == nil {
		return nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var out []ai.SequenceAnomalyTime
	if err := json.Unmarshal(body, &out); err != nil {
		return nil
	}
	return out
}

func stringValue(raw any) string {
	if raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func floatValue(raw any) float64 {
	switch v := raw.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		out, _ := v.Float64()
		return out
	case string:
		out, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return out
	default:
		return 0
	}
}

func (s *Server) saveTestSnapshot(algorithmID, snapshotBase64 string) (string, error) {
	body, err := decodeImageBase64(snapshotBase64)
	if err != nil {
		return "", err
	}
	return s.saveAlgorithmTestImageBytes(algorithmID, body)
}

func (s *Server) saveAlgorithmTestImageBytes(ownerID string, body []byte) (string, error) {
	mimeType := http.DetectContentType(body)
	ext, ok := imageExtensionByMIME(mimeType)
	if !ok {
		return "", fmt.Errorf("unsupported image mime type: %s", mimeType)
	}
	now := time.Now()
	batchID := uuid.NewString()
	dir := filepath.Join(algorithmTestMediaRootDir, now.Format("20060102"), batchID)
	if err := s.ensureDir(dir); err != nil {
		return "", err
	}
	filename := ownerID + "_" + now.Format("150405") + "_" + uuid.NewString() + ext
	full := filepath.Join(dir, filename)
	if err := os.WriteFile(full, body, 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(now.Format("20060102"), batchID, filename)), nil
}

func normalizeImageBase64(raw string) string {
	v := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(v), "data:") {
		if idx := strings.Index(v, ","); idx >= 0 {
			v = strings.TrimSpace(v[idx+1:])
		}
	}
	return v
}

func decodeImageBase64(raw string) ([]byte, error) {
	payload := normalizeImageBase64(raw)
	if payload == "" {
		return nil, fmt.Errorf("empty base64 payload")
	}
	body, err := base64.StdEncoding.DecodeString(payload)
	if err == nil {
		return body, nil
	}
	return base64.RawStdEncoding.DecodeString(payload)
}

func sanitizeAlgorithmTestRequestPayload(raw []byte) string {
	return sanitizeJSONTopLevelFields(raw, "image_base64")
}

func sanitizeAlgorithmTestResponsePayload(raw []byte) string {
	return sanitizeJSONTopLevelFields(raw, "snapshot", "snapshot_width", "snapshot_height")
}

func sanitizeAlgorithmTestVideoRequestPayload(raw []byte) string {
	return sanitizeJSONTopLevelFields(raw, "video_base64")
}

func sanitizeAlgorithmTestVideoResponsePayload(raw []byte) string {
	return sanitizeJSONTopLevelFields(raw)
}

func sanitizeJSONTopLevelFields(raw []byte, fields ...string) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}"
	}
	if strings.EqualFold(trimmed, "null") {
		return "null"
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return trimmed
	}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		delete(payload, field)
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return trimmed
	}
	return string(out)
}

type badRequestError struct {
	msg string
}

func (e badRequestError) Error() string { return e.msg }

func errBadRequest(msg string) error { return badRequestError{msg: msg} }

func isBadRequestError(err error) bool {
	var badReq badRequestError
	return errors.As(err, &badReq)
}

func isPromptVersionConflictError(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(strings.ToLower(err.Error()), "version already exists in this algorithm") {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "uk_mb_algorithm_prompt_version") {
		return true
	}
	if strings.Contains(msg, "mb_algorithm_prompts.algorithm_id, mb_algorithm_prompts.version") {
		return true
	}
	if strings.Contains(msg, "duplicate key value violates unique constraint") {
		return true
	}
	return false
}

func isAlgorithmNameConflictError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "算法名称不能重复") {
		return true
	}
	if strings.Contains(msg, "mb_algorithms.name") {
		return true
	}
	if strings.Contains(msg, "unique constraint failed: mb_algorithms.name") {
		return true
	}
	if strings.Contains(msg, "duplicate key value violates unique constraint") {
		return true
	}
	return false
}
