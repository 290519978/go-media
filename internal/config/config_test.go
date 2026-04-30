package config

import "testing"

func TestNormalizeWebFallback(t *testing.T) {
	cfg := defaultConfig()
	cfg.Server.ZLM.Output.WebFallback = "invalid"
	cfg.normalize()
	if cfg.Server.ZLM.Output.WebFallback != "ws_flv" {
		t.Fatalf("invalid fallback should reset to ws_flv, got=%s", cfg.Server.ZLM.Output.WebFallback)
	}

	cfg = defaultConfig()
	cfg.Server.ZLM.Output.WebFallback = "hls"
	cfg.Server.ZLM.Output.EnableHLS = true
	cfg.normalize()
	if cfg.Server.ZLM.Output.WebFallback != "hls" {
		t.Fatalf("expected hls fallback when enabled, got=%s", cfg.Server.ZLM.Output.WebFallback)
	}

	cfg = defaultConfig()
	cfg.Server.ZLM.Output.WebFallback = "hls"
	cfg.Server.ZLM.Output.EnableHLS = false
	cfg.Server.ZLM.Output.EnableWSFLV = false
	cfg.Server.ZLM.Output.EnableHTTPFLV = true
	cfg.normalize()
	if cfg.Server.ZLM.Output.WebFallback != "http_flv" {
		t.Fatalf("expected fallback downgrade to enabled protocol, got=%s", cfg.Server.ZLM.Output.WebFallback)
	}
}

func TestNormalizeLogLevel(t *testing.T) {
	cfg := defaultConfig()
	cfg.Log.Level = "DEBUG"
	cfg.normalize()
	if cfg.Log.Level != "debug" {
		t.Fatalf("expected debug, got=%s", cfg.Log.Level)
	}

	cfg = defaultConfig()
	cfg.Log.Level = "verbose"
	cfg.normalize()
	if cfg.Log.Level != "info" {
		t.Fatalf("invalid log level should fallback to info, got=%s", cfg.Log.Level)
	}
}

func TestNormalizeAlgorithmTestVideoFPS(t *testing.T) {
	cfg := defaultConfig()
	cfg.Server.AI.AlgorithmTestVideoFPS = 0
	cfg.normalize()
	if cfg.Server.AI.AlgorithmTestVideoFPS != 1 {
		t.Fatalf("invalid algorithm test video fps should fallback to 1, got=%d", cfg.Server.AI.AlgorithmTestVideoFPS)
	}

	cfg = defaultConfig()
	cfg.Server.AI.AlgorithmTestVideoFPS = 3
	cfg.normalize()
	if cfg.Server.AI.AlgorithmTestVideoFPS != 3 {
		t.Fatalf("expected configured algorithm test video fps to be preserved, got=%d", cfg.Server.AI.AlgorithmTestVideoFPS)
	}
}

func TestNormalizeAlgorithmTestMediaCountLimits(t *testing.T) {
	cfg := defaultConfig()
	cfg.Server.AI.AlgorithmTestImageMaxCount = 0
	cfg.Server.AI.AlgorithmTestVideoMaxCount = -1
	cfg.normalize()
	if cfg.Server.AI.AlgorithmTestImageMaxCount != 5 {
		t.Fatalf("invalid algorithm test image max count should fallback to 5, got=%d", cfg.Server.AI.AlgorithmTestImageMaxCount)
	}
	if cfg.Server.AI.AlgorithmTestVideoMaxCount != 1 {
		t.Fatalf("invalid algorithm test video max count should fallback to 1, got=%d", cfg.Server.AI.AlgorithmTestVideoMaxCount)
	}

	cfg = defaultConfig()
	cfg.Server.AI.AlgorithmTestImageMaxCount = 3
	cfg.Server.AI.AlgorithmTestVideoMaxCount = 2
	cfg.normalize()
	if cfg.Server.AI.AlgorithmTestImageMaxCount != 3 {
		t.Fatalf("expected configured algorithm test image max count to be preserved, got=%d", cfg.Server.AI.AlgorithmTestImageMaxCount)
	}
	if cfg.Server.AI.AlgorithmTestVideoMaxCount != 2 {
		t.Fatalf("expected configured algorithm test video max count to be preserved, got=%d", cfg.Server.AI.AlgorithmTestVideoMaxCount)
	}
}

func TestNormalizeAlgorithmTestVideoLimits(t *testing.T) {
	cfg := defaultConfig()
	cfg.Server.AI.AlgorithmTestVideoMaxBytes = 0
	cfg.Server.AI.AlgorithmTestVideoMinSeconds = 0
	cfg.Server.AI.AlgorithmTestVideoMaxSeconds = 1
	cfg.normalize()
	if cfg.Server.AI.AlgorithmTestVideoMaxBytes != 100*1024*1024 {
		t.Fatalf("invalid algorithm test video max bytes should fallback to 100MB, got=%d", cfg.Server.AI.AlgorithmTestVideoMaxBytes)
	}
	if cfg.Server.AI.AlgorithmTestVideoMinSeconds != 2 {
		t.Fatalf("invalid algorithm test video min seconds should fallback to 2, got=%d", cfg.Server.AI.AlgorithmTestVideoMinSeconds)
	}
	if cfg.Server.AI.AlgorithmTestVideoMaxSeconds != 20*60 {
		t.Fatalf("invalid algorithm test video max seconds should fallback to 1200, got=%d", cfg.Server.AI.AlgorithmTestVideoMaxSeconds)
	}

	cfg = defaultConfig()
	cfg.Server.AI.AlgorithmTestVideoMaxBytes = 50 * 1024 * 1024
	cfg.Server.AI.AlgorithmTestVideoMinSeconds = 5
	cfg.Server.AI.AlgorithmTestVideoMaxSeconds = 600
	cfg.normalize()
	if cfg.Server.AI.AlgorithmTestVideoMaxBytes != 50*1024*1024 {
		t.Fatalf("expected configured algorithm test video max bytes to be preserved, got=%d", cfg.Server.AI.AlgorithmTestVideoMaxBytes)
	}
	if cfg.Server.AI.AlgorithmTestVideoMinSeconds != 5 {
		t.Fatalf("expected configured algorithm test video min seconds to be preserved, got=%d", cfg.Server.AI.AlgorithmTestVideoMinSeconds)
	}
	if cfg.Server.AI.AlgorithmTestVideoMaxSeconds != 600 {
		t.Fatalf("expected configured algorithm test video max seconds to be preserved, got=%d", cfg.Server.AI.AlgorithmTestVideoMaxSeconds)
	}
}

func TestNormalizeAnalyzeImageFailureRetryCount(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Server.AI.AnalyzeImageFailureRetryCount != 1 {
		t.Fatalf("expected analyze image failure retry count default 1, got=%d", cfg.Server.AI.AnalyzeImageFailureRetryCount)
	}

	cfg = defaultConfig()
	cfg.Server.AI.AnalyzeImageFailureRetryCount = -3
	cfg.normalize()
	if cfg.Server.AI.AnalyzeImageFailureRetryCount != 0 {
		t.Fatalf("negative analyze image failure retry count should fallback to 0, got=%d", cfg.Server.AI.AnalyzeImageFailureRetryCount)
	}

	cfg = defaultConfig()
	cfg.Server.AI.AnalyzeImageFailureRetryCount = 2
	cfg.normalize()
	if cfg.Server.AI.AnalyzeImageFailureRetryCount != 2 {
		t.Fatalf("expected configured analyze image failure retry count to be preserved, got=%d", cfg.Server.AI.AnalyzeImageFailureRetryCount)
	}
}

func TestNormalizeLLMTokenLimitControls(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Server.AI.DisableOnTokenLimitExceeded {
		t.Fatalf("expected DisableOnTokenLimitExceeded default false")
	}

	cfg.Server.AI.TotalTokenLimit = -1
	cfg.Server.AI.DisableOnTokenLimitExceeded = true
	cfg.normalize()
	if cfg.Server.AI.TotalTokenLimit != 0 {
		t.Fatalf("negative TotalTokenLimit should fallback to 0, got=%d", cfg.Server.AI.TotalTokenLimit)
	}
	if !cfg.Server.AI.DisableOnTokenLimitExceeded {
		t.Fatalf("expected DisableOnTokenLimitExceeded to preserve configured true")
	}
}

func TestNormalizeVideoTaskDefaults(t *testing.T) {
	cfg := defaultConfig()
	cfg.Server.TaskDefaults.Video = VideoTaskDefaultsConfig{
		AlertCycleSecondsDefault: 999999,
		AlarmLevelIDDefault:      "alarm_level_9",
		FrameRateModes:           []string{"bad", "fps", "fps"},
		FrameRateModeDefault:     "interval",
		FrameRateValueDefault:    0,
	}

	cfg.normalize()

	if cfg.Server.TaskDefaults.Video.AlertCycleSecondsDefault != 60 {
		t.Fatalf("expected alert cycle default fallback 60, got %d", cfg.Server.TaskDefaults.Video.AlertCycleSecondsDefault)
	}
	if cfg.Server.TaskDefaults.Video.AlarmLevelIDDefault != "alarm_level_1" {
		t.Fatalf("expected alarm level default fallback alarm_level_1, got %s", cfg.Server.TaskDefaults.Video.AlarmLevelIDDefault)
	}
	if len(cfg.Server.TaskDefaults.Video.FrameRateModes) != 1 || cfg.Server.TaskDefaults.Video.FrameRateModes[0] != "fps" {
		t.Fatalf("unexpected normalized frame rate modes: %+v", cfg.Server.TaskDefaults.Video.FrameRateModes)
	}
	if cfg.Server.TaskDefaults.Video.FrameRateModeDefault != "fps" {
		t.Fatalf("expected frame rate mode default fallback fps, got %s", cfg.Server.TaskDefaults.Video.FrameRateModeDefault)
	}
	if cfg.Server.TaskDefaults.Video.FrameRateValueDefault != 5 {
		t.Fatalf("expected frame rate value default fallback 5, got %d", cfg.Server.TaskDefaults.Video.FrameRateValueDefault)
	}
}
