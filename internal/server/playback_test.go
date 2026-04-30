package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newMockZLMMediaListServer(t *testing.T, items []map[string]string, statusCode int, payload map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/index/api/getMediaList" {
			http.NotFound(w, r)
			return
		}
		if statusCode > 0 {
			w.WriteHeader(statusCode)
		}
		if payload != nil {
			_ = json.NewEncoder(w).Encode(payload)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "ok",
			"data": items,
		})
	}))
}

func TestGetPlaybackStreamStatusReturnsActive(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	zlmServer := newMockZLMMediaListServer(t, []map[string]string{
		{
			"schema": "rtsp",
			"app":    "live",
			"stream": "device_001",
		},
	}, 0, nil)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playback/stream-status?app=live&stream=device_001", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream status failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Active bool   `json:"active"`
			App    string `json:"app"`
			Stream string `json:"stream"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp.Code != 0 || !resp.Data.Active {
		t.Fatalf("expected active=true, got body=%s", rec.Body.String())
	}
}

func TestGetPlaybackStreamStatusReturnsInactive(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	zlmServer := newMockZLMMediaListServer(t, []map[string]string{
		{
			"schema": "rtsp",
			"app":    "live",
			"stream": "device_other",
		},
	}, 0, nil)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playback/stream-status?app=live&stream=device_001", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream status failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Active bool `json:"active"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp.Code != 0 || resp.Data.Active {
		t.Fatalf("expected active=false, got body=%s", rec.Body.String())
	}
}

func TestGetPlaybackStreamStatusReturnsBadGatewayWhenZLMCheckFails(t *testing.T) {
	s := newFocusedTestServer(t)
	engine := s.Engine()
	zlmServer := newMockZLMMediaListServer(t, nil, http.StatusInternalServerError, nil)
	defer zlmServer.Close()
	s.cfg.Server.ZLM.APIURL = zlmServer.URL

	token := loginToken(t, engine, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playback/stream-status?app=live&stream=device_001", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got=%d body=%s", rec.Code, rec.Body.String())
	}
}
