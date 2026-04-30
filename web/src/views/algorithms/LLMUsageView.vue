<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import { llmUsageAPI, type LLMUsageQueryParams } from '@/api/modules'
import { formatDateTime } from '@/utils/datetime'

type SummaryData = {
  call_count: number
  success_count: number
  empty_content_count: number
  error_count: number
  usage_available_count: number
  usage_missing_count: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  avg_total_tokens_per_call: number
}

type HourlyItem = {
  bucket_start: string
  call_count: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  usage_missing_count: number
}

type DailyItem = {
  bucket_date: string
  call_count: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  usage_missing_count: number
}

type CallItem = {
  id: string
  source: string
  task_id: string
  task_name: string
  device_id: string
  device_name: string
  provider_id: string
  provider_name: string
  model: string
  detect_mode: number
  call_status: string
  usage_available: boolean
  prompt_tokens?: number | null
  completion_tokens?: number | null
  total_tokens?: number | null
  latency_ms: number
  error_message: string
  request_context: string
  occurred_at: string
}

const summary = ref<SummaryData>({
  call_count: 0,
  success_count: 0,
  empty_content_count: 0,
  error_count: 0,
  usage_available_count: 0,
  usage_missing_count: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  total_tokens: 0,
  avg_total_tokens_per_call: 0,
})
const hourlyItems = ref<HourlyItem[]>([])
const dailyItems = ref<DailyItem[]>([])
const callItems = ref<CallItem[]>([])

const loadingSummary = ref(false)
const loadingHourly = ref(false)
const loadingDaily = ref(false)
const loadingCalls = ref(false)

const filters = reactive({
  rangePreset: '24h',
  source: '',
  call_status: '',
  usage_available: '',
})

const pager = reactive({
  page: 1,
  pageSize: 20,
  total: 0,
})

const rangeOptions = [
  { label: '最近24小时', value: '24h' },
  { label: '最近7天', value: '7d' },
  { label: '最近30天', value: '30d' },
]

const sourceOptions = [
  { label: '全部来源', value: '' },
  { label: '运行任务', value: 'task_runtime' },
  { label: '算法测试', value: 'algorithm_test' },
  { label: '直接分析', value: 'direct_analyze' },
]

const callStatusOptions = [
  { label: '全部状态', value: '' },
  { label: '成功', value: 'success' },
  { label: '空结果', value: 'empty_content' },
  { label: '失败', value: 'error' },
]

const usageOptions = [
  { label: '全部 usage 状态', value: '' },
  { label: '已返回 usage', value: 'true' },
  { label: 'usage 缺失', value: 'false' },
]

function buildTimeRange() {
  const end = new Date()
  const start = new Date(end)
  switch (filters.rangePreset) {
    case '7d':
      start.setDate(start.getDate() - 7)
      break
    case '30d':
      start.setDate(start.getDate() - 30)
      break
    default:
      start.setHours(start.getHours() - 24)
      break
  }
  return {
    start_at: start.toISOString(),
    end_at: end.toISOString(),
  }
}

function buildQueryParams(extra?: Partial<LLMUsageQueryParams>): LLMUsageQueryParams {
  const range = buildTimeRange()
  return {
    ...range,
    source: filters.source || undefined,
    call_status: filters.call_status || undefined,
    usage_available: filters.usage_available || undefined,
    ...extra,
  }
}

async function loadSummary() {
  loadingSummary.value = true
  try {
    const data = await llmUsageAPI.summary(buildQueryParams()) as { summary: SummaryData }
    summary.value = data.summary || { ...summary.value }
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loadingSummary.value = false
  }
}

async function loadHourly() {
  loadingHourly.value = true
  try {
    const data = await llmUsageAPI.hourly(buildQueryParams()) as { items: HourlyItem[] }
    hourlyItems.value = data.items || []
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loadingHourly.value = false
  }
}

async function loadDaily() {
  loadingDaily.value = true
  try {
    const data = await llmUsageAPI.daily(buildQueryParams()) as { items: DailyItem[] }
    dailyItems.value = data.items || []
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loadingDaily.value = false
  }
}

async function loadCalls(page = pager.page, pageSize = pager.pageSize) {
  loadingCalls.value = true
  try {
    const data = await llmUsageAPI.calls(buildQueryParams({
      page,
      page_size: pageSize,
    })) as {
      items: CallItem[]
      total: number
      page: number
      page_size: number
    }
    callItems.value = data.items || []
    pager.total = Number(data.total || 0)
    pager.page = Number(data.page || page)
    pager.pageSize = Number(data.page_size || pageSize)
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loadingCalls.value = false
  }
}

async function loadAll(resetPage = true) {
  if (resetPage) {
    pager.page = 1
  }
  await Promise.all([
    loadSummary(),
    loadHourly(),
    loadDaily(),
    loadCalls(pager.page, pager.pageSize),
  ])
}

function handleSearch() {
  void loadAll(true)
}

function handleReset() {
  filters.rangePreset = '24h'
  filters.source = ''
  filters.call_status = ''
  filters.usage_available = ''
  void loadAll(true)
}

function handlePageChange(page: number, pageSize: number) {
  pager.page = page
  pager.pageSize = pageSize
  void loadCalls(page, pageSize)
}

function formatSource(value: string) {
  switch (value) {
    case 'task_runtime':
      return '运行任务'
    case 'algorithm_test':
      return '算法测试'
    case 'direct_analyze':
      return '直接分析'
    default:
      return value || '-'
  }
}

function formatStatus(value: string) {
  switch (value) {
    case 'success':
      return '成功'
    case 'empty_content':
      return '空结果'
    case 'error':
      return '失败'
    default:
      return value || '-'
  }
}

function safeNumber(value: number | null | undefined) {
  return value == null ? '-' : value
}

function formatDay(value: string) {
  return String(value || '').slice(0, 10) || '-'
}

onMounted(async () => {
  await loadAll(true)
})
</script>

<template>
  <div>
    <h2 class="page-title">LLM用量统计</h2>
    <p class="page-subtitle">查看 LLM 调用总量、每小时、每天和每次调用明细。</p>

    <a-card class="glass-card">
      <div class="filters-grid">
        <a-select v-model:value="filters.rangePreset" :options="rangeOptions" />
        <a-select v-model:value="filters.source" :options="sourceOptions" />
        <a-select v-model:value="filters.call_status" :options="callStatusOptions" />
        <a-select v-model:value="filters.usage_available" :options="usageOptions" />
      </div>
      <div class="table-toolbar">
        <a-space>
          <a-button type="primary" @click="handleSearch">查询</a-button>
          <a-button @click="handleReset">重置</a-button>
        </a-space>
      </div>
    </a-card>

    <div class="stats-grid">
      <a-card class="glass-card" :loading="loadingSummary">
        <a-statistic title="调用总次数" :value="summary.call_count" />
      </a-card>
      <a-card class="glass-card" :loading="loadingSummary">
        <a-statistic title="Prompt Tokens" :value="summary.prompt_tokens" />
      </a-card>
      <a-card class="glass-card" :loading="loadingSummary">
        <a-statistic title="Completion Tokens" :value="summary.completion_tokens" />
      </a-card>
      <a-card class="glass-card" :loading="loadingSummary">
        <a-statistic title="Total Tokens" :value="summary.total_tokens" />
      </a-card>
      <a-card class="glass-card" :loading="loadingSummary">
        <a-statistic title="usage 缺失调用数" :value="summary.usage_missing_count" />
      </a-card>
      <a-card class="glass-card" :loading="loadingSummary">
        <a-statistic title="平均每次 Total Tokens" :precision="2" :value="summary.avg_total_tokens_per_call" />
      </a-card>
    </div>

    <a-card class="glass-card section-card" title="每小时统计">
      <a-table
        row-key="bucket_start"
        :data-source="hourlyItems"
        :loading="loadingHourly"
        :pagination="false"
        size="small"
      >
        <a-table-column title="小时">
          <template #default="{ record }">
            {{ formatDateTime(record.bucket_start) }}
          </template>
        </a-table-column>
        <a-table-column title="调用次数" data-index="call_count" />
        <a-table-column title="Prompt" data-index="prompt_tokens" />
        <a-table-column title="Completion" data-index="completion_tokens" />
        <a-table-column title="Total" data-index="total_tokens" />
        <a-table-column title="usage 缺失数" data-index="usage_missing_count" />
      </a-table>
    </a-card>

    <a-card class="glass-card section-card" title="每天统计">
      <a-table
        row-key="bucket_date"
        :data-source="dailyItems"
        :loading="loadingDaily"
        :pagination="false"
        size="small"
      >
        <a-table-column title="日期">
          <template #default="{ record }">
            {{ formatDay(record.bucket_date) }}
          </template>
        </a-table-column>
        <a-table-column title="调用次数" data-index="call_count" />
        <a-table-column title="Prompt" data-index="prompt_tokens" />
        <a-table-column title="Completion" data-index="completion_tokens" />
        <a-table-column title="Total" data-index="total_tokens" />
        <a-table-column title="usage 缺失数" data-index="usage_missing_count" />
      </a-table>
    </a-card>

    <a-card class="glass-card section-card" title="每次调用明细">
      <a-table
        row-key="id"
        :data-source="callItems"
        :loading="loadingCalls"
        size="small"
        :scroll="{ x: 1380 }"
        :pagination="{
          current: pager.page,
          pageSize: pager.pageSize,
          total: pager.total,
          showSizeChanger: true,
          onChange: handlePageChange,
          onShowSizeChange: handlePageChange,
        }"
      >
        <a-table-column title="时间" width="180">
          <template #default="{ record }">
            {{ formatDateTime(record.occurred_at) }}
          </template>
        </a-table-column>
        <a-table-column title="来源" width="100">
          <template #default="{ record }">
            {{ formatSource(record.source) }}
          </template>
        </a-table-column>
        <a-table-column title="任务" width="140">
          <template #default="{ record }">
            {{ record.task_name || '-' }}
          </template>
        </a-table-column>
        <a-table-column title="设备" width="140">
          <template #default="{ record }">
            {{ record.device_name || record.device_id || '-' }}
          </template>
        </a-table-column>
        <a-table-column title="Provider" width="140">
          <template #default="{ record }">
            {{ record.provider_name || '-' }}
          </template>
        </a-table-column>
        <a-table-column title="模型" data-index="model" width="180" />
        <a-table-column title="状态" width="90">
          <template #default="{ record }">
            <a-tag :color="record.call_status === 'success' ? 'green' : record.call_status === 'empty_content' ? 'gold' : 'red'">
              {{ formatStatus(record.call_status) }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="usage 状态" width="110">
          <template #default="{ record }">
            <a-tag :color="record.usage_available ? 'blue' : 'default'">
              {{ record.usage_available ? '已返回' : '缺失' }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="Prompt" width="90">
          <template #default="{ record }">
            {{ safeNumber(record.prompt_tokens) }}
          </template>
        </a-table-column>
        <a-table-column title="Completion" width="110">
          <template #default="{ record }">
            {{ safeNumber(record.completion_tokens) }}
          </template>
        </a-table-column>
        <a-table-column title="Total" width="90">
          <template #default="{ record }">
            {{ safeNumber(record.total_tokens) }}
          </template>
        </a-table-column>
        <a-table-column title="耗时(ms)" width="100">
          <template #default="{ record }">
            {{ Number(record.latency_ms || 0).toFixed(1) }}
          </template>
        </a-table-column>
        <a-table-column title="备注 / 错误" min-width="220">
          <template #default="{ record }">
            <span class="muted-text">{{ record.error_message || record.request_context || '-' }}</span>
          </template>
        </a-table-column>
      </a-table>
    </a-card>
  </div>
</template>

<style scoped>
.filters-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 16px;
  margin: 16px 0;
}

.section-card {
  margin-top: 16px;
}

.muted-text {
  color: rgba(0, 0, 0, 0.65);
}
</style>
