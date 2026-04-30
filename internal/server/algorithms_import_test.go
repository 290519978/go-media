package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"maas-box/internal/model"
)

func TestAlgorithmImportUpsertIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	existing := model.Algorithm{
		ID:                "alg-import-existing",
		Code:              "ALG_IMPORT_EXIST",
		Name:              "Import Existing",
		Description:       "old description",
		Scene:             "old-scene",
		Category:          "old-category",
		Mode:              model.AlgorithmModeHybrid,
		Enabled:           true,
		SmallModelLabel:   "fire",
		DetectMode:        model.AlgorithmDetectModeHybrid,
		YoloThreshold:     0.5,
		IOUThreshold:      0.8,
		LabelsTriggerMode: model.LabelsTriggerModeAny,
	}
	if err := s.db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing algorithm failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	payload := []map[string]any{
		{
			"code":                "ALG_IMPORT_NEW",
			"name":                "Import New",
			"description":         "new description",
			"scene":               "factory",
			"category":            "safety",
			"enabled":             true,
			"detect_mode":         model.AlgorithmDetectModeLLMOnly,
			"labels_trigger_mode": model.LabelsTriggerModeAny,
			"yolo_threshold":      0.52,
			"iou_threshold":       0.81,
		},
		{
			"code":                "ALG_IMPORT_EXIST",
			"name":                "Import Existing Updated",
			"description":         "updated description",
			"scene":               "warehouse",
			"category":            "security",
			"enabled":             false,
			"detect_mode":         model.AlgorithmDetectModeHybrid,
			"small_model_label":   []string{"smoke", "fire"},
			"labels_trigger_mode": model.LabelsTriggerModeAll,
			"yolo_threshold":      0.66,
			"iou_threshold":       0.91,
		},
		{
			"code":              "bad-code",
			"name":              "Import Invalid",
			"enabled":           true,
			"detect_mode":       model.AlgorithmDetectModeSmallOnly,
			"small_model_label": []string{"person"},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/import", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import algorithms failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Total   int `json:"total"`
			Created int `json:"created"`
			Updated int `json:"updated"`
			Failed  int `json:"failed"`
			Errors  []struct {
				Index   int    `json:"index"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode import response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected response code 0, got=%d body=%s", resp.Code, rec.Body.String())
	}
	if resp.Data.Total != 3 || resp.Data.Created != 1 || resp.Data.Updated != 1 || resp.Data.Failed != 1 {
		t.Fatalf("unexpected summary: %+v", resp.Data)
	}
	if len(resp.Data.Errors) != 1 {
		t.Fatalf("expected 1 item error, got=%d", len(resp.Data.Errors))
	}
	if resp.Data.Errors[0].Index != 3 {
		t.Fatalf("expected failed index=3, got=%d", resp.Data.Errors[0].Index)
	}
	if !strings.Contains(strings.ToLower(resp.Data.Errors[0].Message), "code format invalid") {
		t.Fatalf("unexpected import error message: %+v", resp.Data.Errors[0])
	}

	var created model.Algorithm
	if err := s.db.Where("code = ?", "ALG_IMPORT_NEW").First(&created).Error; err != nil {
		t.Fatalf("query created algorithm failed: %v", err)
	}
	if created.Name != "Import New" || created.DetectMode != model.AlgorithmDetectModeLLMOnly {
		t.Fatalf("created algorithm mismatch: %+v", created)
	}

	var updated model.Algorithm
	if err := s.db.Where("id = ?", existing.ID).First(&updated).Error; err != nil {
		t.Fatalf("query updated algorithm failed: %v", err)
	}
	if updated.Name != "Import Existing Updated" {
		t.Fatalf("existing algorithm name not updated: %s", updated.Name)
	}
	if updated.Enabled {
		t.Fatalf("existing algorithm enabled should be false")
	}
	if updated.LabelsTriggerMode != model.LabelsTriggerModeAll {
		t.Fatalf("labels trigger mode not updated: %s", updated.LabelsTriggerMode)
	}
	if !strings.Contains(updated.SmallModelLabel, "smoke") || !strings.Contains(updated.SmallModelLabel, "fire") {
		t.Fatalf("small model labels not updated: %s", updated.SmallModelLabel)
	}
}

func TestAlgorithmImportEmptyPayloadIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	token := loginToken(t, engine, "admin", "admin")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/import", bytes.NewReader([]byte(`[]`)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty payload, got=%d body=%s", rec.Code, rec.Body.String())
	}
}
