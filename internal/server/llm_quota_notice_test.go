package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"maas-box/internal/ai"
	"maas-box/internal/model"
)

func TestAuthMeReturnsLLMQuotaNoticeWhenReached(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.DisableOnTokenLimitExceeded = true
	s.cfg.Server.AI.TotalTokenLimit = 100

	usage := makeLLMUsageCall("llm-quota-notice-auth-me", time.Now(), model.LLMUsageSourceTaskRuntime, 100)
	if err := s.db.Create(&usage).Error; err != nil {
		t.Fatalf("create llm usage failed: %v", err)
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
			LLMQuotaNotice map[string]any `json:"llm_quota_notice"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode auth me response failed: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected auth me response code: %d body=%s", resp.Code, rec.Body.String())
	}
	if len(resp.Data.LLMQuotaNotice) == 0 {
		t.Fatalf("expected llm_quota_notice in auth me response")
	}
	if got := strings.TrimSpace(toString(resp.Data.LLMQuotaNotice["type"])); got != "llm_quota_notice" {
		t.Fatalf("unexpected llm_quota_notice.type: got=%s", got)
	}
	if got := strings.TrimSpace(toString(resp.Data.LLMQuotaNotice["title"])); got != llmTokenQuotaNoticeTitle {
		t.Fatalf("unexpected llm_quota_notice.title: got=%s want=%s", got, llmTokenQuotaNoticeTitle)
	}
	if got := strings.TrimSpace(toString(resp.Data.LLMQuotaNotice["message"])); got != llmTokenQuotaNoticeDefaultBody {
		t.Fatalf("unexpected llm_quota_notice.message: got=%s want=%s", got, llmTokenQuotaNoticeDefaultBody)
	}
}

func TestLLMQuotaNoticeBroadcastsOnceWhenReached(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.DisableOnTokenLimitExceeded = true
	s.cfg.Server.AI.TotalTokenLimit = 100

	engine := s.Engine()
	httpSrv := httptest.NewServer(engine)
	defer httpSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/alerts"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	defer conn.Close()

	totalTokens := 100
	if persisted, err := s.recordLLMUsage(nil, llmUsagePersistRequest{
		Source:     model.LLMUsageSourceTaskRuntime,
		OccurredAt: time.Now(),
		Usage: &ai.LLMUsage{
			CallID:         "llm-quota-notice-broadcast-1",
			CallStatus:     model.LLMUsageStatusSuccess,
			UsageAvailable: true,
			TotalTokens:    &totalTokens,
		},
	}); err != nil {
		t.Fatalf("record llm usage failed: %v", err)
	} else if !persisted {
		t.Fatalf("expected first llm usage to be persisted")
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(msg, &payload); err != nil {
		t.Fatalf("decode websocket payload failed: %v", err)
	}
	if got := strings.TrimSpace(toString(payload["type"])); got != "llm_quota_notice" {
		t.Fatalf("unexpected websocket payload: %+v", payload)
	}

	notice, err := s.loadLLMTokenQuotaNoticeState()
	if err != nil {
		t.Fatalf("load llm quota notice failed: %v", err)
	}
	if notice == nil {
		t.Fatalf("expected llm quota notice state to be saved")
	}
	firstNoticeID := notice.NoticeID

	extraTokens := 10
	if persisted, err := s.recordLLMUsage(nil, llmUsagePersistRequest{
		Source:     model.LLMUsageSourceTaskRuntime,
		OccurredAt: time.Now().Add(time.Second),
		Usage: &ai.LLMUsage{
			CallID:         "llm-quota-notice-broadcast-2",
			CallStatus:     model.LLMUsageStatusSuccess,
			UsageAvailable: true,
			TotalTokens:    &extraTokens,
		},
	}); err != nil {
		t.Fatalf("record second llm usage failed: %v", err)
	} else if !persisted {
		t.Fatalf("expected second llm usage to be persisted")
	}

	_ = conn.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected no duplicated llm quota notice broadcast")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("expected websocket timeout after first llm quota notice, got: %v", err)
	}

	notice, err = s.loadLLMTokenQuotaNoticeState()
	if err != nil {
		t.Fatalf("reload llm quota notice failed: %v", err)
	}
	if notice == nil {
		t.Fatalf("expected llm quota notice state after second usage")
	}
	if notice.NoticeID != firstNoticeID {
		t.Fatalf("expected stable llm quota notice id, got=%s want=%s", notice.NoticeID, firstNoticeID)
	}
	if notice.UsedTokens != 110 {
		t.Fatalf("expected llm quota notice used_tokens=110, got=%d", notice.UsedTokens)
	}
}

func TestPendingLLMQuotaNoticeClearsWhenLimitLifted(t *testing.T) {
	s := newFocusedTestServer(t)
	s.cfg.Server.AI.DisableOnTokenLimitExceeded = true
	s.cfg.Server.AI.TotalTokenLimit = 100

	usage := makeLLMUsageCall("llm-quota-notice-clear", time.Now(), model.LLMUsageSourceTaskRuntime, 100)
	if err := s.db.Create(&usage).Error; err != nil {
		t.Fatalf("create llm usage failed: %v", err)
	}

	notice, err := s.pendingLLMTokenQuotaNotice(time.Now())
	if err != nil {
		t.Fatalf("load pending llm quota notice failed: %v", err)
	}
	if notice == nil {
		t.Fatalf("expected llm quota notice before lifting limit")
	}

	s.cfg.Server.AI.TotalTokenLimit = 1000
	notice, err = s.pendingLLMTokenQuotaNotice(time.Now())
	if err != nil {
		t.Fatalf("load pending llm quota notice after lifting limit failed: %v", err)
	}
	if notice != nil {
		t.Fatalf("expected llm quota notice cleared after lifting limit, got %+v", notice)
	}

	stored, err := s.loadLLMTokenQuotaNoticeState()
	if err != nil {
		t.Fatalf("load stored llm quota notice failed: %v", err)
	}
	if stored != nil {
		t.Fatalf("expected stored llm quota notice cleared, got %+v", stored)
	}
}

func toString(value any) string {
	return strings.TrimSpace(fmt.Sprint(value))
}
