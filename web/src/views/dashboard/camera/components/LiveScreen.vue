<!-- 直播画面 -->
<template>
  <div class="live-screen">
    <JessibucaPlayer
      v-if="hasLiveUrl && playerVisible"
      :key="playerKey"
      :url="String(liveUrl || '')"
      :stream-app="streamApp"
      :stream-id="streamId"
      class="player"
    />
    <div v-else class="cover">
      <img src="@/assets/dashboard/icon-camera-close.png" alt="摄像头离线" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import JessibucaPlayer from '@/components/JessibucaPlayer.vue'

const props = defineProps<{
  liveUrl?: string
  streamApp?: string
  streamId?: string
}>()

const playerKey = ref(0)
const playerVisible = ref(true)

const hasLiveUrl = computed(() => String(props.liveUrl || '').trim().length > 0)

function destroyPlayer() {
  playerVisible.value = false
  playerKey.value += 1
  window.setTimeout(() => {
    playerVisible.value = true
  }, 0)
}

watch(
  () => props.liveUrl,
  () => {
    playerVisible.value = true
    playerKey.value += 1
  },
)

defineExpose({
  destroyPlayer,
})
</script>

<style scoped lang="less">
.live-screen {
  width: 100%;
  height: 100%;

  .player {
    width: 100%;
    height: 100%;
  }

  .cover {
    width: 100%;
    height: 100%;
    background: #222222;
    display: flex;
    justify-content: center;
    align-items: center;

    img {
      width: 30%;
      height: auto;
    }
  }
}
</style>
