import { alarmLevelAPI, dashboardAPI, eventAPI } from '@/api/modules'
import { appendTokenQuery } from '@/api/request'

export type DeviceFilterMode = 'online' | 'all' | 'alarm'
export type AlarmStatusFilter = '' | 'pending' | 'handled'

type DashboardAlgorithmStat = {
  algorithm_id: string
  algorithm_name: string
  alarm_count: number
}

type DashboardLevelStat = {
  alarm_level_id: string
  alarm_level_name: string
  alarm_level_color: string
  alarm_count: number
}

type DashboardAreaStat = {
  area_id: string
  area_name: string
  alarm_count: number
}

type DashboardChannel = {
  id: string
  name: string
  status: string
  area_id: string
  area_name: string
  app?: string
  stream_id?: string
  play_webrtc_url?: string
  play_ws_flv_url?: string
  today_alarm_count: number
  total_alarm_count: number
  alarming_60s: boolean
  algorithms: string[]
}

type DashboardRuntime = {
  version: string
  uptime_seconds: number
  cpu_percent: number
  memory?: { used_percent?: number }
  disk?: { used_percent?: number }
  network?: { tx_bps?: number; rx_bps?: number }
  gpu?: { supported?: boolean; util_percent?: number }
}

type DashboardOverviewResponse = {
  algorithm_stats: DashboardAlgorithmStat[]
  level_stats: DashboardLevelStat[]
  area_stats: DashboardAreaStat[]
  channels: DashboardChannel[]
  runtime: DashboardRuntime
}

type EventListItem = {
  id: string
  algorithm_id: string
  algorithm_name?: string
  alarm_level_id: string
  alarm_level_name?: string
  alarm_level_color?: string
  alarm_level_severity?: number
  area_id?: string
  area_name?: string
  status: string
  occurred_at: string | number
  snapshot_path?: string
}

type EventListResponse = {
  items?: EventListItem[]
  total?: number
  page?: number
  page_size?: number
}

type AlarmLevelEntity = {
  id: string
  name: string
  severity: number
  color: string
  description?: string
}

type AlarmLevelListResponse = {
  items?: AlarmLevelEntity[]
}

export interface SystemInfoData {
  version: string
  uptimeSeconds: number
  cpuUsage: number
  memUsage: number
  diskUsage: number
  netTxBps: number
  netRxBps: number
}

export interface AlgorithmStatItem {
  algorithmID: string
  algorithmName: string
  alarmCount: number
}

export interface AlarmLevelItem {
  levelID: string
  levelName: string
  levelColor: string
  alarmCount: number
  severity: number
}

export interface AreaStatItem {
  areaID: string
  areaName: string
  deviceCount: number
  alarmCount: number
}

export interface LeftPanelData {
  alarmAlgorithmList: AlgorithmStatItem[]
  alarmLevelList: AlarmLevelItem[]
  alarmAreaList: AreaStatItem[]
}

export interface DeviceItem {
  id: string
  deviceName: string
  deviceArea: string
  areaID: string
  streamApp: string
  streamID: string
  status: 0 | 1
  alarming60s: boolean
  todayAlarm: number
  totalAlarm: number
  bindingAlgorithm: string
  algorithms: string[]
  streamUrl: string
  streamUrlWebrtc: string
  streamUrlWsFlv: string
}

export interface AlarmItem {
  id: string
  status: string
  level: '0' | '1' | '2'
  alarmLevelID: string
  alarmLevelName: string
  alarmLevelColor: string
  alarmLevelSeverity: number
  relAlgorithmName: string
  alarmTime: string
  occurredAt: number
  relAreaName: string
  areaID: string
  algorithmID: string
  images: string
  hasSnapshot: boolean
}

export interface AlarmQueryParams {
  page?: number
  pageSize?: number
  status?: AlarmStatusFilter
  areaID?: string
  algorithmID?: string
  alarmLevelID?: string
  beginTime?: string
  endTime?: string
}

export interface AlarmListResult {
  items: AlarmItem[]
  totalRaw: number
  page: number
  pageSize: number
}

function toNumber(value: unknown): number {
  const num = Number(value)
  return Number.isFinite(num) ? num : 0
}

function normalizeStatus(status: string): string {
  return String(status || '').trim().toLowerCase()
}

function formatDateTime(value: string | number | undefined): string {
  if (value === undefined || value === null || value === '') return '-'
  const source = typeof value === 'number' ? new Date(value) : new Date(String(value))
  if (Number.isNaN(source.getTime())) return String(value)
  const yyyy = source.getFullYear()
  const mm = String(source.getMonth() + 1).padStart(2, '0')
  const dd = String(source.getDate()).padStart(2, '0')
  const hh = String(source.getHours()).padStart(2, '0')
  const mi = String(source.getMinutes()).padStart(2, '0')
  const ss = String(source.getSeconds()).padStart(2, '0')
  return `${yyyy}-${mm}-${dd} ${hh}:${mi}:${ss}`
}

function encodeEventImagePath(rawPath: string): string {
  return String(rawPath || '')
    .split('/')
    .filter(Boolean)
    .map((segment) => encodeURIComponent(segment))
    .join('/')
}

function mapAlarmLevel(event: EventListItem): '0' | '1' | '2' {
  const severity = toNumber(event.alarm_level_severity)
  if (severity >= 3) return '0'
  if (severity >= 2) return '1'
  const name = String(event.alarm_level_name || '')
  if (name.includes('紧急') || name.includes('严重')) return '0'
  if (name.includes('重要') || name.includes('高')) return '1'
  return '2'
}

function mapEventItem(item: EventListItem): AlarmItem {
  const occurredAt = toNumber(item.occurred_at)
  const fallbackTime = new Date(String(item.occurred_at || '')).getTime()
  const normalizedOccurredAt = occurredAt > 0
    ? occurredAt
    : (Number.isFinite(fallbackTime) ? fallbackTime : 0)
  const occurredAtDate = normalizedOccurredAt > 0 ? normalizedOccurredAt : String(item.occurred_at || '')
  const snapshot = String(item.snapshot_path || '').trim()
  const hasSnapshot = snapshot.length > 0
  const snapshotURL = snapshot
    ? appendTokenQuery(`/api/v1/events/image/${encodeEventImagePath(snapshot)}`)
    : new URL('../../../assets/dashboard/entry-camera-preview.png', import.meta.url).href

  return {
    id: String(item.id || ''),
    status: String(item.status || ''),
    level: mapAlarmLevel(item),
    alarmLevelID: String(item.alarm_level_id || ''),
    alarmLevelName: String(item.alarm_level_name || item.alarm_level_id || '-'),
    alarmLevelColor: String(item.alarm_level_color || ''),
    alarmLevelSeverity: toNumber(item.alarm_level_severity),
    relAlgorithmName: String(item.algorithm_name || item.algorithm_id || '-'),
    alarmTime: formatDateTime(occurredAtDate),
    occurredAt: normalizedOccurredAt,
    relAreaName: String(item.area_name || item.area_id || '-'),
    areaID: String(item.area_id || ''),
    algorithmID: String(item.algorithm_id || ''),
    images: snapshotURL,
    hasSnapshot,
  }
}

function buildEventListQuery(params: AlarmQueryParams, page: number, pageSize: number): Record<string, unknown> {
  const query: Record<string, unknown> = {
    page,
    page_size: pageSize,
  }
  if (params.status === 'pending') {
    query.status = 'pending'
  }

  const areaID = String(params.areaID || '').trim()
  const algorithmID = String(params.algorithmID || '').trim()
  const levelID = String(params.alarmLevelID || '').trim()
  const beginTime = String(params.beginTime || '').trim()
  const endTime = String(params.endTime || '').trim()

  if (areaID) query.area_id = areaID
  if (algorithmID) query.algorithm_id = algorithmID
  if (levelID) query.alarm_level_id = levelID
  if (beginTime) query.start_at = beginTime
  if (endTime) query.end_at = endTime

  return query
}

async function fetchOverview(): Promise<DashboardOverviewResponse> {
  return dashboardAPI.overview() as Promise<DashboardOverviewResponse>
}

export async function systemInfoApi(): Promise<SystemInfoData> {
  const overview = await fetchOverview()
  const runtime = overview.runtime || ({} as DashboardRuntime)
  return {
    version: String(runtime.version || '-'),
    uptimeSeconds: Math.max(0, Math.floor(toNumber(runtime.uptime_seconds))),
    cpuUsage: toNumber(runtime.cpu_percent),
    memUsage: toNumber(runtime.memory?.used_percent),
    diskUsage: toNumber(runtime.disk?.used_percent),
    netTxBps: toNumber(runtime.network?.tx_bps),
    netRxBps: toNumber(runtime.network?.rx_bps),
  }
}

export async function leftPanelApi(): Promise<LeftPanelData> {
  const [overview, levelRes] = await Promise.all([
    fetchOverview(),
    (alarmLevelAPI.list() as Promise<AlarmLevelListResponse>).catch(() => null),
  ])
  const channels = Array.isArray(overview.channels) ? overview.channels : []
  const areaDeviceCount = new Map<string, number>()

  channels.forEach((channel) => {
    const key = String(channel.area_id || '')
    areaDeviceCount.set(key, (areaDeviceCount.get(key) || 0) + 1)
  })

  const alarmAlgorithmList: AlgorithmStatItem[] = (overview.algorithm_stats || []).map((item) => ({
    algorithmID: String(item.algorithm_id || ''),
    algorithmName: String(item.algorithm_name || item.algorithm_id || '-'),
    alarmCount: toNumber(item.alarm_count),
  }))

  const levelCountMap = new Map<string, number>()
  const levelNameMap = new Map<string, string>()
  const levelColorMap = new Map<string, string>()

  ;(overview.level_stats || []).forEach((item) => {
    const levelID = String(item.alarm_level_id || '')
    if (!levelID) return
    levelCountMap.set(levelID, toNumber(item.alarm_count))
    levelNameMap.set(levelID, String(item.alarm_level_name || levelID || '-'))
    levelColorMap.set(levelID, String(item.alarm_level_color || ''))
  })

  const levelItems = Array.isArray(levelRes?.items) ? levelRes.items : []
  let alarmLevelList: AlarmLevelItem[] = []

  if (levelItems.length > 0) {
    alarmLevelList = levelItems
      .slice()
      .sort((a, b) => toNumber(a.severity) - toNumber(b.severity))
      .map((item) => {
        const levelID = String(item.id || '')
        return {
          levelID,
          levelName: String(item.name || levelNameMap.get(levelID) || levelID || '-'),
          levelColor: String(item.color || levelColorMap.get(levelID) || ''),
          alarmCount: levelCountMap.get(levelID) || 0,
          severity: Math.max(0, Math.floor(toNumber(item.severity))),
        }
      })
  } else {
    alarmLevelList = (overview.level_stats || [])
      .slice()
      .sort((a, b) => toNumber(b.alarm_count) - toNumber(a.alarm_count))
      .map((item, index) => ({
        levelID: String(item.alarm_level_id || ''),
        levelName: String(item.alarm_level_name || item.alarm_level_id || '-'),
        levelColor: String(item.alarm_level_color || ''),
        alarmCount: toNumber(item.alarm_count),
        severity: index + 1,
      }))
  }

  const alarmAreaList: AreaStatItem[] = (overview.area_stats || []).map((item) => {
    const areaID = String(item.area_id || '')
    return {
      areaID,
      areaName: String(item.area_name || item.area_id || '未分配区域'),
      deviceCount: areaDeviceCount.get(areaID) || 0,
      alarmCount: toNumber(item.alarm_count),
    }
  })

  return {
    alarmAlgorithmList,
    alarmLevelList,
    alarmAreaList,
  }
}

export async function deviceListApi(): Promise<DeviceItem[]> {
  const overview = await fetchOverview()
  const channels = Array.isArray(overview.channels) ? overview.channels : []

  return channels.map((item) => {
    const statusText = normalizeStatus(item.status)
    const streamWebrtc = String(item.play_webrtc_url || '')
    const streamWsFlv = String(item.play_ws_flv_url || '')
    const algorithms = Array.isArray(item.algorithms)
      ? item.algorithms
        .map((name) => String(name || '').trim())
        .filter(Boolean)
      : []

    return {
      id: String(item.id || ''),
      deviceName: String(item.name || '-'),
      deviceArea: String(item.area_name || item.area_id || '未分配区域'),
      areaID: String(item.area_id || ''),
      streamApp: String(item.app || ''),
      streamID: String(item.stream_id || ''),
      status: statusText === 'online' ? 1 : 0,
      alarming60s: Boolean(item.alarming_60s),
      todayAlarm: toNumber(item.today_alarm_count),
      totalAlarm: toNumber(item.total_alarm_count),
      bindingAlgorithm: algorithms[0] || '',
      algorithms,
      streamUrl: streamWebrtc || streamWsFlv,
      streamUrlWebrtc: streamWebrtc,
      streamUrlWsFlv: streamWsFlv,
    }
  })
}

export async function alarmListApi(params: AlarmQueryParams = {}): Promise<AlarmListResult> {
  const page = Math.max(1, Math.floor(toNumber(params.page || 1)))
  const pageSize = Math.max(1, Math.floor(toNumber(params.pageSize || 20)))
  const query = buildEventListQuery(params, page, pageSize)
  const res = await eventAPI.list(query) as EventListResponse

  const rawItems = Array.isArray(res.items) ? res.items : []
  let mapped = rawItems.map(mapEventItem)

  if (params.status === 'handled') {
    mapped = mapped.filter((item) => normalizeStatus(item.status) !== 'pending')
  }

  return {
    items: mapped,
    totalRaw: Math.max(0, Math.floor(toNumber(res.total))),
    page: Math.max(1, Math.floor(toNumber(res.page || page))),
    pageSize: Math.max(1, Math.floor(toNumber(res.page_size || pageSize))),
  }
}

export async function alarmDetailApi(id: string): Promise<AlarmItem> {
  const detail = await eventAPI.detail(id) as EventListItem
  return mapEventItem(detail)
}
