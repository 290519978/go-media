<template>
  <div class="app-container">
    <div class="screen-stage" :style="stageStyle">
      <ConfigProvider :theme="{
        token: {
          colorPrimary: '#3B79BF', 
          colorPrimaryActive: '#0082e6',
          controlItemBgActive: '#0082e625',
          colorBorder: '#3B79BF',
          colorBgElevated: '#021536',
          controlItemBgActiveHover: '#0082e625',
          colorBorderSecondary: '#3B79BF',
          colorPrimaryHover: '#3B79BF',
          colorBgTextActive: '#3B79BF',
          controlOutlineWidth:0,
          colorBgContainer: '#021536',
          colorTextPlaceholder: '#9EC2E9',
          colorTextBase: '#9EC2E9',
          borderRadius: 4,                // 全局圆角
          // fontSize: 14
        }
      }">
        <DashboardView @exit="requestExit" />
      </ConfigProvider>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ConfigProvider } from 'ant-design-vue'
import DashboardView from './DashboardView.vue'
import { useScale } from './hooks/useScale'

const { stageStyle } = useScale()

function requestExit() {
  // 独立页面通过 postMessage 通知父层关闭 iframe，从而保持样式与组件树双向隔离。
  if (window.parent && window.parent !== window) {
    window.parent.postMessage({ type: 'camera2-exit' }, window.location.origin)
    return
  }
  window.location.replace(import.meta.env.BASE_URL)
}
</script>

<style scoped>
.app-container {
  position: relative;
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  background: #021536;
}

.screen-stage {
  position: relative;
  flex: 0 0 auto;
}

.screen-stage > * {
  position: absolute;
  inset: 0 auto auto 0;
  width: 1920px;
  height: 1080px;
  transform: scale(var(--screen-scale));
  transform-origin: top left;
}
</style>
