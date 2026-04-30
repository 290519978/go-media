package server

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"maas-box/internal/config"
	"maas-box/internal/logutil"
	"maas-box/internal/model"
)

const (
	recordingStatusStopped   = "stopped"
	recordingStatusRecording = "recording"
	recordingStatusBuffering = "buffering"
)

const (
	gbStreamWarmupTimeout      = 10 * time.Second
	gbStreamWarmupPollInterval = 200 * time.Millisecond
)

func normalizeTaskRecordingPolicy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case model.RecordingPolicyNone:
		return model.RecordingPolicyNone
	case model.RecordingPolicyAlarmClip:
		return model.RecordingPolicyAlarmClip
	default:
		return ""
	}
}

func normalizeTaskFrameRateMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case model.FrameRateModeFPS:
		return model.FrameRateModeFPS
	case model.FrameRateModeInterval:
		return model.FrameRateModeInterval
	default:
		return ""
	}
}

func (s *Server) validateTaskFrameRate(rawMode string, rawValue int) (string, int, error) {
	mode := normalizeTaskFrameRateMode(rawMode)
	allowedModes := s.taskFrameRateModes()
	if strings.TrimSpace(rawMode) == "" {
		mode = s.taskFrameRateDefaultMode()
	}
	if mode == "" {
		return "", 0, fmt.Errorf("frame_rate_mode must be one of: %s", strings.Join(allowedModes, "/"))
	}
	allowed := false
	for _, item := range allowedModes {
		if item == mode {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", 0, fmt.Errorf("frame_rate_mode must be one of: %s", strings.Join(allowedModes, "/"))
	}
	value := rawValue
	if value <= 0 {
		value = s.taskFrameRateDefaultValue()
	}
	if value < 1 || value > 60 {
		return "", 0, errors.New("frame_rate_value must be in range 1..60")
	}
	return mode, value, nil
}

func (s *Server) validateTaskRecordingPolicy(raw string) (string, error) {
	policy := normalizeTaskRecordingPolicy(raw)
	if strings.TrimSpace(raw) == "" {
		policy = s.taskRecordingPolicyDefault()
	}
	if policy == "" {
		return "", errors.New("recording_policy must be none/alarm_clip")
	}
	return policy, nil
}

func (s *Server) resolveTaskRecordingPolicyBySourceID(sourceID string) (string, int, int, bool, error) {
	pre := s.alarmClipDefaultPreSeconds()
	post := s.alarmClipDefaultPostSeconds()
	if s == nil || s.db == nil {
		return model.RecordingPolicyNone, pre, post, false, nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return model.RecordingPolicyNone, pre, post, false, nil
	}
	var row struct {
		RecordingPolicy  string `gorm:"column:recording_policy"`
		AlarmPreSeconds  int    `gorm:"column:alarm_pre_seconds"`
		AlarmPostSeconds int    `gorm:"column:alarm_post_seconds"`
		TaskStatus       string `gorm:"column:task_status"`
	}
	err := s.db.Table("mb_video_task_device_profiles p").
		Select("p.recording_policy, p.alarm_pre_seconds, p.alarm_post_seconds, t.status AS task_status").
		Joins("LEFT JOIN mb_video_tasks t ON t.id = p.task_id").
		Where("p.device_id = ?", sourceID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.RecordingPolicyNone, pre, post, false, nil
		}
		return model.RecordingPolicyNone, pre, post, false, err
	}
	if row.AlarmPreSeconds > 0 {
		pre = clampInt(row.AlarmPreSeconds, 1, 600)
	}
	if row.AlarmPostSeconds > 0 {
		post = clampInt(row.AlarmPostSeconds, 1, 600)
	}
	policy, policyErr := s.validateTaskRecordingPolicy(row.RecordingPolicy)
	if policyErr != nil {
		return model.RecordingPolicyNone, pre, post, false, policyErr
	}
	taskStatus := strings.TrimSpace(row.TaskStatus)
	taskRunning := taskStatus == model.TaskStatusRunning || taskStatus == model.TaskStatusPartialFail
	return policy, pre, post, taskRunning, nil
}

func normalizeRecordingModeValue(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case model.RecordingModeNone:
		return model.RecordingModeNone
	case model.RecordingModeContinuous:
		return model.RecordingModeContinuous
	default:
		return ""
	}
}

func recordingModeOptionsFromConfig(cfg *config.Config) []string {
	defaultModes := []string{model.RecordingModeNone}
	if cfg == nil {
		return defaultModes
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 3)
	for _, raw := range cfg.Server.Recording.Modes {
		mode := normalizeRecordingModeValue(raw)
		if mode == "" {
			continue
		}
		if mode == model.RecordingModeContinuous && !cfg.Server.Recording.AllowContinuous {
			continue
		}
		if _, ok := seen[mode]; ok {
			continue
		}
		seen[mode] = struct{}{}
		out = append(out, mode)
	}
	if len(out) == 0 {
		out = append(out, defaultModes...)
	}
	if _, ok := seen[model.RecordingModeNone]; !ok {
		out = append([]string{model.RecordingModeNone}, out...)
		seen[model.RecordingModeNone] = struct{}{}
	}
	if cfg.Server.Recording.AllowContinuous {
		if _, ok := seen[model.RecordingModeContinuous]; !ok {
			out = append(out, model.RecordingModeContinuous)
		}
	}
	return out
}

func recordingDefaultModeFromConfig(cfg *config.Config) string {
	options := recordingModeOptionsFromConfig(cfg)
	allowed := map[string]struct{}{}
	for _, item := range options {
		allowed[item] = struct{}{}
	}
	mode := normalizeRecordingModeValue("")
	if cfg != nil {
		mode = normalizeRecordingModeValue(cfg.Server.Recording.DefaultMode)
	}
	if mode == model.RecordingModeContinuous && cfg != nil && !cfg.Server.Recording.AllowContinuous {
		mode = model.RecordingModeNone
	}
	if mode != "" {
		if _, ok := allowed[mode]; ok {
			return mode
		}
	}
	if _, ok := allowed[model.RecordingModeNone]; ok {
		return model.RecordingModeNone
	}
	if _, ok := allowed[model.RecordingModeContinuous]; ok {
		return model.RecordingModeContinuous
	}
	return model.RecordingModeNone
}

func resolveRecordingPolicyFromConfig(cfg *config.Config, enable bool, rawMode string) (bool, string, error) {
	if cfg != nil && cfg.Server.Recording.Disabled {
		return false, model.RecordingModeNone, nil
	}
	explicitRaw := strings.TrimSpace(rawMode)
	mode := normalizeRecordingModeValue(explicitRaw)
	explicit := explicitRaw != ""
	if explicit && mode == "" {
		return false, "", errors.New("recording_mode must be none/continuous")
	}
	if mode == "" {
		mode = recordingDefaultModeFromConfig(cfg)
	}
	if !enable || mode == model.RecordingModeNone {
		return false, model.RecordingModeNone, nil
	}
	if mode == model.RecordingModeContinuous {
		if cfg == nil || !cfg.Server.Recording.AllowContinuous {
			if explicit {
				return false, "", errors.New("continuous recording is disabled by policy")
			}
			mode = model.RecordingModeNone
		}
	}
	if mode != model.RecordingModeContinuous && mode != model.RecordingModeNone {
		return false, "", errors.New("recording_mode must be none/continuous")
	}
	if mode == model.RecordingModeNone {
		return false, model.RecordingModeNone, nil
	}
	return true, model.RecordingModeContinuous, nil
}

func (s *Server) recordingModeOptions() []string {
	if s == nil {
		return []string{model.RecordingModeNone}
	}
	return recordingModeOptionsFromConfig(s.cfg)
}

func (s *Server) recordingDefaultMode() string {
	if s == nil {
		return model.RecordingModeNone
	}
	return recordingDefaultModeFromConfig(s.cfg)
}

func (s *Server) resolveRecordingPolicy(enable bool, rawMode string) (bool, string, error) {
	if s == nil {
		return false, model.RecordingModeNone, nil
	}
	return resolveRecordingPolicyFromConfig(s.cfg, enable, rawMode)
}

func applyDefaultRecordingPolicyToSource(cfg *config.Config, source *model.MediaSource) {
	if source == nil {
		return
	}
	source.EnableRecording = false
	source.RecordingMode = model.RecordingModeNone
	source.RecordingStatus = recordingStatusStopped
	_ = cfg
}

func applyDefaultAlarmClipPolicyToSource(cfg *config.Config, source *model.MediaSource) {
	if source == nil {
		return
	}
	enableDefault := false
	pre := 8
	post := 12
	if cfg != nil {
		if cfg.Server.Recording.AlarmClip.PreSeconds > 0 {
			pre = cfg.Server.Recording.AlarmClip.PreSeconds
		}
		if cfg.Server.Recording.AlarmClip.PostSeconds > 0 {
			post = cfg.Server.Recording.AlarmClip.PostSeconds
		}
	}
	source.EnableAlarmClip = enableDefault
	source.AlarmPreSeconds = clampInt(pre, 1, 600)
	source.AlarmPostSeconds = clampInt(post, 1, 600)
}

func isRecordingStatusActive(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == recordingStatusRecording || status == "running" || status == recordingStatusBuffering
}

func (s *Server) setRecordingStatus(sourceID, status string) error {
	if s == nil || s.db == nil {
		return nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = recordingStatusStopped
	}
	return s.db.Model(&model.MediaSource{}).Where("id = ?", sourceID).
		Updates(map[string]any{"recording_status": status, "updated_at": time.Now()}).Error
}

func (s *Server) startRecordingForSource(source model.MediaSource, maxSecond int) error {
	if s == nil || s.cfg == nil || s.db == nil || s.cfg.Server.Recording.Disabled {
		return nil
	}
	app, stream := parseDeviceZLMAppStream(strings.ToLower(strings.TrimSpace(source.Protocol)), source, strings.TrimSpace(s.cfg.Server.ZLM.App))
	app = strings.TrimSpace(app)
	stream = strings.TrimSpace(stream)
	if stream == "" {
		return fmt.Errorf("record stream is empty")
	}
	if app == "" {
		app = strings.TrimSpace(s.cfg.Server.ZLM.App)
	}
	if app == "" {
		app = "live"
	}
	recordDir, err := s.safeRecordingDeviceDir(source.ID)
	if err != nil {
		return err
	}
	if err := s.ensureDir(recordDir); err != nil {
		return err
	}
	zlmRecordDir, err := s.resolveZLMRecordDir(source.ID, recordDir)
	if err != nil {
		return err
	}
	return s.startRecordByAppStream(source.ID, app, stream, zlmRecordDir, maxSecond, recordingStatusRecording)
}

func (s *Server) startRecordByAppStream(sourceID, app, stream, zlmRecordDir string, maxSecond int, status string) error {
	if s == nil || s.cfg == nil || s.db == nil || s.cfg.Server.Recording.Disabled {
		return nil
	}
	app = strings.TrimSpace(app)
	stream = strings.TrimSpace(stream)
	if stream == "" {
		return fmt.Errorf("record stream is empty")
	}
	if app == "" {
		app = strings.TrimSpace(s.cfg.Server.ZLM.App)
	}
	if app == "" {
		app = "live"
	}
	form := url.Values{}
	form.Set("secret", strings.TrimSpace(s.cfg.Server.ZLM.Secret))
	form.Set("type", "1")
	form.Set("vhost", "__defaultVhost__")
	form.Set("app", app)
	form.Set("stream", stream)
	form.Set("customized_path", zlmRecordDir)
	if maxSecond > 0 {
		form.Set("max_second", fmt.Sprintf("%d", maxSecond))
	}
	if _, err := s.callZLMAPI("/index/api/startRecord", form); err != nil {
		return err
	}
	if strings.TrimSpace(status) == "" {
		status = recordingStatusRecording
	}
	return s.setRecordingStatus(sourceID, status)
}

func (s *Server) resolveZLMRecordDir(sourceID, localRecordDir string) (string, error) {
	if s == nil || s.cfg == nil {
		return "", errors.New("invalid recording context")
	}
	localRecordDir = strings.TrimSpace(localRecordDir)
	if localRecordDir == "" {
		return "", errors.New("local recording dir is empty")
	}
	zlmRoot := strings.TrimSpace(s.cfg.Server.Recording.ZLMStorageDir)
	if zlmRoot == "" {
		return localRecordDir, nil
	}
	sourceID = strings.TrimSpace(sourceID)
	sourceID = strings.ReplaceAll(sourceID, "/", "_")
	sourceID = strings.ReplaceAll(sourceID, "\\", "_")
	if sourceID == "" {
		return "", errors.New("record source id is empty")
	}
	zlmRoot = strings.TrimRight(zlmRoot, "/")
	return zlmRoot + "/" + sourceID, nil
}

func (s *Server) stopRecordingByAppStream(sourceID, app, stream string) error {
	if s == nil || s.cfg == nil || s.db == nil || s.cfg.Server.Recording.Disabled {
		return nil
	}
	app = strings.TrimSpace(app)
	stream = strings.TrimSpace(stream)
	if stream == "" {
		if sourceID != "" {
			return s.setRecordingStatus(sourceID, recordingStatusStopped)
		}
		return nil
	}
	if app == "" {
		app = strings.TrimSpace(s.cfg.Server.ZLM.App)
	}
	if app == "" {
		app = "live"
	}
	form := url.Values{}
	form.Set("secret", strings.TrimSpace(s.cfg.Server.ZLM.Secret))
	form.Set("type", "1")
	form.Set("vhost", "__defaultVhost__")
	form.Set("app", app)
	form.Set("stream", stream)
	_, err := s.callZLMAPI("/index/api/stopRecord", form)
	if err != nil && !isZLMStopRecordIgnorableError(err) {
		return err
	}
	if sourceID != "" {
		return s.setRecordingStatus(sourceID, recordingStatusStopped)
	}
	return nil
}

func isZLMStopRecordIgnorableError(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no such") ||
		strings.Contains(msg, "record not") ||
		strings.Contains(msg, "recording not") ||
		strings.Contains(msg, "already stop") ||
		strings.Contains(msg, "count_hit=0")
}

func (s *Server) stopRecordingForSource(source model.MediaSource) error {
	if s == nil || s.cfg == nil {
		return nil
	}
	app, stream := parseDeviceZLMAppStream(strings.ToLower(strings.TrimSpace(source.Protocol)), source, strings.TrimSpace(s.cfg.Server.ZLM.App))
	return s.stopRecordingByAppStream(source.ID, app, stream)
}

func (s *Server) stopRecordingIfNeeded(source *model.MediaSource) error {
	if source == nil {
		return nil
	}
	if isRecordingStatusActive(source.RecordingStatus) {
		if err := s.stopRecordingForSource(*source); err != nil {
			return err
		}
		source.RecordingStatus = recordingStatusStopped
		return nil
	}
	source.RecordingStatus = recordingStatusStopped
	return s.setRecordingStatus(source.ID, recordingStatusStopped)
}

func (s *Server) applyRecordingPolicyForSourceID(sourceID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil
	}
	var source model.MediaSource
	if err := s.db.Where("id = ?", sourceID).First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	return s.applyRecordingPolicyForSource(&source)
}

func (s *Server) applyRecordingPolicyForSource(source *model.MediaSource) error {
	if s == nil || source == nil {
		return nil
	}
	recordingPolicy, preSeconds, postSeconds, taskRunning, policyErr := s.resolveTaskRecordingPolicyBySourceID(source.ID)
	if policyErr != nil {
		return policyErr
	}
	if !taskRunning {
		recordingPolicy = model.RecordingPolicyNone
	}
	if recordingPolicy != model.RecordingPolicyAlarmClip {
		if closeErr := s.closeActiveAlarmClipSessionForSource(source.ID, "policy_changed"); closeErr != nil {
			logutil.Warnf("close alarm clip session on policy change failed: source_id=%s err=%v", source.ID, closeErr)
		}
	}
	shouldBuffer := recordingPolicy == model.RecordingPolicyAlarmClip && s.shouldRunAlarmClipBuffer(source)

	if !strings.EqualFold(strings.TrimSpace(source.Status), "online") {
		if recordingPolicy == model.RecordingPolicyAlarmClip {
			if closeErr := s.closeActiveAlarmClipSessionForSource(source.ID, "source_offline"); closeErr != nil {
				logutil.Warnf("close alarm clip session on source offline failed: source_id=%s err=%v", source.ID, closeErr)
			}
		}
		s.cancelAlarmRecordingStop(source.ID)
		return s.stopRecordingIfNeeded(source)
	}

	streamActive, err := s.isSourceStreamActive(*source)
	if err != nil {
		return err
	}
	if !streamActive {
		if recordingPolicy == model.RecordingPolicyAlarmClip {
			if closeErr := s.closeActiveAlarmClipSessionForSource(source.ID, "stream_inactive"); closeErr != nil {
				logutil.Warnf("close alarm clip session on stream inactive failed: source_id=%s err=%v", source.ID, closeErr)
			}
		}
		s.cancelAlarmRecordingStop(source.ID)
		return s.stopRecordingIfNeeded(source)
	}

	if shouldBuffer {
		s.cancelAlarmRecordingStop(source.ID)
		if strings.EqualFold(strings.TrimSpace(source.RecordingStatus), recordingStatusRecording) {
			if err := s.stopRecordingForSource(*source); err != nil {
				return err
			}
			source.RecordingStatus = recordingStatusStopped
		}
		if strings.EqualFold(strings.TrimSpace(source.RecordingStatus), recordingStatusBuffering) {
			healthy, healthErr := s.hasRecentAlarmBufferFiles(source.ID)
			if healthErr != nil {
				return healthErr
			}
			if !healthy {
				logutil.Infof("alarm buffer self-heal restart: source_id=%s status=%s", source.ID, source.RecordingStatus)
				if err := s.stopRecordingForSource(*source); err != nil {
					return err
				}
				source.RecordingStatus = recordingStatusStopped
				if err := s.startAlarmBufferForSource(*source); err != nil {
					return err
				}
				source.RecordingStatus = recordingStatusBuffering
				return nil
			}
			if err := s.pruneAlarmBufferFiles(source.ID, preSeconds, postSeconds); err != nil {
				return err
			}
			return s.setRecordingStatus(source.ID, recordingStatusBuffering)
		}
		if err := s.startAlarmBufferForSource(*source); err != nil {
			return err
		}
		source.RecordingStatus = recordingStatusBuffering
		return nil
	}

	s.cancelAlarmRecordingStop(source.ID)
	return s.stopRecordingIfNeeded(source)
}

func (s *Server) healBufferedAlarmRecordings() {
	if s == nil || s.db == nil || s.cfg == nil || s.cfg.Server.Recording.Disabled {
		return
	}
	var sourceIDs []string
	if err := s.db.Model(&model.MediaSource{}).
		Where("recording_status = ?", recordingStatusBuffering).
		Pluck("id", &sourceIDs).Error; err != nil {
		logutil.Warnf("heal buffered alarm recordings query failed: %v", err)
		return
	}
	for _, sourceID := range sourceIDs {
		sourceID = strings.TrimSpace(sourceID)
		if sourceID == "" {
			continue
		}
		if err := s.applyRecordingPolicyForSourceID(sourceID); err != nil {
			logutil.Warnf("heal buffered alarm recording failed: source_id=%s err=%v", sourceID, err)
		}
	}
}

func (s *Server) triggerAlarmRecordingBySourceID(sourceID string) {
	// 已改为 triggerAlarmClipBySourceID 事件驱动归档。
	_ = sourceID
}

func (s *Server) recordingSegmentSeconds() int {
	if s != nil && s.cfg != nil && s.cfg.Server.Recording.SegmentSeconds > 0 {
		return s.cfg.Server.Recording.SegmentSeconds
	}
	return 60
}

func (s *Server) isSourceStreamActive(source model.MediaSource) (bool, error) {
	if s == nil || s.cfg == nil || s.cfg.Server.ZLM.Disabled {
		return false, nil
	}
	app, stream := parseDeviceZLMAppStream(
		strings.ToLower(strings.TrimSpace(source.Protocol)),
		source,
		strings.TrimSpace(s.cfg.Server.ZLM.App),
	)
	app = strings.TrimSpace(app)
	stream = strings.TrimSpace(stream)
	if stream == "" {
		return false, nil
	}
	return s.isZLMStreamActive(app, stream)
}

func (s *Server) waitForSourceStreamActive(source *model.MediaSource, timeout, pollInterval time.Duration) (bool, error) {
	if s == nil || source == nil {
		return false, nil
	}
	if timeout <= 0 {
		timeout = gbStreamWarmupTimeout
	}
	if pollInterval <= 0 {
		pollInterval = gbStreamWarmupPollInterval
	}
	deadline := time.Now().Add(timeout)
	for {
		active, err := s.isSourceStreamActive(*source)
		if err != nil {
			return false, err
		}
		if active {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(pollInterval)
	}
}

func (s *Server) stopRecordingAndCancelBySource(source model.MediaSource) error {
	if s == nil {
		return nil
	}
	s.cancelAlarmRecordingStop(source.ID)
	return s.stopRecordingIfNeeded(&source)
}

func (s *Server) scheduleAlarmRecordingStop(sourceID string, delay time.Duration) {
	if s == nil {
		return
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return
	}
	if delay <= 0 {
		delay = 60 * time.Second
	}
	s.recordingMu.Lock()
	if s.alarmRecordingSeq == nil {
		s.alarmRecordingSeq = make(map[string]uint64)
	}
	if s.alarmRecordingTimers == nil {
		s.alarmRecordingTimers = make(map[string]*time.Timer)
	}
	if oldTimer, ok := s.alarmRecordingTimers[sourceID]; ok && oldTimer != nil {
		oldTimer.Stop()
	}
	seq := s.alarmRecordingSeq[sourceID] + 1
	s.alarmRecordingSeq[sourceID] = seq
	timer := time.AfterFunc(delay, func() {
		s.handleAlarmRecordingTimeout(sourceID, seq)
	})
	s.alarmRecordingTimers[sourceID] = timer
	s.recordingMu.Unlock()
}

func (s *Server) cancelAlarmRecordingStop(sourceID string) {
	if s == nil {
		return
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return
	}
	s.recordingMu.Lock()
	if s.alarmRecordingSeq == nil {
		s.alarmRecordingSeq = make(map[string]uint64)
	}
	if s.alarmRecordingTimers == nil {
		s.alarmRecordingTimers = make(map[string]*time.Timer)
	}
	if timer, ok := s.alarmRecordingTimers[sourceID]; ok && timer != nil {
		timer.Stop()
	}
	delete(s.alarmRecordingTimers, sourceID)
	delete(s.alarmRecordingSeq, sourceID)
	s.recordingMu.Unlock()
}

func (s *Server) stopAllAlarmRecordingTimers() {
	if s == nil {
		return
	}
	s.recordingMu.Lock()
	if s.alarmRecordingSeq == nil {
		s.alarmRecordingSeq = make(map[string]uint64)
	}
	if s.alarmRecordingTimers == nil {
		s.alarmRecordingTimers = make(map[string]*time.Timer)
	}
	for key, timer := range s.alarmRecordingTimers {
		if timer != nil {
			timer.Stop()
		}
		delete(s.alarmRecordingTimers, key)
	}
	for key := range s.alarmRecordingSeq {
		delete(s.alarmRecordingSeq, key)
	}
	s.recordingMu.Unlock()
}

func (s *Server) handleAlarmRecordingTimeout(sourceID string, seq uint64) {
	if s == nil || s.db == nil {
		return
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return
	}
	s.recordingMu.Lock()
	current, ok := s.alarmRecordingSeq[sourceID]
	if !ok || current != seq {
		s.recordingMu.Unlock()
		return
	}
	delete(s.alarmRecordingSeq, sourceID)
	delete(s.alarmRecordingTimers, sourceID)
	s.recordingMu.Unlock()

	var source model.MediaSource
	if err := s.db.Where("id = ?", sourceID).First(&source).Error; err != nil {
		return
	}
	recordingPolicy, _, _, taskRunning, err := s.resolveTaskRecordingPolicyBySourceID(sourceID)
	if err != nil || !taskRunning || recordingPolicy != model.RecordingPolicyAlarmClip {
		_ = s.setRecordingStatus(sourceID, recordingStatusStopped)
		return
	}
	if err := s.stopRecordingForSource(source); err != nil {
		logutil.Warnf("alarm recording timeout stop failed: source_id=%s err=%v", sourceID, err)
		return
	}
}

func (s *Server) handleZLMOnRecordMP4(c *gin.Context) {
	if !s.verifyZLMHookSecret(c) {
		zlmHookDeny(c, "invalid zlm hook secret")
		return
	}
	var in zlmOnRecordMP4Input
	if err := c.ShouldBindJSON(&in); err != nil {
		zlmHookDeny(c, "invalid payload")
		return
	}
	app, stream := normalizeZLMHookAppStream(in.App, in.Stream, in.AppName, in.StreamName)
	if app == "" || stream == "" {
		zlmHookOK(c)
		return
	}
	source, err := s.findMediaSourceByAppStream(app, stream)
	if err != nil || source == nil {
		zlmHookOK(c)
		return
	}
	recordingPolicy, preSeconds, postSeconds, taskRunning, _ := s.resolveTaskRecordingPolicyBySourceID(source.ID)
	if !taskRunning {
		recordingPolicy = model.RecordingPolicyNone
	}
	if recordingPolicy == model.RecordingPolicyAlarmClip && s.shouldRunAlarmClipBuffer(source) {
		_ = s.setRecordingStatus(source.ID, recordingStatusBuffering)
		_ = s.pruneAlarmBufferFiles(source.ID, preSeconds, postSeconds)
	} else {
		_ = s.setRecordingStatus(source.ID, recordingStatusStopped)
	}
	zlmHookOK(c)
}
