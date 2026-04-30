package config

import (
	"fmt"
	"os"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"maas-box/internal/logutil"
)

type Config struct {
	Server ServerConfig `toml:"Server"`
	Data   DataConfig   `toml:"Data"`
	Log    LogConfig    `toml:"Log"`
}

type ServerConfig struct {
	Debug        bool               `toml:"Debug"`
	Development  string             `toml:"Development"`
	Version      string             `toml:"Version"`
	Username     string             `toml:"Username"`
	Password     string             `toml:"Password"`
	HTTP         HTTPConfig         `toml:"HTTP"`
	AI           AIConfig           `toml:"AI"`
	ZLM          ZLMConfig          `toml:"ZLM"`
	SIP          SIPConfig          `toml:"SIP"`
	Recording    RecordingConfig    `toml:"Recording"`
	TaskDefaults TaskDefaultsConfig `toml:"TaskDefaults"`
	Cleanup      CleanupConfig      `toml:"Cleanup"`
}

type HTTPConfig struct {
	Port      int    `toml:"Port"`
	Timeout   string `toml:"Timeout"`
	JwtSecret string `toml:"JwtSecret"`
}

type AIConfig struct {
	Disabled                      bool   `toml:"Disabled"`
	RetainDays                    int    `toml:"RetainDays"`
	ServiceURL                    string `toml:"ServiceURL"`
	CallbackURL                   string `toml:"CallbackURL"`
	CallbackToken                 string `toml:"CallbackToken"`
	RequestTimeout                string `toml:"RequestTimeout"`
	AnalyzeImageFailureRetryCount int    `toml:"AnalyzeImageFailureRetryCount"`
	LLMAPIURL                     string `toml:"LLMAPIURL"`
	LLMAPIKey                     string `toml:"LLMAPIKey"`
	LLMModel                      string `toml:"LLMModel"`
	TotalTokenLimit               int64  `toml:"TotalTokenLimit"`
	DisableOnTokenLimitExceeded   bool   `toml:"DisableOnTokenLimitExceeded"`
	AlgorithmTestImageMaxCount    int    `toml:"AlgorithmTestImageMaxCount"`
	AlgorithmTestVideoMaxCount    int    `toml:"AlgorithmTestVideoMaxCount"`
	AlgorithmTestVideoFPS         int    `toml:"AlgorithmTestVideoFPS"`
	AlgorithmTestVideoMaxBytes    int64  `toml:"AlgorithmTestVideoMaxBytes"`
	AlgorithmTestVideoMinSeconds  int    `toml:"AlgorithmTestVideoMinSeconds"`
	AlgorithmTestVideoMaxSeconds  int    `toml:"AlgorithmTestVideoMaxSeconds"`
}

type ZLMConfig struct {
	Disabled             bool            `toml:"Disabled"`
	APIURL               string          `toml:"APIURL"`
	Secret               string          `toml:"Secret"`
	PlayHost             string          `toml:"PlayHost"`
	AIInputHost          string          `toml:"AIInputHost"`
	HTTPPort             int             `toml:"HTTPPort"`
	RTSPPort             int             `toml:"RTSPPort"`
	RTMPPort             int             `toml:"RTMPPort"`
	App                  string          `toml:"App"`
	RTMPAutoPublishToken string          `toml:"RTMPAutoPublishToken"`
	Output               ZLMOutputConfig `toml:"Output"`
}

type ZLMOutputConfig struct {
	EnableWebRTC  bool   `toml:"EnableWebRTC"`
	EnableWSFLV   bool   `toml:"EnableWSFLV"`
	EnableHTTPFLV bool   `toml:"EnableHTTPFLV"`
	EnableHLS     bool   `toml:"EnableHLS"`
	WebFallback   string `toml:"WebFallback"`
}

type SIPConfig struct {
	Enabled              bool     `toml:"Enabled"`
	ListenIP             string   `toml:"ListenIP"`
	Port                 int      `toml:"Port"`
	ServerID             string   `toml:"ServerID"`
	Domain               string   `toml:"Domain"`
	Password             string   `toml:"Password"`
	GuideNote            string   `toml:"GuideNote"`
	Tips                 []string `toml:"Tips"`
	RecommendedTransport string   `toml:"RecommendedTransport"`
	RegisterExpires      int      `toml:"RegisterExpires"`
	KeepaliveInterval    int      `toml:"KeepaliveInterval"`
	MediaIP              string   `toml:"MediaIP"`
	MediaRTPPort         int      `toml:"MediaRTPPort"`
	MediaPortRange       string   `toml:"MediaPortRange"`
	SampleDeviceID       string   `toml:"SampleDeviceID"`
	KeepaliveTimeout     int      `toml:"KeepaliveTimeoutSec"`
	RegisterGrace        int      `toml:"RegisterGraceSec"`
}

type RecordingConfig struct {
	Disabled        bool                     `toml:"Disabled"`
	AllowContinuous bool                     `toml:"AllowContinuous"`
	Modes           []string                 `toml:"Modes"`
	DefaultMode     string                   `toml:"DefaultMode"`
	StorageDir      string                   `toml:"StorageDir"`
	ZLMStorageDir   string                   `toml:"ZLMStorageDir"`
	RetainDays      int                      `toml:"RetainDays"`
	SegmentSeconds  int                      `toml:"SegmentSeconds"`
	DiskThreshold   float64                  `toml:"DiskUsageThreshold"`
	AlarmClip       RecordingAlarmClipConfig `toml:"AlarmClip"`
}

type RecordingAlarmClipConfig struct {
	EnabledDefault       bool   `toml:"EnabledDefault"`
	PreSeconds           int    `toml:"PreSeconds"`
	PostSeconds          int    `toml:"PostSeconds"`
	BufferSegmentSeconds int    `toml:"BufferSegmentSeconds"`
	BufferKeepSeconds    int    `toml:"BufferKeepSeconds"`
	BufferDir            string `toml:"BufferDir"`
	ZLMBufferDir         string `toml:"ZLMBufferDir"`
	SessionSettleSeconds int    `toml:"SessionSettleSeconds"`
	MaxSessionSeconds    int    `toml:"MaxSessionSeconds"`
	RecoverOnStartup     bool   `toml:"RecoverOnStartup"`
}

type TaskDefaultsConfig struct {
	Video VideoTaskDefaultsConfig `toml:"Video"`
}

type VideoTaskDefaultsConfig struct {
	AlertCycleSecondsDefault int      `toml:"AlertCycleSecondsDefault"`
	AlarmLevelIDDefault      string   `toml:"AlarmLevelIDDefault"`
	FrameRateModes           []string `toml:"FrameRateModes"`
	FrameRateModeDefault     string   `toml:"FrameRateModeDefault"`
	FrameRateValueDefault    int      `toml:"FrameRateValueDefault"`
}

type CleanupConfig struct {
	Enabled              bool    `toml:"Enabled"`
	Interval             string  `toml:"Interval"`
	SoftWatermark        float64 `toml:"SoftWatermark"`
	HardWatermark        float64 `toml:"HardWatermark"`
	CriticalWatermark    float64 `toml:"CriticalWatermark"`
	MinFreeGB            float64 `toml:"MinFreeGB"`
	AlarmClipRetainDays  int     `toml:"AlarmClipRetainDays"`
	ZLMSnapDir           string  `toml:"ZLMSnapDir"`
	ZLMSnapRetainMinutes int     `toml:"ZLMSnapRetainMinutes"`
	EmergencyBreakGlass  bool    `toml:"EmergencyBreakGlass"`
}

type DataConfig struct {
	Database DatabaseConfig `toml:"Database"`
}

type DatabaseConfig struct {
	Dsn string `toml:"Dsn"`
}

type LogConfig struct {
	Dir   string `toml:"Dir"`
	Level string `toml:"Level"`
}

func Load(path string) (*Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := defaultConfig()
	if err := toml.Unmarshal(body, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.normalize()
	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Debug:       false,
			Development: "",
			Version:     "",
			Username:    "admin",
			Password:    "admin",
			HTTP: HTTPConfig{
				Port:      15123,
				Timeout:   "1m",
				JwtSecret: "maas-box-default-jwt-secret",
			},
			AI: AIConfig{
				Disabled:                      false,
				RetainDays:                    7,
				ServiceURL:                    "http://127.0.0.1:50052",
				CallbackURL:                   "",
				CallbackToken:                 "maas-box-callback-token",
				RequestTimeout:                "10s",
				AnalyzeImageFailureRetryCount: 1,
				LLMAPIURL:                     "http://127.0.0.1:11434/v1/chat/completions",
				LLMAPIKey:                     "",
				LLMModel:                      "qwen2.5:7b",
				TotalTokenLimit:               0,
				DisableOnTokenLimitExceeded:   false,
				AlgorithmTestImageMaxCount:    5,
				AlgorithmTestVideoMaxCount:    1,
				AlgorithmTestVideoFPS:         1,
				AlgorithmTestVideoMaxBytes:    100 * 1024 * 1024,
				AlgorithmTestVideoMinSeconds:  2,
				AlgorithmTestVideoMaxSeconds:  20 * 60,
			},
			ZLM: ZLMConfig{
				Disabled:             false,
				APIURL:               "http://127.0.0.1:11029",
				Secret:               "jvRqCAzEg7AszBi4gm1cfhwXpmnVmJMG",
				PlayHost:             "127.0.0.1",
				AIInputHost:          "",
				HTTPPort:             11029,
				RTSPPort:             1554,
				RTMPPort:             11935,
				App:                  "live",
				RTMPAutoPublishToken: "",
				Output: ZLMOutputConfig{
					EnableWebRTC:  true,
					EnableWSFLV:   true,
					EnableHTTPFLV: false,
					EnableHLS:     false,
					WebFallback:   "ws_flv",
				},
			},
			SIP: SIPConfig{
				Enabled:   true,
				ListenIP:  "0.0.0.0",
				Port:      15060,
				ServerID:  "34020000002000000001",
				Domain:    "3402000000",
				Password:  "",
				GuideNote: "参数预检仅用于检查配置正确性，不代表设备已真实注册到平台",
				Tips: []string{
					"GB28181 的设备ID/服务ID必须是20位数字编码",
					"弱网和复杂 NAT 场景优先使用 UDP",
					"请确保防火墙已放行 SIP 和媒体端口",
				},
				RecommendedTransport: "udp",
				RegisterExpires:      3600,
				KeepaliveInterval:    60,
				MediaIP:              "",
				MediaRTPPort:         11000,
				MediaPortRange:       "21000-21100",
				SampleDeviceID:       "34020000001320000001",
				KeepaliveTimeout:     180,
				RegisterGrace:        30,
			},
			Recording: RecordingConfig{
				Disabled:        false,
				AllowContinuous: false,
				Modes:           []string{"none", "continuous"},
				DefaultMode:     "none",
				StorageDir:      "./configs/recordings",
				ZLMStorageDir:   "",
				RetainDays:      7,
				SegmentSeconds:  60,
				DiskThreshold:   95,
				AlarmClip: RecordingAlarmClipConfig{
					EnabledDefault:       false,
					PreSeconds:           8,
					PostSeconds:          12,
					BufferSegmentSeconds: 2,
					BufferKeepSeconds:    120,
					BufferDir:            "./configs/recordings-buffer",
					ZLMBufferDir:         "",
					SessionSettleSeconds: 2,
					MaxSessionSeconds:    180,
					RecoverOnStartup:     true,
				},
			},
			TaskDefaults: TaskDefaultsConfig{
				Video: VideoTaskDefaultsConfig{
					AlertCycleSecondsDefault: 60,
					AlarmLevelIDDefault:      "alarm_level_1",
					FrameRateModes:           []string{"interval", "fps"},
					FrameRateModeDefault:     "interval",
					FrameRateValueDefault:    5,
				},
			},
			Cleanup: CleanupConfig{
				Enabled:              true,
				Interval:             "30m",
				SoftWatermark:        85,
				HardWatermark:        92,
				CriticalWatermark:    97,
				MinFreeGB:            1,
				AlarmClipRetainDays:  7,
				ZLMSnapDir:           "./configs/zlm-www/snap",
				ZLMSnapRetainMinutes: 30,
				EmergencyBreakGlass:  true,
			},
		},
		Data: DataConfig{
			Database: DatabaseConfig{Dsn: "./configs/data.db"},
		},
		Log: LogConfig{
			Dir:   "./logs",
			Level: "info",
		},
	}
}

func (c *Config) normalize() {
	c.Server.Development = strings.TrimSpace(c.Server.Development)
	c.Server.Version = strings.TrimSpace(c.Server.Version)
	c.Log.Dir = strings.TrimSpace(c.Log.Dir)
	if c.Log.Dir == "" {
		c.Log.Dir = "./logs"
	}
	c.Log.Level = logutil.NormalizeLevel(c.Log.Level)

	if v := strings.TrimSpace(os.Getenv("MB_AI_SERVICE_URL")); v != "" {
		c.Server.AI.ServiceURL = v
	}
	if v := strings.TrimSpace(os.Getenv("MB_AI_CALLBACK_URL")); v != "" {
		c.Server.AI.CallbackURL = v
	}
	if v := strings.TrimSpace(os.Getenv("MB_AI_CALLBACK_TOKEN")); v != "" {
		c.Server.AI.CallbackToken = v
	}
	if v := strings.TrimSpace(os.Getenv("MB_LLM_API_URL")); v != "" {
		c.Server.AI.LLMAPIURL = v
	}
	if v := strings.TrimSpace(os.Getenv("MB_LLM_API_KEY")); v != "" {
		c.Server.AI.LLMAPIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("MB_LLM_MODEL")); v != "" {
		c.Server.AI.LLMModel = v
	}
	if v := strings.TrimSpace(os.Getenv("MB_ZLM_API_URL")); v != "" {
		c.Server.ZLM.APIURL = v
	}
	if v := strings.TrimSpace(os.Getenv("MB_ZLM_PLAY_HOST")); v != "" {
		c.Server.ZLM.PlayHost = v
	}
	if v := strings.TrimSpace(os.Getenv("MB_ZLM_AI_INPUT_HOST")); v != "" {
		c.Server.ZLM.AIInputHost = v
	}
	if v := strings.TrimSpace(os.Getenv("MB_ZLM_RTMP_AUTO_PUBLISH_TOKEN")); v != "" {
		c.Server.ZLM.RTMPAutoPublishToken = v
	}

	if c.Server.HTTP.Port <= 0 {
		c.Server.HTTP.Port = 15123
	}
	if strings.TrimSpace(c.Server.HTTP.Timeout) == "" {
		c.Server.HTTP.Timeout = "1m"
	}
	if strings.TrimSpace(c.Server.HTTP.JwtSecret) == "" {
		c.Server.HTTP.JwtSecret = "maas-box-default-jwt-secret"
	}
	if strings.TrimSpace(c.Server.AI.ServiceURL) == "" {
		c.Server.AI.ServiceURL = "http://127.0.0.1:50052"
	}
	if strings.TrimSpace(c.Server.AI.CallbackToken) == "" {
		c.Server.AI.CallbackToken = "maas-box-callback-token"
	}
	if strings.TrimSpace(c.Server.AI.RequestTimeout) == "" {
		c.Server.AI.RequestTimeout = "10s"
	}
	if c.Server.AI.AnalyzeImageFailureRetryCount < 0 {
		c.Server.AI.AnalyzeImageFailureRetryCount = 0
	}
	if c.Server.AI.AlgorithmTestVideoFPS <= 0 {
		c.Server.AI.AlgorithmTestVideoFPS = 1
	}
	if c.Server.AI.AlgorithmTestImageMaxCount <= 0 {
		c.Server.AI.AlgorithmTestImageMaxCount = 5
	}
	if c.Server.AI.AlgorithmTestVideoMaxCount <= 0 {
		c.Server.AI.AlgorithmTestVideoMaxCount = 1
	}
	if c.Server.AI.AlgorithmTestVideoMaxBytes <= 0 {
		c.Server.AI.AlgorithmTestVideoMaxBytes = 100 * 1024 * 1024
	}
	if c.Server.AI.AlgorithmTestVideoMinSeconds < 2 {
		c.Server.AI.AlgorithmTestVideoMinSeconds = 2
	}
	if c.Server.AI.AlgorithmTestVideoMaxSeconds < c.Server.AI.AlgorithmTestVideoMinSeconds {
		c.Server.AI.AlgorithmTestVideoMaxSeconds = 20 * 60
	}
	if c.Server.AI.TotalTokenLimit < 0 {
		c.Server.AI.TotalTokenLimit = 0
	}
	c.Server.AI.LLMAPIURL = strings.TrimSpace(c.Server.AI.LLMAPIURL)
	c.Server.AI.LLMAPIKey = strings.TrimSpace(c.Server.AI.LLMAPIKey)
	c.Server.AI.LLMModel = strings.TrimSpace(c.Server.AI.LLMModel)
	if c.Server.AI.LLMAPIURL == "" {
		c.Server.AI.LLMAPIURL = "http://127.0.0.1:11434/v1/chat/completions"
	}
	if c.Server.AI.LLMModel == "" {
		c.Server.AI.LLMModel = "qwen2.5:7b"
	}
	if strings.TrimSpace(c.Server.AI.CallbackURL) == "" {
		c.Server.AI.CallbackURL = fmt.Sprintf("http://127.0.0.1:%d/ai", c.Server.HTTP.Port)
	}
	if strings.TrimSpace(c.Server.ZLM.APIURL) == "" {
		c.Server.ZLM.APIURL = "http://127.0.0.1:11029"
	}
	if strings.TrimSpace(c.Server.ZLM.Secret) == "" {
		c.Server.ZLM.Secret = "jvRqCAzEg7AszBi4gm1cfhwXpmnVmJMG"
	}
	if strings.TrimSpace(c.Server.ZLM.PlayHost) == "" {
		c.Server.ZLM.PlayHost = "127.0.0.1"
	}
	if c.Server.ZLM.HTTPPort <= 0 {
		c.Server.ZLM.HTTPPort = 11029
	}
	if c.Server.ZLM.RTSPPort <= 0 {
		c.Server.ZLM.RTSPPort = 1554
	}
	if c.Server.ZLM.RTMPPort <= 0 {
		c.Server.ZLM.RTMPPort = 11935
	}
	if strings.TrimSpace(c.Server.ZLM.App) == "" {
		c.Server.ZLM.App = "live"
	}
	c.Server.ZLM.RTMPAutoPublishToken = strings.TrimSpace(c.Server.ZLM.RTMPAutoPublishToken)
	c.Server.ZLM.Output.WebFallback = normalizeWebFallback(c.Server.ZLM.Output.WebFallback)
	if !isWebFallbackEnabled(c.Server.ZLM.Output, c.Server.ZLM.Output.WebFallback) {
		for _, candidate := range []string{"ws_flv", "http_flv", "hls"} {
			if isWebFallbackEnabled(c.Server.ZLM.Output, candidate) {
				c.Server.ZLM.Output.WebFallback = candidate
				break
			}
		}
	}
	if strings.TrimSpace(c.Server.SIP.ListenIP) == "" {
		c.Server.SIP.ListenIP = "0.0.0.0"
	}
	if c.Server.SIP.Port <= 0 {
		c.Server.SIP.Port = 15060
	}
	if strings.TrimSpace(c.Server.SIP.ServerID) == "" {
		c.Server.SIP.ServerID = "34020000002000000001"
	}
	if strings.TrimSpace(c.Server.SIP.Domain) == "" {
		c.Server.SIP.Domain = "3402000000"
	}
	if strings.TrimSpace(c.Server.SIP.GuideNote) == "" {
		c.Server.SIP.GuideNote = "参数预检仅用于检查配置正确性，不代表设备已真实注册到平台"
	}
	if len(c.Server.SIP.Tips) == 0 {
		c.Server.SIP.Tips = []string{
			"GB28181 的设备ID/服务ID必须是20位数字编码",
			"弱网和复杂 NAT 场景优先使用 UDP",
			"请确保防火墙已放行 SIP 和媒体端口",
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.Server.SIP.RecommendedTransport)) {
	case "tcp":
		c.Server.SIP.RecommendedTransport = "tcp"
	default:
		c.Server.SIP.RecommendedTransport = "udp"
	}
	if c.Server.SIP.RegisterExpires <= 0 {
		c.Server.SIP.RegisterExpires = 3600
	}
	if c.Server.SIP.KeepaliveInterval <= 0 {
		c.Server.SIP.KeepaliveInterval = 60
	}
	if c.Server.SIP.MediaRTPPort <= 0 {
		c.Server.SIP.MediaRTPPort = 11000
	}
	if strings.TrimSpace(c.Server.SIP.MediaPortRange) == "" {
		c.Server.SIP.MediaPortRange = "21000-21100"
	}
	if strings.TrimSpace(c.Server.SIP.SampleDeviceID) == "" {
		c.Server.SIP.SampleDeviceID = "34020000001320000001"
	}
	if c.Server.SIP.KeepaliveTimeout <= 0 {
		c.Server.SIP.KeepaliveTimeout = 180
	}
	if c.Server.SIP.RegisterGrace <= 0 {
		c.Server.SIP.RegisterGrace = 30
	}
	if strings.TrimSpace(c.Data.Database.Dsn) == "" {
		c.Data.Database.Dsn = "./configs/data.db"
	}
	if strings.TrimSpace(c.Server.Recording.StorageDir) == "" {
		c.Server.Recording.StorageDir = "./configs/recordings"
	}
	c.Server.Recording.ZLMStorageDir = strings.TrimSpace(c.Server.Recording.ZLMStorageDir)
	if c.Server.Recording.RetainDays <= 0 {
		c.Server.Recording.RetainDays = 7
	}
	if c.Server.Recording.DiskThreshold <= 0 {
		c.Server.Recording.DiskThreshold = 95
	}
	if c.Server.Recording.SegmentSeconds <= 0 {
		c.Server.Recording.SegmentSeconds = 60
	}
	if c.Server.Recording.AlarmClip.PreSeconds <= 0 {
		c.Server.Recording.AlarmClip.PreSeconds = 8
	}
	if c.Server.Recording.AlarmClip.PostSeconds <= 0 {
		c.Server.Recording.AlarmClip.PostSeconds = 12
	}
	if c.Server.Recording.AlarmClip.BufferSegmentSeconds <= 0 {
		c.Server.Recording.AlarmClip.BufferSegmentSeconds = 2
	}
	if c.Server.Recording.AlarmClip.BufferSegmentSeconds > 30 {
		c.Server.Recording.AlarmClip.BufferSegmentSeconds = 30
	}
	minKeep := c.Server.Recording.AlarmClip.PreSeconds + c.Server.Recording.AlarmClip.PostSeconds + c.Server.Recording.AlarmClip.BufferSegmentSeconds
	if minKeep < 30 {
		minKeep = 30
	}
	if c.Server.Recording.AlarmClip.BufferKeepSeconds <= 0 {
		c.Server.Recording.AlarmClip.BufferKeepSeconds = c.Server.Recording.AlarmClip.PreSeconds + c.Server.Recording.AlarmClip.PostSeconds + 60
	}
	if c.Server.Recording.AlarmClip.BufferKeepSeconds < minKeep {
		c.Server.Recording.AlarmClip.BufferKeepSeconds = minKeep
	}
	if c.Server.Recording.AlarmClip.BufferKeepSeconds > 3600 {
		c.Server.Recording.AlarmClip.BufferKeepSeconds = 3600
	}
	if strings.TrimSpace(c.Server.Recording.AlarmClip.BufferDir) == "" {
		c.Server.Recording.AlarmClip.BufferDir = "./configs/recordings-buffer"
	}
	c.Server.Recording.AlarmClip.ZLMBufferDir = strings.TrimSpace(c.Server.Recording.AlarmClip.ZLMBufferDir)
	if c.Server.Recording.AlarmClip.SessionSettleSeconds <= 0 {
		c.Server.Recording.AlarmClip.SessionSettleSeconds = 2
	}
	if c.Server.Recording.AlarmClip.SessionSettleSeconds > 10 {
		c.Server.Recording.AlarmClip.SessionSettleSeconds = 10
	}
	if c.Server.Recording.AlarmClip.MaxSessionSeconds <= 0 {
		c.Server.Recording.AlarmClip.MaxSessionSeconds = 180
	}
	if c.Server.Recording.AlarmClip.MaxSessionSeconds < 30 {
		c.Server.Recording.AlarmClip.MaxSessionSeconds = 30
	}
	if c.Server.Recording.AlarmClip.MaxSessionSeconds > 1800 {
		c.Server.Recording.AlarmClip.MaxSessionSeconds = 1800
	}
	c.Server.Recording.Modes = normalizeRecordingModes(c.Server.Recording.Modes, c.Server.Recording.AllowContinuous)
	c.Server.Recording.DefaultMode = normalizeRecordingDefaultMode(
		c.Server.Recording.DefaultMode,
		c.Server.Recording.Modes,
		c.Server.Recording.AllowContinuous,
	)
	if c.Server.TaskDefaults.Video.AlertCycleSecondsDefault < 0 || c.Server.TaskDefaults.Video.AlertCycleSecondsDefault > 86400 {
		c.Server.TaskDefaults.Video.AlertCycleSecondsDefault = 60
	}
	c.Server.TaskDefaults.Video.AlarmLevelIDDefault = normalizeVideoTaskAlarmLevelIDDefault(c.Server.TaskDefaults.Video.AlarmLevelIDDefault)
	c.Server.TaskDefaults.Video.FrameRateModes = normalizeVideoTaskFrameRateModes(c.Server.TaskDefaults.Video.FrameRateModes)
	c.Server.TaskDefaults.Video.FrameRateModeDefault = normalizeVideoTaskFrameRateDefaultMode(
		c.Server.TaskDefaults.Video.FrameRateModeDefault,
		c.Server.TaskDefaults.Video.FrameRateModes,
	)
	if c.Server.TaskDefaults.Video.FrameRateValueDefault <= 0 || c.Server.TaskDefaults.Video.FrameRateValueDefault > 60 {
		c.Server.TaskDefaults.Video.FrameRateValueDefault = 5
	}
	if c.Server.AI.RetainDays <= 0 {
		c.Server.AI.RetainDays = 7
	}
	c.Server.Cleanup.Interval = strings.TrimSpace(c.Server.Cleanup.Interval)
	if c.Server.Cleanup.Interval == "" {
		c.Server.Cleanup.Interval = "30m"
	}
	if c.Server.Cleanup.SoftWatermark <= 0 || c.Server.Cleanup.SoftWatermark >= 100 {
		c.Server.Cleanup.SoftWatermark = 85
	}
	if c.Server.Cleanup.HardWatermark <= 0 || c.Server.Cleanup.HardWatermark >= 100 {
		c.Server.Cleanup.HardWatermark = 92
	}
	if c.Server.Cleanup.CriticalWatermark <= 0 || c.Server.Cleanup.CriticalWatermark >= 100 {
		c.Server.Cleanup.CriticalWatermark = 97
	}
	if c.Server.Cleanup.HardWatermark < c.Server.Cleanup.SoftWatermark {
		c.Server.Cleanup.HardWatermark = c.Server.Cleanup.SoftWatermark
	}
	if c.Server.Cleanup.CriticalWatermark < c.Server.Cleanup.HardWatermark {
		c.Server.Cleanup.CriticalWatermark = c.Server.Cleanup.HardWatermark
	}
	if c.Server.Cleanup.MinFreeGB <= 0 {
		c.Server.Cleanup.MinFreeGB = 1
	}
	if c.Server.Cleanup.AlarmClipRetainDays <= 0 {
		c.Server.Cleanup.AlarmClipRetainDays = 7
	}
	c.Server.Cleanup.ZLMSnapDir = strings.TrimSpace(c.Server.Cleanup.ZLMSnapDir)
	if c.Server.Cleanup.ZLMSnapDir == "" {
		c.Server.Cleanup.ZLMSnapDir = "./configs/zlm-www/snap"
	}
	if c.Server.Cleanup.ZLMSnapRetainMinutes <= 0 {
		c.Server.Cleanup.ZLMSnapRetainMinutes = 30
	}
}

func normalizeWebFallback(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ws_flv":
		return "ws_flv"
	case "http_flv":
		return "http_flv"
	case "hls":
		return "hls"
	default:
		return "ws_flv"
	}
}

func isWebFallbackEnabled(output ZLMOutputConfig, key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "ws_flv":
		return output.EnableWSFLV
	case "http_flv":
		return output.EnableHTTPFLV
	case "hls":
		return output.EnableHLS
	default:
		return false
	}
}

func normalizeRecordingMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none":
		return "none"
	case "continuous":
		return "continuous"
	default:
		return ""
	}
}

func normalizeRecordingModes(input []string, allowContinuous bool) []string {
	if len(input) == 0 {
		if allowContinuous {
			return []string{"none", "continuous"}
		}
		return []string{"none"}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(input))
	for _, raw := range input {
		mode := normalizeRecordingMode(raw)
		if mode == "" {
			continue
		}
		if mode == "continuous" && !allowContinuous {
			continue
		}
		if _, ok := seen[mode]; ok {
			continue
		}
		seen[mode] = struct{}{}
		out = append(out, mode)
	}
	if _, ok := seen["none"]; !ok {
		out = append([]string{"none"}, out...)
		seen["none"] = struct{}{}
	}
	if allowContinuous {
		if _, ok := seen["continuous"]; !ok {
			out = append(out, "continuous")
		}
	}
	return out
}

func normalizeRecordingDefaultMode(raw string, modes []string, allowContinuous bool) string {
	mode := normalizeRecordingMode(raw)
	allowed := map[string]struct{}{}
	for _, item := range modes {
		normalized := normalizeRecordingMode(item)
		if normalized == "" {
			continue
		}
		allowed[normalized] = struct{}{}
	}
	if mode != "" {
		if mode == "continuous" && !allowContinuous {
			mode = "none"
		}
		if _, ok := allowed[mode]; ok {
			return mode
		}
	}
	if _, ok := allowed["none"]; ok {
		return "none"
	}
	if allowContinuous {
		if _, ok := allowed["continuous"]; ok {
			return "continuous"
		}
	}
	return "none"
}

func normalizeVideoTaskAlarmLevelIDDefault(raw string) string {
	switch strings.TrimSpace(raw) {
	case "alarm_level_1", "alarm_level_2", "alarm_level_3":
		return strings.TrimSpace(raw)
	default:
		return "alarm_level_1"
	}
}

func normalizeVideoTaskFrameRateMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "fps":
		return "fps"
	case "interval":
		return "interval"
	default:
		return ""
	}
}

func normalizeVideoTaskFrameRateModes(input []string) []string {
	defaultModes := []string{"interval", "fps"}
	if len(input) == 0 {
		return defaultModes
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(input))
	for _, raw := range input {
		mode := normalizeVideoTaskFrameRateMode(raw)
		if mode == "" {
			continue
		}
		if _, ok := seen[mode]; ok {
			continue
		}
		seen[mode] = struct{}{}
		out = append(out, mode)
	}
	if len(out) == 0 {
		return defaultModes
	}
	return out
}

func normalizeVideoTaskFrameRateDefaultMode(raw string, modes []string) string {
	mode := normalizeVideoTaskFrameRateMode(raw)
	if mode != "" {
		for _, item := range modes {
			if mode == normalizeVideoTaskFrameRateMode(item) {
				return mode
			}
		}
	}
	for _, item := range modes {
		if normalizeVideoTaskFrameRateMode(item) == "interval" {
			return "interval"
		}
	}
	if len(modes) > 0 {
		return normalizeVideoTaskFrameRateMode(modes[0])
	}
	return "interval"
}
