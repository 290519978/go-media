<template>
  <div class="center-panel">
    <div class="filter-bar">
      <div class="left-filters">
        <div class="filter-tags">
          <span class="tag" :class="{ active: currStatus === 'online' }" @click="currStatus = 'online'">在线</span>
          <span class="tag" :class="{ active: currStatus === 'all' }" @click="currStatus = 'all'">全部</span>
          <span class="tag" :class="{ active: currStatus === 'alarm' }" @click="currStatus = 'alarm'">告警</span>
        </div>

        <div class="protocol-tags">
          <span class="tag" :class="{ active: currProtocol === 'webrtc' }" @click="currProtocol = 'webrtc'">WebRTC</span>
          <span class="tag" :class="{ active: currProtocol === 'ws_flv' }" @click="currProtocol = 'ws_flv'">WS-FLV</span>
        </div>
      </div>

      <div class="maximize-btn">
        <button class="btn" :class="{ active: currLayout === 1 }" @click="currLayout = 1; currPage = 1" title="1x1">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M8 3H5a2 2 0 0 0-2 2v3"></path>
            <path d="M21 8V5a2 2 0 0 0-2-2h-3"></path>
            <path d="M3 16v3a2 2 0 0 0 2 2h3"></path>
            <path d="M16 21h3a2 2 0 0 0 2-2v-3"></path>
          </svg>
        </button>

        <button class="btn" :class="{ active: currLayout === 4 }" @click="currLayout = 4; currPage = 1" title="2x2">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect width="7" height="7" x="3" y="3" rx="1"></rect>
            <rect width="7" height="7" x="14" y="3" rx="1"></rect>
            <rect width="7" height="7" x="14" y="14" rx="1"></rect>
            <rect width="7" height="7" x="3" y="14" rx="1"></rect>
          </svg>
        </button>

        <button class="btn" :class="{ active: currLayout === 9 }" @click="currLayout = 9; currPage = 1" title="3x3">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect width="18" height="18" x="3" y="3" rx="2"></rect>
            <path d="M3 9h18"></path>
            <path d="M3 15h18"></path>
            <path d="M9 3v18"></path>
            <path d="M15 3v18"></path>
          </svg>
        </button>
      </div>
    </div>

    <div class="video-grid" :class="`layout-${currLayout}`">
      <div class="video-card" v-for="(item, index) in deviceList" :key="`${item.id}-${currPage}-${index}`">
        <div class="video-content">
          <LiveScreen
            :key="`${item.id}-${currPage}-${index}`"
            :liveUrl="resolveLiveUrl(item)"
            :streamApp="item.streamApp"
            :streamId="item.streamID"
            :ref="(el) => setLiveScreenRef(index, el)"
          />
          <div v-if="displayAlgorithmText(item)" class="overlay-tags">
            <span class="tag purple" :title="displayAlgorithmText(item)">
              {{ displayAlgorithmText(item) }}
            </span>
          </div>
        </div>

        <div class="video-info">
          <div class="name-row">
            <div class="info-item name">
              <span class="svg-icon">
                <svg viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg">
                  <path d="M896 256H128c-35.3 0-64 28.7-64 64v448c0 35.3 28.7 64 64 64h768c35.3 0 64-28.7 64-64V320c0-35.3-28.7-64-64-64z" fill="#3AA3FF" />
                </svg>
              </span>
              <span class="text" :title="item.deviceName">{{ item.deviceName }}</span>
            </div>
          </div>

          <div class="meta-row">
            <div class="info-item location">
              <span class="svg-icon">
                <svg viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg">
                  <path d="M512 85.3c-164.9 0-298.7 133.8-298.7 298.7 0 205.1 274.6 534 285.3 546.9 7 8.5 19.9 8.5 26.9 0 10.7-12.9 285.2-341.8 285.2-546.9C810.7 219.1 676.9 85.3 512 85.3z" fill="#B9D5ED" />
                </svg>
              </span>
              <span class="text" :title="item.deviceArea">{{ item.deviceArea }}</span>
            </div>

            <div class="info-item today-total">
              <span class="svg-icon">
                <svg viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg">
                  <path d="M341.9 63.4c35.5 0 64.4 28.9 64.4 64.4v26.3h235.9v-26.3c0-35.5 28.9-64.4 64.4-64.4 35.4 0 64.3 28.9 64.3 64.4v26.3h117.1c40.7 0 73.8 33.1 73.8 73.8v660.3c0 40.7-33.1 73.8-73.8 73.8H136.4c-40.7 0-73.8-33.1-73.8-73.8V227.9c0-40.7 33.1-73.8 73.8-73.8h141.1v-26.3c0-35.5 28.9-64.4 64.4-64.4z" fill="#B9D5ED" />
                </svg>
              </span>
              <span class="text">今日 {{ item.todayAlarm }}</span>
            </div>

            <div class="info-item all-total">
              <span class="svg-icon">
                <svg viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg">
                  <path d="M512 118.5l448.9 202.3-448.9 220.3L63.1 320.8 512 118.5zm0 172.6l311.3-140.5L512 9.3 200.7 150.6 512 291.1zm0 248.9l448.9-220.3v190.4L512 730.4 63.1 510.1V319.7L512 540zm0 202.9l448.9-220.3V714L512 934.3 63.1 714V522.6L512 742.9z" fill="#B9D5ED" />
                </svg>
              </span>
              <span class="text">累计 {{ item.totalAlarm }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="bottom-bar">
      <div class="stats-info">
        共{{ list.length }}个摄像头
        <span class="status-dot online">●</span> 在线 {{ deviceOnline }}
        <span class="status-dot offline">●</span> 离线 {{ deviceOffline }}
        <span class="status-dot warn">●</span> 告警 {{ deviceAlarm }}
      </div>

      <div class="pagination">
        <button class="prev" :disabled="currPage <= 1" @click="currPage--">上一页</button>
        <button class="next" :disabled="currPage >= totalPage" @click="currPage++">下一页</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import LiveScreen from './LiveScreen.vue'
import { deviceListApi, type DeviceItem } from '../Camera.api'

type LayoutType = 1 | 4 | 9
type StatusType = 'online' | 'all' | 'alarm'
type PlayProtocol = 'webrtc' | 'ws_flv'

type LiveScreenExpose = {
  destroyPlayer: () => void
}

const list = ref<DeviceItem[]>([])
const currPage = ref(1)
const currLayout = ref<LayoutType>(4)
const currStatus = ref<StatusType>('online')
const currProtocol = ref<PlayProtocol>('webrtc')
const liveScreenRefs = ref<Array<LiveScreenExpose | null>>([])
let intervalId: ReturnType<typeof setInterval> | null = null

const deviceOnline = computed(() => list.value.filter((item) => item.status === 1).length)
const deviceOffline = computed(() => list.value.filter((item) => item.status === 0).length)
const deviceAlarm = computed(() => list.value.filter((item) => item.alarming60s).length)

const filteredList = computed(() => {
  if (currStatus.value === 'online') {
    return list.value.filter((item) => item.status === 1)
  }
  if (currStatus.value === 'alarm') {
    return list.value.filter((item) => item.alarming60s)
  }
  return list.value
})

const totalPage = computed(() => Math.max(1, Math.ceil(filteredList.value.length / currLayout.value)))

const deviceList = computed(() => {
  const start = (currPage.value - 1) * currLayout.value
  const end = currPage.value * currLayout.value
  return filteredList.value.slice(start, end)
})

function resolveLiveUrl(item: DeviceItem): string {
  if (currProtocol.value === 'webrtc') {
    return item.streamUrlWebrtc || ''
  }
  return item.streamUrlWsFlv || ''
}

function displayAlgorithmText(item: DeviceItem): string {
  if (item.algorithms.length > 0) {
    return item.algorithms.join(' / ')
  }
  return item.bindingAlgorithm || ''
}

function setLiveScreenRef(index: number, el: unknown) {
  if (!el || typeof el !== 'object') {
    liveScreenRefs.value[index] = null
    return
  }
  const instance = el as Partial<LiveScreenExpose>
  liveScreenRefs.value[index] = typeof instance.destroyPlayer === 'function'
    ? (instance as LiveScreenExpose)
    : null
}

const destroyAllPlayers = () => {
  liveScreenRefs.value.forEach((player) => {
    if (player && typeof player.destroyPlayer === 'function') {
      player.destroyPlayer()
    }
  })
  liveScreenRefs.value = []
}

watch(currPage, (newPage, oldPage) => {
  if (newPage !== oldPage) {
    destroyAllPlayers()
  }
})

watch(currLayout, (newLayout, oldLayout) => {
  if (newLayout !== oldLayout) {
    destroyAllPlayers()
  }
})

watch(currProtocol, (newProtocol, oldProtocol) => {
  if (newProtocol !== oldProtocol) {
    destroyAllPlayers()
  }
})

watch([currStatus, currLayout], () => {
  currPage.value = 1
})

watch(totalPage, (pageCount) => {
  if (currPage.value > pageCount) {
    currPage.value = pageCount
  }
})

async function getData() {
  const res = await deviceListApi()
  list.value = res
}

function onRealtimeAlarm() {
  void getData()
}

onMounted(() => {
  void getData()
  intervalId = setInterval(() => {
    void getData()
  }, 60000)
  window.addEventListener('maas-alarm', onRealtimeAlarm)
})

onUnmounted(() => {
  destroyAllPlayers()
  if (intervalId !== null) {
    clearInterval(intervalId)
  }
  window.removeEventListener('maas-alarm', onRealtimeAlarm)
})
</script>

<style lang="less" scoped>
  .center-panel {
    display: flex;
    flex-direction: column;
    height: 100%;
    gap: 15px;

    .filter-bar {
      display: flex;
      justify-content: space-between;
      align-items: center;
      background: linear-gradient(0deg, #074084 0%, #001c3c 100%);
      padding: 10px 20px;
      border-radius: 4px;
      color: #ffffff;

      .left-filters {
        display: flex;
        align-items: center;
        gap: 20px;

        .ant-dropdown-link {
          color: #ffffff;
          font-size: 16px;
        }

        .filter-tags,
        .protocol-tags {
          display: flex;
          gap: 5px;

          .tag {
            padding: 6px 15px;
            border: 1px solid #5493d8;
            cursor: pointer;
            border-radius: 4px;

            &.active {
              background: linear-gradient(0deg, #07408400 0%, #5493d8f0 100%);
              color: #fff;
            }
          }
        }
      }

      .maximize-btn {
        .btn {
          background: transparent;
          border: none;
          cursor: pointer;
          color: #ccc;

          &.active {
            color: #2588f1;
          }
        }
      }
    }

    .video-grid {
      flex: 1;
      min-height: 0;
      display: grid;
      grid-template-columns: 1fr 1fr;
      grid-template-rows: 1fr 1fr;
      gap: 15px;

      &.layout-9 {
        grid-template-columns: 1fr 1fr 1fr;
        grid-template-rows: 1fr 1fr 1fr;

        .video-card .video-info {
          padding: 8px;

          .name-row {
            font-size: 12px;

            .name {
              font-size: 16px;
            }
          }

          .meta-row {
            margin-top: 8px;
            gap: 10px;
            font-size: 12px;
          }
        }
      }
      &.layout-1 {
        grid-template-columns: 1fr;
        grid-template-rows: 1fr;
      }

      .video-card {
        display: flex;
        flex-direction: column;
        overflow: hidden;
        border-radius: 10px;

        .video-content {
          flex: 1;
          position: relative;
          overflow: hidden;

          img {
            width: 100%;
            height: 100%;
            object-fit: cover;
          }

          .overlay-tags {
            position: absolute;
            top: 10px;
            right: 10px;
            display: flex;
            z-index: 3;
            pointer-events: none;
            max-width: calc(100% - 20px);
            justify-content: flex-end;

            .tag {
              padding: 2px 8px;
              font-size: 12px;
              color: #fff;
              border-radius: 4px;
              display: inline-block;
              max-width: 100%;
              white-space: nowrap;
              overflow: hidden;
              text-overflow: ellipsis;

              &.purple {
                background: #722ed1;
              }
              &.pink {
                background: #eb2f96;
              }
            }
          }
        }

        .video-info {
          padding: 12px;
          background: linear-gradient(0deg, #073468 0%, #04274c 100%);
          color: #b9d5ed;
          font-size: 14px;

          .info-item {
            display: flex;
            align-items: center;
            gap: 5px;
            min-width: 0;

            .svg-icon {
              width: 20px;
              height: 20px;
              flex: 0 0 auto;
              display: flex;
              align-items: center;
              justify-content: center;

              svg {
                width: 100%;
              }
            }

            .text {
              min-width: 0;
            }
          }

          .name-row {
            display: block;
            margin-bottom: 6px;
            font-size: 14px;

            .name {
              width: 100%;
              font-size: 18px;
              color: #ffffff;
              font-weight: 600;

              svg {
                width: 30px;
                height: 30px;
              }

              .text {
                display: block;
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
              }
            }
          }

          .meta-row {
            display: flex;
            align-items: center;
            display: flex;
            gap: 20px;
            font-size: 14px;

            .location {
              flex: 1 1 auto;
              min-width: 0;

              .text {
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
              }
            }

            .today-total,
            .all-total {
              flex: 0 0 auto;
            }
          }
        }
      }
    }

    .bottom-bar {
      background: linear-gradient(180deg, #001c3c 0%, #074084 100%);
      border: 2px solid #00356d;
      border-radius: 4px;
      font-size: 14px;
      color: #ffffff;
      overflow: hidden;
      padding: 6px 20px;
      display: flex;
      justify-content: space-between;
      align-items: center;

      .stats-info {
        display: flex;
        gap: 10px;
        align-items: center;

        .status-dot {
          font-size: 12px;
          &.online {
            color: #52c41a;
          }
          &.offline {
            color: #ff4d4f;
          }
          &.warn {
            color: #faad14;
          }
        }
      }

      .pagination {
        display: flex;
        gap: 10px;
        align-items: center;

        .prev,
        .next {
          padding: 4px 12px;
          border: 1px solid #5493d8;
          cursor: pointer;
          border-radius: 4px;
          background: transparent;
        }
      }
    }
  }
</style>
