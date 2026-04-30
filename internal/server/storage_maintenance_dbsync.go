package server

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"maas-box/internal/model"
)

const cleanupDBChunkSize = 200

func (s *Server) clearAlgorithmTestMediaPaths(paths []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	normalized := normalizeCleanupRelPaths(paths)
	if len(normalized) == 0 {
		return nil
	}
	for _, chunk := range chunkStrings(normalized, cleanupDBChunkSize) {
		if len(chunk) == 0 {
			continue
		}
		if err := s.db.Model(&model.AlgorithmTestRecord{}).
			Where("media_path IN ? OR image_path IN ?", chunk, chunk).
			Updates(map[string]any{
				"media_path": "",
				"image_path": "",
			}).Error; err != nil {
			return err
		}
		if err := s.clearAlgorithmTestJobItemMediaPaths(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) clearAlgorithmTestJobItemMediaPaths(paths []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	normalized := normalizeCleanupRelPaths(paths)
	if len(normalized) == 0 {
		return nil
	}
	for _, chunk := range chunkStrings(normalized, cleanupDBChunkSize) {
		if len(chunk) == 0 {
			continue
		}
		if err := s.db.Model(&model.AlgorithmTestJobItem{}).
			Where("media_path IN ?", chunk).
			Update("media_path", "").Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) clearEventSnapshotPaths(paths []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	normalized := normalizeCleanupRelPaths(paths)
	if len(normalized) == 0 {
		return nil
	}
	for _, chunk := range chunkStrings(normalized, cleanupDBChunkSize) {
		if len(chunk) == 0 {
			continue
		}
		if err := s.db.Model(&model.AlarmEvent{}).
			Where("snapshot_path IN ?", chunk).
			Update("snapshot_path", "").Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) clearEventClipFields(eventIDs []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	eventIDs = uniqueStrings(eventIDs)
	if len(eventIDs) == 0 {
		return nil
	}
	for _, chunk := range chunkStrings(eventIDs, cleanupDBChunkSize) {
		if len(chunk) == 0 {
			continue
		}
		if err := s.db.Model(&model.AlarmEvent{}).
			Where("id IN ?", chunk).
			Updates(map[string]any{
				"clip_ready":      false,
				"clip_path":       "",
				"clip_files_json": "[]",
				"updated_at":      time.Now(),
			}).Error; err != nil {
			return err
		}
	}
	return nil
}

func clipPathFromRemovedAlarmDir(relPath string) string {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	relPath = strings.Trim(relPath, "/")
	if relPath == "" {
		return ""
	}
	parts := strings.Split(relPath, "/")
	for i, part := range parts {
		if part != alarmClipDirName {
			continue
		}
		if i+1 >= len(parts) {
			return ""
		}
		return filepath.ToSlash(strings.Join(parts[i:], "/"))
	}
	return ""
}

func (s *Server) clearEventClipFieldsByRemovedDirs(dirs []alarmClipEventDir) error {
	if s == nil || s.db == nil || len(dirs) == 0 {
		return nil
	}
	now := time.Now()
	for _, item := range dirs {
		deviceID := strings.TrimSpace(item.DeviceID)
		clipPath := clipPathFromRemovedAlarmDir(item.RelPath)
		if deviceID == "" || clipPath == "" {
			continue
		}
		if err := s.db.Model(&model.AlarmEvent{}).
			Where("device_id = ? AND (clip_path = ? OR clip_path LIKE ?)", deviceID, clipPath, clipPath+"/%").
			Updates(map[string]any{
				"clip_ready":      true,
				"clip_path":       "",
				"clip_files_json": "[]",
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		if err := s.db.Model(&model.AlarmClipSession{}).
			Where("source_id = ? AND (clip_path = ? OR clip_path LIKE ?)", deviceID, clipPath, clipPath+"/%").
			Updates(map[string]any{
				"clip_path":       "",
				"clip_files_json": "[]",
				"status":          alarmClipSessionStatusClosedEmpty,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) pruneEventClipFilesByRemovedPaths(paths []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	normalized := normalizeCleanupRelPaths(paths)
	if len(normalized) == 0 {
		return nil
	}
	removedSet := make(map[string]struct{}, len(normalized))
	for _, item := range normalized {
		removedSet[item] = struct{}{}
	}

	var events []model.AlarmEvent
	if err := s.db.Select("id", "clip_ready", "clip_path", "clip_files_json").
		Where("clip_files_json <> '' AND clip_files_json <> '[]'").
		Find(&events).Error; err != nil {
		return err
	}
	now := time.Now()
	for _, event := range events {
		files := decodeEventClipFiles(event.ClipFilesJSON)
		if len(files) == 0 {
			continue
		}
		kept := make([]string, 0, len(files))
		changed := false
		for _, item := range files {
			rel := filepath.ToSlash(filepath.Clean(strings.TrimSpace(item)))
			rel = strings.TrimPrefix(rel, "./")
			if rel == "" || rel == "." {
				changed = true
				continue
			}
			if _, removed := removedSet[rel]; removed {
				changed = true
				continue
			}
			kept = append(kept, rel)
		}
		if !changed {
			continue
		}
		kept = uniqueStrings(kept)
		clipPath := strings.TrimSpace(event.ClipPath)
		clipFilesJSON := "[]"
		if len(kept) > 0 {
			body, _ := json.Marshal(kept)
			clipFilesJSON = string(body)
		} else {
			clipPath = ""
		}
		if err := s.db.Model(&model.AlarmEvent{}).
			Where("id = ?", event.ID).
			Updates(map[string]any{
				"clip_ready":      true,
				"clip_path":       clipPath,
				"clip_files_json": clipFilesJSON,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
	}
	var sessions []model.AlarmClipSession
	if err := s.db.Select("id", "status", "clip_path", "clip_files_json").
		Where("clip_files_json <> '' AND clip_files_json <> '[]'").
		Find(&sessions).Error; err != nil {
		return err
	}
	for _, session := range sessions {
		files := decodeEventClipFiles(session.ClipFilesJSON)
		if len(files) == 0 {
			continue
		}
		kept := make([]string, 0, len(files))
		changed := false
		for _, item := range files {
			rel := filepath.ToSlash(filepath.Clean(strings.TrimSpace(item)))
			rel = strings.TrimPrefix(rel, "./")
			if rel == "" || rel == "." {
				changed = true
				continue
			}
			if _, removed := removedSet[rel]; removed {
				changed = true
				continue
			}
			kept = append(kept, rel)
		}
		if !changed {
			continue
		}
		kept = uniqueStrings(kept)
		clipPath := strings.TrimSpace(session.ClipPath)
		clipFilesJSON := "[]"
		nextStatus := strings.TrimSpace(session.Status)
		if len(kept) > 0 {
			body, _ := json.Marshal(kept)
			clipFilesJSON = string(body)
		} else {
			clipPath = ""
			nextStatus = alarmClipSessionStatusClosedEmpty
		}
		if err := s.db.Model(&model.AlarmClipSession{}).
			Where("id = ?", session.ID).
			Updates(map[string]any{
				"status":          nextStatus,
				"clip_path":       clipPath,
				"clip_files_json": clipFilesJSON,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
	}
	return nil
}

func decodeEventClipFiles(raw string) []string {
	text := strings.TrimSpace(raw)
	if text == "" || text == "[]" {
		return nil
	}
	var files []string
	if err := json.Unmarshal([]byte(text), &files); err != nil || len(files) == 0 {
		return nil
	}
	return normalizeCleanupRelPaths(files)
}

func (s *Server) clearDeviceSnapshotURLs(deviceIDs []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	deviceIDs = uniqueStrings(deviceIDs)
	if len(deviceIDs) == 0 {
		return nil
	}
	for _, chunk := range chunkStrings(deviceIDs, cleanupDBChunkSize) {
		if len(chunk) == 0 {
			continue
		}
		if err := s.db.Model(&model.MediaSource{}).
			Where("id IN ?", chunk).
			Update("snapshot_url", "").Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) clearDeviceSnapshotURLsByRelPaths(paths []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	normalized := normalizeCleanupRelPaths(paths)
	if len(normalized) == 0 {
		return nil
	}
	removed := make(map[string]struct{}, len(normalized))
	for _, item := range normalized {
		removed[item] = struct{}{}
	}
	type snapshotRef struct {
		ID          string
		SnapshotURL string
	}
	rows := make([]snapshotRef, 0, len(normalized))
	if err := s.db.Model(&model.MediaSource{}).
		Select("id", "snapshot_url").
		Where("snapshot_url <> ''").
		Find(&rows).Error; err != nil {
		return err
	}
	deviceIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		relPath := normalizeDeviceSnapshotRelPath(row.SnapshotURL)
		if relPath == "" {
			continue
		}
		if _, ok := removed[relPath]; ok {
			deviceIDs = append(deviceIDs, strings.TrimSpace(row.ID))
		}
	}
	return s.clearDeviceSnapshotURLs(deviceIDs)
}

func (s *Server) clearLocalDeviceSnapshotURLs() error {
	if s == nil || s.db == nil {
		return nil
	}
	type snapshotRef struct {
		ID          string
		SnapshotURL string
	}
	rows := make([]snapshotRef, 0, 128)
	if err := s.db.Model(&model.MediaSource{}).
		Select("id", "snapshot_url").
		Where("snapshot_url <> ''").
		Find(&rows).Error; err != nil {
		return err
	}
	deviceIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if normalizeDeviceSnapshotRelPath(row.SnapshotURL) == "" {
			continue
		}
		deviceIDs = append(deviceIDs, strings.TrimSpace(row.ID))
	}
	return s.clearDeviceSnapshotURLs(deviceIDs)
}

func (s *Server) clearAlgorithmCoverURLsByRelPaths(paths []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	normalized := normalizeCleanupRelPaths(paths)
	if len(normalized) == 0 {
		return nil
	}
	removed := make(map[string]struct{}, len(normalized))
	for _, item := range normalized {
		removed[item] = struct{}{}
	}
	type coverRef struct {
		ID       string
		ImageURL string
	}
	rows := make([]coverRef, 0, len(normalized))
	if err := s.db.Model(&model.Algorithm{}).
		Select("id", "image_url").
		Where("image_url <> ''").
		Find(&rows).Error; err != nil {
		return err
	}
	algorithmIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		relPath := normalizeCoverImageRelPath(row.ImageURL)
		if relPath == "" {
			continue
		}
		if _, ok := removed[relPath]; ok {
			algorithmIDs = append(algorithmIDs, strings.TrimSpace(row.ID))
		}
	}
	algorithmIDs = uniqueStrings(algorithmIDs)
	if len(algorithmIDs) == 0 {
		return nil
	}
	for _, chunk := range chunkStrings(algorithmIDs, cleanupDBChunkSize) {
		if len(chunk) == 0 {
			continue
		}
		if err := s.db.Model(&model.Algorithm{}).
			Where("id IN ?", chunk).
			Update("image_url", "").Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) clearLocalAlgorithmCoverURLs() error {
	if s == nil || s.db == nil {
		return nil
	}
	type coverRef struct {
		ID       string
		ImageURL string
	}
	rows := make([]coverRef, 0, 128)
	if err := s.db.Model(&model.Algorithm{}).
		Select("id", "image_url").
		Where("image_url <> ''").
		Find(&rows).Error; err != nil {
		return err
	}
	algorithmIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if normalizeCoverImageRelPath(row.ImageURL) == "" {
			continue
		}
		algorithmIDs = append(algorithmIDs, strings.TrimSpace(row.ID))
	}
	algorithmIDs = uniqueStrings(algorithmIDs)
	if len(algorithmIDs) == 0 {
		return nil
	}
	for _, chunk := range chunkStrings(algorithmIDs, cleanupDBChunkSize) {
		if len(chunk) == 0 {
			continue
		}
		if err := s.db.Model(&model.Algorithm{}).
			Where("id IN ?", chunk).
			Update("image_url", "").Error; err != nil {
			return err
		}
	}
	return nil
}

func normalizeCleanupRelPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, item := range paths {
		item = filepath.ToSlash(filepath.Clean(strings.TrimSpace(item)))
		item = strings.TrimPrefix(item, "./")
		if item == "" || item == "." {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func chunkStrings(items []string, size int) [][]string {
	if len(items) == 0 {
		return nil
	}
	if size <= 0 {
		size = len(items)
	}
	chunks := make([][]string, 0, (len(items)+size-1)/size)
	for start := 0; start < len(items); start += size {
		end := start + size
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[start:end])
	}
	return chunks
}
