package model

import (
	"time"
)

const (
	RootAreaID = "root"
)

const (
	ProtocolRTSP    = "rtsp"
	ProtocolRTMP    = "rtmp"
	ProtocolGB28181 = "gb28181"
	ProtocolONVIF   = "onvif"
)

const (
	AlgorithmModeSmall  = "small"
	AlgorithmModeLarge  = "large"
	AlgorithmModeHybrid = "hybrid"
)

const (
	LabelsTriggerModeAny = "any"
	LabelsTriggerModeAll = "all"
)

const (
	AlgorithmDetectModeSmallOnly = 1
	AlgorithmDetectModeLLMOnly   = 2
	AlgorithmDetectModeHybrid    = 3
)

const (
	TaskStatusStopped     = "stopped"
	TaskStatusRunning     = "running"
	TaskStatusPartialFail = "partial_fail"
)

const (
	DeviceAIStatusIdle    = "idle"
	DeviceAIStatusRunning = "running"
	DeviceAIStatusError   = "error"
	DeviceAIStatusStopped = "stopped"
)

const (
	RecordingModeNone       = "none"
	RecordingModeContinuous = "continuous"
)

const (
	RecordingPolicyNone       = "none"
	RecordingPolicyAlarmClip  = "alarm_clip"
	RecordingPolicyContinuous = "continuous"
)

const (
	FrameRateModeFPS      = "fps"
	FrameRateModeInterval = "interval"
)

const (
	EventStatusPending = "pending"
	EventStatusValid   = "valid"
	EventStatusInvalid = "invalid"
)

const (
	AlarmEventSourceRuntime = "runtime"
	AlarmEventSourcePatrol  = "patrol"
)

const (
	LLMUsageSourceTaskRuntime   = "task_runtime"
	LLMUsageSourceAlgorithmTest = "algorithm_test"
	LLMUsageSourceDirectAnalyze = "direct_analyze"
)

const (
	LLMUsageStatusSuccess      = "success"
	LLMUsageStatusEmptyContent = "empty_content"
	LLMUsageStatusError        = "error"
)

const (
	AlgorithmTestJobStatusPending       = "pending"
	AlgorithmTestJobStatusRunning       = "running"
	AlgorithmTestJobStatusCompleted     = "completed"
	AlgorithmTestJobStatusPartialFailed = "partial_failed"
	AlgorithmTestJobStatusFailed        = "failed"
)

const (
	AlgorithmTestJobItemStatusPending = "pending"
	AlgorithmTestJobItemStatusRunning = "running"
	AlgorithmTestJobItemStatusSuccess = "success"
	AlgorithmTestJobItemStatusFailed  = "failed"
)

type Area struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	ParentID  string    `gorm:"size:64;index" json:"parent_id"`
	Name      string    `gorm:"size:128;not null" json:"name"`
	IsRoot    bool      `gorm:"not null;default:false" json:"is_root"`
	Sort      int       `gorm:"not null;default:0" json:"sort"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Area) TableName() string { return "mb_areas" }

const (
	SourceTypeGB28181 = "gb28181"
	SourceTypePull    = "pull"
	SourceTypePush    = "push"
)

const (
	RowKindDevice  = "device"
	RowKindChannel = "channel"
)

type MediaSource struct {
	ID               string    `gorm:"primaryKey;size:64" json:"id"`
	Name             string    `gorm:"size:128;not null;index" json:"name"`
	AreaID           string    `gorm:"size:64;not null;index:idx_mb_media_area" json:"area_id"`
	SourceType       string    `gorm:"size:32;not null;index:idx_mb_media_source_type" json:"source_type"`
	RowKind          string    `gorm:"size:16;not null;index:idx_mb_media_row_kind" json:"row_kind"`
	ParentID         string    `gorm:"size:64;index:idx_mb_media_parent_id" json:"parent_id"`
	Protocol         string    `gorm:"size:32;not null;index:idx_mb_media_protocol" json:"protocol"`
	Transport        string    `gorm:"size:8;not null;default:tcp" json:"transport"`
	App              string    `gorm:"size:128;not null;default:live;index:idx_mb_media_app_stream,unique" json:"app"`
	StreamID         string    `gorm:"size:256;not null;index:idx_mb_media_app_stream,unique" json:"stream_id"`
	StreamURL        string    `gorm:"size:1024;not null" json:"stream_url"`
	Status           string    `gorm:"size:32;not null;default:offline;index:idx_mb_media_status" json:"status"`
	AIStatus         string    `gorm:"size:32;not null;default:idle" json:"ai_status"`
	EnableRecording  bool      `gorm:"not null;default:false" json:"enable_recording"`
	RecordingMode    string    `gorm:"size:32;not null;default:none" json:"recording_mode"`
	RecordingStatus  string    `gorm:"size:32;not null;default:stopped" json:"recording_status"`
	EnableAlarmClip  bool      `gorm:"not null;default:false" json:"enable_alarm_clip"`
	AlarmPreSeconds  int       `gorm:"not null;default:8" json:"alarm_pre_seconds"`
	AlarmPostSeconds int       `gorm:"not null;default:12" json:"alarm_post_seconds"`
	MediaServerID    string    `gorm:"size:64;not null;default:local" json:"media_server_id"`
	PlayWebRTCURL    string    `gorm:"size:1024" json:"play_webrtc_url"`
	PlayWSFLVURL     string    `gorm:"size:1024" json:"play_ws_flv_url"`
	PlayHTTPFLVURL   string    `gorm:"size:1024" json:"play_http_flv_url"`
	PlayHLSURL       string    `gorm:"size:1024" json:"play_hls_url"`
	PlayRTSPURL      string    `gorm:"size:1024" json:"play_rtsp_url"`
	PlayRTMPURL      string    `gorm:"size:1024" json:"play_rtmp_url"`
	SnapshotURL      string    `gorm:"size:1024" json:"snapshot_url"`
	ExtraJSON        string    `gorm:"type:text;not null;default:'{}'" json:"extra_json"`
	OutputConfig     string    `gorm:"type:text;not null;default:'{}'" json:"output_config"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (MediaSource) TableName() string { return "mb_media_sources" }

// Device 兼容旧调用语义，底层映射到媒体主表。
// Device 是媒体主表的语义别名。
type Device = MediaSource
type StreamProxy struct {
	SourceID   string    `gorm:"primaryKey;size:64" json:"source_id"`
	OriginURL  string    `gorm:"size:1024;not null" json:"origin_url"`
	Transport  string    `gorm:"size:8;not null;default:tcp" json:"transport"`
	Enable     bool      `gorm:"not null;default:true" json:"enable"`
	RetryCount int       `gorm:"not null;default:1" json:"retry_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (StreamProxy) TableName() string { return "mb_stream_proxies" }

type StreamPush struct {
	SourceID     string    `gorm:"primaryKey;size:64" json:"source_id"`
	PublishToken string    `gorm:"size:512" json:"publish_token"`
	LastPushAt   time.Time `json:"last_push_at"`
	ClientIP     string    `gorm:"size:64" json:"client_ip"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (StreamPush) TableName() string { return "mb_stream_pushes" }

type GBDeviceBlock struct {
	DeviceID  string    `gorm:"primaryKey;size:32" json:"device_id"`
	Reason    string    `gorm:"size:255" json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (GBDeviceBlock) TableName() string { return "mb_gb_device_blocks" }

type StreamBlock struct {
	App       string    `gorm:"primaryKey;size:128" json:"app"`
	StreamID  string    `gorm:"primaryKey;size:256" json:"stream_id"`
	Reason    string    `gorm:"size:255" json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (StreamBlock) TableName() string { return "mb_stream_blocks" }

type GBDevice struct {
	DeviceID        string    `gorm:"primaryKey;size:32" json:"device_id"`
	SourceIDDevice  string    `gorm:"size:64;index" json:"source_id_device"`
	Name            string    `gorm:"size:128;not null;index" json:"name"`
	AreaID          string    `gorm:"size:64;not null;index" json:"area_id"`
	Password        string    `gorm:"size:255" json:"password,omitempty"`
	Enabled         bool      `gorm:"not null;default:true;index" json:"enabled"`
	Status          string    `gorm:"size:32;not null;default:offline;index" json:"status"`
	Transport       string    `gorm:"size:8;not null;default:udp" json:"transport"`
	SourceAddr      string    `gorm:"size:128" json:"source_addr"`
	Expires         int       `gorm:"not null;default:3600" json:"expires"`
	LastRegisterAt  time.Time `json:"last_register_at"`
	LastKeepaliveAt time.Time `json:"last_keepalive_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (GBDevice) TableName() string { return "mb_gb_devices" }

type GBChannel struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID        string    `gorm:"size:32;not null;index:idx_mb_gb_device_channel,unique" json:"device_id"`
	ChannelID       string    `gorm:"size:64;not null;index:idx_mb_gb_device_channel,unique" json:"channel_id"`
	SourceIDChannel string    `gorm:"size:64;index" json:"source_id_channel"`
	Name            string    `gorm:"size:255" json:"name"`
	Manufacturer    string    `gorm:"size:255" json:"manufacturer"`
	Model           string    `gorm:"size:255" json:"model"`
	Owner           string    `gorm:"size:255" json:"owner"`
	Status          string    `gorm:"size:32" json:"status"`
	RawXML          string    `gorm:"type:text" json:"raw_xml"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (GBChannel) TableName() string { return "mb_gb_channels" }

type Algorithm struct {
	ID                string    `gorm:"primaryKey;size:64" json:"id"`
	Code              string    `gorm:"size:32;index" json:"code"`
	Name              string    `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Description       string    `gorm:"type:text" json:"description"`
	ImageURL          string    `gorm:"size:1024" json:"image_url"`
	Scene             string    `gorm:"size:128" json:"scene"`
	Category          string    `gorm:"size:128" json:"category"`
	Mode              string    `gorm:"size:32;not null" json:"mode"`
	Enabled           bool      `gorm:"not null;default:true" json:"enabled"`
	SmallModelLabel   string    `gorm:"size:512" json:"small_model_label"`
	DetectMode        int       `gorm:"not null;default:3" json:"detect_mode"`
	YoloThreshold     float64   `gorm:"not null;default:0.5" json:"yolo_threshold"`
	IOUThreshold      float64   `gorm:"not null;default:0.8" json:"iou_threshold"`
	LabelsTriggerMode string    `gorm:"size:16;not null;default:any" json:"labels_trigger_mode"`
	ModelProviderID   string    `gorm:"size:64;index" json:"model_provider_id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (Algorithm) TableName() string { return "mb_algorithms" }

type AlgorithmPromptVersion struct {
	ID          string    `gorm:"primaryKey;size:64" json:"id"`
	AlgorithmID string    `gorm:"size:64;not null;index;uniqueIndex:uk_mb_algorithm_prompt_version" json:"algorithm_id"`
	Version     string    `gorm:"size:64;not null;uniqueIndex:uk_mb_algorithm_prompt_version" json:"version"`
	Prompt      string    `gorm:"type:text;not null" json:"prompt"`
	IsActive    bool      `gorm:"not null;default:false;index" json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (AlgorithmPromptVersion) TableName() string { return "mb_algorithm_prompts" }

type ModelProvider struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	Name      string    `gorm:"size:128;not null;uniqueIndex" json:"name"`
	APIURL    string    `gorm:"size:1024;not null" json:"api_url"`
	APIKey    string    `gorm:"size:512" json:"api_key"`
	Model     string    `gorm:"size:256;not null" json:"model"`
	Enabled   bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (ModelProvider) TableName() string { return "mb_model_providers" }

type AlarmLevel struct {
	ID          string    `gorm:"primaryKey;size:64" json:"id"`
	Name        string    `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Severity    int       `gorm:"not null;default:1" json:"severity"`
	Color       string    `gorm:"size:32;not null;default:#faad14" json:"color"`
	Description string    `gorm:"size:255" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (AlarmLevel) TableName() string { return "mb_alarm_levels" }

type VideoTask struct {
	ID              string    `gorm:"primaryKey;size:64" json:"id"`
	Name            string    `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Status          string    `gorm:"size:32;not null;default:stopped;index" json:"status"`
	FrameInterval   int       `gorm:"not null;default:5" json:"frame_interval"`
	SmallConfidence float64   `gorm:"not null;default:0.5" json:"small_confidence"`
	LargeConfidence float64   `gorm:"not null;default:0.8" json:"large_confidence"`
	SmallIOU        float64   `gorm:"not null;default:0.8" json:"small_iou"`
	AlarmLevelID    string    `gorm:"size:64;not null;index" json:"alarm_level_id"`
	Notes           string    `gorm:"size:255" json:"notes"`
	LastStartAt     time.Time `json:"last_start_at"`
	LastStopAt      time.Time `json:"last_stop_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (VideoTask) TableName() string { return "mb_video_tasks" }

type VideoTaskDevice struct {
	TaskID    string    `gorm:"primaryKey;size:64" json:"task_id"`
	DeviceID  string    `gorm:"primaryKey;size:64;index" json:"device_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (VideoTaskDevice) TableName() string { return "mb_video_task_devices" }

type VideoTaskAlgorithm struct {
	TaskID      string    `gorm:"primaryKey;size:64" json:"task_id"`
	AlgorithmID string    `gorm:"primaryKey;size:64;index" json:"algorithm_id"`
	CreatedAt   time.Time `json:"created_at"`
}

func (VideoTaskAlgorithm) TableName() string { return "mb_video_task_algorithms" }

type VideoTaskDeviceProfile struct {
	TaskID           string    `gorm:"primaryKey;size:64" json:"task_id"`
	DeviceID         string    `gorm:"primaryKey;size:64;index;uniqueIndex:uk_mb_video_task_device_profiles_device" json:"device_id"`
	FrameInterval    int       `gorm:"not null;default:5" json:"frame_interval"`
	FrameRateMode    string    `gorm:"size:16;not null;default:interval" json:"frame_rate_mode"`
	FrameRateValue   int       `gorm:"not null;default:5" json:"frame_rate_value"`
	SmallConfidence  float64   `gorm:"not null;default:0.5" json:"small_confidence"`
	LargeConfidence  float64   `gorm:"not null;default:0.8" json:"large_confidence"`
	SmallIOU         float64   `gorm:"not null;default:0.8" json:"small_iou"`
	AlarmLevelID     string    `gorm:"size:64;not null;index" json:"alarm_level_id"`
	RecordingPolicy  string    `gorm:"size:32;not null;default:none" json:"recording_policy"`
	AlarmPreSeconds  int       `gorm:"not null;default:8" json:"alarm_pre_seconds"`
	AlarmPostSeconds int       `gorm:"not null;default:12" json:"alarm_post_seconds"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (VideoTaskDeviceProfile) TableName() string { return "mb_video_task_device_profiles" }

type VideoTaskDeviceAlgorithm struct {
	TaskID            string    `gorm:"primaryKey;size:64" json:"task_id"`
	DeviceID          string    `gorm:"primaryKey;size:64;index" json:"device_id"`
	AlgorithmID       string    `gorm:"primaryKey;size:64;index" json:"algorithm_id"`
	AlarmLevelID      string    `gorm:"size:64;not null;default:alarm_level_1;index" json:"alarm_level_id"`
	AlertCycleSeconds int       `gorm:"not null;default:60" json:"alert_cycle_seconds"`
	CreatedAt         time.Time `json:"created_at"`
}

func (VideoTaskDeviceAlgorithm) TableName() string { return "mb_video_task_device_algorithms" }

type AlgorithmTestRecord struct {
	ID               string    `gorm:"primaryKey;size:64" json:"id"`
	AlgorithmID      string    `gorm:"size:64;not null;index" json:"algorithm_id"`
	BatchID          string    `gorm:"size:64;not null;default:'';index" json:"batch_id"`
	MediaType        string    `gorm:"size:16;not null;default:'image';index" json:"media_type"`
	MediaPath        string    `gorm:"size:1024" json:"media_path"`
	OriginalFileName string    `gorm:"size:255;not null;default:''" json:"original_file_name"`
	ImagePath        string    `gorm:"size:1024" json:"image_path"`
	RequestPayload   string    `gorm:"type:text" json:"request_payload"`
	ResponsePayload  string    `gorm:"type:text" json:"response_payload"`
	Success          bool      `gorm:"not null;default:false" json:"success"`
	CreatedAt        time.Time `json:"created_at"`
}

func (AlgorithmTestRecord) TableName() string { return "mb_algorithm_test_records" }

type AlgorithmTestJob struct {
	ID           string    `gorm:"primaryKey;size:64" json:"id"`
	AlgorithmID  string    `gorm:"size:64;not null;default:'';index" json:"algorithm_id"`
	BatchID      string    `gorm:"size:64;not null;default:'';index" json:"batch_id"`
	CameraID     string    `gorm:"size:64;not null;default:''" json:"camera_id"`
	Status       string    `gorm:"size:32;not null;default:'pending';index" json:"status"`
	TotalCount   int       `gorm:"not null;default:0" json:"total_count"`
	SuccessCount int       `gorm:"not null;default:0" json:"success_count"`
	FailedCount  int       `gorm:"not null;default:0" json:"failed_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (AlgorithmTestJob) TableName() string { return "mb_algorithm_test_jobs" }

type AlgorithmTestJobItem struct {
	ID               string    `gorm:"primaryKey;size:64" json:"id"`
	JobID            string    `gorm:"size:64;not null;default:'';index:idx_mb_algorithm_test_job_items_job_sort,priority:1;index" json:"job_id"`
	AlgorithmID      string    `gorm:"size:64;not null;default:'';index" json:"algorithm_id"`
	SortOrder        int       `gorm:"not null;default:0;index:idx_mb_algorithm_test_job_items_job_sort,priority:2" json:"sort_order"`
	FileName         string    `gorm:"size:255;not null;default:''" json:"file_name"`
	MediaType        string    `gorm:"size:16;not null;default:'';index" json:"media_type"`
	MediaPath        string    `gorm:"size:1024" json:"media_path"`
	OriginalFileName string    `gorm:"size:255;not null;default:''" json:"original_file_name"`
	Status           string    `gorm:"size:32;not null;default:'pending';index" json:"status"`
	Success          bool      `gorm:"not null;default:false" json:"success"`
	RecordID         string    `gorm:"size:64;not null;default:'';index" json:"record_id"`
	Conclusion       string    `gorm:"type:text;not null;default:''" json:"conclusion"`
	Basis            string    `gorm:"type:text;not null;default:''" json:"basis"`
	NormalizedBoxes  string    `gorm:"type:text;not null;default:'[]'" json:"normalized_boxes"`
	SnapshotWidth    int       `gorm:"not null;default:0" json:"snapshot_width"`
	SnapshotHeight   int       `gorm:"not null;default:0" json:"snapshot_height"`
	AnomalyTimes     string    `gorm:"type:text;not null;default:'[]'" json:"anomaly_times"`
	DurationSeconds  float64   `gorm:"not null;default:0" json:"duration_seconds"`
	ErrorMessage     string    `gorm:"type:text;not null;default:''" json:"error_message"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (AlgorithmTestJobItem) TableName() string { return "mb_algorithm_test_job_items" }

type LLMUsageCall struct {
	ID               string    `gorm:"primaryKey;size:64" json:"id"`
	Source           string    `gorm:"size:32;not null;index:idx_mb_llm_usage_calls_source_occurred_at,priority:1" json:"source"`
	TaskID           string    `gorm:"size:64;index:idx_mb_llm_usage_calls_task_occurred_at,priority:1" json:"task_id"`
	DeviceID         string    `gorm:"size:64;index:idx_mb_llm_usage_calls_device_occurred_at,priority:1" json:"device_id"`
	ProviderID       string    `gorm:"size:64;index:idx_mb_llm_usage_calls_provider_occurred_at,priority:1" json:"provider_id"`
	ProviderName     string    `gorm:"size:128" json:"provider_name"`
	Model            string    `gorm:"size:256;index:idx_mb_llm_usage_calls_model_occurred_at,priority:1" json:"model"`
	DetectMode       int       `gorm:"not null;default:0" json:"detect_mode"`
	CallStatus       string    `gorm:"size:32;not null;index:idx_mb_llm_usage_calls_status_occurred_at,priority:1" json:"call_status"`
	UsageAvailable   bool      `gorm:"not null;default:false;index:idx_mb_llm_usage_calls_usage_occurred_at,priority:1" json:"usage_available"`
	PromptTokens     *int      `json:"prompt_tokens"`
	CompletionTokens *int      `json:"completion_tokens"`
	TotalTokens      *int      `json:"total_tokens"`
	LatencyMS        float64   `gorm:"not null;default:0" json:"latency_ms"`
	ErrorMessage     string    `gorm:"size:1024" json:"error_message"`
	RequestContext   string    `gorm:"size:512" json:"request_context"`
	OccurredAt       time.Time `gorm:"not null;index:idx_mb_llm_usage_calls_source_occurred_at,priority:2;index:idx_mb_llm_usage_calls_provider_occurred_at,priority:2;index:idx_mb_llm_usage_calls_model_occurred_at,priority:2;index:idx_mb_llm_usage_calls_status_occurred_at,priority:2;index:idx_mb_llm_usage_calls_usage_occurred_at,priority:2;index:idx_mb_llm_usage_calls_task_occurred_at,priority:2;index:idx_mb_llm_usage_calls_device_occurred_at,priority:2" json:"occurred_at"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (LLMUsageCall) TableName() string { return "mb_llm_usage_calls" }

type LLMUsageHourly struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	BucketStart      time.Time `gorm:"not null;uniqueIndex:uk_mb_llm_usage_hourly,priority:1;index:idx_mb_llm_usage_hourly_bucket" json:"bucket_start"`
	Source           string    `gorm:"size:32;not null;uniqueIndex:uk_mb_llm_usage_hourly,priority:2" json:"source"`
	ProviderID       string    `gorm:"size:64;uniqueIndex:uk_mb_llm_usage_hourly,priority:3" json:"provider_id"`
	Model            string    `gorm:"size:256;uniqueIndex:uk_mb_llm_usage_hourly,priority:4" json:"model"`
	CallStatus       string    `gorm:"size:32;not null;uniqueIndex:uk_mb_llm_usage_hourly,priority:5" json:"call_status"`
	UsageAvailable   bool      `gorm:"not null;uniqueIndex:uk_mb_llm_usage_hourly,priority:6" json:"usage_available"`
	CallCount        int64     `gorm:"not null;default:0" json:"call_count"`
	PromptTokens     int64     `gorm:"not null;default:0" json:"prompt_tokens"`
	CompletionTokens int64     `gorm:"not null;default:0" json:"completion_tokens"`
	TotalTokens      int64     `gorm:"not null;default:0" json:"total_tokens"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (LLMUsageHourly) TableName() string { return "mb_llm_usage_hourly" }

type LLMUsageDaily struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	BucketDate       time.Time `gorm:"not null;uniqueIndex:uk_mb_llm_usage_daily,priority:1;index:idx_mb_llm_usage_daily_bucket" json:"bucket_date"`
	Source           string    `gorm:"size:32;not null;uniqueIndex:uk_mb_llm_usage_daily,priority:2" json:"source"`
	ProviderID       string    `gorm:"size:64;uniqueIndex:uk_mb_llm_usage_daily,priority:3" json:"provider_id"`
	Model            string    `gorm:"size:256;uniqueIndex:uk_mb_llm_usage_daily,priority:4" json:"model"`
	CallStatus       string    `gorm:"size:32;not null;uniqueIndex:uk_mb_llm_usage_daily,priority:5" json:"call_status"`
	UsageAvailable   bool      `gorm:"not null;uniqueIndex:uk_mb_llm_usage_daily,priority:6" json:"usage_available"`
	CallCount        int64     `gorm:"not null;default:0" json:"call_count"`
	PromptTokens     int64     `gorm:"not null;default:0" json:"prompt_tokens"`
	CompletionTokens int64     `gorm:"not null;default:0" json:"completion_tokens"`
	TotalTokens      int64     `gorm:"not null;default:0" json:"total_tokens"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (LLMUsageDaily) TableName() string { return "mb_llm_usage_daily" }

type AlarmEvent struct {
	ID             string     `gorm:"primaryKey;size:64" json:"id"`
	TaskID         string     `gorm:"size:64;not null;index" json:"task_id"`
	DeviceID       string     `gorm:"size:64;not null;index" json:"device_id"`
	AlgorithmID    string     `gorm:"size:64;not null;index" json:"algorithm_id"`
	EventSource    string     `gorm:"size:32;not null;default:runtime;index" json:"event_source"`
	DisplayName    string     `gorm:"size:255" json:"display_name"`
	PromptText     string     `gorm:"type:text" json:"prompt_text"`
	AlarmLevelID   string     `gorm:"size:64;not null;index" json:"alarm_level_id"`
	Status         string     `gorm:"size:32;not null;default:pending;index" json:"status"`
	ReviewNote     string     `gorm:"size:500" json:"review_note"`
	ReviewedBy     string     `gorm:"size:64" json:"reviewed_by"`
	ReviewedAt     time.Time  `json:"reviewed_at"`
	OccurredAt     time.Time  `gorm:"not null;index" json:"occurred_at"`
	SnapshotPath   string     `gorm:"size:1024" json:"snapshot_path"`
	SnapshotWidth  int        `gorm:"not null;default:0" json:"snapshot_width"`
	SnapshotHeight int        `gorm:"not null;default:0" json:"snapshot_height"`
	BoxesJSON      string     `gorm:"type:text" json:"boxes_json"`
	YoloJSON       string     `gorm:"type:text" json:"yolo_json"`
	LLMJSON        string     `gorm:"type:text" json:"llm_json"`
	SourceCallback string     `gorm:"type:text" json:"source_callback"`
	ClipSessionID  string     `gorm:"size:64;index" json:"clip_session_id"`
	ClipReady      bool       `gorm:"not null;default:false" json:"clip_ready"`
	ClipPath       string     `gorm:"size:1024" json:"clip_path"`
	ClipFilesJSON  string     `gorm:"type:text" json:"clip_files_json"`
	NotifiedAt     *time.Time `gorm:"index" json:"notified_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (AlarmEvent) TableName() string { return "mb_alarm_events" }

type AlarmClipSession struct {
	ID             string    `gorm:"primaryKey;size:64" json:"id"`
	SourceID       string    `gorm:"size:64;not null;index:idx_mb_alarm_clip_session_source_status" json:"source_id"`
	Status         string    `gorm:"size:32;not null;index:idx_mb_alarm_clip_session_source_status" json:"status"`
	AnchorEventID  string    `gorm:"size:64;not null" json:"anchor_event_id"`
	PreSeconds     int       `gorm:"not null;default:8" json:"pre_seconds"`
	PostSeconds    int       `gorm:"not null;default:12" json:"post_seconds"`
	StartedAt      time.Time `gorm:"not null;index" json:"started_at"`
	LastAlarmAt    time.Time `gorm:"not null;index" json:"last_alarm_at"`
	ExpectedEndAt  time.Time `gorm:"not null;index:idx_mb_alarm_clip_session_expected_end" json:"expected_end_at"`
	HardDeadlineAt time.Time `gorm:"not null" json:"hard_deadline_at"`
	ClipPath       string    `gorm:"size:1024" json:"clip_path"`
	ClipFilesJSON  string    `gorm:"type:text" json:"clip_files_json"`
	FinalizedAt    time.Time `json:"finalized_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (AlarmClipSession) TableName() string { return "mb_alarm_clip_sessions" }

type AlarmClipSessionEvent struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID       string    `gorm:"size:64;not null;index:idx_mb_alarm_clip_session_event_session;uniqueIndex:uk_mb_alarm_clip_session_event" json:"session_id"`
	EventID         string    `gorm:"size:64;not null;uniqueIndex:uk_mb_alarm_clip_session_event" json:"event_id"`
	EventOccurredAt time.Time `gorm:"not null;index" json:"event_occurred_at"`
	CreatedAt       time.Time `json:"created_at"`
}

func (AlarmClipSessionEvent) TableName() string { return "mb_alarm_clip_session_events" }

type User struct {
	ID           string    `gorm:"primaryKey;size:64" json:"id"`
	Username     string    `gorm:"size:64;not null;uniqueIndex" json:"username"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	Enabled      bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (User) TableName() string { return "mb_users" }

type Role struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	Name      string    `gorm:"size:64;not null;uniqueIndex" json:"name"`
	Remark    string    `gorm:"size:255" json:"remark"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Role) TableName() string { return "mb_roles" }

type Menu struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	Name      string    `gorm:"size:64;not null" json:"name"`
	Path      string    `gorm:"size:255;index" json:"path"`
	MenuType  string    `gorm:"size:32;not null;default:menu;index" json:"menu_type"`
	ViewPath  string    `gorm:"size:255" json:"view_path"`
	Icon      string    `gorm:"size:64" json:"icon"`
	ParentID  string    `gorm:"size:64;index" json:"parent_id"`
	Sort      int       `gorm:"not null;default:0" json:"sort"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Menu) TableName() string { return "mb_menus" }

type UserRole struct {
	UserID    string    `gorm:"primaryKey;size:64" json:"user_id"`
	RoleID    string    `gorm:"primaryKey;size:64" json:"role_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (UserRole) TableName() string { return "mb_user_roles" }

type RoleMenu struct {
	RoleID    string    `gorm:"primaryKey;size:64" json:"role_id"`
	MenuID    string    `gorm:"primaryKey;size:64" json:"menu_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (RoleMenu) TableName() string { return "mb_role_menus" }

type SystemSetting struct {
	Key       string    `gorm:"primaryKey;size:128" json:"key"`
	Value     string    `gorm:"type:text;not null" json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (SystemSetting) TableName() string { return "mb_system_settings" }
