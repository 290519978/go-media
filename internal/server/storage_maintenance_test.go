package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"maas-box/internal/model"
)

func TestRemoveFilesOlderThan(t *testing.T) {
	root := t.TempDir()
	oldFile := filepath.Join(root, "old.mp4")
	newFile := filepath.Join(root, "new.mp4")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatalf("write old file failed: %v", err)
	}
	if err := os.WriteFile(newFile, []byte("new"), 0o644); err != nil {
		t.Fatalf("write new file failed: %v", err)
	}

	oldTime := time.Now().Add(-72 * time.Hour)
	newTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old file failed: %v", err)
	}
	if err := os.Chtimes(newFile, newTime, newTime); err != nil {
		t.Fatalf("chtimes new file failed: %v", err)
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	removed, _, err := removeFilesOlderThan(root, cutoff)
	if err != nil {
		t.Fatalf("removeFilesOlderThan failed: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 removed file, got %d", removed)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatalf("expected old file removed, stat err: %v", err)
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Fatalf("expected new file exists, stat err: %v", err)
	}
}

func TestCleanupEmptyDirs(t *testing.T) {
	root := t.TempDir()
	keepDir := filepath.Join(root, "keep")
	emptyDir := filepath.Join(root, "nested", "empty")
	if err := os.MkdirAll(keepDir, 0o755); err != nil {
		t.Fatalf("mkdir keep dir failed: %v", err)
	}
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("mkdir empty dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keepDir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write keep file failed: %v", err)
	}

	if err := cleanupEmptyDirs(root); err != nil {
		t.Fatalf("cleanupEmptyDirs failed: %v", err)
	}
	if _, err := os.Stat(emptyDir); !os.IsNotExist(err) {
		t.Fatalf("expected empty dir removed, stat err: %v", err)
	}
	if _, err := os.Stat(keepDir); err != nil {
		t.Fatalf("expected keep dir still exists, stat err: %v", err)
	}
}

func TestCleanupRunStatsDetailLines(t *testing.T) {
	stats := newCleanupRunStats()
	stats.addFiles("compact_recordings", filepath.Join("configs", "recordings"), []cleanupFile{
		{RelPath: "device-a/clip-1.mp4", Size: 2 * 1024 * 1024},
		{RelPath: "device-a/clip-2.mp4", Size: 3 * 1024 * 1024},
		{RelPath: "device-b/clip-3.mp4", Size: 1024 * 1024},
		{RelPath: "device-c/clip-4.mp4", Size: 1024},
	})
	stats.addDirs("hard_alarm_clips_retained", filepath.Join("configs", "recordings"), []alarmClipEventDir{
		{RelPath: "device-a/alarm_clips/event-1", Files: 4, Size: 5 * 1024 * 1024},
	})

	lines := stats.detailLines()
	if len(lines) != 2 {
		t.Fatalf("expected 2 detail lines, got %d", len(lines))
	}
	recordingLine := lines[0]
	if !strings.Contains(recordingLine, "category=软清理-持续录制分片") {
		t.Fatalf("expected readable category label, got %s", recordingLine)
	}
	if !strings.Contains(recordingLine, "root=configs/recordings") {
		t.Fatalf("expected root dir in detail line, got %s", recordingLine)
	}
	if !strings.Contains(recordingLine, "removed_files=4") {
		t.Fatalf("expected removed file count, got %s", recordingLine)
	}
	if !strings.Contains(recordingLine, "removed_mb=6.00") {
		t.Fatalf("expected removed mb summary, got %s", recordingLine)
	}
	if strings.Contains(recordingLine, "device-c/clip-4.mp4") {
		t.Fatalf("expected samples to be truncated to 3 entries, got %s", recordingLine)
	}

	clipLine := lines[1]
	if !strings.Contains(clipLine, "category=硬清理-报警片段留证") {
		t.Fatalf("expected retained clip label, got %s", clipLine)
	}
	if !strings.Contains(clipLine, "removed_dirs=1") {
		t.Fatalf("expected removed dir count, got %s", clipLine)
	}
}

func TestStorageCleanupClearsRemovedDBPaths(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.AI.RetainDays = 1
	s.cfg.Server.Cleanup.AlarmClipRetainDays = 1

	testsRel := filepath.ToSlash(filepath.Join("storage_cleanup_case", "image.jpg"))
	testsAbs := filepath.Join(algorithmTestMediaRootDir, filepath.FromSlash(testsRel))
	if err := os.MkdirAll(filepath.Dir(testsAbs), 0o755); err != nil {
		t.Fatalf("mkdir test image dir failed: %v", err)
	}
	if err := os.WriteFile(testsAbs, []byte("img"), 0o644); err != nil {
		t.Fatalf("write test image failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(testsAbs)
		_ = os.Remove(filepath.Dir(testsAbs))
	})

	eventsRel := filepath.ToSlash(filepath.Join("storage_cleanup_case", "snapshot.jpg"))
	eventsAbs := filepath.Join("configs", "events", filepath.FromSlash(eventsRel))
	if err := os.MkdirAll(filepath.Dir(eventsAbs), 0o755); err != nil {
		t.Fatalf("mkdir event image dir failed: %v", err)
	}
	if err := os.WriteFile(eventsAbs, []byte("img"), 0o644); err != nil {
		t.Fatalf("write event image failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(eventsAbs)
		_ = os.Remove(filepath.Dir(eventsAbs))
	})

	old := time.Now().Add(-72 * time.Hour)
	_ = os.Chtimes(testsAbs, old, old)
	_ = os.Chtimes(eventsAbs, old, old)

	record := model.AlgorithmTestRecord{
		ID:          "cleanup-record-1",
		AlgorithmID: "cleanup-alg-1",
		MediaPath:   testsRel,
		ImagePath:   testsRel,
		Success:     true,
	}
	if err := s.db.Create(&record).Error; err != nil {
		t.Fatalf("insert algorithm test record failed: %v", err)
	}
	jobItem := model.AlgorithmTestJobItem{
		ID:               "cleanup-job-item-1",
		JobID:            "cleanup-job-1",
		AlgorithmID:      "cleanup-alg-1",
		SortOrder:        0,
		FileName:         "image.jpg",
		OriginalFileName: "image.jpg",
		MediaType:        string(algorithmTestMediaTypeImage),
		MediaPath:        testsRel,
		Status:           model.AlgorithmTestJobItemStatusSuccess,
		Success:          true,
	}
	if err := s.db.Create(&jobItem).Error; err != nil {
		t.Fatalf("insert algorithm test job item failed: %v", err)
	}

	event := model.AlarmEvent{
		ID:             "cleanup-event-1",
		TaskID:         "task-x",
		DeviceID:       "device-x",
		AlgorithmID:    "alg-x",
		AlarmLevelID:   "level-x",
		Status:         model.EventStatusPending,
		OccurredAt:     time.Now(),
		SnapshotPath:   eventsRel,
		SnapshotWidth:  10,
		SnapshotHeight: 10,
	}
	if err := s.db.Create(&event).Error; err != nil {
		t.Fatalf("insert alarm event failed: %v", err)
	}

	s.runStorageCleanup()

	var gotRecord model.AlgorithmTestRecord
	if err := s.db.Where("id = ?", record.ID).First(&gotRecord).Error; err != nil {
		t.Fatalf("query algorithm test record failed: %v", err)
	}
	if gotRecord.ImagePath != "" {
		t.Fatalf("expected image_path cleared, got %q", gotRecord.ImagePath)
	}
	if gotRecord.MediaPath != "" {
		t.Fatalf("expected media_path cleared, got %q", gotRecord.MediaPath)
	}
	var gotJobItem model.AlgorithmTestJobItem
	if err := s.db.Where("id = ?", jobItem.ID).First(&gotJobItem).Error; err != nil {
		t.Fatalf("query algorithm test job item failed: %v", err)
	}
	if gotJobItem.MediaPath != "" {
		t.Fatalf("expected job item media_path cleared, got %q", gotJobItem.MediaPath)
	}

	var gotEvent model.AlarmEvent
	if err := s.db.Where("id = ?", event.ID).First(&gotEvent).Error; err != nil {
		t.Fatalf("query alarm event failed: %v", err)
	}
	if gotEvent.SnapshotPath != eventsRel {
		t.Fatalf("expected snapshot_path kept in routine cleanup, got %q want %q", gotEvent.SnapshotPath, eventsRel)
	}
	if _, err := os.Stat(eventsAbs); err != nil {
		t.Fatalf("expected event snapshot kept in routine cleanup, err=%v", err)
	}
}

func TestStorageCleanupKeepsActiveAlgorithmTestMedia(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.AI.RetainDays = 1

	protectedRel := filepath.ToSlash(filepath.Join("storage_cleanup_active_job", "protected.jpg"))
	protectedAbs := filepath.Join(algorithmTestMediaRootDir, filepath.FromSlash(protectedRel))
	removableRel := filepath.ToSlash(filepath.Join("storage_cleanup_active_job", "removable.jpg"))
	removableAbs := filepath.Join(algorithmTestMediaRootDir, filepath.FromSlash(removableRel))
	for _, p := range []string{protectedAbs, removableAbs} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir algorithm test dir failed: %v", err)
		}
		if err := os.WriteFile(p, []byte("img"), 0o644); err != nil {
			t.Fatalf("write algorithm test file failed: %v", err)
		}
		old := time.Now().Add(-72 * time.Hour)
		_ = os.Chtimes(p, old, old)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(algorithmTestMediaRootDir, "storage_cleanup_active_job"))
	})

	if err := s.db.Create(&model.AlgorithmTestJobItem{
		ID:               "cleanup-active-job-item-1",
		JobID:            "cleanup-active-job-1",
		AlgorithmID:      "cleanup-active-alg-1",
		SortOrder:        0,
		FileName:         "protected.jpg",
		OriginalFileName: "protected.jpg",
		MediaType:        string(algorithmTestMediaTypeImage),
		MediaPath:        protectedRel,
		Status:           model.AlgorithmTestJobItemStatusRunning,
	}).Error; err != nil {
		t.Fatalf("insert active algorithm test job item failed: %v", err)
	}

	s.runStorageCleanup()

	if _, err := os.Stat(protectedAbs); err != nil {
		t.Fatalf("active algorithm test media should exist, err=%v", err)
	}
	if _, err := os.Stat(removableAbs); !os.IsNotExist(err) {
		t.Fatalf("inactive algorithm test media should be deleted, err=%v", err)
	}
}

func TestStorageCleanupKeepsReferencedCoverAndDeletesOrphan(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 1
	s.cfg.Server.Cleanup.HardWatermark = 99
	s.cfg.Server.Cleanup.CriticalWatermark = 99
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Cleanup.EmergencyBreakGlass = false

	refRel := filepath.ToSlash(filepath.Join("storage_cleanup_cover", "ref.jpg"))
	refAbs := filepath.Join("configs", "cover", filepath.FromSlash(refRel))
	orphanRel := filepath.ToSlash(filepath.Join("storage_cleanup_cover", "orphan.jpg"))
	orphanAbs := filepath.Join("configs", "cover", filepath.FromSlash(orphanRel))

	for _, p := range []string{refAbs, orphanAbs} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir cover dir failed: %v", err)
		}
		if err := os.WriteFile(p, []byte("cover"), 0o644); err != nil {
			t.Fatalf("write cover file failed: %v", err)
		}
		old := time.Now().Add(-72 * time.Hour)
		_ = os.Chtimes(p, old, old)
	}
	t.Cleanup(func() {
		_ = os.Remove(refAbs)
		_ = os.Remove(orphanAbs)
		_ = os.Remove(filepath.Dir(refAbs))
	})

	alg := model.Algorithm{
		ID:              "cleanup-cover-alg-1",
		Name:            "cleanup-cover-alg-1",
		Mode:            model.AlgorithmModeSmall,
		Enabled:         true,
		SmallModelLabel: "person",
		ImageURL:        "/api/v1/algorithms/cover/" + refRel,
	}
	if err := s.db.Create(&alg).Error; err != nil {
		t.Fatalf("insert algorithm failed: %v", err)
	}

	s.runStorageCleanup()

	if _, err := os.Stat(refAbs); err != nil {
		t.Fatalf("referenced cover should exist, err=%v", err)
	}
	if _, err := os.Stat(orphanAbs); !os.IsNotExist(err) {
		t.Fatalf("orphan cover should be deleted, err=%v", err)
	}
}

func TestStorageCleanupSoftKeepsReferencedDeviceSnapshotAndDeletesOrphan(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 1
	s.cfg.Server.Cleanup.HardWatermark = 99
	s.cfg.Server.Cleanup.CriticalWatermark = 99
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Cleanup.EmergencyBreakGlass = false

	refRel := "snapshot_ref_device.jpg"
	refAbs := filepath.Join("configs", deviceSnapshotDirName, refRel)
	orphanRel := "snapshot_orphan_extra.jpg"
	orphanAbs := filepath.Join("configs", deviceSnapshotDirName, orphanRel)

	for _, p := range []string{refAbs, orphanAbs} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir snapshot dir failed: %v", err)
		}
		if err := os.WriteFile(p, []byte("snapshot"), 0o644); err != nil {
			t.Fatalf("write snapshot file failed: %v", err)
		}
	}
	t.Cleanup(func() {
		_ = os.Remove(refAbs)
		_ = os.Remove(orphanAbs)
		_ = os.Remove(filepath.Dir(refAbs))
	})

	source := model.MediaSource{
		ID:               "snapshot-ref-device",
		Name:             "Snapshot Ref Device",
		AreaID:           model.RootAreaID,
		SourceType:       model.SourceTypePush,
		RowKind:          model.RowKindChannel,
		Protocol:         model.ProtocolRTMP,
		Transport:        "tcp",
		App:              "live",
		StreamID:         "snapshot_ref_device",
		StreamURL:        "rtmp://127.0.0.1:11935/live/snapshot_ref_device",
		Status:           "offline",
		AIStatus:         model.DeviceAIStatusIdle,
		EnableRecording:  false,
		RecordingMode:    model.RecordingModeNone,
		RecordingStatus:  "stopped",
		EnableAlarmClip:  false,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
		MediaServerID:    "local",
		SnapshotURL:      "/api/v1/devices/snapshot/" + refRel,
		ExtraJSON:        "{}",
		OutputConfig:     "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("insert source failed: %v", err)
	}

	s.runStorageCleanup()

	if _, err := os.Stat(refAbs); err != nil {
		t.Fatalf("referenced snapshot should exist, err=%v", err)
	}
	if _, err := os.Stat(orphanAbs); !os.IsNotExist(err) {
		t.Fatalf("orphan snapshot should be deleted, err=%v", err)
	}

	var got model.MediaSource
	if err := s.db.Where("id = ?", source.ID).First(&got).Error; err != nil {
		t.Fatalf("query source failed: %v", err)
	}
	if got.SnapshotURL != source.SnapshotURL {
		t.Fatalf("referenced snapshot url should be kept, got=%q want=%q", got.SnapshotURL, source.SnapshotURL)
	}
}

func TestStorageCleanupSoftKeepsActiveAlgorithmTestMedia(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.SoftWatermark = 1
	s.cfg.Server.Cleanup.HardWatermark = 99
	s.cfg.Server.Cleanup.CriticalWatermark = 99
	s.cfg.Server.Cleanup.MinFreeGB = 0

	protectedRel := filepath.ToSlash(filepath.Join("storage_cleanup_soft_active_job", "protected.jpg"))
	protectedAbs := filepath.Join(algorithmTestMediaRootDir, filepath.FromSlash(protectedRel))
	removableRel := filepath.ToSlash(filepath.Join("storage_cleanup_soft_active_job", "removable.jpg"))
	removableAbs := filepath.Join(algorithmTestMediaRootDir, filepath.FromSlash(removableRel))
	for _, p := range []string{protectedAbs, removableAbs} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir algorithm test dir failed: %v", err)
		}
		if err := os.WriteFile(p, []byte("img"), 0o644); err != nil {
			t.Fatalf("write algorithm test file failed: %v", err)
		}
		old := time.Now().Add(-72 * time.Hour)
		_ = os.Chtimes(p, old, old)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(algorithmTestMediaRootDir, "storage_cleanup_soft_active_job"))
	})

	if err := s.db.Create(&model.AlgorithmTestJobItem{
		ID:               "cleanup-soft-active-job-item-1",
		JobID:            "cleanup-soft-active-job-1",
		AlgorithmID:      "cleanup-soft-active-alg-1",
		SortOrder:        0,
		FileName:         "protected.jpg",
		OriginalFileName: "protected.jpg",
		MediaType:        string(algorithmTestMediaTypeImage),
		MediaPath:        protectedRel,
		Status:           model.AlgorithmTestJobItemStatusPending,
	}).Error; err != nil {
		t.Fatalf("insert active algorithm test job item failed: %v", err)
	}

	s.runSoftCompaction(time.Now(), s.cfg.Server.Recording.StorageDir, newCriticalUsageSnapshot(1000, 990), newCleanupRunStats())

	if _, err := os.Stat(protectedAbs); err != nil {
		t.Fatalf("active algorithm test media should exist after soft compaction, err=%v", err)
	}
	if _, err := os.Stat(removableAbs); !os.IsNotExist(err) {
		t.Fatalf("inactive algorithm test media should be deleted by soft compaction, err=%v", err)
	}
}

func TestStorageCleanupKeepsAlarmBufferFilesRequiredByActiveSession(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 100
	s.cfg.Server.Cleanup.HardWatermark = 100
	s.cfg.Server.Cleanup.CriticalWatermark = 100
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Recording.AlarmClip.BufferKeepSeconds = 60
	s.cfg.Server.Recording.AlarmClip.BufferDir = filepath.Join(t.TempDir(), "recordings-buffer")

	sourceID := "cleanup-buffer-protected-device"
	bufferDir := filepath.Join(s.cfg.Server.Recording.AlarmClip.BufferDir, sourceID)
	protectedAbs := filepath.Join(bufferDir, "protected.mp4")
	removableAbs := filepath.Join(bufferDir, "removable.mp4")
	for _, p := range []string{protectedAbs, removableAbs} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir buffer dir failed: %v", err)
		}
		if err := os.WriteFile(p, []byte("buffer"), 0o644); err != nil {
			t.Fatalf("write buffer file failed: %v", err)
		}
	}
	now := time.Now()
	protectedTime := now.Add(-10 * time.Minute)
	removableTime := now.Add(-3 * time.Hour)
	if err := os.Chtimes(protectedAbs, protectedTime, protectedTime); err != nil {
		t.Fatalf("chtimes protected buffer file failed: %v", err)
	}
	if err := os.Chtimes(removableAbs, removableTime, removableTime); err != nil {
		t.Fatalf("chtimes removable buffer file failed: %v", err)
	}

	session := model.AlarmClipSession{
		ID:             "cleanup-buffer-session-1",
		SourceID:       sourceID,
		Status:         alarmClipSessionStatusRecording,
		AnchorEventID:  "cleanup-buffer-event-1",
		PreSeconds:     8,
		PostSeconds:    12,
		StartedAt:      now.Add(-2 * time.Hour),
		LastAlarmAt:    now.Add(-5 * time.Minute),
		ExpectedEndAt:  now.Add(5 * time.Minute),
		HardDeadlineAt: now.Add(30 * time.Minute),
		ClipFilesJSON:  "[]",
	}
	if err := s.db.Create(&session).Error; err != nil {
		t.Fatalf("insert alarm clip session failed: %v", err)
	}

	s.runStorageCleanup()

	if _, err := os.Stat(protectedAbs); err != nil {
		t.Fatalf("protected buffer file should exist, err=%v", err)
	}
	if _, err := os.Stat(removableAbs); !os.IsNotExist(err) {
		t.Fatalf("out-of-window buffer file should be removed, err=%v", err)
	}
}

func TestPruneAlarmBufferFilesKeepsActiveSessionWindow(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Recording.AlarmClip.BufferDir = filepath.Join(t.TempDir(), "recordings-buffer")
	sourceID := "cleanup-buffer-prune-device"
	bufferDir := filepath.Join(s.cfg.Server.Recording.AlarmClip.BufferDir, sourceID)
	protectedAbs := filepath.Join(bufferDir, "protected_prune.mp4")
	removableAbs := filepath.Join(bufferDir, "removable_prune.mp4")
	for _, p := range []string{protectedAbs, removableAbs} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir buffer dir failed: %v", err)
		}
		if err := os.WriteFile(p, []byte("buffer"), 0o644); err != nil {
			t.Fatalf("write buffer file failed: %v", err)
		}
	}
	now := time.Now()
	protectedTime := now.Add(-10 * time.Minute)
	removableTime := now.Add(-3 * time.Hour)
	if err := os.Chtimes(protectedAbs, protectedTime, protectedTime); err != nil {
		t.Fatalf("chtimes protected buffer file failed: %v", err)
	}
	if err := os.Chtimes(removableAbs, removableTime, removableTime); err != nil {
		t.Fatalf("chtimes removable buffer file failed: %v", err)
	}

	session := model.AlarmClipSession{
		ID:             "cleanup-buffer-prune-session-1",
		SourceID:       sourceID,
		Status:         alarmClipSessionStatusClosing,
		AnchorEventID:  "cleanup-buffer-prune-event-1",
		PreSeconds:     8,
		PostSeconds:    12,
		StartedAt:      now.Add(-2 * time.Hour),
		LastAlarmAt:    now.Add(-5 * time.Minute),
		ExpectedEndAt:  now.Add(5 * time.Minute),
		HardDeadlineAt: now.Add(30 * time.Minute),
		ClipFilesJSON:  "[]",
	}
	if err := s.db.Create(&session).Error; err != nil {
		t.Fatalf("insert alarm clip session failed: %v", err)
	}

	if err := s.pruneAlarmBufferFiles(sourceID, 8, 12); err != nil {
		t.Fatalf("prune alarm buffer files failed: %v", err)
	}

	if _, err := os.Stat(protectedAbs); err != nil {
		t.Fatalf("protected buffer file should exist, err=%v", err)
	}
	if _, err := os.Stat(removableAbs); !os.IsNotExist(err) {
		t.Fatalf("out-of-window buffer file should be removed, err=%v", err)
	}
}

func TestStorageCleanupRecordingRetentionSkipsAlarmClips(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Recording.RetainDays = 1
	s.cfg.Server.Cleanup.AlarmClipRetainDays = 365

	recordRoot := s.cfg.Server.Recording.StorageDir
	deviceID := "cleanup-device-1"
	normalAbs := filepath.Join(recordRoot, deviceID, "normal.mp4")
	clipAbs := filepath.Join(recordRoot, deviceID, alarmClipDirName, "event-1", "001.mp4")

	for _, p := range []string{normalAbs, clipAbs} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir recording dir failed: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write recording file failed: %v", err)
		}
		old := time.Now().Add(-72 * time.Hour)
		_ = os.Chtimes(p, old, old)
	}

	s.runStorageCleanup()

	if _, err := os.Stat(normalAbs); !os.IsNotExist(err) {
		t.Fatalf("normal recording should be deleted by retain days, err=%v", err)
	}
	if _, err := os.Stat(clipAbs); err != nil {
		t.Fatalf("alarm clip should be retained by alarm clip retain days, err=%v", err)
	}
}

func TestStorageCleanupRoutineDoesNotDeleteRetentionEvidence(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 100
	s.cfg.Server.Cleanup.HardWatermark = 100
	s.cfg.Server.Cleanup.CriticalWatermark = 100
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Cleanup.EmergencyBreakGlass = false
	s.cfg.Server.AI.RetainDays = 1
	s.cfg.Server.Cleanup.AlarmClipRetainDays = 1

	evidence := seedOverdueRetentionEvidence(t, s, "routine")
	s.runStorageCleanup()

	if _, err := os.Stat(evidence.snapshotAbs); err != nil {
		t.Fatalf("routine cleanup should keep overdue snapshot evidence, err=%v", err)
	}
	if _, err := os.Stat(evidence.clipAbs); err != nil {
		t.Fatalf("routine cleanup should keep overdue alarm clip evidence, err=%v", err)
	}
	var got model.AlarmEvent
	if err := s.db.Where("id = ?", evidence.eventID).First(&got).Error; err != nil {
		t.Fatalf("query event failed: %v", err)
	}
	if got.SnapshotPath == "" || got.ClipPath == "" {
		t.Fatalf("routine cleanup should not clear evidence fields: snapshot=%q clip=%q", got.SnapshotPath, got.ClipPath)
	}
}

func TestStorageCleanupSoftIssuesRetentionNoticeBeforeDelete(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 1
	s.cfg.Server.Cleanup.HardWatermark = 99
	s.cfg.Server.Cleanup.CriticalWatermark = 99
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Cleanup.EmergencyBreakGlass = false
	s.cfg.Server.AI.RetainDays = 1
	s.cfg.Server.Cleanup.AlarmClipRetainDays = 1

	evidence := seedOverdueRetentionEvidence(t, s, "soft-notice")
	s.runStorageCleanup()

	if _, err := os.Stat(evidence.snapshotAbs); err != nil {
		t.Fatalf("soft first round should skip deleting snapshot evidence, err=%v", err)
	}
	if _, err := os.Stat(evidence.clipAbs); err != nil {
		t.Fatalf("soft first round should skip deleting alarm clip evidence, err=%v", err)
	}
	notice := mustLoadCleanupRetentionNotice(t, s)
	if notice == nil {
		t.Fatalf("expected retention cleanup notice to be created")
	}
	if strings.TrimSpace(notice.CandidateFingerprint) == "" {
		t.Fatalf("expected retention cleanup notice fingerprint")
	}
}

func TestStorageCleanupSoftPressureIssuesNoticeWithoutRetentionCandidates(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 1
	s.cfg.Server.Cleanup.HardWatermark = 99
	s.cfg.Server.Cleanup.CriticalWatermark = 99
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Cleanup.EmergencyBreakGlass = false

	s.runStorageCleanup()

	softNotice := mustLoadCleanupSoftPressureNotice(t, s)
	if softNotice == nil {
		t.Fatalf("expected soft pressure notice")
	}
	if softNotice.UsedPercent <= 0 {
		t.Fatalf("unexpected soft pressure used percent: %.2f", softNotice.UsedPercent)
	}

	retentionNotice := mustLoadCleanupRetentionNotice(t, s)
	if retentionNotice != nil {
		t.Fatalf("did not expect retention notice without overdue candidates")
	}
}

func TestStorageCleanupSoftPressureNoticeDedupesWhileInSoft(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 1
	s.cfg.Server.Cleanup.HardWatermark = 99
	s.cfg.Server.Cleanup.CriticalWatermark = 99
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Cleanup.EmergencyBreakGlass = false

	s.runStorageCleanup()
	first := mustLoadCleanupSoftPressureNotice(t, s)
	if first == nil {
		t.Fatalf("expected first soft pressure notice")
	}

	s.runStorageCleanup()
	second := mustLoadCleanupSoftPressureNotice(t, s)
	if second == nil {
		t.Fatalf("expected second soft pressure notice")
	}
	if second.NoticeID != first.NoticeID {
		t.Fatalf("soft pressure notice should be deduped in same soft stage, first=%s second=%s", first.NoticeID, second.NoticeID)
	}
}

func TestStorageCleanupSoftPressureNoticeReissuesAfterExitSoft(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 1
	s.cfg.Server.Cleanup.HardWatermark = 99
	s.cfg.Server.Cleanup.CriticalWatermark = 99
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Cleanup.EmergencyBreakGlass = false

	s.runStorageCleanup()
	first := mustLoadCleanupSoftPressureNotice(t, s)
	if first == nil {
		t.Fatalf("expected first soft pressure notice")
	}

	s.cfg.Server.Cleanup.SoftWatermark = 100
	s.runStorageCleanup()
	if notice := mustLoadCleanupSoftPressureNotice(t, s); notice != nil {
		t.Fatalf("expected soft pressure notice cleared after leaving soft stage")
	}

	s.cfg.Server.Cleanup.SoftWatermark = 1
	s.runStorageCleanup()
	second := mustLoadCleanupSoftPressureNotice(t, s)
	if second == nil {
		t.Fatalf("expected reissued soft pressure notice")
	}
	if second.NoticeID == first.NoticeID {
		t.Fatalf("expected new soft pressure notice after re-entering soft stage")
	}
}

func TestStorageCleanupSoftDedupesRetentionNoticeForSameCandidates(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 1
	s.cfg.Server.Cleanup.HardWatermark = 99
	s.cfg.Server.Cleanup.CriticalWatermark = 99
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Cleanup.EmergencyBreakGlass = false
	s.cfg.Server.AI.RetainDays = 1
	s.cfg.Server.Cleanup.AlarmClipRetainDays = 1

	evidence := seedOverdueRetentionEvidence(t, s, "soft-dedup")
	s.runStorageCleanup()
	noticeFirst := mustLoadCleanupRetentionNotice(t, s)
	if noticeFirst == nil {
		t.Fatalf("expected first retention cleanup notice")
	}

	s.runStorageCleanup()

	if _, err := os.Stat(evidence.snapshotAbs); err != nil {
		t.Fatalf("soft cleanup should keep snapshot evidence, err=%v", err)
	}
	if _, err := os.Stat(evidence.clipAbs); err != nil {
		t.Fatalf("soft cleanup should keep clip evidence, err=%v", err)
	}

	noticeSecond := mustLoadCleanupRetentionNotice(t, s)
	if noticeSecond == nil {
		t.Fatalf("expected retention cleanup notice after second round")
	}
	if noticeSecond.NoticeID != noticeFirst.NoticeID {
		t.Fatalf("expected same notice for same candidate batch, first=%s second=%s", noticeFirst.NoticeID, noticeSecond.NoticeID)
	}
}

func TestStorageCleanupHardDeletesRetentionEvidenceWithoutNoticeGate(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 100
	s.cfg.Server.Cleanup.HardWatermark = 1
	s.cfg.Server.Cleanup.CriticalWatermark = 99
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Cleanup.EmergencyBreakGlass = false
	s.cfg.Server.AI.RetainDays = 1
	s.cfg.Server.Cleanup.AlarmClipRetainDays = 1

	evidence := seedOverdueRetentionEvidence(t, s, "hard-direct-delete")
	now := time.Now()
	if err := s.saveCleanupRetentionNoticeState(&cleanupRetentionNoticeState{
		NoticeID:             "hard-stale-notice",
		IssuedAt:             now.Add(-2 * time.Hour),
		EventSnapshotCount:   1,
		AlarmClipCount:       1,
		CandidateFingerprint: "hard-stale-fingerprint",
	}); err != nil {
		t.Fatalf("save stale notice failed: %v", err)
	}

	s.runStorageCleanup()

	if _, err := os.Stat(evidence.snapshotAbs); !os.IsNotExist(err) {
		t.Fatalf("hard cleanup should delete snapshot evidence immediately, err=%v", err)
	}
	if _, err := os.Stat(evidence.clipAbs); !os.IsNotExist(err) {
		t.Fatalf("hard cleanup should delete clip evidence immediately, err=%v", err)
	}

	var got model.AlarmEvent
	if err := s.db.Where("id = ?", evidence.eventID).First(&got).Error; err != nil {
		t.Fatalf("query event failed: %v", err)
	}
	if got.SnapshotPath != "" || got.ClipPath != "" || got.ClipFilesJSON != "[]" {
		t.Fatalf("expected evidence fields cleared after retention cleanup, snapshot=%q clip=%q files=%q", got.SnapshotPath, got.ClipPath, got.ClipFilesJSON)
	}

	notice := mustLoadCleanupRetentionNotice(t, s)
	if notice != nil {
		t.Fatalf("expected retention notice state to be cleared after cleanup")
	}
}

func TestAuthMeReturnsPendingCleanupNotice(t *testing.T) {
	s := newFocusedTestServer(t)
	now := time.Now()
	if err := s.saveCleanupSoftPressureNoticeState(&cleanupSoftPressureNoticeState{
		NoticeID:      "auth-me-soft-notice",
		IssuedAt:      now.Add(-2 * time.Hour),
		UsedPercent:   91.2,
		FreeGB:        1.3,
		SoftWatermark: 90,
	}); err != nil {
		t.Fatalf("save pending soft pressure notice failed: %v", err)
	}
	if err := s.saveCleanupRetentionNoticeState(&cleanupRetentionNoticeState{
		NoticeID:             "auth-me-notice",
		IssuedAt:             now.Add(-time.Hour),
		EventSnapshotCount:   3,
		AlarmClipCount:       2,
		CandidateFingerprint: "auth-me-fingerprint",
	}); err != nil {
		t.Fatalf("save pending notice failed: %v", err)
	}

	engine := s.Engine()
	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("auth me failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			DevelopmentMode bool             `json:"development_mode"`
			CleanupNotice   map[string]any   `json:"cleanup_notice"`
			CleanupNotices  []map[string]any `json:"cleanup_notices"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode auth me response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("auth me response code mismatch: %d", resp.Code)
	}
	if resp.Data.DevelopmentMode {
		t.Fatalf("expected development_mode=false by default")
	}
	if len(resp.Data.CleanupNotice) == 0 {
		t.Fatalf("expected cleanup_notice in auth me response")
	}
	if got := resp.Data.CleanupNotice["notice_id"]; got != "auth-me-soft-notice" {
		t.Fatalf("cleanup_notice.notice_id mismatch: got=%v want=%s", got, "auth-me-soft-notice")
	}
	if got := resp.Data.CleanupNotice["notice_kind"]; got != "soft_pressure" {
		t.Fatalf("cleanup_notice.notice_kind mismatch: got=%v want=%s", got, "soft_pressure")
	}
	if len(resp.Data.CleanupNotices) != 2 {
		t.Fatalf("expected 2 cleanup_notices, got=%d", len(resp.Data.CleanupNotices))
	}
	kinds := map[string]int{}
	for _, item := range resp.Data.CleanupNotices {
		if item == nil {
			continue
		}
		kinds[strings.TrimSpace(fmt.Sprint(item["notice_kind"]))]++
	}
	if kinds["soft_pressure"] == 0 || kinds["retention_risk"] == 0 {
		t.Fatalf("cleanup_notices should include soft_pressure and retention_risk, got=%v", kinds)
	}
}

func TestAuthMeReturnsDevelopmentModeWhenConfigured(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Development = "hzwlzhg"

	engine := s.Engine()
	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("auth me failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			DevelopmentMode bool `json:"development_mode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode auth me response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("auth me response code mismatch: %d", resp.Code)
	}
	if !resp.Data.DevelopmentMode {
		t.Fatalf("expected development_mode=true when configured")
	}
}

type retentionEvidenceFixture struct {
	eventID     string
	snapshotRel string
	snapshotAbs string
	clipAbs     string
}

func seedOverdueRetentionEvidence(t *testing.T, s *Server, prefix string) retentionEvidenceFixture {
	t.Helper()

	eventID := "retention-" + prefix + "-event"
	deviceID := "retention-" + prefix + "-device"
	snapshotRel := filepath.ToSlash(filepath.Join("storage_cleanup_retention", prefix, "snapshot.jpg"))
	snapshotAbs := filepath.Join("configs", "events", filepath.FromSlash(snapshotRel))
	if err := os.MkdirAll(filepath.Dir(snapshotAbs), 0o755); err != nil {
		t.Fatalf("mkdir snapshot dir failed: %v", err)
	}
	if err := os.WriteFile(snapshotAbs, []byte("snapshot"), 0o644); err != nil {
		t.Fatalf("write snapshot failed: %v", err)
	}

	clipRel := filepath.ToSlash(filepath.Join(alarmClipDirName, eventID, "001.mp4"))
	clipAbs := filepath.Join(s.cfg.Server.Recording.StorageDir, deviceID, filepath.FromSlash(clipRel))
	if err := os.MkdirAll(filepath.Dir(clipAbs), 0o755); err != nil {
		t.Fatalf("mkdir clip dir failed: %v", err)
	}
	if err := os.WriteFile(clipAbs, []byte("clip"), 0o644); err != nil {
		t.Fatalf("write clip failed: %v", err)
	}

	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(snapshotAbs, old, old); err != nil {
		t.Fatalf("chtimes snapshot failed: %v", err)
	}
	if err := os.Chtimes(clipAbs, old, old); err != nil {
		t.Fatalf("chtimes clip failed: %v", err)
	}
	if err := os.Chtimes(filepath.Dir(clipAbs), old, old); err != nil {
		t.Fatalf("chtimes clip event dir failed: %v", err)
	}

	event := model.AlarmEvent{
		ID:             eventID,
		TaskID:         "task-" + prefix,
		DeviceID:       deviceID,
		AlgorithmID:    "algorithm-" + prefix,
		AlarmLevelID:   "alarm_level_1",
		Status:         model.EventStatusPending,
		OccurredAt:     old,
		SnapshotPath:   snapshotRel,
		SnapshotWidth:  100,
		SnapshotHeight: 80,
		ClipReady:      true,
		ClipPath:       filepath.ToSlash(filepath.Join(alarmClipDirName, eventID)),
		ClipFilesJSON:  `["` + clipRel + `"]`,
	}
	if err := s.db.Create(&event).Error; err != nil {
		t.Fatalf("insert retention event failed: %v", err)
	}

	t.Cleanup(func() {
		_ = os.Remove(snapshotAbs)
		_ = os.Remove(clipAbs)
		_ = os.RemoveAll(filepath.Join("configs", "events", "storage_cleanup_retention"))
		_ = os.RemoveAll(filepath.Join(s.cfg.Server.Recording.StorageDir, deviceID))
	})

	return retentionEvidenceFixture{
		eventID:     eventID,
		snapshotRel: snapshotRel,
		snapshotAbs: snapshotAbs,
		clipAbs:     clipAbs,
	}
}

func mustLoadCleanupRetentionNotice(t *testing.T, s *Server) *cleanupRetentionNoticeState {
	t.Helper()
	notice, err := s.loadCleanupRetentionNoticeState()
	if err != nil {
		t.Fatalf("load cleanup retention notice failed: %v", err)
	}
	return notice
}

func mustLoadCleanupSoftPressureNotice(t *testing.T, s *Server) *cleanupSoftPressureNoticeState {
	t.Helper()
	notice, err := s.loadCleanupSoftPressureNoticeState()
	if err != nil {
		t.Fatalf("load cleanup soft pressure notice failed: %v", err)
	}
	return notice
}

type criticalAlarmEvidenceFixture struct {
	eventID     string
	deviceID    string
	occurredAt  time.Time
	snapshotRel string
	snapshotAbs string
	clipDirRel  string
	clipFileRel string
	clipDirAbs  string
	clipFileAbs string
}

func seedCriticalAlarmEvidence(
	t *testing.T,
	s *Server,
	prefix string,
	occurredAt time.Time,
	snapshotContent string,
	clipContent string,
) criticalAlarmEvidenceFixture {
	t.Helper()

	fixture := criticalAlarmEvidenceFixture{
		eventID:    "critical-" + prefix + "-event",
		deviceID:   "critical-" + prefix + "-device",
		occurredAt: occurredAt,
	}
	if snapshotContent != "" {
		fixture.snapshotRel = filepath.ToSlash(filepath.Join("storage_cleanup_break_glass", prefix, "snapshot.jpg"))
		fixture.snapshotAbs = filepath.Join("configs", "events", filepath.FromSlash(fixture.snapshotRel))
		if err := os.MkdirAll(filepath.Dir(fixture.snapshotAbs), 0o755); err != nil {
			t.Fatalf("mkdir event snapshot dir failed: %v", err)
		}
		if err := os.WriteFile(fixture.snapshotAbs, []byte(snapshotContent), 0o644); err != nil {
			t.Fatalf("write event snapshot failed: %v", err)
		}
		if err := os.Chtimes(fixture.snapshotAbs, occurredAt, occurredAt); err != nil {
			t.Fatalf("chtimes event snapshot failed: %v", err)
		}
	}
	if clipContent != "" {
		fixture.clipDirRel = filepath.ToSlash(filepath.Join(alarmClipDirName, fixture.eventID))
		fixture.clipFileRel = filepath.ToSlash(filepath.Join(fixture.clipDirRel, "001.mp4"))
		fixture.clipFileAbs = filepath.Join(s.cfg.Server.Recording.StorageDir, fixture.deviceID, filepath.FromSlash(fixture.clipFileRel))
		fixture.clipDirAbs = filepath.Dir(fixture.clipFileAbs)
		if err := os.MkdirAll(fixture.clipDirAbs, 0o755); err != nil {
			t.Fatalf("mkdir clip dir failed: %v", err)
		}
		if err := os.WriteFile(fixture.clipFileAbs, []byte(clipContent), 0o644); err != nil {
			t.Fatalf("write clip file failed: %v", err)
		}
		if err := os.Chtimes(fixture.clipFileAbs, occurredAt, occurredAt); err != nil {
			t.Fatalf("chtimes clip file failed: %v", err)
		}
		if err := os.Chtimes(fixture.clipDirAbs, occurredAt, occurredAt); err != nil {
			t.Fatalf("chtimes clip dir failed: %v", err)
		}
	}

	event := model.AlarmEvent{
		ID:             fixture.eventID,
		TaskID:         "task-" + prefix,
		DeviceID:       fixture.deviceID,
		AlgorithmID:    "algorithm-" + prefix,
		AlarmLevelID:   "alarm-level-" + prefix,
		Status:         model.EventStatusPending,
		OccurredAt:     occurredAt,
		SnapshotPath:   fixture.snapshotRel,
		SnapshotWidth:  64,
		SnapshotHeight: 48,
		ClipReady:      fixture.clipDirRel != "",
		ClipPath:       fixture.clipDirRel,
		ClipFilesJSON:  "[]",
	}
	if fixture.clipFileRel != "" {
		event.ClipFilesJSON = `["` + fixture.clipFileRel + `"]`
	}
	if err := s.db.Create(&event).Error; err != nil {
		t.Fatalf("insert critical evidence event failed: %v", err)
	}

	t.Cleanup(func() {
		if fixture.snapshotAbs != "" {
			_ = os.Remove(fixture.snapshotAbs)
		}
		if fixture.deviceID != "" {
			_ = os.RemoveAll(filepath.Join(s.cfg.Server.Recording.StorageDir, fixture.deviceID))
		}
		_ = os.RemoveAll(filepath.Join("configs", "events", "storage_cleanup_break_glass"))
	})

	return fixture
}

func mustLoadAlarmEvent(t *testing.T, s *Server, eventID string) model.AlarmEvent {
	t.Helper()
	var event model.AlarmEvent
	if err := s.db.Where("id = ?", eventID).First(&event).Error; err != nil {
		t.Fatalf("query event failed: %v", err)
	}
	return event
}

func newCriticalUsageSnapshot(total, used uint64) *diskUsageSnapshot {
	usage := &diskUsageSnapshot{
		Root:  ".",
		Total: total,
		Used:  used,
		Valid: total > 0 && used <= total,
	}
	if usage.Valid {
		usage.Free = total - used
		usage.UsedPercent = float64(used) / float64(total) * 100
	}
	return usage
}

func TestCriticalAlarmEvidenceRemovesOldestHourFirst(t *testing.T) {
	s := newFocusedTestServer(t)
	oldFixture := seedCriticalAlarmEvidence(t, s, "old-hour-clip", time.Date(2026, 3, 19, 7, 34, 0, 0, time.UTC), "", "clip")
	newFixture := seedCriticalAlarmEvidence(t, s, "new-hour-snapshot", time.Date(2026, 3, 20, 8, 3, 0, 0, time.UTC), "snap", "")

	usage := newCriticalUsageSnapshot(1000, 990)
	removed, err := s.removeCriticalAlarmEvidenceByHour(s.cfg.Server.Recording.StorageDir, usage, 98.6, 0)
	if err != nil {
		t.Fatalf("removeCriticalAlarmEvidenceByHour failed: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("expected 1 removed bucket, got %d", len(removed))
	}
	if !removed[0].Hour.Equal(oldFixture.occurredAt.Truncate(time.Hour)) {
		t.Fatalf("expected oldest hour removed first, got %s want %s", removed[0].Hour, oldFixture.occurredAt.Truncate(time.Hour))
	}

	if _, err := os.Stat(oldFixture.clipFileAbs); !os.IsNotExist(err) {
		t.Fatalf("old clip should be deleted first, err=%v", err)
	}
	if _, err := os.Stat(newFixture.snapshotAbs); err != nil {
		t.Fatalf("newer snapshot should be kept, err=%v", err)
	}

	oldEvent := mustLoadAlarmEvent(t, s, oldFixture.eventID)
	if oldEvent.ClipPath != "" || oldEvent.ClipFilesJSON != "[]" {
		t.Fatalf("expected old clip fields cleared, path=%q files=%q", oldEvent.ClipPath, oldEvent.ClipFilesJSON)
	}
	newEvent := mustLoadAlarmEvent(t, s, newFixture.eventID)
	if newEvent.SnapshotPath != newFixture.snapshotRel {
		t.Fatalf("expected newer snapshot path kept, got %q want %q", newEvent.SnapshotPath, newFixture.snapshotRel)
	}
}

func TestCriticalAlarmEvidenceRemovesSameHourSnapshotAndClipTogether(t *testing.T) {
	s := newFocusedTestServer(t)
	sameHour := time.Date(2026, 3, 19, 7, 34, 0, 0, time.UTC)
	oldSnapshot := seedCriticalAlarmEvidence(t, s, "same-hour-snapshot", sameHour, "snap", "")
	oldClip := seedCriticalAlarmEvidence(t, s, "same-hour-clip", sameHour.Add(20*time.Minute), "", "clp")
	newFixture := seedCriticalAlarmEvidence(t, s, "later-hour-snapshot", time.Date(2026, 3, 20, 8, 3, 0, 0, time.UTC), "news", "")

	usage := newCriticalUsageSnapshot(1000, 990)
	removed, err := s.removeCriticalAlarmEvidenceByHour(s.cfg.Server.Recording.StorageDir, usage, 98.4, 0)
	if err != nil {
		t.Fatalf("removeCriticalAlarmEvidenceByHour failed: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("expected 1 removed bucket, got %d", len(removed))
	}
	if got := len(removed[0].SnapshotFiles); got != 1 {
		t.Fatalf("expected same-hour bucket to remove 1 snapshot, got %d", got)
	}
	if got := len(removed[0].ClipDirs); got != 1 {
		t.Fatalf("expected same-hour bucket to remove 1 clip dir, got %d", got)
	}

	if _, err := os.Stat(oldSnapshot.snapshotAbs); !os.IsNotExist(err) {
		t.Fatalf("same-hour snapshot should be deleted, err=%v", err)
	}
	if _, err := os.Stat(oldClip.clipFileAbs); !os.IsNotExist(err) {
		t.Fatalf("same-hour clip should be deleted, err=%v", err)
	}
	if _, err := os.Stat(newFixture.snapshotAbs); err != nil {
		t.Fatalf("later-hour snapshot should be kept, err=%v", err)
	}

	oldSnapshotEvent := mustLoadAlarmEvent(t, s, oldSnapshot.eventID)
	if oldSnapshotEvent.SnapshotPath != "" {
		t.Fatalf("expected same-hour snapshot path cleared, got %q", oldSnapshotEvent.SnapshotPath)
	}
	oldClipEvent := mustLoadAlarmEvent(t, s, oldClip.eventID)
	if oldClipEvent.ClipPath != "" || oldClipEvent.ClipFilesJSON != "[]" {
		t.Fatalf("expected same-hour clip fields cleared, path=%q files=%q", oldClipEvent.ClipPath, oldClipEvent.ClipFilesJSON)
	}
	newEvent := mustLoadAlarmEvent(t, s, newFixture.eventID)
	if newEvent.SnapshotPath != newFixture.snapshotRel {
		t.Fatalf("expected later-hour snapshot path kept, got %q want %q", newEvent.SnapshotPath, newFixture.snapshotRel)
	}
}

func TestStorageCleanupBreakGlassRemovesNonAlarmEvidenceBeforeLatestAlarmEvidence(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.Cleanup.Enabled = true
	s.cfg.Server.Cleanup.SoftWatermark = 1
	s.cfg.Server.Cleanup.HardWatermark = 99
	s.cfg.Server.Cleanup.CriticalWatermark = 98
	s.cfg.Server.Cleanup.MinFreeGB = 0
	s.cfg.Server.Cleanup.EmergencyBreakGlass = true
	s.cfg.Server.Cleanup.AlarmClipRetainDays = 365
	s.cfg.Server.AI.RetainDays = 365

	oldFixture := seedCriticalAlarmEvidence(t, s, "break-glass-old-clip", time.Date(2026, 3, 19, 7, 34, 0, 0, time.UTC), "", "clip")
	newFixture := seedCriticalAlarmEvidence(t, s, "break-glass-new-snapshot", time.Date(2026, 3, 20, 8, 3, 0, 0, time.UTC), "snap", "")

	coverRel := filepath.ToSlash(filepath.Join("storage_cleanup_break_glass", "cover.jpg"))
	coverAbs := filepath.Join("configs", "cover", filepath.FromSlash(coverRel))
	if err := os.MkdirAll(filepath.Dir(coverAbs), 0o755); err != nil {
		t.Fatalf("mkdir cover dir failed: %v", err)
	}
	if err := os.WriteFile(coverAbs, []byte("c"), 0o644); err != nil {
		t.Fatalf("write cover file failed: %v", err)
	}
	algorithm := model.Algorithm{
		ID:                "cleanup-break-glass-alg-1",
		Code:              "ALG_BREAK_GLASS_001",
		Name:              "cleanup-break-glass-alg-1",
		Scene:             "scene",
		Category:          "category",
		Mode:              model.AlgorithmModeHybrid,
		Enabled:           true,
		SmallModelLabel:   "person",
		DetectMode:        model.AlgorithmDetectModeHybrid,
		YoloThreshold:     0.5,
		IOUThreshold:      0.8,
		LabelsTriggerMode: model.LabelsTriggerModeAny,
		ImageURL:          "/api/v1/algorithms/cover/" + coverRel,
	}
	if err := s.db.Create(&algorithm).Error; err != nil {
		t.Fatalf("insert algorithm failed: %v", err)
	}

	deviceSnapshotRel := "cleanup_break_glass_device.jpg"
	deviceSnapshotAbs := filepath.Join("configs", deviceSnapshotDirName, deviceSnapshotRel)
	if err := os.MkdirAll(filepath.Dir(deviceSnapshotAbs), 0o755); err != nil {
		t.Fatalf("mkdir device snapshot dir failed: %v", err)
	}
	if err := os.WriteFile(deviceSnapshotAbs, []byte("d"), 0o644); err != nil {
		t.Fatalf("write device snapshot failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(coverAbs)
		_ = os.Remove(deviceSnapshotAbs)
		_ = os.RemoveAll(filepath.Join("configs", "cover", "storage_cleanup_break_glass"))
	})
	source := model.MediaSource{
		ID:               "cleanup-break-glass-source-1",
		Name:             "cleanup-break-glass-source-1",
		AreaID:           model.RootAreaID,
		SourceType:       model.SourceTypePush,
		RowKind:          model.RowKindChannel,
		Protocol:         model.ProtocolRTMP,
		Transport:        "tcp",
		App:              "live",
		StreamID:         "cleanup_break_glass_source_1",
		StreamURL:        "rtmp://127.0.0.1:11935/live/cleanup_break_glass_source_1",
		Status:           "offline",
		AIStatus:         model.DeviceAIStatusIdle,
		EnableRecording:  false,
		RecordingMode:    model.RecordingModeNone,
		RecordingStatus:  "stopped",
		EnableAlarmClip:  false,
		AlarmPreSeconds:  8,
		AlarmPostSeconds: 12,
		MediaServerID:    "local",
		SnapshotURL:      "/api/v1/devices/snapshot/" + deviceSnapshotRel,
		ExtraJSON:        "{}",
		OutputConfig:     "{}",
	}
	if err := s.db.Create(&source).Error; err != nil {
		t.Fatalf("insert source failed: %v", err)
	}

	usage := newCriticalUsageSnapshot(1000, 990)
	stats := newCleanupRunStats()
	s.runCriticalCompaction(time.Now(), s.cfg.Server.Recording.StorageDir, usage, stats)

	if _, err := os.Stat(coverAbs); !os.IsNotExist(err) {
		t.Fatalf("cover file should be deleted in break_glass mode, err=%v", err)
	}
	if _, err := os.Stat(deviceSnapshotAbs); !os.IsNotExist(err) {
		t.Fatalf("device snapshot file should be deleted in break_glass mode, err=%v", err)
	}
	if _, err := os.Stat(oldFixture.clipFileAbs); !os.IsNotExist(err) {
		t.Fatalf("old clip should be deleted in break_glass mode, err=%v", err)
	}
	if _, err := os.Stat(newFixture.snapshotAbs); err != nil {
		t.Fatalf("latest snapshot should be preserved after older hour removal, err=%v", err)
	}

	oldEvent := mustLoadAlarmEvent(t, s, oldFixture.eventID)
	if oldEvent.ClipPath != "" || oldEvent.ClipFilesJSON != "[]" {
		t.Fatalf("expected old clip fields cleared, path=%q files=%q", oldEvent.ClipPath, oldEvent.ClipFilesJSON)
	}
	newEvent := mustLoadAlarmEvent(t, s, newFixture.eventID)
	if newEvent.SnapshotPath != newFixture.snapshotRel {
		t.Fatalf("expected latest snapshot path kept, got %q want %q", newEvent.SnapshotPath, newFixture.snapshotRel)
	}

	var gotAlgorithm model.Algorithm
	if err := s.db.Where("id = ?", algorithm.ID).First(&gotAlgorithm).Error; err != nil {
		t.Fatalf("query algorithm failed: %v", err)
	}
	if gotAlgorithm.ImageURL != "" {
		t.Fatalf("expected algorithm image_url cleared, got %q", gotAlgorithm.ImageURL)
	}

	var gotSource model.MediaSource
	if err := s.db.Where("id = ?", source.ID).First(&gotSource).Error; err != nil {
		t.Fatalf("query source failed: %v", err)
	}
	if gotSource.SnapshotURL != "" {
		t.Fatalf("expected source snapshot_url cleared, got %q", gotSource.SnapshotURL)
	}
	if stats.byCategory["break_glass_alarm_clips"] == nil {
		t.Fatalf("expected break_glass_alarm_clips stats recorded")
	}
	if stats.byCategory["break_glass_covers"] == nil {
		t.Fatalf("expected break_glass_covers stats recorded")
	}
	if stats.byCategory["break_glass_device_snapshots"] == nil {
		t.Fatalf("expected break_glass_device_snapshots stats recorded")
	}
}
