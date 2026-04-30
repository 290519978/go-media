import { computed, ref, watch } from 'vue'
import { fetchCamera2Algorithms, fetchCamera2Areas, fetchCamera2Channels } from '../api'

type MonitorGridOption = {
  value: string
  label: string
}

type MonitorGridDeviceStatus = 'online' | 'alarm' | 'all'

export type MonitorGridVideoItem = {
  id: string
  name: string
  areaID: string
  areaName: string
  status: string
  statusText: string
  alarming60s: boolean
  algorithms: string[]
  liveUrl: string
  streamApp: string
  streamID: string
}

const selectedRegion = ref('')
const selectedAlgorithm = ref('')
const selectedDeviceStatus = ref<MonitorGridDeviceStatus>('all')
const searchText = ref('')
const currentLayout = ref(4)
const activeVideo = ref('')
const isFullscreen = ref(false)
const currentPage = ref(1)

const regionOptions = ref<MonitorGridOption[]>([{ value: '', label: '全部区域' }])
const algorithmOptions = ref<MonitorGridOption[]>([{ value: '', label: '全部算法' }])
const deviceStatusOptions = ref<MonitorGridOption[]>([
  { value: 'online', label: '在线' },
  { value: 'alarm', label: '报警' },
  { value: 'all', label: '全部' },
])
const allVideoList = ref<MonitorGridVideoItem[]>([])

const layoutColumnCount = computed(() => {
  if (currentLayout.value === 1) return 1
  if (currentLayout.value === 4) return 2
  return 3
})

const filteredVideoList = computed(() => {
  const keyword = String(searchText.value || '').trim().toLowerCase()
  return allVideoList.value.filter((item) => {
    if (selectedRegion.value && item.areaID !== selectedRegion.value) {
      return false
    }
    if (selectedAlgorithm.value && !item.algorithms.includes(selectedAlgorithm.value)) {
      return false
    }
    // 监控画面按在线/报警/全部三种状态口径筛选，不再按单个设备枚举过滤。
    if (selectedDeviceStatus.value === 'online' && String(item.status || '').trim().toLowerCase() !== 'online') {
      return false
    }
    if (selectedDeviceStatus.value === 'alarm' && !item.alarming60s) {
      return false
    }
    if (keyword && !item.name.toLowerCase().includes(keyword)) {
      return false
    }
    return true
  })
})

const totalPages = computed(() => Math.max(1, Math.ceil(filteredVideoList.value.length / currentLayout.value)))

const videoList = computed(() => {
  const safePage = Math.min(currentPage.value, totalPages.value)
  const startIndex = (safePage - 1) * currentLayout.value
  return filteredVideoList.value.slice(startIndex, startIndex + currentLayout.value)
})

watch([selectedRegion, selectedAlgorithm, selectedDeviceStatus, searchText, currentLayout], () => {
  currentPage.value = 1
})

watch(totalPages, (value) => {
  if (currentPage.value > value) {
    currentPage.value = value
  }
})

function handleLayoutChange(value: number) {
  currentLayout.value = value
}

function handleClick(id: string) {
  activeVideo.value = id
}

function handlePrePageClick() {
  if (currentPage.value > 1) {
    currentPage.value -= 1
  }
}

function handleNextPageClick() {
  if (currentPage.value < totalPages.value) {
    currentPage.value += 1
  }
}

async function loadWidgetData() {
  const [areas, algorithms, channels] = await Promise.all([
    fetchCamera2Areas().catch(() => []),
    fetchCamera2Algorithms().catch(() => []),
    fetchCamera2Channels().catch(() => []),
  ])

  regionOptions.value = [
    { value: '', label: '全部区域' },
    ...areas.map((item) => ({
      value: item.id,
      label: item.name,
    })),
  ]
  algorithmOptions.value = [
    { value: '', label: '全部算法' },
    ...algorithms.map((item) => ({
      value: item.name,
      label: item.name,
    })),
  ]

  allVideoList.value = channels.map((item) => ({
    id: String(item.id || ''),
    name: String(item.name || item.id || '未命名设备'),
    areaID: String(item.area_id || ''),
    areaName: String(item.area_name || item.area_id || '未分配区域'),
    status: String(item.status || ''),
    statusText: String(item.status || '').toLowerCase() === 'online' ? '在线' : '离线',
    alarming60s: Boolean(item.alarming_60s),
    algorithms: Array.isArray(item.algorithms)
      ? item.algorithms.map((name) => String(name || '').trim()).filter(Boolean)
      : [],
    liveUrl: String(item.play_webrtc_url || item.play_ws_flv_url || ''),
    streamApp: String(item.app || ''),
    streamID: String(item.stream_id || ''),
  }))

  if (!activeVideo.value || !allVideoList.value.some((item) => item.id === activeVideo.value)) {
    activeVideo.value = allVideoList.value[0]?.id || ''
  }
}

export function useWidgetData() {
  return {
    selectedRegion,
    regionOptions,
    selectedAlgorithm,
    algorithmOptions,
    selectedDeviceStatus,
    deviceStatusOptions,
    searchText,
    currentLayout,
    layoutColumnCount,
    activeVideo,
    isFullscreen,
    currentPage,
    totalPages,
    videoList,
    allVideoList,
    handleLayoutChange,
    handleClick,
    handlePrePageClick,
    handleNextPageClick,
    loadWidgetData,
  }
}
