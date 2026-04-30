package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"maas-box/internal/logutil"
	"maas-box/internal/model"
)

const (
	llmTokenLimitExceededMessage   = "LLM 总 token 已达到限制，AI 识别已禁用，请调整配置后手动重启任务"
	llmTokenQuotaNoticeSettingKey  = "llm_token_quota_notice_v1"
	llmTokenQuotaNoticeTitle       = "LLM 配额提醒"
	llmTokenQuotaNoticeDefaultBody = "LLM 总 token 已达到限制，AI 识别已禁用。请前往 LLM 用量统计调整限额配置，解除限制后手动重启任务。"
)

var errLLMTokenLimitExceeded = errors.New(llmTokenLimitExceededMessage)

type llmTokenQuotaState struct {
	Enabled    bool
	Reached    bool
	Limit      int64
	UsedTokens int64
	Remaining  int64
}

type llmTokenUsageRow struct {
	OccurredAt  time.Time `gorm:"column:occurred_at"`
	TotalTokens int64     `gorm:"column:total_tokens"`
}

type llmTokenUsageTotalRow struct {
	Count int64 `gorm:"column:count"`
}

type llmTokenQuotaNoticeState struct {
	NoticeID        string    `json:"notice_id"`
	IssuedAt        time.Time `json:"issued_at"`
	TokenTotalLimit int64     `json:"token_total_limit"`
	UsedTokens      int64     `json:"used_tokens"`
	Title           string    `json:"title"`
	Message         string    `json:"message"`
}

func (s *Server) loadLLMTokenQuotaState(tx *gorm.DB) (llmTokenQuotaState, error) {
	state := llmTokenQuotaState{}
	if s == nil || s.cfg == nil || s.db == nil {
		return state, nil
	}
	if tx == nil {
		tx = s.db
	}

	var usedRow llmTokenUsageTotalRow
	if err := tx.Model(&model.LLMUsageCall{}).
		Select("COALESCE(SUM(COALESCE(total_tokens, 0)), 0) AS count").
		Scan(&usedRow).Error; err != nil {
		return state, err
	}

	limit := s.cfg.Server.AI.TotalTokenLimit
	state.Limit = limit
	state.Enabled = s.cfg.Server.AI.DisableOnTokenLimitExceeded && limit > 0
	state.UsedTokens = usedRow.Count
	if state.Enabled {
		if usedRow.Count >= limit {
			state.Reached = true
			state.Remaining = 0
		} else {
			state.Remaining = limit - usedRow.Count
		}
	}
	return state, nil
}

func (s *Server) ensureLLMTokenQuotaAvailable() error {
	state, err := s.loadLLMTokenQuotaState(nil)
	if err != nil {
		return err
	}
	s.maybeSyncLLMTokenQuotaNotice(nil, state, "ensure_quota_available", false)
	if state.Reached {
		return errLLMTokenLimitExceeded
	}
	return nil
}

func isLLMTokenLimitExceededError(err error) bool {
	return errors.Is(err, errLLMTokenLimitExceeded)
}

func buildLLMTokenQuotaNoticeState(state llmTokenQuotaState, now time.Time) *llmTokenQuotaNoticeState {
	if now.IsZero() {
		now = time.Now()
	}
	return &llmTokenQuotaNoticeState{
		NoticeID:        fmt.Sprintf("llm-token-quota-%d", now.UnixMilli()),
		IssuedAt:        now,
		TokenTotalLimit: state.Limit,
		UsedTokens:      state.UsedTokens,
		Title:           llmTokenQuotaNoticeTitle,
		Message:         llmTokenQuotaNoticeDefaultBody,
	}
}

func (s *Server) loadLLMTokenQuotaNoticeState() (*llmTokenQuotaNoticeState, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	raw := strings.TrimSpace(s.getSetting(llmTokenQuotaNoticeSettingKey))
	if raw == "" {
		return nil, nil
	}
	var notice llmTokenQuotaNoticeState
	if err := json.Unmarshal([]byte(raw), &notice); err != nil {
		return nil, err
	}
	notice.NoticeID = strings.TrimSpace(notice.NoticeID)
	notice.Title = strings.TrimSpace(notice.Title)
	notice.Message = strings.TrimSpace(notice.Message)
	if notice.NoticeID == "" {
		return nil, fmt.Errorf("llm token quota notice id is empty")
	}
	if notice.IssuedAt.IsZero() {
		return nil, fmt.Errorf("llm token quota notice issued_at is invalid")
	}
	if notice.Title == "" {
		notice.Title = llmTokenQuotaNoticeTitle
	}
	if notice.Message == "" {
		notice.Message = llmTokenQuotaNoticeDefaultBody
	}
	return &notice, nil
}

func (s *Server) saveLLMTokenQuotaNoticeState(notice *llmTokenQuotaNoticeState) error {
	if s == nil || s.db == nil {
		return nil
	}
	if notice == nil {
		return s.upsertSetting(llmTokenQuotaNoticeSettingKey, "")
	}
	body, err := json.Marshal(notice)
	if err != nil {
		return err
	}
	return s.upsertSetting(llmTokenQuotaNoticeSettingKey, string(body))
}

func (s *Server) clearLLMTokenQuotaNoticeState() error {
	return s.saveLLMTokenQuotaNoticeState(nil)
}

func (s *Server) llmTokenQuotaNoticePayload(notice *llmTokenQuotaNoticeState) map[string]any {
	if notice == nil {
		return map[string]any{}
	}
	return map[string]any{
		"type":              "llm_quota_notice",
		"notice_id":         strings.TrimSpace(notice.NoticeID),
		"issued_at":         notice.IssuedAt,
		"token_total_limit": notice.TokenTotalLimit,
		"used_tokens":       notice.UsedTokens,
		"title":             strings.TrimSpace(firstNonEmpty(notice.Title, llmTokenQuotaNoticeTitle)),
		"message":           strings.TrimSpace(firstNonEmpty(notice.Message, llmTokenQuotaNoticeDefaultBody)),
	}
}

func (s *Server) broadcastLLMTokenQuotaNotice(notice *llmTokenQuotaNoticeState) {
	if s == nil || s.wsHub == nil || notice == nil {
		return
	}
	s.wsHub.Broadcast(s.llmTokenQuotaNoticePayload(notice))
}

func (s *Server) syncLLMTokenQuotaNoticeState(tx *gorm.DB, state llmTokenQuotaState, reason string, shouldBroadcast bool) (*llmTokenQuotaNoticeState, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}

	// notice 需要在多条识别链路之间共享同一个状态，这里串行化读写，避免并发下重复生成或被旧状态覆盖。
	s.llmQuotaNoticeMu.Lock()
	defer s.llmQuotaNoticeMu.Unlock()

	current, err := s.loadLLMTokenQuotaNoticeState()
	if err != nil {
		logutil.Warnf("llm token quota load notice state failed: reason=%s err=%v", reason, err)
		_ = s.clearLLMTokenQuotaNoticeState()
		current = nil
	}

	if !state.Reached {
		if current != nil {
			if err := s.clearLLMTokenQuotaNoticeState(); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}

	if current == nil {
		current = buildLLMTokenQuotaNoticeState(state, time.Now())
		if err := s.saveLLMTokenQuotaNoticeState(current); err != nil {
			return nil, err
		}
		if shouldBroadcast {
			s.broadcastLLMTokenQuotaNotice(current)
		}
		return current, nil
	}

	changed := false
	if current.TokenTotalLimit != state.Limit {
		current.TokenTotalLimit = state.Limit
		changed = true
	}
	if current.UsedTokens != state.UsedTokens {
		current.UsedTokens = state.UsedTokens
		changed = true
	}
	if strings.TrimSpace(current.Title) == "" {
		current.Title = llmTokenQuotaNoticeTitle
		changed = true
	}
	if strings.TrimSpace(current.Message) == "" {
		current.Message = llmTokenQuotaNoticeDefaultBody
		changed = true
	}
	if strings.TrimSpace(current.NoticeID) == "" {
		current.NoticeID = fmt.Sprintf("llm-token-quota-%d", time.Now().UnixMilli())
		changed = true
	}
	if current.IssuedAt.IsZero() {
		current.IssuedAt = time.Now()
		changed = true
	}
	if changed {
		if err := s.saveLLMTokenQuotaNoticeState(current); err != nil {
			return nil, err
		}
	}
	return current, nil
}

func (s *Server) maybeSyncLLMTokenQuotaNotice(tx *gorm.DB, state llmTokenQuotaState, reason string, shouldBroadcast bool) {
	if _, err := s.syncLLMTokenQuotaNoticeState(tx, state, reason, shouldBroadcast); err != nil {
		logutil.Warnf("llm token quota sync notice failed: reason=%s err=%v", reason, err)
	}
}

func (s *Server) pendingLLMTokenQuotaNotice(now time.Time) (*llmTokenQuotaNoticeState, error) {
	if now.IsZero() {
		now = time.Now()
	}
	state, err := s.loadLLMTokenQuotaState(nil)
	if err != nil {
		return nil, err
	}
	return s.syncLLMTokenQuotaNoticeState(nil, state, "pending_notice", false)
}

func (s *Server) loadLLMTokenUsageRows(start, end time.Time) ([]llmTokenUsageRow, error) {
	rows := make([]llmTokenUsageRow, 0)
	if s == nil || s.db == nil {
		return rows, nil
	}
	query := s.db.Model(&model.LLMUsageCall{}).
		Select("occurred_at, COALESCE(total_tokens, 0) AS total_tokens")
	if !start.IsZero() {
		query = query.Where("occurred_at >= ?", start)
	}
	if !end.IsZero() {
		query = query.Where("occurred_at <= ?", end)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Server) disableTaskAutoResumeForQuota(taskID string) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || s == nil {
		return
	}
	if err := s.setTaskAutoResumeIntent(taskID, false); err != nil {
		logutil.Warnf("llm token quota disable task auto resume failed: task_id=%s err=%v", taskID, err)
	}
	s.clearStartupResumePendingByTask(taskID)
	s.clearStartupTaskResumePendingByTask(taskID)
}

func (s *Server) buildQuotaBlockedTaskResults(runtimes []taskDeviceRuntime, onlyDeviceIDs map[string]struct{}) []taskActionResult {
	results := make([]taskActionResult, 0, len(runtimes))
	for _, item := range runtimes {
		deviceID := strings.TrimSpace(item.Device.ID)
		if deviceID == "" {
			continue
		}
		if len(onlyDeviceIDs) > 0 {
			if _, ok := onlyDeviceIDs[deviceID]; !ok {
				continue
			}
		}
		results = append(results, taskActionResult{
			DeviceID: deviceID,
			Success:  false,
			Message:  llmTokenLimitExceededMessage,
		})
	}
	return results
}

func (s *Server) persistTaskStatusAfterQuotaStop(taskID string, runtimes []taskDeviceRuntime) {
	if s == nil || s.db == nil {
		return
	}
	nextStatus, _, totalCount, err := s.computeTaskRuntimeStatus(runtimes)
	if err != nil {
		logutil.Warnf("llm token quota update task runtime status failed: task_id=%s err=%v", taskID, err)
		return
	}
	updates := map[string]any{"status": nextStatus}
	if nextStatus == model.TaskStatusStopped && totalCount > 0 {
		updates["last_stop_at"] = time.Now()
	}
	if err := s.db.Model(&model.VideoTask{}).Where("id = ?", taskID).Updates(updates).Error; err != nil {
		logutil.Warnf("llm token quota persist task status failed: task_id=%s err=%v", taskID, err)
	}
}

// 达到配额后统一走现有停机链路，同时关闭自动恢复，避免配置解除前任务被后台再次拉起。
func (s *Server) stopTasksWhenLLMTokenLimitReached(reason string) {
	if s == nil || s.db == nil || s.aiClient == nil {
		return
	}
	state, err := s.loadLLMTokenQuotaState(nil)
	if err != nil {
		logutil.Warnf("llm token quota load state failed before stop: reason=%s err=%v", reason, err)
		return
	}
	if !state.Reached {
		return
	}

	s.llmQuotaStopMu.Lock()
	if s.llmQuotaStopRunning {
		s.llmQuotaStopMu.Unlock()
		return
	}
	s.llmQuotaStopRunning = true
	s.llmQuotaStopMu.Unlock()
	defer func() {
		s.llmQuotaStopMu.Lock()
		s.llmQuotaStopRunning = false
		s.llmQuotaStopMu.Unlock()
	}()

	type runningRow struct {
		TaskID   string `gorm:"column:task_id"`
		DeviceID string `gorm:"column:device_id"`
	}
	var rows []runningRow
	if err := s.db.Model(&model.VideoTaskDeviceProfile{}).
		Select("mb_video_task_device_profiles.task_id AS task_id, mb_video_task_device_profiles.device_id AS device_id").
		Joins("JOIN mb_media_sources ON mb_media_sources.id = mb_video_task_device_profiles.device_id").
		Where("mb_media_sources.ai_status = ?", model.DeviceAIStatusRunning).
		Find(&rows).Error; err != nil {
		logutil.Warnf("llm token quota query running tasks failed: reason=%s err=%v", reason, err)
		return
	}
	if len(rows) == 0 {
		return
	}

	taskDevices := make(map[string]map[string]struct{})
	for _, row := range rows {
		taskID := strings.TrimSpace(row.TaskID)
		deviceID := strings.TrimSpace(row.DeviceID)
		if taskID == "" || deviceID == "" {
			continue
		}
		if _, ok := taskDevices[taskID]; !ok {
			taskDevices[taskID] = make(map[string]struct{})
		}
		taskDevices[taskID][deviceID] = struct{}{}
	}

	for taskID, selected := range taskDevices {
		s.disableTaskAutoResumeForQuota(taskID)

		_, runtimes, err := s.loadTaskDeviceContexts(taskID)
		if err != nil {
			logutil.Warnf("llm token quota load task contexts failed: task_id=%s reason=%s err=%v", taskID, reason, err)
			continue
		}
		if len(runtimes) == 0 || len(selected) == 0 {
			continue
		}

		results := s.stopTaskRuntimes(context.Background(), taskID, runtimes, selected, "llm_token_limit_"+strings.TrimSpace(reason))
		s.persistTaskStatusAfterQuotaStop(taskID, runtimes)
		logutil.Warnf(
			"llm token quota stopped running task: task_id=%s reason=%s limit=%d used_tokens=%d stopped_devices=%d",
			taskID,
			reason,
			state.Limit,
			state.UsedTokens,
			len(results),
		)
	}
}

func (s *Server) maybeStopTasksWhenLLMTokenLimitReached(tx *gorm.DB, reason string) {
	state, err := s.loadLLMTokenQuotaState(tx)
	if err != nil {
		logutil.Warnf("llm token quota load state failed: reason=%s err=%v", reason, err)
		return
	}
	s.maybeSyncLLMTokenQuotaNotice(tx, state, reason, true)
	if !state.Reached {
		return
	}
	go s.stopTasksWhenLLMTokenLimitReached(reason)
}
