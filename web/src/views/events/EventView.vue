<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { message } from 'ant-design-vue'
import { eventAPI, yoloLabelAPI } from '@/api/modules'
import { appendTokenQuery } from '@/api/request'
import { formatDateTime } from '@/utils/datetime'

type EventItem = {
  id: string
  task_id: string
  device_id: string
  algorithm_id: string
  algorithm_code?: string
  task_name?: string
  device_name?: string
  algorithm_name?: string
  alarm_level_id: string
  alarm_level_name?: string
  alarm_level_color?: string
  alarm_level_severity?: number
  area_id?: string
  area_name?: string
  status: string
  review_note: string
  occurred_at: string
  snapshot_path: string
  reviewed_by: string
  reviewed_at: string
  clip_ready?: boolean
  clip_files_json?: string
}

type EventDetail = EventItem & {
  snapshot_width: number
  snapshot_height: number
  boxes_json: string
  yolo_json: string
  llm_json: string
  source_callback: string
  clip_path?: string
}

type Label = {
  label: string
  name: string
}

type NormalizedBox = {
  label: string
  confidence: number
  x: number
  y: number
  w: number
  h: number
}

const route = useRoute()
const loading = ref(false)
const reviewSubmitting = ref(false)
const events = ref<EventItem[]>([])
const yoloLabels = ref<Label[]>([])

const filters = reactive({
  status: '',
  task_name: '',
  device_name: '',
  algorithm_name: '',
})

const pager = reactive({
  page: 1,
  page_size: 10,
  total: 0,
})

const detailModal = ref(false)
const detailData = ref<EventDetail | null>(null)
const showBoxes = ref(true)
const selectedClipPath = ref('')
const clipPendingTimeoutMs = 2 * 60 * 1000
const reviewForm = reactive({
  status: 'valid',
  review_note: '',
})

const imageBase = import.meta.env.VITE_API_BASE_URL || ''

const yoloLabelNameMap = computed(() => {
  const out = new Map<string, string>()
  for (const item of yoloLabels.value) {
    const key = String(item?.label || '').trim().toLowerCase()
    const name = String(item?.name || '').trim()
    if (!key || !name) continue
    out.set(key, name)
  }
  return out
})

const detailBoxes = computed(() => {
  if (!detailData.value?.boxes_json) return [] as NormalizedBox[]
  try {
    const parsed = JSON.parse(detailData.value.boxes_json)
    return Array.isArray(parsed) ? (parsed as NormalizedBox[]) : []
  } catch {
    return [] as NormalizedBox[]
  }
})

const snapshotURL = computed(() => eventImageURL(detailData.value?.snapshot_path || ''))

const clipPaths = computed(() => {
  const raw = String(detailData.value?.clip_files_json || '').trim()
  if (!raw) return [] as string[]
  try {
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return [] as string[]
    return parsed.map((item) => String(item || '').trim()).filter(Boolean)
  } catch {
    return [] as string[]
  }
})

const clipVideoURL = computed(() => {
  if (!detailData.value) return ''
  const eventID = String(detailData.value.id || '').trim()
  const clipPath = String(selectedClipPath.value || '').trim()
  if (!eventID || !clipPath) return ''
  return appendTokenQuery(eventAPI.clipFileURL(eventID, clipPath))
})

const clipPendingExpired = computed(() => {
  const detail = detailData.value
  if (!detail || detail.clip_ready) return false
  const rawOccurredAt = String(detail.occurred_at || '').trim()
  if (!rawOccurredAt) return false
  const occurredAtMS = Date.parse(rawOccurredAt)
  if (Number.isNaN(occurredAtMS)) return false
  return Date.now() - occurredAtMS >= clipPendingTimeoutMs
})

function eventImageURL(path: string) {
  const rawPath = String(path || '').trim()
  if (!rawPath) return ''
  return withAuthURL(`${imageBase}/api/v1/events/image/${rawPath}`)
}

function withAuthURL(url: string) {
  const target = String(url || '').trim()
  if (!target) return ''
  if (target.startsWith('/api/')) return appendTokenQuery(target)
  if (imageBase && target.startsWith(`${imageBase}/api/`)) return appendTokenQuery(target)
  const origin = window.location.origin
  if (target.startsWith(`${origin}/api/`)) return appendTokenQuery(target)
  return target
}

function eventStatusText(status: string) {
  if (status === 'pending') return '待处理'
  if (status === 'valid') return '有效'
  if (status === 'invalid') return '无效'
  return status || '-'
}

function alarmLevelText(item: Pick<EventItem, 'alarm_level_name' | 'alarm_level_severity' | 'alarm_level_id'>) {
  const name = String(item.alarm_level_name || '').trim()
  if (name) return name
  return String(item.alarm_level_id || '-').trim() || '-'
}

function alarmLevelColor(color: string) {
  const normalized = String(color || '').trim()
  return normalized || 'default'
}

function areaText(item: Pick<EventItem, 'area_name' | 'area_id'>) {
  const areaName = String(item.area_name || '').trim()
  if (areaName) return areaName
  const areaID = String(item.area_id || '').trim()
  return areaID || '-'
}

async function load(page = pager.page, pageSize = pager.page_size) {
  loading.value = true
  try {
    const params = {
      ...Object.fromEntries(Object.entries(filters).filter(([, v]) => String(v || '').trim())),
      page,
      page_size: pageSize,
    }
    const data = (await eventAPI.list(params)) as {
      items: EventItem[]
      total: number
      page?: number
      page_size?: number
    }
    events.value = data.items || []
    pager.total = Number(data.total || 0)
    pager.page = Number(data.page || page)
    pager.page_size = Number(data.page_size || pageSize)
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}

function onSearch() {
  void load(1, pager.page_size)
}

function onTableChange(pagination: { current?: number; pageSize?: number }) {
  const nextPage = Number(pagination?.current || 1)
  const nextPageSize = Number(pagination?.pageSize || pager.page_size || 10)
  void load(nextPage, nextPageSize)
}

async function loadYoloLabels() {
  try {
    const data = (await yoloLabelAPI.list()) as { items: Label[] }
    yoloLabels.value = data.items || []
  } catch {
    yoloLabels.value = []
  }
}

async function showDetail(id: string) {
  try {
    const event = (await eventAPI.detail(id)) as EventDetail
    detailData.value = event
    showBoxes.value = true
    reviewForm.status = event.status === 'invalid' ? 'invalid' : event.status === 'pending' ? 'pending' : 'valid'
    reviewForm.review_note = event.review_note || ''

    const clips = parseClipFilesFromDetail(event)
    selectedClipPath.value = clips[0] || ''
    detailModal.value = true
  } catch (err) {
    message.error((err as Error).message)
  }
}

function parseClipFilesFromDetail(item: EventDetail | null) {
  const raw = String(item?.clip_files_json || '').trim()
  if (!raw) return [] as string[]
  try {
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return [] as string[]
    return parsed.map((row) => String(row || '').trim()).filter(Boolean)
  } catch {
    return [] as string[]
  }
}

async function submitReview() {
  if (!detailData.value) return
  reviewSubmitting.value = true
  try {
    await eventAPI.review(detailData.value.id, {
      status: reviewForm.status,
      review_note: reviewForm.review_note,
    })
    message.success('事件审核已提交')
    await Promise.all([showDetail(detailData.value.id), load(pager.page, pager.page_size)])
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    reviewSubmitting.value = false
  }
}

function formatYoloLabelName(label: string) {
  const raw = String(label || '').trim()
  if (!raw) return '-'
  return yoloLabelNameMap.value.get(raw.toLowerCase()) || raw
}

function clamp01(v: number) {
  if (!Number.isFinite(v)) return 0
  if (v < 0) return 0
  if (v > 1) return 1
  return v
}

function normalizedBoxStyle(item: NormalizedBox) {
  const w = clamp01(Number(item.w || 0))
  const h = clamp01(Number(item.h || 0))
  const x = clamp01(Number(item.x || 0))
  const y = clamp01(Number(item.y || 0))
  const left = Math.max(0, x - w / 2)
  const top = Math.max(0, y - h / 2)
  return {
    left: `${left * 100}%`,
    top: `${top * 100}%`,
    width: `${w * 100}%`,
    height: `${h * 100}%`,
  }
}

function clipName(path: string) {
  const raw = String(path || '').trim()
  if (!raw) return '-'
  const parts = raw.split('/').filter(Boolean)
  return parts[parts.length - 1] || raw
}

function formatAlgorithmText(record: Pick<EventItem, 'algorithm_code' | 'algorithm_name' | 'algorithm_id'>) {
  const code = String(record.algorithm_code || '').trim()
  const name = String(record.algorithm_name || '').trim()
  const id = String(record.algorithm_id || '').trim()
  if (code && name) return `${code} / ${name}`
  if (code) return code
  if (name) return name
  return id || '-'
}

watch(
  () => route.query.focus,
  (focusID) => {
    if (focusID && typeof focusID === 'string') {
      void showDetail(focusID)
    }
  },
  { immediate: true },
)

watch(
  clipPaths,
  (paths) => {
    if (paths.length === 0) {
      selectedClipPath.value = ''
      return
    }
    if (!paths.includes(selectedClipPath.value)) {
      selectedClipPath.value = paths[0]
    }
  },
  { immediate: true },
)

onMounted(() => {
  void load(1, pager.page_size)
  void loadYoloLabels()
})
</script>

<template>
  <div>
    <h2 class="page-title">视频告警事件</h2>
    <p class="page-subtitle">支持按名称筛选事件，在同一弹窗完成详情查看与审核。</p>

    <a-card class="glass-card">
      <div class="table-toolbar">
        <a-space wrap>
          <a-select
            v-model:value="filters.status"
            style="width: 160px"
            allow-clear
            placeholder="状态"
            :options="[
              { label: '待处理', value: 'pending' },
              { label: '有效', value: 'valid' },
              { label: '无效', value: 'invalid' },
            ]"
          />
          <a-input v-model:value="filters.task_name" placeholder="任务名称关键字" style="width: 200px" />
          <a-input v-model:value="filters.device_name" placeholder="设备名称关键字" style="width: 200px" />
          <a-input v-model:value="filters.algorithm_name" placeholder="算法名称关键字" style="width: 200px" />
          <a-button type="primary" @click="onSearch">查询</a-button>
        </a-space>
      </div>

      <a-table
        :data-source="events"
        row-key="id"
        :loading="loading"
        :pagination="{
          current: pager.page,
          pageSize: pager.page_size,
          total: pager.total,
          showSizeChanger: true,
          showTotal: (total: number) => `共 ${total} 条`,
        }"
        @change="onTableChange"
      >
        <a-table-column title="发生时间">
          <template #default="{ record }">{{ formatDateTime(record.occurred_at) }}</template>
        </a-table-column>
        <a-table-column title="任务">
          <template #default="{ record }">{{ record.task_name || record.task_id || '-' }}</template>
        </a-table-column>
        <a-table-column title="设备">
          <template #default="{ record }">{{ record.device_name || record.device_id || '-' }}</template>
        </a-table-column>
        <a-table-column title="算法">
          <template #default="{ record }">{{ formatAlgorithmText(record) }}</template>
        </a-table-column>
        <a-table-column title="报警等级" width="160">
          <template #default="{ record }">
            <a-tag :color="alarmLevelColor(record.alarm_level_color || '')">
              {{ alarmLevelText(record) }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="区域" width="160">
          <template #default="{ record }">{{ areaText(record) }}</template>
        </a-table-column>
        <a-table-column title="状态">
          <template #default="{ record }">
            <a-tag :color="record.status === 'valid' ? 'green' : record.status === 'invalid' ? 'red' : 'gold'">
              {{ eventStatusText(record.status) }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="快照" width="96">
          <template #default="{ record }">
            <a-image
              v-if="record.snapshot_path"
              :src="eventImageURL(record.snapshot_path)"
              :width="68"
              :height="44"
              style="object-fit: cover; border-radius: 6px"
            />
            <span v-else>-</span>
          </template>
        </a-table-column>
        <a-table-column title="操作" width="140">
          <template #default="{ record }">
            <a-button size="small" type="primary" @click="showDetail(record.id)">详情/审核</a-button>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal
      v-model:open="detailModal"
      title="事件详情与审核"
      width="1180px"
      ok-text="提交审核"
      :confirm-loading="reviewSubmitting"
      @ok="submitReview"
    >
      <a-row :gutter="14">
        <a-col :xs="24" :xl="14">
          <a-card size="small" title="快照与框选结果">
            <template #extra>
              <a-button size="small" :disabled="detailBoxes.length === 0" @click="showBoxes = !showBoxes">
                {{ showBoxes ? '隐藏框选结果' : '显示框选结果' }}
              </a-button>
            </template>
            <div v-if="snapshotURL" class="snapshot-wrap">
              <img :src="snapshotURL" class="snapshot-image" alt="snapshot" />
              <template v-if="showBoxes">
                <div
                  v-for="(box, idx) in detailBoxes"
                  :key="`${box.label}-${idx}`"
                  class="box"
                  :style="normalizedBoxStyle(box)"
                >
                  <span class="box-label">{{ formatYoloLabelName(box.label) }} {{ (box.confidence * 100).toFixed(1) }}%</span>
                </div>
              </template>
            </div>
            <a-empty v-else description="暂无快照" />
          </a-card>
        </a-col>

        <a-col :xs="24" :xl="10">
          <a-card size="small" title="报警片段">
            <template v-if="detailData?.clip_ready && clipPaths.length > 0">
              <video v-if="clipVideoURL" class="clip-player" :src="clipVideoURL" controls preload="metadata" />
              <a-segmented
                v-model:value="selectedClipPath"
                :options="clipPaths.map((item) => ({ label: clipName(item), value: item }))"
                block
                style="margin-top: 10px"
              />
            </template>
            <template v-else-if="detailData?.clip_ready">
              <a-empty description="暂无可播放片段" />
            </template>
            <template v-else-if="clipPendingExpired">
              <a-result status="warning" title="片段未生成" sub-title="请检查录制配置或后端日志。" />
            </template>
            <template v-else>
              <a-result status="warning" title="片段生成中" sub-title="请稍后刷新事件详情。" />
            </template>
          </a-card>

          <a-card size="small" title="审核" style="margin-top: 12px">
            <a-form layout="vertical">
              <a-form-item label="审核结果">
                <a-radio-group v-model:value="reviewForm.status">
                  <a-radio value="valid">有效告警</a-radio>
                  <a-radio value="invalid">无效告警</a-radio>
                  <a-radio value="pending">待处理</a-radio>
                </a-radio-group>
              </a-form-item>
              <a-form-item label="审核备注">
                <a-textarea v-model:value="reviewForm.review_note" :rows="3" />
              </a-form-item>
            </a-form>
          </a-card>

          <a-card size="small" title="基础信息" style="margin-top: 12px">
            <div class="field"><span>ID</span><code>{{ detailData?.id }}</code></div>
            <div class="field"><span>状态</span><code>{{ eventStatusText(detailData?.status || '') }}</code></div>
            <div class="field"><span>任务</span><code>{{ detailData?.task_name || detailData?.task_id || '-' }}</code></div>
            <div class="field"><span>设备</span><code>{{ detailData?.device_name || detailData?.device_id || '-' }}</code></div>
            <div class="field"><span>算法</span><code>{{ formatAlgorithmText(detailData || { algorithm_id: '' }) }}</code></div>
            <div class="field"><span>报警等级</span><code>{{ detailData ? alarmLevelText(detailData) : '-' }}</code></div>
            <div class="field"><span>区域</span><code>{{ detailData ? areaText(detailData) : '-' }}</code></div>
            <div class="field"><span>发生时间</span><code>{{ formatDateTime(detailData?.occurred_at || '') }}</code></div>
          </a-card>
        </a-col>
      </a-row>
    </a-modal>
  </div>
</template>

<style scoped>
.snapshot-wrap {
  position: relative;
  width: 100%;
  background: #111;
  border-radius: 8px;
  overflow: hidden;
  min-height: 280px;
}

.snapshot-image {
  width: 100%;
  display: block;
}

.box {
  position: absolute;
  border: 2px solid #ff4d4f;
  box-sizing: border-box;
}

.box-label {
  position: absolute;
  left: 0;
  top: -22px;
  background: rgba(255, 77, 79, 0.9);
  color: #fff;
  padding: 2px 6px;
  border-radius: 6px;
  font-size: 11px;
  white-space: nowrap;
}

.clip-player {
  width: 100%;
  max-height: 240px;
  border-radius: 8px;
  background: #000;
}

.field {
  display: flex;
  gap: 10px;
  align-items: baseline;
  margin-bottom: 6px;
}

.field span {
  width: 70px;
  color: #6d7b5c;
}
</style>
