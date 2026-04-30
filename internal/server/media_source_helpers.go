package server

import (
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"

	"maas-box/internal/model"
)

func normalizeMediaSourceType(sourceType, protocol string) string {
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	switch sourceType {
	case model.SourceTypePull, model.SourceTypePush, model.SourceTypeGB28181:
		return sourceType
	}
	switch protocol {
	case model.ProtocolRTSP:
		return model.SourceTypePull
	case model.ProtocolRTMP:
		return model.SourceTypePush
	case model.ProtocolGB28181:
		return model.SourceTypeGB28181
	default:
		return ""
	}
}

func sourceOutputConfigMap(source model.MediaSource) map[string]string {
	out := map[string]string{}
	if parsed, err := parseOutputConfigMap(source.OutputConfig); err == nil {
		for key, value := range parsed {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			out[key] = value
		}
	}
	mergeOutputURLToMap(out, source)
	return out
}

func mergeOutputURLToMap(out map[string]string, source model.MediaSource) {
	if out == nil {
		return
	}
	setIfNotEmpty(out, "webrtc", source.PlayWebRTCURL)
	setIfNotEmpty(out, "ws_flv", source.PlayWSFLVURL)
	setIfNotEmpty(out, "http_flv", source.PlayHTTPFLVURL)
	setIfNotEmpty(out, "hls", source.PlayHLSURL)
	setIfNotEmpty(out, "rtsp", source.PlayRTSPURL)
	setIfNotEmpty(out, "rtmp", source.PlayRTMPURL)
	setIfNotEmpty(out, "zlm_app", source.App)
	setIfNotEmpty(out, "zlm_stream", source.StreamID)
}

func setIfNotEmpty(target map[string]string, key, value string) {
	if target == nil {
		return
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	target[strings.TrimSpace(key)] = value
}

func applyOutputConfigToSource(source *model.MediaSource, output map[string]string) {
	if source == nil {
		return
	}
	if output == nil {
		output = map[string]string{}
	}
	normalized := make(map[string]string, len(output)+2)
	for key, value := range output {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		normalized[key] = value
	}
	if app := strings.TrimSpace(normalized["zlm_app"]); app != "" {
		source.App = app
	} else if strings.TrimSpace(source.App) != "" {
		normalized["zlm_app"] = strings.TrimSpace(source.App)
	}
	if streamID := strings.TrimSpace(normalized["zlm_stream"]); streamID != "" {
		source.StreamID = streamID
	} else if strings.TrimSpace(source.StreamID) != "" {
		normalized["zlm_stream"] = strings.TrimSpace(source.StreamID)
	}
	source.PlayWebRTCURL = strings.TrimSpace(normalized["webrtc"])
	source.PlayWSFLVURL = strings.TrimSpace(normalized["ws_flv"])
	source.PlayHTTPFLVURL = strings.TrimSpace(normalized["http_flv"])
	source.PlayHLSURL = strings.TrimSpace(normalized["hls"])
	source.PlayRTSPURL = strings.TrimSpace(normalized["rtsp"])
	source.PlayRTMPURL = strings.TrimSpace(normalized["rtmp"])
	raw, err := json.Marshal(normalized)
	if err != nil {
		source.OutputConfig = "{}"
		return
	}
	source.OutputConfig = string(raw)
}

func findPushTokenBySource(db *gorm.DB, sourceID string) string {
	if db == nil {
		return ""
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return ""
	}
	var item model.StreamPush
	if err := db.Where("source_id = ?", sourceID).First(&item).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(item.PublishToken)
}

func ensureAppStreamUnique(db *gorm.DB, currentID, app, streamID string) error {
	if db == nil {
		return nil
	}
	app = strings.TrimSpace(app)
	streamID = strings.TrimSpace(streamID)
	if app == "" || streamID == "" {
		return nil
	}
	var hit model.MediaSource
	err := db.Where("app = ? AND stream_id = ?", app, streamID).First(&hit).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(hit.ID) == strings.TrimSpace(currentID) {
		return nil
	}
	return errors.New("app and stream_id already exists")
}
