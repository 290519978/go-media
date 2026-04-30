package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"maas-box/internal/model"
)

func TestAlgorithmUpdateReplaceCoverDeletesOldFileIntegration(t *testing.T) {
	root := t.TempDir()
	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		engine := s.Engine()
		token := loginToken(t, engine, "admin", "admin")

		oldRel := filepath.ToSlash(filepath.Join("20260315", "old-cover.jpg"))
		newRel := filepath.ToSlash(filepath.Join("20260315", "new-cover.jpg"))
		oldPath := filepath.Join(root, "configs", "cover", filepath.FromSlash(oldRel))
		newPath := filepath.Join(root, "configs", "cover", filepath.FromSlash(newRel))
		mustWriteCoverTestFile(t, oldPath)
		mustWriteCoverTestFile(t, newPath)

		algorithm := model.Algorithm{
			ID:                "alg-cover-update-delete-old",
			Code:              "ALG_COVER_DELETE_OLD",
			Name:              "Cover Delete Old",
			Description:       "cover update test",
			ImageURL:          coverTestURL(oldRel),
			Scene:             "scene",
			Category:          "category",
			Mode:              model.AlgorithmModeHybrid,
			Enabled:           true,
			SmallModelLabel:   "fire",
			DetectMode:        model.AlgorithmDetectModeHybrid,
			YoloThreshold:     0.5,
			IOUThreshold:      0.8,
			LabelsTriggerMode: model.LabelsTriggerModeAny,
		}
		if err := s.db.Create(&algorithm).Error; err != nil {
			t.Fatalf("create algorithm failed: %v", err)
		}

		payload := map[string]any{
			"code":                algorithm.Code,
			"name":                algorithm.Name,
			"description":         algorithm.Description,
			"image_url":           coverTestURL(newRel),
			"scene":               algorithm.Scene,
			"category":            algorithm.Category,
			"enabled":             algorithm.Enabled,
			"detect_mode":         model.AlgorithmDetectModeHybrid,
			"small_model_label":   []string{"fire"},
			"labels_trigger_mode": model.LabelsTriggerModeAny,
			"yolo_threshold":      0.5,
			"iou_threshold":       0.8,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/algorithms/"+algorithm.ID, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("update algorithm failed: status=%d body=%s", rec.Code, rec.Body.String())
		}

		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Fatalf("old cover should be removed after replace, stat err=%v", err)
		}
		if _, err := os.Stat(newPath); err != nil {
			t.Fatalf("new cover should exist, stat err=%v", err)
		}
	})
}

func TestAlgorithmUpdateReplaceCoverKeepsOldFileWhenStillReferencedIntegration(t *testing.T) {
	root := t.TempDir()
	withWorkingDir(t, root, func() {
		s := newFocusedTestServer(t)
		engine := s.Engine()
		token := loginToken(t, engine, "admin", "admin")

		oldRel := filepath.ToSlash(filepath.Join("20260315", "shared-cover.jpg"))
		newRel := filepath.ToSlash(filepath.Join("20260315", "updated-cover.jpg"))
		oldPath := filepath.Join(root, "configs", "cover", filepath.FromSlash(oldRel))
		newPath := filepath.Join(root, "configs", "cover", filepath.FromSlash(newRel))
		mustWriteCoverTestFile(t, oldPath)
		mustWriteCoverTestFile(t, newPath)

		algorithmA := model.Algorithm{
			ID:                "alg-cover-update-keep-old-a",
			Code:              "ALG_COVER_KEEP_A",
			Name:              "Cover Keep A",
			Description:       "cover update test",
			ImageURL:          coverTestURL(oldRel),
			Scene:             "scene",
			Category:          "category",
			Mode:              model.AlgorithmModeHybrid,
			Enabled:           true,
			SmallModelLabel:   "fire",
			DetectMode:        model.AlgorithmDetectModeHybrid,
			YoloThreshold:     0.5,
			IOUThreshold:      0.8,
			LabelsTriggerMode: model.LabelsTriggerModeAny,
		}
		algorithmB := model.Algorithm{
			ID:                "alg-cover-update-keep-old-b",
			Code:              "ALG_COVER_KEEP_B",
			Name:              "Cover Keep B",
			Description:       "cover update test",
			ImageURL:          coverTestURL(oldRel),
			Scene:             "scene",
			Category:          "category",
			Mode:              model.AlgorithmModeHybrid,
			Enabled:           true,
			SmallModelLabel:   "fire",
			DetectMode:        model.AlgorithmDetectModeHybrid,
			YoloThreshold:     0.5,
			IOUThreshold:      0.8,
			LabelsTriggerMode: model.LabelsTriggerModeAny,
		}
		if err := s.db.Create(&algorithmA).Error; err != nil {
			t.Fatalf("create algorithm A failed: %v", err)
		}
		if err := s.db.Create(&algorithmB).Error; err != nil {
			t.Fatalf("create algorithm B failed: %v", err)
		}

		payload := map[string]any{
			"code":                algorithmA.Code,
			"name":                algorithmA.Name,
			"description":         algorithmA.Description,
			"image_url":           coverTestURL(newRel),
			"scene":               algorithmA.Scene,
			"category":            algorithmA.Category,
			"enabled":             algorithmA.Enabled,
			"detect_mode":         model.AlgorithmDetectModeHybrid,
			"small_model_label":   []string{"fire"},
			"labels_trigger_mode": model.LabelsTriggerModeAny,
			"yolo_threshold":      0.5,
			"iou_threshold":       0.8,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/algorithms/"+algorithmA.ID, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("update algorithm failed: status=%d body=%s", rec.Code, rec.Body.String())
		}

		if _, err := os.Stat(oldPath); err != nil {
			t.Fatalf("old cover should be kept because another algorithm still references it: %v", err)
		}
		if _, err := os.Stat(newPath); err != nil {
			t.Fatalf("new cover should exist, stat err=%v", err)
		}
	})
}

func coverTestURL(rel string) string {
	return "/api/v1/algorithms/cover/" + filepath.ToSlash(rel)
}

func mustWriteCoverTestFile(t *testing.T, fullPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir cover dir failed: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("cover-test"), 0o644); err != nil {
		t.Fatalf("write cover test file failed: %v", err)
	}
}
