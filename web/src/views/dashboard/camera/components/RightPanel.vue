<template>
  <div class="right-panel">
    <PanelTitle title="实时告警" />

    <div class="right-panel-content">
      <div class="filters">
        <a-select
          v-model:value="selectedLevel"
          placeholder="等级"
          class="custom-select"
          dropdown-class-name="camera-alarm-filter-popup"
          popup-class-name="camera-alarm-filter-popup"
          placement="bottomLeft"
          :dropdown-match-select-width="false"
          :dropdown-style="filterDropdownStyle"
          :not-found-content="'暂无可选项'"
          :allow-clear="true"
          :options="srhLevel"
          :get-popup-container="getPopupContainer"
          @change="onFilterChange"
        />

        <a-select
          v-model:value="selectedStatus"
          placeholder="状态"
          class="custom-select"
          dropdown-class-name="camera-alarm-filter-popup"
          popup-class-name="camera-alarm-filter-popup"
          placement="bottomLeft"
          :dropdown-match-select-width="false"
          :dropdown-style="filterDropdownStyle"
          :not-found-content="'暂无可选项'"
          :allow-clear="true"
          :options="statusOptions"
          :get-popup-container="getPopupContainer"
          @change="onFilterChange"
        />

        <a-select
          v-model:value="selectedAlgorithm"
          placeholder="算法"
          class="custom-select"
          dropdown-class-name="camera-alarm-filter-popup"
          popup-class-name="camera-alarm-filter-popup"
          placement="bottomLeft"
          :dropdown-match-select-width="false"
          :dropdown-style="filterDropdownStyle"
          :not-found-content="'暂无可选项'"
          :allow-clear="true"
          :options="srhAlgorithm"
          :get-popup-container="getPopupContainer"
          @change="onFilterChange"
        />

        <a-select
          v-model:value="selectedArea"
          placeholder="区域"
          class="custom-select"
          dropdown-class-name="camera-alarm-filter-popup"
          popup-class-name="camera-alarm-filter-popup"
          placement="bottomLeft"
          :dropdown-match-select-width="false"
          :dropdown-style="filterDropdownStyle"
          :not-found-content="'暂无可选项'"
          :allow-clear="true"
          :options="srhArea"
          :get-popup-container="getPopupContainer"
          @change="onFilterChange"
        />
      </div>

      <div ref="alarmListRef" class="alarm-list" @scroll.passive="onListScroll">
        <div
          v-for="alarm in alarms"
          :key="alarm.id"
          class="alarm-card"
          :style="{ '--alarm-border-color': cardBorderColor(alarm) }"
        >
          <div class="corner">
            <div class="corner-top-left"></div>
            <div class="corner-top-right"></div>
            <div class="corner-bottom-left"></div>
            <div class="corner-bottom-right"></div>
          </div>

          <div class="alarm-img">
            <a-image
              :src="alarm.images"
              alt="Alarm"
              :preview="alarm.hasSnapshot ? imagePreviewConfig : false"
              @click.stop="onAlarmImageClick(alarm)"
              @error="onAlarmImageError(alarm)"
            />
          </div>

          <div class="alarm-details">
            <div class="header">
              <span class="tag" :class="`tag-status-${alarm.level}`">
                {{ alarm.alarmLevelName }}
              </span>
              <span class="alarm-title">{{ alarm.relAlgorithmName }}</span>
            </div>

            <div class="center">
              <div class="info-time">
                <span class="icon">🕵</span> {{ alarm.alarmTime }}
              </div>

              <div class="actions">
                <button v-if="isPending(alarm)" class="btn process" @click="gotoEventDetail(alarm.id)">未处理</button>
                <div v-else class="confirmed">
                  <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon lucide-check w-3.5 h-3.5" aria-hidden="true"><path d="M20 6 9 17l-5-5"></path></svg>
                  <span>已确认</span>
                </div>
              </div>
            </div>

            <div class="info-location">
              <span class="icon">📍</span>
              <span class="text">{{ alarm.relAreaName }}</span>
            </div>
          </div>
        </div>

        <div v-if="loading" class="loading-line">加载中...</div>
        <div v-else-if="loadingMore" class="loading-line">加载更多中...</div>
        <div v-else-if="!hasMore && alarms.length > 0" class="loading-line">没有更多了</div>
        <div v-else-if="!loading && alarms.length === 0" class="loading-line">暂无告警</div>
      </div>

      <div class="alarm-stat" @click="toAlarmList">
        <span>查看更多（已加载{{ alarms.length }}条）</span>
        <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon lucide-chevron-right w-3 h-3" aria-hidden="true"><path d="m9 18 6-6-6-6"></path></svg>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { message } from 'ant-design-vue'
import { useRouter } from 'vue-router'
import PanelTitle from './PanelTitle.vue'
import {
  alarmDetailApi,
  alarmListApi,
  deviceListApi,
  leftPanelApi,
  type AlarmItem,
  type AlarmStatusFilter,
} from '../Camera.api'

type OptionItem = {
  label: string
  value: string
}

const router = useRouter()
const alarms = ref<AlarmItem[]>([])
const alarmListRef = ref<HTMLElement | null>(null)

const selectedStatus = ref<AlarmStatusFilter | undefined>(undefined)
const selectedArea = ref<string | undefined>(undefined)
const selectedAlgorithm = ref<string | undefined>(undefined)
const selectedLevel = ref<string | undefined>(undefined)

const srhArea = ref<OptionItem[]>([])
const srhAlgorithm = ref<OptionItem[]>([])
const srhLevel = ref<OptionItem[]>([])

const loading = ref(false)
const loadingMore = ref(false)
const page = ref(0)
const totalRaw = ref(0)
const pageSize = 20

let intervalId: ReturnType<typeof setInterval> | null = null

const statusOptions: Array<{ label: string; value: Exclude<AlarmStatusFilter, ''> }> = [
  { label: '未处理', value: 'pending' },
  { label: '已处理', value: 'handled' },
]

const hasMore = computed(() => page.value * pageSize < totalRaw.value)

const imagePreviewConfig = {
  zIndex: 4000,
  getContainer: () => (document.querySelector('.screen-page') as HTMLElement | null) || document.body,
}

const filterDropdownStyle = {
  minWidth: '220px',
  maxWidth: '420px',
  zIndex: 12000,
}

function getPopupContainer(triggerNode?: HTMLElement): HTMLElement {
  return (triggerNode?.closest('.camera-container') as HTMLElement | null) || document.body
}

function normalizeStatus(status: string): string {
  return String(status || '').trim().toLowerCase()
}

function isPending(item: AlarmItem): boolean {
  return normalizeStatus(item.status) === 'pending'
}

function cardBorderColor(item: AlarmItem): string {
  const customColor = String(item.alarmLevelColor || '').trim()
  if (customColor) {
    return customColor
  }
  if (item.level === '0') return '#D6554D'
  if (item.level === '1') return '#FF7D2C'
  return '#FED955'
}

function onAlarmImageClick(item: AlarmItem) {
  if (!item.hasSnapshot) {
    message.info('暂无快照')
  }
}

function onAlarmImageError(item: AlarmItem) {
  item.hasSnapshot = false
}

function matchCurrentFilter(item: AlarmItem): boolean {
  if (selectedStatus.value === 'pending' && !isPending(item)) {
    return false
  }
  if (selectedStatus.value === 'handled' && isPending(item)) {
    return false
  }

  if (selectedArea.value && item.areaID !== selectedArea.value) {
    return false
  }
  if (selectedAlgorithm.value && item.algorithmID !== selectedAlgorithm.value) {
    return false
  }
  if (selectedLevel.value && item.alarmLevelID !== selectedLevel.value) {
    return false
  }
  return true
}

function mergeItems(source: AlarmItem[], prepend = false) {
  const exists = new Set(alarms.value.map((item) => item.id))
  if (prepend) {
    for (let i = source.length - 1; i >= 0; i -= 1) {
      const item = source[i]
      if (!exists.has(item.id)) {
        alarms.value.unshift(item)
      }
    }
    return
  }

  source.forEach((item) => {
    if (!exists.has(item.id)) {
      alarms.value.push(item)
    }
  })
}

async function loadData(reset = false) {
  if (loading.value || loadingMore.value) {
    return
  }

  const nextPage = reset ? 1 : page.value + 1
  if (reset) {
    loading.value = true
  } else {
    if (!hasMore.value) {
      return
    }
    loadingMore.value = true
  }

  try {
    const res = await alarmListApi({
      page: nextPage,
      pageSize,
      status: selectedStatus.value,
      areaID: selectedArea.value,
      algorithmID: selectedAlgorithm.value,
      alarmLevelID: selectedLevel.value,
    })

    totalRaw.value = res.totalRaw
    page.value = res.page

    if (reset) {
      alarms.value = []
    }
    mergeItems(res.items)
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

async function loadFilterOptions() {
  try {
    const [leftData, devices] = await Promise.all([leftPanelApi(), deviceListApi()])

    const areaMap = new Map<string, string>()
    devices.forEach((item) => {
      if (!item.areaID) {
        return
      }
      if (!areaMap.has(item.areaID)) {
        areaMap.set(item.areaID, item.deviceArea)
      }
    })

    srhArea.value = Array.from(areaMap.entries()).map(([value, label]) => ({ value, label }))
    srhAlgorithm.value = [
      ...leftData.alarmAlgorithmList.map((item) => ({
        label: item.algorithmName,
        value: item.algorithmID,
      })),
    ]
    const sortedLevels = leftData.alarmLevelList
      .slice()
      .sort((a, b) => a.severity - b.severity)

    srhLevel.value = sortedLevels.map((item) => ({
      label: item.levelName,
      value: item.levelID,
    }))
  } catch {
    srhArea.value = []
    srhAlgorithm.value = []
    srhLevel.value = []
    message.warning('筛选项加载失败，已使用默认项')
  }
}

function onFilterChange() {
  page.value = 0
  totalRaw.value = 0
  alarms.value = []
  void loadData(true)
}

function onListScroll() {
  const el = alarmListRef.value
  if (!el || loading.value || loadingMore.value) {
    return
  }

  const nearBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 60
  if (nearBottom && hasMore.value) {
    void loadData(false)
  }
}

async function onRealtimeAlarm(evt: Event) {
  const payload = ((evt as CustomEvent<Record<string, unknown>>).detail || {}) as Record<string, unknown>
  const eventID = String(payload.event_id || '').trim()
  if (!eventID) {
    return
  }

  try {
    const detail = await alarmDetailApi(eventID)
    if (!matchCurrentFilter(detail)) {
      return
    }

    const index = alarms.value.findIndex((item) => item.id === detail.id)
    if (index >= 0) {
      alarms.value.splice(index, 1)
    } else {
      totalRaw.value += 1
    }
    alarms.value.unshift(detail)
    alarms.value = alarms.value.slice(0, 200)
  } catch {
    // ignore realtime detail failure
  }
}

function gotoEventDetail(eventID: string) {
  if (!eventID) {
    return
  }
  void router.push(`/events?focus=${encodeURIComponent(eventID)}`)
}

function toAlarmList() {
  console.info('查看更多告警（大屏占位行为）', alarms.value.length)
}

onMounted(() => {
  void loadFilterOptions()
  void loadData(true)
  intervalId = setInterval(() => {
    void loadData(true)
  }, 60000)
  window.addEventListener('maas-alarm', onRealtimeAlarm as EventListener)
})

onUnmounted(() => {
  if (intervalId !== null) {
    clearInterval(intervalId)
  }
  window.removeEventListener('maas-alarm', onRealtimeAlarm as EventListener)
})
</script>

<style lang="less" scoped>
.right-panel {
  height: 100%;

  .right-panel-content {
    background: linear-gradient(90deg, rgba(27, 118, 218, 0.11) 0%, rgba(27, 118, 218, 0) 100%);
    border-radius: 8px;
    padding: 12px;
  }

  .filters {
    display: flex;
    gap: 12px;
    color: #a0cfff;
    font-size: 14px;
    cursor: pointer;

    .custom-select {
      flex: 1;

      :deep(.ant-select-selector) {
        background-color: transparent !important;
        border: none !important;
        border-bottom: 1px solid rgba(0, 210, 255, 0.3) !important;
        border-radius: 0 !important;
        box-shadow: none !important;
        color: #E8F7FF !important;
        padding-left: 0 !important;

        .ant-select-selection-item {
          color: #E8F7FF !important;
        }

        .ant-select-selection-placeholder {
          color: rgba(232, 247, 255, 0.5) !important;
        }
      }

      :deep(.ant-select-arrow) {
        color: #E8F7FF !important;
      }
    }
  }

  .alarm-list {
    margin-top: 10px;
    display: flex;
    flex-direction: column;
    gap: 10px;
    height: 740px;
    overflow-y: auto;

    &::-webkit-scrollbar {
      display: none;
    }

    .alarm-card {
      --alarm-border-color: #148996;
      background: rgba(18,118,121,0.13);
      border: 1px solid var(--alarm-border-color);
      padding: 6px 8px;
      display: flex;
      gap: 10px;
      position: relative;

      .corner {
        position: absolute;
        top: 0;
        left: 0;
        width: 100%;
        height: 100%;
        pointer-events: none;

        .corner-top-left,
        .corner-top-right,
        .corner-bottom-left,
        .corner-bottom-right {
          position: absolute;
          width: 10px;
          height: 10px;
          border-color: var(--alarm-border-color);
          border-style: solid;
          border-width: 2px;
        }

        .corner-top-left {
          top: -1px;
          left: -1px;
          border-right: transparent;
          border-bottom: transparent;
        }

        .corner-top-right {
          top: -1px;
          right: -1px;
          border-bottom: transparent;
          border-left: transparent;
        }

        .corner-bottom-left {
          bottom: -1px;
          left: -1px;
          border-top: transparent;
          border-right: transparent;
        }

        .corner-bottom-right {
          bottom: -1px;
          right: -1px;
          border-top: transparent;
          border-left: transparent;
        }
      }

      .alarm-img {
        width: 100px;
        height: 80px;
        background: #000;

        :deep(.ant-image) {
          width: 100%;
          height: 100%;
          display: block;
        }

        :deep(.ant-image-img) {
          width: 100%;
          height: 100%;
          object-fit: cover;
          cursor: zoom-in;
        }
      }

      .alarm-details {
        width: 200px;
        display: flex;
        flex-direction: column;
        justify-content: space-between;
        font-size: 12px;

        .header {
          display: flex;
          align-items: center;
          gap: 10px;

          .tag {
            width: 36px;
            height: 20px;
            line-height: 20px;
            text-align: center;
            border-radius: 4px;
            color: #fff;
            font-size: 14px;
            border: 1px solid;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;

            &.tag-status-0 { border-color: #D6554D; background: linear-gradient(0, #D6554D6F 0%, #D6554DD0 100%); }
            &.tag-status-1 { border-color: #FF7D2C; background: linear-gradient(0, #FF7D2C6F 0%, #FF7D2CD0 100%); }
            &.tag-status-2 { border-color: #FED955; background: linear-gradient(0, #FED9556F 0%, #FED955D0 100%); }
          }

          .alarm-title {
            font-weight: bold;
            font-size: 14px;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
          }
        }

        .center {
          display: flex;
          justify-content: space-between;
          align-items: center;

          .info-time {
            color: #ccc;
          }
        }

        .info-location {
          width: 150px;
          color: #ccc;
          display: flex;
          align-items: flex-start;
          justify-content: flex-start;

          .text {
            display: -webkit-box;
            -webkit-line-clamp: 2;
            -webkit-box-orient: vertical;
            overflow: hidden;
          }
        }
      }

      .actions {
        display: flex;
        align-items: center;

        .btn {
          border: none;
          padding: 4px 10px;
          border-radius: 4px;
          cursor: pointer;
          font-size: 12px;
        }
        .process {
          background: rgba(0, 210, 255, 0.2);
          color: #4DD6D6;
          border: 1px solid #4DD6D6;

          &:hover {
            background: #4DD6D6;
            color: #000;
          }
        }
        .confirmed {
          background: transparent;
          color: #34d399;
          display: flex;
          align-items: center;

          svg.icon {
            width: 16px;
          }
        }
      }
    }
  }

  .loading-line {
    text-align: center;
    color: #8fc3dd;
    padding: 6px 0;
    font-size: 12px;
  }

  .alarm-stat {
    margin-top: 10px;
    border-top: 1px solid #4DD6D6;
    color: #4DD6D6;
    font-size: 14px;
    padding: 12px 0 4px;
    display: flex;
    justify-content: center;
    align-items: center;
    cursor: pointer;
  }
}
</style>

<style lang="less">
.camera-alarm-filter-popup {
  z-index: 12000 !important;
  max-width: 420px;

  .ant-select-item {
    height: auto;
    min-height: 32px;
    padding-top: 6px;
    padding-bottom: 6px;
  }

  .rc-virtual-list-holder {
    max-height: 320px !important;
    overflow-y: auto !important;
  }

  .ant-select-item-option-content {
    white-space: normal;
    line-height: 1.4;
    word-break: break-word;
  }
}
</style>
