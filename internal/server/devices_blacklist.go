package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"maas-box/internal/model"
)

type gbDeviceBlockUpsertRequest struct {
	DeviceID string `json:"device_id"`
	Reason   string `json:"reason"`
}

type rtmpStreamBlockUpsertRequest struct {
	App      string `json:"app"`
	StreamID string `json:"stream_id"`
	Reason   string `json:"reason"`
}

func (s *Server) listSourceBlocks(c *gin.Context) {
	gbItems := make([]model.GBDeviceBlock, 0)
	gbQuery := s.db.Model(&model.GBDeviceBlock{})
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		like := "%" + keyword + "%"
		gbQuery = gbQuery.Where("device_id LIKE ? OR reason LIKE ?", like, like)
	}
	if err := gbQuery.Order("updated_at desc").Find(&gbItems).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query gb blacklist failed")
		return
	}

	rtmpItems := make([]model.StreamBlock, 0)
	rtmpQuery := s.db.Model(&model.StreamBlock{})
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		like := "%" + keyword + "%"
		rtmpQuery = rtmpQuery.Where("app LIKE ? OR stream_id LIKE ? OR reason LIKE ?", like, like, like)
	}
	if err := rtmpQuery.Order("updated_at desc").Find(&rtmpItems).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query rtmp blacklist failed")
		return
	}

	s.ok(c, gin.H{
		"gb_devices":   gbItems,
		"rtmp_streams": rtmpItems,
		"total":        len(gbItems) + len(rtmpItems),
	})
}

func (s *Server) addGBDeviceBlock(c *gin.Context) {
	var in gbDeviceBlockUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	deviceID := strings.TrimSpace(in.DeviceID)
	if !gbIDPattern.MatchString(deviceID) {
		s.fail(c, http.StatusBadRequest, "device_id must be 20-digit numeric code")
		return
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "manual blocked"
	}

	relatedSources := make([]model.MediaSource, 0)
	if sources, err := s.listGBDeviceRelatedSources(deviceID); err == nil {
		relatedSources = sources
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := upsertGBDeviceBlockTx(tx, deviceID, reason); err != nil {
			return err
		}
		return tx.Model(&model.GBDevice{}).Where("device_id = ?", deviceID).Updates(map[string]any{
			"enabled":     false,
			"status":      "offline",
			"source_addr": "",
			"updated_at":  time.Now(),
		}).Error
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "add gb blacklist failed")
		return
	}
	s.stopGBDevicePlay(deviceID)
	s.forgetGBDeviceSession(deviceID)
	s.cleanupMediaSourceStreams(relatedSources)
	_ = (&gb28181Store{db: s.db, cfg: s.cfg}).syncGBDeviceAndChannels(deviceID)
	s.ok(c, gin.H{
		"device_id": deviceID,
		"blocked":   true,
	})
}

func (s *Server) removeGBDeviceBlock(c *gin.Context) {
	deviceID := strings.TrimSpace(c.Param("device_id"))
	if !gbIDPattern.MatchString(deviceID) {
		s.fail(c, http.StatusBadRequest, "invalid device_id")
		return
	}
	reenabled := false
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := clearGBDeviceBlockTx(tx, deviceID); err != nil {
			return err
		}
		result := tx.Model(&model.GBDevice{}).Where("device_id = ?", deviceID).Updates(map[string]any{
			"enabled":    true,
			"updated_at": time.Now(),
		})
		if result.Error != nil {
			return result.Error
		}
		reenabled = result.RowsAffected > 0
		return nil
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "remove gb blacklist failed")
		return
	}
	if reenabled {
		_ = (&gb28181Store{db: s.db, cfg: s.cfg}).syncGBDeviceAndChannels(deviceID)
	}
	s.ok(c, gin.H{
		"device_id": deviceID,
		"blocked":   false,
		"reenabled": reenabled,
	})
}

func (s *Server) addRTMPStreamBlock(c *gin.Context) {
	var in rtmpStreamBlockUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	defaultApp := strings.TrimSpace(s.cfg.Server.ZLM.App)
	app, stream := normalizeStreamBlockKey(in.App, in.StreamID, defaultApp)
	if stream == "" {
		s.fail(c, http.StatusBadRequest, "stream_id is required")
		return
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "manual blocked"
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := upsertStreamBlockTx(tx, app, stream, reason, defaultApp); err != nil {
			return err
		}
		return tx.Model(&model.MediaSource{}).Where(
			"source_type = ? AND protocol = ? AND app = ? AND stream_id = ?",
			model.SourceTypePush, model.ProtocolRTMP, app, stream,
		).Updates(map[string]any{
			"status":     "offline",
			"updated_at": time.Now(),
		}).Error
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "add rtmp blacklist failed")
		return
	}
	_ = s.closeZLMStreams(app, stream)
	s.ok(c, gin.H{
		"app":       app,
		"stream_id": stream,
		"blocked":   true,
	})
}

func (s *Server) removeRTMPStreamBlock(c *gin.Context) {
	app := strings.TrimSpace(c.Param("app"))
	stream := strings.TrimSpace(c.Param("stream_id"))
	defaultApp := strings.TrimSpace(s.cfg.Server.ZLM.App)
	app, stream = normalizeStreamBlockKey(app, stream, defaultApp)
	if stream == "" {
		s.fail(c, http.StatusBadRequest, "stream_id is required")
		return
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		return clearStreamBlockTx(tx, app, stream, defaultApp)
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "remove rtmp blacklist failed")
		return
	}
	s.ok(c, gin.H{
		"app":       app,
		"stream_id": stream,
		"blocked":   false,
	})
}
