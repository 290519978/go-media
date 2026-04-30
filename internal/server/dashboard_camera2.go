package server

import (
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"maas-box/internal/model"
)

const (
	camera2RangeToday  = "today"
	camera2Range7Days  = "7days"
	camera2RangeCustom = "custom"

	camera2TrendUnitHour = "hour"
	camera2TrendUnitDay  = "day"
)

type camera2OverviewRange struct {
	Range     string
	StartAt   time.Time
	EndAt     time.Time
	TrendUnit string
}

type camera2OverviewPayload struct {
	Range               string                     `json:"range"`
	StartAt             int64                      `json:"start_at"`
	EndAt               int64                      `json:"end_at"`
	AlarmStatistics     camera2AlarmStatistics     `json:"alarm_statistics"`
	AlgorithmStatistics camera2AlgorithmStatistics `json:"algorithm_statistics"`
	DeviceStatistics    camera2DeviceStatistics    `json:"device_statistics"`
	Analysis            camera2Analysis            `json:"analysis"`
	ResourceStatistics  camera2ResourceStatistics  `json:"resource_statistics"`
	GeneratedAt         int64                      `json:"generated_at"`
}

type camera2AlarmStatistics struct {
	TotalAlarmCount int64   `json:"total_alarm_count"`
	PendingCount    int64   `json:"pending_count"`
	HandlingRate    float64 `json:"handling_rate"`
	FalseAlarmRate  float64 `json:"false_alarm_rate"`
	HighCount       int64   `json:"high_count"`
	MediumCount     int64   `json:"medium_count"`
	LowCount        int64   `json:"low_count"`
}

type camera2AlgorithmStatistics struct {
	DeployTotal     int64                            `json:"deploy_total"`
	RunningTotal    int64                            `json:"running_total"`
	AverageAccuracy float64                          `json:"average_accuracy"`
	TodayCallCount  int64                            `json:"today_call_count"`
	Items           []camera2AlgorithmStatisticsItem `json:"items"`
}

type camera2AlgorithmStatisticsItem struct {
	AlgorithmID   string  `json:"algorithm_id"`
	AlgorithmName string  `json:"algorithm_name"`
	AlarmCount    int64   `json:"alarm_count"`
	Accuracy      float64 `json:"accuracy"`
}

type camera2DeviceStatistics struct {
	TotalDevices   int64                  `json:"total_devices"`
	AreaCount      int64                  `json:"area_count"`
	OnlineDevices  int64                  `json:"online_devices"`
	OnlineRate     float64                `json:"online_rate"`
	AlarmDevices   int64                  `json:"alarm_devices"`
	OfflineDevices int64                  `json:"offline_devices"`
	TopDevices     []camera2DeviceTopItem `json:"top_devices"`
}

type camera2DeviceTopItem struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	AreaID     string `json:"area_id"`
	AreaName   string `json:"area_name"`
	AlarmCount int64  `json:"alarm_count"`
}

type camera2Analysis struct {
	AreaDistribution []camera2AnalysisCountItem `json:"area_distribution"`
	TypeDistribution []camera2AnalysisCountItem `json:"type_distribution"`
	Trend            []camera2TrendPoint        `json:"trend"`
	TrendUnit        string                     `json:"trend_unit"`
}

type camera2AnalysisCountItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type camera2TrendPoint struct {
	Label      string `json:"label"`
	BucketAt   int64  `json:"bucket_at"`
	AlarmCount int64  `json:"alarm_count"`
}

type camera2ResourceStatistics struct {
	CPUPercent             float64  `json:"cpu_percent"`
	MemoryPercent          float64  `json:"memory_percent"`
	DiskPercent            float64  `json:"disk_percent"`
	NetworkStatus          string   `json:"network_status"`
	NetworkTXBPS           float64  `json:"network_tx_bps"`
	NetworkRXBPS           float64  `json:"network_rx_bps"`
	TokenTotalLimit        int64    `json:"token_total_limit"`
	TokenUsed              int64    `json:"token_used"`
	TokenRemaining         int64    `json:"token_remaining"`
	TokenUsageRate         float64  `json:"token_usage_rate"`
	EstimatedRemainingDays *float64 `json:"estimated_remaining_days"`
}

type camera2EventStatRow struct {
	DeviceID      string    `gorm:"column:device_id"`
	DeviceName    string    `gorm:"column:device_name"`
	AlgorithmID   string    `gorm:"column:algorithm_id"`
	AlgorithmName string    `gorm:"column:algorithm_name"`
	AreaID        string    `gorm:"column:area_id"`
	AreaName      string    `gorm:"column:area_name"`
	Status        string    `gorm:"column:status"`
	Severity      int       `gorm:"column:severity"`
	OccurredAt    time.Time `gorm:"column:occurred_at"`
}

type camera2AlgorithmAggregate struct {
	AlgorithmID   string
	AlgorithmName string
	AlarmCount    int64
	PendingCount  int64
	ValidCount    int64
	InvalidCount  int64
}

type camera2DeviceAggregate struct {
	DeviceID   string
	DeviceName string
	AreaID     string
	AreaName   string
	AlarmCount int64
}

type camera2TokenUsageRow struct {
	OccurredAt  time.Time `gorm:"column:occurred_at"`
	TotalTokens int64     `gorm:"column:total_tokens"`
}

type camera2CountRow struct {
	Count int64 `gorm:"column:count"`
}

func (s *Server) dashboardCamera2Overview(c *gin.Context) {
	now := time.Now()
	rangeQuery, err := parseCamera2OverviewRange(c, now)
	if err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	payload, err := s.buildCamera2OverviewPayload(rangeQuery, now)
	if err != nil {
		s.fail(c, http.StatusInternalServerError, "查询第二大屏概览失败")
		return
	}
	s.ok(c, payload)
}

func parseCamera2OverviewRange(c *gin.Context, now time.Time) (camera2OverviewRange, error) {
	localNow := now.In(time.Local)
	rangeValue := strings.ToLower(strings.TrimSpace(c.DefaultQuery("range", camera2RangeToday)))
	startOfToday := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, localNow.Location())

	switch rangeValue {
	case "", camera2RangeToday:
		return camera2OverviewRange{
			Range:     camera2RangeToday,
			StartAt:   startOfToday,
			EndAt:     localNow,
			TrendUnit: camera2TrendUnitHour,
		}, nil
	case camera2Range7Days:
		return camera2OverviewRange{
			Range:     camera2Range7Days,
			StartAt:   startOfToday.AddDate(0, 0, -6),
			EndAt:     localNow,
			TrendUnit: camera2TrendUnitDay,
		}, nil
	case camera2RangeCustom:
		startAt, err := parseEventQueryTime(c.Query("start_at"))
		if err != nil || startAt.IsZero() {
			return camera2OverviewRange{}, httpError("start_at 不能为空且格式必须正确")
		}
		endAt, err := parseEventQueryTime(c.Query("end_at"))
		if err != nil || endAt.IsZero() {
			return camera2OverviewRange{}, httpError("end_at 不能为空且格式必须正确")
		}
		startAt = startAt.In(time.Local)
		endAt = endAt.In(time.Local)
		if startAt.After(endAt) {
			return camera2OverviewRange{}, httpError("start_at 不能晚于 end_at")
		}
		trendUnit := camera2TrendUnitDay
		if endAt.Sub(startAt) <= 48*time.Hour {
			trendUnit = camera2TrendUnitHour
		}
		return camera2OverviewRange{
			Range:     camera2RangeCustom,
			StartAt:   startAt,
			EndAt:     endAt,
			TrendUnit: trendUnit,
		}, nil
	default:
		return camera2OverviewRange{}, httpError("range 仅支持 today、7days、custom")
	}
}

func (s *Server) buildCamera2OverviewPayload(rangeQuery camera2OverviewRange, now time.Time) (camera2OverviewPayload, error) {
	eventRows, err := s.loadCamera2EventRows(rangeQuery)
	if err != nil {
		return camera2OverviewPayload{}, err
	}
	currentOverview, err := s.getDashboardOverviewCached(now)
	if err != nil {
		return camera2OverviewPayload{}, err
	}

	alarmStats, averageAccuracy, algorithmItems, areaDistribution, typeDistribution, trendPoints, deviceTopItems := buildCamera2EventDerivedStats(eventRows, rangeQuery)
	algorithmStats, err := s.buildCamera2AlgorithmStatistics(now, averageAccuracy, algorithmItems)
	if err != nil {
		return camera2OverviewPayload{}, err
	}
	deviceStats, err := s.buildCamera2DeviceStatistics(currentOverview, deviceTopItems)
	if err != nil {
		return camera2OverviewPayload{}, err
	}
	resourceStats, err := s.buildCamera2ResourceStatistics(now, currentOverview.Runtime)
	if err != nil {
		return camera2OverviewPayload{}, err
	}

	return camera2OverviewPayload{
		Range:               rangeQuery.Range,
		StartAt:             rangeQuery.StartAt.UnixMilli(),
		EndAt:               rangeQuery.EndAt.UnixMilli(),
		AlarmStatistics:     alarmStats,
		AlgorithmStatistics: algorithmStats,
		DeviceStatistics:    deviceStats,
		Analysis: camera2Analysis{
			AreaDistribution: areaDistribution,
			TypeDistribution: typeDistribution,
			Trend:            trendPoints,
			TrendUnit:        rangeQuery.TrendUnit,
		},
		ResourceStatistics: resourceStats,
		GeneratedAt:        now.UnixMilli(),
	}, nil
}

func (s *Server) loadCamera2EventRows(rangeQuery camera2OverviewRange) ([]camera2EventStatRow, error) {
	rows := make([]camera2EventStatRow, 0)
	query := s.db.Table("mb_alarm_events e")
	query = applyAlarmEventSourceFilter(query, "e.event_source", model.AlarmEventSourceRuntime)
	err := query.
		Select(
			"e.device_id AS device_id, COALESCE(d.name, '') AS device_name, "+
				"e.algorithm_id AS algorithm_id, COALESCE(a.name, '') AS algorithm_name, "+
				"COALESCE(d.area_id, '') AS area_id, COALESCE(ar.name, '') AS area_name, "+
				"e.status AS status, COALESCE(l.severity, 0) AS severity, e.occurred_at AS occurred_at",
		).
		Joins("LEFT JOIN mb_media_sources d ON d.id = e.device_id").
		Joins("LEFT JOIN mb_algorithms a ON a.id = e.algorithm_id").
		Joins("LEFT JOIN mb_areas ar ON ar.id = d.area_id").
		Joins("LEFT JOIN mb_alarm_levels l ON l.id = e.alarm_level_id").
		Where("e.occurred_at >= ? AND e.occurred_at <= ?", rangeQuery.StartAt, rangeQuery.EndAt).
		Order("e.occurred_at asc").
		Find(&rows).Error
	return rows, err
}

func buildCamera2EventDerivedStats(rows []camera2EventStatRow, rangeQuery camera2OverviewRange) (
	camera2AlarmStatistics,
	float64,
	[]camera2AlgorithmStatisticsItem,
	[]camera2AnalysisCountItem,
	[]camera2AnalysisCountItem,
	[]camera2TrendPoint,
	[]camera2DeviceTopItem,
) {
	alarmStats := camera2AlarmStatistics{
		TotalAlarmCount: int64(len(rows)),
	}
	algorithmAgg := make(map[string]*camera2AlgorithmAggregate)
	deviceAgg := make(map[string]*camera2DeviceAggregate)
	areaDistribution := make(map[string]*camera2AnalysisCountItem)
	typeDistribution := make(map[string]*camera2AnalysisCountItem)
	trendCountByBucket := make(map[int64]int64)

	var validCount int64
	var invalidCount int64

	for _, row := range rows {
		status := strings.ToLower(strings.TrimSpace(row.Status))
		switch status {
		case model.EventStatusPending:
			alarmStats.PendingCount++
		case model.EventStatusValid:
			validCount++
		case model.EventStatusInvalid:
			invalidCount++
		}
		switch {
		// camera2 大屏按约定将 1/2/3 展示为高/中/低，口径只收敛在该聚合接口内。
		case row.Severity <= 1:
			alarmStats.HighCount++
		case row.Severity == 2:
			alarmStats.MediumCount++
		default:
			alarmStats.LowCount++
		}

		algorithmID := strings.TrimSpace(row.AlgorithmID)
		algorithmName := normalizeCamera2Name(strings.TrimSpace(row.AlgorithmName), algorithmID, "未知算法")
		agg, exists := algorithmAgg[algorithmID]
		if !exists {
			agg = &camera2AlgorithmAggregate{
				AlgorithmID:   algorithmID,
				AlgorithmName: algorithmName,
			}
			algorithmAgg[algorithmID] = agg
		}
		agg.AlarmCount++
		if status == model.EventStatusPending {
			agg.PendingCount++
		}
		if status == model.EventStatusValid {
			agg.ValidCount++
		}
		if status == model.EventStatusInvalid {
			agg.InvalidCount++
		}

		deviceID := strings.TrimSpace(row.DeviceID)
		deviceName := normalizeCamera2Name(strings.TrimSpace(row.DeviceName), deviceID, "未知设备")
		areaID := strings.TrimSpace(row.AreaID)
		areaName := normalizeCamera2AreaName(strings.TrimSpace(row.AreaName), areaID)
		deviceItem, exists := deviceAgg[deviceID]
		if !exists {
			deviceItem = &camera2DeviceAggregate{
				DeviceID:   deviceID,
				DeviceName: deviceName,
				AreaID:     areaID,
				AreaName:   areaName,
			}
			deviceAgg[deviceID] = deviceItem
		}
		deviceItem.AlarmCount++

		areaKey := areaID
		if areaKey == "" {
			areaKey = "__unassigned__"
		}
		if _, exists := areaDistribution[areaKey]; !exists {
			areaDistribution[areaKey] = &camera2AnalysisCountItem{
				ID:   areaID,
				Name: areaName,
			}
		}
		areaDistribution[areaKey].Count++

		typeKey := algorithmID
		if typeKey == "" {
			typeKey = "__unknown__"
		}
		if _, exists := typeDistribution[typeKey]; !exists {
			typeDistribution[typeKey] = &camera2AnalysisCountItem{
				ID:   algorithmID,
				Name: algorithmName,
			}
		}
		typeDistribution[typeKey].Count++

		bucketTime := truncateCamera2Bucket(row.OccurredAt.In(time.Local), rangeQuery.TrendUnit)
		trendCountByBucket[bucketTime.UnixMilli()]++
	}

	// 处理率和误报率仍按已审核事件口径统计，避免和第二大屏的准确率展示语义混淆。
	alarmStats.HandlingRate = camera2Percent(validCount+invalidCount, alarmStats.TotalAlarmCount)
	alarmStats.FalseAlarmRate = camera2Percent(invalidCount, alarmStats.TotalAlarmCount)

	algorithmItems := make([]camera2AlgorithmStatisticsItem, 0, len(algorithmAgg))
	for _, item := range algorithmAgg {
		// camera2 大屏准确率按需求采用“有效 + 待处理”口径，分母固定为该算法告警总数。
		algorithmItems = append(algorithmItems, camera2AlgorithmStatisticsItem{
			AlgorithmID:   item.AlgorithmID,
			AlgorithmName: item.AlgorithmName,
			AlarmCount:    item.AlarmCount,
			Accuracy:      camera2Percent(item.ValidCount+item.PendingCount, item.AlarmCount),
		})
	}
	sort.Slice(algorithmItems, func(i, j int) bool {
		if algorithmItems[i].AlarmCount == algorithmItems[j].AlarmCount {
			return algorithmItems[i].AlgorithmName < algorithmItems[j].AlgorithmName
		}
		return algorithmItems[i].AlarmCount > algorithmItems[j].AlarmCount
	})

	deviceItems := make([]camera2DeviceTopItem, 0, len(deviceAgg))
	for _, item := range deviceAgg {
		deviceItems = append(deviceItems, camera2DeviceTopItem{
			DeviceID:   item.DeviceID,
			DeviceName: item.DeviceName,
			AreaID:     item.AreaID,
			AreaName:   item.AreaName,
			AlarmCount: item.AlarmCount,
		})
	}
	sort.Slice(deviceItems, func(i, j int) bool {
		if deviceItems[i].AlarmCount == deviceItems[j].AlarmCount {
			return deviceItems[i].DeviceName < deviceItems[j].DeviceName
		}
		return deviceItems[i].AlarmCount > deviceItems[j].AlarmCount
	})
	if len(deviceItems) > 3 {
		deviceItems = deviceItems[:3]
	}

	areaItems := make([]camera2AnalysisCountItem, 0, len(areaDistribution))
	for _, item := range areaDistribution {
		areaItems = append(areaItems, *item)
	}
	sort.Slice(areaItems, func(i, j int) bool {
		if areaItems[i].Count == areaItems[j].Count {
			return areaItems[i].Name < areaItems[j].Name
		}
		return areaItems[i].Count > areaItems[j].Count
	})

	typeItems := make([]camera2AnalysisCountItem, 0, len(typeDistribution))
	for _, item := range typeDistribution {
		typeItems = append(typeItems, *item)
	}
	sort.Slice(typeItems, func(i, j int) bool {
		if typeItems[i].Count == typeItems[j].Count {
			return typeItems[i].Name < typeItems[j].Name
		}
		return typeItems[i].Count > typeItems[j].Count
	})

	trendItems := buildCamera2TrendPoints(rangeQuery, trendCountByBucket)
	return alarmStats, camera2Percent(validCount+alarmStats.PendingCount, alarmStats.TotalAlarmCount), algorithmItems, areaItems, typeItems, trendItems, deviceItems
}

func (s *Server) buildCamera2AlgorithmStatistics(
	now time.Time,
	averageAccuracy float64,
	items []camera2AlgorithmStatisticsItem,
) (camera2AlgorithmStatistics, error) {
	var deployTotal int64
	if err := s.db.Model(&model.Algorithm{}).Where("enabled = ?", true).Count(&deployTotal).Error; err != nil {
		return camera2AlgorithmStatistics{}, err
	}

	runningTotal, err := s.countCamera2RunningAlgorithms()
	if err != nil {
		return camera2AlgorithmStatistics{}, err
	}

	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var todayCallCount int64
	if err := s.db.Model(&model.LLMUsageCall{}).
		Where("source = ? AND occurred_at >= ?", model.LLMUsageSourceTaskRuntime, startOfDay).
		Count(&todayCallCount).Error; err != nil {
		return camera2AlgorithmStatistics{}, err
	}

	return camera2AlgorithmStatistics{
		DeployTotal:     deployTotal,
		RunningTotal:    runningTotal,
		AverageAccuracy: averageAccuracy,
		TodayCallCount:  todayCallCount,
		Items:           items,
	}, nil
}

func (s *Server) countCamera2RunningAlgorithms() (int64, error) {
	var row camera2CountRow
	err := s.db.Raw(`
SELECT COUNT(DISTINCT algorithm_id) AS count
FROM (
	SELECT tda.algorithm_id AS algorithm_id
	FROM mb_video_task_device_algorithms tda
	INNER JOIN mb_media_sources d ON d.id = tda.device_id
	INNER JOIN mb_algorithms a ON a.id = tda.algorithm_id
	WHERE d.ai_status = ? AND a.enabled = ?
	UNION
	SELECT ta.algorithm_id AS algorithm_id
	FROM mb_video_task_devices td
	INNER JOIN mb_video_task_algorithms ta ON ta.task_id = td.task_id
	INNER JOIN mb_media_sources d ON d.id = td.device_id
	INNER JOIN mb_algorithms a ON a.id = ta.algorithm_id
	WHERE d.ai_status = ? AND a.enabled = ?
) AS running_algorithms
`, model.DeviceAIStatusRunning, true, model.DeviceAIStatusRunning, true).Scan(&row).Error
	return row.Count, err
}

func (s *Server) buildCamera2DeviceStatistics(
	overview dashboardOverviewPayload,
	topItems []camera2DeviceTopItem,
) (camera2DeviceStatistics, error) {
	areaSet := make(map[string]struct{})
	for _, channel := range overview.Channels {
		areaID := strings.TrimSpace(channel.AreaID)
		if areaID == "" || areaID == model.RootAreaID {
			continue
		}
		areaSet[areaID] = struct{}{}
	}

	totalDevices := int64(overview.Summary.TotalChannels)
	onlineDevices := int64(overview.Summary.OnlineChannels)
	offlineDevices := int64(overview.Summary.OfflineChannels)
	// camera2 的报警设备卡片需要与 camera 大屏保持一致，展示当前 60 秒内的告警设备数。
	alarmDevices := int64(overview.Summary.AlarmingChannels)
	return camera2DeviceStatistics{
		TotalDevices:   totalDevices,
		AreaCount:      int64(len(areaSet)),
		OnlineDevices:  onlineDevices,
		OnlineRate:     camera2Percent(onlineDevices, totalDevices),
		AlarmDevices:   alarmDevices,
		OfflineDevices: offlineDevices,
		TopDevices:     topItems,
	}, nil
}

func (s *Server) buildCamera2ResourceStatistics(
	now time.Time,
	runtimePayload runtimeMetricsPayload,
) (camera2ResourceStatistics, error) {
	usedTokens, recentRows, err := s.loadCamera2TokenUsageRows(now)
	if err != nil {
		return camera2ResourceStatistics{}, err
	}

	totalLimit := s.cfg.Server.AI.TotalTokenLimit
	var remaining int64
	var usageRate float64
	var estimatedDays *float64
	if totalLimit > 0 {
		if usedTokens >= totalLimit {
			remaining = 0
		} else {
			remaining = totalLimit - usedTokens
		}
		usageRate = camera2Percent(usedTokens, totalLimit)

		averageDailyUsage := calculateCamera2AverageDailyUsage(recentRows, now.In(time.Local))
		if averageDailyUsage > 0 {
			value := float64(remaining) / averageDailyUsage
			estimatedDays = &value
		}
	}

	networkStatus := "正常"
	if runtimePayload.Network.RXBPS <= 0 && runtimePayload.Network.TXBPS <= 0 {
		networkStatus = "空闲"
	}

	return camera2ResourceStatistics{
		CPUPercent:             normalizeCamera2RuntimePercent(runtimePayload.CPUPercent),
		MemoryPercent:          normalizeCamera2RuntimePercent(runtimePayload.Memory.UsedPercent),
		DiskPercent:            normalizeCamera2RuntimePercent(runtimePayload.Disk.UsedPercent),
		NetworkStatus:          networkStatus,
		NetworkTXBPS:           runtimePayload.Network.TXBPS,
		NetworkRXBPS:           runtimePayload.Network.RXBPS,
		TokenTotalLimit:        totalLimit,
		TokenUsed:              usedTokens,
		TokenRemaining:         remaining,
		TokenUsageRate:         normalizeCamera2RuntimePercent(usageRate),
		EstimatedRemainingDays: estimatedDays,
	}, nil
}

func (s *Server) loadCamera2TokenUsageRows(now time.Time) (int64, []camera2TokenUsageRow, error) {
	state, err := s.loadLLMTokenQuotaState(nil)
	if err != nil {
		return 0, nil, err
	}

	startOfWindow := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -6)
	usageRows, err := s.loadLLMTokenUsageRows(startOfWindow, now)
	if err != nil {
		return 0, nil, err
	}
	rows := make([]camera2TokenUsageRow, 0, len(usageRows))
	for _, row := range usageRows {
		rows = append(rows, camera2TokenUsageRow{
			OccurredAt:  row.OccurredAt,
			TotalTokens: row.TotalTokens,
		})
	}
	return state.UsedTokens, rows, nil
}

func calculateCamera2AverageDailyUsage(rows []camera2TokenUsageRow, now time.Time) float64 {
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayTotals := make(map[string]int64, 7)
	for offset := 0; offset < 7; offset++ {
		dayKey := startOfToday.AddDate(0, 0, -offset).Format("2006-01-02")
		dayTotals[dayKey] = 0
	}
	for _, row := range rows {
		dayKey := row.OccurredAt.In(time.Local).Format("2006-01-02")
		if _, exists := dayTotals[dayKey]; !exists {
			continue
		}
		dayTotals[dayKey] += row.TotalTokens
	}
	var total int64
	for _, value := range dayTotals {
		total += value
	}
	return float64(total) / 7
}

func truncateCamera2Bucket(source time.Time, trendUnit string) time.Time {
	local := source.In(time.Local)
	if trendUnit == camera2TrendUnitHour {
		return time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, local.Location())
	}
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
}

func buildCamera2TrendPoints(rangeQuery camera2OverviewRange, countByBucket map[int64]int64) []camera2TrendPoint {
	start := truncateCamera2Bucket(rangeQuery.StartAt, rangeQuery.TrendUnit)
	end := truncateCamera2Bucket(rangeQuery.EndAt, rangeQuery.TrendUnit)
	step := 24 * time.Hour
	if rangeQuery.TrendUnit == camera2TrendUnitHour {
		step = time.Hour
	}

	points := make([]camera2TrendPoint, 0)
	// 这里强制补齐时间桶，前端折线图收到 0 值也能保持稳定刻度和分页体验。
	for cursor := start; !cursor.After(end); cursor = cursor.Add(step) {
		bucketAt := cursor.UnixMilli()
		points = append(points, camera2TrendPoint{
			Label:      formatCamera2TrendLabel(cursor, rangeQuery),
			BucketAt:   bucketAt,
			AlarmCount: countByBucket[bucketAt],
		})
	}
	return points
}

func formatCamera2TrendLabel(bucketAt time.Time, rangeQuery camera2OverviewRange) string {
	if rangeQuery.TrendUnit == camera2TrendUnitHour {
		if rangeQuery.Range == camera2RangeToday {
			return bucketAt.Format("15:00")
		}
		return bucketAt.Format("01-02 15:00")
	}
	return bucketAt.Format("01-02")
}

func normalizeCamera2Name(primary string, fallback string, emptyFallback string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	if strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback)
	}
	return emptyFallback
}

func normalizeCamera2AreaName(primary string, areaID string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	if strings.TrimSpace(areaID) != "" && strings.TrimSpace(areaID) != model.RootAreaID {
		return strings.TrimSpace(areaID)
	}
	return "未分配区域"
}

func camera2Percent(numerator int64, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) * 100 / float64(denominator)
}

func normalizeCamera2RuntimePercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

type httpError string

func (e httpError) Error() string { return string(e) }
