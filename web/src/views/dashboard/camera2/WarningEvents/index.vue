<template>
  <div class="panel warning-events">
    <div class="panel-header">预警事件</div>

    <div class="panel-content">
      <div class="events-toolbar">
        <div class="toolbar-tabs">
          <span class="tab" @click="handleTabClick('real')" :class="{ active: currentWarningTab === 'real' }">实时报警</span>
          <span class="tab" @click="handleTabClick('patrol')" :class="{ active: currentWarningTab === 'patrol' }">巡查报警</span>
        </div>
        <button class="history-btn" @click="handleHistoryClick">历史报警</button>
      </div>

      <div class="events-table">
        <div class="table-header">
          <div class="header-cell">图片</div>
          <div class="header-cell">时间</div>
          <div class="header-cell">区域</div>
          <div class="header-cell">类型</div>
          <div class="header-cell">等级</div>
          <div class="header-cell">状态</div>
        </div>

        <div class="table-body">
          <div class="table-row" v-for="item in eventList" :key="item.id">
            <div class="table-cell">
              <img class="event-image" :src="item.image" alt="" />
            </div>
            <div class="table-cell" style="flex-direction: column;">
              <div class="text">{{ item.dateText }}</div>
              <div class="text">{{ item.timeText }}</div>
            </div>
            <div class="table-cell">
              <div class="text">{{ item.areaName }}</div>
            </div>
            <div class="table-cell">
              <div class="text">{{ item.algorithmName }}</div>
            </div>
            <div class="table-cell">
              <div class="level" :class="item.levelClass">{{ item.levelText }}</div>
            </div>
            <div class="table-cell">
              <div class="status" :class="item.statusClass">{{ item.statusText }}</div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
  <DialogHistory ref="historyDialog" @updated="handleRefresh" />
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'

import { fetchCamera2RealtimeEvents, type Camera2RealtimeEventItem } from '../api'
import { usePatrolCenter, type Camera2WarningTab } from '../hooks/usePatrolCenter'
import DialogHistory from './DialogHistory.vue'

const eventList = ref<Camera2RealtimeEventItem[]>([])
const historyDialog = ref()
const { currentWarningTab, setWarningTab } = usePatrolCenter()

const sourceByTab: Record<Camera2WarningTab, 'runtime' | 'patrol'> = {
  real: 'runtime',
  patrol: 'patrol',
}

const handleTabClick = (tab: Camera2WarningTab) => {
  setWarningTab(tab)
}

const handleHistoryClick = () => {
  historyDialog.value?.open(sourceByTab[currentWarningTab.value])
}

const handleRefresh = async () => {
  eventList.value = await fetchCamera2RealtimeEvents(sourceByTab[currentWarningTab.value])
}

watch(currentWarningTab, () => {
  void handleRefresh()
})

function handlePatrolRefresh() {
  if (currentWarningTab.value === 'patrol') {
    void handleRefresh()
  }
}

onMounted(() => {
  void handleRefresh()
  window.addEventListener('maas-alarm', handleRefresh)
  window.addEventListener('maas-patrol-refresh', handlePatrolRefresh)
})

onUnmounted(() => {
  window.removeEventListener('maas-alarm', handleRefresh)
  window.removeEventListener('maas-patrol-refresh', handlePatrolRefresh)
})
</script>

<style scoped lang="less">
.warning-events {
  width: 100%;
  height: 598px;
  overflow: hidden;
  background: url('../assets/images/bg-section-alarm-list.png') no-repeat center center / cover;
  display: flex;
  flex-direction: column;

  .panel-header {
    font-size: 21px;
    font-weight: 800;
    color: #f1f8ff;
    padding: 6px 0 0 30px;
  }

  .panel-content {
    width: 100%;
    flex: 1 1 0;
    padding: 14px 12px 12px;
    display: flex;
    flex-direction: column;

    .events-toolbar {
      width: 100%;
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-bottom: 8px;

      .toolbar-tabs {
        width: 208px;
        height: 34px;
        padding: 0 6px;
        display: flex;
        background: url('../assets/images/bg-btn1.png') no-repeat center center / cover;

        .tab {
          flex: 1;
          color: #bad8ff;
          font-size: 14px;
          text-align: center;
          line-height: 34px;
          cursor: pointer;
        }

        .tab.active {
          background: url('../assets/images/bg-btn-active.png') no-repeat center center / contain;
        }
      }

      .history-btn {
        width: 82px;
        height: 30px;
        line-height: 30px;
        color: #f2fbff;
        background: url('../assets/images/bg-btn2.png') no-repeat center center / cover;
      }
    }

    .events-table {
      display: flex;
      flex-direction: column;
      width: 100%;
      flex: 1 1 0;

      .table-header {
        width: 100%;
        display: grid;
        grid-template-columns: 60px 90px 1fr 90px 40px 64px;
        align-items: center;

        .header-cell {
          width: 100%;
          text-align: center;
          padding: 10px 0;
          font-size: 13px;
          color: #b7d7fb;
          background: rgba(22, 64, 121, 0.72);
        }
      }

      .table-body {
        width: 100%;
        flex: 1 1 0;
        flex-direction: column;
        overflow-y: auto;
        scrollbar-width: none;

        .table-row {
          width: 100%;
          display: grid;
          grid-template-columns: 60px 90px 1fr 90px 40px 64px;
          align-items: center;
          padding: 5px 0px;

          &:nth-child(2n) {
            background: #0082e625;
          }

          .table-cell {
            flex: 1 0 0;
            padding: 0 4px;
            display: flex;
            align-items: center;
            justify-content: center;

            .event-image {
              width: 50px;
              height: 40px;
              border-radius: 4px;
              overflow: hidden;
              border: 1px solid rgba(255, 255, 255, 0.08);
            }

            .text {
              line-height: 1.35;
              color: #d8e8fb;
              text-align: center;
              padding: 4px 0;
              font-size: 13px;
            }

            .level {
              border-radius: 30px;
              text-align: center;
              font-size: 14px;
              padding: 0 10px;
              width: max-content;

              &.high {
                color: #ff4d59;
                background: rgba(246, 111, 123, 0.35);
              }

              &.middle {
                color: #f19f45;
                background: rgba(246, 153, 75, 0.3);
              }

              &.low {
                color: #e5f03d;
                background: rgba(128, 140, 25, 0.3);
              }
            }

            .status {
              border-radius: 4px;
              text-align: center;
              font-size: 14px;
              width: 50px;
              border: 1px solid transparent;

              &.pending {
                border-color: #ffbf3b;
                color: #ffbf3b;
                background: rgba(255, 191, 59, 0.3);
              }

              &.resolved {
                border-color: #36f7ff;
                color: #36f7ff;
                background: rgba(54, 247, 255, 0.2);
              }
            }
          }
        }
      }
    }
  }
}
</style>
