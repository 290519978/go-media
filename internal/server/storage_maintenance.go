package server

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"maas-box/internal/logutil"
	"maas-box/internal/model"
)

const (
	defaultStorageCleanupInterval    = 30 * time.Minute
	cleanupRetentionNoticeSettingKey = "cleanup_retention_notice_v1"
	cleanupSoftPressureNoticeKey     = "cleanup_soft_pressure_notice_v1"
)

type cleanupRetentionNoticeState struct {
	NoticeID             string    `json:"notice_id"`
	IssuedAt             time.Time `json:"issued_at"`
	EventSnapshotCount   int       `json:"event_snapshot_count"`
	AlarmClipCount       int       `json:"alarm_clip_count"`
	CandidateFingerprint string    `json:"candidate_fingerprint"`
	HardReached          bool      `json:"hard_reached"`
}

type cleanupSoftPressureNoticeState struct {
	NoticeID      string    `json:"notice_id"`
	IssuedAt      time.Time `json:"issued_at"`
	UsedPercent   float64   `json:"used_percent"`
	FreeGB        float64   `json:"free_gb"`
	SoftWatermark float64   `json:"soft_watermark"`
}

type cleanupFile struct {
	Path    string
	RelPath string
	ModTime time.Time
	Size    uint64
}

type alarmClipEventDir struct {
	AbsPath  string
	RelPath  string
	DeviceID string
	EventID  string
	ModTime  time.Time
	Size     uint64
	Files    int
}

type criticalAlarmEvidenceBucket struct {
	Hour          time.Time
	SnapshotFiles []cleanupFile
	ClipDirs      []alarmClipEventDir
	EventIDs      []string
	TotalSize     uint64
}

type cleanupCategorySummary struct {
	Label          string   `json:"label"`
	RootDir        string   `json:"root_dir"`
	RemovedFiles   int      `json:"removed_files"`
	RemovedDirs    int      `json:"removed_dirs"`
	RemovedBytes   uint64   `json:"removed_bytes"`
	SampleRelPaths []string `json:"sample_rel_paths"`
}

type cleanupRunStats struct {
	byCategory map[string]*cleanupCategorySummary
}

func newCleanupRunStats() *cleanupRunStats {
	return &cleanupRunStats{byCategory: make(map[string]*cleanupCategorySummary)}
}

func (s *cleanupRunStats) addFiles(category, rootDir string, files []cleanupFile) {
	if s == nil || len(files) == 0 {
		return
	}
	for _, item := range files {
		s.addCounts(category, rootDir, 1, 0, item.Size, item.RelPath)
	}
}

func (s *cleanupRunStats) addDirs(category, rootDir string, dirs []alarmClipEventDir) {
	if s == nil || len(dirs) == 0 {
		return
	}
	for _, item := range dirs {
		s.addCounts(category, rootDir, item.Files, 1, item.Size, item.RelPath)
	}
}

func (s *cleanupRunStats) addCounts(category, rootDir string, files, dirs int, bytes uint64, sampleRelPath string) {
	if s == nil {
		return
	}
	category = strings.TrimSpace(category)
	if category == "" {
		category = "unknown"
	}
	entry, ok := s.byCategory[category]
	if !ok {
		entry = &cleanupCategorySummary{Label: cleanupCategoryLabel(category)}
		s.byCategory[category] = entry
	}
	if strings.TrimSpace(rootDir) != "" && entry.RootDir == "" {
		entry.RootDir = filepath.ToSlash(strings.TrimSpace(rootDir))
	}
	entry.RemovedFiles += files
	entry.RemovedDirs += dirs
	entry.RemovedBytes += bytes
	entry.SampleRelPaths = appendCleanupSample(entry.SampleRelPaths, sampleRelPath)
}

func (s *cleanupRunStats) totalRemoved() (int, uint64) {
	if s == nil {
		return 0, 0
	}
	files := 0
	var bytes uint64
	for _, item := range s.byCategory {
		files += item.RemovedFiles
		bytes += item.RemovedBytes
	}
	return files, bytes
}

func (s *cleanupRunStats) byCategoryForLog() map[string]cleanupCategorySummary {
	out := make(map[string]cleanupCategorySummary, len(s.byCategory))
	if s == nil {
		return out
	}
	for key, value := range s.byCategory {
		if value == nil {
			continue
		}
		out[key] = *value
	}
	return out
}

func (s *cleanupRunStats) detailLines() []string {
	if s == nil || len(s.byCategory) == 0 {
		return nil
	}
	keys := make([]string, 0, len(s.byCategory))
	for key, item := range s.byCategory {
		if item == nil {
			continue
		}
		if item.RemovedFiles <= 0 && item.RemovedDirs <= 0 && item.RemovedBytes == 0 {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		item := s.byCategory[key]
		if item == nil {
			continue
		}
		rootDir := item.RootDir
		if rootDir == "" {
			rootDir = "-"
		}
		samples := "-"
		if len(item.SampleRelPaths) > 0 {
			samples = strings.Join(item.SampleRelPaths, ", ")
		}
		lines = append(lines, fmt.Sprintf(
			"category=%s root=%s removed_dirs=%d removed_files=%d removed_mb=%.2f samples=%s",
			item.Label,
			rootDir,
			item.RemovedDirs,
			item.RemovedFiles,
			bytesToMB(item.RemovedBytes),
			samples,
		))
	}
	return lines
}

func cleanupCategoryLabel(category string) string {
	switch strings.TrimSpace(category) {
	case "alarm_buffer":
		return "报警缓冲过期分片"
	case "zlm_snap":
		return "ZLM 抓拍临时文件"
	case "test_images":
		return "算法测试图片"
	case "recordings":
		return "持续录制过期分片"
	case "compact_zlm_snap":
		return "软清理-ZLM 抓拍"
	case "compact_test_images":
		return "软清理-算法测试图片"
	case "compact_cover_orphan":
		return "软清理-算法封面孤儿文件"
	case "compact_device_snapshots":
		return "软清理-设备抓拍孤儿文件"
	case "compact_recordings":
		return "软清理-持续录制分片"
	case "hard_recordings":
		return "硬清理-持续录制分片"
	case "break_glass_recordings":
		return "紧急破保-持续录制分片"
	case "break_glass_event_snapshots":
		return "紧急破保-事件快照"
	case "break_glass_covers":
		return "紧急破保-算法封面"
	case "break_glass_device_snapshots":
		return "紧急破保-设备抓拍"
	case "break_glass_alarm_clips":
		return "紧急破保-报警片段目录"
	case "soft_event_snapshots_retained":
		return "软清理-事件快照留证"
	case "soft_alarm_clips_retained":
		return "软清理-报警片段留证"
	case "hard_event_snapshots_retained":
		return "硬清理-事件快照留证"
	case "hard_alarm_clips_retained":
		return "硬清理-报警片段留证"
	default:
		return strings.TrimSpace(category)
	}
}

func appendCleanupSample(samples []string, sample string) []string {
	sample = filepath.ToSlash(strings.TrimSpace(sample))
	if sample == "" || len(samples) >= 3 {
		return samples
	}
	for _, item := range samples {
		if item == sample {
			return samples
		}
	}
	return append(samples, sample)
}

func bytesToMB(size uint64) float64 {
	return float64(size) / (1024 * 1024)
}

type diskUsageSnapshot struct {
	Root        string
	Total       uint64
	Used        uint64
	UsedPercent float64
	Free        uint64
	Valid       bool
}

func (d *diskUsageSnapshot) freeGB() float64 {
	if d == nil || !d.Valid {
		return 0
	}
	return float64(d.Free) / (1024 * 1024 * 1024)
}

func (d *diskUsageSnapshot) isPressure(watermark, minFreeGB float64) bool {
	if d == nil || !d.Valid {
		return false
	}
	if watermark > 0 && d.UsedPercent >= watermark {
		return true
	}
	if minFreeGB > 0 && d.freeGB() < minFreeGB {
		return true
	}
	return false
}

func (d *diskUsageSnapshot) reachedTarget(targetPercent, minFreeGB float64) bool {
	if d == nil || !d.Valid {
		return true
	}
	if targetPercent > 0 && d.UsedPercent > targetPercent {
		return false
	}
	if minFreeGB > 0 && d.freeGB() < minFreeGB {
		return false
	}
	return true
}

func (d *diskUsageSnapshot) subtract(size uint64) {
	if d == nil || !d.Valid {
		return
	}
	if size >= d.Used {
		d.Used = 0
	} else {
		d.Used -= size
	}
	if d.Total > 0 {
		d.Free = d.Total - d.Used
		d.UsedPercent = float64(d.Used) / float64(d.Total) * 100
	}
}

func (d *diskUsageSnapshot) refresh() error {
	if d == nil {
		return nil
	}
	next, err := readDiskUsageSnapshot(d.Root)
	if err != nil {
		return err
	}
	*d = next
	return nil
}

func readDiskUsageSnapshot(root string) (diskUsageSnapshot, error) {
	resolved := resolveExistingPath(root)
	usage, err := disk.Usage(resolved)
	if err != nil {
		return diskUsageSnapshot{}, err
	}
	return diskUsageSnapshot{
		Root:        resolved,
		Total:       usage.Total,
		Used:        usage.Used,
		Free:        usage.Free,
		UsedPercent: usage.UsedPercent,
		Valid:       true,
	}, nil
}

func resolveExistingPath(root string) string {
	candidate := strings.TrimSpace(root)
	if candidate == "" {
		return "."
	}
	candidate = filepath.Clean(candidate)
	for {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "."
		}
		candidate = parent
	}
}

func (s *Server) startStorageJanitor() {
	if s == nil || s.cfg == nil || !s.cfg.Server.Cleanup.Enabled {
		return
	}
	interval := s.storageCleanupInterval()
	go func() {
		s.runStorageCleanup()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			s.runStorageCleanup()
		}
	}()
}

func (s *Server) storageCleanupInterval() time.Duration {
	if s == nil || s.cfg == nil {
		return defaultStorageCleanupInterval
	}
	raw := strings.TrimSpace(s.cfg.Server.Cleanup.Interval)
	if raw == "" {
		return defaultStorageCleanupInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultStorageCleanupInterval
	}
	if d < time.Minute {
		return time.Minute
	}
	return d
}

func (s *Server) runStorageCleanup() {
	if s == nil || s.cfg == nil || !s.cfg.Server.Cleanup.Enabled {
		return
	}

	now := time.Now()
	stats := newCleanupRunStats()

	recordRoot := strings.TrimSpace(s.cfg.Server.Recording.StorageDir)
	if recordRoot == "" {
		recordRoot = filepath.Join("configs", "recordings")
	}
	monitorRoot := recordRoot
	if strings.TrimSpace(monitorRoot) == "" {
		monitorRoot = "."
	}

	before, beforeErr := readDiskUsageSnapshot(monitorRoot)
	if beforeErr != nil {
		logutil.Warnf("storage cleanup disk usage read failed before cleanup: root=%s err=%v", monitorRoot, beforeErr)
	}

	s.runRoutineCleanup(now, recordRoot, stats)

	usage, usageErr := readDiskUsageSnapshot(monitorRoot)
	if usageErr != nil {
		logutil.Warnf("storage cleanup disk usage read failed after routine cleanup: root=%s err=%v", monitorRoot, usageErr)
	} else {
		s.syncSoftPressureNoticeState(now, &usage)
		s.runSoftCompaction(now, recordRoot, &usage, stats)
		if err := usage.refresh(); err != nil {
			logutil.Warnf("storage cleanup refresh usage failed after soft compaction: root=%s err=%v", monitorRoot, err)
		}
		s.runHardCompaction(now, recordRoot, &usage, stats)
		if err := usage.refresh(); err != nil {
			logutil.Warnf("storage cleanup refresh usage failed after hard compaction: root=%s err=%v", monitorRoot, err)
		}
		s.runCriticalCompaction(now, recordRoot, &usage, stats)
	}

	after, afterErr := readDiskUsageSnapshot(monitorRoot)
	if afterErr != nil {
		logutil.Warnf("storage cleanup disk usage read failed after cleanup: root=%s err=%v", monitorRoot, afterErr)
	}

	beforeUsedPercent := -1.0
	beforeFreeGB := -1.0
	if beforeErr == nil {
		beforeUsedPercent = before.UsedPercent
		beforeFreeGB = before.freeGB()
	}
	afterUsedPercent := -1.0
	afterFreeGB := -1.0
	if afterErr == nil {
		afterUsedPercent = after.UsedPercent
		afterFreeGB = after.freeGB()
	}
	removedFiles, removedBytes := stats.totalRemoved()
	for _, line := range stats.detailLines() {
		logutil.Infof("storage cleanup detail: %s", line)
	}
	logutil.Infof(
		"storage cleanup summary: root=%s before_used=%.2f after_used=%.2f before_free_gb=%.2f after_free_gb=%.2f removed_files=%d removed_mb=%.2f by_category=%s",
		monitorRoot,
		beforeUsedPercent,
		afterUsedPercent,
		beforeFreeGB,
		afterFreeGB,
		removedFiles,
		bytesToMB(removedBytes),
		marshalJSONForLog(stats.byCategoryForLog()),
	)
	s.healBufferedAlarmRecordings()
}

func (s *Server) runRoutineCleanup(now time.Time, recordRoot string, stats *cleanupRunStats) {
	if s == nil || s.cfg == nil {
		return
	}

	alarmBufferRoot := strings.TrimSpace(s.cfg.Server.Recording.AlarmClip.BufferDir)
	bufferKeep := s.cfg.Server.Recording.AlarmClip.BufferKeepSeconds
	if bufferKeep <= 0 {
		bufferKeep = s.cfg.Server.Recording.AlarmClip.PreSeconds + s.cfg.Server.Recording.AlarmClip.PostSeconds + 60
	}
	if bufferKeep > 0 {
		protectedBefore, protectErr := s.loadActiveAlarmBufferProtectedBefore()
		if protectErr != nil {
			logutil.Warnf("storage cleanup skipped for alarm buffer dir: load active sessions failed: %v", protectErr)
		} else {
			cutoff := now.Add(-time.Duration(bufferKeep) * time.Second)
			removed, err := removeFilesOlderThanDetailed(
				alarmBufferRoot,
				cutoff,
				func(file cleanupFile) bool {
					return !isAlarmBufferFileProtected(file.RelPath, file.ModTime, protectedBefore)
				},
				nil,
			)
			if err != nil {
				logutil.Warnf("storage cleanup failed for alarm buffer dir: %v", err)
			} else {
				stats.addFiles("alarm_buffer", alarmBufferRoot, removed)
			}
		}
	}

	snapRoot := strings.TrimSpace(s.cfg.Server.Cleanup.ZLMSnapDir)
	if retainMinutes := s.cfg.Server.Cleanup.ZLMSnapRetainMinutes; retainMinutes > 0 {
		cutoff := now.Add(-time.Duration(retainMinutes) * time.Minute)
		removed, err := removeFilesOlderThanDetailed(snapRoot, cutoff, nil, nil)
		if err != nil {
			logutil.Warnf("storage cleanup failed for zlm snap dir: %v", err)
		} else {
			stats.addFiles("zlm_snap", snapRoot, removed)
		}
	}

	if aiRetainDays := s.cfg.Server.AI.RetainDays; aiRetainDays > 0 {
		// 批量算法测试会异步逐项消费同一批媒体，清理器必须避开 pending/running 项，否则后续项会读不到文件。
		protectedMedia, protectErr := s.loadActiveAlgorithmTestProtectedMedia()
		if protectErr != nil {
			logutil.Warnf("storage cleanup skipped for algorithm test images: load active job items failed: %v", protectErr)
		} else {
			cutoff := now.Add(-time.Duration(aiRetainDays) * 24 * time.Hour)
			removed, err := removeFilesOlderThanDetailed(
				algorithmTestMediaRootDir,
				cutoff,
				func(file cleanupFile) bool {
					return !isAlgorithmTestMediaProtected(file.RelPath, protectedMedia)
				},
				nil,
			)
			if err != nil {
				logutil.Warnf("storage cleanup failed for algorithm test images: %v", err)
			} else {
				stats.addFiles("test_media", algorithmTestMediaRootDir, removed)
				if err := s.clearAlgorithmTestMediaPaths(relPathsFromFiles(removed)); err != nil {
					logutil.Warnf("storage cleanup db sync failed for algorithm test images: %v", err)
				}
			}
		}
	}

	if retainDays := s.cfg.Server.Recording.RetainDays; retainDays > 0 {
		cutoff := now.Add(-time.Duration(retainDays) * 24 * time.Hour)
		removed, err := removeFilesOlderThanDetailed(
			recordRoot,
			cutoff,
			nil,
			func(relDir string) bool { return relHasSegment(relDir, alarmClipDirName) },
		)
		if err != nil {
			logutil.Warnf("storage cleanup failed for continuous recordings: %v", err)
		} else {
			stats.addFiles("recordings", recordRoot, removed)
			if err := s.pruneEventClipFilesByRemovedPaths(relPathsFromFiles(removed)); err != nil {
				logutil.Warnf("storage cleanup db sync failed for recordings: %v", err)
			}
		}
	}

}

func (s *Server) runSoftCompaction(now time.Time, recordRoot string, usage *diskUsageSnapshot, stats *cleanupRunStats) {
	if s == nil || s.cfg == nil || usage == nil || !usage.Valid {
		return
	}
	cfg := s.cfg.Server.Cleanup
	if !usage.isPressure(cfg.SoftWatermark, cfg.MinFreeGB) {
		return
	}
	target := cfg.SoftWatermark - 0.5
	if target < 0 {
		target = 0
	}

	logutil.Infof("storage compaction soft triggered: used=%.2f free_gb=%.2f target=%.2f", usage.UsedPercent, usage.freeGB(), target)

	if removed, err := removeOldestFilesDetailed(strings.TrimSpace(cfg.ZLMSnapDir), nil, nil, usage, target, cfg.MinFreeGB); err != nil {
		logutil.Warnf("storage compaction soft failed for zlm snap: %v", err)
	} else {
		stats.addFiles("compact_zlm_snap", strings.TrimSpace(cfg.ZLMSnapDir), removed)
	}

	protectedMedia, protectErr := s.loadActiveAlgorithmTestProtectedMedia()
	if protectErr != nil {
		logutil.Warnf("storage compaction soft skipped for test images: load active job items failed: %v", protectErr)
	} else {
		if removed, err := removeOldestFilesDetailed(
			algorithmTestMediaRootDir,
			func(file cleanupFile) bool {
				return !isAlgorithmTestMediaProtected(file.RelPath, protectedMedia)
			},
			nil,
			usage,
			target,
			cfg.MinFreeGB,
		); err != nil {
			logutil.Warnf("storage compaction soft failed for test images: %v", err)
		} else {
			stats.addFiles("compact_test_media", algorithmTestMediaRootDir, removed)
			if err := s.clearAlgorithmTestMediaPaths(relPathsFromFiles(removed)); err != nil {
				logutil.Warnf("storage compaction soft db sync failed for test images: %v", err)
			}
		}
	}

	referenced, refErr := s.loadReferencedCoverPaths()
	if refErr != nil {
		logutil.Warnf("storage compaction soft load cover references failed: %v", refErr)
	} else {
		if removed, err := removeOldestFilesDetailed(
			filepath.Join("configs", "cover"),
			func(file cleanupFile) bool {
				_, inUse := referenced[filepath.ToSlash(file.RelPath)]
				return !inUse
			},
			nil,
			usage,
			target,
			cfg.MinFreeGB,
		); err != nil {
			logutil.Warnf("storage compaction soft failed for orphan covers: %v", err)
		} else {
			stats.addFiles("compact_cover_orphan", filepath.Join("configs", "cover"), removed)
		}
	}

	snapshotRefs, snapshotRefErr := s.loadReferencedDeviceSnapshotPaths()
	if snapshotRefErr != nil {
		logutil.Warnf("storage compaction soft load device snapshot references failed: %v", snapshotRefErr)
	} else {
		if removed, err := removeOldestFilesDetailed(
			filepath.Join("configs", deviceSnapshotDirName),
			func(file cleanupFile) bool {
				_, inUse := snapshotRefs[filepath.ToSlash(file.RelPath)]
				return !inUse
			},
			nil,
			usage,
			target,
			cfg.MinFreeGB,
		); err != nil {
			logutil.Warnf("storage compaction soft failed for device snapshots: %v", err)
		} else {
			stats.addFiles("compact_device_snapshots", filepath.Join("configs", deviceSnapshotDirName), removed)
			if err := s.clearDeviceSnapshotURLsByRelPaths(relPathsFromFiles(removed)); err != nil {
				logutil.Warnf("storage compaction soft db sync failed for device snapshots: %v", err)
			}
		}
	}

	if removed, err := removeOldestFilesDetailed(
		recordRoot,
		nil,
		func(relDir string) bool { return relHasSegment(relDir, alarmClipDirName) },
		usage,
		target,
		cfg.MinFreeGB,
	); err != nil {
		logutil.Warnf("storage compaction soft failed for recordings: %v", err)
	} else {
		stats.addFiles("compact_recordings", recordRoot, removed)
		if err := s.pruneEventClipFilesByRemovedPaths(relPathsFromFiles(removed)); err != nil {
			logutil.Warnf("storage compaction soft db sync failed for recordings: %v", err)
		}
	}

	s.runRetentionEvidenceCleanup("soft", now, recordRoot, usage, target, cfg.MinFreeGB, stats)
}

func (s *Server) runHardCompaction(now time.Time, recordRoot string, usage *diskUsageSnapshot, stats *cleanupRunStats) {
	if s == nil || s.cfg == nil || usage == nil || !usage.Valid {
		return
	}
	cfg := s.cfg.Server.Cleanup
	if !usage.isPressure(cfg.HardWatermark, cfg.MinFreeGB) {
		return
	}
	target := cfg.HardWatermark - 0.5
	if target < 0 {
		target = 0
	}

	logutil.Infof("storage compaction hard triggered: used=%.2f free_gb=%.2f target=%.2f", usage.UsedPercent, usage.freeGB(), target)

	if removed, err := removeOldestFilesDetailed(
		recordRoot,
		nil,
		func(relDir string) bool { return relHasSegment(relDir, alarmClipDirName) },
		usage,
		target,
		cfg.MinFreeGB,
	); err != nil {
		logutil.Warnf("storage compaction hard failed for recordings: %v", err)
	} else {
		stats.addFiles("hard_recordings", recordRoot, removed)
		if err := s.pruneEventClipFilesByRemovedPaths(relPathsFromFiles(removed)); err != nil {
			logutil.Warnf("storage compaction hard db sync failed for recordings: %v", err)
		}
	}

	s.runRetentionEvidenceCleanup("hard", now, recordRoot, usage, target, cfg.MinFreeGB, stats)
}

func (s *Server) runCriticalCompaction(_ time.Time, recordRoot string, usage *diskUsageSnapshot, stats *cleanupRunStats) {
	if s == nil || s.cfg == nil || usage == nil || !usage.Valid {
		return
	}
	cfg := s.cfg.Server.Cleanup
	if !usage.isPressure(cfg.CriticalWatermark, cfg.MinFreeGB) {
		return
	}
	if !cfg.EmergencyBreakGlass {
		return
	}
	target := cfg.HardWatermark - 0.5
	if target < 0 {
		target = 0
	}

	logutil.Warnf(
		"storage compaction break_glass activated: used=%.2f free_gb=%.2f critical=%.2f min_free_gb=%.2f",
		usage.UsedPercent,
		usage.freeGB(),
		cfg.CriticalWatermark,
		cfg.MinFreeGB,
	)

	if removed, err := removeOldestFilesDetailed(
		recordRoot,
		nil,
		func(relDir string) bool { return relHasSegment(relDir, alarmClipDirName) },
		usage,
		target,
		cfg.MinFreeGB,
	); err != nil {
		logutil.Warnf("storage compaction break_glass failed for recordings: %v", err)
	} else {
		stats.addFiles("break_glass_recordings", recordRoot, removed)
		if err := s.pruneEventClipFilesByRemovedPaths(relPathsFromFiles(removed)); err != nil {
			logutil.Warnf("storage compaction break_glass db sync failed for recordings: %v", err)
		}
	}

	if removed, err := removeOldestFilesDetailed(filepath.Join("configs", "cover"), nil, nil, usage, target, cfg.MinFreeGB); err != nil {
		logutil.Warnf("storage compaction break_glass failed for algorithm covers: %v", err)
	} else {
		stats.addFiles("break_glass_covers", filepath.Join("configs", "cover"), removed)
		if err := s.clearAlgorithmCoverURLsByRelPaths(relPathsFromFiles(removed)); err != nil {
			logutil.Warnf("storage compaction break_glass db sync failed for algorithm covers: %v", err)
		}
	}

	if removed, err := removeOldestFilesDetailed(filepath.Join("configs", deviceSnapshotDirName), nil, nil, usage, target, cfg.MinFreeGB); err != nil {
		logutil.Warnf("storage compaction break_glass failed for device snapshots: %v", err)
	} else {
		stats.addFiles("break_glass_device_snapshots", filepath.Join("configs", deviceSnapshotDirName), removed)
		if err := s.clearDeviceSnapshotURLsByRelPaths(relPathsFromFiles(removed)); err != nil {
			logutil.Warnf("storage compaction break_glass db sync failed for device snapshots: %v", err)
		}
	}

	removedBuckets, err := s.removeCriticalAlarmEvidenceByHour(recordRoot, usage, target, cfg.MinFreeGB)
	if len(removedBuckets) > 0 {
		stats.addFiles("break_glass_event_snapshots", filepath.Join("configs", "events"), criticalAlarmEvidenceSnapshotFiles(removedBuckets))
		stats.addDirs("break_glass_alarm_clips", recordRoot, criticalAlarmEvidenceClipDirs(removedBuckets))
	}
	if err != nil {
		logutil.Warnf("storage compaction break_glass failed for alarm evidence buckets: %v", err)
	}
}

func (s *Server) removeAlarmClipDirsByPolicy(
	recordRoot string,
	cutoff *time.Time,
	usage *diskUsageSnapshot,
	targetPercent float64,
	minFreeGB float64,
) ([]alarmClipEventDir, error) {
	dirs, err := collectAlarmClipEventDirs(recordRoot)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		return nil, nil
	}

	removed := make([]alarmClipEventDir, 0, len(dirs))
	for _, item := range dirs {
		if cutoff != nil && item.ModTime.After(*cutoff) {
			continue
		}
		if usage != nil && usage.reachedTarget(targetPercent, minFreeGB) {
			break
		}
		if err := os.RemoveAll(item.AbsPath); err != nil {
			continue
		}
		removed = append(removed, item)
		if usage != nil {
			usage.subtract(item.Size)
		}
	}
	_ = cleanupEmptyDirs(recordRoot)
	return removed, nil
}

func (s *Server) removeCriticalAlarmEvidenceByHour(
	recordRoot string,
	usage *diskUsageSnapshot,
	targetPercent float64,
	minFreeGB float64,
) ([]criticalAlarmEvidenceBucket, error) {
	buckets, err := s.collectCriticalAlarmEvidenceBuckets(recordRoot)
	if err != nil || len(buckets) == 0 {
		return nil, err
	}

	removedBuckets := make([]criticalAlarmEvidenceBucket, 0, len(buckets))
	for _, bucket := range buckets {
		if usage != nil && usage.reachedTarget(targetPercent, minFreeGB) {
			break
		}

		removed := criticalAlarmEvidenceBucket{Hour: bucket.Hour}
		for _, item := range bucket.SnapshotFiles {
			if err := os.Remove(item.Path); err != nil {
				continue
			}
			removed.SnapshotFiles = append(removed.SnapshotFiles, item)
			removed.EventIDs = append(removed.EventIDs, bucket.EventIDs...)
			removed.TotalSize += item.Size
			if usage != nil {
				usage.subtract(item.Size)
			}
		}
		for _, item := range bucket.ClipDirs {
			if err := os.RemoveAll(item.AbsPath); err != nil {
				continue
			}
			removed.ClipDirs = append(removed.ClipDirs, item)
			removed.EventIDs = append(removed.EventIDs, bucket.EventIDs...)
			removed.TotalSize += item.Size
			if usage != nil {
				usage.subtract(item.Size)
			}
		}
		removed.EventIDs = uniqueStrings(removed.EventIDs)
		if len(removed.SnapshotFiles) == 0 && len(removed.ClipDirs) == 0 {
			continue
		}
		if err := s.clearEventSnapshotPaths(relPathsFromFiles(removed.SnapshotFiles)); err != nil {
			return removedBuckets, err
		}
		if err := s.clearEventClipFieldsByRemovedDirs(removed.ClipDirs); err != nil {
			return removedBuckets, err
		}
		logutil.Warnf(
			"storage compaction break_glass removed alarm evidence hour_bucket=%s snapshots=%d clip_dirs=%d removed_mb=%.2f events=%d",
			removed.Hour.Format("2006-01-02 15:00:00"),
			len(removed.SnapshotFiles),
			len(removed.ClipDirs),
			bytesToMB(removed.TotalSize),
			len(removed.EventIDs),
		)
		removedBuckets = append(removedBuckets, removed)
	}
	_ = cleanupEmptyDirs(filepath.Join("configs", "events"))
	_ = cleanupEmptyDirs(recordRoot)
	return removedBuckets, nil
}

func (s *Server) collectCriticalAlarmEvidenceBuckets(recordRoot string) ([]criticalAlarmEvidenceBucket, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}

	type alarmEvidenceRow struct {
		ID            string
		DeviceID      string
		OccurredAt    time.Time
		SnapshotPath  string
		ClipPath      string
		ClipFilesJSON string
	}
	rows := make([]alarmEvidenceRow, 0, 64)
	if err := s.db.Model(&model.AlarmEvent{}).
		Select("id", "device_id", "occurred_at", "snapshot_path", "clip_path", "clip_files_json").
		Where("snapshot_path <> '' OR clip_path <> '' OR (clip_files_json <> '' AND clip_files_json <> '[]')").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	type snapshotRef struct {
		EventIDs []string
		Hour     time.Time
	}
	type clipDirRef struct {
		EventIDs []string
		Hour     time.Time
	}
	snapshotRefs := make(map[string]snapshotRef, len(rows))
	clipDirRefs := make(map[string]clipDirRef, len(rows))
	for _, row := range rows {
		hour := row.OccurredAt
		if hour.IsZero() {
			hour = time.Now()
		}
		hour = hour.Truncate(time.Hour)

		if snapshotRel := normalizeSingleCleanupRelPath(row.SnapshotPath); snapshotRel != "" {
			ref := snapshotRefs[snapshotRel]
			if ref.Hour.IsZero() || hour.After(ref.Hour) {
				ref.Hour = hour
			}
			ref.EventIDs = append(ref.EventIDs, strings.TrimSpace(row.ID))
			snapshotRefs[snapshotRel] = ref
		}

		for _, clipDirRel := range eventClipDirRelPaths(row.ClipPath, row.ClipFilesJSON) {
			fullRel := normalizeSingleCleanupRelPath(filepath.Join(strings.TrimSpace(row.DeviceID), filepath.FromSlash(clipDirRel)))
			if fullRel == "" {
				continue
			}
			ref := clipDirRefs[fullRel]
			if ref.Hour.IsZero() || hour.After(ref.Hour) {
				ref.Hour = hour
			}
			ref.EventIDs = append(ref.EventIDs, strings.TrimSpace(row.ID))
			clipDirRefs[fullRel] = ref
		}
	}

	bucketsByHour := make(map[int64]*criticalAlarmEvidenceBucket, len(rows))
	for relPath, ref := range snapshotRefs {
		item, ok := existingSnapshotCleanupFile(relPath)
		if !ok {
			continue
		}
		bucket := ensureCriticalAlarmEvidenceBucket(bucketsByHour, ref.Hour)
		bucket.SnapshotFiles = append(bucket.SnapshotFiles, item)
		bucket.EventIDs = append(bucket.EventIDs, ref.EventIDs...)
		bucket.TotalSize += item.Size
	}
	for relPath, ref := range clipDirRefs {
		item, ok := existingAlarmClipEventDir(recordRoot, relPath)
		if !ok {
			continue
		}
		bucket := ensureCriticalAlarmEvidenceBucket(bucketsByHour, ref.Hour)
		bucket.ClipDirs = append(bucket.ClipDirs, item)
		bucket.EventIDs = append(bucket.EventIDs, ref.EventIDs...)
		bucket.TotalSize += item.Size
	}

	if len(bucketsByHour) == 0 {
		return nil, nil
	}

	buckets := make([]criticalAlarmEvidenceBucket, 0, len(bucketsByHour))
	for _, bucket := range bucketsByHour {
		if bucket == nil {
			continue
		}
		bucket.EventIDs = uniqueStrings(bucket.EventIDs)
		sort.Slice(bucket.SnapshotFiles, func(i, j int) bool {
			if bucket.SnapshotFiles[i].ModTime.Equal(bucket.SnapshotFiles[j].ModTime) {
				return bucket.SnapshotFiles[i].RelPath < bucket.SnapshotFiles[j].RelPath
			}
			return bucket.SnapshotFiles[i].ModTime.Before(bucket.SnapshotFiles[j].ModTime)
		})
		sort.Slice(bucket.ClipDirs, func(i, j int) bool {
			if bucket.ClipDirs[i].ModTime.Equal(bucket.ClipDirs[j].ModTime) {
				return bucket.ClipDirs[i].RelPath < bucket.ClipDirs[j].RelPath
			}
			return bucket.ClipDirs[i].ModTime.Before(bucket.ClipDirs[j].ModTime)
		})
		buckets = append(buckets, *bucket)
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Hour.Equal(buckets[j].Hour) {
			return criticalAlarmEvidenceBucketSortKey(buckets[i]) < criticalAlarmEvidenceBucketSortKey(buckets[j])
		}
		return buckets[i].Hour.Before(buckets[j].Hour)
	})
	return buckets, nil
}

func collectAlarmClipEventDirs(recordRoot string) ([]alarmClipEventDir, error) {
	recordRoot = strings.TrimSpace(recordRoot)
	if recordRoot == "" {
		return nil, nil
	}
	info, err := os.Stat(recordRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", recordRoot)
	}

	items := make([]alarmClipEventDir, 0, 32)
	walkErr := filepath.WalkDir(recordRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(recordRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "." || rel == "" {
			return nil
		}
		deviceID, eventID, isEventDir := parseAlarmClipEventDirRel(rel)
		if !isEventDir {
			return nil
		}
		size, files, modTime, err := dirUsage(path)
		if err != nil {
			return nil
		}
		items = append(items, alarmClipEventDir{
			AbsPath:  path,
			RelPath:  rel,
			DeviceID: deviceID,
			EventID:  eventID,
			ModTime:  modTime,
			Size:     size,
			Files:    files,
		})
		return filepath.SkipDir
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(items) == 0 {
		return nil, nil
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ModTime.Equal(items[j].ModTime) {
			return items[i].RelPath < items[j].RelPath
		}
		return items[i].ModTime.Before(items[j].ModTime)
	})
	return items, nil
}

func existingSnapshotCleanupFile(relPath string) (cleanupFile, bool) {
	relPath = normalizeSingleCleanupRelPath(relPath)
	if relPath == "" {
		return cleanupFile{}, false
	}
	absPath := filepath.Join("configs", "events", filepath.FromSlash(relPath))
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		return cleanupFile{}, false
	}
	size := uint64(0)
	if info.Size() > 0 {
		size = uint64(info.Size())
	}
	return cleanupFile{
		Path:    absPath,
		RelPath: relPath,
		ModTime: info.ModTime(),
		Size:    size,
	}, true
}

func existingAlarmClipEventDir(recordRoot string, relPath string) (alarmClipEventDir, bool) {
	relPath = normalizeSingleCleanupRelPath(relPath)
	if relPath == "" {
		return alarmClipEventDir{}, false
	}
	deviceID, eventID, isEventDir := parseAlarmClipEventDirRel(relPath)
	if !isEventDir {
		return alarmClipEventDir{}, false
	}
	absPath := filepath.Join(recordRoot, filepath.FromSlash(relPath))
	size, files, modTime, err := dirUsage(absPath)
	if err != nil {
		return alarmClipEventDir{}, false
	}
	return alarmClipEventDir{
		AbsPath:  absPath,
		RelPath:  relPath,
		DeviceID: deviceID,
		EventID:  eventID,
		ModTime:  modTime,
		Size:     size,
		Files:    files,
	}, true
}

func ensureCriticalAlarmEvidenceBucket(buckets map[int64]*criticalAlarmEvidenceBucket, hour time.Time) *criticalAlarmEvidenceBucket {
	if hour.IsZero() {
		hour = time.Now().Truncate(time.Hour)
	}
	key := hour.Unix()
	bucket, ok := buckets[key]
	if !ok {
		bucket = &criticalAlarmEvidenceBucket{Hour: hour}
		buckets[key] = bucket
	}
	return bucket
}

func criticalAlarmEvidenceBucketSortKey(bucket criticalAlarmEvidenceBucket) string {
	if len(bucket.SnapshotFiles) > 0 {
		return bucket.SnapshotFiles[0].RelPath
	}
	if len(bucket.ClipDirs) > 0 {
		return bucket.ClipDirs[0].RelPath
	}
	return ""
}

func criticalAlarmEvidenceSnapshotFiles(buckets []criticalAlarmEvidenceBucket) []cleanupFile {
	if len(buckets) == 0 {
		return nil
	}
	files := make([]cleanupFile, 0, len(buckets))
	for _, bucket := range buckets {
		files = append(files, bucket.SnapshotFiles...)
	}
	return files
}

func criticalAlarmEvidenceClipDirs(buckets []criticalAlarmEvidenceBucket) []alarmClipEventDir {
	if len(buckets) == 0 {
		return nil
	}
	dirs := make([]alarmClipEventDir, 0, len(buckets))
	for _, bucket := range buckets {
		dirs = append(dirs, bucket.ClipDirs...)
	}
	return dirs
}

func eventClipDirRelPaths(clipPath string, clipFilesJSON string) []string {
	paths := make([]string, 0, 4)
	if clipPath = normalizeSingleCleanupRelPath(clipPath); clipPath != "" {
		paths = append(paths, clipPath)
	}
	for _, filePath := range decodeEventClipFiles(clipFilesJSON) {
		dirPath := normalizeSingleCleanupRelPath(filepath.Dir(filePath))
		if dirPath == "" {
			continue
		}
		paths = append(paths, dirPath)
	}
	return normalizeCleanupRelPaths(paths)
}

func normalizeSingleCleanupRelPath(path string) string {
	normalized := normalizeCleanupRelPaths([]string{path})
	if len(normalized) == 0 {
		return ""
	}
	return normalized[0]
}

func parseAlarmClipEventDirRel(rel string) (string, string, bool) {
	rel = filepath.ToSlash(strings.Trim(rel, "/"))
	if rel == "" {
		return "", "", false
	}
	parts := strings.Split(rel, "/")
	for idx, part := range parts {
		if part != alarmClipDirName {
			continue
		}
		if idx == 0 || idx+1 >= len(parts) {
			return "", "", false
		}
		if idx+2 != len(parts) {
			return "", "", false
		}
		return parts[idx-1], parts[idx+1], true
	}
	return "", "", false
}

func dirUsage(root string) (uint64, int, time.Time, error) {
	info, err := os.Stat(root)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	if !info.IsDir() {
		return 0, 0, info.ModTime(), nil
	}
	latest := info.ModTime()
	var size uint64
	files := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		fileInfo, err := d.Info()
		if err != nil {
			return nil
		}
		if fileInfo.ModTime().After(latest) {
			latest = fileInfo.ModTime()
		}
		if fileInfo.Size() > 0 {
			size += uint64(fileInfo.Size())
		}
		files++
		return nil
	})
	return size, files, latest, nil
}

func (s *Server) cleanupAlarmClipRetainDays() int {
	if s == nil || s.cfg == nil {
		return 7
	}
	days := s.cfg.Server.Cleanup.AlarmClipRetainDays
	if days <= 0 {
		days = 7
	}
	return days
}

func (s *Server) eventSnapshotRetainDays() int {
	if s == nil || s.cfg == nil {
		return 7
	}
	days := s.cfg.Server.AI.RetainDays
	if days <= 0 {
		days = 7
	}
	clipDays := s.cleanupAlarmClipRetainDays()
	if clipDays > days {
		days = clipDays
	}
	return days
}

func (s *Server) syncSoftPressureNoticeState(now time.Time, usage *diskUsageSnapshot) {
	if s == nil || s.cfg == nil || usage == nil || !usage.Valid {
		return
	}
	cfg := s.cfg.Server.Cleanup
	inSoftPressure := usage.isPressure(cfg.SoftWatermark, cfg.MinFreeGB)

	current, err := s.loadCleanupSoftPressureNoticeState()
	if err != nil {
		logutil.Warnf("storage compaction soft load pressure notice failed, reset state: %v", err)
		_ = s.clearCleanupSoftPressureNoticeState()
		current = nil
	}

	if !inSoftPressure {
		if current != nil {
			if clearErr := s.clearCleanupSoftPressureNoticeState(); clearErr != nil {
				logutil.Warnf("storage compaction soft clear pressure notice failed: %v", clearErr)
			}
		}
		return
	}

	if current != nil {
		return
	}

	notice := buildCleanupSoftPressureNoticeState(now, usage.UsedPercent, usage.freeGB(), cfg.SoftWatermark)
	if err := s.saveCleanupSoftPressureNoticeState(notice); err != nil {
		logutil.Warnf("storage compaction soft create pressure notice failed: %v", err)
		return
	}
	s.broadcastCleanupSoftPressureNotice(notice)
	logutil.Infof(
		"storage compaction soft pressure notice issued: notice_id=%s used=%.2f free_gb=%.2f soft_watermark=%.2f",
		notice.NoticeID,
		notice.UsedPercent,
		notice.FreeGB,
		notice.SoftWatermark,
	)
}

func (s *Server) runRetentionEvidenceCleanup(
	stage string,
	now time.Time,
	recordRoot string,
	usage *diskUsageSnapshot,
	targetPercent float64,
	minFreeGB float64,
	stats *cleanupRunStats,
) {
	stage = strings.ToLower(strings.TrimSpace(stage))
	if stage == "" {
		stage = "soft"
	}

	eventCandidates, clipCandidates, err := s.collectRetentionEvidenceCandidates(now, recordRoot)
	if err != nil {
		logutil.Warnf("storage compaction %s collect retention evidence candidates failed: %v", stage, err)
		return
	}
	if len(eventCandidates) == 0 && len(clipCandidates) == 0 {
		if err := s.clearCleanupRetentionNoticeState(); err != nil {
			logutil.Warnf("storage compaction %s clear retention notice failed: %v", stage, err)
		}
		return
	}
	fingerprint := buildCleanupRetentionCandidateFingerprint(eventCandidates, clipCandidates)

	if stage == "soft" {
		hardReached := false
		if s != nil && s.cfg != nil && usage != nil && usage.Valid {
			cfg := s.cfg.Server.Cleanup
			hardReached = usage.isPressure(cfg.HardWatermark, cfg.MinFreeGB)
		}
		notice, loadErr := s.loadCleanupRetentionNoticeState()
		if loadErr != nil {
			logutil.Warnf("storage compaction %s load retention notice failed, reset notice: %v", stage, loadErr)
			_ = s.clearCleanupRetentionNoticeState()
			notice = nil
		}
		if notice != nil && notice.CandidateFingerprint == fingerprint {
			if notice.HardReached != hardReached {
				notice.HardReached = hardReached
				if saveErr := s.saveCleanupRetentionNoticeState(notice); saveErr != nil {
					logutil.Warnf("storage compaction %s update retention notice hard flag failed: %v", stage, saveErr)
				}
			}
			return
		}
		notice = buildCleanupRetentionNoticeState(now, len(eventCandidates), len(clipCandidates), fingerprint, hardReached)
		if saveErr := s.saveCleanupRetentionNoticeState(notice); saveErr != nil {
			logutil.Warnf("storage compaction %s create retention notice failed: %v", stage, saveErr)
			return
		}
		s.broadcastCleanupRetentionNotice(notice)
		logutil.Infof(
			"storage compaction %s retention notice issued: notice_id=%s event_snapshots=%d alarm_clips=%d hard_reached=%t",
			stage,
			notice.NoticeID,
			notice.EventSnapshotCount,
			notice.AlarmClipCount,
			notice.HardReached,
		)
		return
	}

	if stage != "hard" {
		return
	}

	eventCutoff := now.Add(-time.Duration(s.eventSnapshotRetainDays()) * 24 * time.Hour)
	removedSnapshots, err := removeOldestFilesDetailed(
		filepath.Join("configs", "events"),
		func(file cleanupFile) bool { return !file.ModTime.After(eventCutoff) },
		nil,
		usage,
		targetPercent,
		minFreeGB,
	)
	if err != nil {
		logutil.Warnf("storage compaction %s retention cleanup failed for event snapshots: %v", stage, err)
		return
	}
	if len(removedSnapshots) > 0 {
		stats.addFiles(fmt.Sprintf("%s_event_snapshots_retained", stage), filepath.Join("configs", "events"), removedSnapshots)
		if err := s.clearEventSnapshotPaths(relPathsFromFiles(removedSnapshots)); err != nil {
			logutil.Warnf("storage compaction %s retention cleanup db sync failed for event snapshots: %v", stage, err)
		}
	}

	clipCutoff := now.Add(-time.Duration(s.cleanupAlarmClipRetainDays()) * 24 * time.Hour)
	removedClipDirs, err := s.removeAlarmClipDirsByPolicy(recordRoot, &clipCutoff, usage, targetPercent, minFreeGB)
	if err != nil {
		logutil.Warnf("storage compaction %s retention cleanup failed for alarm clip dirs: %v", stage, err)
		return
	}
	if len(removedClipDirs) > 0 {
		stats.addDirs(fmt.Sprintf("%s_alarm_clips_retained", stage), recordRoot, removedClipDirs)
		if err := s.clearEventClipFieldsByRemovedDirs(removedClipDirs); err != nil {
			logutil.Warnf("storage compaction %s retention cleanup db sync failed for alarm clip dirs: %v", stage, err)
		}
	}

	if err := s.clearCleanupRetentionNoticeState(); err != nil {
		logutil.Warnf("storage compaction %s clear retention notice failed after cleanup: %v", stage, err)
	}
}

func (s *Server) collectRetentionEvidenceCandidates(now time.Time, recordRoot string) ([]cleanupFile, []alarmClipEventDir, error) {
	eventCutoff := now.Add(-time.Duration(s.eventSnapshotRetainDays()) * 24 * time.Hour)
	eventCandidates, err := collectFilesFiltered(
		filepath.Join("configs", "events"),
		func(file cleanupFile) bool {
			return !file.ModTime.After(eventCutoff)
		},
		nil,
	)
	if err != nil {
		return nil, nil, err
	}

	clipCutoff := now.Add(-time.Duration(s.cleanupAlarmClipRetainDays()) * 24 * time.Hour)
	clipDirs, err := collectAlarmClipEventDirs(recordRoot)
	if err != nil {
		return nil, nil, err
	}
	if len(clipDirs) == 0 {
		return eventCandidates, nil, nil
	}
	candidates := make([]alarmClipEventDir, 0, len(clipDirs))
	for _, item := range clipDirs {
		if item.ModTime.After(clipCutoff) {
			continue
		}
		candidates = append(candidates, item)
	}
	return eventCandidates, candidates, nil
}

func buildCleanupSoftPressureNoticeState(now time.Time, usedPercent, freeGB, softWatermark float64) *cleanupSoftPressureNoticeState {
	if now.IsZero() {
		now = time.Now()
	}
	return &cleanupSoftPressureNoticeState{
		NoticeID:      fmt.Sprintf("cleanup-soft-pressure-%d", now.UnixMilli()),
		IssuedAt:      now,
		UsedPercent:   usedPercent,
		FreeGB:        freeGB,
		SoftWatermark: softWatermark,
	}
}

func (s *Server) loadCleanupSoftPressureNoticeState() (*cleanupSoftPressureNoticeState, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	raw := strings.TrimSpace(s.getSetting(cleanupSoftPressureNoticeKey))
	if raw == "" {
		return nil, nil
	}
	var notice cleanupSoftPressureNoticeState
	if err := json.Unmarshal([]byte(raw), &notice); err != nil {
		return nil, err
	}
	notice.NoticeID = strings.TrimSpace(notice.NoticeID)
	if notice.NoticeID == "" {
		return nil, fmt.Errorf("cleanup soft pressure notice id is empty")
	}
	if notice.IssuedAt.IsZero() {
		return nil, fmt.Errorf("cleanup soft pressure notice issued_at is invalid")
	}
	return &notice, nil
}

func (s *Server) saveCleanupSoftPressureNoticeState(notice *cleanupSoftPressureNoticeState) error {
	if s == nil || s.db == nil {
		return nil
	}
	if notice == nil {
		return s.upsertSetting(cleanupSoftPressureNoticeKey, "")
	}
	body, err := json.Marshal(notice)
	if err != nil {
		return err
	}
	return s.upsertSetting(cleanupSoftPressureNoticeKey, string(body))
}

func (s *Server) clearCleanupSoftPressureNoticeState() error {
	return s.saveCleanupSoftPressureNoticeState(nil)
}

func (s *Server) pendingCleanupSoftPressureNotice(now time.Time) (*cleanupSoftPressureNoticeState, error) {
	if now.IsZero() {
		now = time.Now()
	}
	notice, err := s.loadCleanupSoftPressureNoticeState()
	if err != nil || notice == nil {
		return nil, err
	}
	return notice, nil
}

func (s *Server) broadcastCleanupSoftPressureNotice(notice *cleanupSoftPressureNoticeState) {
	if s == nil || s.wsHub == nil || notice == nil {
		return
	}
	s.wsHub.Broadcast(s.cleanupSoftPressureNoticePayload(notice))
}

func (s *Server) cleanupSoftPressureNoticePayload(notice *cleanupSoftPressureNoticeState) map[string]any {
	if notice == nil {
		return map[string]any{}
	}
	return map[string]any{
		"type":           "storage_cleanup_notice",
		"notice_kind":    "soft_pressure",
		"notice_id":      notice.NoticeID,
		"issued_at":      notice.IssuedAt,
		"used_percent":   notice.UsedPercent,
		"free_gb":        notice.FreeGB,
		"soft_watermark": notice.SoftWatermark,
		"title":          "\u5b58\u50a8\u538b\u529b\u63d0\u9192",
		"message":        "\u78c1\u76d8\u5df2\u8fdb\u5165 Soft \u538b\u529b\u9636\u6bb5\uff0c\u8bf7\u4f18\u5148\u5904\u7406\u4e8b\u4ef6\u5e76\u5bfc\u51fa\u62a5\u8b66\u7247\u6bb5\u3002",
	}
}

func buildCleanupRetentionCandidateFingerprint(eventCandidates []cleanupFile, clipCandidates []alarmClipEventDir) string {
	hasher := sha1.New()
	for _, item := range eventCandidates {
		_, _ = hasher.Write([]byte("event|"))
		_, _ = hasher.Write([]byte(filepath.ToSlash(strings.TrimSpace(item.RelPath))))
		_, _ = hasher.Write([]byte("|"))
		_, _ = hasher.Write([]byte(strconv.FormatInt(item.ModTime.Unix(), 10)))
		_, _ = hasher.Write([]byte("\n"))
	}
	for _, item := range clipCandidates {
		_, _ = hasher.Write([]byte("clip|"))
		_, _ = hasher.Write([]byte(filepath.ToSlash(strings.TrimSpace(item.RelPath))))
		_, _ = hasher.Write([]byte("|"))
		_, _ = hasher.Write([]byte(strconv.FormatInt(item.ModTime.Unix(), 10)))
		_, _ = hasher.Write([]byte("|"))
		_, _ = hasher.Write([]byte(strconv.Itoa(item.Files)))
		_, _ = hasher.Write([]byte("|"))
		_, _ = hasher.Write([]byte(strconv.FormatUint(item.Size, 10)))
		_, _ = hasher.Write([]byte("\n"))
	}
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func buildCleanupRetentionNoticeState(
	now time.Time,
	eventSnapshotCount int,
	alarmClipCount int,
	fingerprint string,
	hardReached bool,
) *cleanupRetentionNoticeState {
	if now.IsZero() {
		now = time.Now()
	}
	if eventSnapshotCount < 0 {
		eventSnapshotCount = 0
	}
	if alarmClipCount < 0 {
		alarmClipCount = 0
	}
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		fingerprint = fmt.Sprintf("fallback-%d-%d", eventSnapshotCount, alarmClipCount)
	}
	return &cleanupRetentionNoticeState{
		NoticeID:             fmt.Sprintf("cleanup-retention-%d", now.UnixMilli()),
		IssuedAt:             now,
		EventSnapshotCount:   eventSnapshotCount,
		AlarmClipCount:       alarmClipCount,
		CandidateFingerprint: fingerprint,
		HardReached:          hardReached,
	}
}

func (s *Server) loadCleanupRetentionNoticeState() (*cleanupRetentionNoticeState, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	raw := strings.TrimSpace(s.getSetting(cleanupRetentionNoticeSettingKey))
	if raw == "" {
		return nil, nil
	}
	var notice cleanupRetentionNoticeState
	if err := json.Unmarshal([]byte(raw), &notice); err != nil {
		return nil, err
	}
	notice.NoticeID = strings.TrimSpace(notice.NoticeID)
	if notice.NoticeID == "" {
		return nil, fmt.Errorf("cleanup retention notice id is empty")
	}
	if notice.IssuedAt.IsZero() {
		return nil, fmt.Errorf("cleanup retention notice issued_at is invalid")
	}
	notice.CandidateFingerprint = strings.TrimSpace(notice.CandidateFingerprint)
	if notice.CandidateFingerprint == "" {
		return nil, fmt.Errorf("cleanup retention notice fingerprint is empty")
	}
	if notice.EventSnapshotCount < 0 {
		notice.EventSnapshotCount = 0
	}
	if notice.AlarmClipCount < 0 {
		notice.AlarmClipCount = 0
	}
	return &notice, nil
}

func (s *Server) saveCleanupRetentionNoticeState(notice *cleanupRetentionNoticeState) error {
	if s == nil || s.db == nil {
		return nil
	}
	if notice == nil {
		return s.upsertSetting(cleanupRetentionNoticeSettingKey, "")
	}
	body, err := json.Marshal(notice)
	if err != nil {
		return err
	}
	return s.upsertSetting(cleanupRetentionNoticeSettingKey, string(body))
}

func (s *Server) clearCleanupRetentionNoticeState() error {
	return s.saveCleanupRetentionNoticeState(nil)
}

func (s *Server) pendingCleanupRetentionNotice(now time.Time) (*cleanupRetentionNoticeState, error) {
	if now.IsZero() {
		now = time.Now()
	}
	notice, err := s.loadCleanupRetentionNoticeState()
	if err != nil || notice == nil {
		return nil, err
	}
	return notice, nil
}

func (s *Server) broadcastCleanupRetentionNotice(notice *cleanupRetentionNoticeState) {
	if s == nil || s.wsHub == nil || notice == nil {
		return
	}
	s.wsHub.Broadcast(s.cleanupRetentionNoticePayload(notice))
}

func (s *Server) cleanupRetentionNoticePayload(notice *cleanupRetentionNoticeState) map[string]any {
	if notice == nil {
		return map[string]any{}
	}
	message := "\u5b58\u50a8\u5df2\u8fdb\u5165 Soft \u9636\u6bb5\uff0c\u68c0\u6d4b\u5230\u8d85\u4fdd\u7559\u671f\u4e8b\u4ef6\u5feb\u7167\u548c\u62a5\u8b66\u7247\u6bb5\u76ee\u5f55\uff0c\u8bf7\u5148\u5904\u7406\u4e8b\u4ef6\u5e76\u5bfc\u51fa\u62a5\u8b66\u7247\u6bb5\uff1b\u82e5\u8fdb\u5165 Hard \u9636\u6bb5\u5c06\u81ea\u52a8\u6e05\u7406\u3002"
	if notice.HardReached {
		message = "\u5b58\u50a8\u5df2\u8fdb\u5165 Hard \u9636\u6bb5\uff0c\u672c\u8f6e\u5c06\u81ea\u52a8\u6e05\u7406\u8d85\u4fdd\u7559\u671f\u4e8b\u4ef6\u5feb\u7167\u548c\u62a5\u8b66\u7247\u6bb5\u76ee\u5f55\uff0c\u8bf7\u5c3d\u5feb\u5904\u7406\u4e8b\u4ef6\u5e76\u5bfc\u51fa\u62a5\u8b66\u7247\u6bb5\u3002"
	}
	return map[string]any{
		"type":                 "storage_cleanup_notice",
		"notice_kind":          "retention_risk",
		"notice_id":            notice.NoticeID,
		"issued_at":            notice.IssuedAt,
		"event_snapshot_count": notice.EventSnapshotCount,
		"alarm_clip_count":     notice.AlarmClipCount,
		"hard_reached":         notice.HardReached,
		"title":                "\u5b58\u50a8\u6e05\u7406\u63d0\u9192",
		"message":              message,
	}
}
func (s *Server) loadActiveAlarmBufferProtectedBefore() (map[string]time.Time, error) {
	if s == nil || s.db == nil {
		return map[string]time.Time{}, nil
	}
	var sessions []model.AlarmClipSession
	if err := s.db.
		Select("source_id", "started_at", "pre_seconds").
		Where("status IN ?", []string{alarmClipSessionStatusRecording, alarmClipSessionStatusClosing}).
		Find(&sessions).Error; err != nil {
		return nil, err
	}
	protected := make(map[string]time.Time, len(sessions))
	segmentTolerance := time.Duration(s.alarmClipBufferSegmentSeconds()+2) * time.Second
	for _, session := range sessions {
		sourceID := strings.TrimSpace(session.SourceID)
		if sourceID == "" || session.StartedAt.IsZero() {
			continue
		}
		preSeconds := clampInt(session.PreSeconds, 1, 600)
		protectBefore := session.StartedAt.Add(-time.Duration(preSeconds) * time.Second).Add(-segmentTolerance)
		if current, ok := protected[sourceID]; !ok || protectBefore.Before(current) {
			protected[sourceID] = protectBefore
		}
	}
	return protected, nil
}

func (s *Server) loadActiveAlgorithmTestProtectedMedia() (map[string]struct{}, error) {
	if s == nil || s.db == nil {
		return map[string]struct{}{}, nil
	}
	var items []model.AlgorithmTestJobItem
	if err := s.db.
		Select("media_path").
		Where("status IN ? AND media_path <> ''", []string{model.AlgorithmTestJobItemStatusPending, model.AlgorithmTestJobItemStatusRunning}).
		Find(&items).Error; err != nil {
		return nil, err
	}
	protected := make(map[string]struct{}, len(items))
	for _, item := range items {
		relPath := normalizeAlgorithmTestMediaRelPath(item.MediaPath)
		if relPath == "" {
			continue
		}
		protected[relPath] = struct{}{}
	}
	return protected, nil
}

func isAlgorithmTestMediaProtected(relPath string, protected map[string]struct{}) bool {
	if len(protected) == 0 {
		return false
	}
	relPath = normalizeAlgorithmTestMediaRelPath(relPath)
	if relPath == "" {
		return false
	}
	_, ok := protected[relPath]
	return ok
}

func sourceIDFromAlarmBufferRelPath(relPath string) string {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	relPath = strings.Trim(relPath, "/")
	if relPath == "" || relPath == "." {
		return ""
	}
	parts := strings.Split(relPath, "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func isAlarmBufferFileProtected(relPath string, modTime time.Time, protectedBefore map[string]time.Time) bool {
	sourceID := sourceIDFromAlarmBufferRelPath(relPath)
	return isAlarmBufferFileProtectedForSource(sourceID, modTime, protectedBefore)
}

func isAlarmBufferFileProtectedForSource(sourceID string, modTime time.Time, protectedBefore map[string]time.Time) bool {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || len(protectedBefore) == 0 {
		return false
	}
	protectBefore, ok := protectedBefore[sourceID]
	if !ok {
		return false
	}
	if modTime.IsZero() {
		return true
	}
	return !modTime.Before(protectBefore)
}

func relPathsFromFiles(items []cleanupFile) []string {
	if len(items) == 0 {
		return nil
	}
	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.RelPath)
	}
	return normalizeCleanupRelPaths(paths)
}

func eventIDsFromClipDirs(items []alarmClipEventDir) []string {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, strings.TrimSpace(item.EventID))
	}
	return uniqueStrings(ids)
}

func deviceIDsFromSnapshotFiles(items []cleanupFile) []string {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if deviceID := deviceIDFromSnapshotFile(item.RelPath); deviceID != "" {
			ids = append(ids, deviceID)
		}
	}
	return uniqueStrings(ids)
}

func deviceIDFromSnapshotFile(relPath string) string {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" || relPath == "." {
		return ""
	}
	name := filepath.Base(relPath)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return strings.TrimSpace(name)
}

func (s *Server) loadExistingDeviceIDs() (map[string]struct{}, error) {
	if s == nil || s.db == nil {
		return map[string]struct{}{}, nil
	}
	var ids []string
	if err := s.db.Model(&model.MediaSource{}).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(ids))
	for _, item := range ids {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out[item] = struct{}{}
	}
	return out, nil
}

func (s *Server) loadReferencedCoverPaths() (map[string]struct{}, error) {
	if s == nil || s.db == nil {
		return map[string]struct{}{}, nil
	}
	var imageURLs []string
	if err := s.db.Model(&model.Algorithm{}).Pluck("image_url", &imageURLs).Error; err != nil {
		return nil, err
	}
	refs := make(map[string]struct{}, len(imageURLs))
	for _, item := range imageURLs {
		rel := normalizeCoverImageRelPath(item)
		if rel == "" {
			continue
		}
		refs[rel] = struct{}{}
	}
	return refs, nil
}

func (s *Server) loadReferencedDeviceSnapshotPaths() (map[string]struct{}, error) {
	if s == nil || s.db == nil {
		return map[string]struct{}{}, nil
	}
	var snapshotURLs []string
	if err := s.db.Model(&model.MediaSource{}).Pluck("snapshot_url", &snapshotURLs).Error; err != nil {
		return nil, err
	}
	refs := make(map[string]struct{}, len(snapshotURLs))
	for _, item := range snapshotURLs {
		rel := normalizeDeviceSnapshotRelPath(item)
		if rel == "" {
			continue
		}
		refs[rel] = struct{}{}
	}
	return refs, nil
}

func normalizeCoverImageRelPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil {
		if strings.TrimSpace(parsed.Path) != "" {
			raw = strings.TrimSpace(parsed.Path)
		}
	}
	raw = filepath.ToSlash(strings.TrimSpace(raw))
	if idx := strings.Index(raw, "/api/v1/algorithms/cover/"); idx >= 0 {
		raw = raw[idx+len("/api/v1/algorithms/cover/"):]
	}
	raw = strings.TrimPrefix(raw, "/api/v1/algorithms/cover/")
	raw = strings.TrimPrefix(raw, "api/v1/algorithms/cover/")
	raw = strings.TrimPrefix(raw, "/")
	raw = filepath.ToSlash(filepath.Clean(raw))
	raw = strings.TrimPrefix(raw, "./")
	if raw == "" || raw == "." {
		return ""
	}
	return raw
}

func normalizeDeviceSnapshotRelPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil {
		if strings.TrimSpace(parsed.Path) != "" {
			raw = strings.TrimSpace(parsed.Path)
		}
	}
	raw = filepath.ToSlash(strings.TrimSpace(raw))
	if idx := strings.Index(raw, "/api/v1/devices/snapshot/"); idx >= 0 {
		raw = raw[idx+len("/api/v1/devices/snapshot/"):]
	}
	raw = strings.TrimPrefix(raw, "/api/v1/devices/snapshot/")
	raw = strings.TrimPrefix(raw, "api/v1/devices/snapshot/")
	raw = strings.TrimPrefix(raw, "/")
	raw = filepath.ToSlash(filepath.Clean(raw))
	raw = strings.TrimPrefix(raw, "./")
	if raw == "" || raw == "." {
		return ""
	}
	return raw
}

func relHasSegment(relPath, segment string) bool {
	relPath = filepath.ToSlash(strings.Trim(relPath, "/"))
	segment = strings.TrimSpace(segment)
	if relPath == "" || segment == "" {
		return false
	}
	for _, part := range strings.Split(relPath, "/") {
		if strings.EqualFold(strings.TrimSpace(part), segment) {
			return true
		}
	}
	return false
}

func removeFilesOlderThanDetailed(
	root string,
	cutoff time.Time,
	include func(cleanupFile) bool,
	skipDir func(relDir string) bool,
) ([]cleanupFile, error) {
	return removeOldestFilesDetailed(
		root,
		func(file cleanupFile) bool {
			if file.ModTime.After(cutoff) {
				return false
			}
			if include != nil && !include(file) {
				return false
			}
			return true
		},
		skipDir,
		nil,
		0,
		0,
	)
}

func removeOldestFilesDetailed(
	root string,
	include func(cleanupFile) bool,
	skipDir func(relDir string) bool,
	usage *diskUsageSnapshot,
	targetPercent float64,
	minFreeGB float64,
) ([]cleanupFile, error) {
	files, err := collectFilesFiltered(root, include, skipDir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	removed := make([]cleanupFile, 0, len(files))
	for _, file := range files {
		if usage != nil && usage.reachedTarget(targetPercent, minFreeGB) {
			break
		}
		if err := os.Remove(file.Path); err != nil {
			continue
		}
		removed = append(removed, file)
		if usage != nil {
			usage.subtract(file.Size)
		}
	}
	_ = cleanupEmptyDirs(strings.TrimSpace(root))
	return removed, nil
}

func removeFilesOlderThan(root string, cutoff time.Time) (int, uint64, error) {
	removedFiles, err := removeFilesOlderThanDetailed(root, cutoff, nil, nil)
	if err != nil {
		return 0, 0, err
	}
	removed := 0
	var removedBytes uint64
	for _, item := range removedFiles {
		removed++
		removedBytes += item.Size
	}
	return removed, removedBytes, nil
}

func removeOldestUntilThreshold(root string, threshold float64) (int, uint64, float64, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0, 0, 0, nil
	}
	if threshold <= 0 || threshold >= 100 {
		return 0, 0, 0, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return 0, 0, 0, nil
		}
		return 0, 0, 0, err
	}

	usage, err := disk.Usage(root)
	if err != nil {
		return 0, 0, 0, err
	}
	if usage.Total == 0 || usage.UsedPercent <= threshold {
		return 0, 0, usage.UsedPercent, nil
	}

	files, err := collectFiles(root)
	if err != nil {
		return 0, 0, usage.UsedPercent, err
	}
	if len(files) == 0 {
		return 0, 0, usage.UsedPercent, nil
	}

	targetPercent := threshold - 0.5
	if targetPercent < 0 {
		targetPercent = 0
	}
	targetUsed := uint64(float64(usage.Total) * targetPercent / 100.0)
	used := usage.Used
	removed := 0
	var removedBytes uint64

	for _, f := range files {
		if used <= targetUsed {
			break
		}
		if err := os.Remove(f.Path); err != nil {
			continue
		}
		removed++
		removedBytes += f.Size
		if f.Size >= used {
			used = 0
		} else {
			used -= f.Size
		}
	}

	_ = cleanupEmptyDirs(root)

	finalPercent := float64(used) / float64(usage.Total) * 100
	if refreshed, err := disk.Usage(root); err == nil {
		finalPercent = refreshed.UsedPercent
	}
	return removed, removedBytes, finalPercent, nil
}

func collectFiles(root string) ([]cleanupFile, error) {
	return collectFilesFiltered(root, nil, nil)
}

func collectFilesFiltered(
	root string,
	include func(cleanupFile) bool,
	skipDir func(relDir string) bool,
) ([]cleanupFile, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	files := make([]cleanupFile, 0, 64)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = ""
		}
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if d.IsDir() {
			if rel != "" && rel != "." && skipDir != nil && skipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		fileInfo, err := d.Info()
		if err != nil {
			return nil
		}
		size := uint64(0)
		if fileInfo.Size() > 0 {
			size = uint64(fileInfo.Size())
		}
		item := cleanupFile{
			Path:    path,
			RelPath: rel,
			ModTime: fileInfo.ModTime(),
			Size:    size,
		}
		if include != nil && !include(item) {
			return nil
		}
		files = append(files, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModTime.Equal(files[j].ModTime) {
			return files[i].RelPath < files[j].RelPath
		}
		return files[i].ModTime.Before(files[j].ModTime)
	})
	return files, nil
}

func cleanupEmptyDirs(root string) error {
	dirs := make([]string, 0, 32)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, dir := range dirs {
		if filepath.Clean(dir) == filepath.Clean(root) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		if len(entries) == 0 {
			_ = os.Remove(dir)
		}
	}
	return nil
}
