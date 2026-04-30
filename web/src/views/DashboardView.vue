<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import CameraDashboard from '@/views/dashboard/camera/index.vue'

type DashboardScreenKey = 'camera' | 'camera2'

type DashboardEntry = {
  key: DashboardScreenKey
  title: string
  preview: string
  description: string
  badge?: string
}

const camera2FrameRef = ref<HTMLIFrameElement | null>(null)
const activeScreen = ref<DashboardScreenKey | null>(null)
const camera2PageURL = `${import.meta.env.BASE_URL}camera2.html`
const router = useRouter()

const entryList: DashboardEntry[] = [
  {
    key: 'camera',
    title: '摄像头监控大屏',
    preview: '摄像头监控大屏',
    description: '聚焦固定点位巡检，实时展示设备画面、告警数据与系统运行状态。',
  },
  {
    key: 'camera2',
    title: '鸿眸多模态视频监控预警中心',
    preview: '鸿眸多模态视频监控预警中心',
    description: '当前使用前端假数据演示第二套大屏布局，用于预览多模态监控与预警中心效果。',
    // badge: '假数据演示',
  },
]

function enterScreen(screenKey: DashboardScreenKey) {
  activeScreen.value = screenKey
}

function exitScreen() {
  activeScreen.value = null
}

function handleCamera2Message(event: MessageEvent<unknown>) {
  if (activeScreen.value !== 'camera2') return
  if (event.origin !== window.location.origin) return
  if (!camera2FrameRef.value?.contentWindow || event.source !== camera2FrameRef.value.contentWindow) return
  const payload = event.data as { type?: string; path?: string } | null
  if (payload?.type === 'camera2-exit') {
    void exitScreen()
    return
  }
  if (payload?.type !== 'camera2-open-route') return
  const path = String(payload.path || '').trim()
  activeScreen.value = null
  if (path) {
    void router.push(path)
  }
}

onMounted(() => {
  window.addEventListener('message', handleCamera2Message)
})

onBeforeUnmount(() => {
  window.removeEventListener('message', handleCamera2Message)
})
</script>

<template>
  <div class="dashboard-root">
    <section v-if="!activeScreen" class="entry-page">
      <header class="entry-head">
        <h2>多模态智能巡检中心</h2>
        <p>请选择要进入的数据大屏，当前共提供 2 套展示视图。</p>
      </header>

      <div class="entry-grid">
        <a-card
          v-for="entry in entryList"
          :key="entry.key"
          class="entry-card"
          :class="`entry-card--${entry.key}`"
          hoverable
          @click="enterScreen(entry.key)"
        >
          <div class="entry-preview">
            <span class="entry-preview__text">{{ entry.preview }}</span>
            <span v-if="entry.badge" class="entry-badge">{{ entry.badge }}</span>
          </div>
          <div class="entry-title">{{ entry.title }}</div>
          <div class="entry-desc">{{ entry.description }}</div>
        </a-card>
      </div>
    </section>

    <section v-else class="screen-page">
      <CameraDashboard v-if="activeScreen === 'camera'" @exit="exitScreen" />
      <iframe
        v-else
        ref="camera2FrameRef"
        class="camera2-frame"
        :src="camera2PageURL"
        title="鸿眸多模态视频监控预警中心"
      />
    </section>
  </div>
</template>

<style scoped>
.dashboard-root {
  min-height: calc(100vh - 120px);
}

.entry-page {
  background: #f8fafc;
  border: 1px solid #e6ecf5;
  border-radius: 10px;
  padding: 18px;
  min-height: calc(100vh - 140px);
}

.entry-head h2 {
  margin: 0;
  font-size: 22px;
  color: #253043;
}

.entry-head p {
  margin: 10px 0 0;
  color: #617287;
  line-height: 1.6;
}

.entry-grid {
  margin-top: 18px;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 420px));
  gap: 16px;
}

.entry-card {
  cursor: pointer;
  border: 1px solid #dfe8f5;
}

.entry-preview {
  position: relative;
  height: 180px;
  border-radius: 8px;
  color: #eaf5ff;
  display: flex;
  align-items: flex-end;
  padding: 18px;
  overflow: hidden;
}

.entry-card--camera .entry-preview {
  background:
    linear-gradient(120deg, rgba(0, 77, 161, 0.9), rgba(6, 34, 72, 0.92)),
    radial-gradient(circle at 25% 15%, rgba(126, 214, 255, 0.45), transparent 40%);
}

.entry-card--camera2 .entry-preview {
  background:
    linear-gradient(135deg, rgba(0, 45, 89, 0.96), rgba(7, 90, 154, 0.92)),
    radial-gradient(circle at 78% 18%, rgba(43, 190, 255, 0.42), transparent 32%),
    radial-gradient(circle at 18% 78%, rgba(0, 255, 209, 0.18), transparent 34%);
}

.entry-preview__text {
  max-width: 260px;
  font-size: 22px;
  font-weight: 700;
  line-height: 1.45;
}

.entry-badge {
  position: absolute;
  top: 14px;
  right: 14px;
  padding: 4px 10px;
  border-radius: 999px;
  background: rgba(7, 18, 40, 0.36);
  border: 1px solid rgba(154, 220, 255, 0.35);
  color: #dff6ff;
  font-size: 12px;
  line-height: 1;
}

.entry-title {
  margin-top: 12px;
  font-size: 16px;
  font-weight: 600;
  color: #2b3950;
}

.entry-desc {
  margin-top: 8px;
  color: #5d6d85;
  line-height: 1.6;
}

.screen-page {
  position: fixed;
  inset: 0;
  z-index: 3000;
  overflow: hidden;
  background: #05162e;
}

.camera2-frame {
  width: 100%;
  height: 100%;
  border: none;
  display: block;
  background: #021536;
}
</style>
