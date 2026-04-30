package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"maas-box/internal/logutil"
	"maas-box/internal/model"
)

const (
	alarmBufferRootName    = "_alarm_buffer"
	alarmClipDirName       = "alarm_clips"
	alarmClipMergedName    = "merged.mp4"
	alarmClipConcatFile    = ".merge_concat.txt"
	alarmClipSessionPrefix = "session_"
)

const (
	alarmClipSessionStatusRecording   = "recording"
	alarmClipSessionStatusClosing     = "closing"
	alarmClipSessionStatusClosed      = "closed"
	alarmClipSessionStatusClosedEmpty = "closed_empty"
	alarmClipSessionStatusFailed      = "failed"
)

var (
	alarmClipFinalizeRetryInterval   = 2 * time.Second
	alarmClipFinalizeContinuousFloor = 30 * time.Second
	alarmClipFinalizeBufferFloor     = 12 * time.Second
	alarmClipFinalizeContinuousGrace = 5 * time.Second
	alarmClipFinalizeBufferGrace     = 6 * time.Second
	alarmClipMergeRunner             = runAlarmClipMergeWithFFmpeg
)

type alarmClipFile struct {
	absPath  string
	relPath  string
	size     int64
	modTime  time.Time
	fileName string
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func (s *Server) alarmClipDefaultPreSeconds() int {
	if s != nil && s.cfg != nil && s.cfg.Server.Recording.AlarmClip.PreSeconds > 0 {
		return s.cfg.Server.Recording.AlarmClip.PreSeconds
	}
	return 8
}

func (s *Server) alarmClipDefaultPostSeconds() int {
	if s != nil && s.cfg != nil && s.cfg.Server.Recording.AlarmClip.PostSeconds > 0 {
		return s.cfg.Server.Recording.AlarmClip.PostSeconds
	}
	return 12
}

func (s *Server) alarmClipSessionSettleSeconds() int {
	if s != nil && s.cfg != nil && s.cfg.Server.Recording.AlarmClip.SessionSettleSeconds > 0 {
		return clampInt(s.cfg.Server.Recording.AlarmClip.SessionSettleSeconds, 1, 10)
	}
	return 2
}

func (s *Server) alarmClipMaxSessionSeconds() int {
	if s != nil && s.cfg != nil && s.cfg.Server.Recording.AlarmClip.MaxSessionSeconds > 0 {
		return clampInt(s.cfg.Server.Recording.AlarmClip.MaxSessionSeconds, 30, 1800)
	}
	return 180
}

func (s *Server) alarmClipBufferSegmentSeconds() int {
	if s != nil && s.cfg != nil && s.cfg.Server.Recording.AlarmClip.BufferSegmentSeconds > 0 {
		return clampInt(s.cfg.Server.Recording.AlarmClip.BufferSegmentSeconds, 1, 30)
	}
	return 2
}

func (s *Server) alarmClipBufferKeepSeconds(preSec, postSec int) int {
	minKeep := preSec + postSec + s.alarmClipBufferSegmentSeconds()
	if minKeep < 30 {
		minKeep = 30
	}
	if s != nil && s.cfg != nil && s.cfg.Server.Recording.AlarmClip.BufferKeepSeconds > 0 {
		return clampInt(s.cfg.Server.Recording.AlarmClip.BufferKeepSeconds, minKeep, 3600)
	}
	return clampInt(preSec+postSec+60, minKeep, 3600)
}

func sanitizePathSegment(v string) string {
	v = strings.TrimSpace(v)
	v = strings.ReplaceAll(v, "/", "_")
	v = strings.ReplaceAll(v, "\\", "_")
	if v == "" {
		return "unknown"
	}
	return v
}

func formatAlarmClipTS(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.Local().Format("20060102150405")
}

func resolveAlarmClipSessionAnchorTime(startedAt, createdAt time.Time) time.Time {
	if !startedAt.IsZero() {
		return startedAt
	}
	if !createdAt.IsZero() {
		return createdAt
	}
	return time.Now()
}

func buildAlarmClipSessionName(sessionID string, anchorTime time.Time) string {
	return alarmClipSessionPrefix + sanitizePathSegment(sessionID) + "_" + formatAlarmClipTS(anchorTime)
}

func buildAlarmClipSegmentName(ts string, seq int, sourceName string) string {
	return fmt.Sprintf("%s_%03d_%s", ts, seq, sanitizePathSegment(sourceName))
}

func buildAlarmClipMergedFileName(ts string) string {
	return ts + "_" + alarmClipMergedName
}

func (s *Server) resolveAlarmClipInput(existing *model.MediaSource, enable *bool, preSeconds *int, postSeconds *int) (bool, int, int, error) {
	defaultEnable := false
	if s != nil && s.cfg != nil {
		defaultEnable = s.cfg.Server.Recording.AlarmClip.EnabledDefault
	}
	pre := s.alarmClipDefaultPreSeconds()
	post := s.alarmClipDefaultPostSeconds()
	enabled := defaultEnable
	if existing != nil {
		enabled = existing.EnableAlarmClip
		if existing.AlarmPreSeconds > 0 {
			pre = existing.AlarmPreSeconds
		}
		if existing.AlarmPostSeconds > 0 {
			post = existing.AlarmPostSeconds
		}
	}
	if enable != nil {
		enabled = *enable
	}
	if preSeconds != nil {
		pre = *preSeconds
	}
	if postSeconds != nil {
		post = *postSeconds
	}
	pre = clampInt(pre, 1, 600)
	post = clampInt(post, 1, 600)
	if enabled && pre <= 0 {
		return false, 0, 0, errors.New("alarm_pre_seconds must be greater than 0")
	}
	if enabled && post <= 0 {
		return false, 0, 0, errors.New("alarm_post_seconds must be greater than 0")
	}
	return enabled, pre, post, nil
}

func (s *Server) shouldRunAlarmClipBuffer(source *model.MediaSource) bool {
	if s == nil || source == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(source.AIStatus), model.DeviceAIStatusRunning) {
		return true
	}
	return false
}

func (s *Server) safeAlarmBufferDeviceDir(sourceID string) (string, error) {
	root := filepath.Clean(strings.TrimSpace(s.cfg.Server.Recording.AlarmClip.BufferDir))
	if root == "" {
		return "", errors.New("alarm buffer storage dir is empty")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	sourceDirAbs, err := filepath.Abs(filepath.Join(rootAbs, sanitizePathSegment(sourceID)))
	if err != nil {
		return "", err
	}
	if !isSubPath(rootAbs, sourceDirAbs) {
		return "", errors.New("invalid alarm buffer path")
	}
	return sourceDirAbs, nil
}

func (s *Server) safeAlarmClipEventDir(sourceID, eventID string) (string, error) {
	sourceDir, err := s.safeRecordingDeviceDir(sourceID)
	if err != nil {
		return "", err
	}
	eventID = sanitizePathSegment(eventID)
	dirAbs, err := filepath.Abs(filepath.Join(sourceDir, alarmClipDirName, eventID))
	if err != nil {
		return "", err
	}
	if !isSubPath(sourceDir, dirAbs) {
		return "", errors.New("invalid alarm clip path")
	}
	return dirAbs, nil
}

func (s *Server) safeAlarmClipSessionDir(sourceID, sessionID string, anchorTime time.Time) (string, error) {
	sourceDir, err := s.safeRecordingDeviceDir(sourceID)
	if err != nil {
		return "", err
	}
	sessionName := buildAlarmClipSessionName(sessionID, anchorTime)
	dirAbs, err := filepath.Abs(filepath.Join(sourceDir, alarmClipDirName, sessionName))
	if err != nil {
		return "", err
	}
	if !isSubPath(sourceDir, dirAbs) {
		return "", errors.New("invalid alarm clip session path")
	}
	return dirAbs, nil
}

func (s *Server) resolveZLMAlarmBufferDir(sourceID, localBufferDir string) (string, error) {
	if s == nil || s.cfg == nil {
		return "", errors.New("invalid recording context")
	}
	localBufferDir = strings.TrimSpace(localBufferDir)
	if localBufferDir == "" {
		return "", errors.New("local alarm buffer dir is empty")
	}
	zlmRoot := strings.TrimSpace(s.cfg.Server.Recording.AlarmClip.ZLMBufferDir)
	if zlmRoot == "" {
		recordRoot := strings.TrimSpace(s.cfg.Server.Recording.ZLMStorageDir)
		if recordRoot != "" {
			zlmRoot = strings.TrimRight(recordRoot, "/") + "/" + alarmBufferRootName
		}
	}
	if zlmRoot == "" {
		return localBufferDir, nil
	}
	zlmRoot = strings.TrimRight(zlmRoot, "/")
	return zlmRoot + "/" + sanitizePathSegment(sourceID), nil
}

func (s *Server) startAlarmBufferForSource(source model.MediaSource) error {
	if s == nil || s.cfg == nil || s.db == nil || s.cfg.Server.Recording.Disabled {
		return nil
	}
	app, stream := parseDeviceZLMAppStream(strings.ToLower(strings.TrimSpace(source.Protocol)), source, strings.TrimSpace(s.cfg.Server.ZLM.App))
	app = strings.TrimSpace(app)
	stream = strings.TrimSpace(stream)
	if stream == "" {
		return fmt.Errorf("alarm buffer stream is empty")
	}
	if app == "" {
		app = strings.TrimSpace(s.cfg.Server.ZLM.App)
	}
	if app == "" {
		app = "live"
	}
	bufferDir, err := s.safeAlarmBufferDeviceDir(source.ID)
	if err != nil {
		return err
	}
	if err := s.ensureDir(bufferDir); err != nil {
		return err
	}
	zlmBufferDir, err := s.resolveZLMAlarmBufferDir(source.ID, bufferDir)
	if err != nil {
		return err
	}
	if err := s.startRecordByAppStream(source.ID, app, stream, zlmBufferDir, s.alarmClipBufferSegmentSeconds(), recordingStatusBuffering); err != nil {
		return err
	}
	_, preSeconds, postSeconds, _, policyErr := s.resolveTaskRecordingPolicyBySourceID(source.ID)
	if policyErr != nil {
		return policyErr
	}
	return s.pruneAlarmBufferFiles(source.ID, preSeconds, postSeconds)
}

func (s *Server) alarmBufferHealthWindow() time.Duration {
	seconds := s.alarmClipBufferSegmentSeconds()*4 + 10
	if seconds < 30 {
		seconds = 30
	}
	if seconds > 120 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func (s *Server) hasRecentAlarmBufferFiles(sourceID string) (bool, error) {
	if s == nil {
		return false, nil
	}
	dir, err := s.safeAlarmBufferDeviceDir(sourceID)
	if err != nil {
		return false, err
	}
	info, statErr := os.Stat(dir)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil
		}
		return false, statErr
	}
	if !info.IsDir() {
		return false, nil
	}

	cutoff := time.Now().Add(-s.alarmBufferHealthWindow())
	found := false
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil || d.IsDir() {
			return nil
		}
		fileInfo, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if fileInfo.Size() <= 0 {
			return nil
		}
		if !fileInfo.ModTime().Before(cutoff) {
			found = true
			return io.EOF
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, io.EOF) {
		return false, walkErr
	}
	return found, nil
}

func (s *Server) pruneAlarmBufferFiles(sourceID string, preSeconds, postSeconds int) error {
	if s == nil {
		return nil
	}
	dir, err := s.safeAlarmBufferDeviceDir(sourceID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-time.Duration(s.alarmClipBufferKeepSeconds(preSeconds, postSeconds)) * time.Second)
	protectedBefore, protectErr := s.loadActiveAlarmBufferProtectedBefore()
	if protectErr != nil {
		logutil.Warnf("prune alarm buffer skipped: source_id=%s load active sessions failed: %v", strings.TrimSpace(sourceID), protectErr)
		return nil
	}
	_, rmErr := removeFilesOlderThanDetailed(
		dir,
		cutoff,
		func(file cleanupFile) bool {
			return !isAlarmBufferFileProtectedForSource(sourceID, file.ModTime, protectedBefore)
		},
		nil,
	)
	return rmErr
}

func (s *Server) triggerAlarmClipBySourceID(sourceID string, occurredAt time.Time, eventIDs []string) {
	if s == nil || s.db == nil {
		return
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || len(eventIDs) == 0 {
		return
	}
	var source model.MediaSource
	if err := s.db.Where("id = ?", sourceID).First(&source).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logutil.Warnf("trigger alarm clip query source failed: source_id=%s err=%v", sourceID, err)
		}
		return
	}
	recordingPolicy, preSeconds, postSeconds, _, err := s.resolveTaskRecordingPolicyBySourceID(source.ID)
	if err != nil {
		return
	}
	ids := make([]string, 0, len(eventIDs))
	for _, eventID := range eventIDs {
		eventID = strings.TrimSpace(eventID)
		if eventID == "" {
			continue
		}
		ids = append(ids, eventID)
	}
	if len(ids) == 0 {
		return
	}
	switch recordingPolicy {
	case model.RecordingPolicyAlarmClip:
		_ = s.applyRecordingPolicyForSourceID(source.ID)
		session, startErr := s.startOrExtendAlarmClipSessionBySourceID(source.ID, occurredAt, preSeconds, postSeconds, ids)
		if startErr != nil {
			logutil.Warnf("start or extend alarm clip session failed: source_id=%s err=%v", source.ID, startErr)
			return
		}
		if session != nil {
			s.scheduleAlarmClipSessionFinalizeBySource(session.SourceID, session.ExpectedEndAt)
		}
	default:
		s.markEventClipsReadyWithoutFiles(ids)
	}
}

func (s *Server) resolveAlarmClipSourceDir(source *model.MediaSource, recordingPolicy string) (string, bool, error) {
	_ = recordingPolicy
	dir, dirErr := s.safeAlarmBufferDeviceDir(source.ID)
	return dir, false, dirErr
}

func (s *Server) alarmClipFinalizeMaxWait(fromContinuous bool, postSeconds int) time.Duration {
	if fromContinuous {
		wait := time.Duration(s.recordingSegmentSeconds()+postSeconds)*time.Second + alarmClipFinalizeContinuousGrace
		if wait < alarmClipFinalizeContinuousFloor {
			wait = alarmClipFinalizeContinuousFloor
		}
		return wait
	}
	wait := time.Duration(postSeconds)*time.Second + alarmClipFinalizeBufferGrace
	if wait < alarmClipFinalizeBufferFloor {
		wait = alarmClipFinalizeBufferFloor
	}
	return wait
}

func (s *Server) collectAlarmClipFiles(dir string, fromContinuous bool, occurredAt time.Time, preSeconds, postSeconds int) ([]alarmClipFile, error) {
	segmentTolerance := time.Duration(s.alarmClipBufferSegmentSeconds()+2) * time.Second
	windowStart := occurredAt.Add(-time.Duration(preSeconds) * time.Second).Add(-segmentTolerance)
	windowEnd := occurredAt.Add(time.Duration(postSeconds) * time.Second).Add(segmentTolerance)
	return s.collectAlarmClipFilesByRange(dir, fromContinuous, windowStart, windowEnd)
}

func (s *Server) collectAlarmClipFilesByRange(dir string, fromContinuous bool, windowStart, windowEnd time.Time) ([]alarmClipFile, error) {
	_, statErr := os.Stat(dir)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return []alarmClipFile{}, nil
		}
		return nil, statErr
	}
	if windowEnd.Before(windowStart) {
		windowStart, windowEnd = windowEnd, windowStart
	}
	files := make([]alarmClipFile, 0, 16)
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if fromContinuous {
				relDir, err := filepath.Rel(dir, path)
				if err == nil {
					relDir = filepath.ToSlash(relDir)
					if relDir == alarmClipDirName || strings.HasPrefix(relDir, alarmClipDirName+"/") {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		fileName := strings.TrimSpace(d.Name())
		if fileName == "" || strings.HasPrefix(fileName, ".") {
			// Skip unfinished temporary files to avoid broken clips without moov atom.
			return nil
		}
		ext := strings.ToLower(filepath.Ext(fileName))
		if ext != ".mp4" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() <= 0 {
			return nil
		}
		modTime := info.ModTime()
		if modTime.Before(windowStart) || modTime.After(windowEnd) {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		files = append(files, alarmClipFile{
			absPath:  path,
			relPath:  rel,
			size:     info.Size(),
			modTime:  modTime,
			fileName: fileName,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].relPath < files[j].relPath
		}
		return files[i].modTime.Before(files[j].modTime)
	})
	return files, nil
}

func copyFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Sync()
}
func writeAlarmClipFilesIntoDir(targetDir, relDir string, files []alarmClipFile, anchorTime time.Time) (string, error) {
	if len(files) == 0 {
		return "[]", nil
	}
	_ = os.RemoveAll(targetDir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}
	clipFiles := make([]string, 0, len(files))
	segmentNames := make([]string, 0, len(files))
	ts := formatAlarmClipTS(anchorTime)
	for i, file := range files {
		dstName := buildAlarmClipSegmentName(ts, i+1, file.fileName)
		dstPath := filepath.Join(targetDir, dstName)
		if err := copyFile(file.absPath, dstPath); err != nil {
			return "", err
		}
		segmentNames = append(segmentNames, dstName)
		clipFiles = append(clipFiles, filepath.ToSlash(filepath.Join(relDir, dstName)))
	}
	if len(segmentNames) > 1 {
		mergedName := buildAlarmClipMergedFileName(ts)
		if mergeErr := alarmClipMergeRunner(targetDir, mergedName, segmentNames); mergeErr != nil {
			return "", mergeErr
		}
		mergedRelPath := filepath.ToSlash(filepath.Join(relDir, mergedName))
		clipFiles = []string{mergedRelPath}
		for _, name := range segmentNames {
			if strings.EqualFold(strings.TrimSpace(name), mergedName) {
				continue
			}
			if err := os.Remove(filepath.Join(targetDir, name)); err != nil && !os.IsNotExist(err) {
				logutil.Warnf("remove merged source segment failed: file=%s err=%v", name, err)
			}
		}
	}
	clipFilesJSON, _ := json.Marshal(clipFiles)
	return string(clipFilesJSON), nil
}
func (s *Server) writeAlarmClipEventFiles(sourceID, eventID string, files []alarmClipFile, anchorTime time.Time) (string, string, error) {
	if len(files) == 0 {
		return "", "[]", nil
	}
	eventDir, err := s.safeAlarmClipEventDir(sourceID, eventID)
	if err != nil {
		return "", "", err
	}
	relDir := filepath.ToSlash(filepath.Join(alarmClipDirName, sanitizePathSegment(eventID)))
	ts := formatAlarmClipTS(anchorTime)
	clipFilesJSON, writeErr := writeAlarmClipFilesIntoDir(eventDir, relDir, files, anchorTime)
	if writeErr != nil {
		logutil.Warnf("merge alarm clip segments failed, fallback to segments: source_id=%s event_id=%s err=%v", sourceID, eventID, writeErr)
		_ = os.RemoveAll(eventDir)
		if err := os.MkdirAll(eventDir, 0o755); err != nil {
			return "", "", err
		}
		segmentItems := make([]string, 0, len(files))
		for i, file := range files {
			dstName := buildAlarmClipSegmentName(ts, i+1, file.fileName)
			dstPath := filepath.Join(eventDir, dstName)
			if err := copyFile(file.absPath, dstPath); err != nil {
				return "", "", err
			}
			segmentItems = append(segmentItems, filepath.ToSlash(filepath.Join(relDir, dstName)))
		}
		segmentJSON, _ := json.Marshal(segmentItems)
		return relDir, string(segmentJSON), nil
	}
	return relDir, clipFilesJSON, nil
}
func (s *Server) writeAlarmClipSessionFiles(sourceID, sessionID string, files []alarmClipFile, anchorTime time.Time) (string, string, error) {
	if len(files) == 0 {
		return "", "[]", nil
	}
	sessionDir, err := s.safeAlarmClipSessionDir(sourceID, sessionID, anchorTime)
	if err != nil {
		return "", "", err
	}
	sessionRel := filepath.ToSlash(filepath.Join(alarmClipDirName, buildAlarmClipSessionName(sessionID, anchorTime)))
	ts := formatAlarmClipTS(anchorTime)
	clipFilesJSON, writeErr := writeAlarmClipFilesIntoDir(sessionDir, sessionRel, files, anchorTime)
	if writeErr != nil {
		logutil.Warnf("merge alarm clip session segments failed, fallback to segments: source_id=%s session_id=%s err=%v", sourceID, sessionID, writeErr)
		_ = os.RemoveAll(sessionDir)
		if err := os.MkdirAll(sessionDir, 0o755); err != nil {
			return "", "", err
		}
		segmentItems := make([]string, 0, len(files))
		for i, file := range files {
			dstName := buildAlarmClipSegmentName(ts, i+1, file.fileName)
			dstPath := filepath.Join(sessionDir, dstName)
			if err := copyFile(file.absPath, dstPath); err != nil {
				return "", "", err
			}
			segmentItems = append(segmentItems, filepath.ToSlash(filepath.Join(sessionRel, dstName)))
		}
		segmentJSON, _ := json.Marshal(segmentItems)
		return sessionRel, string(segmentJSON), nil
	}
	return sessionRel, clipFilesJSON, nil
}

func runAlarmClipMergeWithFFmpeg(eventDir, outputName string, segmentNames []string) error {
	eventDir = strings.TrimSpace(eventDir)
	if eventDir == "" {
		return errors.New("event dir is empty")
	}
	outputName = strings.TrimSpace(outputName)
	if outputName == "" {
		return errors.New("output name is empty")
	}
	segmentNames = uniqueStrings(segmentNames)
	if len(segmentNames) <= 1 {
		return nil
	}
	concatPath := filepath.Join(eventDir, alarmClipConcatFile)
	outputPath := filepath.Join(eventDir, outputName)
	lines := make([]string, 0, len(segmentNames))
	for _, item := range segmentNames {
		name := filepath.Clean(strings.TrimSpace(item))
		if name == "" || name == "." || name == ".." || filepath.IsAbs(name) {
			return fmt.Errorf("invalid segment name: %q", item)
		}
		name = filepath.ToSlash(name)
		if strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
			return fmt.Errorf("invalid segment path: %q", item)
		}
		escaped := strings.ReplaceAll(name, "'", "'\\''")
		lines = append(lines, "file '"+escaped+"'")
	}
	concatBody := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(concatPath, []byte(concatBody), 0o644); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(concatPath)
	}()
	_ = os.Remove(outputPath)
	cmd := exec.Command(
		"ffmpeg",
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", concatPath,
		"-c", "copy",
		"-movflags", "+faststart",
		outputPath,
	)
	cmd.Dir = eventDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ffmpeg merge failed: %s", msg)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Size() <= 0 {
		return errors.New("merged clip is invalid")
	}
	return nil
}

func marshalAlarmClipFileRelPaths(files []alarmClipFile) string {
	if len(files) == 0 {
		return "[]"
	}
	items := make([]string, 0, len(files))
	for _, file := range files {
		rel := filepath.ToSlash(filepath.Clean(strings.TrimSpace(file.relPath)))
		rel = strings.TrimPrefix(rel, "./")
		if rel == "" || rel == "." {
			continue
		}
		items = append(items, rel)
	}
	items = uniqueStrings(items)
	if len(items) == 0 {
		return "[]"
	}
	body, _ := json.Marshal(items)
	return string(body)
}

func (s *Server) markEventClipsReadyWithoutFiles(eventIDs []string) {
	if s == nil || s.db == nil {
		return
	}
	ids := normalizeEventIDList(eventIDs)
	for _, eventID := range ids {
		if err := s.db.Model(&model.AlarmEvent{}).
			Where("id = ?", eventID).
			Updates(map[string]any{
				"clip_ready":      true,
				"clip_path":       "",
				"clip_files_json": "[]",
				"updated_at":      time.Now(),
			}).Error; err != nil {
			logutil.Warnf("mark event clip empty failed: event_id=%s err=%v", eventID, err)
		}
	}
}

func normalizeEventIDList(eventIDs []string) []string {
	out := make([]string, 0, len(eventIDs))
	seen := make(map[string]struct{}, len(eventIDs))
	for _, eventID := range eventIDs {
		id := strings.TrimSpace(eventID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func marshalClipPathsJSON(paths []string) string {
	normalized := normalizeCleanupRelPaths(paths)
	if len(normalized) == 0 {
		return "[]"
	}
	body, _ := json.Marshal(normalized)
	return string(body)
}

func (s *Server) updateAlarmClipResultForEvents(eventIDs []string, clipPath, clipFilesJSON string) error {
	if s == nil || s.db == nil {
		return nil
	}
	ids := normalizeEventIDList(eventIDs)
	if len(ids) == 0 {
		return nil
	}
	clipPath = strings.TrimSpace(clipPath)
	clipFilesJSON = strings.TrimSpace(clipFilesJSON)
	if clipFilesJSON == "" {
		clipFilesJSON = "[]"
	}
	return s.db.Model(&model.AlarmEvent{}).
		Where("id IN ?", ids).
		Updates(map[string]any{
			"clip_ready":      true,
			"clip_path":       clipPath,
			"clip_files_json": clipFilesJSON,
			"updated_at":      time.Now(),
		}).Error
}

func alarmWindowsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	if aStart.After(aEnd) {
		aStart, aEnd = aEnd, aStart
	}
	if bStart.After(bEnd) {
		bStart, bEnd = bEnd, bStart
	}
	return !aEnd.Before(bStart) && !bEnd.Before(aStart)
}

func alarmWindowBounds(occurredAt time.Time, preSeconds, postSeconds int) (time.Time, time.Time) {
	start := occurredAt.Add(-time.Duration(preSeconds) * time.Second)
	end := occurredAt.Add(time.Duration(postSeconds) * time.Second)
	return start, end
}

func (s *Server) hasAllClipFiles(sourceID string, clipFiles []string) bool {
	if s == nil || s.db == nil {
		return false
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || len(clipFiles) == 0 {
		return false
	}
	for _, item := range clipFiles {
		clipPath := strings.TrimSpace(item)
		if clipPath == "" {
			return false
		}
		fullPath, err := s.safeRecordingFilePath(sourceID, clipPath)
		if err != nil {
			return false
		}
		info, statErr := os.Stat(fullPath)
		if statErr != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func (s *Server) findReusableAlarmClipByWindow(
	sourceID string,
	occurredAt time.Time,
	preSeconds, postSeconds int,
	excludeEventIDs []string,
) (string, string, bool, error) {
	if s == nil || s.db == nil {
		return "", "", false, nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return "", "", false, nil
	}
	currentStart, currentEnd := alarmWindowBounds(occurredAt, preSeconds, postSeconds)
	queryStart := currentStart.Add(-time.Duration(postSeconds) * time.Second)
	queryEnd := currentEnd.Add(time.Duration(preSeconds) * time.Second)

	type clipCandidate struct {
		ID            string
		OccurredAt    time.Time
		ClipPath      string
		ClipFilesJSON string
	}
	candidates := make([]clipCandidate, 0, 32)
	query := s.db.Model(&model.AlarmEvent{}).
		Select("id", "occurred_at", "clip_path", "clip_files_json").
		Where("device_id = ? AND clip_ready = ? AND clip_files_json <> '' AND clip_files_json <> '[]'", sourceID, true).
		Where("occurred_at >= ? AND occurred_at <= ?", queryStart, queryEnd)
	excludeIDs := normalizeEventIDList(excludeEventIDs)
	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}
	if err := query.Order("occurred_at desc").Limit(100).Find(&candidates).Error; err != nil {
		return "", "", false, err
	}

	for _, candidate := range candidates {
		candidateFiles := decodeEventClipFiles(candidate.ClipFilesJSON)
		if len(candidateFiles) == 0 {
			continue
		}
		if !s.hasAllClipFiles(sourceID, candidateFiles) {
			continue
		}
		candidateStart, candidateEnd := alarmWindowBounds(candidate.OccurredAt, preSeconds, postSeconds)
		if !alarmWindowsOverlap(currentStart, currentEnd, candidateStart, candidateEnd) {
			continue
		}
		return strings.TrimSpace(candidate.ClipPath), marshalClipPathsJSON(candidateFiles), true, nil
	}
	return "", "", false, nil
}

func (s *Server) finalizeAlarmClipByEvents(sourceID string, occurredAt time.Time, preSeconds, postSeconds int, eventIDs []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_ = preSeconds
	_ = postSeconds
	eventIDs = normalizeEventIDList(eventIDs)
	if len(eventIDs) == 0 {
		return nil
	}
	var source model.MediaSource
	if err := s.db.Where("id = ?", strings.TrimSpace(sourceID)).First(&source).Error; err != nil {
		return err
	}
	recordingPolicy, pre, post, _, err := s.resolveTaskRecordingPolicyBySourceID(source.ID)
	if err != nil {
		return nil
	}
	recordingPolicy = normalizeTaskRecordingPolicy(recordingPolicy)
	if recordingPolicy == model.RecordingPolicyNone {
		s.markEventClipsReadyWithoutFiles(eventIDs)
		return nil
	}
	dir, fromContinuous, err := s.resolveAlarmClipSourceDir(&source, recordingPolicy)
	if err != nil {
		return err
	}
	maxWait := s.alarmClipFinalizeMaxWait(fromContinuous, post)
	if maxWait <= 0 {
		maxWait = alarmClipFinalizeRetryInterval
		if maxWait <= 0 {
			maxWait = 2 * time.Second
		}
	}
	retryInterval := alarmClipFinalizeRetryInterval
	if retryInterval <= 0 {
		retryInterval = 2 * time.Second
	}

	if recordingPolicy == model.RecordingPolicyAlarmClip {
		s.alarmClipFinalizeMu.Lock()
		defer s.alarmClipFinalizeMu.Unlock()

		reusePath, reuseFilesJSON, found, reuseErr := s.findReusableAlarmClipByWindow(source.ID, occurredAt, pre, post, eventIDs)
		if reuseErr != nil {
			logutil.Warnf("query reusable alarm clip failed: source_id=%s err=%v", source.ID, reuseErr)
		}
		if found {
			if updateErr := s.updateAlarmClipResultForEvents(eventIDs, reusePath, reuseFilesJSON); updateErr != nil {
				return updateErr
			}
			logutil.Infof(
				"alarm clip reused: source_id=%s event_count=%d occurred_at=%s clip_path=%s",
				source.ID,
				len(eventIDs),
				occurredAt.Format(time.RFC3339Nano),
				reusePath,
			)
			return nil
		}
	}

	startAt := time.Now()
	retryCount := 0
	files := make([]alarmClipFile, 0, 4)
	for {
		files, err = s.collectAlarmClipFiles(dir, fromContinuous, occurredAt, pre, post)
		if err != nil {
			return err
		}
		if len(files) > 0 {
			break
		}
		elapsed := time.Since(startAt)
		if elapsed >= maxWait {
			break
		}
		sleepFor := retryInterval
		remain := maxWait - elapsed
		if remain < sleepFor {
			sleepFor = remain
		}
		if sleepFor <= 0 {
			break
		}
		retryCount += 1
		time.Sleep(sleepFor)
	}
	finalFileCount := len(files)
	clipPath := ""
	clipFilesJSON := "[]"
	if finalFileCount > 0 {
		if fromContinuous {
			clipFilesJSON = marshalAlarmClipFileRelPaths(files)
		} else {
			anchorEventID := eventIDs[0]
			clipPath, clipFilesJSON, err = s.writeAlarmClipEventFiles(source.ID, anchorEventID, files, occurredAt)
			if err != nil {
				return err
			}
		}
	}
	if err := s.updateAlarmClipResultForEvents(eventIDs, clipPath, clipFilesJSON); err != nil {
		return err
	}
	logutil.Infof(
		"alarm clip finalized: source_id=%s event_count=%d occurred_at=%s from_continuous=%t retry_count=%d final_file_count=%d clip_path=%s",
		source.ID,
		len(eventIDs),
		occurredAt.Format(time.RFC3339Nano),
		fromContinuous,
		retryCount,
		finalFileCount,
		clipPath,
	)
	return nil
}

func (s *Server) startOrExtendAlarmClipSessionBySourceID(
	sourceID string,
	occurredAt time.Time,
	preSeconds, postSeconds int,
	eventIDs []string,
) (*model.AlarmClipSession, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, nil
	}
	eventIDs = normalizeEventIDList(eventIDs)
	if len(eventIDs) == 0 {
		return nil, nil
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	if preSeconds <= 0 {
		preSeconds = s.alarmClipDefaultPreSeconds()
	}
	if postSeconds <= 0 {
		postSeconds = s.alarmClipDefaultPostSeconds()
	}
	preSeconds = clampInt(preSeconds, 1, 600)
	postSeconds = clampInt(postSeconds, 1, 600)
	now := time.Now()

	var sessionOut model.AlarmClipSession
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var source model.MediaSource
		if err := tx.Select("id", "ai_status", "status").Where("id = ?", sourceID).First(&source).Error; err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(source.AIStatus), model.DeviceAIStatusRunning) {
			return fmt.Errorf("source ai is not running")
		}
		if !strings.EqualFold(strings.TrimSpace(source.Status), "online") {
			return fmt.Errorf("source is offline")
		}

		active := model.AlarmClipSession{}
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("source_id = ? AND status = ?", sourceID, alarmClipSessionStatusRecording).
			Order("started_at desc").
			First(&active).Error
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}

		maxSessionDuration := time.Duration(s.alarmClipMaxSessionSeconds()) * time.Second
		if errors.Is(findErr, gorm.ErrRecordNotFound) || !now.Before(active.HardDeadlineAt) {
			sessionID := uuid.NewString()
			expectedEnd := now.Add(time.Duration(postSeconds) * time.Second)
			hardDeadline := now.Add(maxSessionDuration)
			if expectedEnd.After(hardDeadline) {
				expectedEnd = hardDeadline
			}
			session := model.AlarmClipSession{
				ID:             sessionID,
				SourceID:       sourceID,
				Status:         alarmClipSessionStatusRecording,
				AnchorEventID:  eventIDs[0],
				PreSeconds:     preSeconds,
				PostSeconds:    postSeconds,
				StartedAt:      now,
				LastAlarmAt:    occurredAt,
				ExpectedEndAt:  expectedEnd,
				HardDeadlineAt: hardDeadline,
				ClipFilesJSON:  "[]",
			}
			if err := tx.Create(&session).Error; err != nil {
				return err
			}
			active = session
		} else {
			expectedEnd := now.Add(time.Duration(postSeconds) * time.Second)
			if expectedEnd.After(active.HardDeadlineAt) {
				expectedEnd = active.HardDeadlineAt
			}
			if expectedEnd.Before(active.ExpectedEndAt) {
				expectedEnd = active.ExpectedEndAt
			}
			if err := tx.Model(&model.AlarmClipSession{}).Where("id = ?", active.ID).
				Updates(map[string]any{
					"last_alarm_at":   occurredAt,
					"expected_end_at": expectedEnd,
					"updated_at":      time.Now(),
				}).Error; err != nil {
				return err
			}
			active.LastAlarmAt = occurredAt
			active.ExpectedEndAt = expectedEnd
		}

		for _, eventID := range eventIDs {
			row := model.AlarmClipSessionEvent{
				SessionID:       active.ID,
				EventID:         eventID,
				EventOccurredAt: occurredAt,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.AlarmEvent{}).
			Where("id IN ?", eventIDs).
			Updates(map[string]any{
				"clip_session_id": active.ID,
				"clip_ready":      false,
				"clip_path":       "",
				"clip_files_json": "[]",
				"updated_at":      time.Now(),
			}).Error; err != nil {
			return err
		}
		sessionOut = active
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &sessionOut, nil
}

func (s *Server) scheduleAlarmClipSessionFinalizeBySource(sourceID string, expectedEndAt time.Time) {
	if s == nil {
		return
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return
	}
	deadline := expectedEndAt.Add(time.Duration(s.alarmClipSessionSettleSeconds()) * time.Second)
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	s.alarmClipSessionMu.Lock()
	if s.alarmClipSessionSeq == nil {
		s.alarmClipSessionSeq = make(map[string]uint64)
	}
	if s.alarmClipSessionTimers == nil {
		s.alarmClipSessionTimers = make(map[string]*time.Timer)
	}
	if oldTimer, ok := s.alarmClipSessionTimers[sourceID]; ok && oldTimer != nil {
		oldTimer.Stop()
	}
	seq := s.alarmClipSessionSeq[sourceID] + 1
	s.alarmClipSessionSeq[sourceID] = seq
	timer := time.AfterFunc(delay, func() {
		s.handleAlarmClipSessionDeadline(sourceID, seq)
	})
	s.alarmClipSessionTimers[sourceID] = timer
	s.alarmClipSessionMu.Unlock()
}

func (s *Server) cancelAlarmClipSessionFinalizeBySource(sourceID string) {
	if s == nil {
		return
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return
	}
	s.alarmClipSessionMu.Lock()
	if timer, ok := s.alarmClipSessionTimers[sourceID]; ok && timer != nil {
		timer.Stop()
	}
	delete(s.alarmClipSessionTimers, sourceID)
	delete(s.alarmClipSessionSeq, sourceID)
	s.alarmClipSessionMu.Unlock()
}

func (s *Server) stopAllAlarmClipSessionTimers() {
	if s == nil {
		return
	}
	s.alarmClipSessionMu.Lock()
	for sourceID, timer := range s.alarmClipSessionTimers {
		if timer != nil {
			timer.Stop()
		}
		delete(s.alarmClipSessionTimers, sourceID)
	}
	for sourceID := range s.alarmClipSessionSeq {
		delete(s.alarmClipSessionSeq, sourceID)
	}
	s.alarmClipSessionMu.Unlock()
}

func (s *Server) handleAlarmClipSessionDeadline(sourceID string, seq uint64) {
	if s == nil || s.db == nil {
		return
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return
	}
	s.alarmClipSessionMu.Lock()
	current, ok := s.alarmClipSessionSeq[sourceID]
	if !ok || current != seq {
		s.alarmClipSessionMu.Unlock()
		return
	}
	delete(s.alarmClipSessionSeq, sourceID)
	delete(s.alarmClipSessionTimers, sourceID)
	s.alarmClipSessionMu.Unlock()

	session, err := s.findActiveAlarmClipSessionBySource(sourceID)
	if err != nil || session == nil {
		return
	}
	if finalizeErr := s.finalizeAlarmClipSession(session.ID, "timer"); finalizeErr != nil {
		logutil.Warnf("finalize alarm clip session on timer failed: source_id=%s session_id=%s err=%v", sourceID, session.ID, finalizeErr)
	}
}

func (s *Server) findActiveAlarmClipSessionBySource(sourceID string) (*model.AlarmClipSession, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, nil
	}
	var session model.AlarmClipSession
	err := s.db.Where("source_id = ? AND status = ?", sourceID, alarmClipSessionStatusRecording).
		Order("started_at desc").First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}

func (s *Server) finalizeAlarmClipSession(sessionID, reason string) error {
	if s == nil || s.db == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	s.alarmClipFinalizeMu.Lock()
	defer s.alarmClipFinalizeMu.Unlock()

	var session model.AlarmClipSession
	if err := s.db.Where("id = ?", sessionID).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	status := strings.ToLower(strings.TrimSpace(session.Status))
	if status != alarmClipSessionStatusRecording && status != alarmClipSessionStatusClosing {
		return nil
	}
	if status == alarmClipSessionStatusRecording {
		if err := s.db.Model(&model.AlarmClipSession{}).Where("id = ?", session.ID).
			Updates(map[string]any{
				"status":     alarmClipSessionStatusClosing,
				"updated_at": time.Now(),
			}).Error; err != nil {
			return err
		}
		session.Status = alarmClipSessionStatusClosing
	}
	s.cancelAlarmClipSessionFinalizeBySource(session.SourceID)

	relations := make([]model.AlarmClipSessionEvent, 0, 16)
	if err := s.db.Where("session_id = ?", session.ID).Order("event_occurred_at asc, id asc").Find(&relations).Error; err != nil {
		return err
	}
	eventIDs := make([]string, 0, len(relations))
	for _, relation := range relations {
		eventID := strings.TrimSpace(relation.EventID)
		if eventID == "" {
			continue
		}
		eventIDs = append(eventIDs, eventID)
	}
	eventIDs = normalizeEventIDList(eventIDs)

	var source model.MediaSource
	if err := s.db.Where("id = ?", session.SourceID).First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = s.updateAlarmClipResultForSessionEvents(session.ID, eventIDs, "", "[]", true)
			return s.db.Model(&model.AlarmClipSession{}).Where("id = ?", session.ID).
				Updates(map[string]any{
					"status":          alarmClipSessionStatusClosedEmpty,
					"clip_path":       "",
					"clip_files_json": "[]",
					"finalized_at":    time.Now(),
					"updated_at":      time.Now(),
				}).Error
		}
		return err
	}
	dir, _, err := s.resolveAlarmClipSourceDir(&source, model.RecordingPolicyAlarmClip)
	if err != nil {
		return err
	}
	settle := time.Duration(s.alarmClipSessionSettleSeconds()) * time.Second
	windowStart := session.StartedAt.Add(-time.Duration(session.PreSeconds) * time.Second)
	windowEnd := session.ExpectedEndAt.Add(settle)
	files, collectErr := s.collectAlarmClipFilesByRange(dir, false, windowStart, windowEnd)
	if collectErr != nil {
		_ = s.updateAlarmClipResultForSessionEvents(session.ID, eventIDs, "", "[]", true)
		_ = s.db.Model(&model.AlarmClipSession{}).Where("id = ?", session.ID).
			Updates(map[string]any{
				"status":          alarmClipSessionStatusFailed,
				"clip_path":       "",
				"clip_files_json": "[]",
				"finalized_at":    time.Now(),
				"updated_at":      time.Now(),
			}).Error
		return collectErr
	}

	clipPath := ""
	clipFilesJSON := "[]"
	sessionStatus := alarmClipSessionStatusClosedEmpty
	if len(files) > 0 {
		anchorTime := resolveAlarmClipSessionAnchorTime(session.StartedAt, session.CreatedAt)
		clipPath, clipFilesJSON, err = s.writeAlarmClipSessionFiles(source.ID, session.ID, files, anchorTime)
		if err != nil {
			_ = s.updateAlarmClipResultForSessionEvents(session.ID, eventIDs, "", "[]", true)
			_ = s.db.Model(&model.AlarmClipSession{}).Where("id = ?", session.ID).
				Updates(map[string]any{
					"status":          alarmClipSessionStatusFailed,
					"clip_path":       "",
					"clip_files_json": "[]",
					"finalized_at":    time.Now(),
					"updated_at":      time.Now(),
				}).Error
			return err
		}
		sessionStatus = alarmClipSessionStatusClosed
	}
	if err := s.updateAlarmClipResultForSessionEvents(session.ID, eventIDs, clipPath, clipFilesJSON, true); err != nil {
		return err
	}
	if err := s.db.Model(&model.AlarmClipSession{}).Where("id = ?", session.ID).
		Updates(map[string]any{
			"status":          sessionStatus,
			"clip_path":       clipPath,
			"clip_files_json": clipFilesJSON,
			"finalized_at":    time.Now(),
			"updated_at":      time.Now(),
		}).Error; err != nil {
		return err
	}
	logutil.Infof(
		"alarm clip session finalized: session_id=%s source_id=%s status=%s reason=%s event_count=%d clip_path=%s",
		session.ID,
		session.SourceID,
		sessionStatus,
		strings.TrimSpace(reason),
		len(eventIDs),
		clipPath,
	)
	return nil
}

func (s *Server) updateAlarmClipResultForSessionEvents(
	sessionID string,
	eventIDs []string,
	clipPath string,
	clipFilesJSON string,
	clipReady bool,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	eventIDs = normalizeEventIDList(eventIDs)
	if len(eventIDs) == 0 {
		return nil
	}
	clipPath = strings.TrimSpace(clipPath)
	clipFilesJSON = strings.TrimSpace(clipFilesJSON)
	if clipFilesJSON == "" {
		clipFilesJSON = "[]"
	}
	return s.db.Model(&model.AlarmEvent{}).Where("id IN ?", eventIDs).
		Updates(map[string]any{
			"clip_session_id": sessionID,
			"clip_ready":      clipReady,
			"clip_path":       clipPath,
			"clip_files_json": clipFilesJSON,
			"updated_at":      time.Now(),
		}).Error
}

func (s *Server) closeActiveAlarmClipSessionForSource(sourceID, reason string) error {
	if s == nil || s.db == nil {
		return nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil
	}
	s.cancelAlarmClipSessionFinalizeBySource(sourceID)
	session, err := s.findActiveAlarmClipSessionBySource(sourceID)
	if err != nil || session == nil {
		return err
	}
	return s.finalizeAlarmClipSession(session.ID, reason)
}

func (s *Server) closeAllActiveAlarmClipSessionsOnShutdown() {
	if s == nil || s.db == nil {
		return
	}
	var sessions []model.AlarmClipSession
	if err := s.db.Select("id").
		Where("status IN ?", []string{alarmClipSessionStatusRecording, alarmClipSessionStatusClosing}).
		Find(&sessions).Error; err != nil {
		return
	}
	for _, session := range sessions {
		if err := s.finalizeAlarmClipSession(strings.TrimSpace(session.ID), "server_close"); err != nil {
			logutil.Warnf("close alarm clip session on shutdown failed: session_id=%s err=%v", session.ID, err)
		}
	}
}

func (s *Server) isAlarmClipRuntimeEligibleForSource(sourceID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return false, nil
	}
	var source model.MediaSource
	if err := s.db.Where("id = ?", sourceID).First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	recordingPolicy, _, _, taskRunning, err := s.resolveTaskRecordingPolicyBySourceID(source.ID)
	if err != nil {
		return false, err
	}
	if !taskRunning || normalizeTaskRecordingPolicy(recordingPolicy) != model.RecordingPolicyAlarmClip {
		return false, nil
	}
	if !s.shouldRunAlarmClipBuffer(&source) {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(source.Status), "online") {
		return false, nil
	}
	return true, nil
}

func (s *Server) recoverAlarmClipSessionsOnStartup() error {
	if s == nil || s.db == nil || s.cfg == nil {
		return nil
	}
	if !s.cfg.Server.Recording.AlarmClip.RecoverOnStartup {
		return nil
	}
	sessions := make([]model.AlarmClipSession, 0, 64)
	if err := s.db.
		Where("status IN ?", []string{alarmClipSessionStatusRecording, alarmClipSessionStatusClosing}).
		Order("source_id asc, expected_end_at desc, created_at desc").
		Find(&sessions).Error; err != nil {
		return err
	}
	if len(sessions) == 0 {
		return nil
	}
	seenSource := make(map[string]struct{}, len(sessions))
	now := time.Now()
	settle := time.Duration(s.alarmClipSessionSettleSeconds()) * time.Second
	for _, session := range sessions {
		sessionID := strings.TrimSpace(session.ID)
		sourceID := strings.TrimSpace(session.SourceID)
		if sessionID == "" || sourceID == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(session.Status), alarmClipSessionStatusClosing) {
			if err := s.finalizeAlarmClipSession(sessionID, "recover_closing"); err != nil {
				logutil.Warnf("recover closing alarm clip session failed: session_id=%s err=%v", sessionID, err)
			}
			continue
		}
		if _, exists := seenSource[sourceID]; exists {
			if err := s.finalizeAlarmClipSession(sessionID, "recover_duplicate"); err != nil {
				logutil.Warnf("recover duplicate alarm clip session failed: session_id=%s err=%v", sessionID, err)
			}
			continue
		}
		seenSource[sourceID] = struct{}{}
		eligible, err := s.isAlarmClipRuntimeEligibleForSource(sourceID)
		if err != nil {
			logutil.Warnf("recover check alarm clip session eligibility failed: session_id=%s err=%v", sessionID, err)
			eligible = false
		}
		if !eligible {
			if err := s.finalizeAlarmClipSession(sessionID, "recover_not_eligible"); err != nil {
				logutil.Warnf("recover finalize not-eligible alarm clip session failed: session_id=%s err=%v", sessionID, err)
			}
			continue
		}
		if !session.ExpectedEndAt.Add(settle).After(now) {
			if err := s.finalizeAlarmClipSession(sessionID, "recover_expired"); err != nil {
				logutil.Warnf("recover finalize expired alarm clip session failed: session_id=%s err=%v", sessionID, err)
			}
			continue
		}
		s.scheduleAlarmClipSessionFinalizeBySource(sourceID, session.ExpectedEndAt)
	}
	return nil
}
