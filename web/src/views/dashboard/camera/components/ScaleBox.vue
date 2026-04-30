<template>
  <div
    class="scale-box"
    ref="scaleBoxRef"
    :style="{
      width: width + 'px',
      height: height + 'px',
    }"
  >
    <slot></slot>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

// 定义组件属性
const props = withDefaults(defineProps<{
  width?: number
  height?: number
}>(), {
  width: 1920,
  height: 1080
})

const scaleBoxRef = ref<HTMLElement | null>(null)
const scale = ref(1)

// 获取缩放比例
const getScale = () => {
  const { width, height } = props
  const wh = window.innerHeight / height
  const ww = window.innerWidth / width
  return ww < wh ? ww : wh
}

// 设置缩放
const setScale = () => {
  if (scaleBoxRef.value) {
    scale.value = getScale()
    scaleBoxRef.value.style.setProperty('--scale', scale.value.toString())
  }
}

// 防抖函数
const debounce = (fn: () => void, delay: number) => {
  let timer: ReturnType<typeof setTimeout> | null = null
  return () => {
    if (timer !== null) clearTimeout(timer)
    timer = setTimeout(() => {
      fn()
    }, delay)
  }
}

const resize = debounce(setScale, 100)

onMounted(() => {
  setScale()
  window.addEventListener('resize', resize)
})

onUnmounted(() => {
  window.removeEventListener('resize', resize)
})
</script>

<style lang="less" scoped>
.scale-box {
  position: absolute;
  transform: translate(-50%, -50%) scale(var(--scale));
  display: flex;
  flex-direction: column;
  transform-origin: center center;
  left: 50%;
  top: 50%;
  transition: 0.3s;
  z-index: 999;
}
</style>
