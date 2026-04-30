<template>
  <div class="snapshot-preview">
    <div class="preview-toolbar">
      <button class="toolbar-btn" :disabled="!hasBoxes" @click="toggleBoxes">
        {{ showBoxes ? '隐藏线框' : '显示线框' }}
      </button>
      <slot name="toolbar"></slot>
    </div>

    <div class="preview-stage">
      <template v-if="imageUrl">
        <img :src="imageUrl" class="preview-image" alt="snapshot" />
        <slot></slot>
        <template v-if="showBoxes && hasBoxes">
          <div
            v-for="(box, index) in drawableBoxes"
            :key="`${box.label}-${index}`"
            class="box"
            :style="boxStyle(box)"
          >
            <span class="box-label" :title="boxLabel(box)">{{ boxLabel(box) }}</span>
          </div>
        </template>
      </template>
      <div v-else class="empty-state">{{ emptyText }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Camera2DetectedBox } from '../api'

const props = withDefaults(defineProps<{
  imageUrl?: string
  boxes?: Camera2DetectedBox[]
  emptyText?: string
  resetKey?: string | number
}>(), {
  imageUrl: '',
  boxes: () => [],
  emptyText: '暂无报警截图',
  resetKey: '',
})

const showBoxes = ref(true)
const drawableBoxes = computed(() => props.boxes.filter((item) => item.w > 0 && item.h > 0))
const hasBoxes = computed(() => drawableBoxes.value.length > 0)

watch(
  () => props.resetKey,
  () => {
    showBoxes.value = true
  },
  { immediate: true },
)

function toggleBoxes() {
  if (!hasBoxes.value) {
    return
  }
  showBoxes.value = !showBoxes.value
}

function clampBoxValue(value: number) {
  if (!Number.isFinite(value)) {
    return 0
  }
  if (value < 0) {
    return 0
  }
  if (value > 1) {
    return 1
  }
  return value
}

function boxStyle(box: Camera2DetectedBox) {
  const width = clampBoxValue(box.w)
  const height = clampBoxValue(box.h)
  const centerX = clampBoxValue(box.x)
  const centerY = clampBoxValue(box.y)
  return {
    left: `${Math.max(0, centerX - width / 2) * 100}%`,
    top: `${Math.max(0, centerY - height / 2) * 100}%`,
    width: `${width * 100}%`,
    height: `${height * 100}%`,
  }
}

function boxLabel(box: Camera2DetectedBox) {
  const label = String(box.label || '-').trim() || '-'
  const confidence = Number(box.confidence || 0)
  if (confidence <= 0) {
    return label
  }
  return `${label} ${(confidence * 100).toFixed(1)}%`
}
</script>

<style scoped lang="less">
.snapshot-preview {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 0;
}

.preview-toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
}

.toolbar-btn {
  min-width: 94px;
  height: 30px;
  padding: 0 12px;
  border: 1px solid rgba(114, 204, 255, 0.5);
  border-radius: 4px;
  background: rgba(13, 61, 112, 0.62);
  color: #e8f7ff;
  cursor: pointer;

  &:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }
}

.preview-stage {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: hidden;
  border-radius: 10px;
  background: #07162f;
}

.preview-image {
  width: 100%;
  height: 100%;
  display: block;
  object-fit: contain;
  background: #02060e;
}

.box {
  position: absolute;
  border: 2px solid #ff4d4f;
  box-sizing: border-box;
  pointer-events: none;
}

.box-label {
  position: absolute;
  left: 0;
  top: -24px;
  max-width: 220px;
  padding: 2px 6px;
  border-radius: 4px;
  background: rgba(255, 77, 79, 0.92);
  color: #fff;
  font-size: 11px;
  line-height: 18px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.empty-state {
  width: 100%;
  height: 100%;
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: rgba(226, 239, 255, 0.72);
  font-size: 14px;
}
</style>
