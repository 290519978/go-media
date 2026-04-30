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

func TestAlgorithmPromptVersionUniquePerAlgorithmIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	algA := model.Algorithm{
		ID:              "alg-prompt-unique-a",
		Code:            "ALG_PROMPT_UNIQUE_A",
		Name:            "Prompt Unique A",
		Mode:            model.AlgorithmModeLarge,
		Enabled:         true,
		ModelProviderID: "provider-not-required-in-db-fixture",
	}
	algB := model.Algorithm{
		ID:              "alg-prompt-unique-b",
		Code:            "ALG_PROMPT_UNIQUE_B",
		Name:            "Prompt Unique B",
		Mode:            model.AlgorithmModeLarge,
		Enabled:         true,
		ModelProviderID: "provider-not-required-in-db-fixture",
	}
	if err := s.db.Create(&algA).Error; err != nil {
		t.Fatalf("create algorithm A failed: %v", err)
	}
	if err := s.db.Create(&algB).Error; err != nil {
		t.Fatalf("create algorithm B failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")

	createPayload := map[string]any{
		"version":   "v1",
		"prompt":    "first prompt",
		"is_active": false,
	}
	rec1 := performAuthedJSONRequest(t, engine, token, http.MethodPost, "/api/v1/algorithms/"+algA.ID+"/prompts", createPayload)
	if rec1.Code != http.StatusOK {
		t.Fatalf("create first prompt failed: status=%d body=%s", rec1.Code, rec1.Body.String())
	}

	duplicatePayload := map[string]any{
		"version":   " v1 ",
		"prompt":    "duplicate prompt",
		"is_active": false,
	}
	rec2 := performAuthedJSONRequest(t, engine, token, http.MethodPost, "/api/v1/algorithms/"+algA.ID+"/prompts", duplicatePayload)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for duplicate version, got=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec2.Body.String()), "version already exists in this algorithm") {
		t.Fatalf("unexpected duplicate error message: %s", rec2.Body.String())
	}

	rec3 := performAuthedJSONRequest(t, engine, token, http.MethodPost, "/api/v1/algorithms/"+algB.ID+"/prompts", createPayload)
	if rec3.Code != http.StatusOK {
		t.Fatalf("create same version in another algorithm failed: status=%d body=%s", rec3.Code, rec3.Body.String())
	}
}

func TestAlgorithmPromptUpdateVersionConflictIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	alg := model.Algorithm{
		ID:              "alg-prompt-update-conflict",
		Code:            "ALG_PROMPT_UPDATE_CONFLICT",
		Name:            "Prompt Update Conflict",
		Mode:            model.AlgorithmModeLarge,
		Enabled:         true,
		ModelProviderID: "provider-not-required-in-db-fixture",
	}
	if err := s.db.Create(&alg).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	promptA := model.AlgorithmPromptVersion{
		ID:          "prompt-conflict-a",
		AlgorithmID: alg.ID,
		Version:     "v1",
		Prompt:      "prompt a",
		IsActive:    false,
	}
	promptB := model.AlgorithmPromptVersion{
		ID:          "prompt-conflict-b",
		AlgorithmID: alg.ID,
		Version:     "v2",
		Prompt:      "prompt b",
		IsActive:    false,
	}
	if err := s.db.Create(&promptA).Error; err != nil {
		t.Fatalf("create prompt A failed: %v", err)
	}
	if err := s.db.Create(&promptB).Error; err != nil {
		t.Fatalf("create prompt B failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")
	updatePayload := map[string]any{
		"version":   "v1",
		"prompt":    "updated prompt b",
		"is_active": false,
	}
	rec := performAuthedJSONRequest(
		t,
		engine,
		token,
		http.MethodPut,
		"/api/v1/algorithms/"+alg.ID+"/prompts/"+promptB.ID,
		updatePayload,
	)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for update conflict, got=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "version already exists in this algorithm") {
		t.Fatalf("unexpected update conflict message: %s", rec.Body.String())
	}
}

func TestAlgorithmPromptDeletePolicyIntegration(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()

	alg := model.Algorithm{
		ID:              "alg-prompt-delete-policy",
		Code:            "ALG_PROMPT_DELETE_POLICY",
		Name:            "Prompt Delete Policy",
		Mode:            model.AlgorithmModeLarge,
		Enabled:         true,
		ModelProviderID: "provider-not-required-in-db-fixture",
	}
	if err := s.db.Create(&alg).Error; err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	inactivePrompt := model.AlgorithmPromptVersion{
		ID:          "prompt-delete-inactive",
		AlgorithmID: alg.ID,
		Version:     "v1",
		Prompt:      "inactive prompt",
		IsActive:    false,
	}
	activePrompt := model.AlgorithmPromptVersion{
		ID:          "prompt-delete-active",
		AlgorithmID: alg.ID,
		Version:     "v2",
		Prompt:      "active prompt",
		IsActive:    true,
	}
	if err := s.db.Create(&inactivePrompt).Error; err != nil {
		t.Fatalf("create inactive prompt failed: %v", err)
	}
	if err := s.db.Create(&activePrompt).Error; err != nil {
		t.Fatalf("create active prompt failed: %v", err)
	}

	token := loginToken(t, engine, "admin", "admin")

	rec1 := performAuthedJSONRequest(
		t,
		engine,
		token,
		http.MethodDelete,
		"/api/v1/algorithms/"+alg.ID+"/prompts/"+inactivePrompt.ID,
		nil,
	)
	if rec1.Code != http.StatusOK {
		t.Fatalf("delete inactive prompt failed: status=%d body=%s", rec1.Code, rec1.Body.String())
	}
	var count int64
	if err := s.db.Model(&model.AlgorithmPromptVersion{}).Where("id = ?", inactivePrompt.ID).Count(&count).Error; err != nil {
		t.Fatalf("query inactive prompt failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("inactive prompt should be deleted, count=%d", count)
	}

	rec2 := performAuthedJSONRequest(
		t,
		engine,
		token,
		http.MethodDelete,
		"/api/v1/algorithms/"+alg.ID+"/prompts/"+activePrompt.ID,
		nil,
	)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for deleting active prompt, got=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec2.Body.String()), "active prompt version cannot be deleted") {
		t.Fatalf("unexpected delete active message: %s", rec2.Body.String())
	}

	rec3 := performAuthedJSONRequest(
		t,
		engine,
		token,
		http.MethodDelete,
		"/api/v1/algorithms/"+alg.ID+"/prompts/prompt-delete-missing",
		nil,
	)
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing prompt, got=%d body=%s", rec3.Code, rec3.Body.String())
	}
}

func performAuthedJSONRequest(
	t *testing.T,
	engine http.Handler,
	token string,
	method string,
	path string,
	payload map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}
