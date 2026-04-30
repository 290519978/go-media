package server

import (
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"maas-box/internal/ai"
	"maas-box/internal/model"
)

type llmUsagePersistRequest struct {
	Source       string
	TaskID       string
	DeviceID     string
	ProviderID   string
	ProviderName string
	Model        string
	DetectMode   int
	OccurredAt   time.Time
	Usage        *ai.LLMUsage
}

func normalizeLLMUsageSource(raw string) string {
	switch strings.TrimSpace(raw) {
	case model.LLMUsageSourceTaskRuntime:
		return model.LLMUsageSourceTaskRuntime
	case model.LLMUsageSourceAlgorithmTest:
		return model.LLMUsageSourceAlgorithmTest
	case model.LLMUsageSourceDirectAnalyze:
		return model.LLMUsageSourceDirectAnalyze
	default:
		return model.LLMUsageSourceDirectAnalyze
	}
}

func normalizeLLMUsageStatus(raw string) string {
	switch strings.TrimSpace(raw) {
	case model.LLMUsageStatusSuccess:
		return model.LLMUsageStatusSuccess
	case model.LLMUsageStatusEmptyContent:
		return model.LLMUsageStatusEmptyContent
	case model.LLMUsageStatusError:
		return model.LLMUsageStatusError
	default:
		return model.LLMUsageStatusError
	}
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	next := *value
	return &next
}

func optionalIntValue(value *int) int64 {
	if value == nil {
		return 0
	}
	return int64(*value)
}

func trimLLMUsageText(raw string, limit int) string {
	text := strings.TrimSpace(raw)
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func llmUsageHourBucket(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

func llmUsageDayBucket(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func (s *Server) resolveTaskLLMProvider(algorithmConfigs []taskAlgorithmRuntime) (model.ModelProvider, bool) {
	hasLargeCapability := false
	for _, item := range algorithmConfigs {
		mode := strings.TrimSpace(item.Algorithm.Mode)
		if mode != model.AlgorithmModeLarge && mode != model.AlgorithmModeHybrid {
			continue
		}
		hasLargeCapability = true
		break
	}
	if !hasLargeCapability {
		return model.ModelProvider{}, false
	}
	provider, err := s.getConfiguredLLMProvider()
	if err != nil {
		return model.ModelProvider{}, false
	}
	return provider, true
}

func (s *Server) recordLLMUsage(tx *gorm.DB, req llmUsagePersistRequest) (bool, error) {
	if s == nil || s.db == nil || req.Usage == nil {
		return false, nil
	}
	callID := strings.TrimSpace(req.Usage.CallID)
	if callID == "" {
		return false, nil
	}
	if tx == nil {
		tx = s.db
	}

	occurredAt := req.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}

	call := model.LLMUsageCall{
		ID:               callID,
		Source:           normalizeLLMUsageSource(req.Source),
		TaskID:           strings.TrimSpace(req.TaskID),
		DeviceID:         strings.TrimSpace(req.DeviceID),
		ProviderID:       strings.TrimSpace(req.ProviderID),
		ProviderName:     trimLLMUsageText(req.ProviderName, 128),
		Model:            trimLLMUsageText(firstNonEmpty(strings.TrimSpace(req.Usage.Model), strings.TrimSpace(req.Model)), 256),
		DetectMode:       req.DetectMode,
		CallStatus:       normalizeLLMUsageStatus(req.Usage.CallStatus),
		UsageAvailable:   req.Usage.UsageAvailable,
		PromptTokens:     cloneOptionalInt(req.Usage.PromptTokens),
		CompletionTokens: cloneOptionalInt(req.Usage.CompletionTokens),
		TotalTokens:      cloneOptionalInt(req.Usage.TotalTokens),
		LatencyMS:        req.Usage.LatencyMS,
		ErrorMessage:     trimLLMUsageText(req.Usage.ErrorMessage, 1024),
		RequestContext:   trimLLMUsageText(req.Usage.RequestContext, 512),
		OccurredAt:       occurredAt,
	}

	createResult := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoNothing: true,
	}).Create(&call)
	if createResult.Error != nil {
		return false, createResult.Error
	}
	if createResult.RowsAffected == 0 {
		return false, nil
	}

	if err := s.upsertLLMUsageHourly(tx, call); err != nil {
		return false, err
	}
	if err := s.upsertLLMUsageDaily(tx, call); err != nil {
		return false, err
	}
	s.maybeStopTasksWhenLLMTokenLimitReached(tx, "llm_usage_"+call.Source)
	return true, nil
}

func (s *Server) upsertLLMUsageHourly(tx *gorm.DB, call model.LLMUsageCall) error {
	item := model.LLMUsageHourly{
		BucketStart:      llmUsageHourBucket(call.OccurredAt),
		Source:           call.Source,
		ProviderID:       call.ProviderID,
		Model:            call.Model,
		CallStatus:       call.CallStatus,
		UsageAvailable:   call.UsageAvailable,
		CallCount:        1,
		PromptTokens:     optionalIntValue(call.PromptTokens),
		CompletionTokens: optionalIntValue(call.CompletionTokens),
		TotalTokens:      optionalIntValue(call.TotalTokens),
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "bucket_start"},
			{Name: "source"},
			{Name: "provider_id"},
			{Name: "model"},
			{Name: "call_status"},
			{Name: "usage_available"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"call_count":        gorm.Expr("call_count + ?", item.CallCount),
			"prompt_tokens":     gorm.Expr("prompt_tokens + ?", item.PromptTokens),
			"completion_tokens": gorm.Expr("completion_tokens + ?", item.CompletionTokens),
			"total_tokens":      gorm.Expr("total_tokens + ?", item.TotalTokens),
			"updated_at":        time.Now(),
		}),
	}).Create(&item).Error
}

func (s *Server) upsertLLMUsageDaily(tx *gorm.DB, call model.LLMUsageCall) error {
	item := model.LLMUsageDaily{
		BucketDate:       llmUsageDayBucket(call.OccurredAt),
		Source:           call.Source,
		ProviderID:       call.ProviderID,
		Model:            call.Model,
		CallStatus:       call.CallStatus,
		UsageAvailable:   call.UsageAvailable,
		CallCount:        1,
		PromptTokens:     optionalIntValue(call.PromptTokens),
		CompletionTokens: optionalIntValue(call.CompletionTokens),
		TotalTokens:      optionalIntValue(call.TotalTokens),
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "bucket_date"},
			{Name: "source"},
			{Name: "provider_id"},
			{Name: "model"},
			{Name: "call_status"},
			{Name: "usage_available"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"call_count":        gorm.Expr("call_count + ?", item.CallCount),
			"prompt_tokens":     gorm.Expr("prompt_tokens + ?", item.PromptTokens),
			"completion_tokens": gorm.Expr("completion_tokens + ?", item.CompletionTokens),
			"total_tokens":      gorm.Expr("total_tokens + ?", item.TotalTokens),
			"updated_at":        time.Now(),
		}),
	}).Create(&item).Error
}
