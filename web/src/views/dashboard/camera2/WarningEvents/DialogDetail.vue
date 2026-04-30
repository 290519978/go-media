<template>
  <Dialog v-model="configVisible" width="1380px" title="报警处理详情">
    <div class="base-content">
      <div class="count">
        <div class="count-item time">
          <p class="text title">报警时间</p>
          <p class="text sub-title" :title="detailView.time">{{ detailView.time }}</p>
        </div>
        <div class="count-item area">
          <p class="text title">摄像头/区域</p>
          <p class="text sub-title" :title="detailView.area">{{ detailView.area }}</p>
        </div>
        <div class="count-item type">
          <p class="text title">事件类型</p>
          <p class="text sub-title" :title="detailView.type">{{ detailView.type }}</p>
        </div>
        <div class="count-item level">
          <p class="text title">报警等级</p>
          <p class="text sub-title" :title="detailView.level">{{ detailView.level }}</p>
        </div>
      </div>

      <div class="title-wrapper">
        <p class="title">{{ mediaSectionTitle }}</p>
      </div>
      <div class="media-grid" :class="{ 'media-grid-patrol': !showClipCard }">
        <div class="media-card snapshot-card">
          <div class="card-header">
            <span class="card-title">报警截图</span>
            <div class="card-tools">
              <button class="tool-btn" @click="openImageDialog">放大查看</button>
            </div>
          </div>
          <div class="snapshot-body">
            <SnapshotWithBoxes
              class="snapshot-preview"
              :image-url="snapshotURL"
              :boxes="boxes"
              :reset-key="detail?.id || ''"
            />
          </div>
        </div>

        <div class="media-card realtime-card">
          <div class="card-header">
            <span class="card-title">实时画面</span>
          </div>
          <div v-if="hasRealtimeStream" class="player-wrap">
            <LiveScreen
              class="live-player"
              :live-url="realtimePlayer?.liveUrl"
              :stream-app="realtimePlayer?.streamApp"
              :stream-id="realtimePlayer?.streamID"
            />
            <div class="player-footer">
              <span class="player-name">{{ realtimePlayer?.deviceName || '-' }}</span>
              <span class="player-area">{{ realtimePlayer?.areaName || '-' }}</span>
            </div>
          </div>
          <div v-else class="empty-wrap">
            <AEmpty description="暂无可用实时流" />
          </div>
        </div>

        <div v-if="showClipCard" class="media-card clip-card">
          <div class="card-header">
            <span class="card-title">报警片段</span>
            <button class="tool-btn" @click="openVideoDialog">单独播放</button>
          </div>
          <div v-if="hasClipVideo" class="clip-body">
            <video :key="clipURL" class="clip-player" :src="clipURL" controls preload="metadata" />
            <ASegmented
              v-if="clipOptions.length > 1"
              v-model:value="selectedClipPath"
              :options="clipOptions.map((item) => ({ label: item.label, value: item.path }))"
              block
              class="clip-tabs"
            />
          </div>
          <div v-else-if="detail?.clip_ready === false" class="clip-status">
            <p class="status-title">{{ clipStatusTitle }}</p>
            <p class="status-subtitle">{{ clipStatusSubtitle }}</p>
          </div>
          <div v-else class="empty-wrap">
            <AEmpty description="暂无视频片段" />
          </div>
        </div>
      </div>

      <div class="title-wrapper">
        <p class="title">AI分析结果</p>
      </div>
      <div class="result">
        <div class="conclusion">
          <img class="icon" src="../assets/images/warning.png" alt="" />
          <span class="title">分析结论：</span>
          <span class="content">{{ conclusionText }}</span>
        </div>      
        <!-- <div v-if="promptText" class="prompt-row">
          <span class="title">巡查提示词：</span>
          <span class="content" :title="promptText">{{ promptText }}</span>
        </div> -->
        <div class="ratio">
          <div class="label">
            <span>置信度：</span>
            <span>{{ confidenceText }}</span>
          </div>
          <div class="progress">
            <div class="completed" :style="{ width: `${confidenceValue}%` }"></div>
            <div class="line"></div>
            <div class="uncompleted" :style="{ width: `${100 - confidenceValue}%` }"></div>
          </div>
        </div>
        <div class="category">
          <div class="category-item">
            <img src="../assets/images/icon1.png" alt="" class="icon" />
            <span class="title">检测目标</span>
            <span class="sub-title truncate-text" :title="detectedLabelText">{{ detectedLabelText }}</span>
          </div>
          <div class="category-item">
            <img src="../assets/images/icon3.png" alt="" class="icon" />
            <span class="title">目标数量</span>
            <span class="sub-title">{{ detectedCountText }}</span>
          </div>
        </div>
      </div>

      <div class="title-wrapper">
        <p class="title">处理操作</p>
      </div>
      <div class="form">
        <div class="form-item">
          <label class="label">审核结果：</label>
          <ARadioGroup v-model:value="selectedStatus" class="review-group">
            <ARadio value="valid">有效告警</ARadio>
            <ARadio value="invalid">无效告警</ARadio>
            <ARadio value="pending">待处理</ARadio>
          </ARadioGroup>
        </div>
        <div class="form-item textarea-item">
          <label class="label textarea-label">审核备注：</label>
          <ATextarea
            v-model:value="remark"
            placeholder="请输入审核备注"
            :auto-size="{ minRows: 4, maxRows: 4 }"
          />
        </div>
      </div>

      <div class="btn-group">
        <button class="btn cancel" :disabled="submitting" @click="close">取消</button>
        <button class="btn" :disabled="submitting" @click="submitReview">
          {{ submitting ? '提交中...' : '提交处理结果' }}
        </button>
      </div>
    </div>
  </Dialog>
  <DialogImage ref="imageDialog" />
  <DialogVideo ref="videoDialog" />
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { Empty as AEmpty, Radio as ARadio, RadioGroup as ARadioGroup, Segmented as ASegmented, Textarea as ATextarea, message } from 'ant-design-vue'
import LiveScreen from '@/views/dashboard/camera/components/LiveScreen.vue'
import Dialog from '../components/Dialog/index.vue'
import SnapshotWithBoxes from './SnapshotWithBoxes.vue'
import DialogImage from './DialogImage.vue'
import DialogVideo from './DialogVideo.vue'
import {
  buildCamera2ClipOptions,
  buildCamera2ClipURL,
  buildCamera2SnapshotURL,
  fetchCamera2Channels,
  fetchCamera2EventDetail,
  formatCamera2DateTime,
  parseCamera2Boxes,
  parseCamera2Conclusion,
  parseCamera2PatrolConclusion,
  resolveCamera2RealtimePlayer,
  reviewCamera2Event,
  type Camera2ChannelEntity,
  type Camera2ClipOption,
  type Camera2DetectedBox,
  type Camera2EventEntity,
} from '../api'

const emits = defineEmits(['handler'])

type DialogImageInstance = {
  open: (payload?: string | { url?: string; boxes?: Camera2DetectedBox[]; resetKey?: string | number }) => void;
}

type DialogVideoInstance = {
  open: (payload?: string | { clips?: Camera2ClipOption[]; currentPath?: string }) => void;
}

const detail = ref<Camera2EventEntity | null>(null)
const channels = ref<Camera2ChannelEntity[]>([])
const configVisible = ref(false)
const submitting = ref(false)
const selectedStatus = ref<'valid' | 'invalid' | 'pending'>('pending')
const selectedClipPath = ref('')
const remark = ref('')
const clipPendingTimeoutMs = 2 * 60 * 1000

const imageDialog = ref<DialogImageInstance | null>(null)
const videoDialog = ref<DialogVideoInstance | null>(null)

// 第二大屏详情不额外扩后端接口，实时画面直接复用概览接口里的直播地址。
const open = async (eventItem?: { id?: string }) => {
  if (!eventItem?.id) {
    return
  }
  try {
    const [nextDetail, nextChannels] = await Promise.all([
      fetchCamera2EventDetail(eventItem.id),
      fetchCamera2Channels().catch(() => []),
    ])
    const nextClipOptions = buildCamera2ClipOptions(nextDetail.id, nextDetail.clip_path, nextDetail.clip_files_json)
    detail.value = nextDetail
    channels.value = nextChannels
    selectedStatus.value = normalizeReviewStatus(nextDetail.status)
    remark.value = String(nextDetail.review_note || '')
    selectedClipPath.value = String(nextClipOptions[0]?.path || '')
    configVisible.value = true
  } catch (error) {
    message.error((error as Error).message || '加载报警详情失败')
  }
}

const close = () => {
  configVisible.value = false
}

defineExpose({
  open,
  close,
})

const boxes = computed(() => parseCamera2Boxes(detail.value?.boxes_json || detail.value?.yolo_json))

const confidenceValue = computed(() => {
  if (boxes.value.length === 0) {
    return 0
  }
  return Math.max(...boxes.value.map((item) => Math.round(Number(item.confidence || 0) * 100)))
})

const confidenceText = computed(() => confidenceValue.value > 0 ? `${confidenceValue.value}%` : '--')
const snapshotURL = computed(() => buildCamera2SnapshotURL(detail.value?.snapshot_path))
const promptText = computed(() => String(detail.value?.prompt_text || '').trim())
const isPatrolEvent = computed(() => String(detail.value?.event_source || '').trim() === 'patrol')
const showClipCard = computed(() => !isPatrolEvent.value)
const mediaSectionTitle = computed(() => showClipCard.value ? '报警截图、实时画面与视频片段' : '报警截图与实时画面')
const conclusionText = computed(() => (
  isPatrolEvent.value
    ? parseCamera2PatrolConclusion(detail.value?.llm_json, detail.value?.algorithm_code)
    : parseCamera2Conclusion(detail.value?.llm_json, detail.value?.algorithm_code)
))
const detailTypeText = computed(() => String(
  detail.value?.display_name || detail.value?.algorithm_name || detail.value?.algorithm_code || detail.value?.algorithm_id || '-',
))

const detectedLabelText = computed(() => {
  const labels = Array.from(new Set(
    boxes.value.map((item) => String(item.label || '').trim()).filter(Boolean),
  ))
  if (labels.length > 0) {
    return labels.join('、')
  }
  return detailTypeText.value || '暂无'
})

const detectedCountText = computed(() => `${boxes.value.length}个目标`)

const detailView = computed(() => ({
  time: formatCamera2DateTime(detail.value?.occurred_at),
  area: `${String(detail.value?.device_name || detail.value?.device_id || '-')} / ${String(detail.value?.area_name || detail.value?.area_id || '未分配区域')}`,
  type: detailTypeText.value,
  level: String(detail.value?.alarm_level_name || detail.value?.alarm_level_id || '-'),
}))

const clipOptions = computed(() => buildCamera2ClipOptions(
  detail.value?.id || '',
  detail.value?.clip_path,
  detail.value?.clip_files_json,
))
const clipURL = computed(() => buildCamera2ClipURL(detail.value?.id || '', selectedClipPath.value, detail.value?.clip_files_json))
const hasClipVideo = computed(() => Boolean(clipURL.value))

const clipPendingExpired = computed(() => {
  const currentDetail = detail.value
  if (!currentDetail || currentDetail.clip_ready !== false) {
    return false
  }
  const occurredAt = Date.parse(String(currentDetail.occurred_at || ''))
  if (Number.isNaN(occurredAt)) {
    return false
  }
  return Date.now() - occurredAt >= clipPendingTimeoutMs
})
const clipStatusTitle = computed(() => {
  if (isPatrolEvent.value) {
    return '任务巡查不生成片段'
  }
  return clipPendingExpired.value ? '片段未生成' : '片段生成中'
})
const clipStatusSubtitle = computed(() => {
  if (isPatrolEvent.value) {
    return '本次巡查仅抓取当前一帧并进行单次 LLM 分析。'
  }
  return clipPendingExpired.value ? '请检查录制配置或后端日志。' : '请稍后刷新报警详情。'
})

const realtimePlayer = computed(() => resolveCamera2RealtimePlayer(channels.value, detail.value?.device_id))
const hasRealtimeStream = computed(() => Boolean(realtimePlayer.value?.liveUrl))

function normalizeReviewStatus(status?: string): 'valid' | 'invalid' | 'pending' {
  const normalized = String(status || '').trim().toLowerCase()
  if (normalized === 'invalid' || normalized === 'pending') {
    return normalized
  }
  return 'valid'
}

function openImageDialog() {
  imageDialog.value?.open({
    url: snapshotURL.value,
    boxes: boxes.value,
    resetKey: detail.value?.id || snapshotURL.value,
  })
}

function openVideoDialog() {
  if (!clipURL.value) {
    message.info('暂无视频片段')
    return
  }
  videoDialog.value?.open({
    clips: clipOptions.value,
    currentPath: selectedClipPath.value,
  })
}

async function submitReview() {
  if (!detail.value?.id || submitting.value) {
    return
  }
  submitting.value = true
  try {
    await reviewCamera2Event(detail.value.id, {
      status: selectedStatus.value,
      review_note: remark.value.trim(),
    })
    message.success('处理结果已提交')
    emits('handler')
    close()
  } catch (error) {
    message.error((error as Error).message || '提交处理结果失败')
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped lang="less">
.base-content {
  display: flex;
  width: 100%;
  flex-direction: column;
  gap: 16px;
}

.title-wrapper {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;

  .title {
    width: 360px;
    height: 29px;
    padding-left: 32px;
    display: flex;
    align-items: center;
    background-image: url("../assets/images/config-bar.png");
    background-size: cover;
    font-size: 20px;
    font-weight: 600;
    color: #f1f8ff;
  }
}

.count {
  width: 100%;
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 14px;

  .count-item {
    width: 100%;
    height: 99px;
    min-width: 0;
    padding-left: 135px;
    background-repeat: no-repeat;
    background-position: center;
    background-size: 100% 100%;

    &.time {
      background-image: url("../assets/images/time.png");
    }

    &.area {
      background-image: url("../assets/images/area.png");
    }

    &.type {
      background-image: url("../assets/images/type.png");
    }

    &.level {
      background-image: url("../assets/images/level.png");
    }

    .title {
      margin-top: 8px;
      font-size: 14px;
      color: #d6eeff;
    }

    .sub-title {
      margin-top: 8px;
      font-size: 14px;
      color: #97e0ff;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
  }
}

.media-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(0, 1fr) minmax(0, 1fr);
  gap: 16px;
}

.media-grid.media-grid-patrol {
  grid-template-columns: minmax(0, 1.35fr) minmax(0, 1fr);
}

.media-card {
  min-width: 0;
  min-height: 0;
  padding: 12px;
  border: 1px solid rgba(110, 194, 255, 0.28);
  border-radius: 10px;
  background: linear-gradient(180deg, rgba(18, 59, 113, 0.48) 0%, rgba(5, 20, 43, 0.72) 100%);
  box-shadow: inset 0 0 20px rgba(49, 137, 232, 0.08);
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 10px;
}

.card-title {
  color: #f1f8ff;
  font-size: 16px;
  font-weight: 600;
}

.card-tools {
  display: flex;
  align-items: center;
  gap: 10px;
}

.tool-btn {
  min-width: 88px;
  height: 30px;
  padding: 0 12px;
  border: 1px solid rgba(114, 204, 255, 0.45);
  border-radius: 4px;
  background: rgba(13, 61, 112, 0.62);
  color: #e8f7ff;
  cursor: pointer;
}

.snapshot-body,
.player-wrap,
.clip-body,
.empty-wrap,
.clip-status {
  height: 320px;
}

.snapshot-preview {
  width: 100%;
  height: 100%;
}

.player-wrap {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.live-player {
  width: 100%;
  height: 0;
  flex: 1;
  min-height: 0;
  border-radius: 10px;
  overflow: hidden;
}

.player-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 6px 10px;
  border-radius: 6px;
  background: rgba(5, 22, 46, 0.77);
  color: #f4fbff;
  font-size: 13px;
}

.player-name,
.player-area {
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.clip-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.clip-player {
  width: 100%;
  height: 0;
  flex: 1;
  min-height: 0;
  border-radius: 10px;
  background: #000;
}

.clip-tabs {
  flex: 0 0 auto;
}

.clip-status,
.empty-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-direction: column;
}

.status-title {
  color: #f1f8ff;
  font-size: 18px;
  font-weight: 600;
}

.status-subtitle {
  margin-top: 8px;
  color: rgba(226, 239, 255, 0.72);
  font-size: 14px;
}

.result {
  width: 100%;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  background-color: #0501234d;

  .conclusion {
    min-height: 32px;
    padding: 8px 10px;
    display: flex;
    align-items: flex-start;
    gap: 8px;
    background-color: #cc060675;
    box-shadow: inset 0 0 7.29px 0 #ff1c0ebd;

    .icon {
      width: 16px;
      height: 16px;
      margin-top: 2px;
      flex: 0 0 auto;
    }

    .title {
      color: #f2cbcb;
      font-size: 14px;
      font-weight: 600;
      white-space: nowrap;
    }

    .content {
      color: #ff5b5b;
      font-size: 14px;
      line-height: 1.6;
    }
  }

  .prompt-row {
    min-height: 32px;
    padding: 10px 12px;
    display: flex;
    align-items: flex-start;
    gap: 8px;
    background: rgba(22, 80, 126, 0.32);
    border: 1px solid rgba(90, 182, 255, 0.22);

    .title {
      color: #96e0ff;
      font-size: 14px;
      white-space: nowrap;
    }

    .content {
      color: #e8f7ff;
      font-size: 14px;
      line-height: 1.6;
      word-break: break-all;
    }
  }

  .ratio {
    width: 100%;
    height: 58px;
    padding: 8px;
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 8px;
    background: linear-gradient(
      180deg,
      rgba(205, 20, 20, 0.2) 0%,
      rgba(205, 55, 22, 0.16) 40.12%,
      rgba(205, 31, 20, 0.0001) 100%
    );

    .label {
      display: flex;
      align-items: center;
      justify-content: space-between;
      color: #f2cbcb;
      font-size: 14px;
    }

    .progress {
      width: 100%;
      display: flex;
      align-items: center;

      .completed {
        height: 16px;
        background: linear-gradient(90deg, #650000 0%, #ff4e4e 100%);
      }

      .line {
        width: 4px;
        height: 20px;
        background-color: #fff;
      }

      .uncompleted {
        height: 16px;
        background-color: #ffffff25;
      }
    }
  }

  .category {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px;
  }

  .category-item {
    width: 100%;
    min-width: 0;
    height: 85px;
    padding: 16px;
    display: grid;
    column-gap: 16px;
    grid-template-columns: 48px minmax(0, 1fr);
    grid-template-rows: 1fr 1fr;
    align-items: center;
    background-image: url("../assets/images/category.png");
    background-size: 100% 100%;

    .icon {
      width: 100%;
      height: 100%;
      grid-row: span 2;
    }

    .title {
      font-size: 14px;
      color: #96e0ff;
    }

    .sub-title {
      font-size: 14px;
      color: #ffffff;
      font-weight: 600;
    }
  }
}

.truncate-text {
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.form {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.form-item {
  display: grid;
  grid-template-columns: 78px minmax(0, 1fr);
  gap: 10px;
  align-items: start;
}

.label {
  padding-top: 6px;
  color: #a0c2e9;
  font-size: 14px;
}

.review-group {
  display: flex;
  align-items: center;
  gap: 18px;
  flex-wrap: wrap;
}

.textarea-item {
  :deep(.ant-input) {
    resize: none;
  }
}

.btn-group {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 28px;

  .btn {
    width: 130px;
    height: 38px;
    border: none;
    cursor: pointer;
    color: #fff;
    font-size: 16px;
    background-image: url("../assets/images/btn1.png");
    background-size: 100% 100%;

    &:disabled {
      opacity: 0.6;
      cursor: not-allowed;
    }

    &.cancel {
      background-image: url("../assets/images/btn2.png");
    }
  }
}

:deep(.ant-radio-wrapper) {
  color: #fff;
}

:deep(.ant-radio-inner) {
  background-color: transparent;
  border-color: #9bc2ea;
}

:deep(.ant-radio-wrapper-checked) {
  color: #36c7ff;
}

:deep(.ant-radio-checked .ant-radio-inner) {
  border-color: #36c7ff;
  background-color: transparent;
}

:deep(.ant-radio-inner::after) {
  background-color: #36c7ff;
}

:deep(.ant-input),
:deep(.ant-input-textarea textarea) {
  color: #e8f7ff;
  background: rgba(7, 32, 66, 0.88);
  border-color: rgba(114, 204, 255, 0.28);
}

:deep(.ant-input::placeholder),
:deep(.ant-input-textarea textarea::placeholder) {
  color: rgba(216, 232, 251, 0.45);
}

:deep(.ant-segmented) {
  background: rgba(9, 36, 73, 0.82);
}

:deep(.ant-segmented-item-label) {
  color: #dce9ff;
}

:deep(.ant-segmented-item-selected .ant-segmented-item-label) {
  color: #001a34;
}

:deep(.ant-empty-description) {
  color: rgba(226, 239, 255, 0.72);
}
</style>
