package server

import (
	"strings"
	"testing"
	"time"

	"maas-box/internal/model"
)

func TestSanitizeEventSourceCallbackRemovesSnapshot(t *testing.T) {
	raw := []byte(`{"camera_id":"dev-1","snapshot":"abc123","timestamp":123}`)
	got := sanitizeEventSourceCallback(raw)
	if strings.Contains(got, "\"snapshot\"") {
		t.Fatalf("snapshot field should be removed: %s", got)
	}
	if !strings.Contains(got, "\"camera_id\"") {
		t.Fatalf("expected camera_id kept: %s", got)
	}
}

func TestStripSnapshotFromAlarmEventCallbacks(t *testing.T) {
	s := newFocusedTestServer(t)

	eventWithSnapshot := model.AlarmEvent{
		ID:             "event-optimize-1",
		TaskID:         "task-optimize-1",
		DeviceID:       "dev-optimize-1",
		AlgorithmID:    "alg-optimize-1",
		AlarmLevelID:   "level-optimize-1",
		Status:         model.EventStatusPending,
		OccurredAt:     time.Now(),
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        "{}",
		SourceCallback: `{"camera_id":"dev-optimize-1","snapshot":"bigbase64"}`,
	}
	eventPlain := model.AlarmEvent{
		ID:             "event-optimize-2",
		TaskID:         "task-optimize-1",
		DeviceID:       "dev-optimize-1",
		AlgorithmID:    "alg-optimize-1",
		AlarmLevelID:   "level-optimize-1",
		Status:         model.EventStatusPending,
		OccurredAt:     time.Now(),
		BoxesJSON:      "[]",
		YoloJSON:       "[]",
		LLMJSON:        "{}",
		SourceCallback: `{"camera_id":"dev-optimize-1"}`,
	}
	if err := s.db.Create(&eventWithSnapshot).Error; err != nil {
		t.Fatalf("create eventWithSnapshot failed: %v", err)
	}
	if err := s.db.Create(&eventPlain).Error; err != nil {
		t.Fatalf("create eventPlain failed: %v", err)
	}

	checked, updated, err := s.stripSnapshotFromAlarmEventCallbacks()
	if err != nil {
		t.Fatalf("stripSnapshotFromAlarmEventCallbacks failed: %v", err)
	}
	if checked < 1 {
		t.Fatalf("expected checked >= 1, got %d", checked)
	}
	if updated != 1 {
		t.Fatalf("expected updated=1, got %d", updated)
	}

	var got model.AlarmEvent
	if err := s.db.Where("id = ?", eventWithSnapshot.ID).First(&got).Error; err != nil {
		t.Fatalf("query event failed: %v", err)
	}
	if strings.Contains(got.SourceCallback, "\"snapshot\"") {
		t.Fatalf("snapshot should be removed, got=%s", got.SourceCallback)
	}
}

func TestEnsureAlarmEventCompositeIndexes(t *testing.T) {
	s := newFocusedTestServer(t)
	if err := s.ensureAlarmEventCompositeIndexes(); err != nil {
		t.Fatalf("ensureAlarmEventCompositeIndexes failed: %v", err)
	}

	indexes := []string{
		"idx_mb_alarm_events_status_occurred_at",
		"idx_mb_alarm_events_task_occurred_at",
		"idx_mb_alarm_events_device_occurred_at",
		"idx_mb_alarm_events_algorithm_occurred_at",
		"idx_mb_alarm_events_level_occurred_at",
		"idx_mb_alarm_events_occurred_at",
	}
	for _, indexName := range indexes {
		var count int64
		if err := s.db.Raw(
			"SELECT COUNT(1) FROM sqlite_master WHERE type = 'index' AND name = ?",
			indexName,
		).Scan(&count).Error; err != nil {
			t.Fatalf("query sqlite_master failed: %v", err)
		}
		if count != 1 {
			t.Fatalf("index %s should exist, got count=%d", indexName, count)
		}
	}
}
