package server

import (
	"net"
	"strings"
	"testing"

	"maas-box/internal/config"
	"maas-box/internal/model"
)

func TestValidateRecordingMode(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	if err := s.validateRecordingMode(true, "on_alarm"); err == nil {
		t.Fatal("expected on_alarm mode to fail")
	}
	if err := s.validateRecordingMode(true, "manual"); err == nil {
		t.Fatal("expected manual mode to fail")
	}
	if err := s.validateRecordingMode(true, "continuous"); err == nil {
		t.Fatal("expected continuous mode to fail")
	}
	if err := s.validateRecordingMode(false, "continuous"); err != nil {
		t.Fatalf("disabled recording should ignore mode, got: %v", err)
	}

	// 显式放开后才允许 continuous。
	s.cfg.Server.Recording.AllowContinuous = true
	if err := s.validateRecordingMode(true, "continuous"); err != nil {
		t.Fatalf("expected continuous mode to pass when enabled by policy, got: %v", err)
	}
}

func TestParsePortList(t *testing.T) {
	got := parsePortList("8554,554,554,abc,70000,1935")
	if len(got) != 3 {
		t.Fatalf("unexpected port count: %d", len(got))
	}
	if got[0] != 554 || got[1] != 1935 || got[2] != 8554 {
		t.Fatalf("unexpected ports order/content: %v", got)
	}
}

func TestExpandIPv4Hosts(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("192.168.10.0/30")
	if err != nil {
		t.Fatalf("parse cidr failed: %v", err)
	}
	hosts, truncated, err := expandIPv4Hosts(ipNet, 10)
	if err != nil {
		t.Fatalf("expand hosts failed: %v", err)
	}
	if truncated {
		t.Fatalf("unexpected truncated result")
	}
	if len(hosts) != 2 {
		t.Fatalf("unexpected host size: %d", len(hosts))
	}
	if hosts[0] != "192.168.10.1" || hosts[1] != "192.168.10.2" {
		t.Fatalf("unexpected hosts: %v", hosts)
	}

	hosts2, truncated2, err := expandIPv4Hosts(ipNet, 1)
	if err != nil {
		t.Fatalf("expand hosts with cap failed: %v", err)
	}
	if !truncated2 {
		t.Fatalf("expected truncated result")
	}
	if len(hosts2) != 1 || hosts2[0] != "192.168.10.1" {
		t.Fatalf("unexpected hosts after cap: %v", hosts2)
	}
}

func TestValidateRTMPStreamURL(t *testing.T) {
	cases := []struct {
		Name    string
		Input   string
		WantErr bool
	}{
		{Name: "ok", Input: "rtmp://127.0.0.1:11935/live/cam01", WantErr: false},
		{Name: "missing scheme", Input: "http://127.0.0.1/live/cam01", WantErr: true},
		{Name: "missing host", Input: "rtmp:///live/cam01", WantErr: true},
		{Name: "missing path", Input: "rtmp://127.0.0.1:11935/live", WantErr: true},
	}
	for _, tc := range cases {
		err := validateRTMPStreamURL(tc.Input)
		if tc.WantErr && err == nil {
			t.Fatalf("%s: expected error, got nil", tc.Name)
		}
		if !tc.WantErr && err != nil {
			t.Fatalf("%s: expected nil, got %v", tc.Name, err)
		}
	}
}

func TestNormalizeMediaSourceType(t *testing.T) {
	cases := []struct {
		sourceType string
		protocol   string
		want       string
	}{
		{sourceType: "pull", protocol: "", want: model.SourceTypePull},
		{sourceType: "push", protocol: "", want: model.SourceTypePush},
		{sourceType: "", protocol: "rtsp", want: model.SourceTypePull},
		{sourceType: "", protocol: "rtmp", want: model.SourceTypePush},
		{sourceType: "", protocol: "gb28181", want: model.SourceTypeGB28181},
		{sourceType: "unknown", protocol: "unknown", want: ""},
	}
	for _, tc := range cases {
		got := normalizeMediaSourceType(tc.sourceType, tc.protocol)
		if got != tc.want {
			t.Fatalf("normalizeMediaSourceType(%q,%q)=%q want=%q", tc.sourceType, tc.protocol, got, tc.want)
		}
	}
}

func TestApplyOutputConfigToSource(t *testing.T) {
	source := model.MediaSource{ID: "s1", App: "live", StreamID: "stream1"}
	applyOutputConfigToSource(&source, map[string]string{
		"webrtc":     "http://127.0.0.1/index/api/webrtc?app=live&stream=stream1&type=play",
		"ws_flv":     "ws://127.0.0.1/live/stream1.live.flv",
		"http_flv":   "http://127.0.0.1/live/stream1.live.flv",
		"hls":        "http://127.0.0.1/live/stream1/hls.m3u8",
		"rtsp":       "rtsp://127.0.0.1:1554/live/stream1",
		"rtmp":       "rtmp://127.0.0.1:11935/live/stream1",
		"zlm_app":    "live",
		"zlm_stream": "stream1",
	})
	if !strings.Contains(source.OutputConfig, "\"webrtc\"") {
		t.Fatalf("expected output_config contains webrtc, got=%s", source.OutputConfig)
	}
	if source.PlayRTSPURL == "" || source.PlayRTMPURL == "" || source.PlayWebRTCURL == "" {
		t.Fatalf("expected play urls filled, got=%+v", source)
	}
}

func TestBuildGBChannelSourceStreamID(t *testing.T) {
	got := buildGBChannelSourceStreamID("34020000001110103911", "34020000001320000001")
	want := "34020000001110103911_34020000001320000001"
	if got != want {
		t.Fatalf("unexpected stream id: got=%s want=%s", got, want)
	}

	got = buildGBChannelSourceStreamID("device 01", "ch/01")
	want = "device_01_ch_01"
	if got != want {
		t.Fatalf("unexpected sanitized stream id: got=%s want=%s", got, want)
	}
}
