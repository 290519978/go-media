<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import { alarmLevelAPI, algorithmAPI, deviceAPI, taskAPI } from '@/api/modules'

type Device = { id: string; name: string; ai_status: string }
type Algorithm = { id: string; name: string; mode: string }
type AlarmLevel = { id: string; name: string; severity: number; color: string; description: string }
type Task = {
  task: {
    id: string
    name: string
    status: string
    frame_interval: number
    small_confidence: number
    large_confidence: number
    small_iou: number
    alarm_level_id: string
    notes: string
  }
  device_ids: string[]
  algorithm_ids: string[]
}

type TaskStartResponse = {
  status?: string
  message?: string
}

const tabKey = ref('tasks')
const loading = ref(false)
const tasks = ref<Task[]>([])
const devices = ref<Device[]>([])
const algorithms = ref<Algorithm[]>([])
const levels = ref<AlarmLevel[]>([])

const modalOpen = ref(false)
const editingID = ref('')
const form = reactive({
  name: '',
  device_ids: [] as string[],
  algorithm_ids: [] as string[],
  frame_interval: 5,
  small_confidence: 0.5,
  large_confidence: 0.8,
  small_iou: 0.8,
  alarm_level_id: '',
  notes: '',
})

const promptPreviewOpen = ref(false)
const promptPreview = ref<any>(null)

const levelModal = ref(false)
const editingLevelID = ref('')
const levelForm = reactive({
  name: '',
  severity: 1,
  color: '#faad14',
  description: '',
})

const levelMap = computed(() => {
  const out = new Map<string, AlarmLevel>()
  for (const item of levels.value) out.set(item.id, item)
  return out
})

const occupiedMap = computed(() => {
  const out = new Map<string, string>()
  for (const task of tasks.value) {
    for (const deviceID of task.device_ids || []) {
      out.set(deviceID, task.task.id)
    }
  }
  return out
})

const editingTaskDeviceSet = computed(() => {
  if (!editingID.value) return new Set<string>()
  const hit = tasks.value.find((item) => item.task.id === editingID.value)
  return new Set((hit?.device_ids || []).map((id) => String(id)))
})

const availableDevices = computed(() => {
  return devices.value.filter((item) => {
    if (editingTaskDeviceSet.value.has(item.id)) return true
    return !occupiedMap.value.has(item.id)
  })
})

const unavailableCount = computed(() => {
  let count = 0
  for (const item of devices.value) {
    if (editingTaskDeviceSet.value.has(item.id)) continue
    if (occupiedMap.value.has(item.id)) count += 1
  }
  return count
})

function taskStatusText(status: string) {
  if (status === 'running') return '运行中'
  if (status === 'partial_fail') return '部分失败'
  if (status === 'stopped') return '已停止'
  if (status === 'pending') return '等待中'
  return status || '-'
}

function algorithmModeText(mode: string) {
  if (mode === 'small') return '小模型'
  if (mode === 'large') return '大模型'
  if (mode === 'hybrid') return '小模型 + 大模型'
  return mode || '-'
}

async function loadAll() {
  loading.value = true
  try {
    const [t, d, a, l] = await Promise.all([
      taskAPI.list() as Promise<{ items: Task[] }>,
      deviceAPI.list() as Promise<{ items: Device[] }>,
      algorithmAPI.list() as Promise<{ items: any[] }>,
      alarmLevelAPI.list() as Promise<{ items: AlarmLevel[] }>,
    ])
    tasks.value = t.items || []
    devices.value = d.items || []
    algorithms.value = (a.items || []).map((item: any) => item.algorithm ? item.algorithm : item)
    levels.value = (l.items || []).sort((x, y) => x.severity - y.severity)
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingID.value = ''
  Object.assign(form, {
    name: '',
    device_ids: [],
    algorithm_ids: [],
    frame_interval: 5,
    small_confidence: 0.5,
    large_confidence: 0.8,
    small_iou: 0.8,
    alarm_level_id: levels.value[0]?.id || '',
    notes: '',
  })
  modalOpen.value = true
}

function openEdit(row: Task) {
  editingID.value = row.task.id
  Object.assign(form, {
    name: row.task.name,
    device_ids: row.device_ids || [],
    algorithm_ids: row.algorithm_ids || [],
    frame_interval: row.task.frame_interval,
    small_confidence: row.task.small_confidence,
    large_confidence: row.task.large_confidence,
    small_iou: row.task.small_iou,
    alarm_level_id: row.task.alarm_level_id,
    notes: row.task.notes || '',
  })
  modalOpen.value = true
}

async function submit() {
  try {
    if (editingID.value) {
      await taskAPI.update(editingID.value, form)
      message.success('任务已更新')
    } else {
      await taskAPI.create(form)
      message.success('任务已创建')
    }
    modalOpen.value = false
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
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
  }
}

async function stop(id: string) {
  try {
    await taskAPI.stop(id)
    message.success('任务停止请求已发送')
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
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
  try {
    const data = await taskAPI.promptPreview(id)
    promptPreview.value = data
    promptPreviewOpen.value = true
  } catch (err) {
    message.error((err as Error).message)
  }
}

function openCreateLevel() {
  editingLevelID.value = ''
  Object.assign(levelForm, {
    name: '',
    severity: levels.value.length + 1,
    color: '#faad14',
    description: '',
  })
  levelModal.value = true
}

function openEditLevel(row: AlarmLevel) {
  editingLevelID.value = row.id
  Object.assign(levelForm, {
    name: row.name,
    severity: row.severity,
    color: row.color || '#faad14',
    description: row.description || '',
  })
  levelModal.value = true
}

async function submitLevel() {
  try {
    if (editingLevelID.value) {
      await alarmLevelAPI.update(editingLevelID.value, levelForm)
      message.success('告警等级已更新')
    } else {
      await alarmLevelAPI.create(levelForm)
      message.success('告警等级已创建')
    }
    levelModal.value = false
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function removeLevel(id: string) {
  try {
    await alarmLevelAPI.remove(id)
    message.success('告警等级已删除')
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  }
}

onMounted(loadAll)
</script>

<template>
  <div>
    <h2 class="page-title">任务管理</h2>
    <p class="page-subtitle">任务编排、设备占用控制与告警等级策略管理。</p>

    <a-card class="glass-card">
      <a-tabs v-model:active-key="tabKey">
        <a-tab-pane key="tasks" tab="视频任务">
          <div class="table-toolbar">
            <a-space>
              <a-button type="primary" @click="openCreate">新增任务</a-button>
              <a-button @click="loadAll">刷新</a-button>
            </a-space>
          </div>
          <a-table :data-source="tasks" row-key="task.id" :loading="loading" :pagination="{ pageSize: 8 }">
            <a-table-column title="名称">
              <template #default="{ record }">{{ record.task.name }}</template>
            </a-table-column>
            <a-table-column title="状态">
              <template #default="{ record }">
                <a-tag :color="record.task.status === 'running' ? 'green' : record.task.status === 'partial_fail' ? 'gold' : 'default'">
                  {{ taskStatusText(record.task.status) }}
                </a-tag>
              </template>
            </a-table-column>
            <a-table-column title="设备数">
              <template #default="{ record }">{{ record.device_ids?.length || 0 }}</template>
            </a-table-column>
            <a-table-column title="算法数">
              <template #default="{ record }">{{ record.algorithm_ids?.length || 0 }}</template>
            </a-table-column>
            <a-table-column title="告警等级">
              <template #default="{ record }">
                <a-tag :color="levelMap.get(record.task.alarm_level_id)?.color || 'default'">
                  {{ levelMap.get(record.task.alarm_level_id)?.name || record.task.alarm_level_id }}
                </a-tag>
              </template>
            </a-table-column>
            <a-table-column title="抽帧间隔">
              <template #default="{ record }">{{ record.task.frame_interval }}</template>
            </a-table-column>
            <a-table-column title="操作" width="420">
              <template #default="{ record }">
                <a-space wrap>
                  <a-button size="small" :disabled="record.task.status === 'running'" @click="openEdit(record)">编辑</a-button>
                  <a-button size="small" type="primary" @click="start(record.task.id)">启动</a-button>
                  <a-button size="small" @click="stop(record.task.id)">停止</a-button>
                  <a-button size="small" @click="syncStatus(record.task.id)">同步状态</a-button>
                  <a-button size="small" @click="showPromptPreview(record.task.id)">提示词预览</a-button>
                  <a-popconfirm title="确定删除该任务？" @confirm="remove(record.task.id)">
                    <a-button size="small" danger>删除</a-button>
                  </a-popconfirm>
                </a-space>
              </template>
            </a-table-column>
          </a-table>
        </a-tab-pane>

        <a-tab-pane key="levels" tab="告警等级">
          <div class="table-toolbar">
            <a-space>
              <a-button type="primary" @click="openCreateLevel">新增告警等级</a-button>
              <a-button @click="loadAll">刷新</a-button>
            </a-space>
          </div>
          <a-table :data-source="levels" row-key="id" :loading="loading" :pagination="false">
            <a-table-column title="名称" data-index="name" />
            <a-table-column title="级别" data-index="severity" width="100" />
            <a-table-column title="颜色" width="120">
              <template #default="{ record }">
                <a-tag :color="record.color">{{ record.color }}</a-tag>
              </template>
            </a-table-column>
            <a-table-column title="描述" data-index="description" />
            <a-table-column title="操作" width="180">
              <template #default="{ record }">
                <a-space>
                  <a-button size="small" @click="openEditLevel(record)">编辑</a-button>
                  <a-popconfirm title="确定删除该告警等级？" @confirm="removeLevel(record.id)">
                    <a-button size="small" danger>删除</a-button>
                  </a-popconfirm>
                </a-space>
              </template>
            </a-table-column>
          </a-table>
        </a-tab-pane>
      </a-tabs>
    </a-card>

    <a-modal v-model:open="modalOpen" :title="editingID ? '编辑任务' : '新增任务'" width="760px" @ok="submit">
      <a-form layout="vertical">
        <a-form-item label="任务名称"><a-input v-model:value="form.name" /></a-form-item>
        <a-form-item label="设备（已被占用设备已过滤）">
          <a-select
            v-model:value="form.device_ids"
            mode="multiple"
            :options="availableDevices.map((d) => ({ label: d.name, value: d.id }))"
          />
          <div class="hint">
            已隐藏占用设备：{{ unavailableCount }}
          </div>
        </a-form-item>
        <a-form-item label="算法">
          <a-select
            v-model:value="form.algorithm_ids"
            mode="multiple"
            :options="algorithms.map((a) => ({ label: `${a.name} (${algorithmModeText(a.mode)})`, value: a.id }))"
          />
        </a-form-item>
        <a-row :gutter="12">
          <a-col :span="8">
            <a-form-item label="抽帧间隔">
              <a-input-number v-model:value="form.frame_interval" :min="1" style="width: 100%" />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="小模型置信度">
              <a-input-number
                v-model:value="form.small_confidence"
                :min="0.01"
                :max="0.99"
                :step="0.01"
                style="width: 100%"
              />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="大模型置信度">
              <a-input-number
                v-model:value="form.large_confidence"
                :min="0.01"
                :max="0.99"
                :step="0.01"
                style="width: 100%"
              />
            </a-form-item>
          </a-col>
        </a-row>
        <a-row :gutter="12">
          <a-col :span="8">
            <a-form-item label="小模型 IoU">
              <a-input-number v-model:value="form.small_iou" :min="0.1" :max="0.99" :step="0.01" style="width: 100%" />
            </a-form-item>
          </a-col>
          <a-col :span="16">
            <a-form-item label="告警等级">
              <a-select
                v-model:value="form.alarm_level_id"
                :options="levels.map((l) => ({ label: `${l.name} (S${l.severity})`, value: l.id }))"
              />
            </a-form-item>
          </a-col>
        </a-row>
        <a-form-item label="备注"><a-textarea v-model:value="form.notes" :rows="2" /></a-form-item>
      </a-form>
    </a-modal>

    <a-modal v-model:open="levelModal" :title="editingLevelID ? '编辑告警等级' : '新增告警等级'" @ok="submitLevel">
      <a-form layout="vertical">
        <a-form-item label="名称"><a-input v-model:value="levelForm.name" /></a-form-item>
        <a-form-item label="级别"><a-input-number v-model:value="levelForm.severity" :min="1" :max="10" style="width: 100%" /></a-form-item>
        <a-form-item label="颜色"><a-input v-model:value="levelForm.color" placeholder="#faad14" /></a-form-item>
        <a-form-item label="描述"><a-textarea v-model:value="levelForm.description" :rows="2" /></a-form-item>
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
