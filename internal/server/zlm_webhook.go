package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"maas-box/internal/logutil"
	"maas-box/internal/model"
)

type zlmHookReply struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type zlmOnPublishInput struct {
	App    string `json:"app"`
	Stream string `json:"stream"`
	Schema string `json:"schema"`
	Params string `json:"params"`
	IP     string `json:"ip"`
	Port   int    `json:"port"`
}

type zlmOnStreamChangedInput struct {
	App        string `json:"app"`
	Stream     string `json:"stream"`
	Schema     string `json:"schema"`
	Regist     bool   `json:"regist"`
	AppName    string `json:"app_name"`
	StreamName string `json:"stream_name"`
}

type zlmOnPlayInput struct {
	App        string `json:"app"`
	Stream     string `json:"stream"`
	Schema     string `json:"schema"`
	AppName    string `json:"app_name"`
	StreamName string `json:"stream_name"`
}

type zlmOnRecordMP4Input struct {
	App        string  `json:"app"`
	Stream     string  `json:"stream"`
	AppName    string  `json:"app_name"`
	StreamName string  `json:"stream_name"`
	FileName   string  `json:"file_name"`
	FilePath   string  `json:"file_path"`
	URL        string  `json:"url"`
	TimeLen    float64 `json:"time_len"`
}

const (
	pullRecoverConfirmTimeout      = 2 * time.Second
	pullRecoverConfirmPollInterval = 200 * time.Millisecond
)

func (s *Server) registerWebhookRoutes(r gin.IRouter) {
	group := r.Group("/webhook")
	group.POST("/on_publish", s.handleZLMOnPublish)
	group.POST("/on_stream_changed", s.handleZLMOnStreamChanged)
	group.POST("/on_server_started", s.handleZLMOnServerStarted)
	group.POST("/on_server_keepalive", s.handleZLMNoop)
	group.POST("/on_stream_not_found", s.handleZLMOnStreamNotFound)
	group.POST("/on_record_mp4", s.handleZLMOnRecordMP4)
}

func (s *Server) handleZLMOnPublish(c *gin.Context) {
	if !s.verifyZLMHookSecret(c) {
		zlmHookDeny(c, "invalid zlm hook secret")
		return
	}
	var in zlmOnPublishInput
	if err := c.ShouldBindJSON(&in); err != nil {
		zlmHookDeny(c, "invalid payload")
		return
	}
	schema := strings.ToLower(strings.TrimSpace(in.Schema))
	if schema != "" && schema != model.ProtocolRTMP {
		zlmHookOK(c)
		return
	}
	app := strings.TrimSpace(in.App)
	stream := strings.TrimSpace(in.Stream)
	if app == "" || stream == "" {
		zlmHookDeny(c, "missing app or stream")
		return
	}
	if blocked, berr := s.isRTMPStreamBlocked(app, stream); berr != nil {
		zlmHookDeny(c, "query stream block failed")
		return
	} else if blocked {
		zlmHookDeny(c, "rtmp stream blocked")
		return
	}

	device, err := s.findRTMPDeviceByAppStream(app, stream)
	if err != nil {
		zlmHookDeny(c, "query device failed")
		return
	}
	providedToken := strings.TrimSpace(parseRTMPPublishToken(in.Params))
	globalToken := strings.TrimSpace(s.cfg.Server.ZLM.RTMPAutoPublishToken)
	now := time.Now()
	clientIP := strings.TrimSpace(in.IP)
	if device != nil {
		expectedToken := resolveRtmpExpectedToken(device, globalToken)
		if expectedToken != "" && subtle.ConstantTimeCompare([]byte(providedToken), []byte(expectedToken)) != 1 {
			zlmHookDeny(c, "publish token mismatch")
			return
		}
		if err := s.touchRTMPSourceOnPublish(device.source.ID, clientIP, now); err != nil {
			zlmHookDeny(c, "update rtmp publish state failed")
			return
		}
		if recErr := s.applyRecordingPolicyForSourceID(device.source.ID); recErr != nil {
			logutil.Warnf("apply recording policy failed on on_publish: source_id=%s err=%v", device.source.ID, recErr)
		}
		zlmHookOK(c)
		return
	}

	if globalToken == "" {
		zlmHookDeny(c, "rtmp auto publish token is not configured")
		return
	}
	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(globalToken)) != 1 {
		zlmHookDeny(c, "publish token mismatch")
		return
	}
	sourceMatch, err := s.autoCreateRTMPSourceOnPublish(app, stream, providedToken, clientIP, now, resolveRequestHost(c))
	if err != nil {
		zlmHookDeny(c, "auto create rtmp source failed")
		return
	}
	if sourceMatch != nil {
		if recErr := s.applyRecordingPolicyForSourceID(sourceMatch.source.ID); recErr != nil {
			logutil.Warnf("apply recording policy failed on auto on_publish: source_id=%s err=%v", sourceMatch.source.ID, recErr)
		}
	}
	zlmHookOK(c)
}

func (s *Server) handleZLMOnStreamChanged(c *gin.Context) {
	if !s.verifyZLMHookSecret(c) {
		zlmHookDeny(c, "invalid zlm hook secret")
		return
	}
	var in zlmOnStreamChangedInput
	if err := c.ShouldBindJSON(&in); err != nil {
		zlmHookDeny(c, "invalid payload")
		return
	}
	app := strings.TrimSpace(in.AppName)
	if app == "" {
		app = strings.TrimSpace(in.App)
	}
	stream := strings.TrimSpace(in.StreamName)
	if stream == "" {
		stream = strings.TrimSpace(in.Stream)
	}
	if app == "" || stream == "" {
		zlmHookOK(c)
		return
	}
	source, err := s.findMediaSourceByAppStream(app, stream)
	if err != nil {
		zlmHookDeny(c, "query device failed")
		return
	}
	if source != nil {
		nextStatus := "offline"
		reason := "stream_changed_regist_false"
		if in.Regist {
			nextStatus = "online"
			reason = "stream_changed_regist_true"
		}
		logutil.Infof(
			"stream_changed status sync: source_id=%s old_status=%s new_status=%s reason=%s regist=%t app=%s stream=%s",
			source.ID,
			strings.ToLower(strings.TrimSpace(source.Status)),
			strings.ToLower(strings.TrimSpace(nextStatus)),
			reason,
			in.Regist,
			app,
			stream,
		)
		_ = s.db.Model(&model.MediaSource{}).Where("id = ?", source.ID).
			Updates(map[string]any{"status": strings.ToLower(strings.TrimSpace(nextStatus)), "updated_at": time.Now()}).Error
		if recErr := s.applyRecordingPolicyForSourceID(source.ID); recErr != nil {
			logutil.Warnf("apply recording policy failed on stream_changed: source_id=%s status=%s err=%v", source.ID, nextStatus, recErr)
		}
		if in.Regist {
			go s.resumePendingStartupTaskForDevice(source.ID, "stream_changed_regist_true")
		}
	}
	zlmHookOK(c)
}

func (s *Server) handleZLMOnServerStarted(c *gin.Context) {
	if !s.verifyZLMHookSecret(c) {
		zlmHookDeny(c, "invalid zlm hook secret")
		return
	}
	now := time.Now()
	_ = s.db.Model(&model.MediaSource{}).
		Where("1 = 1").
		Updates(map[string]any{"status": "offline", "recording_status": "stopped", "updated_at": now}).Error
	s.scheduleZLMRestartRecovery("on_server_started")
	zlmHookOK(c)
}

func (s *Server) scheduleZLMRestartRecovery(reason string) {
	if s == nil || s.db == nil || s.cfg == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}
	s.zlmRecoverMu.Lock()
	if s.zlmRecoverRunning {
		s.zlmRecoverPending = true
		s.zlmRecoverMu.Unlock()
		logutil.Infof("zlm restart recovery queued: reason=%s", reason)
		return
	}
	s.zlmRecoverRunning = true
	s.zlmRecoverPending = false
	s.zlmRecoverMu.Unlock()
	go s.runZLMRestartRecovery(reason)
}

func (s *Server) runZLMRestartRecovery(reason string) {
	for {
		s.runZLMRestartRecoveryOnce(reason)
		s.zlmRecoverMu.Lock()
		if s.zlmRecoverPending {
			s.zlmRecoverPending = false
			s.zlmRecoverMu.Unlock()
			reason = "coalesced_pending"
			continue
		}
		s.zlmRecoverRunning = false
		s.zlmRecoverMu.Unlock()
		return
	}
}

func (s *Server) runZLMRestartRecoveryOnce(reason string) {
	if s == nil || s.db == nil {
		return
	}
	candidateTaskIDs, err := s.listAutoResumeCandidateTaskIDs()
	if err != nil {
		logutil.Warnf("runtime recovery load auto resume candidates failed: reason=%s err=%v", reason, err)
	}
	if s.isGoStartupRecovery(reason) {
		if err := s.correctPullSourceStatusOnStartup(reason); err != nil {
			logutil.Warnf("go startup correct pull source status failed: reason=%s err=%v", reason, err)
		}
		if err := s.markGBSourcesOfflineOnStartup(reason); err != nil {
			logutil.Warnf("go startup mark gb offline failed: reason=%s err=%v", reason, err)
		}
	}
	if err := s.recoverPullSourcesAfterZLMRestart(reason); err != nil {
		logutil.Warnf("zlm restart recover pull failed: reason=%s err=%v", reason, err)
	}
	if !s.isGoStartupRecovery(reason) {
		s.recoverGBSourcesAfterZLMRestart(reason)
	}
	if s.cfg != nil && !s.cfg.Server.AI.Disabled {
		if len(candidateTaskIDs) > 0 {
			s.setStartupTaskResumePending(candidateTaskIDs)
		}
		if ok := s.syncAIStatusAndResumeTasks(context.Background(), candidateTaskIDs, reason); !ok {
			s.scheduleStartupTaskResumeRetry()
		}
	}
}

func (s *Server) syncAIStatusAndResumeTasks(ctx context.Context, taskIDs []string, reason string) bool {
	if s == nil || s.db == nil || s.cfg == nil || s.cfg.Server.AI.Disabled {
		return false
	}
	resp, running, err := s.fetchAIRunningDevices(ctx)
	if err != nil {
		s.setStartupAISyncPending(true)
		logutil.Warnf("runtime recovery sync ai status failed: reason=%s err=%v", reason, err)
		return false
	}
	if err := s.syncAllTaskStatusesFromRunningSet(running); err != nil {
		logutil.Warnf("runtime recovery sync task status failed: reason=%s err=%v", reason, err)
	}
	logutil.Infof("runtime recovery ai status synced: reason=%s running_cameras=%d", reason, len(resp.Cameras))
	s.setStartupAISyncPending(false)
	if len(taskIDs) > 0 {
		s.resumeAutoResumeTasks(ctx, taskIDs, reason)
		s.clearStartupTaskResumePending(taskIDs...)
	}
	return true
}

func (s *Server) attemptPendingStartupRecovery(reason string) {
	if s == nil || s.db == nil || s.cfg == nil || s.cfg.Server.AI.Disabled {
		return
	}
	taskIDs := s.pendingStartupTaskResumeIDs()
	if !s.hasStartupRecoveryPending() && len(taskIDs) == 0 {
		return
	}
	if ok := s.syncAIStatusAndResumeTasks(context.Background(), taskIDs, reason); !ok {
		s.scheduleStartupTaskResumeRetry()
	}
}

func (s *Server) recoverPullSourcesAfterZLMRestart(reason string) error {
	if s == nil || s.db == nil || s.cfg == nil {
		return nil
	}
	if s.cfg.Server.ZLM.Disabled {
		return nil
	}
	var sources []model.MediaSource
	if err := s.db.Where("source_type = ?", model.SourceTypePull).Find(&sources).Error; err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}

	sourceIDs := make([]string, 0, len(sources))
	for i := range sources {
		if id := strings.TrimSpace(sources[i].ID); id != "" {
			sourceIDs = append(sourceIDs, id)
		}
	}
	proxyBySourceID := make(map[string]model.StreamProxy, len(sourceIDs))
	if len(sourceIDs) > 0 {
		var proxies []model.StreamProxy
		if err := s.db.Where("source_id IN ?", sourceIDs).Find(&proxies).Error; err != nil {
			return err
		}
		for i := range proxies {
			sourceID := strings.TrimSpace(proxies[i].SourceID)
			if sourceID == "" {
				continue
			}
			proxyBySourceID[sourceID] = proxies[i]
		}
	}

	failures := make([]string, 0)
	for i := range sources {
		source := sources[i]
		sourceID := strings.TrimSpace(source.ID)
		if sourceID == "" {
			continue
		}
		proxy := proxyBySourceID[sourceID]
		originURL := strings.TrimSpace(firstNonEmpty(proxy.OriginURL, source.StreamURL))
		if originURL == "" {
			failures = append(failures, fmt.Sprintf("source=%s empty_origin_url", sourceID))
			continue
		}
		transport := strings.ToLower(strings.TrimSpace(firstNonEmpty(proxy.Transport, source.Transport, "tcp")))
		if transport != "udp" {
			transport = "tcp"
		}
		if err := s.recoverSinglePullSourceAfterZLMRestart(source, proxy, originURL, transport, reason); err != nil {
			failures = append(failures, fmt.Sprintf("source=%s err=%v", sourceID, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (s *Server) recoverSinglePullSourceAfterZLMRestart(source model.MediaSource, proxy model.StreamProxy, originURL, transport, reason string) error {
	if s == nil || s.db == nil {
		return nil
	}
	sourceID := strings.TrimSpace(source.ID)
	if sourceID == "" {
		return nil
	}
	retryCount := normalizeStreamProxyRetryCount(proxy.RetryCount)
	backoffs := []time.Duration{0, 1200 * time.Millisecond, 3 * time.Second}
	var lastErr error
	for idx, delay := range backoffs {
		attempt := idx + 1
		if delay > 0 {
			time.Sleep(delay)
		}
		output, err := s.ensureZLMProxy(sourceID, originURL, transport, retryCount, "")
		if err != nil {
			lastErr = err
			logutil.Warnf(
				"zlm restart recover pull attempt failed: source_id=%s attempt=%d reason=%s err=%v",
				sourceID, attempt, reason, err,
			)
			continue
		}
		updated := source
		updated.Protocol = model.ProtocolRTSP
		updated.Transport = transport
		updated.StreamURL = originURL
		updated.App = strings.TrimSpace(firstNonEmpty(output["zlm_app"], updated.App))
		updated.StreamID = strings.TrimSpace(firstNonEmpty(output["zlm_stream"], updated.StreamID, buildZLMStreamID(sourceID)))
		applyOutputConfigToSource(&updated, output)
		nextStatus := "offline"
		// ZLM 重启后 addStreamProxy 成功不代表上游已经稳定恢复，这里确认活跃后再写在线状态。
		if active, activeErr := s.waitForSourceStreamActive(&updated, pullRecoverConfirmTimeout, pullRecoverConfirmPollInterval); activeErr != nil {
			logutil.Warnf(
				"zlm restart recover pull active confirm failed: source_id=%s attempt=%d reason=%s err=%v",
				sourceID, attempt, reason, activeErr,
			)
		} else if active {
			nextStatus = "online"
		}
		_ = s.db.Model(&model.MediaSource{}).Where("id = ?", sourceID).Updates(map[string]any{
			"protocol":          updated.Protocol,
			"transport":         updated.Transport,
			"stream_url":        updated.StreamURL,
			"app":               updated.App,
			"stream_id":         updated.StreamID,
			"status":            nextStatus,
			"output_config":     updated.OutputConfig,
			"play_webrtc_url":   updated.PlayWebRTCURL,
			"play_ws_flv_url":   updated.PlayWSFLVURL,
			"play_http_flv_url": updated.PlayHTTPFLVURL,
			"play_hls_url":      updated.PlayHLSURL,
			"play_rtsp_url":     updated.PlayRTSPURL,
			"play_rtmp_url":     updated.PlayRTMPURL,
			"updated_at":        time.Now(),
		}).Error
		if nextStatus == "online" {
			_ = s.applyRecordingPolicyForSourceID(sourceID)
			logutil.Infof(
				"zlm restart recover pull success: source_id=%s app=%s stream=%s attempt=%d reason=%s",
				sourceID, updated.App, updated.StreamID, attempt, reason,
			)
		} else {
			logutil.Infof(
				"zlm restart recover pull pending active confirm: source_id=%s app=%s stream=%s attempt=%d reason=%s",
				sourceID, updated.App, updated.StreamID, attempt, reason,
			)
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("unknown pull recover failure")
	}
	return lastErr
}

func (s *Server) recoverGBSourcesAfterZLMRestart(reason string) {
	if s == nil || s.db == nil || s.gbService == nil {
		return
	}
	var gbDevices []model.GBDevice
	if err := s.db.Where("enabled = ? AND status = ?", true, "online").Find(&gbDevices).Error; err != nil {
		logutil.Warnf("zlm restart recover gb query failed: reason=%s err=%v", reason, err)
		return
	}
	for i := range gbDevices {
		deviceID := strings.TrimSpace(gbDevices[i].DeviceID)
		if deviceID == "" {
			continue
		}
		s.scheduleGBDeviceAutoInvite(deviceID, "zlm_restart_recover_"+reason)
	}
}

func (s *Server) isGoStartupRecovery(reason string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(reason)), "go_startup")
}

func (s *Server) correctPullSourceStatusOnStartup(reason string) error {
	if s == nil || s.db == nil || s.cfg == nil || s.cfg.Server.ZLM.Disabled {
		return nil
	}
	active, err := s.listZLMActiveStreams(model.ProtocolRTSP, model.ProtocolRTMP)
	if err != nil {
		return err
	}
	var sources []model.MediaSource
	if err := s.db.Where("source_type = ?", model.SourceTypePull).Find(&sources).Error; err != nil {
		return err
	}
	now := time.Now()
	defaultApp := strings.TrimSpace(s.cfg.Server.ZLM.App)
	for _, source := range sources {
		app, stream := parseDeviceZLMAppStream(strings.ToLower(strings.TrimSpace(source.Protocol)), source, defaultApp)
		nextStatus := "offline"
		if stream != "" {
			if _, ok := active[buildZLMAppStreamKey(app, stream)]; ok {
				nextStatus = "online"
			}
		}
		if strings.EqualFold(strings.TrimSpace(source.Status), nextStatus) {
			continue
		}
		logutil.Infof("go startup pull status corrected: source_id=%s old_status=%s new_status=%s reason=%s", source.ID, source.Status, nextStatus, reason)
		if err := s.db.Model(&model.MediaSource{}).Where("id = ?", source.ID).Updates(map[string]any{
			"status":     nextStatus,
			"updated_at": now,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) markGBSourcesOfflineOnStartup(reason string) error {
	if s == nil || s.db == nil {
		return nil
	}
	now := time.Now()
	if err := s.db.Model(&model.GBDevice{}).
		Where("enabled = ?", true).
		Updates(map[string]any{"status": "offline", "source_addr": "", "updated_at": now}).Error; err != nil {
		return err
	}
	if err := s.db.Model(&model.MediaSource{}).
		Where("source_type = ?", model.SourceTypeGB28181).
		Updates(map[string]any{"status": "offline", "updated_at": now}).Error; err != nil {
		return err
	}
	logutil.Infof("go startup gb sources marked offline: reason=%s", reason)
	return nil
}

func (s *Server) resumeAutoResumeTasks(ctx context.Context, taskIDs []string, reason string) {
	if s == nil || s.db == nil || s.cfg == nil || s.cfg.Server.AI.Disabled {
		return
	}
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" || !s.isTaskAutoResumeEnabled(taskID) {
			continue
		}
		_, runtimes, err := s.loadTaskDeviceContexts(taskID)
		if err != nil {
			logutil.Warnf("runtime recovery load task contexts failed: task_id=%s reason=%s err=%v", taskID, reason, err)
			continue
		}
		deviceIDs := make([]string, 0, len(runtimes))
		selected := make(map[string]struct{})
		for _, item := range runtimes {
			deviceIDs = append(deviceIDs, item.Device.ID)
			if item.Device.AIStatus == model.DeviceAIStatusRunning {
				s.clearStartupResumePending(item.Device.ID)
				continue
			}
			if !s.canAutoResumeDevice(item.Device) {
				continue
			}
			selected[item.Device.ID] = struct{}{}
		}
		s.setStartupResumePending(taskID, deviceIDs)
		if len(selected) == 0 {
			continue
		}
		results, successCount := s.startTaskRuntimes(ctx, taskID, runtimes, selected, "auto_resume_"+reason)
		if _, err := s.updateTaskRuntimeStatus(taskID, runtimes, true); err != nil {
			logutil.Warnf("runtime recovery update task status failed: task_id=%s reason=%s err=%v", taskID, reason, err)
		}
		if successCount == 0 {
			failureMessage := ""
			if len(results) > 0 {
				failureMessage = strings.TrimSpace(results[0].Message)
			}
			logutil.Warnf("runtime recovery task resume pending: task_id=%s reason=%s attempted=%d first_message=%s", taskID, reason, len(results), failureMessage)
		}
	}
}

func (s *Server) resumePendingStartupTaskForDevice(deviceID, reason string) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || s == nil || s.db == nil || s.cfg == nil || s.cfg.Server.AI.Disabled {
		return
	}
	taskID := s.pendingStartupResumeTaskID(deviceID)
	if taskID == "" {
		return
	}
	if !s.isTaskAutoResumeEnabled(taskID) {
		s.clearStartupResumePending(deviceID)
		return
	}
	_, runtimes, err := s.loadTaskDeviceContexts(taskID)
	if err != nil {
		logutil.Warnf("resume pending startup task load contexts failed: task_id=%s device_id=%s reason=%s err=%v", taskID, deviceID, reason, err)
		return
	}
	selected := make(map[string]struct{})
	for _, item := range runtimes {
		if item.Device.ID != deviceID {
			continue
		}
		if item.Device.AIStatus == model.DeviceAIStatusRunning {
			s.clearStartupResumePending(deviceID)
			return
		}
		if !s.canAutoResumeDevice(item.Device) {
			return
		}
		selected[deviceID] = struct{}{}
		break
	}
	if len(selected) == 0 {
		return
	}
	_, successCount := s.startTaskRuntimes(context.Background(), taskID, runtimes, selected, "pending_resume_"+reason)
	if _, err := s.updateTaskRuntimeStatus(taskID, runtimes, true); err != nil {
		logutil.Warnf("resume pending startup task update status failed: task_id=%s device_id=%s reason=%s err=%v", taskID, deviceID, reason, err)
	}
	if successCount > 0 {
		logutil.Infof("resume pending startup task success: task_id=%s device_id=%s reason=%s", taskID, deviceID, reason)
	}
}

func (s *Server) canAutoResumeDevice(device model.Device) bool {
	if device.AIStatus == model.DeviceAIStatusRunning {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(device.Status), "online") {
		return false
	}
	return strings.TrimSpace(pickDeviceRTSPURLForAI(device)) != ""
}

func (s *Server) handleZLMNoop(c *gin.Context) {
	if !s.verifyZLMHookSecret(c) {
		zlmHookDeny(c, "invalid zlm hook secret")
		return
	}
	zlmHookOK(c)
}

func (s *Server) handleZLMOnStreamNotFound(c *gin.Context) {
	if !s.verifyZLMHookSecret(c) {
		zlmHookDeny(c, "invalid zlm hook secret")
		return
	}
	var in zlmOnPlayInput
	if err := c.ShouldBindJSON(&in); err != nil {
		logutil.Warnf("zlm hook on_stream_not_found ignored invalid payload: err=%v", err)
		zlmHookOK(c)
		return
	}
	app, stream := normalizeZLMHookAppStream(in.App, in.Stream, in.AppName, in.StreamName)
	logutil.Infof(
		"zlm hook on_stream_not_found: app=%s stream=%s schema=%s",
		strings.TrimSpace(app),
		strings.TrimSpace(stream),
		strings.TrimSpace(in.Schema),
	)
	source, err := s.findMediaSourceByAppStream(app, stream)
	if err != nil {
		logutil.Warnf("zlm hook on_stream_not_found lookup failed: app=%s stream=%s err=%v", app, stream, err)
		zlmHookOK(c)
		return
	}
	if source == nil {
		logutil.Infof("zlm hook on_stream_not_found skipped unknown source: app=%s stream=%s", app, stream)
		zlmHookOK(c)
		return
	}
	// on_stream_not_found 只负责触发后台自愈，媒体在线状态仍由 on_stream_changed 统一驱动。
	switch strings.ToLower(strings.TrimSpace(source.SourceType)) {
	case model.SourceTypePull:
		s.schedulePullSourceAutoHeal(source.ID, "on_stream_not_found")
		logutil.Infof("zlm hook on_stream_not_found scheduled pull auto heal: source_id=%s app=%s stream=%s", source.ID, app, stream)
	case model.SourceTypeGB28181:
		if !strings.EqualFold(strings.TrimSpace(source.RowKind), model.RowKindChannel) {
			logutil.Infof("zlm hook on_stream_not_found skipped gb non-channel source: source_id=%s row_kind=%s", source.ID, source.RowKind)
			break
		}
		deviceID, channelID, resolveErr := s.resolveGBChannelAutoInviteIDs(*source)
		if resolveErr != nil {
			logutil.Warnf("zlm hook on_stream_not_found resolve gb ids failed: source_id=%s app=%s stream=%s err=%v", source.ID, app, stream, resolveErr)
			break
		}
		s.scheduleGBChannelAutoInvite(deviceID, channelID, "on_stream_not_found")
		logutil.Infof("zlm hook on_stream_not_found scheduled gb channel auto invite: source_id=%s device_id=%s channel_id=%s", source.ID, deviceID, channelID)
	default:
		logutil.Infof("zlm hook on_stream_not_found skipped source auto heal: source_id=%s source_type=%s app=%s stream=%s", source.ID, source.SourceType, app, stream)
	}
	zlmHookOK(c)
}

func (s *Server) resolveGBChannelAutoInviteIDs(source model.MediaSource) (string, string, error) {
	outputConfigMap, err := parseOutputConfigMap(source.OutputConfig)
	if err != nil {
		outputConfigMap = map[string]string{}
	}
	return s.resolveGBPreviewIDs((*model.Device)(&source), outputConfigMap)
}

func normalizeZLMHookAppStream(app, stream, appName, streamName string) (string, string) {
	app = strings.TrimSpace(app)
	if app == "" {
		app = strings.TrimSpace(appName)
	}
	stream = strings.TrimSpace(stream)
	if stream == "" {
		stream = strings.TrimSpace(streamName)
	}
	return app, stream
}

func (s *Server) verifyZLMHookSecret(c *gin.Context) bool {
	expected := strings.TrimSpace(s.cfg.Server.ZLM.Secret)
	if expected == "" {
		return true
	}
	got := strings.TrimSpace(c.Query("secret"))
	if got == "" {
		got = strings.TrimSpace(c.GetHeader("X-ZLM-Secret"))
	}
	if got == "" {
		got = strings.TrimSpace(c.GetHeader("X-Zlm-Secret"))
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

type rtmpSourceMatch struct {
	source    model.MediaSource
	pushToken string
}

func resolveRtmpExpectedToken(match *rtmpSourceMatch, globalToken string) string {
	if match == nil {
		return strings.TrimSpace(globalToken)
	}
	if token := strings.TrimSpace(match.pushToken); token != "" {
		return token
	}
	if token := strings.TrimSpace(parsePublishTokenFromRTMPURL(match.source.StreamURL)); token != "" {
		return token
	}
	return strings.TrimSpace(globalToken)
}

func (s *Server) touchRTMPSourceOnPublish(sourceID, clientIP string, now time.Time) error {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return errors.New("empty source id")
	}
	if now.IsZero() {
		now = time.Now()
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.MediaSource{}).Where("id = ?", sourceID).
			Update("updated_at", now).Error; err != nil {
			return err
		}
		updateFields := map[string]any{
			"last_push_at": now,
			"client_ip":    strings.TrimSpace(clientIP),
			"updated_at":   now,
		}
		item := model.StreamPush{
			SourceID:   sourceID,
			LastPushAt: now,
			ClientIP:   strings.TrimSpace(clientIP),
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "source_id"}},
			DoUpdates: clause.Assignments(updateFields),
		}).Create(&item).Error
	})
}

func (s *Server) autoCreateRTMPSourceOnPublish(app, stream, publishToken, clientIP string, now time.Time, requestHost string) (*rtmpSourceMatch, error) {
	app = strings.TrimSpace(app)
	stream = strings.TrimSpace(stream)
	publishToken = strings.TrimSpace(publishToken)
	clientIP = strings.TrimSpace(clientIP)
	if app == "" || stream == "" {
		return nil, errors.New("missing app or stream")
	}
	if now.IsZero() {
		now = time.Now()
	}
	defaultName := fmt.Sprintf("RTMP-%s/%s", app, stream)
	returnMatch := &rtmpSourceMatch{}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var source model.MediaSource
		err := tx.Where("app = ? AND stream_id = ?", app, stream).First(&source).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			source = model.MediaSource{
				ID:            uuid.NewString(),
				Name:          defaultName,
				AreaID:        model.RootAreaID,
				SourceType:    model.SourceTypePush,
				RowKind:       model.RowKindChannel,
				ParentID:      "",
				Protocol:      model.ProtocolRTMP,
				Transport:     "tcp",
				App:           app,
				StreamID:      stream,
				Status:        "offline",
				AIStatus:      model.DeviceAIStatusIdle,
				MediaServerID: "local",
				ExtraJSON:     "{}",
				OutputConfig:  "{}",
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			applyDefaultRecordingPolicyToSource(s.cfg, &source)
			applyDefaultAlarmClipPolicyToSource(s.cfg, &source)
			output := s.buildZLMOutputConfig(app, stream, requestHost)
			source.StreamURL = s.buildZLMRTMPPublishURL(app, stream, requestHost)
			output["publish_url"] = source.StreamURL
			if publishToken != "" {
				output["publish_token"] = publishToken
			}
			applyOutputConfigToSource(&source, output)
			if err := tx.Create(&source).Error; err != nil {
				if !isUniqueConstraintError(err) {
					return err
				}
				if err := tx.Where("app = ? AND stream_id = ?", app, stream).First(&source).Error; err != nil {
					return err
				}
			}
		}
		if source.SourceType != model.SourceTypePush || source.Protocol != model.ProtocolRTMP {
			return errors.New("stream occupied by non-rtmp push source")
		}
		updateSource := map[string]any{
			"updated_at": now,
		}
		if strings.TrimSpace(source.AreaID) == "" {
			updateSource["area_id"] = model.RootAreaID
		}
		if strings.TrimSpace(source.Name) == "" {
			updateSource["name"] = defaultName
		}
		if strings.TrimSpace(source.StreamURL) == "" || strings.TrimSpace(source.OutputConfig) == "" || strings.TrimSpace(source.OutputConfig) == "{}" {
			output := s.buildZLMOutputConfig(app, stream, requestHost)
			streamURL := s.buildZLMRTMPPublishURL(app, stream, requestHost)
			output["publish_url"] = streamURL
			if publishToken != "" {
				output["publish_token"] = publishToken
			}
			source.StreamURL = streamURL
			source.App = app
			source.StreamID = stream
			applyOutputConfigToSource(&source, output)
			updateSource["stream_url"] = source.StreamURL
			updateSource["output_config"] = source.OutputConfig
			updateSource["play_webrtc_url"] = source.PlayWebRTCURL
			updateSource["play_ws_flv_url"] = source.PlayWSFLVURL
			updateSource["play_http_flv_url"] = source.PlayHTTPFLVURL
			updateSource["play_hls_url"] = source.PlayHLSURL
			updateSource["play_rtsp_url"] = source.PlayRTSPURL
			updateSource["play_rtmp_url"] = source.PlayRTMPURL
		}
		if err := tx.Model(&model.MediaSource{}).Where("id = ?", source.ID).Updates(updateSource).Error; err != nil {
			return err
		}

		push := model.StreamPush{
			SourceID:     source.ID,
			PublishToken: publishToken,
			LastPushAt:   now,
			ClientIP:     clientIP,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		pushUpdate := map[string]any{
			"last_push_at": now,
			"client_ip":    clientIP,
			"updated_at":   now,
		}
		if publishToken != "" {
			pushUpdate["publish_token"] = publishToken
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "source_id"}},
			DoUpdates: clause.Assignments(pushUpdate),
		}).Create(&push).Error; err != nil {
			return err
		}
		var refreshed model.MediaSource
		if err := tx.Where("id = ?", source.ID).First(&refreshed).Error; err != nil {
			return err
		}
		returnMatch.source = refreshed
		returnMatch.pushToken = publishToken
		return nil
	})
	if err != nil {
		return nil, err
	}
	return returnMatch, nil
}

func (s *Server) findRTMPDeviceByAppStream(app, stream string) (*rtmpSourceMatch, error) {
	app = strings.TrimSpace(app)
	stream = strings.TrimSpace(stream)
	if app == "" || stream == "" {
		return nil, nil
	}
	var source model.MediaSource
	if err := s.db.Where(
		"source_type = ? AND protocol = ? AND app = ? AND stream_id = ?",
		model.SourceTypePush, model.ProtocolRTMP, app, stream,
	).First(&source).Error; err == nil {
		var push model.StreamPush
		_ = s.db.Where("source_id = ?", source.ID).First(&push).Error
		return &rtmpSourceMatch{source: source, pushToken: strings.TrimSpace(push.PublishToken)}, nil
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		// Fallback to full scan only when direct query fails by non-notfound error.
		return nil, err
	}

	var sources []model.MediaSource
	if err := s.db.Where("source_type = ? AND protocol = ?", model.SourceTypePush, model.ProtocolRTMP).Find(&sources).Error; err != nil {
		return nil, err
	}
	defaultApp := strings.TrimSpace(s.cfg.Server.ZLM.App)
	for i := range sources {
		candidateApp, candidateStream := parseDeviceZLMAppStream(model.ProtocolRTMP, sources[i], defaultApp)
		if strings.EqualFold(candidateApp, app) && strings.EqualFold(candidateStream, stream) {
			var push model.StreamPush
			_ = s.db.Where("source_id = ?", sources[i].ID).First(&push).Error
			return &rtmpSourceMatch{source: sources[i], pushToken: strings.TrimSpace(push.PublishToken)}, nil
		}
	}
	return nil, nil
}

func (s *Server) findMediaSourceByAppStream(app, stream string) (*model.MediaSource, error) {
	app = strings.TrimSpace(app)
	stream = strings.TrimSpace(stream)
	if app == "" || stream == "" {
		return nil, nil
	}
	var source model.MediaSource
	if err := s.db.Where("app = ? AND stream_id = ?", app, stream).First(&source).Error; err == nil {
		return &source, nil
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var sources []model.MediaSource
	if err := s.db.Find(&sources).Error; err != nil {
		return nil, err
	}
	defaultApp := strings.TrimSpace(s.cfg.Server.ZLM.App)
	for i := range sources {
		candidateApp, candidateStream := parseDeviceZLMAppStream(strings.ToLower(strings.TrimSpace(sources[i].Protocol)), sources[i], defaultApp)
		if strings.EqualFold(candidateApp, app) && strings.EqualFold(candidateStream, stream) {
			return &sources[i], nil
		}
	}
	return nil, nil
}

func zlmHookOK(c *gin.Context) {
	c.JSON(http.StatusOK, zlmHookReply{Code: 0, Msg: "success"})
}

func zlmHookDeny(c *gin.Context, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "denied"
	}
	c.JSON(http.StatusOK, zlmHookReply{Code: 1, Msg: msg})
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}
	return strings.Contains(text, "unique") || strings.Contains(text, "duplicate")
}
