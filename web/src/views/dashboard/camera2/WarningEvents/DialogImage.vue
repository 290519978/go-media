<template>
  <Dialog title="报警截图" width="1640px" v-model="visible">
    <div class="base-content">
      <SnapshotWithBoxes
        class="snapshot-boxes"
        :image-url="imageURL"
        :boxes="boxes"
        :reset-key="resetKey"
        empty-text="暂无报警截图"
      />
    </div>
  </Dialog>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import Dialog from '../components/Dialog/index.vue'
import SnapshotWithBoxes from './SnapshotWithBoxes.vue'
import type { Camera2DetectedBox } from '../api'
import testImage from '../assets/images/image.png'

type DialogImagePayload = string | {
  url?: string
  boxes?: Camera2DetectedBox[]
  resetKey?: string | number
}

const visible = ref(false)
const imageURL = ref(testImage)
const boxes = ref<Camera2DetectedBox[]>([])
const resetKey = ref<string | number>('')

const open = (payload?: DialogImagePayload) => {
  if (typeof payload === 'string') {
    imageURL.value = payload || testImage
    boxes.value = []
    resetKey.value = payload || Date.now()
  } else {
    imageURL.value = String(payload?.url || testImage)
    boxes.value = Array.isArray(payload?.boxes) ? payload.boxes : []
    resetKey.value = payload?.resetKey ?? payload?.url ?? Date.now()
  }
  visible.value = true
}

defineExpose({
  open,
})
</script>

<style scoped lang="less">
.base-content {
  width: 1591px;
  height: 895px;
}

.snapshot-boxes {
  width: 100%;
  height: 100%;
}
</style>
