package server

import (
	"log"
	"strings"

	"maas-box/internal/model"
)

const alarmEventStorageOptimizeKey = "alarm_event_storage_optimized_v1"

func (s *Server) optimizeAlarmEventStorage() {
	if s == nil || s.db == nil {
		return
	}
	if err := s.ensureAlarmEventCompositeIndexes(); err != nil {
		log.Printf("ensure alarm event composite indexes failed: err=%v", err)
	}

	if strings.EqualFold(strings.TrimSpace(s.getSetting(alarmEventStorageOptimizeKey)), "done") {
		return
	}

	checked, updated, err := s.stripSnapshotFromAlarmEventCallbacks()
	if err != nil {
		log.Printf("strip snapshot from alarm event callbacks failed: checked=%d updated=%d err=%v", checked, updated, err)
		return
	}

	vacuumed := false
	if updated > 0 && strings.EqualFold(strings.TrimSpace(s.db.Dialector.Name()), "sqlite") {
		if err := s.db.Exec("VACUUM").Error; err != nil {
			log.Printf("vacuum after alarm event optimization failed: err=%v", err)
		} else {
			vacuumed = true
		}
	}

	if err := s.upsertSetting(alarmEventStorageOptimizeKey, "done"); err != nil {
		log.Printf("persist alarm event optimization marker failed: err=%v", err)
	}

	log.Printf(
		"alarm event storage optimization finished: checked=%d updated=%d vacuumed=%t",
		checked,
		updated,
		vacuumed,
	)
}

func (s *Server) ensureAlarmEventCompositeIndexes() error {
	statements := []string{
		"CREATE INDEX IF NOT EXISTS idx_mb_alarm_events_status_occurred_at ON mb_alarm_events(status, occurred_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_mb_alarm_events_task_occurred_at ON mb_alarm_events(task_id, occurred_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_mb_alarm_events_device_occurred_at ON mb_alarm_events(device_id, occurred_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_mb_alarm_events_algorithm_occurred_at ON mb_alarm_events(algorithm_id, occurred_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_mb_alarm_events_level_occurred_at ON mb_alarm_events(alarm_level_id, occurred_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_mb_alarm_events_occurred_at ON mb_alarm_events(occurred_at DESC)",
	}
	for _, stmt := range statements {
		if err := s.db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) stripSnapshotFromAlarmEventCallbacks() (int, int, error) {
	type eventCallbackRow struct {
		ID             string `gorm:"column:id"`
		SourceCallback string `gorm:"column:source_callback"`
	}

	rows := make([]eventCallbackRow, 0)
	if err := s.db.Model(&model.AlarmEvent{}).
		Select("id, source_callback").
		Where("source_callback LIKE ?", "%\"snapshot\"%").
		Find(&rows).Error; err != nil {
		return 0, 0, err
	}

	checked := len(rows)
	updated := 0
	for _, row := range rows {
		original := strings.TrimSpace(row.SourceCallback)
		if original == "" {
			continue
		}
		sanitized := sanitizeEventSourceCallback([]byte(original))
		if sanitized == original {
			continue
		}
		if err := s.db.Model(&model.AlarmEvent{}).
			Where("id = ?", row.ID).
			Update("source_callback", sanitized).Error; err != nil {
			return checked, updated, err
		}
		updated++
	}

	return checked, updated, nil
}
