<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { message } from 'ant-design-vue'
import { alarmLevelAPI, algorithmAPI, deviceAPI, taskAPI } from '@/api/modules'
import { useAuthStore } from '@/stores/auth'

type Device = {
  id: string
  name: string
  status: string
  ai_status: string
}

type RecordingPolicy = 'none' | 'alarm_clip'
type FrameRateMode = 'fps' | 'interval'

type Algorithm = {
  id: string
  name: string
}

type AlgorithmConfig = {
  algorithm_id: string
  algorithm_name?: string
  mode?: string
  alarm_level_id?: string
  alarm_level_name?: string
  alarm_level_color?: string
  alarm_level_severity?: number
  alert_cycle_seconds: number
}

type TaskModel = {
  id: string
  name: string
  status: string
  notes: string
  created_at?: string
  updated_at?: string
}

type TaskDeviceConfig = {
  device_id: string
  device_name: string
  algorithm_configs: AlgorithmConfig[]
  frame_rate_mode: FrameRateMode
  frame_rate_value: number
  recording_policy: RecordingPolicy
  recording_pre_seconds: number
  recording_post_seconds: number
}

type TaskItem = {
  task: TaskModel
  device_configs: TaskDeviceConfig[]
}

type TaskStartResponse = {
  status?: string
  message?: string
}

type DeviceConfigForm = {
  device_id: string
  algorithm_ids: string[]
  algorithm_cycle_map: Record<string, number>
  algorithm_level_map: Record<string, string>
  frame_rate_mode: FrameRateMode
  frame_rate_value: number
  recording_policy: RecordingPolicy
  recording_pre_seconds: number
  recording_post_seconds: number
}

type AlarmLevel = {
  id: string
  name: string
  severity: number
  color: string
}

type TaskDefaults = {
  recording_policy_default: RecordingPolicy
  alarm_clip_enabled_default: boolean
  recording_pre_seconds_default: number
  recording_post_seconds_default: number
  alert_cycle_seconds_default: number
  alarm_level_id_default: string
  frame_rate_modes: FrameRateMode[]
  frame_rate_mode_default: FrameRateMode
  frame_rate_value_default: number
}

const fallbackTaskDefaults: TaskDefaults = {
  recording_policy_default: 'none',
  alarm_clip_enabled_default: false,
  recording_pre_seconds_default: 8,
  recording_post_seconds_default: 12,
  alert_cycle_seconds_default: 60,
  alarm_level_id_default: 'alarm_level_1',
  frame_rate_modes: ['interval', 'fps'],
  frame_rate_mode_default: 'interval',
  frame_rate_value_default: 5,
}

const loading = ref(false)
const submitting = ref(false)
const tasks = ref<TaskItem[]>([])
const devices = ref<Device[]>([])
const algorithms = ref<Algorithm[]>([])
const alarmLevels = ref<AlarmLevel[]>([])
const taskDefaults = ref<TaskDefaults>({ ...fallbackTaskDefaults })
const authStore = useAuthStore()
const isDevelopmentMode = computed(() => authStore.developmentMode)

const modalOpen = ref(false)
const editingID = ref('')
const form = reactive<{
  name: string
  notes: string
  device_ids: string[]
  device_config_map: Record<string, DeviceConfigForm>
}>({
  name: '',
  notes: '',
  device_ids: [],
  device_config_map: {},
})

const promptPreviewOpen = ref(false)
const promptPreview = ref<any>(null)
const taskActionLoading = reactive<Record<string, 'start' | 'stop'>>({})

const deviceMap = computed(() => {
  const out = new Map<string, Device>()
  for (const item of devices.value) out.set(item.id, item)
  return out
})

const occupiedDeviceTaskMap = computed(() => {
  const out = new Map<string, string>()
  for (const task of tasks.value) {
    for (const cfg of task.device_configs || []) {
      const deviceID = String(cfg.device_id || '').trim()
      if (!deviceID) continue
      out.set(deviceID, task.task.id)
    }
  }
  return out
})

const editingTaskDeviceSet = computed(() => {
  if (!editingID.value) return new Set<string>()
  const hit = tasks.value.find((item) => item.task.id === editingID.value)
  const ids = (hit?.device_configs || []).map((item) => String(item.device_id || '').trim()).filter(Boolean)
  return new Set(ids)
})

const availableDevices = computed(() => {
  return devices.value.filter((item) => {
    if (editingTaskDeviceSet.value.has(item.id)) return true
    return !occupiedDeviceTaskMap.value.has(item.id)
  })
})

const availableDeviceOptions = computed(() => {
  return availableDevices.value.map((item) => ({
    label: `${item.name}（${deviceStatusText(item.status)}）`,
    value: item.id,
  }))
})

const unavailableCount = computed(() => {
  let count = 0
  for (const item of devices.value) {
    if (editingTaskDeviceSet.value.has(item.id)) continue
    if (occupiedDeviceTaskMap.value.has(item.id)) count += 1
  }
  return count
})

const sortedSelectedDeviceIDs = computed(() => {
  const ids = [...form.device_ids]
  ids.sort((a, b) => {
    const left = String(deviceMap.value.get(a)?.name || a)
    const right = String(deviceMap.value.get(b)?.name || b)
    return left.localeCompare(right, 'zh-CN')
  })
  return ids
})

function createDefaultDeviceConfig(deviceID: string): DeviceConfigForm {
  return {
    device_id: deviceID,
    algorithm_ids: [],
    algorithm_cycle_map: {},
    algorithm_level_map: {},
    frame_rate_mode: taskDefaults.value.frame_rate_mode_default,
    frame_rate_value: taskDefaults.value.frame_rate_value_default,
    recording_policy: taskDefaults.value.recording_policy_default,
    recording_pre_seconds: taskDefaults.value.recording_pre_seconds_default,
    recording_post_seconds: taskDefaults.value.recording_post_seconds_default,
  }
}

const frameRateModeOptions = computed<Array<{ label: string; value: FrameRateMode }>>(() => {
  return taskDefaults.value.frame_rate_modes.map((item) => ({
    label: item === 'interval' ? '每几秒1帧' : '每秒几帧',
    value: item,
  }))
})

const recordingPolicyOptions: Array<{ label: string; value: RecordingPolicy }> = [
  { label: '不录制', value: 'none' },
  { label: '报警片段录制', value: 'alarm_clip' },
]

function ensureDeviceConfig(deviceID: string): DeviceConfigForm {
  if (!form.device_config_map[deviceID]) {
    form.device_config_map[deviceID] = createDefaultDeviceConfig(deviceID)
  }
  return form.device_config_map[deviceID]
}

function normalizeAlertCycle(raw: unknown) {
  const value = Number(raw)
  if (!Number.isFinite(value)) return taskDefaults.value.alert_cycle_seconds_default
  if (value < 0) return 0
  if (value > 86400) return 86400
  return Math.round(value)
}

function normalizeRecordingPolicy(raw: unknown): RecordingPolicy {
  const normalized = String(raw || '').trim().toLowerCase()
  if (normalized === 'alarm_clip') return 'alarm_clip'
  if (normalized === 'none') return 'none'
  return taskDefaults.value.recording_policy_default
}

function normalizeAlarmLevelID(raw: unknown) {
  const normalized = String(raw || '').trim()
  const fallback = String(taskDefaults.value.alarm_level_id_default || '').trim() || fallbackTaskDefaults.alarm_level_id_default
  if (!normalized) return fallback
  if (!alarmLevels.value.some((item) => item.id === normalized)) return fallback
  return normalized
}

function normalizeFrameRateMode(raw: unknown): FrameRateMode {
  const normalized = String(raw || '').trim().toLowerCase()
  if (taskDefaults.value.frame_rate_modes.includes(normalized as FrameRateMode)) {
    return normalized as FrameRateMode
  }
  return taskDefaults.value.frame_rate_mode_default
}

function normalizeFrameRateValue(raw: unknown) {
  const value = Number(raw)
  if (!Number.isFinite(value) || value < 1 || value > 60) {
    return taskDefaults.value.frame_rate_value_default
  }
  return Math.round(value)
}

function normalizeTaskDefaults(raw: Partial<TaskDefaults> | null | undefined): TaskDefaults {
  const frameRateModes = Array.from(new Set((Array.isArray(raw?.frame_rate_modes) ? raw?.frame_rate_modes : [])
    .map((item) => String(item || '').trim().toLowerCase())
    .filter((item): item is FrameRateMode => item === 'interval' || item === 'fps')))
  const normalizedModes = frameRateModes.length > 0 ? frameRateModes : fallbackTaskDefaults.frame_rate_modes
  const requestedDefaultMode = String(raw?.frame_rate_mode_default || '').trim().toLowerCase() as FrameRateMode
  const frameRateModeDefault = normalizedModes.includes(requestedDefaultMode)
    ? requestedDefaultMode
    : (normalizedModes.includes('interval') ? 'interval' : normalizedModes[0])
  const alertCycleValue = Number(raw?.alert_cycle_seconds_default)
  const frameRateValue = Number(raw?.frame_rate_value_default)
  const preSeconds = Number(raw?.recording_pre_seconds_default)
  const postSeconds = Number(raw?.recording_post_seconds_default)
  const alarmClipEnabledDefault = raw?.alarm_clip_enabled_default === true
  const rawRecordingPolicy = String(raw?.recording_policy_default || '').trim().toLowerCase()
  const recordingPolicyDefault: RecordingPolicy = rawRecordingPolicy === 'alarm_clip'
    ? 'alarm_clip'
    : rawRecordingPolicy === 'none'
      ? 'none'
      : (alarmClipEnabledDefault ? 'alarm_clip' : 'none')
  return {
    recording_policy_default: recordingPolicyDefault,
    alarm_clip_enabled_default: alarmClipEnabledDefault,
    recording_pre_seconds_default: Number.isFinite(preSeconds) && preSeconds >= 1 && preSeconds <= 600 ? Math.round(preSeconds) : fallbackTaskDefaults.recording_pre_seconds_default,
    recording_post_seconds_default: Number.isFinite(postSeconds) && postSeconds >= 1 && postSeconds <= 600 ? Math.round(postSeconds) : fallbackTaskDefaults.recording_post_seconds_default,
    alert_cycle_seconds_default: Number.isFinite(alertCycleValue) && alertCycleValue >= 0 && alertCycleValue <= 86400 ? Math.round(alertCycleValue) : fallbackTaskDefaults.alert_cycle_seconds_default,
    alarm_level_id_default: String(raw?.alarm_level_id_default || '').trim() || fallbackTaskDefaults.alarm_level_id_default,
    frame_rate_modes: normalizedModes,
    frame_rate_mode_default: frameRateModeDefault,
    frame_rate_value_default: Number.isFinite(frameRateValue) && frameRateValue >= 1 && frameRateValue <= 60 ? Math.round(frameRateValue) : fallbackTaskDefaults.frame_rate_value_default,
  }
}

function syncAlgorithmCycleMap(cfg: DeviceConfigForm) {
  const selectedIDs = Array.from(new Set((cfg.algorithm_ids || []).map((item) => String(item || '').trim()).filter(Boolean)))
  const current = cfg.algorithm_cycle_map || {}
  const currentLevelMap = cfg.algorithm_level_map || {}
  const next: Record<string, number> = {}
  const nextLevelMap: Record<string, string> = {}
  for (const algorithmID of selectedIDs) {
    const currentValue = Object.prototype.hasOwnProperty.call(current, algorithmID) ? current[algorithmID] : taskDefaults.value.alert_cycle_seconds_default
    next[algorithmID] = normalizeAlertCycle(currentValue)
    const levelID = Object.prototype.hasOwnProperty.call(currentLevelMap, algorithmID)
      ? currentLevelMap[algorithmID]
      : taskDefaults.value.alarm_level_id_default
    nextLevelMap[algorithmID] = normalizeAlarmLevelID(levelID)
  }
  cfg.algorithm_ids = selectedIDs
  cfg.algorithm_cycle_map = next
  cfg.algorithm_level_map = nextLevelMap
}

function syncSelectedDeviceConfigs() {
  const selected = new Set(form.device_ids.map((item) => String(item || '').trim()).filter(Boolean))
  const currentKeys = Object.keys(form.device_config_map)
  for (const key of currentKeys) {
    if (!selected.has(key)) {
      delete form.device_config_map[key]
    }
  }
  for (const deviceID of selected) {
    syncAlgorithmCycleMap(ensureDeviceConfig(deviceID))
  }
}

watch(
  () => [...form.device_ids],
  () => syncSelectedDeviceConfigs(),
  { immediate: true },
)

function taskStatusText(status: string) {
  if (status === 'running') return '运行中'
  if (status === 'partial_fail') return '部分失败'
  if (status === 'stopped') return '已停止'
  if (status === 'pending') return '等待中'
  return status || '-'
}

function deviceStatusText(status: string) {
  const normalized = String(status || '').trim().toLowerCase()
  if (normalized === 'online') return '在线'
  if (normalized === 'offline') return '离线'
  if (!normalized) return '-'
  return normalized
}

function deviceStatusColor(status: string) {
  const normalized = String(status || '').trim().toLowerCase()
  if (normalized === 'online') return 'green'
  if (normalized === 'offline') return 'default'
  return 'gold'
}

const alarmLevelOptions = computed(() => {
  return alarmLevels.value.map((item) => ({
    label: item.name,
    value: item.id,
  }))
})

function setTaskActionLoading(id: string, action: 'start' | 'stop' | null) {
  const key = String(id || '').trim()
  if (!key) return
  if (action) {
    taskActionLoading[key] = action
  } else {
    delete taskActionLoading[key]
  }
}

function isTaskActionLoading(id: string, action?: 'start' | 'stop') {
  const key = String(id || '').trim()
  if (!key) return false
  const current = taskActionLoading[key]
  if (!current) return false
  if (!action) return true
  return current === action
}

async function loadAll() {
  loading.value = true
  try {
    const [taskResp, deviceResp, algorithmResp, alarmLevelResp, taskDefaultsResp] = await Promise.all([
      taskAPI.list() as Promise<{ items: TaskItem[] }>,
      deviceAPI.list({ row_kind: 'channel' }) as Promise<{ items: Device[] }>,
      algorithmAPI.list() as Promise<{ items: any[] }>,
      alarmLevelAPI.list() as Promise<{ items: AlarmLevel[] }>,
      taskAPI.defaults() as Promise<Partial<TaskDefaults>>,
    ])
    tasks.value = taskResp.items || []
    devices.value = deviceResp.items || []
    algorithms.value = (algorithmResp.items || []).map((item: any) => (item.algorithm ? item.algorithm : item))
    alarmLevels.value = [...(alarmLevelResp.items || [])].sort((a, b) => Number(a.severity || 0) - Number(b.severity || 0))
    taskDefaults.value = normalizeTaskDefaults(taskDefaultsResp)
    syncSelectedDeviceConfigs()
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}

function resetForm() {
  form.name = ''
  form.notes = ''
  form.device_ids = []
  form.device_config_map = {}
}

function openCreate() {
  editingID.value = ''
  resetForm()
  modalOpen.value = true
}

function openEdit(row: TaskItem) {
  editingID.value = row.task.id
  form.name = row.task.name
  form.notes = row.task.notes || ''
  form.device_ids = (row.device_configs || []).map((item) => String(item.device_id || '').trim()).filter(Boolean)
  const nextMap: Record<string, DeviceConfigForm> = {}
  for (const cfg of row.device_configs || []) {
    const deviceID = String(cfg.device_id || '').trim()
    if (!deviceID) continue
    const algorithmConfigs = Array.isArray(cfg.algorithm_configs) ? cfg.algorithm_configs : []
    const algorithmIDs = algorithmConfigs.map((item) => String(item.algorithm_id || '').trim()).filter(Boolean)
    const algorithmCycleMap: Record<string, number> = {}
    const algorithmLevelMap: Record<string, string> = {}
    for (const item of algorithmConfigs) {
      const algorithmID = String(item.algorithm_id || '').trim()
      if (!algorithmID) continue
      algorithmCycleMap[algorithmID] = normalizeAlertCycle(item.alert_cycle_seconds)
      algorithmLevelMap[algorithmID] = normalizeAlarmLevelID(item.alarm_level_id)
    }
    nextMap[deviceID] = {
      device_id: deviceID,
      algorithm_ids: algorithmIDs,
      algorithm_cycle_map: algorithmCycleMap,
      algorithm_level_map: algorithmLevelMap,
      frame_rate_mode: normalizeFrameRateMode(cfg.frame_rate_mode),
      frame_rate_value: normalizeFrameRateValue(cfg.frame_rate_value),
      recording_policy: normalizeRecordingPolicy(cfg.recording_policy),
      recording_pre_seconds: Number(cfg.recording_pre_seconds || taskDefaults.value.recording_pre_seconds_default),
      recording_post_seconds: Number(cfg.recording_post_seconds || taskDefaults.value.recording_post_seconds_default),
    }
    syncAlgorithmCycleMap(nextMap[deviceID])
  }
  form.device_config_map = nextMap
  syncSelectedDeviceConfigs()
  modalOpen.value = true
}

function buildSubmitPayload() {
  const name = String(form.name || '').trim()
  if (!name) {
    throw new Error('任务名称不能为空')
  }
  const deviceIDs = form.device_ids.map((item) => String(item || '').trim()).filter(Boolean)
  if (deviceIDs.length === 0) {
    throw new Error('请至少选择一个设备')
  }
  const configs = deviceIDs.map((deviceID) => {
    const cfg = ensureDeviceConfig(deviceID)
    syncAlgorithmCycleMap(cfg)
    if ((cfg.algorithm_ids || []).length === 0) {
      const deviceName = deviceMap.value.get(deviceID)?.name || deviceID
      throw new Error(`设备【${deviceName}】至少选择一个算法`)
    }
    const algorithmConfigs = cfg.algorithm_ids.map((algorithmID) => {
      const alarmLevelID = normalizeAlarmLevelID(cfg.algorithm_level_map[algorithmID])
      if (!alarmLevelID) {
        throw new Error(`设备【${deviceMap.value.get(deviceID)?.name || deviceID}】算法缺少报警等级`)
      }
      return {
        algorithm_id: algorithmID,
        alarm_level_id: alarmLevelID,
        alert_cycle_seconds: normalizeAlertCycle(cfg.algorithm_cycle_map[algorithmID]),
      }
    })

    const recordingPolicy = normalizeRecordingPolicy(cfg.recording_policy)
    if (!['none', 'alarm_clip'].includes(recordingPolicy)) {
      throw new Error('录制策略必须为 none/alarm_clip')
    }

    const frameRateMode = normalizeFrameRateMode(cfg.frame_rate_mode)
    if (!taskDefaults.value.frame_rate_modes.includes(frameRateMode)) {
      throw new Error(`抽帧模式必须为 ${taskDefaults.value.frame_rate_modes.join('/')}`)
    }
    const frameRateValue = normalizeFrameRateValue(cfg.frame_rate_value)
    if (!Number.isFinite(frameRateValue) || frameRateValue < 1 || frameRateValue > 60) {
      throw new Error('抽帧数值必须在 1~60')
    }

    return {
      device_id: deviceID,
      algorithm_configs: algorithmConfigs,
      frame_rate_mode: frameRateMode,
      frame_rate_value: frameRateValue,
      recording_policy: recordingPolicy,
      recording_pre_seconds: Number(cfg.recording_pre_seconds || taskDefaults.value.recording_pre_seconds_default),
      recording_post_seconds: Number(cfg.recording_post_seconds || taskDefaults.value.recording_post_seconds_default),
    }
  })
  return {
    name,
    notes: String(form.notes || '').trim(),
    device_configs: configs,
  }
}

async function submit() {
  if (submitting.value) return
  submitting.value = true
  try {
    const payload = buildSubmitPayload()
    if (editingID.value) {
      await taskAPI.update(editingID.value, payload)
      message.success('任务已更新')
    } else {
      await taskAPI.create(payload)
      message.success('任务已创建')
    }
    modalOpen.value = false
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    submitting.value = false
  }
}

async function remove(id: string) {
  try {
    await taskAPI.remove(id)
    message.success('任务已删除')
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function start(id: string) {
  if (isTaskActionLoading(id)) return
  setTaskActionLoading(id, 'start')
  try {
    const resp = await taskAPI.start(id) as TaskStartResponse
    if (resp?.status === 'running') {
      message.success(resp.message || '\u4efb\u52a1\u5df2\u542f\u52a8')
    } else if (resp?.status === 'partial_fail') {
      message.warning(resp.message || '\u4efb\u52a1\u90e8\u5206\u542f\u52a8\u6210\u529f')
    } else {
      message.error(resp?.message || '\u4efb\u52a1\u542f\u52a8\u5931\u8d25')
    }
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    setTaskActionLoading(id, null)
  }
}

async function stop(id: string) {
  if (isTaskActionLoading(id)) return
  setTaskActionLoading(id, 'stop')
  try {
    await taskAPI.stop(id)
    message.success('任务停止请求已发送')
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    setTaskActionLoading(id, null)
  }
}

async function syncStatus(id: string) {
  try {
    await taskAPI.sync(id)
    message.success('任务状态已同步')
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function showPromptPreview(id: string) {
  if (!isDevelopmentMode.value) return
  try {
    promptPreview.value = await taskAPI.promptPreview(id)
    promptPreviewOpen.value = true
  } catch (err) {
    message.error((err as Error).message)
  }
}

onMounted(loadAll)
</script>

<template>
  <div>
    <h2 class="page-title">视频任务配置</h2>
    <p class="page-subtitle">按设备独立配置算法、抽帧策略与录制策略</p>

    <a-card class="glass-card">
      <div class="table-toolbar">
        <a-space>
          <a-button type="primary" @click="openCreate">新增任务</a-button>
        </a-space>
        <a-space>
          <a-button @click="loadAll">刷新</a-button>
        </a-space>
      </div>

      <a-table :data-source="tasks" row-key="task.id" :loading="loading" :pagination="{ pageSize: 8 }">
        <a-table-column title="任务名称">
          <template #default="{ record }">{{ record.task.name }}</template>
        </a-table-column>
        <a-table-column title="状态">
          <template #default="{ record }">
            <a-tag :color="record.task.status === 'running' ? 'green' : record.task.status === 'partial_fail' ? 'gold' : 'default'">
              {{ taskStatusText(record.task.status) }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="设备数" width="100">
          <template #default="{ record }">{{ record.device_configs?.length || 0 }}</template>
        </a-table-column>
        <a-table-column title="备注">
          <template #default="{ record }">{{ record.task.notes || '-' }}</template>
        </a-table-column>
        <a-table-column title="操作" width="430">
          <template #default="{ record }">
            <a-space wrap>
              <a-button size="small" :disabled="record.task.status === 'running'" @click="openEdit(record)">编辑</a-button>
              <a-button
                size="small"
                type="primary"
                :loading="isTaskActionLoading(record.task.id, 'start')"
                :disabled="isTaskActionLoading(record.task.id)"
                @click="start(record.task.id)"
              >
                启动
              </a-button>
              <a-button
                size="small"
                :loading="isTaskActionLoading(record.task.id, 'stop')"
                :disabled="isTaskActionLoading(record.task.id)"
                @click="stop(record.task.id)"
              >
                停止
              </a-button>
              <a-button size="small" @click="syncStatus(record.task.id)">同步状态</a-button>
              <a-button v-if="isDevelopmentMode" size="small" @click="showPromptPreview(record.task.id)">提示词预览</a-button>
              <a-popconfirm title="确认删除该任务？" @confirm="remove(record.task.id)">
                <a-button size="small" danger>删除</a-button>
              </a-popconfirm>
            </a-space>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal
      v-model:open="modalOpen"
      :title="editingID ? '编辑任务' : '新增任务'"
      width="1120px"
      :confirm-loading="submitting"
      @ok="submit"
    >
      <a-form layout="vertical">
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="任务名称" required>
              <a-input v-model:value="form.name" placeholder="请输入任务名称" />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="设备选择（设备独占，已被其他任务占用的设备已过滤）" required>
              <a-select
                v-model:value="form.device_ids"
                mode="multiple"
                allow-clear
                :options="availableDeviceOptions"
                placeholder="请选择设备"
              />
              <div class="hint">已隐藏占用设备：{{ unavailableCount }}</div>
            </a-form-item>
          </a-col>
        </a-row>

        <a-form-item label="备注">
          <a-textarea v-model:value="form.notes" :rows="2" placeholder="可选" />
        </a-form-item>

        <a-divider orientation="left">按设备配置</a-divider>
        <a-empty v-if="sortedSelectedDeviceIDs.length === 0" description="请先选择设备" />

        <a-space v-else direction="vertical" size="middle" style="width: 100%">
          <a-card v-for="(deviceID, index) in sortedSelectedDeviceIDs" :key="deviceID" size="small">
            <template #title>
              <a-space>
                <span>{{ index + 1 }}. {{ deviceMap.get(deviceID)?.name || deviceID }}</span>
                <a-tag :color="deviceStatusColor(deviceMap.get(deviceID)?.status || '')">
                  {{ deviceStatusText(deviceMap.get(deviceID)?.status || '') }}
                </a-tag>
              </a-space>
            </template>

            <a-row :gutter="12">
              <a-col :span="24">
                <a-form-item label="算法（可多选）" required>
                  <a-select
                    v-model:value="ensureDeviceConfig(deviceID).algorithm_ids"
                    mode="multiple"
                    allow-clear
                    :options="algorithms.map((item) => ({ label: item.name, value: item.id }))"
                    placeholder="请选择算法"
                    @change="syncAlgorithmCycleMap(ensureDeviceConfig(deviceID))"
                  />
                </a-form-item>
              </a-col>
            </a-row>

            <a-form-item label="每算法配置（报警周期 + 报警等级）" required>
              <a-empty v-if="ensureDeviceConfig(deviceID).algorithm_ids.length === 0" description="请先选择算法" />
              <a-row v-else :gutter="[12, 8]">
                <a-col
                  v-for="algorithmID in ensureDeviceConfig(deviceID).algorithm_ids"
                  :key="`${deviceID}-${algorithmID}`"
                  :span="24"
                >
                  <a-row :gutter="12" align="middle">
                    <a-col :span="8">
                      <span>
                        {{
                          algorithms.find((item) => item.id === algorithmID)?.name
                          || algorithmID
                        }}
                      </span>
                    </a-col>
                    <a-col :span="8">
                      <a-space style="width: 100%">
                        <a-input-number
                          v-model:value="ensureDeviceConfig(deviceID).algorithm_cycle_map[algorithmID]"
                          :min="0"
                          :max="86400"
                          style="width: calc(100% - 40px)"
                        />
                        <span>秒</span>
                      </a-space>
                    </a-col>
                    <a-col :span="8">
                      <a-select
                        v-model:value="ensureDeviceConfig(deviceID).algorithm_level_map[algorithmID]"
                        :options="alarmLevelOptions"
                        style="width: 100%"
                      />
                    </a-col>
                  </a-row>
                </a-col>
              </a-row>
            </a-form-item>

            <a-row :gutter="12">
              <a-col :span="8">
                <a-form-item label="抽帧模式">
                  <a-select v-model:value="ensureDeviceConfig(deviceID).frame_rate_mode" :options="frameRateModeOptions" />
                </a-form-item>
              </a-col>
              <a-col :span="8">
                <a-form-item :label="ensureDeviceConfig(deviceID).frame_rate_mode === 'interval' ? '每几秒1帧' : '每秒几帧'">
                  <a-input-number
                    v-model:value="ensureDeviceConfig(deviceID).frame_rate_value"
                    :min="1"
                    :max="60"
                    style="width: 100%"
                  />
                </a-form-item>
              </a-col>
            </a-row>

            <a-row :gutter="12">
              <a-col :span="ensureDeviceConfig(deviceID).recording_policy === 'alarm_clip' ? 8 : 24">
                <a-form-item label="录制策略">
                  <a-select v-model:value="ensureDeviceConfig(deviceID).recording_policy" :options="recordingPolicyOptions" />
                </a-form-item>
              </a-col>
              <a-col v-if="ensureDeviceConfig(deviceID).recording_policy === 'alarm_clip'" :span="8">
                <a-form-item label="报警前秒数">
                  <a-input-number
                    v-model:value="ensureDeviceConfig(deviceID).recording_pre_seconds"
                    :min="1"
                    :max="600"
                    style="width: 100%"
                  />
                </a-form-item>
              </a-col>
              <a-col v-if="ensureDeviceConfig(deviceID).recording_policy === 'alarm_clip'" :span="8">
                <a-form-item label="报警后秒数">
                  <a-input-number
                    v-model:value="ensureDeviceConfig(deviceID).recording_post_seconds"
                    :min="1"
                    :max="600"
                    style="width: 100%"
                  />
                </a-form-item>
              </a-col>
            </a-row>
          </a-card>
        </a-space>
      </a-form>
    </a-modal>

    <a-modal v-model:open="promptPreviewOpen" title="提示词预览" :footer="null" width="900px">
      <pre class="preview">{{ JSON.stringify(promptPreview, null, 2) }}</pre>
    </a-modal>
  </div>
</template>

<style scoped>
.preview {
  max-height: 560px;
  overflow: auto;
  background: #f7fbf3;
  border: 1px solid #d6e4c4;
  border-radius: 8px;
  padding: 10px;
}

.hint {
  margin-top: 6px;
  color: #6d7a5f;
  font-size: 12px;
}
</style>
