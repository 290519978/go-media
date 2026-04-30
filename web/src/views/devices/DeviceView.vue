<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import { areaAPI, deviceAPI } from '@/api/modules'
import { appendTokenQuery } from '@/api/request'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/datetime'
import GB28181View from '@/views/devices/GB28181View.vue'
import JessibucaPlayer from '@/components/JessibucaPlayer.vue'

type Area = { id: string; name: string }

type MediaSource = {
  id: string
  name: string
  area_id: string
  source_type: 'gb28181' | 'pull' | 'push' | string
  row_kind: 'device' | 'channel' | string
  parent_id: string
  protocol: string
  transport: string
  app: string
  stream_id: string
  stream_url: string
  status: string
  ai_status: string
  output_config: string
  play_webrtc_url?: string
  play_ws_flv_url?: string
  play_http_flv_url?: string
  play_hls_url?: string
  play_rtsp_url?: string
  play_rtmp_url?: string
  snapshot_url?: string
  children?: MediaSource[]
}

type GBDeviceStatus = {
  device_id: string
  status: string
}

type PreviewProtocol = 'ws_flv' | 'webrtc'
type RecordingKind = 'alarm'

type RecordingFile = {
  name: string
  size: number
  mod_time: string
  path: string
  kind?: RecordingKind
  event_id?: string
  event_occurred_at?: string
}

type GBDeviceBlock = {
  device_id: string
  reason: string
  created_at: string
  updated_at: string
}

type RTMPStreamBlock = {
  key: string
  app: string
  stream_id: string
  reason: string
  created_at: string
  updated_at: string
}

const gbIDReg = /^\d{20}$/

const loading = ref(false)
const sources = ref<MediaSource[]>([])
const areas = ref<Area[]>([])
const gbStatusByDeviceID = ref<Record<string, string>>({})
const authStore = useAuthStore()
const isDevelopmentMode = computed(() => authStore.developmentMode)

const rtspModalOpen = ref(false)
const rtmpModalOpen = ref(false)
const editModalOpen = ref(false)
const gbModalOpen = ref(false)
const previewOpen = ref(false)
const blockModalOpen = ref(false)
const blockLoading = ref(false)
const rtspSubmitting = ref(false)
const editSubmitting = ref(false)

const editingID = ref('')
const editingSourceType = ref<'gb28181' | 'pull' | 'push' | ''>('')
const gbBlocks = ref<GBDeviceBlock[]>([])
const rtmpBlocks = ref<RTMPStreamBlock[]>([])

const rtspForm = reactive({
  name: '',
  area_id: modelRootArea(),
  origin_url: '',
  transport: 'tcp',
})

const rtmpForm = reactive({
  name: '',
  area_id: modelRootArea(),
  app: 'live',
  stream_id: '',
  publish_token: '',
})

const editForm = reactive({
  name: '',
  area_id: modelRootArea(),
  transport: 'tcp',
  origin_url: '',
  app: 'live',
  stream_id: '',
  publish_token: '',
})

const gbBlockForm = reactive({
  device_id: '',
  reason: '',
})

const rtmpBlockForm = reactive({
  app: 'live',
  stream_id: '',
  reason: '',
})

const previewLoading = ref(false)
const snapshotLoadingID = ref('')
const previewProtocol = ref<PreviewProtocol>('webrtc')
const previewDevice = reactive({
  id: '',
  name: '',
  app: '',
  stream_id: '',
})
const previewOutput = reactive({
  webrtc: '',
  ws_flv: '',
  http_flv: '',
  hls: '',
  rtsp: '',
  rtmp: '',
})

const recordingsOpen = ref(false)
const recordingsLoading = ref(false)
const recordingsExporting = ref(false)
const recordings = ref<RecordingFile[]>([])
const recordingKind = ref<RecordingKind>('alarm')
const selectedRecordingPaths = ref<string[]>([])
const recordingDevice = reactive({
  id: '',
  name: '',
})
const recordingPager = reactive({
  page: 1,
  page_size: 10,
  total: 0,
  total_pages: 0,
  total_size: 0,
  flash_safe_policy: '',
})

function modelRootArea() {
  return 'root'
}

const areaMap = computed(() => {
  const map = new Map<string, string>()
  for (const area of areas.value) {
    map.set(area.id, area.name)
  }
  return map
})

const areaOptions = computed(() => areas.value.map((item) => ({ label: item.name, value: item.id })))

const sourceRows = computed(() => buildSourceTree(sources.value || []))

const previewProtocolOptions = computed(() => {
  const options: Array<{ label: string; value: PreviewProtocol }> = []
  if (previewOutput.webrtc) options.push({ label: 'WebRTC', value: 'webrtc' })
  if (previewOutput.ws_flv) options.push({ label: 'WS-FLV', value: 'ws_flv' })
  return options
})

const previewPlayURL = computed(() => {
  const bucket: Record<PreviewProtocol, string> = {
    webrtc: previewOutput.webrtc,
    ws_flv: previewOutput.ws_flv,
  }
  const selected = String(bucket[previewProtocol.value] || '').trim()
  if (selected) return selected
  return String(previewOutput.webrtc || '').trim() || String(previewOutput.ws_flv || '').trim()
})

const recordingRowSelection = computed(() => ({
  selectedRowKeys: selectedRecordingPaths.value,
  onChange: (keys: (string | number)[]) => {
    selectedRecordingPaths.value = keys.map((key) => String(key || '').trim()).filter(Boolean)
  },
}))

function buildSourceTree(items: MediaSource[]): MediaSource[] {
  const sorted = [...(items || [])].sort((a, b) => {
    if (a.row_kind !== b.row_kind) {
      if (a.row_kind === 'device') return -1
      if (b.row_kind === 'device') return 1
    }
    return String(a.name || '').localeCompare(String(b.name || ''), 'zh-CN')
  })
  const map = new Map<string, MediaSource>()
  const roots: MediaSource[] = []
  for (const item of sorted) {
    map.set(item.id, { ...item })
  }
  for (const item of sorted) {
    const node = map.get(item.id)!
    const parentID = String(item.parent_id || '').trim()
    if (parentID && map.has(parentID)) {
      const parent = map.get(parentID)!
      if (!Array.isArray(parent.children)) {
        parent.children = []
      }
      parent.children.push(node)
    } else {
      roots.push(node)
    }
  }
  return roots
}

function canExpandRow(item: MediaSource) {
  return String(item.row_kind || '').toLowerCase() === 'device' && Array.isArray(item.children) && item.children.length > 0
}

function statusText(v: string) {
  if (v === 'online') return '在线'
  if (v === 'offline') return '离线'
  return v || '-'
}

function isGBDeviceRow(item: MediaSource) {
  return String(item.source_type || '').trim().toLowerCase() === 'gb28181' &&
    String(item.row_kind || '').trim().toLowerCase() === 'device'
}

function isGBChannel(item: MediaSource) {
  return String(item.source_type || '').trim().toLowerCase() === 'gb28181' &&
    String(item.row_kind || '').trim().toLowerCase() === 'channel'
}

function resolveGBDeviceStatus(item: MediaSource) {
  if (!isGBDeviceRow(item) && !isGBChannel(item)) {
    return String(item.status || '').trim().toLowerCase()
  }
  const deviceID = String(resolveGBDeviceID(item) || '').trim()
  if (!gbIDReg.test(deviceID)) {
    return String(item.status || '').trim().toLowerCase() || 'unknown'
  }
  const status = String(gbStatusByDeviceID.value[deviceID] || '').trim().toLowerCase()
  if (status === 'online' || status === 'offline') return status
  return String(item.status || '').trim().toLowerCase() || 'unknown'
}

function sipStatusText(v: string) {
  if (v === 'online') return 'SIP在线'
  if (v === 'offline') return 'SIP离线'
  return 'SIP未知'
}

function sipStatusColor(v: string) {
  if (v === 'online') return 'cyan'
  if (v === 'offline') return 'default'
  return 'blue'
}

function aiStatusText(v: string) {
  if (v === 'running') return '运行中'
  if (v === 'stopped') return '已停止'
  if (v === 'error') return '异常'
  if (v === 'idle') return '空闲'
  return v || '-'
}

function canPreview(item: MediaSource) {
  return String(item.row_kind || '').toLowerCase() === 'channel'
}

function canSnapshot(item: MediaSource) {
  return canPreview(item) && String(item.status || '').toLowerCase() === 'online'
}

function canManageRecordings(item: MediaSource) {
  return String(item.row_kind || '').toLowerCase() === 'channel'
}

function canDelete(item: MediaSource) {
  const sourceType = String(item.source_type || '').toLowerCase()
  if (sourceType === 'pull' || sourceType === 'push') return true
  if (sourceType === 'gb28181' && String(item.row_kind || '').toLowerCase() === 'device') return true
  return false
}

function resolveGBDeviceID(item: MediaSource) {
  if (!item) return ''
  const streamURL = String(item.stream_url || '').trim()
  if (streamURL.toLowerCase().startsWith('gb28181://')) {
    const path = streamURL.substring('gb28181://'.length)
    const first = String(path.split('/').filter(Boolean)[0] || '').trim()
    if (gbIDReg.test(first)) return first
  }
  const streamID = String(item.stream_id || '').trim()
  if (gbIDReg.test(streamID)) return streamID

  const output = parseOutputConfig(item.output_config)
  const outputDeviceID = String(output.gb_device_id || '').trim()
  if (gbIDReg.test(outputDeviceID)) return outputDeviceID

  const parentID = String(item.parent_id || '').trim()
  if (parentID) {
    const parent = sources.value.find((src) => String(src.id || '').trim() === parentID)
    const parentStreamID = String(parent?.stream_id || '').trim()
    if (gbIDReg.test(parentStreamID)) return parentStreamID
  }
  return ''
}

function deleteConfirmText(item: MediaSource) {
  const sourceType = String(item.source_type || '').toLowerCase()
  if (sourceType === 'gb28181') return '确认删除该 GB28181 设备及其通道？'
  return '确认删除该通道？'
}

function parseOutputConfig(raw: string) {
  const text = String(raw || '').trim()
  if (!text) return {} as Record<string, unknown>
  try {
    const parsed = JSON.parse(text)
    if (parsed && typeof parsed === 'object') {
      return parsed as Record<string, unknown>
    }
    return {} as Record<string, unknown>
  } catch {
    return {} as Record<string, unknown>
  }
}

function parsePublishTokenFromOutput(raw: string) {
  const payload = parseOutputConfig(raw)
  return String(payload.publish_token || '')
}

function resolveSnapshotURL(raw: string) {
  const value = String(raw || '').trim()
  if (!value) return ''
  if (/^(data|blob):/i.test(value)) return value
  if (/^https?:\/\//i.test(value)) {
    const origin = window.location.origin
    const apiBase = String(import.meta.env.VITE_API_BASE_URL || '').trim()
    if (value.startsWith(`${origin}/api/`)) return appendTokenQuery(value)
    if (apiBase && value.startsWith(`${apiBase}/api/`)) return appendTokenQuery(value)
    return value
  }
  if (value.startsWith('/api/')) return appendTokenQuery(value)
  return value
}

function isValidRTSPURL(raw: string) {
  const value = String(raw || '').trim()
  if (!/^rtsp:\/\//i.test(value)) return false
  try {
    const parsed = new URL(value)
    return Boolean(parsed.hostname)
  } catch {
    return false
  }
}

function bytesToText(size: number) {
  if (!Number.isFinite(size) || size <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = size
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  return `${value.toFixed(index === 0 ? 0 : 2)} ${units[index]}`
}

async function loadAll() {
  loading.value = true
  try {
    const gbDevicePromise = deviceAPI.gbDevices() as Promise<{ items: GBDeviceStatus[] }>
    const [deviceResp, areaResp] = await Promise.all([
      deviceAPI.list() as Promise<{ items: MediaSource[] }>,
      areaAPI.list() as Promise<{ flat: Area[] }>,
    ])
    const gbDeviceResp = await gbDevicePromise.catch(() => ({ items: [] as GBDeviceStatus[] }))
    const gbDeviceStatusMap: Record<string, string> = {}
    for (const item of gbDeviceResp.items || []) {
      const deviceID = String(item.device_id || '').trim()
      if (!gbIDReg.test(deviceID)) continue
      gbDeviceStatusMap[deviceID] = String(item.status || '').trim().toLowerCase()
    }
    sources.value = deviceResp.items || []
    areas.value = areaResp.flat || []
    gbStatusByDeviceID.value = gbDeviceStatusMap
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}

function resetBlockForms() {
  Object.assign(gbBlockForm, {
    device_id: '',
    reason: '',
  })
  Object.assign(rtmpBlockForm, {
    app: 'live',
    stream_id: '',
    reason: '',
  })
}

function timeText(v: string) {
  return formatDateTime(v)
}

async function loadSourceBlocks() {
  blockLoading.value = true
  try {
    const data = await deviceAPI.blacklist() as {
      gb_devices: GBDeviceBlock[]
      rtmp_streams: RTMPStreamBlock[]
    }
    gbBlocks.value = data.gb_devices || []
    rtmpBlocks.value = (data.rtmp_streams || []).map((item) => ({
      ...item,
      key: `${item.app}/${item.stream_id}`,
    }))
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    blockLoading.value = false
  }
}

async function openBlockManager() {
  resetBlockForms()
  blockModalOpen.value = true
  await loadSourceBlocks()
}

async function submitAddGBBlock() {
  const deviceID = String(gbBlockForm.device_id || '').trim()
  if (!gbIDReg.test(deviceID)) {
    message.error('GB 设备ID必须是20位数字编码')
    return
  }
  try {
    await deviceAPI.addGBBlacklist({
      device_id: deviceID,
      reason: String(gbBlockForm.reason || '').trim(),
    })
    message.success('GB 设备已加入黑名单')
    gbBlockForm.device_id = ''
    gbBlockForm.reason = ''
    await Promise.all([loadSourceBlocks(), loadAll()])
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function submitAddRTMPBlock() {
  const streamID = String(rtmpBlockForm.stream_id || '').trim()
  if (!streamID) {
    message.error('请输入 RTMP Stream ID')
    return
  }
  try {
    await deviceAPI.addRTMPBlacklist({
      app: String(rtmpBlockForm.app || '').trim(),
      stream_id: streamID,
      reason: String(rtmpBlockForm.reason || '').trim(),
    })
    message.success('RTMP 流已加入黑名单')
    rtmpBlockForm.stream_id = ''
    rtmpBlockForm.reason = ''
    await Promise.all([loadSourceBlocks(), loadAll()])
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function removeGBBlock(deviceID: string) {
  try {
    await deviceAPI.removeGBBlacklist(deviceID)
    message.success('已移出 GB 黑名单')
    await loadSourceBlocks()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function removeRTMPBlock(app: string, streamID: string) {
  try {
    await deviceAPI.removeRTMPBlacklist(app, streamID)
    message.success('已移出 RTMP 黑名单')
    await loadSourceBlocks()
  } catch (err) {
    message.error((err as Error).message)
  }
}

function openCreateRTSP() {
  Object.assign(rtspForm, {
    name: '',
    area_id: modelRootArea(),
    origin_url: '',
    transport: 'tcp',
  })
  rtspModalOpen.value = true
}

function openCreateRTMP() {
  Object.assign(rtmpForm, {
    name: '',
    area_id: modelRootArea(),
    app: 'live',
    stream_id: `stream_${Date.now()}`,
    publish_token: '',
  })
  rtmpModalOpen.value = true
}

async function submitCreateRTSP() {
  if (rtspSubmitting.value) return
  if (!rtspForm.name.trim()) {
    message.error('请输入名称')
    return
  }
  if (!rtspForm.area_id.trim()) {
    message.error('请选择区域')
    return
  }
  if (!isValidRTSPURL(rtspForm.origin_url)) {
    message.error('RTSP 地址格式错误，请使用 rtsp:// 并包含主机地址')
    return
  }
  // RTSP 保存会同步等待 ZLM 建流返回，提交期间必须锁定弹窗，避免重复点击触发多次请求。
  rtspSubmitting.value = true
  try {
    await deviceAPI.create({
      source_type: 'pull',
      name: rtspForm.name.trim(),
      area_id: rtspForm.area_id.trim(),
      origin_url: rtspForm.origin_url.trim(),
      transport: rtspForm.transport,
    })
    message.success('RTSP 拉流已创建')
    rtspModalOpen.value = false
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    rtspSubmitting.value = false
  }
}

async function submitCreateRTMP() {
  if (!rtmpForm.name.trim()) {
    message.error('请输入名称')
    return
  }
  if (!rtmpForm.app.trim() || !rtmpForm.stream_id.trim()) {
    message.error('请输入 app 和 stream_id')
    return
  }
  try {
    await deviceAPI.create({
      source_type: 'push',
      name: rtmpForm.name.trim(),
      area_id: (rtmpForm.area_id || modelRootArea()).trim(),
      app: rtmpForm.app.trim(),
      stream_id: rtmpForm.stream_id.trim(),
      publish_token: rtmpForm.publish_token.trim(),
    })
    message.success('RTMP 推流通道已创建')
    rtmpModalOpen.value = false
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function openEdit(row: MediaSource) {
  try {
    const detail = await deviceAPI.detail(row.id) as MediaSource
    editingID.value = detail.id
    editingSourceType.value = String(detail.source_type || '') as 'gb28181' | 'pull' | 'push'
    Object.assign(editForm, {
      name: detail.name,
      area_id: detail.area_id || modelRootArea(),
      transport: detail.transport || 'tcp',
      origin_url: detail.stream_url || '',
      app: detail.app || 'live',
      stream_id: detail.stream_id || '',
      publish_token: parsePublishTokenFromOutput(detail.output_config),
    })
    editModalOpen.value = true
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function submitEdit() {
  if (editSubmitting.value) return
  if (!editingID.value) return
  if (!editForm.name.trim()) {
    message.error('请输入名称')
    return
  }
  if (!editForm.area_id.trim()) {
    message.error('请选择区域')
    return
  }
  if (editingSourceType.value === 'pull' && !isValidRTSPURL(editForm.origin_url)) {
    message.error('RTSP 地址格式错误，请使用 rtsp:// 并包含主机地址')
    return
  }
  const payload: Record<string, unknown> = {
    name: editForm.name.trim(),
    area_id: editForm.area_id.trim(),
  }
  if (editingSourceType.value === 'pull') {
    payload.origin_url = editForm.origin_url.trim()
    payload.transport = editForm.transport
  }
  if (editingSourceType.value === 'push') {
    payload.app = editForm.app.trim()
    payload.stream_id = editForm.stream_id.trim()
    payload.publish_token = editForm.publish_token.trim()
  }
  // 编辑 RTSP 时同样会等待后端完成建流校验，这里统一锁定保存态，避免用户连续点击。
  editSubmitting.value = true
  try {
    await deviceAPI.update(editingID.value, payload)
    message.success('保存成功')
    editModalOpen.value = false
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    editSubmitting.value = false
  }
}

async function removeSource(item: MediaSource) {
  try {
    const sourceType = String(item.source_type || '').toLowerCase()
    if (sourceType === 'gb28181') {
      const deviceID = resolveGBDeviceID(item)
      if (!gbIDReg.test(deviceID)) {
        message.error('无法解析 GB28181 设备ID，请到 GB28181 维护页删除')
        return
      }
      await deviceAPI.deleteGBDevice(deviceID)
      message.success('GB28181 设备已删除')
    } else {
      await deviceAPI.remove(item.id)
      message.success('删除成功')
    }
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function captureSnapshot(row: MediaSource) {
  if (!canSnapshot(row)) {
    message.warning('仅在线通道支持抓拍')
    return
  }
  snapshotLoadingID.value = row.id
  try {
    const data = await deviceAPI.snapshot(row.id) as { snapshot_url?: string }
    const snapshotURL = String(data?.snapshot_url || '').trim()
    row.snapshot_url = snapshotURL
    if (snapshotURL) {
      message.success('抓拍成功')
    } else {
      message.warning('抓拍成功，但未返回快照地址')
    }
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    snapshotLoadingID.value = ''
  }
}

function resetPreviewOutput() {
  previewOutput.webrtc = ''
  previewOutput.ws_flv = ''
  previewOutput.http_flv = ''
  previewOutput.hls = ''
  previewOutput.rtsp = ''
  previewOutput.rtmp = ''
}

function applyPreviewOutput(output: Record<string, unknown>) {
  previewOutput.webrtc = String(output.webrtc || output.rtc || output.webrtc_url || '')
  previewOutput.ws_flv = String(output.ws_flv || output.wsFlv || output['ws-flv'] || output.ws_url || '')
  previewOutput.http_flv = String(output.http_flv || output.flv || output.flv_url || '')
  previewOutput.hls = String(output.hls || output.hls_fmp4 || output.hls_url || output.m3u8 || '')
  previewOutput.rtsp = String(output.rtsp || output.rtsp_url || '')
  previewOutput.rtmp = String(output.rtmp || output.rtmp_url || '')
}

function applyPreviewOutputFromSource(source: MediaSource) {
  previewOutput.webrtc = String(source.play_webrtc_url || '')
  previewOutput.ws_flv = String(source.play_ws_flv_url || '')
  previewOutput.http_flv = String(source.play_http_flv_url || '')
  previewOutput.hls = String(source.play_hls_url || '')
  previewOutput.rtsp = String(source.play_rtsp_url || '')
  previewOutput.rtmp = String(source.play_rtmp_url || '')
}

function pickPreviewProtocol(playURL: string) {
  const url = String(playURL || '').trim()
  if (url && url === previewOutput.webrtc) {
    previewProtocol.value = 'webrtc'
    return
  }
  if (url && url === previewOutput.ws_flv) {
    previewProtocol.value = 'ws_flv'
    return
  }
  if (previewProtocolOptions.value.length > 0) {
    previewProtocol.value = previewProtocolOptions.value[0].value
  }
}

async function refreshPreview() {
  if (!previewDevice.id) return
  previewLoading.value = true
  try {
    const result = await deviceAPI.preview(previewDevice.id) as {
      play_url: string
      output_config: Record<string, unknown>
    }
    resetPreviewOutput()
    applyPreviewOutput(result.output_config || {})
    pickPreviewProtocol(result.play_url)
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    previewLoading.value = false
  }
}

async function openPreview(row: MediaSource) {
  if (!canPreview(row)) {
    message.warning('仅通道行支持预览')
    return
  }
  previewDevice.id = row.id
  previewDevice.name = row.name
  previewDevice.app = String(row.app || '')
  previewDevice.stream_id = String(row.stream_id || '')
  resetPreviewOutput()
  applyPreviewOutputFromSource(row)
  previewOpen.value = true
  await refreshPreview()
}

function closePreview() {
  previewOpen.value = false
  previewDevice.id = ''
  previewDevice.name = ''
  previewDevice.app = ''
  previewDevice.stream_id = ''
  resetPreviewOutput()
}

function normalizeRecordingKind(raw: string): RecordingKind {
  void raw
  return 'alarm'
}

function parseDownloadFilenameFromDisposition(raw: string, fallback: string) {
  const text = String(raw || '').trim()
  if (!text) return fallback
  const utf8Match = text.match(/filename\*\s*=\s*UTF-8''([^;]+)/i)
  if (utf8Match && utf8Match[1]) {
    try {
      return decodeURIComponent(utf8Match[1].trim())
    } catch {
      return fallback
    }
  }
  const plainMatch = text.match(/filename\s*=\s*\"?([^\";]+)\"?/i)
  if (plainMatch && plainMatch[1]) {
    return String(plainMatch[1]).trim() || fallback
  }
  return fallback
}

function resetRecordingSelection() {
  selectedRecordingPaths.value = []
}

async function loadRecordings(page = recordingPager.page) {
  if (!recordingDevice.id) return
  recordingsLoading.value = true
  try {
    const data = await deviceAPI.recordings(recordingDevice.id, {
      page,
      page_size: recordingPager.page_size,
      kind: recordingKind.value,
    }) as {
      items: RecordingFile[]
      total: number
      page: number
      page_size: number
      total_pages: number
      total_size: number
      kind?: RecordingKind
      flash_safe_policy: string
    }
    recordings.value = (data.items || []).map((item) => ({
      ...item,
      kind: normalizeRecordingKind(item.kind || recordingKind.value),
    }))
    recordingKind.value = normalizeRecordingKind(String(data.kind || recordingKind.value))
    recordingPager.total = data.total || 0
    recordingPager.page = data.page || 1
    recordingPager.page_size = data.page_size || 10
    recordingPager.total_pages = data.total_pages || 0
    recordingPager.total_size = data.total_size || 0
    recordingPager.flash_safe_policy = data.flash_safe_policy || ''
    resetRecordingSelection()
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    recordingsLoading.value = false
  }
}

async function openRecordings(row: MediaSource) {
  recordingDevice.id = row.id
  recordingDevice.name = row.name
  recordingKind.value = 'alarm'
  recordingPager.page = 1
  resetRecordingSelection()
  recordingsOpen.value = true
  await loadRecordings(1)
}

async function deleteRecordings(paths: string[]) {
  const selectedPaths = Array.from(new Set((paths || []).map((path) => String(path || '').trim()).filter(Boolean)))
  if (selectedPaths.length === 0) {
    message.warning('请先选择报警片段')
    return
  }
  try {
    const data = await deviceAPI.deleteRecordings(recordingDevice.id, selectedPaths) as {
      removed?: string[]
      failed?: Array<{ path: string; reason: string }>
      summary?: {
        total?: number
        removed?: number
        failed?: number
      }
    }
    const removedCount = Number(data?.summary?.removed ?? data?.removed?.length ?? 0)
    const failedCount = Number(data?.summary?.failed ?? data?.failed?.length ?? 0)
    if (removedCount > 0 && failedCount > 0) {
      message.warning(`已删除 ${removedCount} 个文件，失败 ${failedCount} 个`)
    } else if (removedCount > 0) {
      message.success(`已删除 ${removedCount} 个文件`)
    } else if (failedCount > 0) {
      message.error(`删除失败 ${failedCount} 个文件`)
    } else {
      message.warning('未删除任何文件')
    }
    resetRecordingSelection()
    await loadRecordings(recordingPager.page)
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function deleteSelectedRecordings() {
  await deleteRecordings(selectedRecordingPaths.value)
}

async function exportSelectedRecordings() {
  const selectedPaths = Array.from(new Set(selectedRecordingPaths.value.map((path) => String(path || '').trim()).filter(Boolean)))
  if (selectedPaths.length === 0) {
    message.warning('请先选择要导出的录制文件')
    return
  }
  if (!recordingDevice.id) return
  recordingsExporting.value = true
  try {
    const token = localStorage.getItem('mb_token')
    const resp = await fetch(deviceAPI.recordingsExportURL(recordingDevice.id), {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ paths: selectedPaths }),
    })
    if (!resp.ok) {
      let msg = `导出失败 (${resp.status})`
      try {
        const payload = await resp.json() as { msg?: string }
        if (payload?.msg) msg = payload.msg
      } catch {
        // ignore parse error
      }
      throw new Error(msg)
    }
    const blob = await resp.blob()
    const objectURL = URL.createObjectURL(blob)
    const fallbackName = `recordings_${recordingDevice.id || 'device'}_${Date.now()}.zip`
    const fileName = parseDownloadFilenameFromDisposition(resp.headers.get('content-disposition') || '', fallbackName)
    const anchor = document.createElement('a')
    anchor.href = objectURL
    anchor.download = fileName
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(objectURL)
    message.success(`已导出 ${selectedPaths.length} 个文件`)
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    recordingsExporting.value = false
  }
}

async function downloadRecording(item: RecordingFile) {
  try {
    const token = localStorage.getItem('mb_token')
    const url = deviceAPI.recordingFileURL(recordingDevice.id, item.path)
    const res = await fetch(url, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    if (!res.ok) {
      throw new Error(`下载失败 (${res.status})`)
    }
    const blob = await res.blob()
    const objectURL = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = objectURL
    anchor.download = item.name || 'recording.mp4'
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(objectURL)
  } catch (err) {
    message.error((err as Error).message)
  }
}

onMounted(loadAll)
</script>

<template>
  <div>
    <h2 class="page-title">摄像头配置</h2>
    <p class="page-subtitle">统一展示媒体主表，按协议分入口维护 RTSP 拉流、RTMP 推流和 GB28181 通道。</p>

    <a-card class="glass-card">
      <div class="table-toolbar">
        <a-space>
          <a-button type="primary" @click="openCreateRTSP">新增RTSP拉流</a-button>
          <a-button type="primary" ghost @click="openCreateRTMP">新增RTMP推流</a-button>
          <a-button @click="gbModalOpen = true">GB28181维护</a-button>
          <a-button @click="openBlockManager">黑名单管理</a-button>
        </a-space>
        <a-button @click="loadAll">刷新</a-button>
      </div>

      <a-table
        :data-source="sourceRows"
        :loading="loading"
        row-key="id"
        :pagination="{ pageSize: 10 }"
        :expandable="{ rowExpandable: canExpandRow }"
        :scroll="{ x: 1260 }"
      >
        <a-table-column title="名称" data-index="name" width="220">
          <template #default="{ record }">
            <a-space>
              <span>{{ record.name }}</span>
              <a-tag v-if="record.source_type === 'gb28181'" color="blue">GB</a-tag>
            </a-space>
          </template>
        </a-table-column>
        <a-table-column title="快照" width="96">
          <template #default="{ record }">
            <a-image
              v-if="record.snapshot_url"
              :src="resolveSnapshotURL(record.snapshot_url)"
              :width="68"
              :height="44"
              style="object-fit: cover; border-radius: 6px"
            />
            <span v-else>-</span>
          </template>
        </a-table-column>

        <a-table-column title="区域" width="160">
          <template #default="{ record }">{{ areaMap.get(record.area_id) || record.area_id || '-' }}</template>
        </a-table-column>

        <a-table-column title="协议" data-index="protocol" width="90" />

        <a-table-column title="状态" width="250">
          <template #default="{ record }">
            <a-space>
              <a-tag v-if="isGBDeviceRow(record)" :color="resolveGBDeviceStatus(record) === 'online' ? 'green' : 'default'">
                {{ statusText(resolveGBDeviceStatus(record)) }}
              </a-tag>
              <template v-else>
                <a-tag :color="record.status === 'online' ? 'green' : 'default'">{{ statusText(record.status) }}</a-tag>
              </template>
              <a-tag v-if="isGBChannel(record)" :color="sipStatusColor(resolveGBDeviceStatus(record))">
                {{ sipStatusText(resolveGBDeviceStatus(record)) }}
              </a-tag>
              <a-tag v-if="!isGBDeviceRow(record)" :color="record.ai_status === 'running' ? 'processing' : 'default'">{{ aiStatusText(record.ai_status) }}</a-tag>
            </a-space>
          </template>
        </a-table-column>

        <a-table-column v-if="isDevelopmentMode" title="流ID" data-index="stream_id" ellipsis />

        <a-table-column title="操作" width="430">
          <template #default="{ record }">
            <a-space>
              <a-button size="small" @click="openEdit(record)">编辑</a-button>
              <a-button
                v-if="canManageRecordings(record)"
                size="small"
                :loading="snapshotLoadingID === record.id"
                :disabled="!canSnapshot(record)"
                @click="captureSnapshot(record)"
              >
                获取快照
              </a-button>
              <a-button
                v-if="canManageRecordings(record)"
                size="small"
                :loading="previewLoading && previewDevice.id === record.id"
                :disabled="!canPreview(record)"
                @click="openPreview(record)"
              >
                预览
              </a-button>
              <a-button v-if="canManageRecordings(record)" size="small" @click="openRecordings(record)">录制文件</a-button>
              <a-popconfirm v-if="canDelete(record)" :title="deleteConfirmText(record)" @confirm="removeSource(record)">
                <a-button size="small" danger>删除</a-button>
              </a-popconfirm>
            </a-space>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal
      v-model:open="rtspModalOpen"
      title="新增 RTSP 拉流"
      width="760px"
      :confirm-loading="rtspSubmitting"
      :closable="!rtspSubmitting"
      :mask-closable="!rtspSubmitting"
      :keyboard="!rtspSubmitting"
      :cancel-button-props="{ disabled: rtspSubmitting }"
      @ok="submitCreateRTSP"
    >
      <a-form layout="vertical">
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="名称" required><a-input v-model:value="rtspForm.name" /></a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="区域" required><a-select v-model:value="rtspForm.area_id" :options="areaOptions" /></a-form-item>
          </a-col>
        </a-row>
        <a-form-item label="RTSP 地址" required>
          <a-input v-model:value="rtspForm.origin_url" placeholder="rtsp://username:password@host:554/stream" />
        </a-form-item>
        <a-form-item label="传输方式">
          <a-select v-model:value="rtspForm.transport" :options="[{ label: 'TCP', value: 'tcp' }, { label: 'UDP', value: 'udp' }]" />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal v-model:open="rtmpModalOpen" title="新增 RTMP 推流" width="760px" @ok="submitCreateRTMP">
      <a-form layout="vertical">
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="名称" required><a-input v-model:value="rtmpForm.name" /></a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="区域"><a-select v-model:value="rtmpForm.area_id" :options="areaOptions" /></a-form-item>
          </a-col>
        </a-row>
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="App" required><a-input v-model:value="rtmpForm.app" /></a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="Stream ID" required><a-input v-model:value="rtmpForm.stream_id" /></a-form-item>
          </a-col>
        </a-row>
        <a-form-item label="推流鉴权 Token（可选）">
          <a-input v-model:value="rtmpForm.publish_token" placeholder="配置后将用于 ZLM on_publish 鉴权" />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal
      v-model:open="editModalOpen"
      title="编辑通道"
      width="760px"
      :confirm-loading="editSubmitting"
      :closable="!editSubmitting"
      :mask-closable="!editSubmitting"
      :keyboard="!editSubmitting"
      :cancel-button-props="{ disabled: editSubmitting }"
      @ok="submitEdit"
    >
      <a-form layout="vertical">
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="名称" required><a-input v-model:value="editForm.name" /></a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="区域" required><a-select v-model:value="editForm.area_id" :options="areaOptions" /></a-form-item>
          </a-col>
        </a-row>

        <template v-if="editingSourceType === 'pull'">
          <a-form-item label="RTSP 地址" required>
            <a-input v-model:value="editForm.origin_url" placeholder="rtsp://username:password@host:554/stream" />
          </a-form-item>
          <a-form-item label="传输方式">
            <a-select v-model:value="editForm.transport" :options="[{ label: 'TCP', value: 'tcp' }, { label: 'UDP', value: 'udp' }]" />
          </a-form-item>
        </template>

        <template v-if="editingSourceType === 'push'">
          <a-row :gutter="12">
            <a-col :span="12"><a-form-item label="App"><a-input v-model:value="editForm.app" /></a-form-item></a-col>
            <a-col :span="12"><a-form-item label="Stream ID"><a-input v-model:value="editForm.stream_id" /></a-form-item></a-col>
          </a-row>
          <a-form-item label="推流鉴权 Token（可选）"><a-input v-model:value="editForm.publish_token" /></a-form-item>
        </template>

      </a-form>
    </a-modal>

    <a-modal v-model:open="gbModalOpen" title="GB28181维护" :footer="null" width="1160px" destroy-on-close>
      <GB28181View :embedded="true" />
    </a-modal>

    <a-modal v-model:open="blockModalOpen" title="黑名单管理" :footer="null" width="1160px" destroy-on-close>
      <div class="table-toolbar" style="margin-bottom: 10px">
        <a-tag color="orange">删除设备后会自动加入黑名单，防止立即重复接入</a-tag>
        <a-button :loading="blockLoading" @click="loadSourceBlocks">刷新</a-button>
      </div>
      <a-row :gutter="12">
        <a-col :span="12">
          <a-card size="small" title="GB28181 设备黑名单">
            <a-space style="margin-bottom: 10px; width: 100%">
              <a-input v-model:value="gbBlockForm.device_id" placeholder="20位 GB 设备ID" />
              <a-input v-model:value="gbBlockForm.reason" placeholder="原因（可选）" />
              <a-button type="primary" @click="submitAddGBBlock">添加</a-button>
            </a-space>
            <a-table :loading="blockLoading" :data-source="gbBlocks" row-key="device_id" :pagination="{ pageSize: 6 }" size="small">
              <a-table-column title="设备ID" data-index="device_id" />
              <a-table-column title="原因" data-index="reason" />
              <a-table-column title="更新时间">
                <template #default="{ record }">{{ timeText(record.updated_at) }}</template>
              </a-table-column>
              <a-table-column title="操作" width="90">
                <template #default="{ record }">
                  <a-popconfirm title="确认移除该 GB 黑名单？" @confirm="removeGBBlock(record.device_id)">
                    <a-button size="small" danger>移除</a-button>
                  </a-popconfirm>
                </template>
              </a-table-column>
            </a-table>
          </a-card>
        </a-col>
        <a-col :span="12">
          <a-card size="small" title="RTMP 推流黑名单">
            <a-space style="margin-bottom: 10px; width: 100%">
              <a-input v-model:value="rtmpBlockForm.app" placeholder="App（默认 live）" />
              <a-input v-model:value="rtmpBlockForm.stream_id" placeholder="Stream ID" />
              <a-input v-model:value="rtmpBlockForm.reason" placeholder="原因（可选）" />
              <a-button type="primary" @click="submitAddRTMPBlock">添加</a-button>
            </a-space>
            <a-table :loading="blockLoading" :data-source="rtmpBlocks" row-key="key" :pagination="{ pageSize: 6 }" size="small">
              <a-table-column title="App" data-index="app" width="120" />
              <a-table-column title="Stream ID" data-index="stream_id" />
              <a-table-column title="原因" data-index="reason" />
              <a-table-column title="更新时间">
                <template #default="{ record }">{{ timeText(record.updated_at) }}</template>
              </a-table-column>
              <a-table-column title="操作" width="90">
                <template #default="{ record }">
                  <a-popconfirm title="确认移除该 RTMP 黑名单？" @confirm="removeRTMPBlock(record.app, record.stream_id)">
                    <a-button size="small" danger>移除</a-button>
                  </a-popconfirm>
                </template>
              </a-table-column>
            </a-table>
          </a-card>
        </a-col>
      </a-row>
    </a-modal>

    <a-modal
      v-model:open="previewOpen"
      :title="`实时预览 - ${previewDevice.name || previewDevice.id}`"
      :footer="null"
      width="1040px"
      destroy-on-close
      @cancel="closePreview"
    >
      <div class="table-toolbar" style="margin-bottom: 10px">
        <a-space>
          <span>播放协议</span>
          <a-select v-model:value="previewProtocol" style="width: 150px" :options="previewProtocolOptions" />
        </a-space>
        <a-button :loading="previewLoading" @click="refreshPreview">刷新地址</a-button>
      </div>

      <div class="preview-player">
        <JessibucaPlayer
          :key="`${previewDevice.id}-${previewProtocol}-${previewPlayURL}`"
          :url="previewPlayURL"
          :stream-app="previewDevice.app"
          :stream-id="previewDevice.stream_id"
        />
      </div>

      <a-divider orientation="left">播放地址</a-divider>
      <div class="preview-urls mono">
        <div>WS-FLV: {{ previewOutput.ws_flv || '-' }}</div>
        <div>WebRTC: {{ previewOutput.webrtc || '-' }}</div>
        <div>RTSP: {{ previewOutput.rtsp || '-' }}</div>
        <div>RTMP: {{ previewOutput.rtmp || '-' }}</div>
      </div>
    </a-modal>

    <a-modal
      v-model:open="recordingsOpen"
      :title="`录制文件 - ${recordingDevice.name || recordingDevice.id}`"
      :footer="null"
      width="1120px"
    >
      <div class="table-toolbar" style="margin-bottom: 10px">
        <a-space>
          <span>文件总数：{{ recordingPager.total }}</span>
          <span>总大小：{{ bytesToText(recordingPager.total_size) }}</span>
          <span>已选：{{ selectedRecordingPaths.length }}</span>
        </a-space>
        <a-space>
          <a-button @click="loadRecordings(recordingPager.page)">刷新</a-button>
          <a-popconfirm title="确认删除已选文件？" @confirm="deleteSelectedRecordings">
            <a-button danger :disabled="selectedRecordingPaths.length === 0">批量删除</a-button>
          </a-popconfirm>
          <a-button :loading="recordingsExporting" :disabled="selectedRecordingPaths.length === 0" @click="exportSelectedRecordings">
            导出已选
          </a-button>
        </a-space>
      </div>

      <a-table
        class="recordings-table"
        :loading="recordingsLoading"
        :data-source="recordings"
        row-key="path"
        :row-selection="recordingRowSelection"
        :pagination="false"
        :scroll="{ x: 1400, y: 420 }"
        size="small"
      >
        <a-table-column title="文件名" data-index="name" width="260" ellipsis />
        <a-table-column title="大小" width="92">
          <template #default="{ record }">{{ bytesToText(record.size) }}</template>
        </a-table-column>
        <a-table-column title="事件时间" width="168">
          <template #default="{ record }">{{ timeText(record.event_occurred_at || '') || '-' }}</template>
        </a-table-column>
        <a-table-column title="事件ID" data-index="event_id" width="220" ellipsis />
        <a-table-column title="修改时间" width="168">
          <template #default="{ record }">{{ timeText(record.mod_time) }}</template>
        </a-table-column>
        <a-table-column title="路径" data-index="path" width="460" ellipsis />
        <a-table-column title="操作" width="136" fixed="right">
          <template #default="{ record }">
            <a-space>
              <a-button size="small" @click="downloadRecording(record)">下载</a-button>
              <a-popconfirm title="确认删除该录制文件？" @confirm="deleteRecordings([record.path])">
                <a-button size="small" danger>删除</a-button>
              </a-popconfirm>
            </a-space>
          </template>
        </a-table-column>
      </a-table>

      <div class="pager">
        <a-pagination
          :current="recordingPager.page"
          :page-size="recordingPager.page_size"
          :total="recordingPager.total"
          size="small"
          @change="(page: number) => loadRecordings(page)"
        />
      </div>
    </a-modal>
  </div>
</template>

<style scoped>
.preview-player {
  height: 460px;
}

.preview-urls {
  display: grid;
  gap: 6px;
}

.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 10px;
}

.recordings-table :deep(.ant-table-cell) {
  vertical-align: middle;
}
</style>
