<template>
  <div class="panel monitor-grid">
    <div class="panel-header">
      <div class="header-left">
        <div class="panel-title-wrap">
          <div class="panel-icon">
            <img src="../assets/images/icon-camera.png" alt="监控画面" />
          </div>
          <span class="panel-title">监控画面</span>
        </div>

        <div class="header-filters">
          <a-select
            v-model:value="selectedRegion"
            placeholder="请选择区域"
            style="width: 120px"
            popup-class-name="camera2-monitor-filter-popup"
            placement="bottomLeft"
            :dropdown-match-select-width="false"
            :dropdown-style="filterDropdownStyle"
            :get-popup-container="getPopupContainer">
            <a-select-option v-for="item in regionOptions" :key="item.value" :value="item.value">{{
              item.label
            }}</a-select-option>
          </a-select>
          <a-select
            v-model:value="selectedAlgorithm"
            placeholder="请选择算法"
            style="width: 120px"
            popup-class-name="camera2-monitor-filter-popup"
            placement="bottomLeft"
            :dropdown-match-select-width="false"
            :dropdown-style="filterDropdownStyle"
            :get-popup-container="getPopupContainer">
            <a-select-option v-for="item in algorithmOptions" :key="item.value" :value="item.value">{{
              item.label
            }}</a-select-option>
          </a-select>
          <a-select
            v-model:value="selectedDeviceStatus"
            placeholder="请选择设备状态"
            style="width: 120px"
            popup-class-name="camera2-monitor-filter-popup"
            placement="bottomLeft"
            :dropdown-match-select-width="false"
            :dropdown-style="filterDropdownStyle"
            :get-popup-container="getPopupContainer">
            <a-select-option v-for="item in deviceStatusOptions" :key="item.value" :value="item.value">{{
              item.label
            }}</a-select-option>
          </a-select>
          <a-input v-model:value="searchText" style="width: 180px" placeholder="搜索设备名称">
            <template #suffix>
              <SearchOutlined />
            </template>
          </a-input>
          <div class="pagination">
            <span class="page-btn" @click="handleLayoutChange(1)" :class="{ active: currentLayout === 1 }">1</span>
            <span class="page-btn" @click="handleLayoutChange(4)" :class="{ active: currentLayout === 4 }">4</span>
            <span class="page-btn" @click="handleLayoutChange(9)" :class="{ active: currentLayout === 9 }">9</span>
          </div>
          <button class="task-btn" @click="handleConfigClick">任务巡查</button>
        </div>
      </div>

      <button
        v-if="!isFullscreen"
        @click="handleFullscreenClick"
        class="fullscreen-btn"
        aria-label="fullscreen"></button>
      <button
        v-if="isFullscreen"
        @click="handleFullscreenClick"
        class="fullscreen-btn not-fullscreen"
        aria-label="fullscreen"></button>
    </div>

    <div class="panel-content">
      <div class="pre-page" :style="{left: isFullscreen ? '-100px' : '-12px'}" @click="handlePrePageClick"></div>
      <div class="next-page" :style="{right: isFullscreen ? '-100px' : '-12px'}" @click="handleNextPageClick"></div>
      <div class="video-grid" :class="`layout-${currentLayout}`">
        <div
          class="video-item"
          :class="{ active: item.id === activeVideo }"
          @click="handleClick(item.id)"
          v-for="item in videoList"
          :key="item.id">
          <div
            v-if="displayAlgorithmText(item)"
            class="overlay-tags"
            :class="{ 'has-warning': item.alarming60s }">
            <span class="tag purple" :title="displayAlgorithmText(item)">
              {{ displayAlgorithmText(item) }}
            </span>
          </div>

          <div class="video-warning" v-if="item.alarming60s">
            <svg
              t="1773991348069"
              class="icon"
              viewBox="0 0 1024 1024"
              version="1.1"
              xmlns="http://www.w3.org/2000/svg"
              p-id="8507"
              width="64"
              height="64">
              <path
                d="M175.787 919.893V604.16c0-189.44 153.6-343.04 343.04-343.04s343.04 153.6 343.04 343.04v315.733h117.76c23.893 0 44.373 20.48 44.373 44.373s-20.48 44.373-44.373 44.373H46.08c-23.893 0-44.373-20.48-44.373-44.373s20.48-44.373 44.373-44.373h129.707z m368.64-520.533l-168.96 278.187h134.827L476.161 885.76l168.96-278.187H510.294l34.133-208.213zM802.133 93.867c15.36 8.533 20.48 29.013 11.947 46.08L752.64 245.76l-58.027-32.427 61.44-105.813c10.24-17.067 30.72-22.187 46.08-13.653zM518.827 15.36c20.48 0 35.84 15.36 35.84 34.133V168.96h-71.68V49.493c0-18.773 15.36-34.133 35.84-34.133zM235.52 93.867c15.36-8.533 35.84-3.413 44.373 11.947l61.44 105.813-58.027 32.427-61.44-105.813c-8.533-15.36-3.413-34.133 13.653-44.373zM27.307 302.08c8.533-15.36 29.013-22.187 44.373-11.947l105.813 61.44-32.427 58.027-105.813-61.44c-15.36-8.533-20.48-29.013-11.947-46.08z m983.04 0c8.533 15.36 3.413 35.84-11.947 46.08L892.587 409.6l-32.427-58.027 105.813-61.44c15.36-8.533 35.84-3.413 44.373 11.947z"
                p-id="8508"></path>
            </svg>
          </div>

          <LiveScreen
            class="video-image"
            :live-url="item.liveUrl"
            :stream-app="item.streamApp"
            :stream-id="item.streamID"
          />

          <div class="video-footer">
            <span class="camera-id">{{ item.name }}</span>
            <span class="camera-location">{{ item.areaName }}</span>
            <!-- <span class="camera-meta">{{ item.statusText }}</span> -->
          </div>

          <div class="video-actions">
            <!-- <i class="icon-handler" @click="handleOpenAlgorithmManageDialog(item.id)">-</i> -->
            <i class="icon-handler" @click="handleOpenAlgorithmManageDialog(item.id)">+</i>
            <img
              class="icon-setting"
              @click="handleTaskConfigClick(item.id)"
              src="../assets/images/icon-setting.png"
              alt="设置" />
          </div>
        </div>
      </div>
    </div>
  </div>
  <DialogConfig ref="configDialog" />
  <DialogTaskConfig ref="taskConfigDialog" @saved="handleRealtimeRefresh" />
  <DialogAlgorithmManage ref="algorithmManageDialog" @saved="handleRealtimeRefresh" />
</template>
<script setup lang="ts">
  import { onMounted, onUnmounted, ref } from "vue";
  import { SearchOutlined } from "@ant-design/icons-vue";
  import { useWidgetData } from "./useWidgetData";
  import LiveScreen from "@/views/dashboard/camera/components/LiveScreen.vue";
  import DialogConfig from "./DialogConfig.vue";
  import DialogTaskConfig from "./DialogTaskConfig.vue";
  import DialogAlgorithmManage from "./DialogAlgorithmManage.vue";
  const {
    selectedRegion,
    regionOptions,
    selectedAlgorithm,
    algorithmOptions,
    selectedDeviceStatus,
    deviceStatusOptions,
    searchText,
    currentLayout,
    handleLayoutChange,
    videoList,
    activeVideo,
    handleClick,
    isFullscreen,
    handlePrePageClick,
    handleNextPageClick,
    loadWidgetData,
  } = useWidgetData();

  const filterDropdownStyle = {
    minWidth: "220px",
    maxWidth: "420px",
    zIndex: 12000,
  };

  // 普通态挂到 app-container，避开 screen-stage 缩放层对定位和遮罩的影响；全屏态继续跟随覆盖层容器。
  function getPopupContainer(triggerNode?: HTMLElement): HTMLElement {
    const fullscreenContainer = triggerNode?.closest(".fullscreen-grid") as HTMLElement | null;
    if (fullscreenContainer) {
      return fullscreenContainer;
    }
    const appContainer = triggerNode?.closest(".app-container") as HTMLElement | null;
    if (appContainer) {
      return appContainer;
    }
    return document.body;
  }

  const emits = defineEmits(["fullscreenClick"]);
  const handleFullscreenClick = () => {
    emits("fullscreenClick", !isFullscreen.value);
  };

  const handleRealtimeRefresh = () => {
    void loadWidgetData();
  };

  const displayAlgorithmText = (item: { algorithms?: string[] }) => {
    return Array.isArray(item.algorithms) ? item.algorithms.join(" / ") : "";
  };

  type DialogInstance = {
    open: () => void;
    close: () => void;
  };

  type DeviceDialogInstance = {
    open: (deviceID: string) => void;
    close: () => void;
  };

  const taskConfigDialog = ref<DeviceDialogInstance | null>(null);
  const handleTaskConfigClick = (id: string) => {
    taskConfigDialog.value?.open(id);
  };
  const algorithmManageDialog = ref<DeviceDialogInstance | null>(null);
  const handleOpenAlgorithmManageDialog = (id: string) => {
    algorithmManageDialog.value?.open(id);
  };

  const configDialog = ref<DialogInstance | null>(null);

  const handleConfigClick = () => {
    configDialog.value?.open();
  };

  onMounted(() => {
    void loadWidgetData();
    window.addEventListener("maas-alarm", handleRealtimeRefresh);
  });

  onUnmounted(() => {
    window.removeEventListener("maas-alarm", handleRealtimeRefresh);
  });
</script>

<style scoped lang="less">
  .monitor-grid {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    .panel-header {
      height: 40px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
      padding: 0 10px;
      background: linear-gradient(180deg, #001c3c 0%, #074084 100%);
      border: 2px solid #00356d;
      border-radius: 4px;
      border-bottom: 1px solid rgba(66, 183, 255, 0.55);
    }

    .header-left {
      flex: 1;
      min-width: 0;
      display: flex;
      align-items: center;
      gap: 12px;
    }

    .panel-title-wrap {
      display: flex;
      align-items: center;
      gap: 10px;
      flex: 0 0 auto;
    }

    .panel-icon {
      width: 26px;
      height: 24px;

      img {
        width: 100%;
        height: 100%;
        object-fit: cover;
      }
    }

    .panel-title {
      font-size: 24px;
      line-height: 24px;
      font-weight: bold;
      color: #ffffff;
    }

    .header-filters {
      min-width: 0;
      display: flex;
      align-items: center;
      gap: 10px;
      flex: 1;
    }

    .filter-select {
      min-width: 120px;
      padding: 0 30px 0 12px;
      font-size: 13px;
      appearance: none;
      background-image:
        linear-gradient(45deg, transparent 50%, #8ec5ef 50%), linear-gradient(135deg, #8ec5ef 50%, transparent 50%),
        linear-gradient(180deg, rgba(18, 56, 108, 0.9) 0%, rgba(11, 38, 77, 0.92) 100%);
      background-position:
        calc(100% - 16px) 14px,
        calc(100% - 11px) 14px,
        0 0;
      background-size:
        5px 5px,
        5px 5px,
        100% 100%;
      background-repeat: no-repeat;
    }

    .search-box {
      position: relative;
      width: 180px;
    }

    .search-box input {
      width: 100%;
      padding: 0 34px 0 12px;
      font-size: 13px;
      outline: none;
    }

    .search-icon {
      position: absolute;
      right: 12px;
      top: 9px;
      width: 14px;
      height: 14px;
      border: 2px solid #a5d6ff;
      border-radius: 50%;

      &::after {
        content: "";
        position: absolute;
        width: 6px;
        height: 2px;
        right: -4px;
        bottom: -1px;
        background: #a5d6ff;
        transform: rotate(45deg);
        transform-origin: right center;
      }
    }

    .pagination {
      display: flex;
      gap: 4px;
      background: #123154;
      padding: 2px;
    }

    .page-btn {
      width: 24px;
      height: 24px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      font-size: 14px;
      color: #fff;
      cursor: pointer;

      &.active {
        background: #0293fd;
        border-radius: 4px;
      }
    }

    .task-btn {
      width: 82px;
      height: 30px;
      background: url("../assets/images/bg-btn2.png") no-repeat center center / cover;
      font-size: 14px;
      color: #ecfbff;
    }

    .fullscreen-btn {
      width: 22px;
      height: 22px;
      background: url("../assets/images/icon-full-screen.png") no-repeat center center / cover;
      cursor: pointer;
      &.not-fullscreen {
        background-image: url("../assets/images/not-fullscreen.png");
      }
    }

    .panel-content {
      flex: 1;
      padding: 10px 0;
      min-height: 0;
      position: relative;
      > .pre-page:hover,
      > .next-page:hover {
        width: 14px;
        height: 42px;
      }
      .pre-page {
        position: absolute;
        top: 50%;
        transform: translateY(-50%);
        width: 12px;
        height: 34px;
        background-image: url("../assets/images/left-page.png");
        background-size: 100% 100%;
        cursor: pointer;
        z-index: 9;
        transition: transform 0.3s ease-in-out;
      }
      .next-page {
        position: absolute;
        top: 50%;
        transform: translateY(-50%);
        width: 12px;
        height: 34px;
        background-image: url("../assets/images/right-page.png");
        background-size: 100% 100%;
        cursor: pointer;
        z-index: 9;
      }
    }

    .video-grid {
      width: 100%;
      height: 100%;
      display: grid;
      min-height: 0;
      gap: 8px;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      grid-template-rows: repeat(2, minmax(0, 1fr));
      grid-auto-rows: minmax(0, 1fr);

      &.layout-1 {
        grid-template-columns: minmax(0, 1fr);
        grid-template-rows: minmax(0, 1fr);
      }

      &.layout-4 {
        grid-template-columns: repeat(2, minmax(0, 1fr));
        grid-template-rows: repeat(2, minmax(0, 1fr));
      }

      &.layout-9 {
        grid-template-columns: repeat(3, minmax(0, 1fr));
        grid-template-rows: repeat(3, minmax(0, 1fr));
      }
    }

    .video-item {
      position: relative;
      min-width: 0;
      min-height: 0;
      overflow: hidden;
      border-radius: 10px;
      border: 2px solid transparent;

      &.active {
        border-color: #21e7fb;
      }
    }

    .video-image {
      position: absolute;
      inset: 0;
      background: #0b1e3d;
    }

    .video-warning {
      position: absolute;
      z-index: 3;
      right: 4px;
      top: 4px;
      svg {
        width: 20px;
        height: 20px;
        fill: #f00;
      }
    }

    .overlay-tags,
    .video-footer,
    .video-actions {
      position: absolute;
      z-index: 2;
    }

    .overlay-tags {
      top: 10px;
      right: 10px;
      display: flex;
      max-width: calc(100% - 20px);
      justify-content: flex-end;
      pointer-events: none;

      &.has-warning {
        top: 32px;
      }

      .tag {
        padding: 2px 8px;
        font-size: 12px;
        color: #fff;
        border-radius: 4px;
        display: inline-block;
        max-width: 100%;
        white-space: nowrap;
        overflow: hidden;
        text-overflow: ellipsis;

        &.purple {
          background: #722ed1;
        }
      }
    }

    .video-tools {
      top: 4px;
      right: 4px;
      display: flex;
      gap: 3px;
    }

    .tool,
    .side-btn,
    .action-btn {
      width: 16px;
      height: 16px;
      border-radius: 3px;
      background: rgba(19, 48, 87, 0.74);
      border: 1px solid rgba(141, 197, 255, 0.3);
      position: relative;
    }

    .tool::before,
    .side-btn::before,
    .action-btn::before {
      content: "";
      position: absolute;
      inset: 4px;
      border: 1px solid rgba(192, 228, 255, 0.7);
      border-radius: 50%;
    }

    .video-side-controls {
      left: 4px;
      top: 74px;
      display: flex;
      flex-direction: column;
      gap: 4px;
    }

    .video-footer {
      left: 0;
      right: 0;
      bottom: 0;
      display: flex;
      align-items: center;
      gap: 6px;
      padding: 4px 6px;
      font-size: 10px;
      color: #f4fbff;
      background: rgba(5, 22, 46, 0.77);
    }

    .camera-id {
      color: #ffffff;
    }

    .camera-location,
    .camera-meta {
      color: rgba(226, 239, 255, 0.72);
    }

    .video-actions {
      right: 4px;
      bottom: 5px;
      display: flex;
      gap: 4px;
      .icon-handler {
        width: 18px;
        height: 18px;
        cursor: pointer;
        color: #a0c2e9;
        background: #05162ec4;
        border-radius: 3px;
        display: flex;
        align-items: center;
        justify-content: center;
        font-style: normal;
      }

      .icon-setting {
        width: 18px;
        height: 18px;
        cursor: pointer;
      }
    }
  }
</style>
