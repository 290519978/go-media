package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

type StartCameraRequest struct {
	CameraID         string                       `json:"camera_id"`
	RTSPURL          string                       `json:"rtsp_url"`
	CallbackURL      string                       `json:"callback_url"`
	CallbackSecret   string                       `json:"callback_secret"`
	RetryLimit       int                          `json:"retry_limit"`
	DetectRateMode   string                       `json:"detect_rate_mode"`
	DetectRateValue  int                          `json:"detect_rate_value"`
	AlgorithmConfigs []StartCameraAlgorithmConfig `json:"algorithm_configs"`
	LLMAPIURL        string                       `json:"llm_api_url"`
	LLMAPIKey        string                       `json:"llm_api_key"`
	LLMModel         string                       `json:"llm_model"`
	LLMPrompt        string                       `json:"llm_prompt"`
}

type StartCameraAlgorithmConfig struct {
	AlgorithmID       string   `json:"algorithm_id"`
	TaskCode          string   `json:"task_code"`
	DetectMode        int      `json:"detect_mode"`
	Labels            []string `json:"labels"`
	YoloThreshold     float64  `json:"yolo_threshold"`
	IOUThreshold      float64  `json:"iou_threshold"`
	LabelsTriggerMode string   `json:"labels_trigger_mode"`
}

type StopCameraRequest struct {
	CameraID string `json:"camera_id"`
}

type GenericResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type StartCameraResponse struct {
	Success      bool    `json:"success"`
	Message      string  `json:"message"`
	CameraID     string  `json:"camera_id"`
	SourceWidth  int     `json:"source_width"`
	SourceHeight int     `json:"source_height"`
	SourceFPS    float64 `json:"source_fps"`
}

type StatusResponse struct {
	IsReady bool `json:"is_ready"`
	Cameras []struct {
		CameraID        string `json:"camera_id"`
		Status          string `json:"status"`
		FramesProcessed int64  `json:"frames_processed"`
		RetryCount      int    `json:"retry_count"`
		LastError       string `json:"last_error"`
	} `json:"cameras"`
	Stats map[string]any `json:"stats"`
}

type AnalyzeImageRequest struct {
	ImageRelPath     string                       `json:"image_rel_path"`
	AlgorithmConfigs []StartCameraAlgorithmConfig `json:"algorithm_configs"`
	LLMAPIURL        string                       `json:"llm_api_url,omitempty"`
	LLMAPIKey        string                       `json:"llm_api_key,omitempty"`
	LLMModel         string                       `json:"llm_model,omitempty"`
	LLMPrompt        string                       `json:"llm_prompt,omitempty"`
}

type LLMUsage struct {
	CallID           string  `json:"call_id"`
	CallStatus       string  `json:"call_status"`
	UsageAvailable   bool    `json:"usage_available"`
	PromptTokens     *int    `json:"prompt_tokens"`
	CompletionTokens *int    `json:"completion_tokens"`
	TotalTokens      *int    `json:"total_tokens"`
	LatencyMS        float64 `json:"latency_ms"`
	Model            string  `json:"model"`
	ErrorMessage     string  `json:"error_message"`
	RequestContext   string  `json:"request_context"`
}

type AnalyzeImageResponse struct {
	Success          bool            `json:"success"`
	Message          string          `json:"message"`
	Detections       json.RawMessage `json:"detections"`
	AlgorithmResults json.RawMessage `json:"algorithm_results"`
	LLMResult        string          `json:"llm_result"`
	LLMUsage         *LLMUsage       `json:"llm_usage"`
}

type SequenceAnomalyTime struct {
	TimestampMS   int64  `json:"timestamp_ms"`
	TimestampText string `json:"timestamp_text"`
	Reason        string `json:"reason"`
}

type AnalyzeVideoTestRequest struct {
	VideoRelPath     string                       `json:"video_rel_path"`
	FPS              int                          `json:"fps,omitempty"`
	AlgorithmConfigs []StartCameraAlgorithmConfig `json:"algorithm_configs"`
	LLMAPIURL        string                       `json:"llm_api_url,omitempty"`
	LLMAPIKey        string                       `json:"llm_api_key,omitempty"`
	LLMModel         string                       `json:"llm_model,omitempty"`
	LLMPrompt        string                       `json:"llm_prompt,omitempty"`
}

type AnalyzeVideoTestResponse struct {
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	LLMResult string    `json:"llm_result"`
	LLMUsage  *LLMUsage `json:"llm_usage"`
}

const defaultAnalyzeVideoTestTimeout = 3 * time.Minute

func (c *Client) StartCamera(ctx context.Context, req StartCameraRequest) (*StartCameraResponse, error) {
	var out StartCameraResponse
	if err := c.postJSON(ctx, "/api/start_camera", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) StopCamera(ctx context.Context, req StopCameraRequest) (*GenericResponse, error) {
	var out GenericResponse
	if err := c.postJSON(ctx, "/api/stop_camera", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Status(ctx context.Context) (*StatusResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("read ai status failed: %w; partial_body=%s", readErr, summarizeAIResponseBody(body))
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ai status failed: %s", summarizeAIResponseBody(body))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, fmt.Errorf("ai status response is empty")
	}
	var out StatusResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode ai status failed: %w; body=%s", err, summarizeAIResponseBody(body))
	}
	return &out, nil
}

func (c *Client) AnalyzeImage(ctx context.Context, req AnalyzeImageRequest) (*AnalyzeImageResponse, error) {
	var out AnalyzeImageResponse
	if err := c.postJSON(ctx, "/api/analyze_image", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) AnalyzeVideoTest(ctx context.Context, req AnalyzeVideoTestRequest) (*AnalyzeVideoTestResponse, error) {
	var out AnalyzeVideoTestResponse
	if err := c.postJSONWithTimeout(ctx, "/api/analyze_video_test", req, &out, defaultAnalyzeVideoTestTimeout); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) postJSON(ctx context.Context, path string, payload any, out any) error {
	return c.postJSONWithTimeout(ctx, path, payload, out, 0)
}

func (c *Client) postJSONWithTimeout(ctx context.Context, path string, payload any, out any, timeout time.Duration) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpClient := c.httpClient
	if timeout > 0 {
		cloned := *c.httpClient
		cloned.Timeout = timeout
		httpClient = &cloned
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read ai response failed: %w; partial_body=%s", readErr, summarizeAIResponseBody(respBody))
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ai request failed [%d]: %s", resp.StatusCode, summarizeAIResponseBody(respBody))
	}
	if len(bytes.TrimSpace(respBody)) == 0 {
		return fmt.Errorf("ai response is empty")
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode ai response failed: %w; body=%s", err, summarizeAIResponseBody(respBody))
	}
	return nil
}

func summarizeAIResponseBody(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "<empty>"
	}
	const limit = 256
	if len(trimmed) <= limit {
		return trimmed
	}
	return trimmed[:limit] + "...(truncated)"
}
