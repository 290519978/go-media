package server

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/disk"
	"gorm.io/gorm"
	"maas-box/internal/logutil"
	"maas-box/internal/model"
)

type deviceUpsertRequest struct {
	Name         string `json:"name"`
	AreaID       string `json:"area_id"`
	SourceType   string `json:"source_type"`
	Protocol     string `json:"protocol"`
	Transport    string `json:"transport"`
	OriginURL    string `json:"origin_url"`
	StreamURL    string `json:"stream_url"`
	App          string `json:"app"`
	StreamID     string `json:"stream_id"`
	PublishToken string `json:"publish_token"`
	SnapshotURL  string `json:"snapshot_url"`
}

type recordingDeleteRequest struct {
	Paths []string `json:"paths"`
}

type recordingExportRequest struct {
	Paths []string `json:"paths"`
}

type gb28181VerifyRequest struct {
	SIPServerID       string `json:"sip_server_id"`
	SIPDomain         string `json:"sip_domain"`
	SIPIP             string `json:"sip_ip"`
	SIPPort           int    `json:"sip_port"`
	Transport         string `json:"transport"`
	DeviceID          string `json:"device_id"`
	Password          string `json:"password"`
	MediaIP           string `json:"media_ip"`
	MediaPort         int    `json:"media_port"`
	RegisterExpires   int    `json:"register_expires"`
	KeepaliveInterval int    `json:"keepalive_interval"`
}

var (
	gbIDPattern = regexp.MustCompile(`^\d{20}$`)
	gbDomainReg = regexp.MustCompile(`^\d{10}(\d{10})?$`)
	hostReg     = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`)
)

const (
	deviceSnapshotDirName = "device_snapshots"
	recordingKindNormal   = "normal"
	recordingKindAlarm    = "alarm"
	recordingExportMaxCnt = 200
	recordingExportMaxSum = int64(2 * 1024 * 1024 * 1024)
)

func (s *Server) registerDeviceRoutes(r gin.IRouter) {
	g := r.Group("/devices")
	g.GET("", s.listDevices)
	g.GET("/snapshot/*path", s.getDeviceSnapshotFile)
	g.GET("/blacklist", s.listSourceBlocks)
	g.POST("/blacklist/gb28181", s.addGBDeviceBlock)
	g.DELETE("/blacklist/gb28181/:device_id", s.removeGBDeviceBlock)
	g.POST("/blacklist/rtmp", s.addRTMPStreamBlock)
	g.DELETE("/blacklist/rtmp/:app/:stream_id", s.removeRTMPStreamBlock)
	g.POST("/:id/snapshot", s.captureDeviceSnapshot)
	g.GET("/:id", s.getDevice)
	g.POST("", s.createDevice)
	g.PUT("/:id", s.updateDevice)
	g.POST("/:id/preview", s.previewDevice)
	g.DELETE("/:id", s.deleteDevice)
	g.GET("/:id/recording-status", s.getRecordingStatus)
	g.GET("/:id/recordings", s.listRecordings)
	g.GET("/:id/recordings/file/*path", s.downloadRecording)
	g.POST("/:id/recordings/export", s.exportRecordings)
	g.DELETE("/:id/recordings", s.deleteRecordings)
	g.GET("/gb28181/info", s.gb28181Info)
	g.POST("/gb28181/verify", s.verifyGB28181Config)
	g.GET("/gb28181/devices", s.listGBDevices)
	g.POST("/gb28181/devices", s.createGBDevice)
	g.PUT("/gb28181/devices/:device_id", s.updateGBDevice)
	g.DELETE("/gb28181/devices/:device_id", s.deleteGBDevice)
	g.POST("/gb28181/devices/:device_id/catalog", s.queryGBDeviceCatalog)
	g.GET("/gb28181/devices/:device_id/channels", s.listGBDeviceChannels)
	g.PUT("/gb28181/channels/:channel_id", s.updateGBChannel)
	g.GET("/gb28181/stats", s.gb28181Stats)
	g.GET("/discover/lan", s.discoverLANDevice)
}

func (s *Server) listDevices(c *gin.Context) {
	var devices []model.MediaSource
	query := s.db.Model(&model.MediaSource{})
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("name LIKE ? OR stream_id LIKE ? OR stream_url LIKE ?", like, like, like)
	}
	if sourceType := strings.TrimSpace(strings.ToLower(c.Query("source_type"))); sourceType != "" {
		query = query.Where("source_type = ?", sourceType)
	}
	if rowKind := strings.TrimSpace(strings.ToLower(c.Query("row_kind"))); rowKind != "" {
		query = query.Where("row_kind = ?", rowKind)
	}
	if areaID := strings.TrimSpace(c.Query("area_id")); areaID != "" {
		query = query.Where("area_id = ?", areaID)
	}
	if protocol := strings.TrimSpace(c.Query("protocol")); protocol != "" {
		query = query.Where("protocol = ?", protocol)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", strings.ToLower(status))
	}
	if err := query.Order("created_at desc").Find(&devices).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query devices failed")
		return
	}

	for i := range devices {
		s.applySourceOutputPolicyView(&devices[i])
	}
	// 前端根据该能力位决定是否展示“持续录制”选项。
	s.ok(c, gin.H{
		"items":                      devices,
		"total":                      len(devices),
		"allow_continuous_recording": s.cfg.Server.Recording.AllowContinuous,
		"recording_default_mode":     s.recordingDefaultMode(),
		"recording_modes":            s.recordingModeOptions(),
		"alarm_clip_enabled_default": s.cfg.Server.Recording.AlarmClip.EnabledDefault,
		"alarm_clip_pre_default":     s.cfg.Server.Recording.AlarmClip.PreSeconds,
		"alarm_clip_post_default":    s.cfg.Server.Recording.AlarmClip.PostSeconds,
	})
}

func (s *Server) getDevice(c *gin.Context) {
	id := c.Param("id")
	var source model.MediaSource
	if err := s.db.Where("id = ?", id).First(&source).Error; err != nil {
		s.fail(c, http.StatusNotFound, "device not found")
		return
	}
	s.applySourceOutputPolicyView(&source)
	s.ok(c, source)
}

func (s *Server) createDevice(c *gin.Context) {
	var in deviceUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	sourceType := normalizeMediaSourceType(in.SourceType, in.Protocol)
	if sourceType != model.SourceTypePull && sourceType != model.SourceTypePush {
		s.fail(c, http.StatusBadRequest, "source_type must be pull or push")
		return
	}

	alarmPreSeconds := s.alarmClipDefaultPreSeconds()
	alarmPostSeconds := s.alarmClipDefaultPostSeconds()

	in.Name = strings.TrimSpace(in.Name)
	in.AreaID = strings.TrimSpace(in.AreaID)
	in.OriginURL = strings.TrimSpace(in.OriginURL)
	in.StreamURL = strings.TrimSpace(in.StreamURL)
	in.App = strings.TrimSpace(in.App)
	in.StreamID = strings.TrimSpace(in.StreamID)
	in.PublishToken = strings.TrimSpace(in.PublishToken)
	if in.Name == "" {
		s.fail(c, http.StatusBadRequest, "name is required")
		return
	}
	if in.AreaID == "" {
		if sourceType == model.SourceTypePush {
			in.AreaID = model.RootAreaID
		} else {
			s.fail(c, http.StatusBadRequest, "area_id is required")
			return
		}
	}
	var areaCount int64
	if err := s.db.Model(&model.Area{}).Where("id = ?", in.AreaID).Count(&areaCount).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query area failed")
		return
	}
	if areaCount == 0 {
		s.fail(c, http.StatusBadRequest, "area does not exist")
		return
	}

	sourceID := uuid.NewString()
	source := model.MediaSource{
		ID:               sourceID,
		Name:             in.Name,
		AreaID:           in.AreaID,
		SourceType:       sourceType,
		RowKind:          model.RowKindChannel,
		Status:           "offline",
		AIStatus:         model.DeviceAIStatusIdle,
		EnableRecording:  false,
		RecordingMode:    model.RecordingModeNone,
		RecordingStatus:  "stopped",
		EnableAlarmClip:  false,
		AlarmPreSeconds:  alarmPreSeconds,
		AlarmPostSeconds: alarmPostSeconds,
		MediaServerID:    "local",
		SnapshotURL:      strings.TrimSpace(in.SnapshotURL),
		ExtraJSON:        "{}",
		OutputConfig:     "{}",
	}

	requestHost := resolveRequestHost(c)
	var (
		outputConfigMap map[string]string
		zlmErr          error
		streamProxy     *model.StreamProxy
		streamPush      *model.StreamPush
	)

	if sourceType == model.SourceTypePull {
		originURL := strings.TrimSpace(firstNonEmpty(in.OriginURL, in.StreamURL))
		if err := validateRTSPStreamURL(originURL); err != nil {
			s.fail(c, http.StatusBadRequest, err.Error())
			return
		}
		transport := strings.ToLower(strings.TrimSpace(in.Transport))
		if !validateTransport(transport) {
			s.fail(c, http.StatusBadRequest, "transport must be tcp or udp")
			return
		}
		retryCount := defaultStreamProxyRetryCount
		outputConfigMap, zlmErr = s.ensureZLMProxy(source.ID, originURL, transport, retryCount, requestHost)
		if zlmErr != nil {
			s.fail(c, http.StatusBadGateway, "zlm pull stream failed: "+zlmErr.Error())
			return
		}
		source.Protocol = model.ProtocolRTSP
		source.Transport = transport
		source.StreamURL = originURL
		source.App = strings.TrimSpace(firstNonEmpty(outputConfigMap["zlm_app"], strings.TrimSpace(s.cfg.Server.ZLM.App), "live"))
		source.StreamID = strings.TrimSpace(firstNonEmpty(outputConfigMap["zlm_stream"], buildZLMStreamID(source.ID)))
		streamProxy = &model.StreamProxy{
			SourceID:   source.ID,
			OriginURL:  originURL,
			Transport:  transport,
			Enable:     true,
			RetryCount: retryCount,
		}
	} else {
		app := in.App
		streamID := in.StreamID
		streamURL := in.StreamURL
		if streamURL != "" {
			if err := validateRTMPStreamURL(streamURL); err != nil {
				s.fail(c, http.StatusBadRequest, err.Error())
				return
			}
			parsedApp, parsedStream, perr := parseRTMPAppStream(streamURL)
			if perr != nil {
				s.fail(c, http.StatusBadRequest, perr.Error())
				return
			}
			if app == "" {
				app = parsedApp
			}
			if streamID == "" {
				streamID = parsedStream
			}
		}
		if app == "" || streamID == "" {
			s.fail(c, http.StatusBadRequest, "app and stream_id are required for push source")
			return
		}
		outputConfigMap = s.buildZLMOutputConfig(app, streamID, requestHost)
		if streamURL == "" {
			streamURL = s.buildZLMRTMPPublishURL(app, streamID, requestHost)
		}
		outputConfigMap["publish_url"] = streamURL
		if in.PublishToken != "" {
			outputConfigMap["publish_token"] = in.PublishToken
		}
		source.Protocol = model.ProtocolRTMP
		source.Transport = "tcp"
		source.StreamURL = streamURL
		source.Status = "offline"
		source.App = app
		source.StreamID = streamID
		streamPush = &model.StreamPush{
			SourceID:     source.ID,
			PublishToken: in.PublishToken,
		}
	}

	applyOutputConfigToSource(&source, outputConfigMap)
	s.syncPullSourceOnlineStatus(&source, false)
	if err := ensureAppStreamUnique(s.db, "", source.App, source.StreamID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			s.fail(c, http.StatusConflict, "app and stream_id already exists")
			return
		}
		s.fail(c, http.StatusInternalServerError, "check app/stream uniqueness failed")
		return
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if source.SourceType == model.SourceTypePush && strings.EqualFold(source.Protocol, model.ProtocolRTMP) {
			if err := clearStreamBlockTx(tx, source.App, source.StreamID, strings.TrimSpace(s.cfg.Server.ZLM.App)); err != nil {
				return err
			}
		}
		if err := tx.Create(&source).Error; err != nil {
			return err
		}
		if streamProxy != nil {
			if err := tx.Create(streamProxy).Error; err != nil {
				return err
			}
		}
		if streamPush != nil {
			if err := tx.Create(streamPush).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "create media source failed")
		return
	}
	if recErr := s.applyRecordingPolicyForSourceID(source.ID); recErr != nil {
		logutil.Warnf("apply recording policy failed on create: source_id=%s err=%v", source.ID, recErr)
	}
	s.ok(c, source)
}

func (s *Server) updateDevice(c *gin.Context) {
	id := c.Param("id")
	var source model.MediaSource
	if err := s.db.Where("id = ?", id).First(&source).Error; err != nil {
		s.fail(c, http.StatusNotFound, "device not found")
		return
	}

	var in deviceUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if source.AIStatus == model.DeviceAIStatusRunning {
		s.fail(c, http.StatusBadRequest, "device is analyzing and cannot be edited")
		return
	}

	recordingEnabled := source.EnableRecording
	recordingMode := source.RecordingMode
	alarmClipEnabled := source.EnableAlarmClip
	alarmPreSeconds := source.AlarmPreSeconds
	alarmPostSeconds := source.AlarmPostSeconds

	in.Name = strings.TrimSpace(in.Name)
	in.AreaID = strings.TrimSpace(in.AreaID)
	in.OriginURL = strings.TrimSpace(in.OriginURL)
	in.StreamURL = strings.TrimSpace(in.StreamURL)
	in.App = strings.TrimSpace(in.App)
	in.StreamID = strings.TrimSpace(in.StreamID)
	in.PublishToken = strings.TrimSpace(in.PublishToken)

	if in.Name == "" {
		in.Name = source.Name
	}
	if in.AreaID == "" {
		in.AreaID = source.AreaID
	}
	if in.Name == "" || in.AreaID == "" {
		s.fail(c, http.StatusBadRequest, "name and area_id are required")
		return
	}

	var areaCount int64
	if err := s.db.Model(&model.Area{}).Where("id = ?", in.AreaID).Count(&areaCount).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query area failed")
		return
	}
	if areaCount == 0 {
		s.fail(c, http.StatusBadRequest, "area does not exist")
		return
	}
	oldApp, oldStream := source.App, source.StreamID
	if oldApp == "" || oldStream == "" {
		oldApp, oldStream = parseDeviceZLMAppStream(strings.ToLower(strings.TrimSpace(source.Protocol)), source, strings.TrimSpace(s.cfg.Server.ZLM.App))
	}
	var (
		outputConfigMap map[string]string
		zlmErr          error
		streamProxy     *model.StreamProxy
		streamPush      *model.StreamPush
	)
	requestHost := resolveRequestHost(c)

	if source.SourceType == model.SourceTypeGB28181 {
		source.Name = in.Name
		source.AreaID = in.AreaID
		source.EnableRecording = recordingEnabled
		source.RecordingMode = recordingMode
		source.EnableAlarmClip = alarmClipEnabled
		source.AlarmPreSeconds = alarmPreSeconds
		source.AlarmPostSeconds = alarmPostSeconds
		if in.SnapshotURL != "" {
			source.SnapshotURL = strings.TrimSpace(in.SnapshotURL)
		}

		childIDs := make([]string, 0, 8)
		updateErr := s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Save(&source).Error; err != nil {
				return err
			}
			// 编辑 GB 设备行时，统一同步子通道录制策略。
			if strings.EqualFold(strings.TrimSpace(source.RowKind), model.RowKindDevice) {
				var children []model.MediaSource
				if err := tx.Where("parent_id = ?", source.ID).Find(&children).Error; err != nil {
					return err
				}
				for _, child := range children {
					childID := strings.TrimSpace(child.ID)
					if childID == "" {
						continue
					}
					childIDs = append(childIDs, childID)
				}
				if len(childIDs) > 0 {
					updates := map[string]any{
						"area_id":            in.AreaID,
						"enable_recording":   recordingEnabled,
						"recording_mode":     recordingMode,
						"enable_alarm_clip":  alarmClipEnabled,
						"alarm_pre_seconds":  alarmPreSeconds,
						"alarm_post_seconds": alarmPostSeconds,
						"updated_at":         time.Now(),
					}
					if err := tx.Model(&model.MediaSource{}).
						Where("id IN ?", childIDs).
						Updates(updates).Error; err != nil {
						return err
					}
				}
			}
			return nil
		})
		if updateErr != nil {
			s.fail(c, http.StatusInternalServerError, "update gb source failed")
			return
		}
		if recErr := s.applyRecordingPolicyForSourceID(source.ID); recErr != nil {
			logutil.Warnf("apply recording policy failed on gb update: source_id=%s err=%v", source.ID, recErr)
		}
		for _, childID := range childIDs {
			if recErr := s.applyRecordingPolicyForSourceID(strings.TrimSpace(childID)); recErr != nil {
				logutil.Warnf("apply recording policy failed on gb child update: source_id=%s err=%v", childID, recErr)
			}
		}

		s.ok(c, source)
		return
	}

	if source.SourceType == model.SourceTypePull {
		originURL := strings.TrimSpace(firstNonEmpty(in.OriginURL, in.StreamURL, source.StreamURL))
		if err := validateRTSPStreamURL(originURL); err != nil {
			s.fail(c, http.StatusBadRequest, err.Error())
			return
		}
		transport := strings.ToLower(strings.TrimSpace(firstNonEmpty(in.Transport, source.Transport, "tcp")))
		if !validateTransport(transport) {
			s.fail(c, http.StatusBadRequest, "transport must be tcp or udp")
			return
		}
		retryCount := defaultStreamProxyRetryCount
		var existingProxy model.StreamProxy
		if err := s.db.Where("source_id = ?", source.ID).First(&existingProxy).Error; err == nil {
			retryCount = normalizeStreamProxyRetryCount(existingProxy.RetryCount)
		}
		outputConfigMap, zlmErr = s.ensureZLMProxy(source.ID, originURL, transport, retryCount, requestHost)
		if zlmErr != nil {
			s.fail(c, http.StatusBadGateway, "zlm pull stream failed: "+zlmErr.Error())
			return
		}
		source.Protocol = model.ProtocolRTSP
		source.Transport = transport
		source.StreamURL = originURL
		source.App = strings.TrimSpace(firstNonEmpty(outputConfigMap["zlm_app"], source.App))
		source.StreamID = strings.TrimSpace(firstNonEmpty(outputConfigMap["zlm_stream"], source.StreamID, buildZLMStreamID(source.ID)))
		streamProxy = &model.StreamProxy{
			SourceID:   source.ID,
			OriginURL:  originURL,
			Transport:  transport,
			Enable:     true,
			RetryCount: retryCount,
		}
	} else if source.SourceType == model.SourceTypePush {
		app := strings.TrimSpace(firstNonEmpty(in.App, source.App))
		streamID := strings.TrimSpace(firstNonEmpty(in.StreamID, source.StreamID))
		streamURL := strings.TrimSpace(firstNonEmpty(in.StreamURL, source.StreamURL))
		if streamURL != "" {
			if err := validateRTMPStreamURL(streamURL); err != nil {
				s.fail(c, http.StatusBadRequest, err.Error())
				return
			}
			parsedApp, parsedStream, perr := parseRTMPAppStream(streamURL)
			if perr == nil {
				if app == "" {
					app = parsedApp
				}
				if streamID == "" {
					streamID = parsedStream
				}
			}
		}
		if app == "" || streamID == "" {
			s.fail(c, http.StatusBadRequest, "app and stream_id are required for push source")
			return
		}
		outputConfigMap = s.buildZLMOutputConfig(app, streamID, requestHost)
		if streamURL == "" {
			streamURL = s.buildZLMRTMPPublishURL(app, streamID, requestHost)
		}
		outputConfigMap["publish_url"] = streamURL
		token := strings.TrimSpace(firstNonEmpty(in.PublishToken, findPushTokenBySource(s.db, source.ID)))
		if token != "" {
			outputConfigMap["publish_token"] = token
		}
		source.Protocol = model.ProtocolRTMP
		source.Transport = "tcp"
		source.StreamURL = streamURL
		source.App = app
		source.StreamID = streamID
		streamPush = &model.StreamPush{
			SourceID:     source.ID,
			PublishToken: token,
		}
	}

	source.Name = in.Name
	source.AreaID = in.AreaID
	source.EnableRecording = recordingEnabled
	source.RecordingMode = recordingMode
	source.EnableAlarmClip = alarmClipEnabled
	source.AlarmPreSeconds = alarmPreSeconds
	source.AlarmPostSeconds = alarmPostSeconds
	if in.SnapshotURL != "" {
		source.SnapshotURL = strings.TrimSpace(in.SnapshotURL)
	}
	applyOutputConfigToSource(&source, outputConfigMap)
	s.syncPullSourceOnlineStatus(&source, false)
	if err := ensureAppStreamUnique(s.db, source.ID, source.App, source.StreamID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			s.fail(c, http.StatusConflict, "app and stream_id already exists")
			return
		}
		s.fail(c, http.StatusInternalServerError, "check app/stream uniqueness failed")
		return
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if source.SourceType == model.SourceTypePush && strings.EqualFold(source.Protocol, model.ProtocolRTMP) {
			if err := clearStreamBlockTx(tx, source.App, source.StreamID, strings.TrimSpace(s.cfg.Server.ZLM.App)); err != nil {
				return err
			}
		}
		if err := tx.Save(&source).Error; err != nil {
			return err
		}
		if streamProxy != nil {
			if err := tx.Where("source_id = ?", source.ID).Delete(&model.StreamProxy{}).Error; err != nil {
				return err
			}
			if err := tx.Create(streamProxy).Error; err != nil {
				return err
			}
		}
		if streamPush != nil {
			if err := tx.Where("source_id = ?", source.ID).Delete(&model.StreamPush{}).Error; err != nil {
				return err
			}
			if err := tx.Create(streamPush).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "update media source failed")
		return
	}

	if source.SourceType == model.SourceTypePull && oldStream != "" {
		if !strings.EqualFold(oldApp, source.App) || oldStream != source.StreamID {
			_ = s.deleteZLMProxy(oldApp, oldStream)
		}
	}
	if recErr := s.applyRecordingPolicyForSourceID(source.ID); recErr != nil {
		logutil.Warnf("apply recording policy failed on update: source_id=%s err=%v", source.ID, recErr)
	}
	s.ok(c, source)
}

func (s *Server) previewDevice(c *gin.Context) {
	id := c.Param("id")
	var source model.MediaSource
	if err := s.db.Where("id = ?", id).First(&source).Error; err != nil {
		s.fail(c, http.StatusNotFound, "device not found")
		return
	}
	var (
		outputConfigMap map[string]string
		err             error
	)
	switch source.SourceType {
	case model.SourceTypePull:
		originURL := source.StreamURL
		retryCount := defaultStreamProxyRetryCount
		var proxy model.StreamProxy
		if err := s.db.Where("source_id = ?", source.ID).First(&proxy).Error; err == nil {
			if strings.TrimSpace(proxy.OriginURL) != "" {
				originURL = strings.TrimSpace(proxy.OriginURL)
			}
			retryCount = normalizeStreamProxyRetryCount(proxy.RetryCount)
		}
		outputConfigMap, err = s.ensureZLMProxy(source.ID, originURL, source.Transport, retryCount, resolveRequestHost(c))
		if err != nil {
			s.fail(c, http.StatusBadGateway, "zlm pull stream failed: "+err.Error())
			return
		}
		source.Protocol = model.ProtocolRTSP
		source.App = strings.TrimSpace(firstNonEmpty(outputConfigMap["zlm_app"], source.App))
		source.StreamID = strings.TrimSpace(firstNonEmpty(outputConfigMap["zlm_stream"], source.StreamID))
	case model.SourceTypePush:
		outputConfigMap = sourceOutputConfigMap(source)
		outputConfigMap["publish_url"] = source.StreamURL
		if token := findPushTokenBySource(s.db, source.ID); token != "" {
			outputConfigMap["publish_token"] = token
		}
	case model.SourceTypeGB28181:
		if source.RowKind != model.RowKindChannel {
			s.fail(c, http.StatusBadRequest, "gb28181 device row does not support preview")
			return
		}
		outputConfigMap, err = s.previewGB28181Device(c, &source, resolveRequestHost(c))
		if err != nil {
			s.fail(c, http.StatusBadGateway, "gb28181 preview failed: "+err.Error())
			return
		}
	default:
		s.fail(c, http.StatusBadRequest, "unsupported source_type for preview")
		return
	}
	applyOutputConfigToSource(&source, outputConfigMap)
	persistErr := s.persistPreviewResult(&source)
	if persistErr != nil {
		logutil.Warnf("preview persist failed: source_id=%s app=%s stream=%s err=%v", source.ID, source.App, source.StreamID, persistErr)
	}
	s.syncPullSourceOnlineStatus(&source, true)
	if recErr := s.applyRecordingPolicyForSourceID(source.ID); recErr != nil {
		logutil.Warnf("apply recording policy failed on preview: source_id=%s err=%v", source.ID, recErr)
	}

	result := gin.H{
		"device_id":     source.ID,
		"stream_url":    source.StreamURL,
		"play_url":      s.pickPreviewPlayURL(outputConfigMap),
		"output_config": outputConfigMap,
	}
	if persistErr != nil {
		result["warning"] = "preview started but config persistence failed"
	}
	s.ok(c, result)
}

func (s *Server) captureDeviceSnapshot(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		s.fail(c, http.StatusBadRequest, "device id is required")
		return
	}

	source, err := s.loadSnapshotSource(id)
	if err != nil {
		switch err.Error() {
		case "device not found":
			s.fail(c, http.StatusNotFound, err.Error())
		default:
			s.fail(c, http.StatusBadRequest, err.Error())
		}
		return
	}
	snapshotBody, err := s.captureSnapshotBody(&source, resolveRequestHost(c))
	if err != nil {
		statusCode := http.StatusBadGateway
		if strings.Contains(strings.ToLower(err.Error()), "device not found") {
			statusCode = http.StatusNotFound
		} else if strings.Contains(strings.ToLower(err.Error()), "only channel row supports snapshot") ||
			strings.Contains(strings.ToLower(err.Error()), "unsupported source_type") ||
			strings.Contains(strings.ToLower(err.Error()), "no rtsp url available") {
			statusCode = http.StatusBadRequest
		}
		s.fail(c, statusCode, err.Error())
		return
	}

	relPath, fullPath, err := s.deviceSnapshotFilePath(source.ID)
	if err != nil {
		s.fail(c, http.StatusInternalServerError, "resolve snapshot path failed")
		return
	}
	if err := s.removeDeviceSnapshotByURL(source.SnapshotURL); err != nil {
		log.Printf("remove old device snapshot failed: source_id=%s snapshot_url=%s err=%v", source.ID, strings.TrimSpace(source.SnapshotURL), err)
	}
	if err := os.WriteFile(fullPath, snapshotBody, 0o644); err != nil {
		s.fail(c, http.StatusInternalServerError, "save snapshot file failed")
		return
	}

	snapshotURL := "/api/v1/devices/snapshot/" + relPath
	if err := s.db.Model(&model.MediaSource{}).Where("id = ?", source.ID).Update("snapshot_url", snapshotURL).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "update snapshot url failed")
		return
	}

	s.ok(c, gin.H{
		"device_id":    source.ID,
		"snapshot_url": snapshotURL,
	})
}

func (s *Server) loadSnapshotSource(id string) (model.MediaSource, error) {
	var source model.MediaSource
	if err := s.db.Where("id = ?", strings.TrimSpace(id)).First(&source).Error; err != nil {
		return model.MediaSource{}, errors.New("device not found")
	}
	if !strings.EqualFold(strings.TrimSpace(source.RowKind), model.RowKindChannel) {
		return model.MediaSource{}, errors.New("only channel row supports snapshot")
	}
	return source, nil
}

func (s *Server) captureSnapshotBody(source *model.MediaSource, requestHost string) ([]byte, error) {
	if s == nil || source == nil {
		return nil, errors.New("invalid snapshot context")
	}
	requestHost = strings.TrimSpace(firstNonEmpty(
		requestHost,
		normalizeHost(strings.TrimSpace(s.cfg.Server.ZLM.PlayHost)),
		normalizeHost(strings.TrimSpace(s.cfg.Server.SIP.ListenIP)),
		"127.0.0.1",
	))

	outputConfigMap := sourceOutputConfigMap(*source)
	var err error
	switch source.SourceType {
	case model.SourceTypePull:
		originURL := source.StreamURL
		retryCount := defaultStreamProxyRetryCount
		var proxy model.StreamProxy
		if pErr := s.db.Where("source_id = ?", source.ID).First(&proxy).Error; pErr == nil {
			if strings.TrimSpace(proxy.OriginURL) != "" {
				originURL = strings.TrimSpace(proxy.OriginURL)
			}
			retryCount = normalizeStreamProxyRetryCount(proxy.RetryCount)
		}
		outputConfigMap, err = s.ensureZLMProxy(source.ID, originURL, source.Transport, retryCount, requestHost)
		if err != nil {
			return nil, errors.New("zlm pull stream failed: " + err.Error())
		}
		source.Protocol = model.ProtocolRTSP
		source.App = strings.TrimSpace(firstNonEmpty(outputConfigMap["zlm_app"], source.App))
		source.StreamID = strings.TrimSpace(firstNonEmpty(outputConfigMap["zlm_stream"], source.StreamID))
	case model.SourceTypeGB28181:
		outputConfigMap, err = s.previewGB28181Device(nil, source, requestHost)
		if err != nil {
			return nil, errors.New("gb28181 snapshot prepare failed: " + err.Error())
		}
	case model.SourceTypePush:
		// Push 流直接复用当前输出配置，避免巡查任务重复改动设备绑定。
	default:
		return nil, errors.New("unsupported source_type for snapshot")
	}

	applyOutputConfigToSource(source, outputConfigMap)
	if source.SourceType == model.SourceTypePull || source.SourceType == model.SourceTypeGB28181 {
		if persistErr := s.persistPreviewResult(source); persistErr != nil {
			log.Printf("snapshot persist stream output failed: source_id=%s err=%v", source.ID, persistErr)
		}
	}

	rtspURL := pickSnapshotRTSPURL(*source, outputConfigMap)
	if rtspURL == "" {
		return nil, errors.New("no rtsp url available for snapshot")
	}
	if strings.EqualFold(strings.TrimSpace(source.SourceType), model.SourceTypeGB28181) {
		active, activeErr := s.waitForSourceStreamActive(source, gbStreamWarmupTimeout, gbStreamWarmupPollInterval)
		if activeErr != nil {
			return nil, errors.New("gb28181 snapshot stream check failed: " + activeErr.Error())
		}
		if !active {
			return nil, errors.New("gb28181 snapshot stream is not active")
		}
	}

	snapshotBody, err := s.requestZLMSnapshotBody(rtspURL)
	if err != nil {
		return nil, errors.New("zlm get snapshot failed: " + err.Error())
	}
	return snapshotBody, nil
}

func (s *Server) getDeviceSnapshotFile(c *gin.Context) {
	rawPath := strings.TrimPrefix(c.Param("path"), "/")
	cleanPath := filepath.Clean(rawPath)
	if cleanPath == "" || cleanPath == "." {
		s.fail(c, http.StatusBadRequest, "invalid snapshot path")
		return
	}

	baseDir := filepath.Join("configs", deviceSnapshotDirName)
	fullPath := filepath.Join(baseDir, cleanPath)
	absBaseDir, _ := filepath.Abs(baseDir)
	absTarget, _ := filepath.Abs(fullPath)
	if absTarget != absBaseDir && !strings.HasPrefix(absTarget, absBaseDir+string(filepath.Separator)) {
		s.fail(c, http.StatusBadRequest, "invalid snapshot path")
		return
	}

	body, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			s.fail(c, http.StatusNotFound, "snapshot not found")
			return
		}
		s.fail(c, http.StatusInternalServerError, "read snapshot failed")
		return
	}

	mimeType := http.DetectContentType(body)
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		mimeType = "application/octet-stream"
	}
	c.Header("Content-Disposition", "inline")
	c.Data(http.StatusOK, mimeType, body)
}

func pickSnapshotRTSPURL(source model.MediaSource, output map[string]string) string {
	candidates := []string{
		strings.TrimSpace(output["rtsp"]),
		strings.TrimSpace(output["rtsp_url"]),
		strings.TrimSpace(source.PlayRTSPURL),
		strings.TrimSpace(source.StreamURL),
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		parsed, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(parsed.Scheme), model.ProtocolRTSP) {
			return candidate
		}
	}
	return ""
}

func (s *Server) requestZLMSnapshotBody(rtspURL string) ([]byte, error) {
	rtspURL = strings.TrimSpace(rtspURL)
	if rtspURL == "" {
		return nil, errors.New("rtsp url is empty")
	}
	if s == nil || s.cfg == nil {
		return nil, errors.New("server config is empty")
	}

	raw, inlineBody, err := s.callZLMGetSnap(rtspURL)
	if err != nil {
		return nil, err
	}
	if len(inlineBody) > 0 {
		return inlineBody, nil
	}

	snapshotRaw := extractZLMSnapshotRawPath(raw)
	if snapshotRaw == "" {
		return nil, errors.New("zlm getSnap returned empty data")
	}
	if isLikelyZLMDefaultSnapshot(snapshotRaw) {
		return nil, errors.New("zlm getSnap returned default fallback snapshot")
	}
	fetchURL := s.resolveZLMSnapshotFetchURL(snapshotRaw)
	if fetchURL == "" {
		return nil, errors.New("zlm snapshot url is invalid")
	}
	if isLikelyZLMDefaultSnapshot(fetchURL) {
		return nil, errors.New("zlm getSnap resolved to default fallback snapshot")
	}
	return s.downloadSnapshotBytes(fetchURL)
}

func (s *Server) callZLMGetSnap(rtspURL string) (json.RawMessage, []byte, error) {
	if s == nil || s.cfg == nil {
		return nil, nil, errors.New("server config is empty")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(s.cfg.Server.ZLM.APIURL), "/")
	if baseURL == "" {
		return nil, nil, errors.New("zlm api url is empty")
	}
	endpoint, err := url.Parse(baseURL + "/index/api/getSnap")
	if err != nil {
		return nil, nil, err
	}
	query := endpoint.Query()
	query.Set("secret", strings.TrimSpace(s.cfg.Server.ZLM.Secret))
	query.Set("url", strings.TrimSpace(rtspURL))
	query.Set("timeout_sec", "5")
	query.Set("expire_sec", "30")
	endpoint.RawQuery = query.Encode()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, errors.New("request zlm failed: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, errors.New("zlm api status=" + strconv.Itoa(resp.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, nil, errors.New("read zlm response failed: " + err.Error())
	}
	if len(body) == 0 {
		return nil, nil, errors.New("zlm response body is empty")
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	detected := strings.ToLower(strings.TrimSpace(http.DetectContentType(body)))
	if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(detected, "image/") {
		return nil, body, nil
	}

	var out zlmAPIResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, nil, errors.New("decode zlm response failed: " + err.Error())
	}
	if out.Code != 0 {
		msg := strings.TrimSpace(out.Msg)
		if msg == "" {
			msg = "unknown zlm error"
		}
		return nil, nil, errors.New("zlm error(code=" + strconv.Itoa(out.Code) + "): " + msg)
	}
	return out.Data, nil, nil
}

func extractZLMSnapshotRawPath(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || strings.EqualFold(trimmed, "null") {
		return ""
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}

	var asObject map[string]any
	if err := json.Unmarshal(raw, &asObject); err != nil {
		return ""
	}
	for _, key := range []string{"url", "snap_url", "path", "snapshot", "snap", "file", "local_path"} {
		value, ok := asObject[key]
		if !ok || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			text = strings.TrimSpace(text)
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func isLikelyZLMDefaultSnapshot(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/")))
	if normalized == "" {
		return false
	}
	if strings.HasSuffix(normalized, "/logo") || strings.Contains(normalized, "/logo.") {
		return true
	}
	if strings.Contains(normalized, "/www/logo.") {
		return true
	}
	if strings.Contains(normalized, "defaultsnap") && strings.Contains(normalized, "logo") {
		return true
	}
	return false
}

func (s *Server) resolveZLMSnapshotFetchURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || s == nil || s.cfg == nil {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return raw
	}

	pathValue := strings.ReplaceAll(raw, "\\", "/")
	if idx := strings.Index(pathValue, "/snap/"); idx >= 0 {
		pathValue = pathValue[idx:]
	} else if strings.HasPrefix(pathValue, "./snap/") {
		pathValue = "/" + strings.TrimPrefix(pathValue, "./")
	} else if strings.HasPrefix(pathValue, "snap/") {
		pathValue = "/" + pathValue
	} else if strings.HasPrefix(pathValue, "www/snap/") {
		pathValue = "/" + strings.TrimPrefix(pathValue, "www/")
	}
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return ""
	}

	baseURL, err := url.Parse(strings.TrimSpace(s.cfg.Server.ZLM.APIURL))
	if err != nil || baseURL == nil || strings.TrimSpace(baseURL.Scheme) == "" || strings.TrimSpace(baseURL.Host) == "" {
		return ""
	}
	ref, err := url.Parse(pathValue)
	if err != nil {
		return ""
	}
	return baseURL.ResolveReference(ref).String()
}

func (s *Server) downloadSnapshotBytes(snapshotURL string) ([]byte, error) {
	snapshotURL = strings.TrimSpace(snapshotURL)
	if snapshotURL == "" {
		return nil, errors.New("snapshot url is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, snapshotURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("snapshot http status=" + strconv.Itoa(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("snapshot body is empty")
	}
	if !strings.HasPrefix(strings.ToLower(http.DetectContentType(body)), "image/") {
		return nil, errors.New("snapshot response is not image")
	}
	return body, nil
}

func (s *Server) deviceSnapshotFilePath(deviceID string) (string, string, error) {
	fileName := sanitizePathSegment(deviceID) + ".jpg"
	snapshotDir := filepath.Join("configs", deviceSnapshotDirName)
	if err := s.ensureDir(snapshotDir); err != nil {
		return "", "", err
	}
	return filepath.ToSlash(fileName), filepath.Join(snapshotDir, fileName), nil
}

func (s *Server) removeDeviceSnapshotByURL(snapshotURL string) error {
	snapshotURL = strings.TrimSpace(snapshotURL)
	if snapshotURL == "" {
		return nil
	}
	const snapshotURLPrefix = "/api/v1/devices/snapshot/"
	if !strings.HasPrefix(snapshotURL, snapshotURLPrefix) {
		return nil
	}
	relPath := strings.TrimPrefix(snapshotURL, snapshotURLPrefix)
	relPath = strings.TrimSpace(strings.TrimPrefix(relPath, "/"))
	if relPath == "" {
		return nil
	}
	baseDir := filepath.Join("configs", deviceSnapshotDirName)
	targetPath := filepath.Clean(filepath.Join(baseDir, relPath))
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	if absTarget != absBase && !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) {
		return errors.New("invalid snapshot path")
	}
	if err := os.Remove(absTarget); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Server) deleteDevice(c *gin.Context) {
	id := c.Param("id")

	var source model.MediaSource
	if err := s.db.Where("id = ?", id).First(&source).Error; err != nil {
		s.fail(c, http.StatusNotFound, "device not found")
		return
	}

	var count int64
	if err := s.db.Model(&model.VideoTaskDeviceProfile{}).Where("device_id = ?", id).Count(&count).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query task relation failed")
		return
	}
	if count > 0 {
		s.fail(c, http.StatusBadRequest, "device is used by task, remove task relation first")
		return
	}

	if source.SourceType == model.SourceTypeGB28181 {
		s.fail(c, http.StatusBadRequest, "gb28181 source must be deleted from gb maintenance api")
		return
	}
	if source.SourceType == model.SourceTypePull {
		app, stream := parseDeviceZLMAppStream(model.ProtocolRTSP, source, strings.TrimSpace(s.cfg.Server.ZLM.App))
		if err := s.deleteZLMProxy(app, stream); err != nil {
			s.fail(c, http.StatusBadGateway, "zlm cleanup failed: "+err.Error())
			return
		}
	} else if source.SourceType == model.SourceTypePush {
		app, stream := parseDeviceZLMAppStream(model.ProtocolRTMP, source, strings.TrimSpace(s.cfg.Server.ZLM.App))
		if err := s.closeZLMStreams(app, stream); err != nil {
			s.fail(c, http.StatusBadGateway, "zlm close stream failed: "+err.Error())
			return
		}
	}
	if err := s.stopRecordingAndCancelBySource(source); err != nil {
		log.Printf("stop recording before delete failed: source_id=%s err=%v", source.ID, err)
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if source.SourceType == model.SourceTypePush && strings.EqualFold(source.Protocol, model.ProtocolRTMP) {
			app, stream := parseDeviceZLMAppStream(model.ProtocolRTMP, source, strings.TrimSpace(s.cfg.Server.ZLM.App))
			if err := upsertStreamBlockTx(tx, app, stream, "deleted by user", strings.TrimSpace(s.cfg.Server.ZLM.App)); err != nil {
				return err
			}
		}
		if err := tx.Where("source_id = ?", source.ID).Delete(&model.StreamProxy{}).Error; err != nil {
			return err
		}
		if err := tx.Where("source_id = ?", source.ID).Delete(&model.StreamPush{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.MediaSource{}, "id = ?", source.ID).Error
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "delete source failed")
		return
	}
	snapshotFile := filepath.Join("configs", deviceSnapshotDirName, sanitizePathSegment(source.ID)+".jpg")
	if err := os.Remove(snapshotFile); err != nil && !os.IsNotExist(err) {
		log.Printf("delete device snapshot file failed: source_id=%s path=%s err=%v", source.ID, snapshotFile, err)
	}
	s.ok(c, gin.H{"deleted": id})
}

func (s *Server) getRecordingStatus(c *gin.Context) {
	id := c.Param("id")
	var device model.Device
	if err := s.db.Where("id = ?", id).First(&device).Error; err != nil {
		s.fail(c, http.StatusNotFound, "device not found")
		return
	}
	s.ok(c, gin.H{
		"device_id":          device.ID,
		"enable_recording":   device.EnableRecording,
		"recording_mode":     device.RecordingMode,
		"recording_status":   device.RecordingStatus,
		"enable_alarm_clip":  device.EnableAlarmClip,
		"alarm_pre_seconds":  device.AlarmPreSeconds,
		"alarm_post_seconds": device.AlarmPostSeconds,
		"flash_safe_policy":  "only_alarm_recording",
	})
}

func (s *Server) listRecordings(c *gin.Context) {
	id := c.Param("id")
	kind := strings.ToLower(strings.TrimSpace(c.Query("kind")))
	if kind == "" {
		kind = recordingKindNormal
	}
	if kind != recordingKindNormal && kind != recordingKindAlarm {
		s.fail(c, http.StatusBadRequest, "kind must be normal or alarm")
		return
	}
	dir, err := s.safeRecordingDeviceDir(id)
	if err != nil {
		s.fail(c, http.StatusBadRequest, "invalid device recording path")
		return
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			s.ok(c, gin.H{
				"items":             []any{},
				"total":             0,
				"total_size":        0,
				"page":              1,
				"page_size":         20,
				"total_pages":       0,
				"kind":              kind,
				"recording_dir":     filepath.ToSlash(filepath.Join(id)),
				"flash_safe_policy": "only_alarm_recording",
			})
			return
		}
		s.fail(c, http.StatusInternalServerError, "read recordings failed")
		return
	}
	page := parsePositiveInt(c.Query("page"), 1)
	pageSize := parsePositiveInt(c.Query("page_size"), 20)
	if pageSize > 200 {
		pageSize = 200
	}
	keyword := strings.TrimSpace(strings.ToLower(c.Query("keyword")))
	order := strings.ToLower(strings.TrimSpace(c.Query("order")))
	asc := order == "asc"

	type recordingClipEvent struct {
		EventID    string
		OccurredAt time.Time
	}
	clipEventByPath := make(map[string]recordingClipEvent, 64)
	var clipEvents []model.AlarmEvent
	if err := s.db.Select("id", "occurred_at", "clip_files_json").
		Where("device_id = ? AND clip_files_json <> '' AND clip_files_json <> '[]'", id).
		Find(&clipEvents).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query event clips failed")
		return
	}
	for _, event := range clipEvents {
		for _, clipPath := range decodeEventClipFiles(event.ClipFilesJSON) {
			if clipPath == "" {
				continue
			}
			current, exists := clipEventByPath[clipPath]
			if !exists || current.OccurredAt.Before(event.OccurredAt) {
				clipEventByPath[clipPath] = recordingClipEvent{
					EventID:    strings.TrimSpace(event.ID),
					OccurredAt: event.OccurredAt,
				}
			}
		}
	}

	type recordingFile struct {
		Name            string     `json:"name"`
		Size            int64      `json:"size"`
		ModTime         time.Time  `json:"mod_time"`
		Path            string     `json:"path"`
		Kind            string     `json:"kind"`
		EventID         string     `json:"event_id"`
		EventOccurredAt *time.Time `json:"event_occurred_at"`
	}
	items := make([]recordingFile, 0, 64)
	var totalSize int64
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if kind == recordingKindNormal {
				relDir, err := filepath.Rel(dir, path)
				if err == nil {
					relDir = filepath.ToSlash(strings.TrimSpace(relDir))
					if relDir == alarmClipDirName || strings.HasPrefix(relDir, alarmClipDirName+"/") {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || rel == "." {
			return nil
		}
		if strings.EqualFold(filepath.Ext(rel), ".json") {
			return nil
		}

		clipEvent, hasEvent := clipEventByPath[rel]
		isAlarm := hasEvent || rel == alarmClipDirName || strings.HasPrefix(rel, alarmClipDirName+"/")
		if kind == recordingKindNormal && isAlarm {
			return nil
		}
		if kind == recordingKindAlarm && !isAlarm {
			return nil
		}

		nameLower := strings.ToLower(filepath.Base(rel))
		relLower := strings.ToLower(rel)
		if keyword != "" && !strings.Contains(nameLower, keyword) {
			if !strings.Contains(relLower, keyword) {
				return nil
			}
		}
		totalSize += info.Size()
		item := recordingFile{
			Name:    filepath.Base(rel),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Path:    rel,
			Kind:    recordingKindNormal,
		}
		if isAlarm {
			item.Kind = recordingKindAlarm
		}
		if hasEvent {
			eventID := strings.TrimSpace(clipEvent.EventID)
			item.EventID = eventID
			if eventID != "" && !clipEvent.OccurredAt.IsZero() {
				eventTime := clipEvent.OccurredAt
				item.EventOccurredAt = &eventTime
			}
		}
		items = append(items, item)
		return nil
	})
	if walkErr != nil {
		s.fail(c, http.StatusInternalServerError, "read recordings failed")
		return
	}
	if kind == recordingKindAlarm {
		sort.Slice(items, func(i, j int) bool {
			leftEventAt := time.Time{}
			rightEventAt := time.Time{}
			if items[i].EventOccurredAt != nil {
				leftEventAt = *items[i].EventOccurredAt
			}
			if items[j].EventOccurredAt != nil {
				rightEventAt = *items[j].EventOccurredAt
			}
			leftHasEvent := !leftEventAt.IsZero()
			rightHasEvent := !rightEventAt.IsZero()
			if leftHasEvent != rightHasEvent {
				return leftHasEvent
			}
			if !leftEventAt.Equal(rightEventAt) {
				return leftEventAt.After(rightEventAt)
			}
			if !items[i].ModTime.Equal(items[j].ModTime) {
				return items[i].ModTime.After(items[j].ModTime)
			}
			return items[i].Path < items[j].Path
		})
	} else {
		sort.Slice(items, func(i, j int) bool {
			if items[i].ModTime.Equal(items[j].ModTime) {
				if asc {
					return items[i].Name < items[j].Name
				}
				return items[i].Name > items[j].Name
			}
			if asc {
				return items[i].ModTime.Before(items[j].ModTime)
			}
			return items[i].ModTime.After(items[j].ModTime)
		})
	}

	total := len(items)
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	if page > totalPages && totalPages > 0 {
		page = totalPages
	}
	start := (page - 1) * pageSize
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pagedItems := items[start:end]

	diskUsage := gin.H{}
	if usage, err := disk.Usage(dir); err == nil {
		diskUsage = gin.H{
			"total":        usage.Total,
			"used":         usage.Used,
			"free":         usage.Free,
			"used_percent": usage.UsedPercent,
		}
	}
	s.ok(c, gin.H{
		"items":             pagedItems,
		"total":             total,
		"total_size":        totalSize,
		"page":              page,
		"page_size":         pageSize,
		"total_pages":       totalPages,
		"kind":              kind,
		"recording_dir":     filepath.ToSlash(filepath.Join(id)),
		"disk_usage":        diskUsage,
		"flash_safe_policy": "only_alarm_recording",
	})
}

func (s *Server) downloadRecording(c *gin.Context) {
	id := c.Param("id")
	fullPath, err := s.safeRecordingFilePath(id, c.Param("path"))
	if err != nil {
		s.fail(c, http.StatusBadRequest, "invalid recording path")
		return
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			s.fail(c, http.StatusNotFound, "recording file not found")
			return
		}
		s.fail(c, http.StatusInternalServerError, "read recording file failed")
		return
	}
	if info.IsDir() {
		s.fail(c, http.StatusBadRequest, "recording path is a directory")
		return
	}
	c.FileAttachment(fullPath, filepath.Base(fullPath))
}

func (s *Server) exportRecordings(c *gin.Context) {
	id := c.Param("id")
	var in recordingExportRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	paths := uniqueStrings(in.Paths)
	if len(paths) == 0 {
		s.fail(c, http.StatusBadRequest, "paths is required")
		return
	}
	if len(paths) > recordingExportMaxCnt {
		s.fail(c, http.StatusBadRequest, "too many files to export")
		return
	}
	type exportItem struct {
		RelPath  string
		FullPath string
		Size     int64
	}
	items := make([]exportItem, 0, len(paths))
	seenRel := make(map[string]struct{}, len(paths))
	var totalSize int64
	for _, rawPath := range paths {
		fullPath, err := s.safeRecordingFilePath(id, rawPath)
		if err != nil {
			s.fail(c, http.StatusBadRequest, "invalid recording path")
			return
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				s.fail(c, http.StatusNotFound, "recording file not found")
				return
			}
			s.fail(c, http.StatusInternalServerError, "read recording file failed")
			return
		}
		if info.IsDir() {
			s.fail(c, http.StatusBadRequest, "recording path is a directory")
			return
		}
		relPath := filepath.ToSlash(filepath.Clean(strings.TrimSpace(rawPath)))
		relPath = strings.TrimPrefix(relPath, "./")
		if relPath == "" || relPath == "." {
			s.fail(c, http.StatusBadRequest, "invalid recording path")
			return
		}
		if _, ok := seenRel[relPath]; ok {
			continue
		}
		seenRel[relPath] = struct{}{}
		totalSize += info.Size()
		if totalSize > recordingExportMaxSum {
			s.fail(c, http.StatusBadRequest, "total export size exceeds limit")
			return
		}
		items = append(items, exportItem{
			RelPath:  relPath,
			FullPath: fullPath,
			Size:     info.Size(),
		})
	}
	if len(items) == 0 {
		s.fail(c, http.StatusBadRequest, "paths is required")
		return
	}

	fileName := fmt.Sprintf("recordings_%s_%s.zip", sanitizePathSegment(id), time.Now().Format("20060102_150405"))
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileName))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)

	zipWriter := zip.NewWriter(c.Writer)
	for _, item := range items {
		entryWriter, err := zipWriter.Create(item.RelPath)
		if err != nil {
			log.Printf("create zip entry failed: device_id=%s rel=%s err=%v", id, item.RelPath, err)
			break
		}
		file, err := os.Open(item.FullPath)
		if err != nil {
			log.Printf("open export source failed: device_id=%s rel=%s err=%v", id, item.RelPath, err)
			break
		}
		_, copyErr := io.Copy(entryWriter, file)
		_ = file.Close()
		if copyErr != nil {
			log.Printf("write zip entry failed: device_id=%s rel=%s err=%v", id, item.RelPath, copyErr)
			break
		}
	}
	if err := zipWriter.Close(); err != nil {
		log.Printf("close recording export zip failed: device_id=%s err=%v", id, err)
	}
}

func (s *Server) deleteRecordings(c *gin.Context) {
	id := c.Param("id")
	var in recordingDeleteRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	paths := uniqueStrings(in.Paths)
	if len(paths) == 0 {
		s.fail(c, http.StatusBadRequest, "paths is required")
		return
	}
	removed := make([]string, 0, len(paths))
	type failedItem struct {
		Path   string `json:"path"`
		Reason string `json:"reason"`
	}
	failed := make([]failedItem, 0)
	for _, p := range paths {
		fullPath, err := s.safeRecordingFilePath(id, p)
		if err != nil {
			failed = append(failed, failedItem{Path: p, Reason: "invalid path"})
			continue
		}
		if err := os.Remove(fullPath); err != nil {
			if os.IsNotExist(err) {
				failed = append(failed, failedItem{Path: p, Reason: "not found"})
			} else {
				failed = append(failed, failedItem{Path: p, Reason: "remove failed"})
			}
			continue
		}
		removed = append(removed, p)
	}
	if len(removed) > 0 {
		if err := s.pruneEventClipFilesByRemovedPaths(removed); err != nil {
			log.Printf("sync event clip paths after recording delete failed: device_id=%s err=%v", id, err)
			s.fail(c, http.StatusInternalServerError, "sync event clips failed")
			return
		}
	}
	if dir, err := s.safeRecordingDeviceDir(id); err == nil {
		_ = cleanupEmptyDirs(dir)
	}
	s.ok(c, gin.H{
		"removed": removed,
		"failed":  failed,
		"summary": gin.H{
			"total":   len(paths),
			"removed": len(removed),
			"failed":  len(failed),
		},
	})
}

func (s *Server) gb28181Info(c *gin.Context) {
	host := resolveRequestHost(c)
	sipHost := strings.TrimSpace(s.cfg.Server.SIP.ListenIP)
	if sipHost == "" || sipHost == "0.0.0.0" || sipHost == "::" {
		sipHost = host
	}
	serverID := strings.TrimSpace(s.cfg.Server.SIP.ServerID)
	if serverID == "" {
		serverID = "34020000002000000001"
	}
	domain := strings.TrimSpace(s.cfg.Server.SIP.Domain)
	if domain == "" {
		domain = "3402000000"
	}
	sipPort := s.cfg.Server.SIP.Port
	if sipPort <= 0 {
		sipPort = 15060
	}
	mediaIP := strings.TrimSpace(s.cfg.Server.SIP.MediaIP)
	if mediaIP == "" {
		mediaIP = host
	}
	s.ok(c, gin.H{
		"note":                  strings.TrimSpace(s.cfg.Server.SIP.GuideNote),
		"sip_server_id":         serverID,
		"sip_domain":            domain,
		"sip_ip":                sipHost,
		"sip_port":              sipPort,
		"sip_password":          strings.TrimSpace(s.cfg.Server.SIP.Password),
		"transport_options":     []string{"tcp", "udp"},
		"recommended_transport": strings.TrimSpace(s.cfg.Server.SIP.RecommendedTransport),
		"register_expires":      s.cfg.Server.SIP.RegisterExpires,
		"keepalive_interval":    s.cfg.Server.SIP.KeepaliveInterval,
		"keepalive_timeout_sec": s.cfg.Server.SIP.KeepaliveTimeout,
		"media": gin.H{
			"ip":         mediaIP,
			"rtp_port":   s.cfg.Server.SIP.MediaRTPPort,
			"port_range": strings.TrimSpace(s.cfg.Server.SIP.MediaPortRange),
		},
		"sample_device_id": strings.TrimSpace(s.cfg.Server.SIP.SampleDeviceID),
		"tips":             append([]string{}, s.cfg.Server.SIP.Tips...),
	})
}

func (s *Server) verifyGB28181Config(c *gin.Context) {
	var in gb28181VerifyRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}

	in.SIPServerID = strings.TrimSpace(in.SIPServerID)
	in.SIPDomain = strings.TrimSpace(in.SIPDomain)
	in.SIPIP = strings.TrimSpace(in.SIPIP)
	in.Transport = strings.ToLower(strings.TrimSpace(in.Transport))
	in.DeviceID = strings.TrimSpace(in.DeviceID)
	in.Password = strings.TrimSpace(in.Password)
	in.MediaIP = strings.TrimSpace(in.MediaIP)

	defaultSIPPort := s.cfg.Server.SIP.Port
	if defaultSIPPort <= 0 {
		defaultSIPPort = 15060
	}
	if in.SIPPort <= 0 {
		in.SIPPort = defaultSIPPort
	}
	if in.MediaPort <= 0 {
		in.MediaPort = s.cfg.Server.SIP.MediaRTPPort
	}
	if in.RegisterExpires <= 0 {
		in.RegisterExpires = s.cfg.Server.SIP.RegisterExpires
	}
	if in.KeepaliveInterval <= 0 {
		in.KeepaliveInterval = s.cfg.Server.SIP.KeepaliveInterval
	}

	type checkItem struct {
		Key     string `json:"key"`
		Result  string `json:"result"`
		Message string `json:"message"`
	}
	checks := make([]checkItem, 0, 8)
	appendCheck := func(key string, pass bool, message string) {
		result := "pass"
		if !pass {
			result = "fail"
		}
		checks = append(checks, checkItem{Key: key, Result: result, Message: message})
	}
	appendWarn := func(key string, message string) {
		checks = append(checks, checkItem{Key: key, Result: "warn", Message: message})
	}

	appendCheck("sip_server_id", gbIDPattern.MatchString(in.SIPServerID), "必须是20位数字编码")
	appendCheck("sip_domain", gbDomainReg.MatchString(in.SIPDomain), "必须是10位或20位数字编码")
	appendCheck("device_id", gbIDPattern.MatchString(in.DeviceID), "必须是20位数字编码")
	appendCheck("transport", in.Transport == "tcp" || in.Transport == "udp", "必须是 tcp 或 udp")
	if in.Password != "" {
		appendCheck("password", true, "已配置密码")
	} else {
		appendWarn("password", "密码为空时，除非服务端配置了默认密码，否则不会启用 Digest 鉴权")
	}
	appendCheck("sip_port", in.SIPPort >= 1 && in.SIPPort <= 65535, "必须在范围 [1,65535]")
	appendCheck("media_port", in.MediaPort >= 1 && in.MediaPort <= 65535, "必须在范围 [1,65535]")
	appendCheck(
		"register_expires",
		in.RegisterExpires >= 60 && in.RegisterExpires <= 86400,
		"必须在范围 [60,86400] 秒",
	)
	appendCheck(
		"keepalive_interval",
		in.KeepaliveInterval >= 5 && in.KeepaliveInterval <= 600,
		"必须在范围 [5,600] 秒",
	)

	if isValidHostOrIP(in.SIPIP) {
		appendCheck("sip_ip", true, "主机/IP 格式有效")
	} else {
		appendCheck("sip_ip", false, "主机/IP 格式无效")
	}
	if in.MediaIP == "" {
		appendWarn("media_ip", "media_ip 为空时将回退到 sip_ip")
	} else if isValidHostOrIP(in.MediaIP) {
		appendCheck("media_ip", true, "主机/IP 格式有效")
	} else {
		appendCheck("media_ip", false, "主机/IP 格式无效")
	}

	valid := true
	for _, item := range checks {
		if item.Result == "fail" {
			valid = false
			break
		}
	}

	bindCheck := checkItem{Key: "port_binding", Result: "warn", Message: "传输协议或端口不合法，已跳过端口检测"}
	if (in.Transport == "tcp" || in.Transport == "udp") && in.SIPPort >= 1 && in.SIPPort <= 65535 {
		if isLocalHost(in.SIPIP) {
			ok, msg := tryBindLocalPort(in.Transport, in.SIPPort)
			if ok {
				bindCheck = checkItem{Key: "port_binding", Result: "pass", Message: msg}
			} else {
				bindCheck = checkItem{Key: "port_binding", Result: "fail", Message: msg}
				valid = false
			}
		} else if in.Transport == "tcp" {
			ok, msg := checkTCPReachable(in.SIPIP, in.SIPPort, 1200*time.Millisecond)
			if ok {
				bindCheck = checkItem{Key: "remote_tcp_reachability", Result: "pass", Message: msg}
			} else {
				bindCheck = checkItem{Key: "remote_tcp_reachability", Result: "warn", Message: msg}
			}
		} else {
			bindCheck = checkItem{
				Key:     "remote_udp_reachability",
				Result:  "warn",
				Message: "远端 UDP 可达性不做主动探测",
			}
		}
	}
	checks = append(checks, bindCheck)

	s.ok(c, gin.H{
		"valid":  valid,
		"checks": checks,
		"normalized": gin.H{
			"sip_server_id":       in.SIPServerID,
			"sip_domain":          in.SIPDomain,
			"sip_ip":              in.SIPIP,
			"sip_port":            in.SIPPort,
			"transport":           in.Transport,
			"device_id":           in.DeviceID,
			"media_ip":            strings.TrimSpace(firstNonEmpty(in.MediaIP, in.SIPIP)),
			"media_port":          in.MediaPort,
			"register_expires":    in.RegisterExpires,
			"keepalive_interval":  in.KeepaliveInterval,
			"password_configured": in.Password != "",
		},
	})
}
func (s *Server) discoverLANDevice(c *gin.Context) {
	cidr := strings.TrimSpace(c.Query("cidr"))
	if cidr == "" {
		cidr = defaultLANCIDR()
	}
	portsRaw := strings.TrimSpace(c.Query("ports"))
	if portsRaw == "" {
		portsRaw = "554,8554,1935,80,8080"
	}
	ports := parsePortList(portsRaw)
	if len(ports) == 0 {
		s.fail(c, http.StatusBadRequest, "ports must include valid tcp ports")
		return
	}

	timeoutMS := parsePositiveInt(c.Query("timeout_ms"), 250)
	if timeoutMS < 50 {
		timeoutMS = 50
	}
	if timeoutMS > 3000 {
		timeoutMS = 3000
	}

	concurrency := parsePositiveInt(c.Query("concurrency"), 32)
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 128 {
		concurrency = 128
	}

	maxHosts := parsePositiveInt(c.Query("max_hosts"), 256)
	if maxHosts < 1 {
		maxHosts = 1
	}
	if maxHosts > 512 {
		maxHosts = 512
	}

	limit := parsePositiveInt(c.Query("limit"), 128)
	if limit < 1 {
		limit = 1
	}
	if limit > 512 {
		limit = 512
	}

	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		s.fail(c, http.StatusBadRequest, "invalid cidr")
		return
	}
	if ipNet.IP.To4() == nil {
		s.fail(c, http.StatusBadRequest, "only ipv4 cidr is supported")
		return
	}

	hosts, hostTruncated, err := expandIPv4Hosts(ipNet, maxHosts)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(hosts) == 0 {
		s.ok(c, gin.H{
			"items":            []any{},
			"cidr":             cidr,
			"ports":            ports,
			"timeout_ms":       timeoutMS,
			"concurrency":      concurrency,
			"scanned_hosts":    0,
			"scanned_targets":  0,
			"host_truncated":   hostTruncated,
			"result_truncated": false,
		})
		return
	}

	type scanTarget struct {
		ip   string
		port int
	}
	type lanDiscoveryItem struct {
		IP            string `json:"ip"`
		Port          int    `json:"port"`
		Address       string `json:"address"`
		Reachable     bool   `json:"reachable"`
		ProtocolGuess string `json:"protocol_guess"`
		StreamURL     string `json:"stream_url"`
		LatencyMS     int64  `json:"latency_ms"`
		Note          string `json:"note,omitempty"`
		ipNum         uint32 `json:"-"`
	}

	targets := make(chan scanTarget, concurrency*2)
	results := make(chan lanDiscoveryItem, concurrency*2)
	ctx := c.Request.Context()
	dialer := net.Dialer{Timeout: time.Duration(timeoutMS) * time.Millisecond}

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range targets {
				start := time.Now()
				conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.ip, strconv.Itoa(target.port)))
				if err != nil {
					continue
				}
				_ = conn.Close()

				protocol, streamURL, note := guessProtocolAndStreamURL(target.ip, target.port)
				latency := time.Since(start).Milliseconds()
				if latency < 1 {
					latency = 1
				}

				item := lanDiscoveryItem{
					IP:            target.ip,
					Port:          target.port,
					Address:       net.JoinHostPort(target.ip, strconv.Itoa(target.port)),
					Reachable:     true,
					ProtocolGuess: protocol,
					StreamURL:     streamURL,
					LatencyMS:     latency,
					Note:          note,
					ipNum:         ipv4ToUint32(net.ParseIP(target.ip)),
				}
				select {
				case results <- item:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(targets)
		for _, host := range hosts {
			for _, port := range ports {
				select {
				case targets <- scanTarget{ip: host, port: port}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	items := make([]lanDiscoveryItem, 0)
	for item := range results {
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].ipNum == items[j].ipNum {
			return items[i].Port < items[j].Port
		}
		return items[i].ipNum < items[j].ipNum
	})

	totalFound := len(items)
	resultTruncated := false
	if len(items) > limit {
		resultTruncated = true
		items = items[:limit]
	}

	s.ok(c, gin.H{
		"items":            items,
		"cidr":             cidr,
		"ports":            ports,
		"timeout_ms":       timeoutMS,
		"concurrency":      concurrency,
		"max_hosts":        maxHosts,
		"limit":            limit,
		"scanned_hosts":    len(hosts),
		"scanned_targets":  len(hosts) * len(ports),
		"reachable_total":  totalFound,
		"host_truncated":   hostTruncated,
		"result_truncated": resultTruncated,
	})
}

func validateProtocol(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case model.ProtocolRTSP, model.ProtocolRTMP, model.ProtocolGB28181, model.ProtocolONVIF:
		return true
	default:
		return false
	}
}

func validateTransport(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "tcp", "udp":
		return true
	default:
		return false
	}
}

func validateRTSPStreamURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("stream_url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("invalid rtsp stream url")
	}
	if !strings.EqualFold(parsed.Scheme, "rtsp") {
		return errors.New("stream_url must start with rtsp://")
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return errors.New("rtsp host is required")
	}
	return nil
}

func validateRTMPStreamURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("stream_url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("invalid rtmp stream url")
	}
	if !strings.EqualFold(parsed.Scheme, "rtmp") {
		return errors.New("stream_url must start with rtmp://")
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return errors.New("rtmp host is required")
	}
	pathParts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(pathParts) < 2 || strings.TrimSpace(pathParts[0]) == "" || strings.TrimSpace(pathParts[1]) == "" {
		return errors.New("rtmp stream url must include app and stream path")
	}
	return nil
}

func parseOutputConfigMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	var source map[string]any
	if err := json.Unmarshal([]byte(raw), &source); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" || value == nil {
			continue
		}
		switch v := value.(type) {
		case string:
			out[trimmedKey] = strings.TrimSpace(v)
		case float64:
			out[trimmedKey] = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			out[trimmedKey] = strconv.FormatBool(v)
		default:
			payload, err := json.Marshal(v)
			if err != nil {
				continue
			}
			out[trimmedKey] = strings.TrimSpace(string(payload))
		}
	}
	return out, nil
}

func (s *Server) pickPreviewPlayURL(output map[string]string) string {
	order := []string{"webrtc"}
	fallback := "ws_flv"
	if s != nil && s.cfg != nil {
		switch strings.ToLower(strings.TrimSpace(s.cfg.Server.ZLM.Output.WebFallback)) {
		case "ws_flv":
			fallback = "ws_flv"
		case "http_flv":
			fallback = "http_flv"
		case "hls":
			fallback = "hls"
		default:
			fallback = "ws_flv"
		}
	}
	if fallback != "webrtc" {
		order = append(order, fallback)
	}
	for _, key := range []string{"ws_flv", "http_flv", "hls"} {
		if key == fallback {
			continue
		}
		order = append(order, key)
	}
	for _, key := range order {
		if value := strings.TrimSpace(output[key]); value != "" {
			return value
		}
	}
	return ""
}

func (s *Server) applySourceOutputPolicyView(source *model.MediaSource) {
	if source == nil || s == nil || s.cfg == nil {
		return
	}
	output := sourceOutputConfigMap(*source)
	output = applyZLMOutputPolicy(output, s.cfg.Server.ZLM.Output)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(source.StreamURL)), "rtmp://") {
		output["publish_url"] = strings.TrimSpace(source.StreamURL)
	}
	applyOutputConfigToSource(source, output)
}

func (s *Server) previewGB28181Device(c *gin.Context, device *model.Device, requestHost string) (map[string]string, error) {
	if s == nil || device == nil {
		return nil, errors.New("invalid gb28181 preview context")
	}
	if s.gbService == nil {
		return nil, errors.New("gb28181 service is disabled")
	}

	outputConfigMap, err := parseOutputConfigMap(device.OutputConfig)
	if err != nil {
		outputConfigMap = map[string]string{}
	}

	gbDeviceID, channelID, err := s.resolveGBPreviewIDs(device, outputConfigMap)
	if err != nil {
		return nil, err
	}
	if !gbIDPattern.MatchString(gbDeviceID) {
		return nil, errors.New("invalid gb28181 device id")
	}

	var gbItem model.GBDevice
	if err := s.db.Where("device_id = ?", gbDeviceID).First(&gbItem).Error; err != nil {
		return nil, errors.New("gb28181 device not found")
	}
	if !gbItem.Enabled {
		return nil, errors.New("gb28181 device is disabled")
	}
	if !strings.EqualFold(strings.TrimSpace(gbItem.Status), "online") {
		return nil, errors.New("gb28181 device is offline")
	}

	if channelID == "" {
		channelID = gbDeviceID
	}

	streamID := strings.TrimSpace(firstNonEmpty(outputConfigMap["zlm_stream"], device.StreamID))
	if streamID == "" {
		streamID = buildGBPreviewStreamID(gbDeviceID, channelID)
	}

	app := strings.TrimSpace(firstNonEmpty(outputConfigMap["zlm_app"], device.App, "rtp"))
	output := s.buildZLMOutputConfig(app, streamID, requestHost)
	output["gb_device_id"] = gbDeviceID
	output["gb_channel_id"] = channelID
	if mediaIP := strings.TrimSpace(outputConfigMap["gb_media_ip"]); mediaIP != "" {
		output["gb_media_ip"] = mediaIP
	}
	if mediaPort := strings.TrimSpace(outputConfigMap["gb_media_port"]); mediaPort != "" {
		output["gb_media_port"] = mediaPort
	}
	output["zlm_app"] = app
	output["zlm_stream"] = streamID
	return output, nil
}

func (s *Server) resolveGBPreviewIDs(device *model.Device, output map[string]string) (string, string, error) {
	if device == nil {
		return "", "", errors.New("invalid gb source")
	}
	gbDeviceID := strings.TrimSpace(output["gb_device_id"])
	channelID := strings.TrimSpace(output["gb_channel_id"])

	streamDeviceID, streamChannelID := parseGBStreamURL(device.StreamURL)
	if gbDeviceID == "" {
		gbDeviceID = streamDeviceID
	}
	if channelID == "" {
		channelID = streamChannelID
	}

	if gbDeviceID == "" && strings.TrimSpace(device.ParentID) != "" {
		var parent model.MediaSource
		if err := s.db.Where("id = ?", strings.TrimSpace(device.ParentID)).First(&parent).Error; err == nil {
			pDeviceID, _ := parseGBStreamURL(parent.StreamURL)
			gbDeviceID = strings.TrimSpace(firstNonEmpty(output["gb_device_id"], pDeviceID, parent.StreamID))
		}
	}

	if gbDeviceID == "" {
		var channel model.GBChannel
		if err := s.db.Where("source_id_channel = ?", strings.TrimSpace(device.ID)).First(&channel).Error; err == nil {
			gbDeviceID = strings.TrimSpace(channel.DeviceID)
			if channelID == "" {
				channelID = strings.TrimSpace(channel.ChannelID)
			}
		}
	}

	if gbDeviceID == "" && gbIDPattern.MatchString(strings.TrimSpace(device.StreamID)) {
		gbDeviceID = strings.TrimSpace(device.StreamID)
	}
	if gbDeviceID == "" && gbIDPattern.MatchString(strings.TrimSpace(device.ID)) {
		gbDeviceID = strings.TrimSpace(device.ID)
	}
	if gbDeviceID == "" {
		return "", "", errors.New("cannot resolve gb28181 device id")
	}
	if channelID == "" {
		channelID = gbDeviceID
	}
	return gbDeviceID, channelID, nil
}

func buildGBPreviewStreamID(deviceID, channelID string) string {
	base := strings.TrimSpace(channelID)
	if base == "" {
		base = strings.TrimSpace(deviceID)
	}
	if base == "" {
		base = "preview"
	}
	base = zlmStreamIDSanitizer.ReplaceAllString(base, "_")
	base = strings.Trim(base, "_")
	if base == "" {
		base = "preview"
	}
	return "gb_" + base
}

func (s *Server) resolveGBMediaIP(c *gin.Context) string {
	candidates := []string{
		normalizeHost(strings.TrimSpace(s.cfg.Server.ZLM.PlayHost)),
		normalizeHost(strings.TrimSpace(s.cfg.Server.SIP.ListenIP)),
		normalizeHost(resolveRequestHost(c)),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || candidate == "0.0.0.0" || candidate == "::" {
			continue
		}
		return candidate
	}
	return "127.0.0.1"
}

func (s *Server) persistPreviewResult(source *model.MediaSource) error {
	if s == nil || s.db == nil || source == nil {
		return errors.New("invalid preview persist context")
	}
	updates := map[string]any{
		"output_config":     source.OutputConfig,
		"app":               source.App,
		"stream_id":         source.StreamID,
		"play_webrtc_url":   source.PlayWebRTCURL,
		"play_ws_flv_url":   source.PlayWSFLVURL,
		"play_http_flv_url": source.PlayHTTPFLVURL,
		"play_hls_url":      source.PlayHLSURL,
		"play_rtsp_url":     source.PlayRTSPURL,
		"play_rtmp_url":     source.PlayRTMPURL,
	}
	var err error
	for i := 0; i < 3; i++ {
		err = s.db.Model(&model.MediaSource{}).Where("id = ?", source.ID).Updates(updates).Error
		if err == nil {
			return nil
		}
		if !isSQLiteBusyError(err) {
			return err
		}
		time.Sleep(time.Duration(40*(i+1)) * time.Millisecond)
	}
	return err
}

func (s *Server) syncPullSourceOnlineStatus(source *model.MediaSource, persist bool) {
	if s == nil || s.cfg == nil || source == nil {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(source.SourceType), model.SourceTypePull) {
		return
	}
	app, stream := parseDeviceZLMAppStream(model.ProtocolRTSP, *source, strings.TrimSpace(s.cfg.Server.ZLM.App))
	active, err := s.isZLMStreamActive(app, stream, model.ProtocolRTSP, model.ProtocolRTMP)
	if err != nil {
		log.Printf("sync pull source status skipped: source_id=%s app=%s stream=%s err=%v", source.ID, app, stream, err)
		return
	}
	if !active {
		return
	}
	source.Status = "online"
	if !persist || s.db == nil {
		return
	}
	if err := s.db.Model(&model.MediaSource{}).Where("id = ?", source.ID).Updates(map[string]any{
		"status":     "online",
		"updated_at": time.Now(),
	}).Error; err != nil {
		log.Printf("persist pull source online status failed: source_id=%s app=%s stream=%s err=%v", source.ID, app, stream, err)
	}
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

func (s *Server) validateRecordingMode(enabled bool, mode string) error {
	if s != nil && s.cfg != nil && s.cfg.Server.Recording.Disabled {
		return nil
	}
	rawMode := strings.TrimSpace(mode)
	mode = normalizeRecordingModeValue(rawMode)
	if rawMode != "" && mode == "" {
		return errors.New("recording_mode must be none/continuous")
	}
	if !enabled {
		return nil
	}
	if mode == "" {
		mode = model.RecordingModeNone
	}
	if mode == model.RecordingModeNone {
		return nil
	}
	if mode == model.RecordingModeContinuous {
		if s == nil || s.cfg == nil || !s.cfg.Server.Recording.AllowContinuous {
			return errors.New("continuous recording is disabled by policy")
		}
		return nil
	}
	if mode != model.RecordingModeContinuous {
		return errors.New("recording_mode must be none/continuous")
	}
	return nil
}

func (s *Server) safeRecordingDeviceDir(deviceID string) (string, error) {
	root := filepath.Clean(strings.TrimSpace(s.cfg.Server.Recording.StorageDir))
	if root == "" {
		return "", errors.New("recording storage dir is empty")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	deviceDirAbs, err := filepath.Abs(filepath.Join(rootAbs, strings.TrimSpace(deviceID)))
	if err != nil {
		return "", err
	}
	if !isSubPath(rootAbs, deviceDirAbs) {
		return "", errors.New("invalid recording path")
	}
	return deviceDirAbs, nil
}

func (s *Server) safeRecordingFilePath(deviceID, rawPath string) (string, error) {
	deviceDir, err := s.safeRecordingDeviceDir(deviceID)
	if err != nil {
		return "", err
	}
	cleanPath := filepath.Clean(strings.TrimSpace(strings.TrimPrefix(rawPath, "/")))
	if cleanPath == "" || cleanPath == "." {
		return "", errors.New("empty recording path")
	}
	targetAbs, err := filepath.Abs(filepath.Join(deviceDir, cleanPath))
	if err != nil {
		return "", err
	}
	if !isSubPath(deviceDir, targetAbs) {
		return "", errors.New("invalid recording path")
	}
	return targetAbs, nil
}

func isSubPath(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func parsePositiveInt(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func resolveRequestHost(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return "127.0.0.1"
	}
	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(c.Request.Host)
	}
	if host == "" {
		return "127.0.0.1"
	}
	if strings.Contains(host, ",") {
		host = strings.TrimSpace(strings.Split(host, ",")[0])
	}
	parsedHost, _, err := net.SplitHostPort(host)
	if err == nil && parsedHost != "" {
		return parsedHost
	}
	return strings.Trim(host, "[]")
}

func isValidHostOrIP(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if ip := net.ParseIP(v); ip != nil {
		return true
	}
	if strings.Contains(v, ":") {
		return false
	}
	return hostReg.MatchString(v)
}

func isLocalHost(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" || v == "localhost" || v == "0.0.0.0" || v == "::" || v == "::1" || v == "127.0.0.1" {
		return true
	}
	ip := net.ParseIP(v)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range ifaces {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP != nil && ipNet.IP.Equal(ip) {
			return true
		}
	}
	return false
}

func tryBindLocalPort(transport string, port int) (bool, string) {
	addr := ":" + strconv.Itoa(port)
	switch transport {
	case "tcp":
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return false, "TCP 端口不可用: " + err.Error()
		}
		_ = ln.Close()
		return true, "TCP 端口可用"
	case "udp":
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return false, "UDP 端口不可用: " + err.Error()
		}
		_ = pc.Close()
		return true, "UDP 端口可用"
	default:
		return false, "不支持的传输协议"
	}
}

func checkTCPReachable(host string, port int, timeout time.Duration) (bool, string) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return false, "TCP 连通性检测失败: " + err.Error()
	}
	_ = conn.Close()
	return true, "TCP 端点可达"
}
func firstNonEmpty(items ...string) string {
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			return item
		}
	}
	return ""
}

func defaultLANCIDR() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "192.168.1.0/24"
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || !isPrivateIPv4(ip4) {
				continue
			}
			return net.IPv4(ip4[0], ip4[1], ip4[2], 0).String() + "/24"
		}
	}
	return "192.168.1.0/24"
}

func isPrivateIPv4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	if ip4[0] == 10 {
		return true
	}
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return true
	}
	return ip4[0] == 192 && ip4[1] == 168
}

func parsePortList(raw string) []int {
	parts := strings.Split(raw, ",")
	seen := make(map[int]struct{}, len(parts))
	ports := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := strconv.Atoi(part)
		if err != nil || p < 1 || p > 65535 {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

func expandIPv4Hosts(ipNet *net.IPNet, maxHosts int) ([]string, bool, error) {
	if ipNet == nil {
		return nil, false, errors.New("invalid cidr")
	}
	baseIP := ipNet.IP.To4()
	if baseIP == nil {
		return nil, false, errors.New("only ipv4 cidr is supported")
	}
	ones, bits := ipNet.Mask.Size()
	if bits != 32 {
		return nil, false, errors.New("only ipv4 cidr is supported")
	}

	hostBits := uint(32 - ones)
	if hostBits >= 32 {
		return nil, false, errors.New("cidr is too large")
	}
	totalAddrs := uint64(1) << hostBits
	network := ipv4ToUint32(baseIP.Mask(ipNet.Mask))
	start := network
	end := network + uint32(totalAddrs) - 1

	// Exclude network and broadcast address for common LAN CIDR blocks.
	if totalAddrs > 2 {
		start++
		end--
	}

	hosts := make([]string, 0, minInt(maxHosts, int(totalAddrs)))
	truncated := false
	for ipNum := start; ; ipNum++ {
		hosts = append(hosts, uint32ToIPv4(ipNum).String())
		if len(hosts) >= maxHosts {
			truncated = ipNum < end
			break
		}
		if ipNum == end {
			break
		}
	}
	return hosts, truncated, nil
}

func guessProtocolAndStreamURL(ip string, port int) (protocol string, streamURL string, note string) {
	switch port {
	case 554, 8554:
		return model.ProtocolRTSP, "rtsp://" + net.JoinHostPort(ip, strconv.Itoa(port)) + "/", "append device path and credentials"
	case 1935:
		return model.ProtocolRTMP, "rtmp://" + net.JoinHostPort(ip, strconv.Itoa(port)) + "/live/stream", "replace app/stream name and credentials if needed"
	case 80, 8080:
		return model.ProtocolONVIF, "http://" + net.JoinHostPort(ip, strconv.Itoa(port)) + "/onvif/device_service", "ONVIF endpoint, verify username/password"
	default:
		return "unknown", "tcp://" + net.JoinHostPort(ip, strconv.Itoa(port)), "port is reachable but protocol is unknown"
	}
}

func ipv4ToUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	return binary.BigEndian.Uint32(ip4)
}

func uint32ToIPv4(v uint32) net.IP {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, v)
	return net.IP(buf)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
