package server

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"maas-box/internal/config"
	"maas-box/internal/gb28181"
	"maas-box/internal/model"
)

type gb28181Store struct {
	db  *gorm.DB
	cfg *config.Config
	srv *Server
}

func (s *Server) startGB28181() error {
	if s == nil || s.cfg == nil || s.db == nil {
		return nil
	}
	if !s.cfg.Server.SIP.Enabled {
		return nil
	}
	service, err := gb28181.NewService(gb28181.Config{
		Enabled:          s.cfg.Server.SIP.Enabled,
		ListenIP:         s.cfg.Server.SIP.ListenIP,
		Port:             s.cfg.Server.SIP.Port,
		ServerID:         s.cfg.Server.SIP.ServerID,
		Domain:           s.cfg.Server.SIP.Domain,
		Password:         s.cfg.Server.SIP.Password,
		KeepaliveTimeout: s.cfg.Server.SIP.KeepaliveTimeout,
		RegisterGrace:    s.cfg.Server.SIP.RegisterGrace,
	}, &gb28181Store{db: s.db, cfg: s.cfg, srv: s})
	if err != nil {
		return err
	}
	if err := service.Start(); err != nil {
		return err
	}
	s.gbService = service
	return nil
}

func (s *Server) reconcileAllGBSourceStatus() error {
	if s == nil || s.db == nil {
		return nil
	}
	var rows []struct {
		DeviceID string `gorm:"column:device_id"`
	}
	if err := s.db.Model(&model.GBDevice{}).Select("device_id").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	store := &gb28181Store{db: s.db, cfg: s.cfg}
	seen := make(map[string]struct{}, len(rows))
	total := 0
	success := 0
	failed := 0
	var firstErr error
	for _, row := range rows {
		deviceID := strings.TrimSpace(row.DeviceID)
		if deviceID == "" {
			continue
		}
		if _, ok := seen[deviceID]; ok {
			continue
		}
		seen[deviceID] = struct{}{}
		total++

		beforeStatusBySource, beforeErr := s.collectGBSourceStatuses(deviceID)
		if beforeErr != nil {
			failed++
			if firstErr == nil {
				firstErr = beforeErr
			}
			log.Printf("gb source status reconcile pre-check failed: device_id=%s err=%v", deviceID, beforeErr)
			continue
		}
		if err := store.syncGBDeviceAndChannels(deviceID); err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			log.Printf("gb source status reconcile sync failed: device_id=%s err=%v", deviceID, err)
			continue
		}
		afterStatusBySource, afterErr := s.collectGBSourceStatuses(deviceID)
		if afterErr != nil {
			failed++
			if firstErr == nil {
				firstErr = afterErr
			}
			log.Printf("gb source status reconcile post-check failed: device_id=%s err=%v", deviceID, afterErr)
			continue
		}
		success++
		changed := false
		unionSourceIDs := make(map[string]struct{}, len(beforeStatusBySource)+len(afterStatusBySource))
		for sourceID := range beforeStatusBySource {
			unionSourceIDs[sourceID] = struct{}{}
		}
		for sourceID := range afterStatusBySource {
			unionSourceIDs[sourceID] = struct{}{}
		}
		for sourceID := range unionSourceIDs {
			sourceID = strings.TrimSpace(sourceID)
			if sourceID == "" {
				continue
			}
			oldStatus := strings.ToLower(strings.TrimSpace(beforeStatusBySource[sourceID]))
			newStatus := strings.ToLower(strings.TrimSpace(afterStatusBySource[sourceID]))
			if oldStatus == newStatus {
				continue
			}
			changed = true
			log.Printf(
				"gb source status reconciled: source_id=%s device_id=%s old_status=%s new_status=%s reason=startup_reconcile",
				sourceID,
				deviceID,
				oldStatus,
				newStatus,
			)
		}
		if !changed {
			log.Printf(
				"gb source status reconcile checked: device_id=%s reason=startup_reconcile",
				deviceID,
			)
		}
	}
	log.Printf(
		"gb source status reconcile summary: total=%d success=%d failed=%d",
		total,
		success,
		failed,
	)
	return firstErr
}

func (s *Server) collectGBSourceStatuses(deviceID string) (map[string]string, error) {
	deviceID = strings.TrimSpace(deviceID)
	out := make(map[string]string, 4)
	if s == nil || s.db == nil || deviceID == "" {
		return out, nil
	}

	var gbDevice model.GBDevice
	if err := s.db.Select("source_id_device").Where("device_id = ?", deviceID).First(&gbDevice).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, nil
		}
		return nil, err
	}

	sourceIDs := make([]string, 0, 8)
	if sourceID := strings.TrimSpace(gbDevice.SourceIDDevice); sourceID != "" {
		sourceIDs = append(sourceIDs, sourceID)
	}

	var channels []model.GBChannel
	if err := s.db.Select("source_id_channel").Where("device_id = ?", deviceID).Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, channel := range channels {
		if sourceID := strings.TrimSpace(channel.SourceIDChannel); sourceID != "" {
			sourceIDs = append(sourceIDs, sourceID)
		}
	}
	if len(sourceIDs) == 0 {
		return out, nil
	}

	uniqSourceIDs := make([]string, 0, len(sourceIDs))
	seen := make(map[string]struct{}, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		sourceID = strings.TrimSpace(sourceID)
		if sourceID == "" {
			continue
		}
		if _, ok := seen[sourceID]; ok {
			continue
		}
		seen[sourceID] = struct{}{}
		uniqSourceIDs = append(uniqSourceIDs, sourceID)
	}
	if len(uniqSourceIDs) == 0 {
		return out, nil
	}

	var sources []model.MediaSource
	if err := s.db.Select("id", "status").Where("id IN ?", uniqSourceIDs).Find(&sources).Error; err != nil {
		return nil, err
	}
	for _, sourceID := range uniqSourceIDs {
		out[sourceID] = ""
	}
	for _, source := range sources {
		sourceID := strings.TrimSpace(source.ID)
		if sourceID == "" {
			continue
		}
		out[sourceID] = strings.ToLower(strings.TrimSpace(source.Status))
	}
	return out, nil
}

func (s *Server) forgetGBDeviceSession(deviceID string) {
	if s == nil || s.gbService == nil {
		return
	}
	s.gbService.ForgetDevice(strings.TrimSpace(deviceID))
}

func (s *Server) stopGBDevicePlay(deviceID string) {
	if s == nil || s.gbService == nil {
		return
	}
	_ = s.gbService.StopAllPlays(strings.TrimSpace(deviceID))
}

func (s *Server) scheduleGBDeviceAutoInvite(deviceID, reason string) {
	if s == nil || s.db == nil || s.gbService == nil || s.cfg == nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}
	s.gbInviteMu.Lock()
	if s.gbInviteRunning == nil {
		s.gbInviteRunning = make(map[string]bool)
	}
	if s.gbInvitePending == nil {
		s.gbInvitePending = make(map[string]bool)
	}
	if s.gbInviteRunning[deviceID] {
		s.gbInvitePending[deviceID] = true
		s.gbInviteMu.Unlock()
		log.Printf("gb auto invite queued: device_id=%s reason=%s", deviceID, reason)
		return
	}
	s.gbInviteRunning[deviceID] = true
	s.gbInviteMu.Unlock()

	go s.runGBDeviceAutoInvite(deviceID, reason)
}

func (s *Server) runGBDeviceAutoInvite(deviceID, reason string) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return
	}
	for {
		s.runGBDeviceAutoInviteWithRetry(deviceID, reason)

		s.gbInviteMu.Lock()
		pending := s.gbInvitePending[deviceID]
		if pending {
			s.gbInvitePending[deviceID] = false
			s.gbInviteMu.Unlock()
			reason = "coalesced_pending"
			continue
		}
		delete(s.gbInviteRunning, deviceID)
		delete(s.gbInvitePending, deviceID)
		s.gbInviteMu.Unlock()
		return
	}
}

func (s *Server) runGBDeviceAutoInviteWithRetry(deviceID, reason string) {
	backoffs := []time.Duration{0, 1200 * time.Millisecond, 3 * time.Second}
	for idx, delay := range backoffs {
		attempt := idx + 1
		if delay > 0 {
			time.Sleep(delay)
		}
		err := s.inviteGBDeviceChannelsOnce(deviceID, attempt, reason)
		if err == nil {
			return
		}
		log.Printf("gb auto invite attempt failed: device_id=%s attempt=%d reason=%s err=%v", deviceID, attempt, reason, err)
	}
}

func (s *Server) scheduleGBChannelAutoInvite(deviceID, channelID, reason string) {
	if s == nil || s.db == nil || s.gbService == nil || s.cfg == nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	channelID = strings.TrimSpace(channelID)
	if deviceID == "" {
		return
	}
	if channelID == "" {
		channelID = deviceID
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}
	key := buildGBChannelInviteKey(deviceID, channelID)
	s.gbInviteChannelMu.Lock()
	if s.gbInviteChannelRunning == nil {
		s.gbInviteChannelRunning = make(map[string]bool)
	}
	if s.gbInviteChannelPending == nil {
		s.gbInviteChannelPending = make(map[string]bool)
	}
	// 缺流回调可能在播放端连续触发，这里按“设备+通道”合并，避免把同一路补邀打爆。
	if s.gbInviteChannelRunning[key] {
		s.gbInviteChannelPending[key] = true
		s.gbInviteChannelMu.Unlock()
		log.Printf("gb channel auto invite queued: device_id=%s channel_id=%s reason=%s", deviceID, channelID, reason)
		return
	}
	s.gbInviteChannelRunning[key] = true
	s.gbInviteChannelMu.Unlock()

	go s.runGBChannelAutoInvite(deviceID, channelID, reason)
}

func (s *Server) runGBChannelAutoInvite(deviceID, channelID, reason string) {
	deviceID = strings.TrimSpace(deviceID)
	channelID = strings.TrimSpace(channelID)
	if deviceID == "" {
		return
	}
	if channelID == "" {
		channelID = deviceID
	}
	key := buildGBChannelInviteKey(deviceID, channelID)
	for {
		s.runGBChannelAutoInviteWithRetry(deviceID, channelID, reason)

		s.gbInviteChannelMu.Lock()
		pending := s.gbInviteChannelPending[key]
		if pending {
			s.gbInviteChannelPending[key] = false
			s.gbInviteChannelMu.Unlock()
			reason = "coalesced_pending"
			continue
		}
		delete(s.gbInviteChannelRunning, key)
		delete(s.gbInviteChannelPending, key)
		s.gbInviteChannelMu.Unlock()
		return
	}
}

func (s *Server) runGBChannelAutoInviteWithRetry(deviceID, channelID, reason string) {
	backoffs := []time.Duration{0, 1200 * time.Millisecond, 3 * time.Second}
	for idx, delay := range backoffs {
		attempt := idx + 1
		if delay > 0 {
			time.Sleep(delay)
		}
		err := s.inviteGBDeviceChannelOnce(deviceID, channelID, attempt, reason)
		if err == nil {
			return
		}
		log.Printf("gb channel auto invite attempt failed: device_id=%s channel_id=%s attempt=%d reason=%s err=%v", deviceID, channelID, attempt, reason, err)
	}
}

func (s *Server) inviteGBDeviceChannelOnce(deviceID, channelID string, attempt int, reason string) error {
	if s == nil || s.db == nil || s.gbService == nil || s.cfg == nil {
		return nil
	}
	deviceID = strings.TrimSpace(deviceID)
	channelID = strings.TrimSpace(channelID)
	if deviceID == "" {
		return nil
	}
	if channelID == "" {
		channelID = deviceID
	}
	if attempt <= 0 {
		attempt = 1
	}

	var device model.GBDevice
	if err := s.db.Where("device_id = ?", deviceID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if !device.Enabled || !strings.EqualFold(strings.TrimSpace(device.Status), "online") {
		return nil
	}

	var channel model.GBChannel
	if err := s.db.Where("device_id = ? AND channel_id = ?", deviceID, channelID).First(&channel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	source, sourceErr := s.loadGBChannelSource(deviceID, channel)
	if sourceErr != nil {
		return fmt.Errorf("load_source_failed:%w", sourceErr)
	}
	return s.inviteSingleGBChannel(device, channel, source, s.resolveGBMediaIP(nil), time.Now(), attempt, reason)
}

func (s *Server) inviteGBDeviceChannelsOnce(deviceID string, attempt int, reason string) error {
	if s == nil || s.db == nil || s.gbService == nil || s.cfg == nil {
		return nil
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	if attempt <= 0 {
		attempt = 1
	}
	var device model.GBDevice
	if err := s.db.Where("device_id = ?", deviceID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if !device.Enabled || !strings.EqualFold(strings.TrimSpace(device.Status), "online") {
		return nil
	}
	var channels []model.GBChannel
	if err := s.db.Where("device_id = ?", deviceID).Find(&channels).Error; err != nil {
		return err
	}
	if len(channels) == 0 {
		return nil
	}

	mediaIP := s.resolveGBMediaIP(nil)
	now := time.Now()
	errs := make([]string, 0)
	for _, channel := range channels {
		channelID := strings.TrimSpace(channel.ChannelID)
		if channelID == "" {
			continue
		}
		source, sourceErr := s.loadGBChannelSource(deviceID, channel)
		if sourceErr != nil {
			errs = append(errs, fmt.Sprintf("channel=%s load_source_failed:%v", channelID, sourceErr))
			continue
		}
		if err := s.inviteSingleGBChannel(device, channel, source, mediaIP, now, attempt, reason); err != nil {
			errs = append(errs, fmt.Sprintf("channel=%s %v", channelID, err))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (s *Server) inviteSingleGBChannel(device model.GBDevice, channel model.GBChannel, source *model.MediaSource, mediaIP string, now time.Time, attempt int, reason string) error {
	deviceID := strings.TrimSpace(device.DeviceID)
	channelID := strings.TrimSpace(channel.ChannelID)
	if deviceID == "" || channelID == "" {
		return nil
	}
	app := "rtp"
	streamID := buildGBChannelSourceStreamID(deviceID, channelID)
	if source != nil {
		if v := strings.TrimSpace(source.App); v != "" {
			app = v
		}
		if v := strings.TrimSpace(source.StreamID); v != "" {
			streamID = v
		}
	}
	streamActive, activeErr := s.isZLMStreamActive(app, streamID)
	if activeErr != nil {
		return fmt.Errorf("query_stream_failed:%w", activeErr)
	}
	if streamActive {
		log.Printf(
			"gb auto invite skip active: device_id=%s channel_id=%s app=%s stream=%s attempt=%d reason=%s",
			deviceID, channelID, app, streamID, attempt, reason,
		)
		return nil
	}

	mediaPort, portErr := s.openZLMRTPServer(streamID, 0, 0)
	if portErr != nil {
		return fmt.Errorf("open_rtp_failed:%w", portErr)
	}
	inviteErr := s.gbService.InvitePlay(deviceID, channelID, mediaIP, mediaPort, streamID)
	if inviteErr != nil {
		_ = s.closeZLMRTPServer(streamID)
		return fmt.Errorf("invite_failed:%w", inviteErr)
	}

	log.Printf(
		"gb auto invite success: device_id=%s channel_id=%s app=%s stream=%s media_ip=%s media_port=%d attempt=%d reason=%s",
		deviceID, channelID, app, streamID, mediaIP, mediaPort, attempt, reason,
	)
	if source == nil {
		return nil
	}
	output := s.buildZLMOutputConfig(app, streamID, "")
	output["gb_device_id"] = deviceID
	output["gb_channel_id"] = channelID
	output["gb_media_ip"] = mediaIP
	output["gb_media_port"] = fmt.Sprintf("%d", mediaPort)
	output["zlm_app"] = app
	output["zlm_stream"] = streamID
	applyOutputConfigToSource(source, output)
	source.App = app
	source.StreamID = streamID
	_ = s.db.Model(&model.MediaSource{}).Where("id = ?", source.ID).Updates(map[string]any{
		"app":               source.App,
		"stream_id":         source.StreamID,
		"output_config":     source.OutputConfig,
		"play_webrtc_url":   source.PlayWebRTCURL,
		"play_ws_flv_url":   source.PlayWSFLVURL,
		"play_http_flv_url": source.PlayHTTPFLVURL,
		"play_hls_url":      source.PlayHLSURL,
		"play_rtsp_url":     source.PlayRTSPURL,
		"play_rtmp_url":     source.PlayRTMPURL,
		"updated_at":        now,
	}).Error
	return nil
}

func (s *Server) loadGBChannelSource(deviceID string, channel model.GBChannel) (*model.MediaSource, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	sourceID := strings.TrimSpace(channel.SourceIDChannel)
	if sourceID != "" {
		var source model.MediaSource
		if err := s.db.Where("id = ?", sourceID).First(&source).Error; err == nil {
			return &source, nil
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	channelID := strings.TrimSpace(channel.ChannelID)
	if channelID == "" {
		return nil, nil
	}
	streamURL := fmt.Sprintf("gb28181://%s/%s", strings.TrimSpace(deviceID), channelID)
	var source model.MediaSource
	if err := s.db.Where(
		"source_type = ? AND row_kind = ? AND protocol = ? AND stream_url = ?",
		model.SourceTypeGB28181,
		model.RowKindChannel,
		model.ProtocolGB28181,
		streamURL,
	).First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &source, nil
}

func (g *gb28181Store) GetDeviceForAuth(deviceID string) (gb28181.AuthDevice, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return gb28181.AuthDevice{}, nil
	}
	var item model.GBDevice
	err := g.db.Where("device_id = ?", deviceID).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var blocked model.GBDeviceBlock
		if berr := g.db.Where("device_id = ?", deviceID).First(&blocked).Error; berr == nil {
			return gb28181.AuthDevice{
				DeviceID: deviceID,
				Enabled:  false,
			}, nil
		} else if berr != nil && !errors.Is(berr, gorm.ErrRecordNotFound) {
			return gb28181.AuthDevice{}, berr
		}
		// No-whitelist mode: unknown devices can register with global password fallback.
		return gb28181.AuthDevice{}, nil
	}
	if err != nil {
		return gb28181.AuthDevice{}, err
	}
	return gb28181.AuthDevice{
		DeviceID: item.DeviceID,
		Password: strings.TrimSpace(item.Password),
		Enabled:  item.Enabled,
	}, nil
}

func (g *gb28181Store) MarkRegistered(event gb28181.RegisterEvent) error {
	deviceID := strings.TrimSpace(event.DeviceID)
	if deviceID == "" {
		return errors.New("empty device id")
	}
	now := event.RegisteredAt
	if now.IsZero() {
		now = time.Now()
	}
	transport := normalizeGBTransport(event.Transport)
	expires := event.Expires
	if expires <= 0 {
		expires = 3600
	}
	sourceAddr := strings.TrimSpace(event.SourceAddr)

	item := model.GBDevice{
		DeviceID:       deviceID,
		Name:           defaultGBDeviceName(deviceID),
		AreaID:         model.RootAreaID,
		Enabled:        true,
		Status:         "online",
		Transport:      transport,
		SourceAddr:     sourceAddr,
		Expires:        expires,
		LastRegisterAt: now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := g.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "device_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"status":           "online",
			"transport":        transport,
			"source_addr":      sourceAddr,
			"expires":          expires,
			"last_register_at": now,
			"updated_at":       now,
		}),
	}).Create(&item).Error; err != nil {
		return err
	}
	if err := g.syncGBDeviceAndChannels(deviceID); err != nil {
		return err
	}
	if g != nil && g.srv != nil {
		g.srv.scheduleGBDeviceAutoInvite(deviceID, "register")
	}
	return nil
}

func (g *gb28181Store) MarkOffline(deviceID string, offlineAt time.Time, _ string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	result := g.db.Model(&model.GBDevice{}).Where("device_id = ?", deviceID).Updates(map[string]any{
		"status":      "offline",
		"source_addr": "",
		"updated_at":  offlineAt,
	})
	if result.Error != nil {
		return result.Error
	}
	return g.syncGBDeviceAndChannels(deviceID)
}

func (g *gb28181Store) MarkKeepalive(event gb28181.KeepaliveEvent) error {
	deviceID := strings.TrimSpace(event.DeviceID)
	if deviceID == "" {
		return errors.New("empty device id")
	}
	now := event.KeepaliveAt
	if now.IsZero() {
		now = time.Now()
	}
	transport := normalizeGBTransport(event.Transport)
	sourceAddr := strings.TrimSpace(event.SourceAddr)
	item := model.GBDevice{
		DeviceID:        deviceID,
		Name:            defaultGBDeviceName(deviceID),
		AreaID:          model.RootAreaID,
		Enabled:         true,
		Status:          "online",
		Transport:       transport,
		SourceAddr:      sourceAddr,
		Expires:         3600,
		LastKeepaliveAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := g.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "device_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"status":            "online",
			"transport":         transport,
			"source_addr":       sourceAddr,
			"last_keepalive_at": now,
			"updated_at":        now,
		}),
	}).Create(&item).Error; err != nil {
		return err
	}
	return g.syncGBDeviceAndChannels(deviceID)
}

func (g *gb28181Store) UpsertCatalog(event gb28181.CatalogEvent) error {
	deviceID := strings.TrimSpace(event.DeviceID)
	if deviceID == "" {
		return errors.New("empty device id")
	}
	now := event.OccurredAt
	if now.IsZero() {
		now = time.Now()
	}
	if err := g.db.Transaction(func(tx *gorm.DB) error {
		device := model.GBDevice{
			DeviceID:        deviceID,
			Name:            defaultGBDeviceName(deviceID),
			AreaID:          model.RootAreaID,
			Enabled:         true,
			Status:          "online",
			Transport:       "udp",
			Expires:         3600,
			LastKeepaliveAt: now,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "device_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"status":            "online",
				"last_keepalive_at": now,
				"updated_at":        now,
			}),
		}).Create(&device).Error; err != nil {
			return err
		}

		var gbDevice model.GBDevice
		if err := tx.Where("device_id = ?", deviceID).First(&gbDevice).Error; err != nil {
			return err
		}
		if err := g.syncGBDeviceSourceTx(tx, &gbDevice); err != nil {
			return err
		}
		if len(event.Channels) == 0 {
			return nil
		}

		for _, item := range event.Channels {
			channelID := strings.TrimSpace(item.ChannelID)
			if channelID == "" {
				continue
			}
			ch := model.GBChannel{
				DeviceID:     deviceID,
				ChannelID:    channelID,
				Name:         strings.TrimSpace(item.Name),
				Manufacturer: strings.TrimSpace(item.Manufacturer),
				Model:        strings.TrimSpace(item.Model),
				Owner:        strings.TrimSpace(item.Owner),
				Status:       strings.TrimSpace(item.Status),
				RawXML:       event.RawXML,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "device_id"}, {Name: "channel_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"name", "manufacturer", "model", "owner", "status", "raw_xml", "updated_at"}),
			}).Create(&ch).Error; err != nil {
				return err
			}
			if err := tx.Where("device_id = ? AND channel_id = ?", deviceID, channelID).First(&ch).Error; err != nil {
				return err
			}
			if err := g.syncGBChannelSourceTx(tx, &gbDevice, &ch); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := g.syncGBDeviceAndChannels(deviceID); err != nil {
		return err
	}
	if g != nil && g.srv != nil {
		g.srv.scheduleGBDeviceAutoInvite(deviceID, "catalog")
	}
	return nil
}

func (g *gb28181Store) ListDeviceStates() ([]gb28181.DeviceState, error) {
	var items []model.GBDevice
	if err := g.db.Where("enabled = ?", true).Find(&items).Error; err != nil {
		return nil, err
	}
	out := make([]gb28181.DeviceState, 0, len(items))
	for _, item := range items {
		out = append(out, gb28181.DeviceState{
			DeviceID:        item.DeviceID,
			Status:          item.Status,
			Expires:         item.Expires,
			LastRegisterAt:  item.LastRegisterAt,
			LastKeepaliveAt: item.LastKeepaliveAt,
		})
	}
	return out, nil
}

func normalizeGBTransport(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "tcp" {
		return "tcp"
	}
	return "udp"
}

func normalizeGBStatus(enabled bool, status string) string {
	if !enabled {
		return "offline"
	}
	if strings.EqualFold(strings.TrimSpace(status), "online") {
		return "online"
	}
	return "offline"
}

func defaultGBDeviceName(deviceID string) string {
	deviceID = strings.TrimSpace(deviceID)
	if len(deviceID) > 6 {
		return fmt.Sprintf("GB\u8bbe\u5907-%s", deviceID[len(deviceID)-6:])
	}
	if deviceID == "" {
		return "GB\u8bbe\u5907"
	}
	return "GB\u8bbe\u5907-" + deviceID
}

func normalizeGBDeviceName(name, deviceID string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return defaultGBDeviceName(deviceID)
	}
	return name
}

func buildGBChannelSourceStreamID(deviceID, channelID string) string {
	devicePart := sanitizeGBStreamPart(deviceID, "device")
	channelPart := sanitizeGBStreamPart(channelID, devicePart)
	return devicePart + "_" + channelPart
}

func sanitizeGBStreamPart(raw, fallback string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		cleaned = strings.TrimSpace(fallback)
	}
	if cleaned == "" {
		cleaned = "unknown"
	}
	cleaned = zlmStreamIDSanitizer.ReplaceAllString(cleaned, "_")
	cleaned = strings.Trim(cleaned, "_")
	if cleaned == "" {
		cleaned = "unknown"
	}
	return cleaned
}

func parseGBDeviceIDFromShadowID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "gb28181_") {
		return strings.TrimSpace(strings.TrimPrefix(raw, "gb28181_"))
	}
	return raw
}

func parseGBStreamURL(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "gb28181://")
	if raw == "" {
		return "", ""
	}
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) == 0 {
		return "", ""
	}
	deviceID := strings.TrimSpace(parts[0])
	channelID := ""
	if len(parts) > 1 {
		channelID = strings.TrimSpace(parts[1])
	}
	return deviceID, channelID
}

func (g *gb28181Store) syncGBDeviceAndChannels(deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	return g.db.Transaction(func(tx *gorm.DB) error {
		var gbDevice model.GBDevice
		if err := tx.Where("device_id = ?", deviceID).First(&gbDevice).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := g.syncGBDeviceSourceTx(tx, &gbDevice); err != nil {
			return err
		}
		var channels []model.GBChannel
		if err := tx.Where("device_id = ?", deviceID).Find(&channels).Error; err != nil {
			return err
		}
		for i := range channels {
			if err := g.syncGBChannelSourceTx(tx, &gbDevice, &channels[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (g *gb28181Store) syncGBDeviceSourceTx(tx *gorm.DB, gbDevice *model.GBDevice) error {
	if tx == nil || gbDevice == nil {
		return nil
	}
	now := time.Now()
	areaID := strings.TrimSpace(gbDevice.AreaID)
	if areaID == "" {
		areaID = model.RootAreaID
	}
	rawName := strings.TrimSpace(gbDevice.Name)
	name := normalizeGBDeviceName(rawName, gbDevice.DeviceID)
	if name != rawName {
		if err := tx.Model(&model.GBDevice{}).Where("device_id = ?", gbDevice.DeviceID).
			Update("name", name).Error; err != nil {
			return err
		}
		gbDevice.Name = name
	}
	transport := normalizeGBTransport(gbDevice.Transport)
	streamID := strings.TrimSpace(gbDevice.DeviceID)
	if streamID == "" {
		return errors.New("empty gb device id")
	}

	source := model.MediaSource{}
	sourceID := strings.TrimSpace(gbDevice.SourceIDDevice)
	if sourceID != "" {
		if err := tx.Where("id = ?", sourceID).First(&source).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if strings.TrimSpace(source.ID) == "" {
		if err := tx.Where(
			"source_type = ? AND row_kind = ? AND protocol = ? AND stream_id = ?",
			model.SourceTypeGB28181, model.RowKindDevice, model.ProtocolGB28181, streamID,
		).First(&source).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if strings.TrimSpace(source.ID) == "" {
		source.ID = uuid.NewString()
		source.CreatedAt = now
		applyDefaultRecordingPolicyToSource(g.cfg, &source)
		applyDefaultAlarmClipPolicyToSource(g.cfg, &source)
		source.Status = "offline"
		source.AIStatus = model.DeviceAIStatusIdle
		source.MediaServerID = "local"
	}
	source.Name = name
	source.AreaID = areaID
	source.SourceType = model.SourceTypeGB28181
	source.RowKind = model.RowKindDevice
	source.ParentID = ""
	source.Protocol = model.ProtocolGB28181
	source.Transport = transport
	source.App = "gb28181"
	source.StreamID = streamID
	source.StreamURL = "gb28181://" + gbDevice.DeviceID
	if strings.TrimSpace(source.Status) == "" {
		source.Status = "offline"
	}
	source.UpdatedAt = now
	output := g.buildZLMOutputConfig(source.App, source.StreamID)
	output["gb_device_id"] = gbDevice.DeviceID
	output["zlm_app"] = source.App
	output["zlm_stream"] = source.StreamID
	applyOutputConfigToSource(&source, output)

	if err := tx.Save(&source).Error; err != nil {
		return err
	}
	if gbDevice.SourceIDDevice != source.ID {
		if err := tx.Model(&model.GBDevice{}).Where("device_id = ?", gbDevice.DeviceID).
			Update("source_id_device", source.ID).Error; err != nil {
			return err
		}
		gbDevice.SourceIDDevice = source.ID
	}
	return nil
}

func (g *gb28181Store) syncGBChannelSourceTx(tx *gorm.DB, gbDevice *model.GBDevice, channel *model.GBChannel) error {
	if tx == nil || gbDevice == nil || channel == nil {
		return nil
	}
	now := time.Now()
	areaID := strings.TrimSpace(gbDevice.AreaID)
	if areaID == "" {
		areaID = model.RootAreaID
	}
	transport := normalizeGBTransport(gbDevice.Transport)
	source := model.MediaSource{}
	sourceID := strings.TrimSpace(channel.SourceIDChannel)
	if sourceID != "" {
		if err := tx.Where("id = ?", sourceID).First(&source).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	streamID := buildGBChannelSourceStreamID(gbDevice.DeviceID, channel.ChannelID)
	if strings.TrimSpace(source.ID) == "" {
		if err := tx.Where(
			"source_type = ? AND row_kind = ? AND protocol = ? AND stream_id = ?",
			model.SourceTypeGB28181, model.RowKindChannel, model.ProtocolGB28181, streamID,
		).First(&source).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if strings.TrimSpace(source.ID) == "" {
		source.ID = uuid.NewString()
		source.CreatedAt = now
		applyDefaultRecordingPolicyToSource(g.cfg, &source)
		applyDefaultAlarmClipPolicyToSource(g.cfg, &source)
		source.Status = "offline"
		source.AIStatus = model.DeviceAIStatusIdle
		source.MediaServerID = "local"
	}

	name := strings.TrimSpace(channel.Name)
	if name == "" {
		name = fmt.Sprintf("%s-\u901a\u9053-%s", defaultGBDeviceName(gbDevice.DeviceID), strings.TrimSpace(channel.ChannelID))
	}
	source.Name = name
	source.AreaID = areaID
	source.SourceType = model.SourceTypeGB28181
	source.RowKind = model.RowKindChannel
	source.ParentID = strings.TrimSpace(gbDevice.SourceIDDevice)
	source.Protocol = model.ProtocolGB28181
	source.Transport = transport
	source.App = "rtp"
	source.StreamID = streamID
	source.StreamURL = fmt.Sprintf("gb28181://%s/%s", gbDevice.DeviceID, channel.ChannelID)
	if strings.TrimSpace(source.Status) == "" {
		source.Status = "offline"
	}
	source.UpdatedAt = now
	output := g.buildZLMOutputConfig(source.App, source.StreamID)
	output["gb_device_id"] = gbDevice.DeviceID
	output["gb_channel_id"] = channel.ChannelID
	output["zlm_app"] = source.App
	output["zlm_stream"] = source.StreamID
	applyOutputConfigToSource(&source, output)
	if err := tx.Save(&source).Error; err != nil {
		return err
	}
	if channel.SourceIDChannel != source.ID {
		if err := tx.Model(&model.GBChannel{}).Where("id = ?", channel.ID).
			Update("source_id_channel", source.ID).Error; err != nil {
			return err
		}
		channel.SourceIDChannel = source.ID
	}
	return nil
}

func (g *gb28181Store) buildZLMOutputConfig(app, stream string) map[string]string {
	app = strings.TrimSpace(app)
	stream = strings.TrimSpace(stream)
	if app == "" || stream == "" {
		return map[string]string{}
	}
	host := "127.0.0.1"
	httpPort := 11029
	rtspPort := 1554
	rtmpPort := 11935
	if g != nil && g.cfg != nil {
		if v := normalizeHost(strings.TrimSpace(g.cfg.Server.ZLM.PlayHost)); v != "" {
			host = v
		}
		if g.cfg.Server.ZLM.HTTPPort > 0 {
			httpPort = g.cfg.Server.ZLM.HTTPPort
		}
		if g.cfg.Server.ZLM.RTSPPort > 0 {
			rtspPort = g.cfg.Server.ZLM.RTSPPort
		}
		if g.cfg.Server.ZLM.RTMPPort > 0 {
			rtmpPort = g.cfg.Server.ZLM.RTMPPort
		}
	}
	httpPrefix := fmt.Sprintf("http://%s:%d", host, httpPort)
	wsPrefix := fmt.Sprintf("ws://%s:%d", host, httpPort)
	output := map[string]string{
		"webrtc":     fmt.Sprintf("%s/index/api/webrtc?app=%s&stream=%s&type=play", httpPrefix, app, stream),
		"ws_flv":     fmt.Sprintf("%s/%s/%s.live.flv", wsPrefix, app, stream),
		"http_flv":   fmt.Sprintf("%s/%s/%s.live.flv", httpPrefix, app, stream),
		"hls":        fmt.Sprintf("%s/%s/%s/hls.m3u8", httpPrefix, app, stream),
		"rtsp":       fmt.Sprintf("rtsp://%s:%d/%s/%s", host, rtspPort, app, stream),
		"rtmp":       fmt.Sprintf("rtmp://%s:%d/%s/%s", host, rtmpPort, app, stream),
		"zlm_app":    app,
		"zlm_stream": stream,
	}
	if g != nil && g.cfg != nil {
		return applyZLMOutputPolicy(output, g.cfg.Server.ZLM.Output)
	}
	return output
}
