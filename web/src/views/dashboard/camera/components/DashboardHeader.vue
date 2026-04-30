<template>
  <div class="dashboard-header">
    <div class="center-section">
      <h1 class="title">多模态视频巡检中心</h1>
    </div>
    <div class="left-section"></div>
    <div class="right-section">
      <div class="time">{{ currentTime }}</div>
      <div class="nav-btn" @click="emit('exit')">退出大屏</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'

const emit = defineEmits<{
  (e: 'exit'): void
}>()

const currentTime = ref('')
let timer: number | null = null

function updateTime() {
  const now = new Date()
  currentTime.value = now.toLocaleString('zh-CN')
}

onMounted(() => {
  updateTime()
  timer = window.setInterval(updateTime, 1000)
})

onUnmounted(() => {
  if (timer !== null) {
    window.clearInterval(timer)
  }
})
</script>

<style lang="less" scoped>
.dashboard-header {
  height: 88px;
  width: 100%;
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 40px;
  background-image: url('@/assets/dashboard/title-bg.png');
  background-size: cover;
  position: relative;
  z-index: 10;

  .left-section {
    display: flex;
    gap: 20px;
  }

  .nav-btn {
    padding: 8px 24px;
    background: rgba(0, 77, 161, 0.3);
    border: 1px solid #004da1;
    color: #ffffff;
    clip-path: polygon(10% 0, 100% 0, 90% 100%, 0% 100%);
    cursor: pointer;
    font-weight: bold;
    transition: all 0.3s;

    &:hover {
      background: #004da1;
      box-shadow: 0 0 10px #004da1;
    }
  }

  .center-section {
    position: absolute;
    left: 0;
    top: 0;
    right: 0;
    line-height: 88px;
    text-align: center;

    .title {
      font-size: 32px;
      margin: 0;
      color: #fff;
      font-weight: 900;
      text-shadow: 0 0 10px #00d2ff;
      letter-spacing: 4px;
      font-style: italic;
    }
  }

  .right-section {
    width: 420px;
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: 16px;

    .time {
      font-size: 18px;
      color: #a0cfff;
      font-family: monospace;
    }
  }
}
</style>
