<template>
  <div class="fullscreen-grid">
    <div class="main-view">
      <MainView @fullscreenClick="handleFullscreenClick" />
    </div>

    <img src="../assets/images/kuang-left.png" alt="" class="kuang-left">
    <img src="../assets/images/kuang-right.png" alt="" class="kuang-right">
  </div>
</template>
<script setup lang="ts">
  import MainView from './MainView.vue'

  const emits = defineEmits(['fullscreenClick'])

  const handleFullscreenClick = (status: boolean) => {
    emits('fullscreenClick', status)
  }
</script>

<style scoped lang="less">
.fullscreen-grid {
  position: fixed;
  bottom: 16px;
  left: 0;
  width: 100%;
  height: calc(90% + 6px);
  z-index: 1000;
  background-color: #021537;
  display: flex;
  align-items: center;
  justify-content: center;
  .kuang-left {
    position: absolute;
    top: 50%;
    left: 0;
    transform: translateY(-50%);
    width: 241px;
    height: 448px;
    pointer-events: none;
  }
  .kuang-right {
    position: absolute;
    top: 50%;
    right: 0;
    transform: translateY(-50%);
    width: 241px;
    height: 448px;
    pointer-events: none;
  }
  .main-view {
    width: 80%;
    height: 100%;
  }
}
</style>
