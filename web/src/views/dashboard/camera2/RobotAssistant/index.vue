<template>
  <div class="robot-assistant">
    <div class="assistant-bar">
      <input
        v-model="prompt"
        type="text"
        placeholder="请输入指令(如：在办公室设备中找人)"
        @keydown.enter.prevent="handleSend"
      />
      <!-- <button class="mic-btn" aria-label="mic" disabled></button> -->
      <button class="send-btn" :class="{ loading: patrolSubmitting }" aria-label="send" @click="handleSend"></button>
    </div>
    <div class="robot-figure">
      <img class="robot-image" :src="robotImage" alt="任务巡查机器人" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { message } from 'ant-design-vue'

import robotImage from '../assets/images/daping2jiqiren.png'
import { useWidgetData } from '../MonitorGrid/useWidgetData'
import { usePatrolCenter } from '../hooks/usePatrolCenter'

const prompt = ref('')
const { allVideoList } = useWidgetData()
const { patrolSubmitting, startPatrol } = usePatrolCenter()

async function handleSend() {
  const nextPrompt = String(prompt.value || '').trim()
  if (!nextPrompt) {
    message.error('请输入任务巡查提示词')
    return
  }
  const deviceIDs = allVideoList.value
    .map((item) => String(item.id || '').trim())
    .filter(Boolean)
  if (deviceIDs.length === 0) {
    message.error('当前没有可巡查的视频设备')
    return
  }
  try {
    await startPatrol({
      device_ids: deviceIDs,
      prompt: nextPrompt,
    })
    prompt.value = ''
  } catch (error) {
    message.error((error as Error).message || '创建任务巡查失败')
  }
}
</script>

<style scoped lang="less">
.robot-assistant {
  position: absolute;
  right: 30px;
  bottom: 12px;
  z-index: 2;
  display: flex;
  align-items: flex-end;
  gap: 12px;
}

.assistant-bar {
  width: 360px;
  height: 38px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 12px;
  border: 1px solid rgba(77, 190, 255, 0.42);
  border-radius: 20px;
  background:
    linear-gradient(180deg, rgba(17, 56, 108, 0.92) 0%, rgba(8, 32, 70, 0.95) 100%);
  box-shadow: inset 0 0 16px rgba(98, 215, 255, 0.12);

  input {
    flex: 1;
    min-width: 0;
    background: transparent;
    border: none;
    color: #dff4ff;
    outline: none;
    font-size: 14px;

    &::placeholder {
      color: rgba(181, 214, 244, 0.75);
    }
  }
}

.mic-btn,
.send-btn {
  width: 20px;
  height: 20px;
  background: transparent;
  border: none;
  position: relative;
}

.mic-btn {
  cursor: not-allowed;
  opacity: 0.5;
}

.send-btn {
  cursor: pointer;

  &.loading {
    opacity: 0.6;
    cursor: wait;
  }
}

.mic-btn::before {
  content: '';
  position: absolute;
  left: 6px;
  top: 2px;
  width: 8px;
  height: 12px;
  border: 2px solid #eaf7ff;
  border-bottom-left-radius: 6px;
  border-bottom-right-radius: 6px;
  border-top-left-radius: 4px;
  border-top-right-radius: 4px;
}

.mic-btn::after {
  content: '';
  position: absolute;
  left: 8px;
  bottom: 1px;
  width: 4px;
  height: 5px;
  background: #eaf7ff;
}

.send-btn::before {
  content: '';
  position: absolute;
  inset: 2px;
  clip-path: polygon(0 50%, 100% 0, 68% 100%, 56% 62%);
  background: #eaf7ff;
}

.robot-figure {
  display: flex;
  align-items: flex-end;
}

.robot-image {
  display: block;
  width: 96px;
  height: auto;
  object-fit: contain;
}
</style>
