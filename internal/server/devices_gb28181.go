package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"maas-box/internal/model"
)

type gbDeviceCreateRequest struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	AreaID   string `json:"area_id"`
	Password string `json:"password"`
	Enabled  *bool  `json:"enabled"`
}

type gbDeviceUpdateRequest struct {
	Name     *string `json:"name"`
	AreaID   *string `json:"area_id"`
	Password *string `json:"password"`
	Enabled  *bool   `json:"enabled"`
}

type gbChannelUpdateRequest struct {
	Name   *string `json:"name"`
	AreaID *string `json:"area_id"`
}

func (s *Server) listGBDevices(c *gin.Context) {
	var items []model.GBDevice
	query := s.db.Model(&model.GBDevice{})
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("device_id LIKE ? OR name LIKE ?", like, like)
	}
	if err := query.Order("created_at desc").Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query gb28181 devices failed")
		return
	}
	s.ok(c, gin.H{"items": items, "total": len(items)})
}

func (s *Server) createGBDevice(c *gin.Context) {
	var in gbDeviceCreateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	in.DeviceID = strings.TrimSpace(in.DeviceID)
	in.Name = strings.TrimSpace(in.Name)
	in.AreaID = strings.TrimSpace(in.AreaID)
	if in.AreaID == "" {
		in.AreaID = model.RootAreaID
	}
	if !gbIDPattern.MatchString(in.DeviceID) {
		s.fail(c, http.StatusBadRequest, "device_id must be 20-digit numeric code")
		return
	}
	if in.Name == "" {
		s.fail(c, http.StatusBadRequest, "name is required")
		return
	}
	var areaCount int64
	if err := s.db.Model(&model.Area{}).Where("id = ?", in.AreaID).Count(&areaCount).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query area failed")
		return
	}
	if areaCount == 0 {
		s.fail(c, http.StatusBadRequest, "area does not exist")
		return
	}

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	item := model.GBDevice{
		DeviceID:   in.DeviceID,
		Name:       in.Name,
		AreaID:     in.AreaID,
		Password:   strings.TrimSpace(in.Password),
		Enabled:    enabled,
		Status:     "offline",
		Transport:  "udp",
		SourceAddr: "",
		Expires:    3600,
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := clearGBDeviceBlockTx(tx, item.DeviceID); err != nil {
			return err
		}
		return tx.Create(&item).Error
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "create gb28181 device failed")
		return
	}
	_ = (&gb28181Store{db: s.db, cfg: s.cfg}).syncGBDeviceAndChannels(item.DeviceID)
	s.ok(c, item)
}

func (s *Server) updateGBDevice(c *gin.Context) {
	deviceID := strings.TrimSpace(c.Param("device_id"))
	if !gbIDPattern.MatchString(deviceID) {
		s.fail(c, http.StatusBadRequest, "invalid device_id")
		return
	}
	var item model.GBDevice
	if err := s.db.Where("device_id = ?", deviceID).First(&item).Error; err != nil {
		s.fail(c, http.StatusNotFound, "gb28181 device not found")
		return
	}

	var in gbDeviceUpdateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if in.Name != nil {
		item.Name = strings.TrimSpace(*in.Name)
		if item.Name == "" {
			s.fail(c, http.StatusBadRequest, "name cannot be empty")
			return
		}
	}
	if in.AreaID != nil {
		areaID := strings.TrimSpace(*in.AreaID)
		if areaID == "" {
			areaID = model.RootAreaID
		}
		var areaCount int64
		if err := s.db.Model(&model.Area{}).Where("id = ?", areaID).Count(&areaCount).Error; err != nil {
			s.fail(c, http.StatusInternalServerError, "query area failed")
			return
		}
		if areaCount == 0 {
			s.fail(c, http.StatusBadRequest, "area does not exist")
			return
		}
		item.AreaID = areaID
	}
	if in.Password != nil {
		item.Password = strings.TrimSpace(*in.Password)
	}
	if in.Enabled != nil {
		item.Enabled = *in.Enabled
		if !item.Enabled {
			item.Status = "offline"
			item.SourceAddr = ""
			item.Transport = ""
		}
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if item.Enabled {
			if err := clearGBDeviceBlockTx(tx, item.DeviceID); err != nil {
				return err
			}
		}
		return tx.Save(&item).Error
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "update gb28181 device failed")
		return
	}
	_ = (&gb28181Store{db: s.db, cfg: s.cfg}).syncGBDeviceAndChannels(item.DeviceID)
	if !item.Enabled {
		s.stopGBDevicePlay(item.DeviceID)
		s.forgetGBDeviceSession(item.DeviceID)
		if sources, serr := s.listGBDeviceRelatedSources(item.DeviceID); serr == nil {
			s.cleanupMediaSourceStreams(sources)
		}
	}
	s.ok(c, item)
}

func (s *Server) deleteGBDevice(c *gin.Context) {
	deviceID := strings.TrimSpace(c.Param("device_id"))
	if !gbIDPattern.MatchString(deviceID) {
		s.fail(c, http.StatusBadRequest, "invalid device_id")
		return
	}
	cleanupSources := make([]model.MediaSource, 0)
	var exists int64
	if err := s.db.Model(&model.GBDevice{}).Where("device_id = ?", deviceID).Count(&exists).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query gb28181 device failed")
		return
	}
	if exists == 0 {
		s.fail(c, http.StatusNotFound, "gb28181 device not found")
		return
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var channels []model.GBChannel
		if err := tx.Where("device_id = ?", deviceID).Find(&channels).Error; err != nil {
			return err
		}
		var gbItem model.GBDevice
		if err := tx.Where("device_id = ?", deviceID).First(&gbItem).Error; err != nil {
			return err
		}

		sourceIDs := make([]string, 0, len(channels)+1)
		if sid := strings.TrimSpace(gbItem.SourceIDDevice); sid != "" {
			sourceIDs = append(sourceIDs, sid)
		}
		for _, ch := range channels {
			if sid := strings.TrimSpace(ch.SourceIDChannel); sid != "" {
				sourceIDs = append(sourceIDs, sid)
			}
		}
		if len(sourceIDs) > 0 {
			var usedCount int64
			if err := tx.Model(&model.VideoTaskDeviceProfile{}).Where("device_id IN ?", sourceIDs).Count(&usedCount).Error; err != nil {
				return err
			}
			if usedCount > 0 {
				return fmt.Errorf("gb28181 source is used by task")
			}
		}
		if len(sourceIDs) > 0 {
			if err := tx.Where("id IN ?", sourceIDs).Find(&cleanupSources).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", sourceIDs).Delete(&model.MediaSource{}).Error; err != nil {
				return err
			}
		}
		if err := upsertGBDeviceBlockTx(tx, deviceID, "deleted by user"); err != nil {
			return err
		}
		if err := tx.Where("device_id = ?", deviceID).Delete(&model.GBChannel{}).Error; err != nil {
			return err
		}
		return tx.Where("device_id = ?", deviceID).Delete(&model.GBDevice{}).Error
	}); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "used by task") {
			s.fail(c, http.StatusBadRequest, "gb28181 source is used by task, remove task relation first")
			return
		}
		s.fail(c, http.StatusInternalServerError, "delete gb28181 device failed")
		return
	}
	s.stopGBDevicePlay(deviceID)
	s.forgetGBDeviceSession(deviceID)
	s.cleanupMediaSourceStreams(cleanupSources)
	s.ok(c, gin.H{"deleted": deviceID})
}

func (s *Server) listGBDeviceRelatedSources(deviceID string) ([]model.MediaSource, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return []model.MediaSource{}, nil
	}
	var gbItem model.GBDevice
	if err := s.db.Where("device_id = ?", deviceID).First(&gbItem).Error; err != nil {
		return nil, err
	}
	var channels []model.GBChannel
	if err := s.db.Where("device_id = ?", deviceID).Find(&channels).Error; err != nil {
		return nil, err
	}
	sourceIDSet := map[string]struct{}{}
	addSourceID := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		sourceIDSet[raw] = struct{}{}
	}
	addSourceID(gbItem.SourceIDDevice)
	for _, item := range channels {
		addSourceID(item.SourceIDChannel)
	}
	sourceIDs := make([]string, 0, len(sourceIDSet))
	for id := range sourceIDSet {
		sourceIDs = append(sourceIDs, id)
	}
	if len(sourceIDs) == 0 {
		return []model.MediaSource{}, nil
	}
	sources := make([]model.MediaSource, 0, len(sourceIDs))
	if err := s.db.Where("id IN ?", sourceIDs).Find(&sources).Error; err != nil {
		return nil, err
	}
	return sources, nil
}

func (s *Server) cleanupMediaSourceStreams(sources []model.MediaSource) {
	if s == nil || len(sources) == 0 {
		return
	}
	defaultApp := "live"
	if s.cfg != nil {
		if app := strings.TrimSpace(s.cfg.Server.ZLM.App); app != "" {
			defaultApp = app
		}
	}
	seen := map[string]struct{}{}
	for _, src := range sources {
		// 先停止录制，避免删除后仍有录制任务残留。
		s.cancelAlarmRecordingStop(src.ID)
		_ = s.stopRecordingForSource(src)
		_ = s.setRecordingStatus(src.ID, recordingStatusStopped)

		app := strings.TrimSpace(src.App)
		stream := strings.TrimSpace(src.StreamID)
		if app == "" || stream == "" {
			parsedApp, parsedStream := parseDeviceZLMAppStream(strings.ToLower(strings.TrimSpace(src.Protocol)), src, defaultApp)
			if app == "" {
				app = strings.TrimSpace(parsedApp)
			}
			if stream == "" {
				stream = strings.TrimSpace(parsedStream)
			}
		}
		if app == "" {
			app = defaultApp
		}
		if stream == "" {
			continue
		}
		key := app + "/" + stream
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		_ = s.closeZLMStreams(app, stream)
		_ = s.closeZLMRTPServer(stream)
	}
}

func (s *Server) queryGBDeviceCatalog(c *gin.Context) {
	deviceID := strings.TrimSpace(c.Param("device_id"))
	if !gbIDPattern.MatchString(deviceID) {
		s.fail(c, http.StatusBadRequest, "invalid device_id")
		return
	}
	if s.gbService == nil {
		s.fail(c, http.StatusServiceUnavailable, "gb28181 service is disabled")
		return
	}
	if err := s.gbService.QueryCatalog(deviceID); err != nil {
		s.fail(c, http.StatusBadGateway, "query catalog failed: "+err.Error())
		return
	}
	s.ok(c, gin.H{
		"device_id":    deviceID,
		"requested_at": time.Now(),
		"triggered":    true,
	})
}

func (s *Server) listGBDeviceChannels(c *gin.Context) {
	deviceID := strings.TrimSpace(c.Param("device_id"))
	if !gbIDPattern.MatchString(deviceID) {
		s.fail(c, http.StatusBadRequest, "invalid device_id")
		return
	}
	var items []model.GBChannel
	if err := s.db.Where("device_id = ?", deviceID).Order("channel_id asc").Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query gb28181 channels failed")
		return
	}
	s.ok(c, gin.H{"items": items, "total": len(items)})
}

func (s *Server) updateGBChannel(c *gin.Context) {
	channelID := strings.TrimSpace(c.Param("channel_id"))
	if channelID == "" {
		s.fail(c, http.StatusBadRequest, "channel_id is required")
		return
	}
	var channel model.GBChannel
	if err := s.db.Where("channel_id = ?", channelID).Order("updated_at desc").First(&channel).Error; err != nil {
		s.fail(c, http.StatusNotFound, "gb28181 channel not found")
		return
	}
	var in gbChannelUpdateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}

	newName := strings.TrimSpace(channel.Name)
	if in.Name != nil {
		newName = strings.TrimSpace(*in.Name)
		if newName == "" {
			s.fail(c, http.StatusBadRequest, "name cannot be empty")
			return
		}
	}

	newAreaID := ""
	if in.AreaID != nil {
		newAreaID = strings.TrimSpace(*in.AreaID)
		if newAreaID == "" {
			newAreaID = model.RootAreaID
		}
		var areaCount int64
		if err := s.db.Model(&model.Area{}).Where("id = ?", newAreaID).Count(&areaCount).Error; err != nil {
			s.fail(c, http.StatusInternalServerError, "query area failed")
			return
		}
		if areaCount == 0 {
			s.fail(c, http.StatusBadRequest, "area does not exist")
			return
		}
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if newName != strings.TrimSpace(channel.Name) {
			if err := tx.Model(&model.GBChannel{}).Where("id = ?", channel.ID).Updates(map[string]any{
				"name":       newName,
				"updated_at": time.Now(),
			}).Error; err != nil {
				return err
			}
		}
		if sid := strings.TrimSpace(channel.SourceIDChannel); sid != "" {
			updates := map[string]any{}
			if newName != strings.TrimSpace(channel.Name) {
				updates["name"] = newName
			}
			if newAreaID != "" {
				updates["area_id"] = newAreaID
			}
			if len(updates) > 0 {
				updates["updated_at"] = time.Now()
				if err := tx.Model(&model.MediaSource{}).Where("id = ?", sid).Updates(updates).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "update gb28181 channel failed")
		return
	}
	var updated model.GBChannel
	if err := s.db.Where("id = ?", channel.ID).First(&updated).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "load updated channel failed")
		return
	}
	s.ok(c, updated)
}

func (s *Server) gb28181Stats(c *gin.Context) {
	var total int64
	var enabled int64
	var online int64
	var channels int64
	if err := s.db.Model(&model.GBDevice{}).Count(&total).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query gb28181 stats failed")
		return
	}
	if err := s.db.Model(&model.GBDevice{}).Where("enabled = ?", true).Count(&enabled).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query gb28181 stats failed")
		return
	}
	if err := s.db.Model(&model.GBDevice{}).Where("status = ?", "online").Count(&online).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query gb28181 stats failed")
		return
	}
	if err := s.db.Model(&model.GBChannel{}).Count(&channels).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query gb28181 stats failed")
		return
	}
	s.ok(c, gin.H{
		"devices_total":   total,
		"enabled_total":   enabled,
		"online_total":    online,
		"offline_total":   total - online,
		"channels_total":  channels,
		"sip_listen_ip":   s.cfg.Server.SIP.ListenIP,
		"sip_listen_port": s.cfg.Server.SIP.Port,
		"sip_enabled":     s.cfg.Server.SIP.Enabled,
	})
}
