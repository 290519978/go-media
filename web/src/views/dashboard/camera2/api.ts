import { alarmLevelAPI, algorithmAPI, areaAPI, authAPI, dashboardAPI, eventAPI } from '@/api/modules'
import { appendTokenQuery } from '@/api/request'
import { normalizeLLMQuotaNotice, type LLMQuotaNotice } from '@/utils/llmQuotaNotice'

export type Camera2RangeType = 'today' | '7days' | 'custom'

export type Camera2OverviewQuery = {
  range: Camera2RangeType
  start_at?: string
  end_at?: string
}

export type Camera2OverviewResponse = {
  range: Camera2RangeType
  start_at: number
  end_at: number
  alarm_statistics: {
    total_alarm_count: number
    pending_count: number
    handling_rate: number
    false_alarm_rate: number
    high_count: number
    medium_count: number
    low_count: number
  }
  algorithm_statistics: {
    deploy_total: number
    running_total: number
    average_accuracy: number
    today_call_count: number
    items: Array<{
      algorithm_id: string
      algorithm_name: string
      alarm_count: number
      accuracy: number
    }>
  }
  device_statistics: {
    total_devices: number
    area_count: number
    online_devices: number
    online_rate: number
    alarm_devices: number
    offline_devices: number
    top_devices: Array<{
      device_id: string
      device_name: string
      area_id: string
      area_name: string
      alarm_count: number
    }>
  }
  analysis: {
    area_distribution: Array<{
      id: string
      name: string
      count: number
    }>
    type_distribution: Array<{
      id: string
      name: string
      count: number
    }>
    trend: Array<{
      label: string
      bucket_at: number
      alarm_count: number
    }>
    trend_unit: 'hour' | 'day'
  }
  resource_statistics: {
    cpu_percent: number
    memory_percent: number
    disk_percent: number
    network_status: string
    network_tx_bps: number
    network_rx_bps: number
    token_total_limit: number
    token_used: number
    token_remaining: number
    token_usage_rate: number
    estimated_remaining_days: number | null
  }
  generated_at: number
}

export type Camera2DashboardOverviewResponse = {
  channels?: Camera2ChannelEntity[]
}

export type Camera2ChannelEntity = {
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

export type Camera2AreaOption = {
  id: string
  name: string
}

export type Camera2AreaTreeNode = {
  id: string
  name: string
  parent_id?: string
  is_root?: boolean
  sort?: number
  children?: Camera2AreaTreeNode[]
}

export type Camera2AlgorithmOption = {
  id: string
  name: string
}

export type Camera2AlarmLevelOption = {
  id: string
  name: string
  severity: number
  color?: string
}

export type Camera2EventEntity = {
  id: string
  task_id?: string
  task_name?: string
  device_id: string
  device_name?: string
  algorithm_id: string
  event_source?: 'runtime' | 'patrol' | string
  display_name?: string
  prompt_text?: string
  algorithm_code?: string
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
  clip_ready?: boolean
  clip_path?: string
  clip_files_json?: string
  review_note?: string
  reviewed_by?: string
  reviewed_at?: string | number
  boxes_json?: string
  yolo_json?: string
  llm_json?: string
  snapshot_width?: number
  snapshot_height?: number
  source_callback?: string
}

export type Camera2DetectedBox = {
  label: string
  confidence: number
  x: number
  y: number
  w: number
  h: number
}

export type Camera2RealtimePlayerInfo = {
  deviceID: string
  deviceName: string
  areaName: string
  liveUrl: string
  streamApp: string
  streamID: string
}

export type Camera2ClipOption = {
  path: string
  url: string
  label: string
}

export type Camera2EventListResponse = {
  items?: Camera2EventEntity[]
  total?: number
  page?: number
  page_size?: number
}

export type Camera2RealtimeEventItem = {
  id: string
  image: string
  dateText: string
  timeText: string
  areaName: string
  algorithmName: string
  levelText: string
  levelClass: 'high' | 'middle' | 'low'
  statusText: string
  statusClass: 'pending' | 'resolved'
  raw: Camera2EventEntity
}

export type Camera2HistoryEventQuery = {
  page?: number
  page_size?: number
  source?: 'runtime' | 'patrol'
  area_id?: string
  algorithm_id?: string
  alarm_level_id?: string
  device_name?: string
}

export type Camera2PatrolCreatePayload = {
  device_ids: string[]
  algorithm_id?: string
  prompt?: string
}

export type Camera2PatrolJobItem = {
  device_id: string
  device_name: string
  status: string
  message: string
  event_id?: string
}

export type Camera2PatrolJobSnapshot = {
  job_id: string
  status: string
  total_count: number
  success_count: number
  failed_count: number
  alarm_count: number
  items: Camera2PatrolJobItem[]
}

export async function fetchCamera2Overview(query: Camera2OverviewQuery): Promise<Camera2OverviewResponse> {
  return dashboardAPI.camera2Overview(query) as Promise<Camera2OverviewResponse>
}

export async function fetchCamera2Profile(): Promise<{ username: string; llmQuotaNotice: LLMQuotaNotice | null }> {
  const response = await authAPI.me() as { username?: string; llm_quota_notice?: unknown }
  return {
    username: String(response.username || '-'),
    llmQuotaNotice: normalizeLLMQuotaNotice(response.llm_quota_notice),
  }
}

export async function fetchCamera2Areas(): Promise<Camera2AreaOption[]> {
  const response = await areaAPI.list() as { items?: Array<{ id: string; name: string }> }
  return Array.isArray(response.items)
    ? response.items.map((item) => ({
      id: String(item.id || ''),
      name: String(item.name || item.id || '未分配区域'),
    }))
    : []
}

export async function fetchCamera2AreaTree(): Promise<Camera2AreaTreeNode[]> {
  const response = await areaAPI.list() as { items?: Camera2AreaTreeNode[] }
  return Array.isArray(response.items) ? response.items : []
}

export async function fetchCamera2Algorithms(): Promise<Camera2AlgorithmOption[]> {
  const response = await algorithmAPI.list() as { items?: Array<{ id: string; name: string }> }
  return Array.isArray(response.items)
    ? response.items.map((item) => ({
      id: String(item.id || ''),
      name: String(item.name || item.id || '未命名算法'),
    }))
    : []
}

export async function fetchCamera2AlarmLevels(): Promise<Camera2AlarmLevelOption[]> {
  const response = await alarmLevelAPI.list() as { items?: Array<{ id: string; name: string; severity: number; color?: string }> }
  return Array.isArray(response.items)
    ? response.items.map((item) => ({
      id: String(item.id || ''),
      name: String(item.name || item.id || '未命名等级'),
      severity: Number(item.severity || 0),
      color: String(item.color || ''),
    }))
    : []
}

export async function fetchCamera2Channels(): Promise<Camera2ChannelEntity[]> {
  const response = await dashboardAPI.overview() as Camera2DashboardOverviewResponse
  return Array.isArray(response.channels) ? response.channels : []
}

export function resolveCamera2RealtimePlayer(
  channels: Camera2ChannelEntity[],
  deviceID?: string,
): Camera2RealtimePlayerInfo | null {
  const normalizedDeviceID = String(deviceID || '').trim()
  if (!normalizedDeviceID) {
    return null
  }
  const target = channels.find((item) => String(item.id || '').trim() === normalizedDeviceID)
  if (!target) {
    return null
  }
  return {
    deviceID: String(target.id || ''),
    deviceName: String(target.name || target.id || '未命名设备'),
    areaName: String(target.area_name || target.area_id || '未分配区域'),
    liveUrl: String(target.play_webrtc_url || target.play_ws_flv_url || ''),
    streamApp: String(target.app || ''),
    streamID: String(target.stream_id || ''),
  }
}

export async function fetchCamera2RealtimeEvents(source: 'runtime' | 'patrol' = 'runtime'): Promise<Camera2RealtimeEventItem[]> {
  const response = await eventAPI.list({ page: 1, page_size: 10, source }) as Camera2EventListResponse
  const items = Array.isArray(response.items) ? response.items : []
  return items.map(mapCamera2RealtimeEvent)
}

export async function fetchCamera2HistoryEvents(query: Camera2HistoryEventQuery): Promise<Camera2EventListResponse> {
  return eventAPI.list(query) as Promise<Camera2EventListResponse>
}

export async function fetchCamera2EventDetail(id: string): Promise<Camera2EventEntity> {
  return eventAPI.detail(id) as Promise<Camera2EventEntity>
}

export async function createCamera2PatrolJob(payload: Camera2PatrolCreatePayload): Promise<{ job_id: string; status: string; total_count: number }> {
  return dashboardAPI.camera2CreatePatrolJob(payload) as Promise<{ job_id: string; status: string; total_count: number }>
}

export async function fetchCamera2PatrolJob(jobID: string): Promise<Camera2PatrolJobSnapshot> {
  return dashboardAPI.camera2PatrolJob(jobID) as Promise<Camera2PatrolJobSnapshot>
}

export async function reviewCamera2Event(id: string, payload: { status: string; review_note: string }) {
  return eventAPI.review(id, payload)
}

export function mapCamera2RealtimeEvent(item: Camera2EventEntity): Camera2RealtimeEventItem {
  const { dateText, timeText } = splitCamera2DateTime(item.occurred_at)
  const level = mapCamera2Level(item.alarm_level_severity, item.alarm_level_name)
  const status = mapCamera2Status(item.status)
  return {
    id: String(item.id || ''),
    image: buildCamera2SnapshotURL(item.snapshot_path),
    dateText,
    timeText,
    areaName: String(item.area_name || item.area_id || '未分配区域'),
    algorithmName: String(item.display_name || item.algorithm_name || item.algorithm_id || '未知巡查'),
    levelText: level.text,
    levelClass: level.className,
    statusText: status.text,
    statusClass: status.className,
    raw: item,
  }
}

export function buildCamera2SnapshotURL(snapshotPath?: string): string {
  const normalized = String(snapshotPath || '').trim()
  if (!normalized) {
    return new URL('./assets/images/bg-page.png', import.meta.url).href
  }
  const encoded = normalized
    .split('/')
    .filter(Boolean)
    .map((segment) => encodeURIComponent(segment))
    .join('/')
  return appendTokenQuery(`/api/v1/events/image/${encoded}`)
}

export function buildCamera2ClipURL(eventID: string, clipPath?: string, clipFilesJSON?: string): string {
  const normalizedEventID = String(eventID || '').trim()
  if (!normalizedEventID) {
    return ''
  }
  const resolvedPath = resolveCamera2PlayableClipPath(clipPath, clipFilesJSON)
  return resolvedPath ? appendTokenQuery(eventAPI.clipFileURL(normalizedEventID, resolvedPath)) : ''
}

export function buildCamera2ClipOptions(eventID: string, clipPath?: string, clipFilesJSON?: string): Camera2ClipOption[] {
  const normalizedEventID = String(eventID || '').trim()
  if (!normalizedEventID) {
    return []
  }
  const pathList = [
    ...parseClipPathList(clipFilesJSON),
    normalizeCamera2ClipFilePath(clipPath),
  ].filter(Boolean)

  const uniquePaths = Array.from(new Set(pathList))
  return uniquePaths.map((path) => ({
    path,
    url: appendTokenQuery(eventAPI.clipFileURL(normalizedEventID, path)),
    label: clipLabelFromPath(path),
  }))
}

export function splitCamera2DateTime(value: string | number | undefined): { dateText: string; timeText: string } {
  const timestamp = parseCamera2Timestamp(value)
  if (timestamp <= 0) {
    return { dateText: '-', timeText: '--:--:--' }
  }
  const date = new Date(timestamp)
  const yyyy = date.getFullYear()
  const mm = String(date.getMonth() + 1).padStart(2, '0')
  const dd = String(date.getDate()).padStart(2, '0')
  const hh = String(date.getHours()).padStart(2, '0')
  const mi = String(date.getMinutes()).padStart(2, '0')
  const ss = String(date.getSeconds()).padStart(2, '0')
  return {
    dateText: `${yyyy}-${mm}-${dd}`,
    timeText: `${hh}:${mi}:${ss}`,
  }
}

export function formatCamera2DateTime(value: string | number | undefined): string {
  const { dateText, timeText } = splitCamera2DateTime(value)
  return `${dateText} ${timeText}`
}

export function parseCamera2Timestamp(value: string | number | undefined): number {
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : 0
  }
  if (!value) {
    return 0
  }
  const numeric = Number(value)
  if (Number.isFinite(numeric) && numeric > 0) {
    return numeric
  }
  const parsed = new Date(String(value)).getTime()
  return Number.isFinite(parsed) ? parsed : 0
}

export function mapCamera2Level(severity?: number, name?: string) {
  const numericSeverity = Number(severity || 0)
  // camera2 大屏展示口径固定为 1=高、2=中、3=低，避免和默认等级文案串位。
  if (numericSeverity <= 1 && numericSeverity > 0) {
    return { text: '高', className: 'high' as const }
  }
  if (numericSeverity === 2) {
    return { text: '中', className: 'middle' as const }
  }
  if (numericSeverity >= 3) {
    return { text: '低', className: 'low' as const }
  }
  const normalizedName = String(name || '')
  if (normalizedName.includes('高')) {
    return { text: '高', className: 'high' as const }
  }
  if (normalizedName.includes('中')) {
    return { text: '中', className: 'middle' as const }
  }
  return { text: '低', className: 'low' as const }
}

export function mapCamera2Status(status?: string) {
  const normalized = String(status || '').trim().toLowerCase()
  if (normalized === 'pending') {
    return { text: '待处理', className: 'pending' as const }
  }
  return { text: '已处理', className: 'resolved' as const }
}

export function formatCamera2Rate(value: number, fractionDigits = 1): string {
  const numeric = Number(value || 0)
  if (!Number.isFinite(numeric)) {
    return '0%'
  }
  const fixed = numeric.toFixed(fractionDigits)
  return `${fixed.replace(/\.0+$/, '').replace(/(\.\d*?)0+$/, '$1')}%`
}

export function formatCamera2Number(value: number): string {
  const numeric = Number(value || 0)
  if (!Number.isFinite(numeric)) {
    return '0'
  }
  return numeric.toLocaleString('zh-CN')
}

export function formatCamera2BPS(value: number): string {
  const numeric = Math.max(0, Number(value || 0))
  if (numeric >= 1024 * 1024) {
    return `${(numeric / 1024 / 1024).toFixed(1)} Mbps`
  }
  if (numeric >= 1024) {
    return `${(numeric / 1024).toFixed(1)} Kbps`
  }
  return `${numeric.toFixed(0)} bps`
}

export function parseCamera2Boxes(raw: string | undefined): Camera2DetectedBox[] {
  const list = safeParseJSON(raw)
  if (!Array.isArray(list)) {
    return []
  }
  return list.map(normalizeCamera2DetectedBox)
}

export function parseCamera2Conclusion(raw: string | undefined, taskCode?: string): string {
  const parsed = safeParseJSON(raw)
  if (!parsed || typeof parsed !== 'object') {
    return '暂无分析结论'
  }

  const matchedTaskReason = findCamera2TaskReason(parsed.task_results, taskCode)
  if (matchedTaskReason) {
    return matchedTaskReason
  }
  const nestedTaskReason = findCamera2TaskReason(parsed.result?.task_results, taskCode)
  return nestedTaskReason || '暂无分析结论'
}

export function parseCamera2PatrolConclusion(raw: string | undefined, taskCode?: string): string {
  const parsed = safeParseJSON(raw)
  if (!parsed || typeof parsed !== 'object') {
    return '暂无分析结论'
  }

  const directReason = normalizeCamera2Reason(parsed.reason)
  if (directReason) {
    return directReason
  }

  const nestedReason = normalizeCamera2Reason(parsed.result?.reason)
  if (nestedReason) {
    return nestedReason
  }

  const matchedTaskReason = findCamera2TaskReason(parsed.task_results, taskCode)
  if (matchedTaskReason) {
    return matchedTaskReason
  }

  const nestedTaskReason = findCamera2TaskReason(parsed.result?.task_results, taskCode)
  return nestedTaskReason || '暂无分析结论'
}

function resolveCamera2PlayableClipPath(clipPath?: string, clipFilesJSON?: string): string {
  // 报警片段实际播放应优先使用 clip_files_json 中的文件路径，clip_path 只是会话目录。
  const parsedPaths = parseClipPathList(clipFilesJSON)
  if (parsedPaths.length > 0) {
    return parsedPaths[0]
  }
  return normalizeCamera2ClipFilePath(clipPath)
}

function safeParseJSON(raw: string | undefined): any {
  const text = unwrapCamera2JSONText(raw)
  if (!text) {
    return null
  }
  try {
    const parsed = JSON.parse(text)
    if (typeof parsed === 'string') {
      const nestedText = unwrapCamera2JSONText(parsed)
      if (looksLikeJSONText(nestedText)) {
        return safeParseJSON(nestedText)
      }
    }
    return parsed
  } catch {
    return text
  }
}

function unwrapCamera2JSONText(raw: string | undefined): string {
  const normalized = String(raw || '').trim()
  if (!normalized) {
    return ''
  }
  const matched = normalized.match(/^```(?:json)?\s*([\s\S]*?)\s*```$/i)
  return matched?.[1]?.trim() || normalized
}

function parseClipPathList(raw: string | undefined): string[] {
  const parsed = safeParseJSON(raw)
  if (!Array.isArray(parsed)) {
    return []
  }
  return parsed
    .map((item) => normalizeCamera2ClipFilePath(item))
    .filter(Boolean)
}

function clipLabelFromPath(path: string): string {
  const normalized = String(path || '').trim()
  if (!normalized) {
    return '报警片段'
  }
  const parts = normalized.split('/').filter(Boolean)
  return parts[parts.length - 1] || normalized
}

function normalizeCamera2ClipFilePath(path: unknown): string {
  const normalized = String(path || '').trim()
  if (!normalized) {
    return ''
  }
  const fileName = normalized.split('/').filter(Boolean).pop() || ''
  return /\.(mp4|m4v|mov|webm|flv|avi)$/i.test(fileName) ? normalized : ''
}

function normalizeCamera2DetectedBox(item: any): Camera2DetectedBox {
  const direct = normalizeCamera2DirectBox(item)
  if (direct) {
    return direct
  }

  const bbox2d = Array.isArray(item?.bbox2d) ? item.bbox2d.map((value: unknown) => Number(value)) : []
  if (bbox2d.length === 4 && bbox2d.every((value: number) => Number.isFinite(value))) {
    const [x1, y1, x2, y2] = bbox2d
    const scale = Math.max(Math.abs(x1), Math.abs(y1), Math.abs(x2), Math.abs(y2)) > 1 ? 1000 : 1
    const left = clampCamera2BoxValue(Math.min(x1, x2) / scale)
    const right = clampCamera2BoxValue(Math.max(x1, x2) / scale)
    const top = clampCamera2BoxValue(Math.min(y1, y2) / scale)
    const bottom = clampCamera2BoxValue(Math.max(y1, y2) / scale)
    return {
      label: normalizeCamera2BoxLabel(item),
      confidence: normalizeCamera2BoxConfidence(item),
      x: clampCamera2BoxValue((left + right) / 2),
      y: clampCamera2BoxValue((top + bottom) / 2),
      w: clampCamera2BoxValue(right - left),
      h: clampCamera2BoxValue(bottom - top),
    }
  }

  return {
    label: normalizeCamera2BoxLabel(item),
    confidence: normalizeCamera2BoxConfidence(item),
    x: 0,
    y: 0,
    w: 0,
    h: 0,
  }
}

function normalizeCamera2DirectBox(item: any): Camera2DetectedBox | null {
  const x = Number(item?.x)
  const y = Number(item?.y)
  const w = Number(item?.w)
  const h = Number(item?.h)
  if (![x, y, w, h].every((value) => Number.isFinite(value))) {
    return null
  }
  return {
    label: normalizeCamera2BoxLabel(item),
    confidence: normalizeCamera2BoxConfidence(item),
    x: clampCamera2BoxValue(x),
    y: clampCamera2BoxValue(y),
    w: clampCamera2BoxValue(w),
    h: clampCamera2BoxValue(h),
  }
}

function normalizeCamera2BoxLabel(item: any): string {
  return String(item?.label || item?.name || item?.task_code || '-')
}

function normalizeCamera2BoxConfidence(item: any): number {
  const numeric = Number(item?.confidence ?? item?.score ?? 0)
  if (!Number.isFinite(numeric)) {
    return 0
  }
  return Math.min(Math.max(numeric, 0), 1)
}

function clampCamera2BoxValue(value: number): number {
  if (!Number.isFinite(value)) {
    return 0
  }
  if (value < 0) {
    return 0
  }
  if (value > 1) {
    return 1
  }
  return value
}

function normalizeCamera2Reason(value: unknown): string {
  const normalized = String(value || '').trim()
  if (!normalized || looksLikeJSONText(normalized)) {
    return ''
  }
  return normalized
}

function findCamera2TaskReason(taskResults: any, taskCode?: string): string {
  if (typeof taskResults === 'string') {
    return findCamera2TaskReason(safeParseJSON(taskResults), taskCode)
  }
  if (taskResults && typeof taskResults === 'object' && !Array.isArray(taskResults)) {
    const directReason = String(taskResults.reason || '').trim()
    return directReason && !looksLikeJSONText(directReason) ? directReason : ''
  }
  if (!Array.isArray(taskResults)) {
    return ''
  }
  const normalizedTaskCode = String(taskCode || '').trim().toUpperCase()
  if (normalizedTaskCode) {
    const exactMatch = taskResults.find((item) => {
      const itemTaskCode = String(item?.task_code || '').trim().toUpperCase()
      return itemTaskCode === normalizedTaskCode && typeof item?.reason === 'string' && item.reason.trim()
    })
    if (exactMatch?.reason) {
      return String(exactMatch.reason).trim()
    }
  }

  const alarmMatch = taskResults.find((item) => isCamera2AlarmTask(item?.alarm) && typeof item?.reason === 'string' && item.reason.trim())
  if (alarmMatch?.reason) {
    return String(alarmMatch.reason).trim()
  }

  const fallbackMatch = taskResults.find((item) => typeof item?.reason === 'string' && item.reason.trim())
  return fallbackMatch?.reason ? String(fallbackMatch.reason).trim() : ''
}

function isCamera2AlarmTask(value: unknown): boolean {
  if (typeof value === 'boolean') {
    return value
  }
  if (typeof value === 'number') {
    return value !== 0
  }
  const normalized = String(value ?? '').trim().toLowerCase()
  return normalized === '1' || normalized === 'true' || normalized === 'yes'
}

function looksLikeJSONText(text: string): boolean {
  const normalized = String(text || '').trim()
  if (!normalized) {
    return false
  }
  return (
    (normalized.startsWith('{') && normalized.endsWith('}')) ||
    (normalized.startsWith('[') && normalized.endsWith(']'))
  )
}
