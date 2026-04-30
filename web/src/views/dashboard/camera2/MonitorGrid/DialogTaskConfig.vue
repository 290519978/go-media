<template>
  <Dialog v-model="configVisible" width="860px" :title="dialogTitle">
    <div class="base-content">
      <div v-if="loading" class="loading-wrapper">
        <ASpin size="large" />
        <p class="loading-text">正在加载设备信息...</p>
      </div>

      <template v-else-if="deviceDetail">
        <div class="title-wrapper">
          <p class="title">设备信息</p>
          <button class="btn" :disabled="deviceSaving" @click="saveDeviceInfo">
            {{ deviceSaving ? "保存中..." : "保存设备信息" }}
          </button>
        </div>

        <div class="form">
          <div class="readonly-grid">
            <div class="readonly-item">
              <span class="readonly-label">协议类型</span>
              <span class="readonly-value">{{ protocolTypeText }}</span>
            </div>
            <div class="readonly-item">
              <span class="readonly-label">来源类型</span>
              <span class="readonly-value">{{ sourceTypeText }}</span>
            </div>
            <div class="readonly-item">
              <span class="readonly-label">当前状态</span>
              <span class="readonly-value">{{ deviceStatusText }}</span>
            </div>
          </div>

          <div class="form-item">
            <label class="label">设备名称：</label>
            <AInput v-model:value="deviceForm.name" size="large" placeholder="请输入设备名称" />
          </div>

          <div class="form-item">
            <label class="label">所属区域：</label>
            <ASelect
              v-model:value="deviceForm.area_id"
              size="large"
              :options="areaOptions"
              placeholder="请选择所属区域"
            />
          </div>

          <template v-if="isRTSPDevice">
            <div class="form-item">
              <label class="label">RTSP地址：</label>
              <AInput v-model:value="deviceForm.origin_url" size="large" placeholder="rtsp://username:password@host:554/stream" />
            </div>

            <div class="form-item">
              <label class="label">传输方式：</label>
              <ASelect v-model:value="deviceForm.transport" size="large" :options="transportOptions" />
            </div>
          </template>

          <template v-else-if="isRTMPDevice">
            <div class="form-item">
              <label class="label">App：</label>
              <AInput v-model:value="deviceForm.app" size="large" placeholder="请输入 App" />
            </div>

            <div class="form-item">
              <label class="label">Stream ID：</label>
              <AInput v-model:value="deviceForm.stream_id" size="large" placeholder="请输入 Stream ID" />
            </div>

            <div class="form-item">
              <label class="label">推流Token：</label>
              <AInput v-model:value="deviceForm.publish_token" size="large" placeholder="可选，不填则清空" />
            </div>
          </template>

          <template v-else-if="isGB28181Device">
            <div class="form-item">
              <label class="label">传输方式：</label>
              <div class="display-value">{{ transportText }}</div>
            </div>

            <div class="form-item">
              <label class="label">流标识：</label>
              <div class="display-value">{{ deviceDetail.stream_id || "-" }}</div>
            </div>

            <div class="form-item">
              <label class="label">流地址：</label>
              <div class="display-value multiline">{{ deviceDetail.stream_url || "-" }}</div>
            </div>

            <AAlert
              class="alert-tip"
              type="info"
              show-icon
              message="GB28181 接入参数在此仅展示，如需修改请到 GB28181 维护页操作。"
            />
          </template>
        </div>

        <div class="btn-group">
          <button class="btn cancel" @click="close">关闭</button>
        </div>
      </template>

      <div v-else class="loading-wrapper">
        <p class="loading-text">未找到设备信息</p>
      </div>
    </div>
  </Dialog>
</template>

<script setup lang="ts">
  import { computed, reactive, ref } from "vue";
  import { Alert as AAlert, Input as AInput, Select as ASelect, Spin as ASpin, message } from "ant-design-vue";
  import Dialog from "../components/Dialog/index.vue";
  import { areaAPI, deviceAPI } from "@/api/modules";

  type AreaOption = {
    label: string;
    value: string;
  };

  type MediaSourceDetail = {
    id: string;
    name: string;
    area_id: string;
    source_type: string;
    protocol: string;
    transport: string;
    stream_url: string;
    app: string;
    stream_id: string;
    status: string;
    output_config: string;
  };

  const emits = defineEmits<{
    saved: [];
  }>();

  const configVisible = ref(false);
  const loading = ref(false);
  const deviceSaving = ref(false);
  const currentDeviceID = ref("");
  const deviceDetail = ref<MediaSourceDetail | null>(null);
  const areaOptions = ref<AreaOption[]>([]);

  const deviceForm = reactive({
    name: "",
    area_id: "",
    transport: "tcp",
    origin_url: "",
    app: "live",
    stream_id: "",
    publish_token: "",
  });

  const transportOptions = [
    { label: "TCP", value: "tcp" },
    { label: "UDP", value: "udp" },
  ];

  const dialogTitle = computed(() => {
    if (!deviceDetail.value) {
      return "设备设置";
    }
    return `设备设置-${deviceDetail.value.name || deviceDetail.value.id}（${areaNameText.value}）`;
  });

  const areaNameText = computed(() => {
    const areaID = String(deviceDetail.value?.area_id || deviceForm.area_id || "").trim();
    return areaOptions.value.find((item) => item.value === areaID)?.label || areaID || "未分配区域";
  });

  const isRTSPDevice = computed(() => {
    return String(deviceDetail.value?.source_type || "").trim().toLowerCase() === "pull";
  });

  const isRTMPDevice = computed(() => {
    return String(deviceDetail.value?.source_type || "").trim().toLowerCase() === "push";
  });

  const isGB28181Device = computed(() => {
    return String(deviceDetail.value?.source_type || "").trim().toLowerCase() === "gb28181";
  });

  const protocolTypeText = computed(() => {
    if (isRTSPDevice.value) return "RTSP 拉流";
    if (isRTMPDevice.value) return "RTMP 推流";
    if (isGB28181Device.value) return "GB28181";
    return String(deviceDetail.value?.protocol || "-").toUpperCase();
  });

  const sourceTypeText = computed(() => {
    const sourceType = String(deviceDetail.value?.source_type || "").trim().toLowerCase();
    if (sourceType === "pull") return "拉流通道";
    if (sourceType === "push") return "推流通道";
    if (sourceType === "gb28181") return "国标通道";
    return sourceType || "-";
  });

  const deviceStatusText = computed(() => {
    const status = String(deviceDetail.value?.status || "").trim().toLowerCase();
    if (status === "online") return "在线";
    if (status === "offline") return "离线";
    return status || "-";
  });

  const transportText = computed(() => {
    const transport = String(deviceDetail.value?.transport || "").trim().toLowerCase();
    if (transport === "tcp") return "TCP";
    if (transport === "udp") return "UDP";
    return transport || "-";
  });

  function parsePublishTokenFromOutput(raw: string) {
    const text = String(raw || "").trim();
    if (!text) {
      return "";
    }
    try {
      const parsed = JSON.parse(text);
      if (parsed && typeof parsed === "object") {
        return String((parsed as Record<string, unknown>).publish_token || "");
      }
    } catch {
      // ignore parse error
    }
    return "";
  }

  function normalizeAreaOptions(raw: Record<string, unknown> | null | undefined): AreaOption[] {
    const source = Array.isArray(raw?.flat)
      ? raw.flat
      : Array.isArray(raw?.items)
        ? raw.items
        : [];
    return source
      .map((item: { id?: string; name?: string }) => ({
        label: String(item.name || item.id || ""),
        value: String(item.id || ""),
      }))
      .filter((item: AreaOption) => Boolean(item.value));
  }

  function fillDeviceForm(detail: MediaSourceDetail) {
    deviceForm.name = String(detail.name || "");
    deviceForm.area_id = String(detail.area_id || "");
    deviceForm.transport = String(detail.transport || "tcp").toLowerCase() || "tcp";
    deviceForm.origin_url = String(detail.stream_url || "");
    deviceForm.app = String(detail.app || "live");
    deviceForm.stream_id = String(detail.stream_id || "");
    deviceForm.publish_token = parsePublishTokenFromOutput(detail.output_config);
  }

  async function loadDialogData(deviceID: string) {
    if (!deviceID) {
      return;
    }
    loading.value = true;
    try {
      const [detailResp, areaResp] = await Promise.all([
        deviceAPI.detail(deviceID) as Promise<MediaSourceDetail>,
        areaAPI.list() as Promise<Record<string, unknown>>,
      ]);

      currentDeviceID.value = deviceID;
      deviceDetail.value = detailResp;
      areaOptions.value = normalizeAreaOptions(areaResp);
      fillDeviceForm(detailResp);
    } catch (error) {
      message.error((error as Error).message || "加载设备信息失败");
      deviceDetail.value = null;
    } finally {
      loading.value = false;
    }
  }

  async function open(deviceID: string) {
    configVisible.value = true;
    await loadDialogData(String(deviceID || "").trim());
  }

  function close() {
    configVisible.value = false;
  }

  defineExpose({
    open,
    close,
  });

  function isValidRTSPURL(raw: string) {
    const value = String(raw || "").trim();
    if (!/^rtsp:\/\//i.test(value)) return false;
    try {
      const parsed = new URL(value);
      return Boolean(parsed.hostname);
    } catch {
      return false;
    }
  }

  async function saveDeviceInfo() {
    if (!deviceDetail.value || !currentDeviceID.value) {
      return;
    }
    const name = String(deviceForm.name || "").trim();
    const areaID = String(deviceForm.area_id || "").trim();
    if (!name) {
      message.error("请输入设备名称");
      return;
    }
    if (!areaID) {
      message.error("请选择所属区域");
      return;
    }

    const payload: Record<string, unknown> = {
      name,
      area_id: areaID,
    };

    if (isRTSPDevice.value) {
      const originURL = String(deviceForm.origin_url || "").trim();
      if (!isValidRTSPURL(originURL)) {
        message.error("RTSP 地址格式错误，请使用 rtsp:// 并包含主机地址");
        return;
      }
      payload.origin_url = originURL;
      payload.transport = String(deviceForm.transport || "tcp").trim().toLowerCase() || "tcp";
    }

    if (isRTMPDevice.value) {
      const app = String(deviceForm.app || "").trim();
      const streamID = String(deviceForm.stream_id || "").trim();
      if (!app || !streamID) {
        message.error("请输入 App 和 Stream ID");
        return;
      }
      payload.app = app;
      payload.stream_id = streamID;
      payload.publish_token = String(deviceForm.publish_token || "").trim();
    }

    deviceSaving.value = true;
    try {
      await deviceAPI.update(currentDeviceID.value, payload);
      message.success("设备信息已保存");
      await loadDialogData(currentDeviceID.value);
      emits("saved");
    } catch (error) {
      message.error((error as Error).message || "设备信息保存失败");
    } finally {
      deviceSaving.value = false;
    }
  }
</script>

<style scoped lang="less">
  .base-content {
    display: flex;
    width: 100%;
    flex-direction: column;
    gap: 20px;

    .loading-wrapper {
      min-height: 320px;
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      gap: 16px;
    }

    .loading-text {
      font-size: 16px;
      color: #f0f7ff;
    }

    .title-wrapper {
      width: 100%;
      display: flex;
      align-items: center;
      justify-content: space-between;

      .title {
        width: 300px;
        height: 29px;
        background-image: url("../assets/images/config-bar.png");
        background-size: 100% 100%;
        display: flex;
        align-items: center;
        font-size: 18px;
        font-weight: 600;
        color: #f1f8ff;
        padding-left: 32px;
      }
    }

    .form {
      width: 100%;
      display: flex;
      flex-direction: column;
      gap: 10px;
    }

    .readonly-grid {
      width: 100%;
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 10px;
      margin-bottom: 4px;
    }

    .readonly-item,
    .form-item {
      width: 100%;
      display: grid;
      grid-template-columns: 88px 1fr;
      align-items: center;
      padding: 8px;
      gap: 8px;
      background: rgba(4, 39, 76, 0.42);
      border: 1px solid rgba(73, 140, 211, 0.36);
      border-radius: 6px;
      box-sizing: border-box;
    }

    .readonly-item {
      grid-template-columns: 72px 1fr;
    }

    .label,
    .readonly-label {
      color: #a0c2e9;
      font-size: 14px;
      text-align: right;
      width: 100%;
    }

    .readonly-value,
    .display-value {
      min-height: 40px;
      display: flex;
      align-items: center;
      padding: 0 12px;
      color: #f0f7ff;
      font-size: 14px;
      background: rgba(2, 24, 60, 0.72);
      border: 1px solid rgba(87, 140, 204, 0.32);
      border-radius: 6px;
      box-sizing: border-box;
    }

    .multiline {
      line-height: 1.5;
      align-items: flex-start;
      padding-top: 10px;
      padding-bottom: 10px;
      white-space: normal;
      word-break: break-all;
    }

    .alert-tip {
      margin-top: 4px;
    }

    .btn-group {
      width: 100%;
      display: flex;
      align-items: center;
      justify-content: flex-end;
      gap: 28px;
      margin-top: 20px;
      margin-bottom: 10px;
    }

    .btn {
      width: 140px;
      height: 38px;
      background-image: url("../assets/images/btn1.png");
      background-size: 100% 100%;
      border: none;
      cursor: pointer;
      color: #fff;
      font-size: 16px;

      &:disabled {
        opacity: 0.5;
        cursor: not-allowed;
      }

      &.cancel {
        width: 120px;
        background-image: url("../assets/images/btn2.png");
      }
    }
  }
</style>
