package server

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"maas-box/internal/logutil"
	"maas-box/internal/model"
)

func (s *Server) schedulePullSourceAutoHeal(sourceID, reason string) {
	if s == nil || s.db == nil || s.cfg == nil {
		return
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}
	s.pullHealMu.Lock()
	if s.pullHealRunning == nil {
		s.pullHealRunning = make(map[string]bool)
	}
	if s.pullHealPending == nil {
		s.pullHealPending = make(map[string]bool)
	}
	// 同一路缺流可能被连续回调，这里按 source_id 合并，避免并发重复建代理。
	if s.pullHealRunning[sourceID] {
		s.pullHealPending[sourceID] = true
		s.pullHealMu.Unlock()
		logutil.Infof("pull auto heal queued: source_id=%s reason=%s", sourceID, reason)
		return
	}
	s.pullHealRunning[sourceID] = true
	s.pullHealMu.Unlock()

	go s.runPullSourceAutoHeal(sourceID, reason)
}

func (s *Server) runPullSourceAutoHeal(sourceID, reason string) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return
	}
	for {
		s.runPullSourceAutoHealWithRetry(sourceID, reason)

		s.pullHealMu.Lock()
		pending := s.pullHealPending[sourceID]
		if pending {
			s.pullHealPending[sourceID] = false
			s.pullHealMu.Unlock()
			reason = "coalesced_pending"
			continue
		}
		delete(s.pullHealRunning, sourceID)
		delete(s.pullHealPending, sourceID)
		s.pullHealMu.Unlock()
		return
	}
}

func (s *Server) runPullSourceAutoHealWithRetry(sourceID, reason string) {
	backoffs := []time.Duration{0, 1200 * time.Millisecond, 3 * time.Second}
	for idx, delay := range backoffs {
		attempt := idx + 1
		if delay > 0 {
			time.Sleep(delay)
		}
		if err := s.autoHealPullSourceOnce(sourceID, attempt, reason); err != nil {
			logutil.Warnf("pull auto heal attempt failed: source_id=%s attempt=%d reason=%s err=%v", sourceID, attempt, reason, err)
			continue
		}
		return
	}
}

func (s *Server) autoHealPullSourceOnce(sourceID string, attempt int, reason string) error {
	if s == nil || s.db == nil || s.cfg == nil {
		return nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil
	}
	if attempt <= 0 {
		attempt = 1
	}

	source, proxy, err := s.loadPullSourceHealTarget(sourceID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(source.ID) == "" {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(source.SourceType), model.SourceTypePull) {
		return nil
	}
	if !proxy.Enable && strings.TrimSpace(proxy.SourceID) != "" {
		logutil.Infof("pull auto heal skipped: source_id=%s reason=proxy_disabled trigger=%s", sourceID, reason)
		return nil
	}

	originURL := strings.TrimSpace(firstNonEmpty(proxy.OriginURL, source.StreamURL))
	if originURL == "" {
		return errors.New("empty origin url")
	}
	transport := strings.ToLower(strings.TrimSpace(firstNonEmpty(proxy.Transport, source.Transport, "tcp")))
	if transport != "udp" {
		transport = "tcp"
	}
	app, stream := parseDeviceZLMAppStream(model.ProtocolRTSP, source, strings.TrimSpace(s.cfg.Server.ZLM.App))
	if active, activeErr := s.isZLMStreamActive(app, stream, model.ProtocolRTSP, model.ProtocolRTMP); activeErr != nil {
		return activeErr
	} else if active {
		logutil.Infof(
			"pull auto heal skipped active: source_id=%s app=%s stream=%s attempt=%d reason=%s",
			sourceID, app, stream, attempt, reason,
		)
		return nil
	}

	retryCount := normalizeStreamProxyRetryCount(proxy.RetryCount)
	output, err := s.ensureZLMProxy(sourceID, originURL, transport, retryCount, "")
	if err != nil {
		return err
	}

	updated := source
	updated.Protocol = model.ProtocolRTSP
	updated.Transport = transport
	updated.StreamURL = originURL
	updated.App = strings.TrimSpace(firstNonEmpty(output["zlm_app"], updated.App, app))
	updated.StreamID = strings.TrimSpace(firstNonEmpty(output["zlm_stream"], updated.StreamID, stream, buildZLMStreamID(sourceID)))
	applyOutputConfigToSource(&updated, output)
	if err := s.db.Model(&model.MediaSource{}).Where("id = ?", sourceID).Updates(map[string]any{
		"protocol":          updated.Protocol,
		"transport":         updated.Transport,
		"stream_url":        updated.StreamURL,
		"app":               updated.App,
		"stream_id":         updated.StreamID,
		"output_config":     updated.OutputConfig,
		"play_webrtc_url":   updated.PlayWebRTCURL,
		"play_ws_flv_url":   updated.PlayWSFLVURL,
		"play_http_flv_url": updated.PlayHTTPFLVURL,
		"play_hls_url":      updated.PlayHLSURL,
		"play_rtsp_url":     updated.PlayRTSPURL,
		"play_rtmp_url":     updated.PlayRTMPURL,
		"updated_at":        time.Now(),
	}).Error; err != nil {
		logutil.Warnf("pull auto heal persist failed: source_id=%s app=%s stream=%s err=%v", sourceID, updated.App, updated.StreamID, err)
	}
	logutil.Infof(
		"pull auto heal success: source_id=%s app=%s stream=%s retry_count=%d attempt=%d reason=%s",
		sourceID, updated.App, updated.StreamID, retryCount, attempt, reason,
	)
	return nil
}

func (s *Server) loadPullSourceHealTarget(sourceID string) (model.MediaSource, model.StreamProxy, error) {
	var (
		source model.MediaSource
		proxy  model.StreamProxy
	)
	if s == nil || s.db == nil {
		return source, proxy, nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return source, proxy, nil
	}
	if err := s.db.Where("id = ?", sourceID).First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.MediaSource{}, model.StreamProxy{}, nil
		}
		return source, proxy, err
	}
	if err := s.db.Where("source_id = ?", sourceID).First(&proxy).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return source, proxy, err
	}
	if strings.TrimSpace(proxy.SourceID) == "" {
		proxy.SourceID = sourceID
	}
	if strings.TrimSpace(proxy.Transport) == "" {
		proxy.Transport = source.Transport
	}
	if strings.TrimSpace(proxy.OriginURL) == "" {
		proxy.OriginURL = source.StreamURL
	}
	if proxy.RetryCount <= 0 {
		proxy.RetryCount = defaultStreamProxyRetryCount
	}
	return source, proxy, nil
}

func buildGBChannelInviteKey(deviceID, channelID string) string {
	deviceID = strings.TrimSpace(deviceID)
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		channelID = deviceID
	}
	return fmt.Sprintf("%s/%s", deviceID, channelID)
}
