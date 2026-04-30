import request from './request'

export type LoginPayload = {
  username: string
  password: string
}

export type AlgorithmUpsertPayload = {
  code?: string
  name: string
  mode: string
  detect_mode?: 1 | 2 | 3
  enabled?: boolean
  image_url?: string
  small_model_label?: string | string[]
  model_provider_id?: string
  yolo_threshold?: number
  iou_threshold?: number
  labels_trigger_mode?: 'any' | 'all'
  description?: string
  scene?: string
  category?: string
  prompt?: string
  prompt_version?: string
  activate_prompt?: boolean
}

export type AlgorithmImportResult = {
  total: number
  created: number
  updated: number
  failed: number
  errors: Array<{
    index: number
    code?: string
    message: string
  }>
}

export type EventQueryParams = {
  page?: number
  page_size?: number
  status?: string
  source?: string
  area_id?: string
  algorithm_id?: string
  alarm_level_id?: string
  start_at?: string
  end_at?: string
  task_name?: string
  device_name?: string
  algorithm_name?: string
}

export type RecordingQueryParams = {
  page?: number
  page_size?: number
  keyword?: string
  order?: 'asc' | 'desc'
  kind?: 'normal' | 'alarm'
}

export type LLMUsageQueryParams = {
  page?: number
  page_size?: number
  start_at?: string
  end_at?: string
  source?: string
  provider_id?: string
  model?: string
  call_status?: string
  usage_available?: string | boolean
}

export const authAPI = {
  login(payload: LoginPayload) {
    return request.post('/api/v1/auth/login', payload)
  },
  me() {
    return request.get('/api/v1/auth/me')
  },
}

export const playbackAPI = {
  streamStatus(app: string, stream: string) {
    return request.get('/api/v1/playback/stream-status', {
      params: {
        app,
        stream,
      },
    })
  },
}

export const areaAPI = {
  list() {
    return request.get('/api/v1/areas')
  },
  create(payload: { name: string; parent_id?: string; sort?: number }) {
    return request.post('/api/v1/areas', payload)
  },
  update(id: string, payload: { name: string; parent_id?: string; sort?: number }) {
    return request.put(`/api/v1/areas/${id}`, payload)
  },
  remove(id: string) {
    return request.delete(`/api/v1/areas/${id}`)
  },
}

export const deviceAPI = {
  list(params?: Record<string, unknown>) {
    return request.get('/api/v1/devices', { params })
  },
  detail(id: string) {
    return request.get(`/api/v1/devices/${id}`)
  },
  create(payload: Record<string, unknown>) {
    return request.post('/api/v1/devices', payload)
  },
  update(id: string, payload: Record<string, unknown>) {
    return request.put(`/api/v1/devices/${id}`, payload)
  },
  snapshot(id: string) {
    return request.post(`/api/v1/devices/${id}/snapshot`)
  },
  preview(id: string) {
    return request.post(`/api/v1/devices/${id}/preview`)
  },
  remove(id: string) {
    return request.delete(`/api/v1/devices/${id}`)
  },
  blacklist(params?: Record<string, unknown>) {
    return request.get('/api/v1/devices/blacklist', { params })
  },
  addGBBlacklist(payload: { device_id: string; reason?: string }) {
    return request.post('/api/v1/devices/blacklist/gb28181', payload)
  },
  removeGBBlacklist(deviceID: string) {
    return request.delete(`/api/v1/devices/blacklist/gb28181/${encodeURIComponent(deviceID)}`)
  },
  addRTMPBlacklist(payload: { app?: string; stream_id: string; reason?: string }) {
    return request.post('/api/v1/devices/blacklist/rtmp', payload)
  },
  removeRTMPBlacklist(app: string, streamID: string) {
    return request.delete(`/api/v1/devices/blacklist/rtmp/${encodeURIComponent(app)}/${encodeURIComponent(streamID)}`)
  },
  recordingStatus(id: string) {
    return request.get(`/api/v1/devices/${id}/recording-status`)
  },
  recordings(id: string, params?: RecordingQueryParams) {
    return request.get(`/api/v1/devices/${id}/recordings`, { params })
  },
  gb28181Info() {
    return request.get('/api/v1/devices/gb28181/info')
  },
  verifyGB28181(payload: Record<string, unknown>) {
    return request.post('/api/v1/devices/gb28181/verify', payload)
  },
  gbDevices(params?: Record<string, unknown>) {
    return request.get('/api/v1/devices/gb28181/devices', { params })
  },
  createGBDevice(payload: Record<string, unknown>) {
    return request.post('/api/v1/devices/gb28181/devices', payload)
  },
  updateGBDevice(deviceID: string, payload: Record<string, unknown>) {
    return request.put(`/api/v1/devices/gb28181/devices/${deviceID}`, payload)
  },
  deleteGBDevice(deviceID: string) {
    return request.delete(`/api/v1/devices/gb28181/devices/${deviceID}`)
  },
  syncGBCatalog(deviceID: string) {
    return request.post(`/api/v1/devices/gb28181/devices/${deviceID}/catalog`)
  },
  gbChannels(deviceID: string, params?: Record<string, unknown>) {
    return request.get(`/api/v1/devices/gb28181/devices/${deviceID}/channels`, { params })
  },
  updateGBChannel(channelID: string, payload: Record<string, unknown>) {
    return request.put(`/api/v1/devices/gb28181/channels/${channelID}`, payload)
  },
  gbStats() {
    return request.get('/api/v1/devices/gb28181/stats')
  },
  recordingFileURL(id: string, path: string) {
    const normalized = String(path || '')
      .split('/')
      .filter(Boolean)
      .map((seg) => encodeURIComponent(seg))
      .join('/')
    return `/api/v1/devices/${id}/recordings/file/${normalized}`
  },
  recordingsExportURL(id: string) {
    return `/api/v1/devices/${id}/recordings/export`
  },
  deleteRecordings(id: string, paths: string[]) {
    return request.delete(`/api/v1/devices/${id}/recordings`, { data: { paths } })
  },
  discoverLAN(params?: Record<string, unknown>) {
    return request.get('/api/v1/devices/discover/lan', { params })
  },
}

export const algorithmAPI = {
  testLimits() {
    return request.get('/api/v1/algorithms/test-limits')
  },
  list() {
    return request.get('/api/v1/algorithms')
  },
  uploadCover(file: File) {
    const formData = new FormData()
    formData.append('file', file)
    return request.post('/api/v1/algorithms/cover', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
    })
  },
  create(payload: AlgorithmUpsertPayload) {
    return request.post('/api/v1/algorithms', payload)
  },
  import(payload: AlgorithmUpsertPayload[]) {
    return request.post('/api/v1/algorithms/import', payload)
  },
  update(id: string, payload: AlgorithmUpsertPayload) {
    return request.put(`/api/v1/algorithms/${id}`, payload)
  },
  remove(id: string) {
    return request.delete(`/api/v1/algorithms/${id}`)
  },
  listPrompts(id: string) {
    return request.get(`/api/v1/algorithms/${id}/prompts`)
  },
  createPrompt(id: string, payload: Record<string, unknown>) {
    return request.post(`/api/v1/algorithms/${id}/prompts`, payload)
  },
  deletePrompt(id: string, promptId: string) {
    return request.delete(`/api/v1/algorithms/${id}/prompts/${promptId}`)
  },
  activatePrompt(id: string, promptId: string) {
    return request.post(`/api/v1/algorithms/${id}/prompts/${promptId}/activate`)
  },
  test(id: string, payload: FormData | Record<string, unknown>) {
    const isFormData = typeof FormData !== 'undefined' && payload instanceof FormData
    return request.post(`/api/v1/algorithms/${id}/test`, payload, isFormData
      ? { headers: { 'Content-Type': 'multipart/form-data' }, timeout: 180000 }
      : { timeout: 180000 })
  },
  draftTest(payload: FormData) {
    return request.post('/api/v1/algorithms/draft-test', payload, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: 180000,
    })
  },
  getTestJob(jobId: string) {
    return request.get(`/api/v1/algorithms/test-jobs/${jobId}`)
  },
  getDraftTestJob(jobId: string) {
    return request.get(`/api/v1/algorithms/draft-test-jobs/${jobId}`)
  },
  listTests(id: string, params?: Record<string, unknown>) {
    return request.get(`/api/v1/algorithms/${id}/tests`, { params })
  },
  clearTests(id: string) {
    return request.delete(`/api/v1/algorithms/${id}/tests`)
  },
  testImageURL(path: string) {
    const normalized = String(path || '')
      .split('/')
      .filter(Boolean)
      .map((seg) => encodeURIComponent(seg))
      .join('/')
    return `/api/v1/algorithms/test-image/${normalized}`
  },
  testMediaURL(path: string) {
    const normalized = String(path || '')
      .split('/')
      .filter(Boolean)
      .map((seg) => encodeURIComponent(seg))
      .join('/')
    return `/api/v1/algorithms/test-media/${normalized}`
  },
}

export const llmUsageAPI = {
  summary(params?: LLMUsageQueryParams) {
    return request.get('/api/v1/llm-usage/summary', { params })
  },
  hourly(params?: LLMUsageQueryParams) {
    return request.get('/api/v1/llm-usage/hourly', { params })
  },
  daily(params?: LLMUsageQueryParams) {
    return request.get('/api/v1/llm-usage/daily', { params })
  },
  calls(params?: LLMUsageQueryParams) {
    return request.get('/api/v1/llm-usage/calls', { params })
  },
}

export const yoloLabelAPI = {
  list() {
    return request.get('/api/v1/yolo-labels')
  },
}

export const taskAPI = {
  list() {
    return request.get('/api/v1/tasks')
  },
  defaults() {
    return request.get('/api/v1/tasks/defaults')
  },
  create(payload: Record<string, unknown>) {
    return request.post('/api/v1/tasks', payload)
  },
  update(id: string, payload: Record<string, unknown>) {
    return request.put(`/api/v1/tasks/${id}`, payload)
  },
  quickUpdateDevice(id: string, deviceID: string, payload: Record<string, unknown>) {
    return request.put(`/api/v1/tasks/${id}/devices/${deviceID}/quick-config`, payload)
  },
  remove(id: string) {
    return request.delete(`/api/v1/tasks/${id}`)
  },
  start(id: string) {
    return request.post(`/api/v1/tasks/${id}/start`)
  },
  stop(id: string) {
    return request.post(`/api/v1/tasks/${id}/stop`)
  },
  sync(id: string) {
    return request.get(`/api/v1/tasks/${id}/sync-status`)
  },
  promptPreview(id: string) {
    return request.get(`/api/v1/tasks/${id}/prompt-preview`)
  },
}

export const alarmLevelAPI = {
  list() {
    return request.get('/api/v1/alarm-levels')
  },
  create(payload: Record<string, unknown>) {
    return request.post('/api/v1/alarm-levels', payload)
  },
  update(id: string, payload: Record<string, unknown>) {
    return request.put(`/api/v1/alarm-levels/${id}`, payload)
  },
  remove(id: string) {
    return request.delete(`/api/v1/alarm-levels/${id}`)
  },
}

export const eventAPI = {
  list(params?: EventQueryParams) {
    return request.get('/api/v1/events', { params })
  },
  detail(id: string) {
    return request.get(`/api/v1/events/${id}`)
  },
  clipFileURL(id: string, path: string) {
    const normalized = String(path || '')
      .split('/')
      .filter(Boolean)
      .map((seg) => encodeURIComponent(seg))
      .join('/')
    return `/api/v1/events/${id}/clips/file/${normalized}`
  },
  review(id: string, payload: { status: string; review_note: string }) {
    return request.put(`/api/v1/events/${id}/review`, payload)
  },
}

export const dashboardAPI = {
  overview() {
    return request.get('/api/v1/dashboard/overview')
  },
  camera2Overview(params?: Record<string, unknown>) {
    return request.get('/api/v1/dashboard/camera2/overview', { params })
  },
  camera2CreatePatrolJob(payload: Record<string, unknown>) {
    return request.post('/api/v1/dashboard/camera2/patrol-jobs', payload)
  },
  camera2PatrolJob(jobID: string) {
    return request.get(`/api/v1/dashboard/camera2/patrol-jobs/${jobID}`)
  },
}

export const systemAPI = {
  metrics() {
    return request.get('/api/v1/system/metrics')
  },
  users() {
    return request.get('/api/v1/system/users')
  },
  createUser(payload: Record<string, unknown>) {
    return request.post('/api/v1/system/users', payload)
  },
  updateUser(id: string, payload: Record<string, unknown>) {
    return request.put(`/api/v1/system/users/${id}`, payload)
  },
  removeUser(id: string) {
    return request.delete(`/api/v1/system/users/${id}`)
  },
  userRoles(id: string) {
    return request.get(`/api/v1/system/users/${id}/roles`)
  },
  setUserRoles(id: string, ids: string[]) {
    return request.put(`/api/v1/system/users/${id}/roles`, { ids })
  },
  roles() {
    return request.get('/api/v1/system/roles')
  },
  createRole(payload: Record<string, unknown>) {
    return request.post('/api/v1/system/roles', payload)
  },
  updateRole(id: string, payload: Record<string, unknown>) {
    return request.put(`/api/v1/system/roles/${id}`, payload)
  },
  removeRole(id: string) {
    return request.delete(`/api/v1/system/roles/${id}`)
  },
  roleMenus(id: string) {
    return request.get(`/api/v1/system/roles/${id}/menus`)
  },
  setRoleMenus(id: string, ids: string[]) {
    return request.put(`/api/v1/system/roles/${id}/menus`, { ids })
  },
  menus() {
    return request.get('/api/v1/system/menus')
  },
  createMenu(payload: Record<string, unknown>) {
    return request.post('/api/v1/system/menus', payload)
  },
  updateMenu(id: string, payload: Record<string, unknown>) {
    return request.put(`/api/v1/system/menus/${id}`, payload)
  },
  removeMenu(id: string) {
    return request.delete(`/api/v1/system/menus/${id}`)
  },
}
