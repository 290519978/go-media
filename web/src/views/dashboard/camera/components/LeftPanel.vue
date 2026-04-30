<template>
  <div class="left-panel">
    <div class="panel-section">
      <PanelTitle title="算法识别统计" />
      <div class="panel-section-content algo-list">
        <div v-for="(item, index) in algoStats" :key="`${item.algorithmID}-${index}`" class="algo-item">
          <div class="rank" :class="`rank-${index + 1}`">{{ index + 1 }}</div>
          <div class="name">{{ item.algorithmName }}</div>
          <div class="count">{{ item.alarmCount }}</div>
        </div>
      </div>
    </div>

    <div class="panel-section">
      <PanelTitle title="事件分级" />
      <div class="panel-section-content event-stats">
        <div
          v-for="(item, index) in eventStats"
          :key="`${item.levelID}-${index}`"
          class="event-item"
          :class="eventCardClass(index)"
        >
          <div class="count">{{ item.count }}</div>
          <div class="label">{{ item.label }}</div>
        </div>
      </div>
    </div>

    <div class="panel-section">
      <PanelTitle title="区域报警统计" />
      <div class="panel-section-content area-list">
        <div v-for="(item, index) in areaStats" :key="`${item.areaID}-${index}`" class="area-item">
          <img class="icon" src="@/assets/dashboard/icon-alarm.png" alt="" />
          <div class="name">{{ item.areaName }}（{{ item.deviceCount }}）</div>
          <div class="number">{{ item.alarmCount }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import PanelTitle from './PanelTitle.vue'
import {
  leftPanelApi,
  type AlgorithmStatItem,
  type AreaStatItem,
  type LeftPanelData,
} from '../Camera.api'

type LevelCard = {
  levelID: string
  label: string
  count: number
}

const algoStats = ref<AlgorithmStatItem[]>([])
const eventStats = ref<LevelCard[]>([])
const areaStats = ref<AreaStatItem[]>([])

let intervalId: ReturnType<typeof setInterval> | null = null

function applyData(res: LeftPanelData) {
  algoStats.value = res.alarmAlgorithmList
  areaStats.value = res.alarmAreaList
  eventStats.value = res.alarmLevelList.map((item) => ({
    levelID: item.levelID,
    label: item.levelName,
    count: item.alarmCount,
  }))
}

function eventCardClass(index: number): 'emergency' | 'important' | 'general' {
  const mod = index % 3
  if (mod === 0) return 'emergency'
  if (mod === 1) return 'important'
  return 'general'
}

async function getData() {
  const res = await leftPanelApi()
  applyData(res)
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
  if (intervalId !== null) {
    clearInterval(intervalId)
  }
  window.removeEventListener('maas-alarm', onRealtimeAlarm)
})
</script>

<style lang="less" scoped>
.left-panel {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 20px;

  .panel-section {
    display: flex;
    flex-direction: column;

    .panel-section-content  {
      background: linear-gradient(90deg, rgba(27, 118, 218, 0.11) 0%, rgba(27, 118, 218, 0) 100%);
      border-radius: 8px;
    }
  }

  .algo-list {
    display: flex;
    flex-direction: column;
    height: 290px;
    overflow-y: auto;

    &::-webkit-scrollbar {
      display: none;
    }

    .algo-item {
      display: flex;
      align-items: center;
      padding: 10px;
      border-radius: 4px;

      .rank {
        box-sizing: border-box;
        width: 28px;
        height: 28px;
        line-height: 28px;
        text-align: center;
        border-radius: 50%;
        background: #333;
        margin-right: 15px;
        font-weight: bold;
        background: linear-gradient(to top, #072A5D, #0775AE);

        &.rank-1 { background: linear-gradient(to top, #f09819, #edde5d); }
        &.rank-2 { background: linear-gradient(to top, #1260E8, #00f2fe); }
        &.rank-3 { background: linear-gradient(to top, #43e97b, #38f9d7); }
      }

      .name {
        flex: 1;
        font-size: 16px;
      }

      .count {
        font-size: 18px;
        color: #00d2ff;
        font-weight: bold;
      }
    }
  }

  .event-stats {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-start;
    gap: 12px 16px;
    padding: 20px 12px;

    .event-item {
      position: relative;
      width: 102px;
      height: 86px;
      text-align: center;

      .count {
        font-size: 26px;
        font-weight: 500;
        text-shadow: 0px 3px 5px rgba(219,131,93,0.6);
      }

      .label {
        font-size: 15px;
        margin-top: 8px;
      }

      &.emergency { background: url('@/assets/dashboard/level-1.png') no-repeat center center / cover; }
      &.important { background: url('@/assets/dashboard/level-2.png') no-repeat center center / cover; }
      &.general { background: url('@/assets/dashboard/level-3.png') no-repeat center center / cover; }
    }
  }

  .area-list {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 12px 0 12px 12px;

    .area-item {
      padding: 0 10px;
      display: flex;
      align-items: center;
      line-height: 50px;
      background: linear-gradient( 90deg, #043563 0%, rgba(4,53,99,0.18) 100%);

      .icon {
        width: 18px;
        height: auto;
        padding-bottom: 2px;
        margin-right: 8px;
      }

      .name {
        flex: 1;
      }

      .stats {
        display: flex;
        gap: 16px;

        .stat-item {
          display: flex;
          align-items: center;
          gap: 5px;

          img {
            width: auto;
            height: 20px;
          }
        }
      }
    }
  }
}
</style>
