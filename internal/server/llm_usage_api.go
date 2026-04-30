package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"maas-box/internal/model"
)

type llmUsageSummaryRow struct {
	CallCount           int64 `gorm:"column:call_count"`
	SuccessCount        int64 `gorm:"column:success_count"`
	EmptyContentCount   int64 `gorm:"column:empty_content_count"`
	ErrorCount          int64 `gorm:"column:error_count"`
	UsageAvailableCount int64 `gorm:"column:usage_available_count"`
	UsageMissingCount   int64 `gorm:"column:usage_missing_count"`
	PromptTokens        int64 `gorm:"column:prompt_tokens"`
	CompletionTokens    int64 `gorm:"column:completion_tokens"`
	TotalTokens         int64 `gorm:"column:total_tokens"`
}

type llmUsageTimeBucketRow struct {
	BucketStart       time.Time `json:"bucket_start" gorm:"column:bucket_start"`
	BucketDate        time.Time `json:"bucket_date" gorm:"column:bucket_date"`
	CallCount         int64     `json:"call_count" gorm:"column:call_count"`
	PromptTokens      int64     `json:"prompt_tokens" gorm:"column:prompt_tokens"`
	CompletionTokens  int64     `json:"completion_tokens" gorm:"column:completion_tokens"`
	TotalTokens       int64     `json:"total_tokens" gorm:"column:total_tokens"`
	UsageMissingCount int64     `json:"usage_missing_count" gorm:"column:usage_missing_count"`
}

type llmUsageCallOutput struct {
	model.LLMUsageCall
	TaskName   string `json:"task_name" gorm:"column:task_name"`
	DeviceName string `json:"device_name" gorm:"column:device_name"`
}

type llmUsageQueryFilters struct {
	StartAt        time.Time
	EndAt          time.Time
	Source         string
	ProviderID     string
	Model          string
	CallStatus     string
	UsageAvailable *bool
}

func (s *Server) registerLLMUsageRoutes(r gin.IRouter) {
	g := r.Group("/llm-usage")
	g.GET("/summary", s.getLLMUsageSummary)
	g.GET("/hourly", s.listLLMUsageHourly)
	g.GET("/daily", s.listLLMUsageDaily)
	g.GET("/calls", s.listLLMUsageCalls)
}

func (s *Server) getLLMUsageSummary(c *gin.Context) {
	filters, err := parseLLMUsageFilters(c, 30*24*time.Hour)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}

	query := applyLLMUsageCallFilters(s.db.Model(&model.LLMUsageCall{}), filters)

	var summary llmUsageSummaryRow
	if err := query.Select(
		"COUNT(*) AS call_count, " +
			"COALESCE(SUM(CASE WHEN call_status = 'success' THEN 1 ELSE 0 END), 0) AS success_count, " +
			"COALESCE(SUM(CASE WHEN call_status = 'empty_content' THEN 1 ELSE 0 END), 0) AS empty_content_count, " +
			"COALESCE(SUM(CASE WHEN call_status = 'error' THEN 1 ELSE 0 END), 0) AS error_count, " +
			"COALESCE(SUM(CASE WHEN usage_available = 1 THEN 1 ELSE 0 END), 0) AS usage_available_count, " +
			"COALESCE(SUM(CASE WHEN usage_available = 0 THEN 1 ELSE 0 END), 0) AS usage_missing_count, " +
			"COALESCE(SUM(CASE WHEN usage_available = 1 THEN COALESCE(prompt_tokens, 0) ELSE 0 END), 0) AS prompt_tokens, " +
			"COALESCE(SUM(CASE WHEN usage_available = 1 THEN COALESCE(completion_tokens, 0) ELSE 0 END), 0) AS completion_tokens, " +
			"COALESCE(SUM(CASE WHEN usage_available = 1 THEN COALESCE(total_tokens, 0) ELSE 0 END), 0) AS total_tokens",
	).Scan(&summary).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "查询LLM用量总览失败")
		return
	}

	avgTotalTokensPerCall := 0.0
	if summary.CallCount > 0 {
		avgTotalTokensPerCall = float64(summary.TotalTokens) / float64(summary.CallCount)
	}

	s.ok(c, gin.H{
		"summary": gin.H{
			"call_count":                summary.CallCount,
			"success_count":             summary.SuccessCount,
			"empty_content_count":       summary.EmptyContentCount,
			"error_count":               summary.ErrorCount,
			"usage_available_count":     summary.UsageAvailableCount,
			"usage_missing_count":       summary.UsageMissingCount,
			"prompt_tokens":             summary.PromptTokens,
			"completion_tokens":         summary.CompletionTokens,
			"total_tokens":              summary.TotalTokens,
			"avg_total_tokens_per_call": avgTotalTokensPerCall,
			"start_at":                  filters.StartAt,
			"end_at":                    filters.EndAt,
		},
	})
}

func (s *Server) listLLMUsageHourly(c *gin.Context) {
	filters, err := parseLLMUsageFilters(c, 24*time.Hour)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if filters.EndAt.Sub(filters.StartAt) > 31*24*time.Hour {
		s.fail(c, http.StatusBadRequest, "小时统计范围不能超过31天")
		return
	}

	query := applyLLMUsageAggregateFilters(s.db.Model(&model.LLMUsageHourly{}), filters)
	query = query.Where(
		"bucket_start >= ? AND bucket_start <= ?",
		llmUsageHourBucket(filters.StartAt),
		llmUsageHourBucket(filters.EndAt),
	)

	var items []llmUsageTimeBucketRow
	if err := query.Select(
		"bucket_start, " +
			"COALESCE(SUM(call_count), 0) AS call_count, " +
			"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, " +
			"COALESCE(SUM(completion_tokens), 0) AS completion_tokens, " +
			"COALESCE(SUM(total_tokens), 0) AS total_tokens, " +
			"COALESCE(SUM(CASE WHEN usage_available = 0 THEN call_count ELSE 0 END), 0) AS usage_missing_count",
	).Group("bucket_start").Order("bucket_start asc").Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "查询LLM每小时统计失败")
		return
	}

	s.ok(c, gin.H{
		"items":    items,
		"start_at": filters.StartAt,
		"end_at":   filters.EndAt,
	})
}

func (s *Server) listLLMUsageDaily(c *gin.Context) {
	filters, err := parseLLMUsageFilters(c, 30*24*time.Hour)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}

	query := applyLLMUsageAggregateFilters(s.db.Model(&model.LLMUsageDaily{}), filters)
	query = query.Where(
		"bucket_date >= ? AND bucket_date <= ?",
		llmUsageDayBucket(filters.StartAt),
		llmUsageDayBucket(filters.EndAt),
	)

	var items []llmUsageTimeBucketRow
	if err := query.Select(
		"bucket_date, " +
			"COALESCE(SUM(call_count), 0) AS call_count, " +
			"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, " +
			"COALESCE(SUM(completion_tokens), 0) AS completion_tokens, " +
			"COALESCE(SUM(total_tokens), 0) AS total_tokens, " +
			"COALESCE(SUM(CASE WHEN usage_available = 0 THEN call_count ELSE 0 END), 0) AS usage_missing_count",
	).Group("bucket_date").Order("bucket_date asc").Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "查询LLM每天统计失败")
		return
	}

	s.ok(c, gin.H{
		"items":    items,
		"start_at": filters.StartAt,
		"end_at":   filters.EndAt,
	})
}

func (s *Server) listLLMUsageCalls(c *gin.Context) {
	filters, err := parseLLMUsageFilters(c, 30*24*time.Hour)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}

	page := parsePositiveInt(c.Query("page"), 1)
	pageSize := parsePositiveInt(c.Query("page_size"), 20)
	if pageSize > 200 {
		pageSize = 200
	}
	offset := (page - 1) * pageSize

	baseQuery := s.db.Model(&model.LLMUsageCall{}).
		Joins("LEFT JOIN mb_video_tasks t ON t.id = mb_llm_usage_calls.task_id").
		Joins("LEFT JOIN mb_media_sources d ON d.id = mb_llm_usage_calls.device_id")
	baseQuery = applyLLMUsageCallFilters(baseQuery, filters)

	var total int64
	if err := baseQuery.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "统计LLM调用明细总数失败")
		return
	}

	var items []llmUsageCallOutput
	if err := baseQuery.Select(
		"mb_llm_usage_calls.*, t.name AS task_name, d.name AS device_name",
	).Order("mb_llm_usage_calls.occurred_at desc").
		Offset(offset).
		Limit(pageSize).
		Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "查询LLM调用明细失败")
		return
	}

	s.ok(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"start_at":  filters.StartAt,
		"end_at":    filters.EndAt,
	})
}

func parseLLMUsageFilters(c *gin.Context, defaultDuration time.Duration) (llmUsageQueryFilters, error) {
	source := normalizeLLMUsageSourceFromQuery(c.Query("source"))
	providerID := strings.TrimSpace(c.Query("provider_id"))
	modelName := strings.TrimSpace(c.Query("model"))
	callStatus := normalizeLLMUsageStatusFromQuery(c.Query("call_status"))

	startAt, err := parseEventQueryTime(strings.TrimSpace(c.Query("start_at")))
	if err != nil {
		return llmUsageQueryFilters{}, errors.New("start_at 格式无效")
	}
	endAt, err := parseEventQueryTime(strings.TrimSpace(c.Query("end_at")))
	if err != nil {
		return llmUsageQueryFilters{}, errors.New("end_at 格式无效")
	}
	if startAt.IsZero() && endAt.IsZero() {
		endAt = time.Now()
		startAt = endAt.Add(-defaultDuration)
	} else if startAt.IsZero() {
		startAt = endAt.Add(-defaultDuration)
	} else if endAt.IsZero() {
		endAt = time.Now()
	}
	if startAt.After(endAt) {
		return llmUsageQueryFilters{}, errors.New("start_at 不能晚于 end_at")
	}

	usageAvailable, err := parseLLMUsageAvailable(c.Query("usage_available"))
	if err != nil {
		return llmUsageQueryFilters{}, err
	}

	return llmUsageQueryFilters{
		StartAt:        startAt,
		EndAt:          endAt,
		Source:         source,
		ProviderID:     providerID,
		Model:          modelName,
		CallStatus:     callStatus,
		UsageAvailable: usageAvailable,
	}, nil
}

func normalizeLLMUsageSourceFromQuery(raw string) string {
	switch strings.TrimSpace(raw) {
	case "", "all":
		return ""
	case model.LLMUsageSourceTaskRuntime:
		return model.LLMUsageSourceTaskRuntime
	case model.LLMUsageSourceAlgorithmTest:
		return model.LLMUsageSourceAlgorithmTest
	case model.LLMUsageSourceDirectAnalyze:
		return model.LLMUsageSourceDirectAnalyze
	default:
		return ""
	}
}

func normalizeLLMUsageStatusFromQuery(raw string) string {
	switch strings.TrimSpace(raw) {
	case "", "all":
		return ""
	case model.LLMUsageStatusSuccess:
		return model.LLMUsageStatusSuccess
	case model.LLMUsageStatusEmptyContent:
		return model.LLMUsageStatusEmptyContent
	case model.LLMUsageStatusError:
		return model.LLMUsageStatusError
	default:
		return ""
	}
}

func parseLLMUsageAvailable(raw string) (*bool, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	switch value {
	case "", "all":
		return nil, nil
	case "1", "true":
		parsed := true
		return &parsed, nil
	case "0", "false":
		parsed := false
		return &parsed, nil
	default:
		return nil, errors.New("usage_available 只能是 true 或 false")
	}
}

func applyLLMUsageCallFilters(query *gorm.DB, filters llmUsageQueryFilters) *gorm.DB {
	if filters.Source != "" {
		query = query.Where("mb_llm_usage_calls.source = ?", filters.Source)
	}
	if filters.ProviderID != "" {
		query = query.Where("mb_llm_usage_calls.provider_id = ?", filters.ProviderID)
	}
	if filters.Model != "" {
		query = query.Where("mb_llm_usage_calls.model = ?", filters.Model)
	}
	if filters.CallStatus != "" {
		query = query.Where("mb_llm_usage_calls.call_status = ?", filters.CallStatus)
	}
	if filters.UsageAvailable != nil {
		query = query.Where("mb_llm_usage_calls.usage_available = ?", *filters.UsageAvailable)
	}
	if !filters.StartAt.IsZero() {
		query = query.Where("mb_llm_usage_calls.occurred_at >= ?", filters.StartAt)
	}
	if !filters.EndAt.IsZero() {
		query = query.Where("mb_llm_usage_calls.occurred_at <= ?", filters.EndAt)
	}
	return query
}

func applyLLMUsageAggregateFilters(query *gorm.DB, filters llmUsageQueryFilters) *gorm.DB {
	if filters.Source != "" {
		query = query.Where("source = ?", filters.Source)
	}
	if filters.ProviderID != "" {
		query = query.Where("provider_id = ?", filters.ProviderID)
	}
	if filters.Model != "" {
		query = query.Where("model = ?", filters.Model)
	}
	if filters.CallStatus != "" {
		query = query.Where("call_status = ?", filters.CallStatus)
	}
	if filters.UsageAvailable != nil {
		query = query.Where("usage_available = ?", *filters.UsageAvailable)
	}
	return query
}
