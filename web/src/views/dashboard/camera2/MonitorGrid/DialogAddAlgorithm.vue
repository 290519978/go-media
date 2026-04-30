<template>
  <Dialog v-model="configVisible" width="760px" :title="dialogTitle">
    <div class="base-content">
      <div class="title-wrapper">
        <p class="title">算法基本信息</p>
      </div>

      <div class="form">
        <div class="form-item">
          <label class="label required">算法名称：</label>
          <AInput v-model:value="form.name" placeholder="请输入算法名称" />
        </div>

        <div class="form-item">
          <label class="label required">提示词：</label>
          <ATextarea
            v-model:value="form.prompt"
            placeholder="请输入用于图片/视频测试和正式算法的提示词"
            :auto-size="{ minRows: 6, maxRows: 8 }"
          />
        </div>
      </div>

      <div class="title-wrapper">
        <p class="title">算法测试</p>
      </div>

      <div class="test-upload">
        <AUploadDragger
          :file-list="uploadList"
          :multiple="true"
          accept="image/*,video/*"
          :custom-request="handleCustomRequest"
          :before-upload="beforeUpload"
          @remove="removeFile"
        >
          <p class="ant-upload-drag-icon">
            <img src="../assets/images/upload.png" alt="" />
          </p>
          <p class="ant-upload-text">将文件拖拽此处或者点击上传</p>
          <p class="ant-upload-hint">
            支持图片和视频。图片最多 {{ testLimits.imageMaxCount }} 张，视频最多 {{ testLimits.videoMaxCount }} 个，视频大小不超过 {{ formatFileSize(testLimits.videoMaxBytes) }}。
          </p>
        </AUploadDragger>
      </div>

      <div class="btn-group">
        <button class="btn" :disabled="runningTest" @click="runDraftTest">
          {{ runningTest ? "测试中..." : "开始测试" }}
        </button>
      </div>

      <div class="title-wrapper">
        <p class="title">测试结果</p>
      </div>

      <div class="test-result">
        <div class="empty" v-if="testResults.length === 0">
          <img src="../assets/images/empty.png" alt="" />
          <span>暂无数据</span>
        </div>

        <div v-else class="result-wrapper">
          <div
            v-for="item in testResults"
            :key="item.job_item_id || item.client_key || item.file_name"
            class="item"
            :class="{ error: item.status === 'failed' }"
          >
            <img
              class="icon"
              :src="resolveStatusIcon(item)"
              alt=""
            />

            <div class="info">
              <p class="result-title">
                {{ item.file_name }}
                <span class="status-text">[{{ formatStatus(item.status) }}]</span>
              </p>
              <p class="desc">测试结果：{{ item.conclusion || "-" }}</p>
              <p class="desc">异常描述：{{ item.basis || "-" }}</p>
              <p v-if="item.media_type === 'video'" class="desc">
                异常时间段：{{ formatAnomalyTimes(item.anomaly_times) }}
              </p>
              <p v-if="item.error_message" class="desc error-text">
                失败原因：{{ item.error_message }}
              </p>

              <div
                v-if="shouldShowImagePreview(item)"
                class="preview-wrap"
                :style="{ aspectRatio: previewAspectRatio }"
              >
                <img class="preview" :src="resolvePreviewImageURL(item)" :alt="item.file_name" />
                <div
                  v-for="(box, idx) in item.normalized_boxes || []"
                  :key="`${item.file_name}-${box.label}-${idx}`"
                  class="preview-box"
                  :style="normalizedBoxStyle(box)"
                >
                  <span class="preview-box-label">
                    {{ box.label }} {{ (Number(box.confidence || 0) * 100).toFixed(1) }}%
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="btn-group footer-btns">
        <button class="btn cancel" @click="close">取消</button>
        <button class="btn" :disabled="creatingAlgorithm" @click="createAlgorithm">
          {{ creatingAlgorithm ? "添加中..." : "添加算法" }}
        </button>
      </div>
    </div>
  </Dialog>
</template>

<script setup lang="ts">
  import { computed, onUnmounted, reactive, ref, watch } from "vue";
  import { Input as AInput, Textarea as ATextarea, UploadDragger as AUploadDragger, Upload, message } from "ant-design-vue";
  import Dialog from "../components/Dialog/index.vue";
  import errorIcon from "../assets/images/error.png";
  import successIcon from "../assets/images/success.png";
  import pendingIcon from "../assets/images/warning2.png";
  import { algorithmAPI, type AlgorithmUpsertPayload } from "@/api/modules";
  import { appendTokenQuery } from "@/api/request";

  type TestAnomalyTime = {
    timestamp_ms: number;
    timestamp_text: string;
    reason: string;
  };

  type NormalizedBox = {
    label: string;
    confidence: number;
    x: number;
    y: number;
    w: number;
    h: number;
  };

  type TestResultItem = {
    client_key?: string;
    job_item_id?: string;
    sort_order?: number;
    status?: "pending" | "running" | "success" | "failed";
    record_id?: string;
    file_name: string;
    media_type: "image" | "video";
    success: boolean;
    conclusion: string;
    basis: string;
    media_url: string;
    normalized_boxes?: NormalizedBox[];
    anomaly_times?: TestAnomalyTime[];
    duration_seconds?: number;
    error_message?: string;
    preview_url?: string;
  };

  type TestJobSnapshot = {
    job_id: string;
    batch_id: string;
    algorithm_id: string;
    status: "pending" | "running" | "completed" | "partial_failed" | "failed";
    total_count: number;
    success_count: number;
    failed_count: number;
    items: TestResultItem[];
  };

  const emits = defineEmits<{
    created: [payload: { id: string; name: string }];
  }>();

  const configVisible = ref(false);
  const currentDeviceID = ref("");
  const currentDeviceName = ref("");
  const runningTest = ref(false);
  const creatingAlgorithm = ref(false);
  const uploadList = ref<any[]>([]);
  const testingFiles = ref<File[]>([]);
  const testResults = ref<TestResultItem[]>([]);
  const testResultPreviewURLs = ref<string[]>([]);
  const currentDraftJobID = ref("");
  const previewAspectRatio = "16 / 9";
  let pollTimer: number | null = null;

  const testLimits = reactive({
    imageMaxCount: 5,
    videoMaxCount: 1,
    videoMaxBytes: 100 * 1024 * 1024,
  });

  const form = reactive({
    name: "",
    prompt: "",
  });

  const dialogTitle = computed(() => {
    const deviceName = String(currentDeviceName.value || "").trim();
    return deviceName ? `为${deviceName}添加算法` : "新增算法";
  });

  function resetState() {
    stopPolling();
    revokeTestResultPreviewURLs();
    form.name = "";
    form.prompt = "";
    currentDeviceID.value = "";
    currentDeviceName.value = "";
    runningTest.value = false;
    creatingAlgorithm.value = false;
    uploadList.value = [];
    testingFiles.value = [];
    testResults.value = [];
    currentDraftJobID.value = "";
  }

  async function loadTestLimits() {
    try {
      const data = await algorithmAPI.testLimits() as {
        image_max_count?: number;
        video_max_count?: number;
        video_max_bytes?: number;
      };
      testLimits.imageMaxCount = Number(data.image_max_count || 5);
      testLimits.videoMaxCount = Number(data.video_max_count || 1);
      testLimits.videoMaxBytes = Number(data.video_max_bytes || 100 * 1024 * 1024);
    } catch (error) {
      message.warning((error as Error).message || "读取测试限制失败，已使用默认值");
    }
  }

  async function open(payload?: { deviceID?: string; deviceName?: string }) {
    resetState();
    currentDeviceID.value = String(payload?.deviceID || "").trim();
    currentDeviceName.value = String(payload?.deviceName || "").trim();
    configVisible.value = true;
    await loadTestLimits();
  }

  function close() {
    configVisible.value = false;
  }

  defineExpose({
    open,
    close,
  });

  function countMedia(files: File[]) {
    let imageCount = 0;
    let videoCount = 0;
    for (const file of files) {
      if (String(file.type || "").startsWith("video/")) {
        videoCount++;
      } else {
        imageCount++;
      }
    }
    return {
      imageCount,
      videoCount,
    };
  }

  function beforeUpload(file: File) {
    const type = String(file.type || "").toLowerCase();
    if (!type.startsWith("image/") && !type.startsWith("video/")) {
      message.error("仅支持图片和视频文件");
      return Upload.LIST_IGNORE;
    }

    if (type.startsWith("video/") && file.size > testLimits.videoMaxBytes) {
      message.error(`视频大小不能超过 ${formatFileSize(testLimits.videoMaxBytes)}`);
      return Upload.LIST_IGNORE;
    }

    const nextFiles = [...testingFiles.value, file];
    const counts = countMedia(nextFiles);
    if (counts.imageCount > testLimits.imageMaxCount) {
      message.error(`测试图片最多上传 ${testLimits.imageMaxCount} 张`);
      return Upload.LIST_IGNORE;
    }
    if (counts.videoCount > testLimits.videoMaxCount) {
      message.error(`测试视频最多上传 ${testLimits.videoMaxCount} 个`);
      return Upload.LIST_IGNORE;
    }

    testingFiles.value = nextFiles;
    uploadList.value = [
      ...uploadList.value,
      {
        uid: (file as unknown as { uid?: string }).uid || `${Date.now()}-${Math.random()}`,
        name: file.name,
        status: "done",
        originFileObj: file,
      },
    ];
    return false;
  }

  function handleCustomRequest(options: any) {
    if (typeof options?.onSuccess === "function") {
      options.onSuccess({}, options.file);
    }
  }

  function removeFile(file: any) {
    const uid = String(file?.uid || "");
    const raw = file?.originFileObj;
    uploadList.value = uploadList.value.filter((item) => String(item.uid || "") !== uid);
    testingFiles.value = testingFiles.value.filter((item) => item !== raw);
    if (testResults.value.length > 0 || currentDraftJobID.value) {
      stopPolling();
      revokeTestResultPreviewURLs();
      testResults.value = [];
      currentDraftJobID.value = "";
    }
    return true;
  }

  function buildPendingResults(files: File[]): TestResultItem[] {
    revokeTestResultPreviewURLs();
    return files.map((file, index) => ({
      client_key: `${file.name}-${index}-${file.size}-${file.lastModified}`,
      sort_order: index,
      status: "pending",
      record_id: "",
      file_name: file.name,
      media_type: String(file.type || "").startsWith("video/") ? "video" : "image",
      success: false,
      conclusion: "等待分析",
      basis: "任务已创建，正在后台分析",
      media_url: "",
      preview_url: createTestResultPreviewURL(file),
      normalized_boxes: [],
      anomaly_times: [],
      error_message: "",
    }));
  }

  async function runDraftTest() {
    const name = String(form.name || "").trim();
    const prompt = String(form.prompt || "").trim();
    if (!name) {
      message.error("请输入算法名称");
      return;
    }
    if (!prompt) {
      message.error("请输入提示词");
      return;
    }
    if (testingFiles.value.length === 0) {
      message.error("请先上传图片或视频");
      return;
    }

    runningTest.value = true;
    testResults.value = buildPendingResults(testingFiles.value);
    try {
      const formData = new FormData();
      formData.append("name", name);
      formData.append("prompt", prompt);
      formData.append("detect_mode", "2");
      if (currentDeviceID.value) {
        formData.append("camera_id", currentDeviceID.value);
      }
      for (const file of testingFiles.value) {
        formData.append("files", file);
      }
      const data = await algorithmAPI.draftTest(formData) as { job_id?: string };
      const jobID = String(data.job_id || "").trim();
      if (!jobID) {
        throw new Error("创建草稿测试任务失败");
      }
      currentDraftJobID.value = jobID;
      message.success("草稿测试任务已创建");
      void startPolling(jobID);
    } catch (error) {
      revokeTestResultPreviewURLs();
      testResults.value = [];
      currentDraftJobID.value = "";
      message.error((error as Error).message || "草稿测试失败");
    } finally {
      runningTest.value = false;
    }
  }

  function stopPolling() {
    if (pollTimer !== null) {
      window.clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  async function startPolling(jobID: string) {
    stopPolling();
    try {
      const snapshot = await algorithmAPI.getDraftTestJob(jobID) as TestJobSnapshot;
      testResults.value = mergeCurrentBatchTestResults(Array.isArray(snapshot.items) ? snapshot.items : []);
      if (snapshot.status === "pending" || snapshot.status === "running") {
        pollTimer = window.setTimeout(() => {
          void startPolling(jobID);
        }, 1500);
      }
    } catch (error) {
      message.error((error as Error).message || "获取草稿测试结果失败");
    }
  }

  async function createAlgorithm() {
    const name = String(form.name || "").trim();
    const prompt = String(form.prompt || "").trim();
    if (!name) {
      message.error("请输入算法名称");
      return;
    }
    if (!prompt) {
      message.error("请输入提示词");
      return;
    }

    creatingAlgorithm.value = true;
    try {
      const payload: AlgorithmUpsertPayload = {
        name,
        mode: "large",
        detect_mode: 2,
        enabled: true,
        small_model_label: [],
        yolo_threshold: 0.5,
        iou_threshold: 0.8,
        labels_trigger_mode: "any",
        prompt,
        prompt_version: "v1",
        activate_prompt: true,
      };
      const data = await algorithmAPI.create(payload) as {
        algorithm?: { id?: string; name?: string };
      };
      const created = data?.algorithm;
      const algorithmID = String(created?.id || "").trim();
      if (!algorithmID) {
        throw new Error("新增算法成功，但未返回算法 ID");
      }
      message.success("算法添加成功");
      emits("created", {
        id: algorithmID,
        name: String(created?.name || name),
      });
      close();
    } catch (error) {
      message.error((error as Error).message || "添加算法失败");
    } finally {
      creatingAlgorithm.value = false;
    }
  }

  function resolveStatusIcon(item: TestResultItem) {
    if (item.status === "failed") {
      return errorIcon;
    }
    if (item.status === "pending" || item.status === "running") {
      return pendingIcon;
    }
    return successIcon;
  }

  function formatStatus(status?: string) {
    if (status === "pending") return "待分析";
    if (status === "running") return "分析中";
    if (status === "failed") return "失败";
    return "完成";
  }

  function resolveMediaURL(url: string) {
    const target = String(url || "").trim();
    if (!target) return "";
    if (target.startsWith("/api/")) {
      return appendTokenQuery(target);
    }
    return target;
  }

  function revokeTestResultPreviewURLs() {
    for (const url of testResultPreviewURLs.value) {
      if (String(url || "").startsWith("blob:")) {
        URL.revokeObjectURL(url);
      }
    }
    testResultPreviewURLs.value = [];
  }

  function createTestResultPreviewURL(file?: File) {
    if (!(file instanceof File) || !String(file.type || "").startsWith("image/")) {
      return "";
    }
    const previewURL = URL.createObjectURL(file);
    testResultPreviewURLs.value.push(previewURL);
    return previewURL;
  }

  function buildTestResultMatchKeys(item: TestResultItem, index: number) {
    const keys: string[] = [];
    const jobItemID = String(item.job_item_id || "").trim();
    if (jobItemID) {
      keys.push(`job:${jobItemID}`);
    }
    const clientKey = String(item.client_key || "").trim();
    if (clientKey) {
      keys.push(`client:${clientKey}`);
    }
    const fileName = String(item.file_name || "").trim();
    const parsedOrder = Number(item.sort_order);
    const sortOrder = Number.isInteger(parsedOrder) ? parsedOrder : index;
    if (fileName) {
      keys.push(`file:${fileName}-${sortOrder}`);
      keys.push(`file-index:${fileName}-${index}`);
    }
    return Array.from(new Set(keys));
  }

  function findUploadFileForResult(item: TestResultItem, index: number) {
    const parsedOrder = Number(item.sort_order);
    if (Number.isInteger(parsedOrder) && parsedOrder >= 0 && parsedOrder < testingFiles.value.length) {
      return testingFiles.value[parsedOrder];
    }
    return testingFiles.value[index];
  }

  function mergeCurrentBatchTestResults(items: TestResultItem[]) {
    const existingByKey = new Map<string, TestResultItem>();
    for (let index = 0; index < testResults.value.length; index++) {
      const item = testResults.value[index];
      for (const key of buildTestResultMatchKeys(item, index)) {
        existingByKey.set(key, item);
      }
    }

    // 当前批次图片优先复用本地 blob 预览，避免依赖服务端媒体接口才能显示异常图片。
    return items.map((item, index) => {
      const current: TestResultItem = { ...item };
      const matchKeys = buildTestResultMatchKeys(current, index);
      const existing = matchKeys
        .map((key) => existingByKey.get(key))
        .find((candidate) => candidate);
      if (existing?.preview_url) {
        current.preview_url = existing.preview_url;
      } else {
        current.preview_url = createTestResultPreviewURL(findUploadFileForResult(current, index));
      }
      return current;
    });
  }

  function resolvePreviewImageURL(item: TestResultItem) {
    return resolveMediaURL(item.preview_url || "") || resolveMediaURL(item.media_url);
  }

  function shouldShowImagePreview(item: TestResultItem) {
    return item.media_type === "image" && Array.isArray(item.normalized_boxes) && item.normalized_boxes.length > 0;
  }

  function formatAnomalyTimes(items?: TestAnomalyTime[]) {
    if (!Array.isArray(items) || items.length === 0) {
      return "未检测到异常";
    }
    return items
      .map((item) => `${item.timestamp_text || "-"}${item.reason ? ` ${item.reason}` : ""}`)
      .join("；");
  }

  function normalizedBoxStyle(box: NormalizedBox) {
    // 草稿测试与正式算法测试共用 normalized_boxes 口径，x/y 表示中心点坐标。
    const x = clamp01(Number(box.x || 0));
    const y = clamp01(Number(box.y || 0));
    const w = clamp01(Number(box.w || 0));
    const h = clamp01(Number(box.h || 0));
    const left = `${Math.max(0, x - w / 2) * 100}%`;
    const top = `${Math.max(0, y - h / 2) * 100}%`;
    const width = `${w * 100}%`;
    const height = `${h * 100}%`;
    return {
      left,
      top,
      width,
      height,
    };
  }

  function clamp01(value: number) {
    if (!Number.isFinite(value)) {
      return 0;
    }
    if (value < 0) {
      return 0;
    }
    if (value > 1) {
      return 1;
    }
    return value;
  }

  function formatFileSize(size: number) {
    const value = Number(size || 0);
    if (value <= 0) return "0B";
    if (value >= 1024 * 1024 * 1024) {
      return `${(value / (1024 * 1024 * 1024)).toFixed(1)}GB`;
    }
    if (value >= 1024 * 1024) {
      return `${(value / (1024 * 1024)).toFixed(0)}MB`;
    }
    if (value >= 1024) {
      return `${(value / 1024).toFixed(0)}KB`;
    }
    return `${value}B`;
  }

  watch(configVisible, (visible) => {
    if (!visible) {
      resetState();
    }
  });

  onUnmounted(() => {
    stopPolling();
    revokeTestResultPreviewURLs();
  });
</script>

<style scoped lang="less">
  .base-content {
    display: flex;
    width: 100%;
    flex-direction: column;
    gap: 14px;

    .title-wrapper {
      width: 100%;
      display: flex;
      align-items: center;
      justify-content: space-between;

      .title {
        width: 300px;
        height: 29px;
        background-image: url("../assets/images/config-bar.png");
        background-size: cover;
        display: flex;
        align-items: center;
        font-size: 20px;
        font-weight: 600;
        color: #f1f8ff;
        padding-left: 32px;
      }
    }

    .form {
      display: flex;
      flex-direction: column;
      gap: 12px;

      .form-item {
        display: flex;
        flex-direction: column;
        gap: 8px;
      }

      .label {
        font-size: 14px;
        color: #a0c2e9;

        &.required::before {
          content: "*";
          color: #ff4d4f;
          margin-right: 2px;
        }
      }
    }

    .test-upload {
      ::v-deep(.ant-upload-wrapper .ant-upload-drag) {
        background: #010f2280;
        border: 0;
      }
    }

    .test-result {
      width: 100%;
      background: #065ccc75;
      box-shadow: 0 0 7.29px 0 #0e9bffbd inset;
      border-radius: 4px;
      min-height: 106px;

      .empty {
        width: 100%;
        min-height: 106px;
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;

        img {
          width: 56px;
          height: 44px;
        }

        span {
          font-size: 14px;
          color: #09459f;
          margin-top: 12px;
        }
      }

      .result-wrapper {
        width: 100%;
        display: flex;
        flex-direction: column;
      }

      .item {
        display: flex;
        gap: 10px;
        padding: 16px;
        border-bottom: 1px solid rgba(160, 194, 233, 0.16);

        &:last-child {
          border-bottom: 0;
        }

        .icon {
          width: 16px;
          height: 16px;
          margin-top: 2px;
        }

        .info {
          width: 0;
          flex-grow: 1;
          display: flex;
          flex-direction: column;
          gap: 8px;
        }

        .result-title {
          font-size: 14px;
          color: #fff;
        }

        .status-text {
          color: #9fd2ff;
          margin-left: 6px;
        }

        .desc {
          font-size: 14px;
          color: #acbdd4;
          line-height: 1.6;
          word-break: break-all;
        }

        .error-text {
          color: #ff9b9b;
        }
      }
    }

    .preview-wrap {
      position: relative;
      width: 100%;
      overflow: hidden;
      border-radius: 8px;
      background: rgba(2, 24, 60, 0.72);
      border: 1px solid rgba(87, 140, 204, 0.32);
    }

    .preview {
      width: 100%;
      height: 100%;
      object-fit: contain;
      display: block;
    }

    .preview-box {
      position: absolute;
      border: 2px solid #ff6868;
      box-sizing: border-box;
    }

    .preview-box-label {
      position: absolute;
      left: 0;
      top: 0;
      transform: translateY(-100%);
      background: rgba(255, 104, 104, 0.9);
      color: #fff;
      font-size: 12px;
      line-height: 1.2;
      padding: 2px 6px;
      white-space: nowrap;
    }

    .btn-group {
      width: 100%;
      display: flex;
      align-items: center;
      justify-content: flex-end;
      gap: 28px;

      .btn {
        width: 120px;
        height: 38px;
        background-image: url("../assets/images/btn1.png");
        background-size: cover;
        border: none;
        cursor: pointer;
        color: #fff;
        font-size: 16px;

        &:disabled {
          opacity: 0.5;
          cursor: not-allowed;
        }

        &.cancel {
          background-image: url("../assets/images/btn2.png");
        }
      }
    }

    .footer-btns {
      margin-top: 4px;
    }
  }
</style>
