<template>
  <div class="camera-container">
    <ScaleBox :width="1920" :height="1080">
      <div class="dashboard-container">
        <DashboardHeader @exit="emit('exit')" />
        <div class="main-content">
          <div class="left-column">
            <LeftPanel />
          </div>
          <div class="center-column">
            <CenterPanel />
          </div>
          <div class="right-column">
            <RightPanel />
          </div>
        </div>

        <div class="footer">
          <FooterPanel />
        </div>
      </div>
    </ScaleBox>
  </div>
</template>

<script setup lang="ts">
import ScaleBox from './components/ScaleBox.vue'
import DashboardHeader from './components/DashboardHeader.vue'
import LeftPanel from './components/LeftPanel.vue'
import CenterPanel from './components/CenterPanel.vue'
import RightPanel from './components/RightPanel.vue'
import FooterPanel from './components/FooterPanel.vue'

const emit = defineEmits<{
  (e: 'exit'): void
}>()
</script>

<style lang="less" scoped>
.camera-container {
  width: 100vw;
  height: 100vh;
  background: #05162E;
}

.dashboard-container {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  color: #ffffff;

  .main-content {
    flex: 1;
    display: flex;
    padding: 0 20px;
    gap: 20px;
    overflow: hidden;
    margin-top: 20px;

    .left-column {
      width: 20%;
    }

    .center-column {
      width: 60%;
    }

    .right-column {
      width: 20%;
    }
  }

  .footer {
    width: 100%;
    height: 48px;
    margin-top: 20px;
  }
}
</style>
