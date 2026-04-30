package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"maas-box/internal/model"
)

func writeAlarmClipTestFile(t *testing.T, path string, body []byte, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes failed: %v", err)
	}
}

func createAlarmClipTestSource(t *testing.T, s *Server, sourceID string, enableRecording bool, recordingMode string) {
	t.Helper()
	source := model.MediaSource{
		ID:              sourceID,
		Name:            "clip-source-" + sourceID,
		AreaID:          model.RootAreaID,
		SourceType:      model.SourceTypePull,
		RowKind:         model.RowKindChannel,
		Protocol:        model.ProtocolRTSP,
		Transport:       "tcp",
		App:             "live",
		StreamID:        "stream-" + sourceID,
		StreamURL:       "rtsp://127.0.0.1/live/" + sourceID,
		Status:          "online",
		AIStatus:        model.DeviceAIStatusRunning,
		EnableRecording: enableRecording,
		RecordingMode:   recordingMode,
		RecordingStatus: recordingStatusStopped,
		OutputConfig:    "{}",
		ExtraJSON:       "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("create source failed: %v", err)
	}
}

func createAlarmClipTestProfile(t *testing.T, s *Server, taskID, sourceID string, preSeconds, postSeconds int) {
	t.Helper()
	profile := model.VideoTaskDeviceProfile{
		TaskID:           taskID,
		DeviceID:         sourceID,
		FrameInterval:    1,
		SmallConfidence:  0.5,
		LargeConfidence:  0.8,
		SmallIOU:         0.5,
		AlarmLevelID:     "level-test",
		RecordingPolicy:  model.RecordingPolicyAlarmClip,
		AlarmPreSeconds:  preSeconds,
		AlarmPostSeconds: postSeconds,
	}
	if err := s.db.Create(&profile).Error; err != nil {
		t.Fatalf("create task device profile failed: %v", err)
	}
}

func createAlarmClipTestEvent(t *testing.T, s *Server, eventID, taskID, sourceID string, occurredAt time.Time) {
	t.Helper()
	event := model.AlarmEvent{
		ID:           eventID,
		TaskID:       taskID,
		DeviceID:     sourceID,
		AlgorithmID:  "alg-test",
		AlarmLevelID: "level-test",
		Status:       model.EventStatusPending,
		OccurredAt:   occurredAt,
	}
	if err := s.db.Create(&event).Error; err != nil {
		t.Fatalf("create event failed: %v", err)
	}
}

func getAlarmClipTestEvent(t *testing.T, s *Server, eventID string) model.AlarmEvent {
	t.Helper()
	var event model.AlarmEvent
	if err := s.db.Where("id = ?", eventID).First(&event).Error; err != nil {
		t.Fatalf("query event failed: %v", err)
	}
	return event
}

func listAlarmClipTestDirs(t *testing.T, s *Server, sourceID string) []string {
	t.Helper()
	deviceDir, err := s.safeRecordingDeviceDir(sourceID)
	if err != nil {
		t.Fatalf("safe recording dir failed: %v", err)
	}
	alarmRoot := filepath.Join(deviceDir, alarmClipDirName)
	entries, err := os.ReadDir(alarmRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read alarm clip dir failed: %v", err)
	}
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			items = append(items, entry.Name())
		}
	}
	sort.Strings(items)
	return items
}

func withAlarmClipRetryTuningForTest(t *testing.T, interval, continuousFloor, bufferFloor, continuousGrace, bufferGrace time.Duration) {
	t.Helper()
	oldInterval := alarmClipFinalizeRetryInterval
	oldContinuousFloor := alarmClipFinalizeContinuousFloor
	oldBufferFloor := alarmClipFinalizeBufferFloor
	oldContinuousGrace := alarmClipFinalizeContinuousGrace
	oldBufferGrace := alarmClipFinalizeBufferGrace

	alarmClipFinalizeRetryInterval = interval
	alarmClipFinalizeContinuousFloor = continuousFloor
	alarmClipFinalizeBufferFloor = bufferFloor
	alarmClipFinalizeContinuousGrace = continuousGrace
	alarmClipFinalizeBufferGrace = bufferGrace

	t.Cleanup(func() {
		alarmClipFinalizeRetryInterval = oldInterval
		alarmClipFinalizeContinuousFloor = oldContinuousFloor
		alarmClipFinalizeBufferFloor = oldBufferFloor
		alarmClipFinalizeContinuousGrace = oldContinuousGrace
		alarmClipFinalizeBufferGrace = oldBufferGrace
	})
}

func withAlarmClipMergeRunnerForTest(
	t *testing.T,
	runner func(eventDir, outputName string, segmentNames []string) error,
) {
	t.Helper()
	oldRunner := alarmClipMergeRunner
	alarmClipMergeRunner = runner
	t.Cleanup(func() {
		alarmClipMergeRunner = oldRunner
	})
}

func TestCollectAlarmClipFilesFiltersTemporaryAndInvalidFiles(t *testing.T) {
	s := newFocusedTestServer(t)
	dir := t.TempDir()
	occurredAt := time.Now()

	writeAlarmClipTestFile(t, filepath.Join(dir, "normal.mp4"), []byte("ok"), occurredAt)
	writeAlarmClipTestFile(t, filepath.Join(dir, ".tmp.mp4"), []byte("tmp"), occurredAt)
	writeAlarmClipTestFile(t, filepath.Join(dir, "empty.mp4"), []byte{}, occurredAt)
	writeAlarmClipTestFile(t, filepath.Join(dir, "other.m4s"), []byte("m4s"), occurredAt)
	writeAlarmClipTestFile(t, filepath.Join(dir, alarmClipDirName, "ev-1", "from_alarm_clips.mp4"), []byte("clip"), occurredAt)

	files, err := s.collectAlarmClipFiles(dir, true, occurredAt, 10, 10)
	if err != nil {
		t.Fatalf("collect files failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected only 1 valid file, got %d", len(files))
	}
	if files[0].fileName != "normal.mp4" {
		t.Fatalf("expected normal.mp4, got %s", files[0].fileName)
	}
	if strings.Contains(files[0].relPath, alarmClipDirName+"/") {
		t.Fatalf("alarm clips directory should be excluded, got %s", files[0].relPath)
	}
}

func TestFinalizeAlarmClipByEventsRetriesAndSucceeds(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.SegmentSeconds = 1
	s.cfg.Server.Recording.AlarmClip.BufferDir = filepath.Join(t.TempDir(), "alarm-buffer")
	withAlarmClipMergeRunnerForTest(t, func(eventDir, outputName string, segmentNames []string) error {
		if len(segmentNames) == 0 {
			return nil
		}
		mergedPath := filepath.Join(eventDir, outputName)
		return os.WriteFile(mergedPath, []byte("merged-video"), 0o644)
	})
	withAlarmClipRetryTuningForTest(
		t,
		20*time.Millisecond,
		150*time.Millisecond,
		100*time.Millisecond,
		0,
		0,
	)

	sourceID := "source-alarm-retry-success"
	taskID := "task-alarm-retry-success"
	eventID := "event-alarm-retry-success"
	occurredAt := time.Now()

	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 1, 1)
	createAlarmClipTestEvent(t, s, eventID, taskID, sourceID, occurredAt)

	recordDir, err := s.safeAlarmBufferDeviceDir(sourceID)
	if err != nil {
		t.Fatalf("safe alarm buffer dir failed: %v", err)
	}
	writeErrCh := make(chan error, 1)
	go func() {
		time.Sleep(60 * time.Millisecond)
		filePathA := filepath.Join(recordDir, "live", "segment-success-a.mp4")
		filePathB := filepath.Join(recordDir, "live", "segment-success-b.mp4")
		if err := os.MkdirAll(filepath.Dir(filePathA), 0o755); err != nil {
			writeErrCh <- err
			return
		}
		if err := os.WriteFile(filePathA, []byte("video-bytes-a"), 0o644); err != nil {
			writeErrCh <- err
			return
		}
		if err := os.Chtimes(filePathA, occurredAt, occurredAt); err != nil {
			writeErrCh <- err
			return
		}
		if err := os.WriteFile(filePathB, []byte("video-bytes-b"), 0o644); err != nil {
			writeErrCh <- err
			return
		}
		if err := os.Chtimes(filePathB, occurredAt.Add(500*time.Millisecond), occurredAt.Add(500*time.Millisecond)); err != nil {
			writeErrCh <- err
			return
		}
		writeErrCh <- nil
	}()

	if err := s.finalizeAlarmClipByEvents(sourceID, occurredAt, 1, 1, []string{eventID}); err != nil {
		t.Fatalf("finalize alarm clip failed: %v", err)
	}
	if writeErr := <-writeErrCh; writeErr != nil {
		t.Fatalf("create retry file failed: %v", writeErr)
	}

	var event model.AlarmEvent
	if err := s.db.Where("id = ?", eventID).First(&event).Error; err != nil {
		t.Fatalf("query event failed: %v", err)
	}
	if !event.ClipReady {
		t.Fatalf("expected clip_ready=true")
	}
	if strings.TrimSpace(event.ClipFilesJSON) == "" || strings.TrimSpace(event.ClipFilesJSON) == "[]" {
		t.Fatalf("expected non-empty clip_files_json, got %q", event.ClipFilesJSON)
	}
	var clipFiles []string
	if err := json.Unmarshal([]byte(event.ClipFilesJSON), &clipFiles); err != nil {
		t.Fatalf("decode clip files failed: %v", err)
	}
	if len(clipFiles) == 0 {
		t.Fatalf("expected at least one clip file")
	}
	for _, item := range clipFiles {
		if !strings.HasSuffix(item, ".mp4") {
			t.Fatalf("unexpected clip file: %s", item)
		}
	}
}

func TestFinalizeAlarmClipByEventsMergeFailureFallsBackToSegments(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.SegmentSeconds = 1
	s.cfg.Server.Recording.AlarmClip.BufferDir = filepath.Join(t.TempDir(), "alarm-buffer")
	withAlarmClipMergeRunnerForTest(t, func(eventDir, outputName string, segmentNames []string) error {
		return errors.New("mock merge failed")
	})
	withAlarmClipRetryTuningForTest(
		t,
		20*time.Millisecond,
		150*time.Millisecond,
		100*time.Millisecond,
		0,
		0,
	)

	sourceID := "source-alarm-merge-fallback"
	taskID := "task-alarm-merge-fallback"
	eventID := "event-alarm-merge-fallback"
	occurredAt := time.Now()

	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 1, 1)
	createAlarmClipTestEvent(t, s, eventID, taskID, sourceID, occurredAt)

	recordDir, err := s.safeAlarmBufferDeviceDir(sourceID)
	if err != nil {
		t.Fatalf("safe alarm buffer dir failed: %v", err)
	}
	filePathA := filepath.Join(recordDir, "live", "segment-a.mp4")
	filePathB := filepath.Join(recordDir, "live", "segment-b.mp4")
	writeAlarmClipTestFile(t, filePathA, []byte("seg-a"), occurredAt)
	writeAlarmClipTestFile(t, filePathB, []byte("seg-b"), occurredAt.Add(500*time.Millisecond))

	if err := s.finalizeAlarmClipByEvents(sourceID, occurredAt, 1, 1, []string{eventID}); err != nil {
		t.Fatalf("finalize alarm clip failed: %v", err)
	}

	var event model.AlarmEvent
	if err := s.db.Where("id = ?", eventID).First(&event).Error; err != nil {
		t.Fatalf("query event failed: %v", err)
	}
	if !event.ClipReady {
		t.Fatalf("expected clip_ready=true")
	}
	var clipFiles []string
	if err := json.Unmarshal([]byte(event.ClipFilesJSON), &clipFiles); err != nil {
		t.Fatalf("decode clip files failed: %v", err)
	}
	if len(clipFiles) != 2 {
		t.Fatalf("expected fallback to segment files, got %d", len(clipFiles))
	}
	for _, item := range clipFiles {
		if !strings.HasSuffix(item, ".mp4") || strings.Contains(item, alarmClipMergedName) {
			t.Fatalf("unexpected fallback clip file: %s", item)
		}
	}
}

func TestFinalizeAlarmClipByEventsMarksTerminalWhenNoFiles(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.BufferDir = filepath.Join(t.TempDir(), "alarm-buffer")
	withAlarmClipRetryTuningForTest(
		t,
		20*time.Millisecond,
		150*time.Millisecond,
		80*time.Millisecond,
		0,
		0,
	)

	sourceID := "source-alarm-timeout"
	taskID := "task-alarm-timeout"
	eventID := "event-alarm-timeout"
	occurredAt := time.Now()

	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 1, 1)
	createAlarmClipTestEvent(t, s, eventID, taskID, sourceID, occurredAt)

	if err := s.finalizeAlarmClipByEvents(sourceID, occurredAt, 1, 1, []string{eventID}); err != nil {
		t.Fatalf("finalize alarm clip failed: %v", err)
	}

	var event model.AlarmEvent
	if err := s.db.Where("id = ?", eventID).First(&event).Error; err != nil {
		t.Fatalf("query event failed: %v", err)
	}
	if !event.ClipReady {
		t.Fatalf("expected clip_ready=true")
	}
	if strings.TrimSpace(event.ClipPath) != "" {
		t.Fatalf("expected empty clip_path, got %q", event.ClipPath)
	}
	if strings.TrimSpace(event.ClipFilesJSON) != "[]" {
		t.Fatalf("expected empty clip_files_json array, got %q", event.ClipFilesJSON)
	}
}

func TestFinalizeAlarmClipByEventsBatchShareSingleOutput(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.BufferDir = filepath.Join(t.TempDir(), "alarm-buffer")
	withAlarmClipRetryTuningForTest(
		t,
		20*time.Millisecond,
		150*time.Millisecond,
		80*time.Millisecond,
		0,
		0,
	)

	sourceID := "source-alarm-batch-share"
	taskID := "task-alarm-batch-share"
	occurredAt := time.Now()
	eventIDs := []string{"event-batch-1", "event-batch-2", "event-batch-3"}

	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 2, 2)
	for _, eventID := range eventIDs {
		createAlarmClipTestEvent(t, s, eventID, taskID, sourceID, occurredAt)
	}
	recordDir, err := s.safeAlarmBufferDeviceDir(sourceID)
	if err != nil {
		t.Fatalf("safe alarm buffer dir failed: %v", err)
	}
	writeAlarmClipTestFile(t, filepath.Join(recordDir, "live", "segment-batch.mp4"), []byte("batch-video"), occurredAt)

	if err := s.finalizeAlarmClipByEvents(sourceID, occurredAt, 2, 2, eventIDs); err != nil {
		t.Fatalf("finalize alarm clip failed: %v", err)
	}

	first := getAlarmClipTestEvent(t, s, eventIDs[0])
	if strings.TrimSpace(first.ClipFilesJSON) == "" || strings.TrimSpace(first.ClipFilesJSON) == "[]" {
		t.Fatalf("expected first event clip files")
	}
	for _, eventID := range eventIDs[1:] {
		current := getAlarmClipTestEvent(t, s, eventID)
		if strings.TrimSpace(current.ClipFilesJSON) != strings.TrimSpace(first.ClipFilesJSON) {
			t.Fatalf("expected shared clip_files_json, event=%s got=%s want=%s", eventID, current.ClipFilesJSON, first.ClipFilesJSON)
		}
		if strings.TrimSpace(current.ClipPath) != strings.TrimSpace(first.ClipPath) {
			t.Fatalf("expected shared clip_path, event=%s got=%s want=%s", eventID, current.ClipPath, first.ClipPath)
		}
	}
	dirs := listAlarmClipTestDirs(t, s, sourceID)
	if len(dirs) != 1 {
		t.Fatalf("expected one alarm clip dir for batch, got %v", dirs)
	}
	expectedTS := formatAlarmClipTS(occurredAt)
	var clipFiles []string
	if err := json.Unmarshal([]byte(first.ClipFilesJSON), &clipFiles); err != nil {
		t.Fatalf("decode clip files failed: %v", err)
	}
	if len(clipFiles) == 0 {
		t.Fatalf("expected non-empty clip files")
	}
	for _, item := range clipFiles {
		base := filepath.Base(item)
		if !strings.HasPrefix(base, expectedTS+"_") {
			t.Fatalf("clip file should include timestamp prefix, file=%s expected_prefix=%s_", base, expectedTS)
		}
	}
	clipDir, err := s.safeRecordingFilePath(sourceID, first.ClipPath)
	if err != nil {
		t.Fatalf("resolve clip dir failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clipDir, "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest.json should not exist, err=%v", err)
	}
}

func TestFinalizeAlarmClipByEventsReuseOverlappedWindow(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.BufferDir = filepath.Join(t.TempDir(), "alarm-buffer")
	withAlarmClipRetryTuningForTest(
		t,
		20*time.Millisecond,
		150*time.Millisecond,
		80*time.Millisecond,
		0,
		0,
	)

	sourceID := "source-alarm-overlap-reuse"
	taskID := "task-alarm-overlap-reuse"
	firstEventID := "event-overlap-1"
	secondEventID := "event-overlap-2"
	firstAt := time.Now()
	secondAt := firstAt.Add(2 * time.Second)

	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 5, 5)
	createAlarmClipTestEvent(t, s, firstEventID, taskID, sourceID, firstAt)
	recordDir, err := s.safeAlarmBufferDeviceDir(sourceID)
	if err != nil {
		t.Fatalf("safe alarm buffer dir failed: %v", err)
	}
	writeAlarmClipTestFile(t, filepath.Join(recordDir, "live", "segment-overlap-1.mp4"), []byte("overlap-video-1"), firstAt)

	if err := s.finalizeAlarmClipByEvents(sourceID, firstAt, 5, 5, []string{firstEventID}); err != nil {
		t.Fatalf("finalize first overlap event failed: %v", err)
	}
	first := getAlarmClipTestEvent(t, s, firstEventID)

	_ = os.RemoveAll(recordDir)
	if err := os.MkdirAll(recordDir, 0o755); err != nil {
		t.Fatalf("recreate record dir failed: %v", err)
	}
	createAlarmClipTestEvent(t, s, secondEventID, taskID, sourceID, secondAt)

	if err := s.finalizeAlarmClipByEvents(sourceID, secondAt, 5, 5, []string{secondEventID}); err != nil {
		t.Fatalf("finalize second overlap event failed: %v", err)
	}
	second := getAlarmClipTestEvent(t, s, secondEventID)
	if strings.TrimSpace(second.ClipFilesJSON) != strings.TrimSpace(first.ClipFilesJSON) {
		t.Fatalf("expected overlap reuse clip_files_json, got=%s want=%s", second.ClipFilesJSON, first.ClipFilesJSON)
	}
	if strings.TrimSpace(second.ClipPath) != strings.TrimSpace(first.ClipPath) {
		t.Fatalf("expected overlap reuse clip_path, got=%s want=%s", second.ClipPath, first.ClipPath)
	}
	dirs := listAlarmClipTestDirs(t, s, sourceID)
	if len(dirs) != 1 {
		t.Fatalf("expected one alarm clip dir after overlap reuse, got %v", dirs)
	}
}

func TestFinalizeAlarmClipByEventsNoReuseForNonOverlapWindow(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.BufferDir = filepath.Join(t.TempDir(), "alarm-buffer")
	withAlarmClipRetryTuningForTest(
		t,
		20*time.Millisecond,
		150*time.Millisecond,
		80*time.Millisecond,
		0,
		0,
	)

	sourceID := "source-alarm-non-overlap"
	taskID := "task-alarm-non-overlap"
	firstEventID := "event-non-overlap-1"
	secondEventID := "event-non-overlap-2"
	firstAt := time.Now()
	secondAt := firstAt.Add(25 * time.Second)

	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 2, 2)
	createAlarmClipTestEvent(t, s, firstEventID, taskID, sourceID, firstAt)
	recordDir, err := s.safeAlarmBufferDeviceDir(sourceID)
	if err != nil {
		t.Fatalf("safe alarm buffer dir failed: %v", err)
	}
	writeAlarmClipTestFile(t, filepath.Join(recordDir, "live", "segment-non-overlap-1.mp4"), []byte("non-overlap-video-1"), firstAt)

	if err := s.finalizeAlarmClipByEvents(sourceID, firstAt, 2, 2, []string{firstEventID}); err != nil {
		t.Fatalf("finalize first non-overlap event failed: %v", err)
	}
	first := getAlarmClipTestEvent(t, s, firstEventID)

	_ = os.RemoveAll(recordDir)
	if err := os.MkdirAll(recordDir, 0o755); err != nil {
		t.Fatalf("recreate record dir failed: %v", err)
	}
	writeAlarmClipTestFile(t, filepath.Join(recordDir, "live", "segment-non-overlap-2.mp4"), []byte("non-overlap-video-2"), secondAt)
	createAlarmClipTestEvent(t, s, secondEventID, taskID, sourceID, secondAt)

	if err := s.finalizeAlarmClipByEvents(sourceID, secondAt, 2, 2, []string{secondEventID}); err != nil {
		t.Fatalf("finalize second non-overlap event failed: %v", err)
	}
	second := getAlarmClipTestEvent(t, s, secondEventID)
	if strings.TrimSpace(second.ClipFilesJSON) == strings.TrimSpace(first.ClipFilesJSON) {
		t.Fatalf("expected different clip_files_json for non-overlap events")
	}
	if strings.TrimSpace(second.ClipPath) == strings.TrimSpace(first.ClipPath) {
		t.Fatalf("expected different clip_path for non-overlap events")
	}
	dirs := listAlarmClipTestDirs(t, s, sourceID)
	if len(dirs) != 2 {
		t.Fatalf("expected two alarm clip dirs for non-overlap events, got %v", dirs)
	}
}

func TestFinalizeAlarmClipByEventsMissingReuseCandidateFallsBackToNewOutput(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.BufferDir = filepath.Join(t.TempDir(), "alarm-buffer")
	withAlarmClipRetryTuningForTest(
		t,
		20*time.Millisecond,
		150*time.Millisecond,
		80*time.Millisecond,
		0,
		0,
	)

	sourceID := "source-alarm-reuse-missing"
	taskID := "task-alarm-reuse-missing"
	firstEventID := "event-reuse-missing-1"
	secondEventID := "event-reuse-missing-2"
	firstAt := time.Now()
	secondAt := firstAt.Add(1 * time.Second)

	createAlarmClipTestSource(t, s, sourceID, false, model.RecordingModeNone)
	createAlarmClipTestProfile(t, s, taskID, sourceID, 5, 5)
	createAlarmClipTestEvent(t, s, firstEventID, taskID, sourceID, firstAt)
	recordDir, err := s.safeAlarmBufferDeviceDir(sourceID)
	if err != nil {
		t.Fatalf("safe alarm buffer dir failed: %v", err)
	}
	writeAlarmClipTestFile(t, filepath.Join(recordDir, "live", "segment-reuse-missing-1.mp4"), []byte("reuse-missing-video-1"), firstAt)

	if err := s.finalizeAlarmClipByEvents(sourceID, firstAt, 5, 5, []string{firstEventID}); err != nil {
		t.Fatalf("finalize first event failed: %v", err)
	}
	first := getAlarmClipTestEvent(t, s, firstEventID)
	if strings.TrimSpace(first.ClipPath) == "" {
		t.Fatalf("expected first clip_path to be non-empty")
	}
	firstDirPath, err := s.safeRecordingFilePath(sourceID, first.ClipPath)
	if err != nil {
		t.Fatalf("resolve first clip dir failed: %v", err)
	}
	_ = os.RemoveAll(firstDirPath)

	_ = os.RemoveAll(recordDir)
	if err := os.MkdirAll(recordDir, 0o755); err != nil {
		t.Fatalf("recreate record dir failed: %v", err)
	}
	writeAlarmClipTestFile(t, filepath.Join(recordDir, "live", "segment-reuse-missing-2.mp4"), []byte("reuse-missing-video-2"), secondAt)
	createAlarmClipTestEvent(t, s, secondEventID, taskID, sourceID, secondAt)

	if err := s.finalizeAlarmClipByEvents(sourceID, secondAt, 5, 5, []string{secondEventID}); err != nil {
		t.Fatalf("finalize second event failed: %v", err)
	}
	second := getAlarmClipTestEvent(t, s, secondEventID)
	if strings.TrimSpace(second.ClipPath) == strings.TrimSpace(first.ClipPath) {
		t.Fatalf("expected fallback to a new clip path when reuse candidate files are missing")
	}
	if strings.TrimSpace(second.ClipFilesJSON) == strings.TrimSpace(first.ClipFilesJSON) {
		t.Fatalf("expected fallback to new clip files when reuse candidate files are missing")
	}
}
