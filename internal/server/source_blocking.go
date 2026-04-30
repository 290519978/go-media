package server

import (
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"maas-box/internal/model"
)

func normalizeStreamBlockKey(app, stream, fallbackApp string) (string, string) {
	app = strings.TrimSpace(app)
	if app == "" {
		app = strings.TrimSpace(fallbackApp)
	}
	if app == "" {
		app = "live"
	}
	stream = strings.TrimSpace(stream)
	return app, stream
}

func upsertGBDeviceBlockTx(tx *gorm.DB, deviceID, reason string) error {
	if tx == nil {
		return nil
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	now := time.Now()
	item := model.GBDeviceBlock{
		DeviceID:  deviceID,
		Reason:    strings.TrimSpace(reason),
		CreatedAt: now,
		UpdatedAt: now,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "device_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"reason":     item.Reason,
			"updated_at": now,
		}),
	}).Create(&item).Error
}

func clearGBDeviceBlockTx(tx *gorm.DB, deviceID string) error {
	if tx == nil {
		return nil
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	return tx.Where("device_id = ?", deviceID).Delete(&model.GBDeviceBlock{}).Error
}

func (s *Server) isGBDeviceBlocked(deviceID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false, nil
	}
	var count int64
	if err := s.db.Model(&model.GBDeviceBlock{}).Where("device_id = ?", deviceID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func upsertStreamBlockTx(tx *gorm.DB, app, stream, reason, fallbackApp string) error {
	if tx == nil {
		return nil
	}
	app, stream = normalizeStreamBlockKey(app, stream, fallbackApp)
	if stream == "" {
		return nil
	}
	now := time.Now()
	item := model.StreamBlock{
		App:       app,
		StreamID:  stream,
		Reason:    strings.TrimSpace(reason),
		CreatedAt: now,
		UpdatedAt: now,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "app"}, {Name: "stream_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"reason":     item.Reason,
			"updated_at": now,
		}),
	}).Create(&item).Error
}

func clearStreamBlockTx(tx *gorm.DB, app, stream, fallbackApp string) error {
	if tx == nil {
		return nil
	}
	app, stream = normalizeStreamBlockKey(app, stream, fallbackApp)
	if stream == "" {
		return nil
	}
	return tx.Where("app = ? AND stream_id = ?", app, stream).Delete(&model.StreamBlock{}).Error
}

func (s *Server) isRTMPStreamBlocked(app, stream string) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	defaultApp := ""
	if s.cfg != nil {
		defaultApp = strings.TrimSpace(s.cfg.Server.ZLM.App)
	}
	app, stream = normalizeStreamBlockKey(app, stream, defaultApp)
	if stream == "" {
		return false, nil
	}
	var count int64
	if err := s.db.Model(&model.StreamBlock{}).Where("app = ? AND stream_id = ?", app, stream).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
