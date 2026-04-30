package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"maas-box/internal/model"
)

func createAlarmClipSessionTestTask(t *testing.T, s *Server, taskID string, status string) {
	t.Helper()
	task := model.VideoTask{
		ID:              taskID,
		Name:            "task-" + taskID,
		Status:          status,
		FrameInterval:   1,
		SmallConfidence: 0.5,
		LargeConfidence: 0.8,
		SmallIOU:        0.5,
		AlarmLevelID:    "level-test",
	}
	if err := s.db.Create(&task).Error; err != nil {
		t.Fatalf("create task failed: %v", err)
	}
}

func TestAlarmClipSessionStartExtendAndFinalize(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.BufferDir = t.TempDir()
	sourceID := "session-source-1"
	taskID := "session-task-1"
	eventA := "session-event-a"
	eventB := "session-event-b"

	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipSessionTestTask(t, s, taskID, model.TaskStatusRunning)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 3, 4)
	createAlarmClipTestEvent(t, s, eventA, taskID, sourceID, time.Now())
	createAlarmClipTestEvent(t, s, eventB, taskID, sourceID, time.Now().Add(500*time.Millisecond))

	first, err := s.startOrExtendAlarmClipSessionBySourceID(sourceID, time.Now(), 3, 4, []string{eventA})
	if err != nil {
		t.Fatalf("start session failed: %v", err)
	}
	if first == nil || strings.TrimSpace(first.ID) == "" {
		t.Fatalf("expected non-empty session")
	}
	firstExpected := first.ExpectedEndAt

	second, err := s.startOrExtendAlarmClipSessionBySourceID(sourceID, time.Now().Add(1*time.Second), 3, 4, []string{eventB})
	if err != nil {
		t.Fatalf("extend session failed: %v", err)
	}
	if second == nil || second.ID != first.ID {
		t.Fatalf("expected same session to be extended")
	}
	if second.ExpectedEndAt.Before(firstExpected) {
		t.Fatalf("expected extended expected_end_at")
	}

	bufferDir, err := s.safeAlarmBufferDeviceDir(sourceID)
	if err != nil {
		t.Fatalf("resolve buffer dir failed: %v", err)
	}
	writeAlarmClipTestFile(t, filepath.Join(bufferDir, "segment-a.mp4"), []byte("a"), time.Now())
	writeAlarmClipTestFile(t, filepath.Join(bufferDir, "segment-b.mp4"), []byte("b"), time.Now().Add(400*time.Millisecond))

	if err := s.finalizeAlarmClipSession(first.ID, "test"); err != nil {
		t.Fatalf("finalize session failed: %v", err)
	}

	ea := getAlarmClipTestEvent(t, s, eventA)
	eb := getAlarmClipTestEvent(t, s, eventB)
	if !ea.ClipReady || !eb.ClipReady {
		t.Fatalf("expected clip_ready=true for all events")
	}
	if strings.TrimSpace(ea.ClipSessionID) == "" || strings.TrimSpace(eb.ClipSessionID) == "" {
		t.Fatalf("expected clip_session_id to be set")
	}
	if ea.ClipSessionID != eb.ClipSessionID {
		t.Fatalf("expected events in same session")
	}
	if strings.TrimSpace(ea.ClipFilesJSON) == "" || strings.TrimSpace(ea.ClipFilesJSON) == "[]" {
		t.Fatalf("expected non-empty clip files")
	}
	if strings.TrimSpace(ea.ClipFilesJSON) != strings.TrimSpace(eb.ClipFilesJSON) {
		t.Fatalf("expected shared clip files")
	}
	anchorTime := resolveAlarmClipSessionAnchorTime(first.StartedAt, first.CreatedAt)
	expectedTS := formatAlarmClipTS(anchorTime)
	expectedClipPath := filepath.ToSlash(filepath.Join(alarmClipDirName, buildAlarmClipSessionName(first.ID, anchorTime)))
	if strings.TrimSpace(ea.ClipPath) != expectedClipPath {
		t.Fatalf("unexpected clip_path, got=%s want=%s", ea.ClipPath, expectedClipPath)
	}
	var clipFiles []string
	if err := json.Unmarshal([]byte(ea.ClipFilesJSON), &clipFiles); err != nil {
		t.Fatalf("decode clip files failed: %v", err)
	}
	if len(clipFiles) == 0 {
		t.Fatalf("expected non-empty clip files")
	}
	for _, item := range clipFiles {
		base := filepath.Base(item)
		if !strings.HasPrefix(base, expectedTS+"_") {
			t.Fatalf("clip file should include session timestamp prefix, file=%s expected_prefix=%s_", base, expectedTS)
		}
	}
	clipDir, err := s.safeRecordingFilePath(sourceID, ea.ClipPath)
	if err != nil {
		t.Fatalf("resolve clip dir failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clipDir, "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest.json should not exist, err=%v", err)
	}

	var session model.AlarmClipSession
	if err := s.db.Where("id = ?", first.ID).First(&session).Error; err != nil {
		t.Fatalf("query session failed: %v", err)
	}
	if session.Status != alarmClipSessionStatusClosed {
		t.Fatalf("expected session closed, got %s", session.Status)
	}
}

func TestAlarmClipSessionRespectsHardDeadline(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.BufferDir = t.TempDir()
	sourceID := "session-source-2"
	taskID := "session-task-2"
	eventA := "session-event-c"
	eventB := "session-event-d"

	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipSessionTestTask(t, s, taskID, model.TaskStatusRunning)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 3, 4)
	createAlarmClipTestEvent(t, s, eventA, taskID, sourceID, time.Now())
	createAlarmClipTestEvent(t, s, eventB, taskID, sourceID, time.Now().Add(2*time.Second))

	first, err := s.startOrExtendAlarmClipSessionBySourceID(sourceID, time.Now(), 3, 4, []string{eventA})
	if err != nil {
		t.Fatalf("start session failed: %v", err)
	}
	if err := s.db.Model(&model.AlarmClipSession{}).Where("id = ?", first.ID).
		Update("hard_deadline_at", time.Now().Add(-1*time.Second)).Error; err != nil {
		t.Fatalf("set hard deadline failed: %v", err)
	}
	second, err := s.startOrExtendAlarmClipSessionBySourceID(sourceID, time.Now().Add(1*time.Second), 3, 4, []string{eventB})
	if err != nil {
		t.Fatalf("start second session failed: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("expected new session after hard deadline")
	}
}

func TestRecoverAlarmClipSessionsOnStartupRebuildsTimer(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.BufferDir = t.TempDir()
	s.cfg.Server.Recording.AlarmClip.RecoverOnStartup = true
	sourceID := "session-source-3"
	taskID := "session-task-3"
	eventID := "session-event-e"

	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipSessionTestTask(t, s, taskID, model.TaskStatusRunning)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 2, 3)
	createAlarmClipTestEvent(t, s, eventID, taskID, sourceID, time.Now())

	session := model.AlarmClipSession{
		ID:             "recover-session-1",
		SourceID:       sourceID,
		Status:         alarmClipSessionStatusRecording,
		AnchorEventID:  eventID,
		PreSeconds:     2,
		PostSeconds:    3,
		StartedAt:      time.Now().Add(-2 * time.Second),
		LastAlarmAt:    time.Now().Add(-1 * time.Second),
		ExpectedEndAt:  time.Now().Add(15 * time.Second),
		HardDeadlineAt: time.Now().Add(120 * time.Second),
		ClipFilesJSON:  "[]",
	}
	if err := s.db.Create(&session).Error; err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	row := model.AlarmClipSessionEvent{
		SessionID:       session.ID,
		EventID:         eventID,
		EventOccurredAt: time.Now(),
	}
	if err := s.db.Create(&row).Error; err != nil {
		t.Fatalf("create session event failed: %v", err)
	}

	if err := s.recoverAlarmClipSessionsOnStartup(); err != nil {
		t.Fatalf("recover sessions failed: %v", err)
	}

	s.alarmClipSessionMu.Lock()
	seq := s.alarmClipSessionSeq[sourceID]
	timer := s.alarmClipSessionTimers[sourceID]
	s.alarmClipSessionMu.Unlock()
	if seq == 0 || timer == nil {
		t.Fatalf("expected recovery to rebuild session timer")
	}
	s.stopAllAlarmClipSessionTimers()
}

func TestFinalizeAlarmClipSessionWithoutFilesMarksClosedEmpty(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.BufferDir = t.TempDir()
	sourceID := "session-source-4"
	taskID := "session-task-4"
	eventID := "session-event-f"
	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipSessionTestTask(t, s, taskID, model.TaskStatusRunning)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 2, 3)
	createAlarmClipTestEvent(t, s, eventID, taskID, sourceID, time.Now())

	session, err := s.startOrExtendAlarmClipSessionBySourceID(sourceID, time.Now(), 2, 3, []string{eventID})
	if err != nil {
		t.Fatalf("start session failed: %v", err)
	}
	if err := s.finalizeAlarmClipSession(session.ID, "manual"); err != nil {
		t.Fatalf("finalize session failed: %v", err)
	}
	event := getAlarmClipTestEvent(t, s, eventID)
	if !event.ClipReady {
		t.Fatalf("expected clip_ready=true")
	}
	if strings.TrimSpace(event.ClipFilesJSON) != "[]" {
		t.Fatalf("expected empty clip files")
	}
	var files []string
	if err := json.Unmarshal([]byte(event.ClipFilesJSON), &files); err != nil {
		t.Fatalf("decode clip files failed: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no clip files")
	}
}
