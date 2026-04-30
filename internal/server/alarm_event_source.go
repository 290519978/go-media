package server

import (
	"strings"

	"gorm.io/gorm"
	"maas-box/internal/model"
)

func normalizeAlarmEventSource(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case model.AlarmEventSourcePatrol:
		return model.AlarmEventSourcePatrol
	default:
		return model.AlarmEventSourceRuntime
	}
}

func normalizedAlarmEventSourceExpr(column string) string {
	column = strings.TrimSpace(column)
	if column == "" {
		column = "mb_alarm_events.event_source"
	}
	// 老数据可能还没有 event_source，运行态查询默认把空值也按 runtime 处理。
	return "COALESCE(NULLIF(" + column + ", ''), '" + model.AlarmEventSourceRuntime + "')"
}

func applyAlarmEventSourceFilter(db *gorm.DB, column string, source string) *gorm.DB {
	if db == nil {
		return db
	}
	return db.Where(normalizedAlarmEventSourceExpr(column)+" = ?", normalizeAlarmEventSource(source))
}
