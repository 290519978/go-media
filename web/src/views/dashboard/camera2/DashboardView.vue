<template>
  <div class="dashboard-container">

    <header class="dashboard-header">
      <div class="header-side header-left">
        <div class="date-time">
          <span class="date">{{ currentDate }}</span>
          <span class="week">{{ currentWeek }}</span>
          <span class="time">{{ currentTime }}</span>
        </div>
      </div>

      <div class="header-center">
        <div class="header-frame">
          <div class="header-line left"></div>
          <h1 class="header-title">鸿眸多模态视频监控预警中心</h1>
          <div class="header-line right"></div>
        </div>
      </div>

      <div class="header-side header-right">
        <div class="user-info">
          <div class="avatar"></div>
          <span class="username">{{ username }}</span>
          <button type="button" class="header-back" @click="emit('exit')">返回入口</button>
        </div>
      </div>
    </header>

    <main class="dashboard-main">
      <aside class="dashboard-column left-column">
        <AlarmStatistics />
        <IntelligentAlgorithm />
        <DeviceStatistics />
      </aside>

      <section class="dashboard-column center-column">
        <MonitorGrid />
        <AlarmTrendAnalysis />
      </section>

      <aside class="dashboard-column right-column">
        <WarningEvents />
        <ResourceStatistics />
      </aside>
    </main>

    <RobotAssistant />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { notification } from 'ant-design-vue'
import AlarmStatistics from './AlarmStatistics/index.vue'
import IntelligentAlgorithm from './IntelligentAlgorithm/index.vue'
import DeviceStatistics from './DeviceStatistics/index.vue'
import MonitorGrid from './MonitorGrid/index.vue'
import AlarmTrendAnalysis from './AlarmTrendAnalysis/index.vue'
import WarningEvents from './WarningEvents/index.vue'
import ResourceStatistics from './ResourceStatistics/index.vue'
import RobotAssistant from './RobotAssistant/index.vue'
import { fetchCamera2Profile } from './api'
import { useAlarmFeed } from './hooks/useAlarmFeed'
import {
  buildAppRouteURL,
  llmQuotaNoticeTargetPath,
  markLLMQuotaNoticeShown,
  resolveLLMQuotaNoticeContent,
  type LLMQuotaNotice,
} from '@/utils/llmQuotaNotice'

const emit = defineEmits<{
  exit: []
}>()

const currentDate = ref('')
const currentWeek = ref('')
const currentTime = ref('')
const username = ref('-')
let timer: number | null = null
const shownLLMQuotaNotices = new Set<string>()

const weekDays = ['星期日', '星期一', '星期二', '星期三', '星期四', '星期五', '星期六']

function openLLMUsagePage() {
  // camera2 既可能独立打开，也可能以内嵌 iframe 方式展示，两种场景分别走父层路由或直接跳转。
  if (window.parent && window.parent !== window) {
    window.parent.postMessage({ type: 'camera2-open-route', path: llmQuotaNoticeTargetPath }, window.location.origin)
    return
  }
  window.location.assign(buildAppRouteURL(llmQuotaNoticeTargetPath))
}

function showLLMQuotaNotice(notice: LLMQuotaNotice) {
  const noticeID = String(notice.notice_id || '').trim()
  if (!noticeID) {
    return
  }
  const key = `camera2|${noticeID}`
  if (shownLLMQuotaNotices.has(key)) {
    return
  }
  if (!markLLMQuotaNoticeShown('camera2', notice)) {
    return
  }
  shownLLMQuotaNotices.add(key)
  const { title, description } = resolveLLMQuotaNoticeContent(notice)
  notification.warning({
    message: title,
    description,
    placement: 'topRight',
    duration: 8,
    onClick: openLLMUsagePage,
  })
}

useAlarmFeed({
  onLLMQuotaNotice: showLLMQuotaNotice,
})

const updateTime = () => {
  const now = new Date()
  const year = now.getFullYear()
  const month = String(now.getMonth() + 1).padStart(2, '0')
  const day = String(now.getDate()).padStart(2, '0')

  currentDate.value = `${year}-${month}-${day}`
  currentWeek.value = weekDays[now.getDay()] ?? ''
  currentTime.value = now.toLocaleTimeString('zh-CN', {
    hour12: false,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

const loadProfile = async () => {
  try {
    const profile = await fetchCamera2Profile()
    username.value = profile.username
    if (profile.llmQuotaNotice) {
      showLLMQuotaNotice(profile.llmQuotaNotice)
    }
  } catch {
    username.value = '-'
  }
}

onMounted(() => {
  updateTime()
  timer = window.setInterval(updateTime, 1000)
  void loadProfile()
})

onUnmounted(() => {
  if (timer) {
    clearInterval(timer)
  }
})
</script>

<style lang="less">
.dashboard-container {
  position: relative;
  width: 1920px;
  height: 1080px;
  overflow: hidden;
  color: #dce9ff;
  background: url('./assets/images/bg-page.png') no-repeat center center / cover;
}

.dashboard-main {
  position: relative;
  z-index: 1;
}

.dashboard-column {
  min-width: 0;
  min-height: 0;
}


</style>

<style scoped lang="less">
.dashboard-container {
  
}

.dashboard-header {
  position: relative;
  z-index: 1;
  height: 115px;
  background: url('./assets/images/bg-header-title.png') no-repeat center center / cover;
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: start;
  margin-bottom: 10px;
}

.header-side {
  display: flex;
  align-items: flex-start;
  height: 100%;
}

.header-left {
  justify-content: flex-start;
  padding: 24px 0 0 20px;
}

.date-time {
  display: flex;
  align-items: center;
  gap: 22px;
  font-size: 21px;
  color: #f6fbff;
  text-shadow: 0 0 10px rgba(80, 180, 255, 0.3);

  .week {
    font-weight: 700;
  }

  .time {
    letter-spacing: 3px;
  }
}

.header-center {
  display: flex;
  justify-content: center;
}

.header-frame {
  position: relative;
  width: 760px;
  height: 72px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.header-title {
  font-size: 34px;
  letter-spacing: 2px;
  color: #f5fbff;
  font-weight: 900;
  text-shadow:
    0 0 18px rgba(145, 225, 255, 0.5),
    0 0 4px rgba(255, 255, 255, 0.4);
}

.header-line {
  position: absolute;
  top: 20px;
  width: 140px;
  height: 10px;
}

.header-line::before,
.header-line::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 10px;
}

.header-line::before {
  background: linear-gradient(90deg, transparent 0%, rgba(76, 203, 255, 0.85) 100%);
}

.header-line::after {
  top: 14px;
  width: 46px;
  height: 6px;
  border-radius: 999px;
  background: radial-gradient(circle, #ffe44b 0 45%, transparent 48%);
  background-size: 16px 6px;
  background-repeat: repeat-x;
}

.header-line.left {
  left: -164px;
}

.header-line.right {
  right: -164px;
  transform: scaleX(-1);
}

.header-right {
  justify-content: flex-end;
  padding: 24px 20px 0 0;
}

.user-info {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 18px;
  color: #f7fbff;
}

.username {
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.avatar {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background:
    radial-gradient(circle at 50% 35%, #fef3dc 0 22%, transparent 23%),
    radial-gradient(circle at 50% 72%, #e2d0b4 0 28%, transparent 29%),
    linear-gradient(180deg, #78533c 0%, #4d301f 100%);
  box-shadow: 0 0 0 2px rgba(255, 255, 255, 0.65);
}

.header-back {
  height: 28px;
  padding: 0 12px;
  border-radius: 999px;
  border: 1px solid rgba(118, 217, 255, 0.42);
  background: rgba(3, 18, 43, 0.72);
  color: #e8f6ff;
  cursor: pointer;
}

.dashboard-main {
  height: calc(100% - 96px);
  display: grid;
  grid-template-columns: 443px 1fr 443px;
  gap: 27px;
  margin-top: -39px;
  padding: 0 10px;
}

.left-column,
.right-column {
  display: grid;
  gap: 10px;
}

.center-column {
  display: grid;
  grid-template-rows: 600px 1fr;
  gap: 12px;
}
</style>
