package server

import (
	"testing"

	"maas-box/internal/config"
)

func TestApplyZLMOutputPolicy(t *testing.T) {
	input := map[string]string{
		"webrtc":     "webrtc://test",
		"ws_flv":     "ws://test/live/test.live.flv",
		"http_flv":   "http://test/live/test.live.flv",
		"hls":        "http://test/live/test/hls.m3u8",
		"rtsp":       "rtsp://test/live/test",
		"rtmp":       "rtmp://test/live/test",
		"zlm_app":    "live",
		"zlm_stream": "test",
	}
	policy := config.ZLMOutputConfig{
		EnableWebRTC:   true,
		EnableWSFLV:    true,
		EnableHTTPFLV:  false,
		EnableHLS:      false,
		WebFallback:    "ws_flv",
	}
	got := applyZLMOutputPolicy(input, policy)
	if got["rtsp"] == "" {
		t.Fatalf("rtsp should always be kept")
	}
	if got["webrtc"] == "" || got["ws_flv"] == "" {
		t.Fatalf("webrtc/ws_flv should be kept by default policy")
	}
	if got["http_flv"] != "" {
		t.Fatalf("http_flv should be removed when disabled")
	}
	if got["hls"] != "" {
		t.Fatalf("hls should be removed when disabled")
	}
	if got["rtmp"] == "" {
		t.Fatalf("rtmp should always be kept")
	}
	if got["zlm_app"] != "live" || got["zlm_stream"] != "test" {
		t.Fatalf("zlm app/stream should be preserved")
	}
}

func TestPickPreviewPlayURLWithFallback(t *testing.T) {
	s := &Server{
		cfg: &config.Config{
			Server: config.ServerConfig{
				ZLM: config.ZLMConfig{
					Output: config.ZLMOutputConfig{
						WebFallback: "hls",
					},
				},
			},
		},
	}

	got := s.pickPreviewPlayURL(map[string]string{
		"hls":    "http://test/live/test/hls.m3u8",
		"ws_flv": "ws://test/live/test.live.flv",
	})
	if got != "http://test/live/test/hls.m3u8" {
		t.Fatalf("expected fallback hls to be selected when webrtc missing, got=%s", got)
	}

	got = s.pickPreviewPlayURL(map[string]string{
		"webrtc": "http://test/index/api/webrtc?app=live&stream=test&type=play",
		"hls":    "http://test/live/test/hls.m3u8",
	})
	if got != "http://test/index/api/webrtc?app=live&stream=test&type=play" {
		t.Fatalf("expected webrtc to be selected first, got=%s", got)
	}
}
