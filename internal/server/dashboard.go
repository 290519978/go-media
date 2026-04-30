package server

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"maas-box/internal/model"
)

const dashboardOverviewCacheTTL = 2 * time.Second

type dashboardSummary struct {
	TotalChannels    int `json:"total_channels"`
	OnlineChannels   int `json:"online_channels"`
	OfflineChannels  int `json:"offline_channels"`
	AlarmingChannels int `json:"alarming_channels"`
}

type dashboardAlgorithmStat struct {
	AlgorithmID   string `json:"algorithm_id" gorm:"column:algorithm_id"`
	AlgorithmName string `json:"algorithm_name" gorm:"column:algorithm_name"`
	AlarmCount    int64  `json:"alarm_count" gorm:"column:alarm_count"`
}

type dashboardLevelStat struct {
	AlarmLevelID    string `json:"alarm_level_id" gorm:"column:alarm_level_id"`
	AlarmLevelName  string `json:"alarm_level_name" gorm:"column:alarm_level_name"`
	AlarmLevelColor string `json:"alarm_level_color" gorm:"column:alarm_level_color"`
	AlarmCount      int64  `json:"alarm_count" gorm:"column:alarm_count"`
}

type dashboardAreaStat struct {
	AreaID     string `json:"area_id" gorm:"column:area_id"`
	AreaName   string `json:"area_name" gorm:"column:area_name"`
	AlarmCount int64  `json:"alarm_count" gorm:"column:alarm_count"`
}

type dashboardChannel struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	AreaID          string   `json:"area_id"`
	AreaName        string   `json:"area_name"`
	App             string   `json:"app"`
	StreamID        string   `json:"stream_id"`
	PlayWebRTCURL   string   `json:"play_webrtc_url"`
	PlayWSFLVURL    string   `json:"play_ws_flv_url"`
	TodayAlarmCount int64    `json:"today_alarm_count"`
	TotalAlarmCount int64    `json:"total_alarm_count"`
	Alarming60S     bool     `json:"alarming_60s"`
	Algorithms      []string `json:"algorithms"`
}

type dashboardOverviewPayload struct {
	Summary        dashboardSummary         `json:"summary"`
	AlgorithmStats []dashboardAlgorithmStat `json:"algorithm_stats"`
	LevelStats     []dashboardLevelStat     `json:"level_stats"`
	AreaStats      []dashboardAreaStat      `json:"area_stats"`
	Channels       []dashboardChannel       `json:"channels"`
	Runtime        runtimeMetricsPayload    `json:"runtime"`
	GeneratedAt    int64                    `json:"generated_at"`
}

type dashboardDeviceAlgorithmRow struct {
	DeviceID      string `gorm:"column:device_id"`
	AlgorithmName string `gorm:"column:algorithm_name"`
}

func (s *Server) registerDashboardRoutes(r gin.IRouter) {
	g := r.Group("/dashboard")
	g.GET("/overview", s.dashboardOverview)
	g.GET("/camera2/overview", s.dashboardCamera2Overview)
	g.POST("/camera2/patrol-jobs", s.createCamera2PatrolJob)
	g.GET("/camera2/patrol-jobs/:job_id", s.getCamera2PatrolJob)
}

func (s *Server) dashboardOverview(c *gin.Context) {
	payload, err := s.getDashboardOverviewCached(time.Now())
	if err != nil {
		s.fail(c, http.StatusInternalServerError, "查询大屏概览失败")
		return
	}
	s.ok(c, payload)
}

func (s *Server) getDashboardOverviewCached(now time.Time) (dashboardOverviewPayload, error) {
	s.dashboardOverviewMu.Lock()
	defer s.dashboardOverviewMu.Unlock()

	if !s.dashboardOverviewCacheAt.IsZero() && now.Sub(s.dashboardOverviewCacheAt) < dashboardOverviewCacheTTL {
		return s.dashboardOverviewCache, nil
	}

	payload, err := s.buildDashboardOverviewPayload(now)
	if err != nil {
		return dashboardOverviewPayload{}, err
	}
	s.dashboardOverviewCache = payload
	s.dashboardOverviewCacheAt = now
	return payload, nil
}

func collectDashboardAlgorithmsByDeviceRows(rows []dashboardDeviceAlgorithmRow, target map[string]map[string]struct{}) {
	for _, row := range rows {
		deviceID := strings.TrimSpace(row.DeviceID)
		algorithmName := strings.TrimSpace(row.AlgorithmName)
		if deviceID == "" || algorithmName == "" {
			continue
		}
		if _, exists := target[deviceID]; !exists {
			target[deviceID] = make(map[string]struct{})
		}
		target[deviceID][algorithmName] = struct{}{}
	}
}

func (s *Server) loadDashboardAlgorithmsByDevice() (map[string][]string, error) {
	uniqueByDevice := make(map[string]map[string]struct{})

	var modernRows []dashboardDeviceAlgorithmRow
	if err := s.db.Table("mb_video_task_device_algorithms dta").
		Select("dta.device_id AS device_id, a.name AS algorithm_name").
		Joins("JOIN mb_algorithms a ON a.id = dta.algorithm_id").
		Find(&modernRows).Error; err != nil {
		return nil, err
	}
	collectDashboardAlgorithmsByDeviceRows(modernRows, uniqueByDevice)

	var legacyRows []dashboardDeviceAlgorithmRow
	if err := s.db.Table("mb_video_task_devices td").
		Select("td.device_id AS device_id, a.name AS algorithm_name").
		Joins("JOIN mb_video_task_algorithms ta ON ta.task_id = td.task_id").
		Joins("JOIN mb_algorithms a ON a.id = ta.algorithm_id").
		Find(&legacyRows).Error; err != nil {
		return nil, err
	}
	collectDashboardAlgorithmsByDeviceRows(legacyRows, uniqueByDevice)

	algorithmsByDevice := make(map[string][]string, len(uniqueByDevice))
	for deviceID, unique := range uniqueByDevice {
		names := make([]string, 0, len(unique))
		for name := range unique {
			names = append(names, name)
		}
		sort.Strings(names)
		algorithmsByDevice[deviceID] = names
	}

	return algorithmsByDevice, nil
}

func (s *Server) buildDashboardOverviewPayload(now time.Time) (dashboardOverviewPayload, error) {
	var channels []model.MediaSource
	if err := s.db.
		Where("row_kind = ?", model.RowKindChannel).
		Order("created_at desc").
		Find(&channels).Error; err != nil {
		return dashboardOverviewPayload{}, err
	}

	areaNameByID := make(map[string]string)
	areaIDs := make([]string, 0, len(channels))
	areaSeen := make(map[string]struct{}, len(channels))
	for _, item := range channels {
		areaID := strings.TrimSpace(item.AreaID)
		if areaID == "" {
			continue
		}
		if _, exists := areaSeen[areaID]; exists {
			continue
		}
		areaSeen[areaID] = struct{}{}
		areaIDs = append(areaIDs, areaID)
	}
	if len(areaIDs) > 0 {
		var areas []model.Area
		if err := s.db.Where("id IN ?", areaIDs).Find(&areas).Error; err != nil {
			return dashboardOverviewPayload{}, err
		}
		for _, item := range areas {
			areaNameByID[item.ID] = strings.TrimSpace(item.Name)
		}
	}

	type deviceCountRow struct {
		DeviceID   string `gorm:"column:device_id"`
		AlarmCount int64  `gorm:"column:alarm_count"`
	}
	totalCountByDevice := make(map[string]int64)
	{
		var rows []deviceCountRow
		query := s.db.Model(&model.AlarmEvent{})
		query = applyAlarmEventSourceFilter(query, "mb_alarm_events.event_source", model.AlarmEventSourceRuntime)
		if err := query.
			Select("device_id, COUNT(1) AS alarm_count").
			Group("device_id").
			Find(&rows).Error; err != nil {
			return dashboardOverviewPayload{}, err
		}
		for _, row := range rows {
			totalCountByDevice[row.DeviceID] = row.AlarmCount
		}
	}

	todayCountByDevice := make(map[string]int64)
	{
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		var rows []deviceCountRow
		query := s.db.Model(&model.AlarmEvent{})
		query = applyAlarmEventSourceFilter(query, "mb_alarm_events.event_source", model.AlarmEventSourceRuntime)
		if err := query.
			Where("occurred_at >= ?", startOfDay).
			Select("device_id, COUNT(1) AS alarm_count").
			Group("device_id").
			Find(&rows).Error; err != nil {
			return dashboardOverviewPayload{}, err
		}
		for _, row := range rows {
			todayCountByDevice[row.DeviceID] = row.AlarmCount
		}
	}

	alarmingSet := make(map[string]struct{})
	{
		var alarmingIDs []string
		query := s.db.Model(&model.AlarmEvent{})
		query = applyAlarmEventSourceFilter(query, "mb_alarm_events.event_source", model.AlarmEventSourceRuntime)
		if err := query.
			Distinct("device_id").
			Where("status = ? AND occurred_at >= ?", model.EventStatusPending, now.Add(-60*time.Second)).
			Pluck("device_id", &alarmingIDs).Error; err != nil {
			return dashboardOverviewPayload{}, err
		}
		for _, id := range alarmingIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			alarmingSet[id] = struct{}{}
		}
	}

	algorithmsByDevice, err := s.loadDashboardAlgorithmsByDevice()
	if err != nil {
		return dashboardOverviewPayload{}, err
	}

	summary := dashboardSummary{
		TotalChannels: len(channels),
	}
	channelItems := make([]dashboardChannel, 0, len(channels))
	for _, item := range channels {
		source := item
		s.applySourceOutputPolicyView(&source)
		status := strings.ToLower(strings.TrimSpace(source.Status))
		if status == "online" {
			summary.OnlineChannels++
		} else {
			summary.OfflineChannels++
		}
		_, alarming := alarmingSet[source.ID]
		if alarming {
			summary.AlarmingChannels++
		}
		areaName := strings.TrimSpace(areaNameByID[source.AreaID])
		if areaName == "" {
			areaName = source.AreaID
		}
		if strings.TrimSpace(areaName) == "" {
			areaName = "未分配区域"
		}
		algorithms := make([]string, 0)
		if names, exists := algorithmsByDevice[source.ID]; exists {
			algorithms = names
		}
		channelItems = append(channelItems, dashboardChannel{
			ID:              source.ID,
			Name:            source.Name,
			Status:          source.Status,
			AreaID:          source.AreaID,
			AreaName:        areaName,
			App:             source.App,
			StreamID:        source.StreamID,
			PlayWebRTCURL:   source.PlayWebRTCURL,
			PlayWSFLVURL:    source.PlayWSFLVURL,
			TodayAlarmCount: todayCountByDevice[source.ID],
			TotalAlarmCount: totalCountByDevice[source.ID],
			Alarming60S:     alarming,
			Algorithms:      algorithms,
		})
	}

	algorithmStats := make([]dashboardAlgorithmStat, 0)
	queryAlgorithmStats := s.db.Table("mb_alarm_events e")
	queryAlgorithmStats = applyAlarmEventSourceFilter(queryAlgorithmStats, "e.event_source", model.AlarmEventSourceRuntime)
	if err := queryAlgorithmStats.
		Select("e.algorithm_id AS algorithm_id, COALESCE(a.name, '') AS algorithm_name, COUNT(1) AS alarm_count").
		Joins("LEFT JOIN mb_algorithms a ON a.id = e.algorithm_id").
		Group("e.algorithm_id, a.name").
		Order("alarm_count DESC").
		Find(&algorithmStats).Error; err != nil {
		return dashboardOverviewPayload{}, err
	}
	for i := range algorithmStats {
		if strings.TrimSpace(algorithmStats[i].AlgorithmName) == "" {
			algorithmStats[i].AlgorithmName = algorithmStats[i].AlgorithmID
		}
	}

	levelStats := make([]dashboardLevelStat, 0)
	queryLevelStats := s.db.Table("mb_alarm_events e")
	queryLevelStats = applyAlarmEventSourceFilter(queryLevelStats, "e.event_source", model.AlarmEventSourceRuntime)
	if err := queryLevelStats.
		Select("e.alarm_level_id AS alarm_level_id, COALESCE(l.name, '') AS alarm_level_name, COALESCE(l.color, '#faad14') AS alarm_level_color, COUNT(1) AS alarm_count").
		Joins("LEFT JOIN mb_alarm_levels l ON l.id = e.alarm_level_id").
		Group("e.alarm_level_id, l.name, l.color").
		Order("alarm_count DESC").
		Find(&levelStats).Error; err != nil {
		return dashboardOverviewPayload{}, err
	}
	for i := range levelStats {
		if strings.TrimSpace(levelStats[i].AlarmLevelName) == "" {
			levelStats[i].AlarmLevelName = levelStats[i].AlarmLevelID
		}
	}

	areaStats := make([]dashboardAreaStat, 0)
	queryAreaStats := s.db.Table("mb_alarm_events e")
	queryAreaStats = applyAlarmEventSourceFilter(queryAreaStats, "e.event_source", model.AlarmEventSourceRuntime)
	if err := queryAreaStats.
		Select("COALESCE(d.area_id, '') AS area_id, COALESCE(ar.name, '') AS area_name, COUNT(1) AS alarm_count").
		Joins("LEFT JOIN mb_media_sources d ON d.id = e.device_id").
		Joins("LEFT JOIN mb_areas ar ON ar.id = d.area_id").
		Group("d.area_id, ar.name").
		Order("alarm_count DESC").
		Find(&areaStats).Error; err != nil {
		return dashboardOverviewPayload{}, err
	}
	for i := range areaStats {
		if strings.TrimSpace(areaStats[i].AreaName) == "" {
			if strings.TrimSpace(areaStats[i].AreaID) != "" {
				areaStats[i].AreaName = areaStats[i].AreaID
			} else {
				areaStats[i].AreaName = "未分配区域"
			}
		}
	}

	runtimePayload := s.collectRuntimeMetrics(now)
	return dashboardOverviewPayload{
		Summary:        summary,
		AlgorithmStats: algorithmStats,
		LevelStats:     levelStats,
		AreaStats:      areaStats,
		Channels:       channelItems,
		Runtime:        runtimePayload,
		GeneratedAt:    now.UnixMilli(),
	}, nil
}
