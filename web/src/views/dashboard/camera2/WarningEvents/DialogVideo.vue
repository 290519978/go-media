<template>
  <Dialog title="报警视频片段" width="1640px" v-model="visible">
    <div class="base-content">
      <div v-if="hasVideo" class="video-content">
        <video :key="activeClip?.url || ''" class="video-player" :src="activeClip?.url" controls preload="metadata" />
        <ASegmented
          v-if="clipOptions.length > 1"
          v-model:value="selectedClipPath"
          :options="clipOptions.map((item) => ({ label: item.label, value: item.path }))"
          block
          class="clip-tabs"
        />
      </div>
      <div v-else class="empty-wrap">
        <AEmpty description="暂无视频片段" />
      </div>
    </div>
  </Dialog>
</template>
<script setup lang="ts">
  import { computed, ref } from "vue";
  import { Empty as AEmpty, Segmented as ASegmented } from "ant-design-vue";
  import Dialog from "../components/Dialog/index.vue";
  import type { Camera2ClipOption } from "../api";

  type DialogVideoPayload =
    | string
    | {
        clips?: Camera2ClipOption[];
        currentPath?: string;
      };

  const visible = ref(false);
  const clipOptions = ref<Camera2ClipOption[]>([]);
  const selectedClipPath = ref("");

  const activeClip = computed(() => {
    return clipOptions.value.find((item) => item.path === selectedClipPath.value) || clipOptions.value[0] || null;
  });
  const hasVideo = computed(() => Boolean(activeClip.value?.url));

  const open = (payload?: DialogVideoPayload) => {
    if (typeof payload === "string") {
      const nextURL = String(payload || "").trim();
      clipOptions.value = nextURL
        ? [{ path: nextURL, url: nextURL, label: "报警片段" }]
        : [];
      selectedClipPath.value = clipOptions.value[0]?.path || "";
    } else {
      const nextClips = Array.isArray(payload?.clips)
        ? payload.clips.filter((item) => String(item?.url || "").trim())
        : [];
      const preferredPath = String(payload?.currentPath || "").trim();
      clipOptions.value = nextClips;
      selectedClipPath.value = nextClips.some((item) => item.path === preferredPath)
        ? preferredPath
        : nextClips[0]?.path || "";
    }
    visible.value = true;
  };

  defineExpose({
    open,
  });
</script>

<style scoped lang="less">
  .base-content {
    width: 100%;
    min-height: 895px;
  }

  .video-content {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .video-player {
    width: 1591px;
    height: 895px;
    object-fit: contain;
    background: #000;
  }

  .clip-tabs {
    width: 100%;
  }

  .empty-wrap {
    width: 1591px;
    height: 895px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
</style>
