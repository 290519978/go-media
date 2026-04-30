<template>
  <div class="monitor-grid-container">
    <MainView v-if="!isFullscreen" @fullscreenClick="handleFullscreenClick" />
  </div>
  <Teleport to="body">
    <FullscreenGrid v-if="isFullscreen" @fullscreenClick="handleFullscreenClick" />
  </Teleport>
</template>

<script setup lang="ts">
  import { useWidgetData } from "./useWidgetData";
  import FullscreenGrid from "./fullscreenGrid.vue";
  import MainView from "./MainView.vue";

  const { isFullscreen } = useWidgetData();
  const handleFullscreenClick = (status: boolean) => {
    isFullscreen.value = status;
  };
</script>

<style scoped lang="less">
  .monitor-grid-container {
    width: 100%;
    height: 100%;
  }
</style>
