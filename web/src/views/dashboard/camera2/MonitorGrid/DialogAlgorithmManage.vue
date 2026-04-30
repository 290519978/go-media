<template>
  <Dialog v-model="configVisible" width="860px" :title="dialogTitle">
    <div class="base-content">
      <div v-if="loading" class="loading-wrapper">
        <ASpin size="large" />
        <p class="loading-text">正在加载任务与算法配置...</p>
      </div>

      <template v-else-if="deviceDetail">
        <div class="title-wrapper">
          <p class="title">任务信息</p>
          <button class="btn" :disabled="submitting" @click="submit">
            {{ submitting ? "保存中..." : "保存设置" }}
          </button>
        </div>

        <div class="form">
          <div class="form-item">
            <label class="label required">任务名称：</label>
            <AInput v-model:value="form.name" size="large" placeholder="请输入任务名称" />
          </div>

          <div class="form-item">
            <label class="label">录制策略：</label>
            <ASelect v-model:value="form.recording_policy" size="large" :options="recordingPolicyOptions" />
          </div>
        </div>

        <AAlert
          class="alert-tip"
          type="info"
          show-icon
          message="此处仅快速维护当前设备的任务名称、录制策略和算法绑定。抽帧频率、录制前后秒、报警周期、报警等级会沿用当前值或默认值，如需精调请到视频任务编辑页。"
        />

        <div class="title-wrapper">
          <p class="title">算法设置</p>
          <button class="btn" @click="handleAddAlgorithm">新增算法</button>
        </div>

        <div class="form">
          <div class="form-item">
            <label class="label required">选择算法：</label>
            <ASelect
              v-model:value="form.algorithm_ids"
              mode="multiple"
              allow-clear
              size="large"
              :options="algorithmOptions"
              placeholder="请选择算法"
              @change="syncAlgorithmIDs"
            />
          </div>
        </div>

        <div class="list">
          <div v-if="selectedAlgorithms.length === 0" class="empty-text">请至少选择一个算法</div>

          <div v-for="item in selectedAlgorithms" :key="item.id" class="item">
            <div class="type-icon"></div>
            <div class="info">
              <p class="title">{{ item.name }}</p>
              <p class="meta">报警周期:{{ item.alertCycleSeconds }}秒 | 报警等级:{{ item.alarmLevelName }}</p>
            </div>
            <div class="del-icon" @click="removeAlgorithm(item.id)"></div>
          </div>
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

  <DialogAddAlgorithm ref="addAlgorithmRef" @created="handleAlgorithmCreated" />
</template>

<script setup lang="ts">
  import { computed, reactive, ref } from "vue";
  import { Alert as AAlert, Input as AInput, Select as ASelect, Spin as ASpin, message } from "ant-design-vue";
  import Dialog from "../components/Dialog/index.vue";
  import DialogAddAlgorithm from "./DialogAddAlgorithm.vue";
  import { alarmLevelAPI, algorithmAPI, deviceAPI, taskAPI } from "@/api/modules";

  type RecordingPolicy = "none" | "alarm_clip";
  type FrameRateMode = "fps" | "interval";

  type DeviceDetail = {
    id: string;
    name: string;
    area_id: string;
    status: string;
  };

  type AlarmLevel = {
    id: string;
    name: string;
    severity: number;
  };

  type AlgorithmOption = {
    id: string;
    name: string;
  };

  type TaskAlgorithmConfig = {
    algorithm_id: string;
    algorithm_name?: string;
    alarm_level_id?: string;
    alarm_level_name?: string;
    alert_cycle_seconds: number;
  };

  type TaskDeviceConfig = {
    device_id: string;
    algorithm_configs: TaskAlgorithmConfig[];
    frame_rate_mode: FrameRateMode;
    frame_rate_value: number;
    recording_policy: RecordingPolicy;
    recording_pre_seconds: number;
    recording_post_seconds: number;
  };

  type TaskItem = {
    task: {
      id: string;
      name: string;
      status: string;
      notes?: string;
    };
    device_configs: TaskDeviceConfig[];
  };

  type TaskDefaults = {
    recording_policy_default: RecordingPolicy;
    recording_pre_seconds_default: number;
    recording_post_seconds_default: number;
    alert_cycle_seconds_default: number;
    alarm_level_id_default: string;
    frame_rate_mode_default: FrameRateMode;
    frame_rate_value_default: number;
  };

  type SelectedAlgorithm = {
    id: string;
    name: string;
    alertCycleSeconds: number;
    alarmLevelID: string;
    alarmLevelName: string;
  };

  const fallbackTaskDefaults: TaskDefaults = {
    recording_policy_default: "none",
    recording_pre_seconds_default: 8,
    recording_post_seconds_default: 12,
    alert_cycle_seconds_default: 60,
    alarm_level_id_default: "alarm_level_1",
    frame_rate_mode_default: "interval",
    frame_rate_value_default: 5,
  };

  const emits = defineEmits<{
    saved: [];
  }>();

  const configVisible = ref(false);
  const loading = ref(false);
  const submitting = ref(false);
  const currentDeviceID = ref("");
  const deviceDetail = ref<DeviceDetail | null>(null);
  const boundTask = ref<TaskItem | null>(null);
  const algorithms = ref<AlgorithmOption[]>([]);
  const alarmLevels = ref<AlarmLevel[]>([]);
  const taskDefaults = ref<TaskDefaults>({ ...fallbackTaskDefaults });
  const existingAlgorithmMetaMap = ref<Record<string, SelectedAlgorithm>>({});

  const form = reactive<{
    name: string;
    recording_policy: RecordingPolicy;
    algorithm_ids: string[];
  }>({
    name: "",
    recording_policy: fallbackTaskDefaults.recording_policy_default,
    algorithm_ids: [],
  });

  const recordingPolicyOptions = [
    { label: "不录制", value: "none" },
    { label: "报警片段录制", value: "alarm_clip" },
  ];

  const dialogTitle = computed(() => {
    const deviceName = deviceDetail.value?.name || currentDeviceID.value || "设备";
    return `任务与算法设置-${deviceName}`;
  });

  const algorithmOptions = computed(() => {
    return algorithms.value.map((item) => ({
      label: item.name,
      value: item.id,
    }));
  });

  const selectedAlgorithms = computed<SelectedAlgorithm[]>(() => {
    return form.algorithm_ids.map((algorithmID) => {
      const existing = existingAlgorithmMetaMap.value[algorithmID];
      if (existing) {
        return existing;
      }
      const defaultAlarmLevelID = String(taskDefaults.value.alarm_level_id_default || "").trim() || fallbackTaskDefaults.alarm_level_id_default;
      return {
        id: algorithmID,
        name: algorithms.value.find((item) => item.id === algorithmID)?.name || algorithmID,
        alertCycleSeconds: taskDefaults.value.alert_cycle_seconds_default,
        alarmLevelID: defaultAlarmLevelID,
        alarmLevelName: alarmLevelNameText(defaultAlarmLevelID),
      };
    });
  });

  const addAlgorithmRef = ref<{ open: (payload?: { deviceID?: string; deviceName?: string }) => void } | null>(null);

  function alarmLevelNameText(alarmLevelID: string) {
    return alarmLevels.value.find((item) => item.id === alarmLevelID)?.name || alarmLevelID || "-";
  }

  function syncAlgorithmIDs() {
    form.algorithm_ids = Array.from(new Set((form.algorithm_ids || []).map((item) => String(item || "").trim()).filter(Boolean)));
  }

  function removeAlgorithm(algorithmID: string) {
    form.algorithm_ids = form.algorithm_ids.filter((item) => item !== algorithmID);
  }

  function handleAddAlgorithm() {
    addAlgorithmRef.value?.open({
      deviceID: currentDeviceID.value,
      deviceName: String(deviceDetail.value?.name || "").trim(),
    });
  }

  function handleAlgorithmCreated(payload: { id: string; name: string }) {
    const algorithmID = String(payload?.id || "").trim();
    const algorithmName = String(payload?.name || "").trim();
    if (!algorithmID) {
      return;
    }
    if (!algorithms.value.some((item) => item.id === algorithmID)) {
      algorithms.value = [
        {
          id: algorithmID,
          name: algorithmName || algorithmID,
        },
        ...algorithms.value,
      ];
    }
    form.algorithm_ids = [...form.algorithm_ids, algorithmID];
    syncAlgorithmIDs();
  }

  function normalizeTaskDefaults(raw: Partial<TaskDefaults> | null | undefined): TaskDefaults {
    const recordingPolicyDefault = String(raw?.recording_policy_default || "").trim().toLowerCase() === "alarm_clip"
      ? "alarm_clip"
      : "none";
    const alertCycleSecondsDefault = Number(raw?.alert_cycle_seconds_default);
    const frameRateValueDefault = Number(raw?.frame_rate_value_default);
    const preSeconds = Number(raw?.recording_pre_seconds_default);
    const postSeconds = Number(raw?.recording_post_seconds_default);
    return {
      recording_policy_default: recordingPolicyDefault,
      recording_pre_seconds_default: Number.isFinite(preSeconds) && preSeconds >= 1 && preSeconds <= 600 ? Math.round(preSeconds) : fallbackTaskDefaults.recording_pre_seconds_default,
      recording_post_seconds_default: Number.isFinite(postSeconds) && postSeconds >= 1 && postSeconds <= 600 ? Math.round(postSeconds) : fallbackTaskDefaults.recording_post_seconds_default,
      alert_cycle_seconds_default: Number.isFinite(alertCycleSecondsDefault) && alertCycleSecondsDefault >= 0 && alertCycleSecondsDefault <= 86400 ? Math.round(alertCycleSecondsDefault) : fallbackTaskDefaults.alert_cycle_seconds_default,
      alarm_level_id_default: String(raw?.alarm_level_id_default || "").trim() || fallbackTaskDefaults.alarm_level_id_default,
      frame_rate_mode_default: String(raw?.frame_rate_mode_default || "").trim().toLowerCase() === "fps" ? "fps" : fallbackTaskDefaults.frame_rate_mode_default,
      frame_rate_value_default: Number.isFinite(frameRateValueDefault) && frameRateValueDefault >= 1 && frameRateValueDefault <= 60 ? Math.round(frameRateValueDefault) : fallbackTaskDefaults.frame_rate_value_default,
    };
  }

  function normalizeAlgorithmList(rawItems: unknown[]): AlgorithmOption[] {
    return rawItems
      .map((item) => {
        const current = item && typeof item === "object" && "algorithm" in item
          ? (item as { algorithm?: Record<string, unknown> }).algorithm
          : item as Record<string, unknown>;
        return {
          id: String(current?.id || ""),
          name: String(current?.name || current?.id || ""),
        };
      })
      .filter((item) => Boolean(item.id));
  }

  function fillFormByTask(task: TaskItem | null) {
    form.name = String(task?.task.name || "");
    const currentConfig = (task?.device_configs || []).find((item) => String(item.device_id || "") === currentDeviceID.value);
    form.recording_policy = currentConfig?.recording_policy === "alarm_clip" ? "alarm_clip" : taskDefaults.value.recording_policy_default;
    form.algorithm_ids = (currentConfig?.algorithm_configs || []).map((item) => String(item.algorithm_id || "").trim()).filter(Boolean);
    syncAlgorithmIDs();

    const nextMetaMap: Record<string, SelectedAlgorithm> = {};
    for (const item of currentConfig?.algorithm_configs || []) {
      const algorithmID = String(item.algorithm_id || "").trim();
      if (!algorithmID) continue;
      const defaultAlarmLevelID = String(taskDefaults.value.alarm_level_id_default || "").trim() || fallbackTaskDefaults.alarm_level_id_default;
      const alarmLevelID = String(item.alarm_level_id || "").trim() || defaultAlarmLevelID;
      nextMetaMap[algorithmID] = {
        id: algorithmID,
        name: String(item.algorithm_name || algorithms.value.find((algorithm) => algorithm.id === algorithmID)?.name || algorithmID),
        alertCycleSeconds: Number(item.alert_cycle_seconds || taskDefaults.value.alert_cycle_seconds_default),
        alarmLevelID,
        alarmLevelName: String(item.alarm_level_name || alarmLevelNameText(alarmLevelID)),
      };
    }
    existingAlgorithmMetaMap.value = nextMetaMap;
  }

  async function loadDialogData(deviceID: string) {
    if (!deviceID) {
      return;
    }
    loading.value = true;
    try {
      // 快速编辑弹框要同时知道任务归属、算法列表和默认参数，才能区分“沿用现值”和“新增走默认值”。
      const [detailResp, taskResp, defaultsResp, algorithmResp, alarmLevelResp] = await Promise.all([
        deviceAPI.detail(deviceID) as Promise<DeviceDetail>,
        taskAPI.list() as Promise<{ items?: TaskItem[] }>,
        taskAPI.defaults() as Promise<Partial<TaskDefaults>>,
        algorithmAPI.list() as Promise<{ items?: unknown[] }>,
        alarmLevelAPI.list() as Promise<{ items?: AlarmLevel[] }>,
      ]);

      currentDeviceID.value = deviceID;
      deviceDetail.value = detailResp;
      taskDefaults.value = normalizeTaskDefaults(defaultsResp);
      algorithms.value = normalizeAlgorithmList(Array.isArray(algorithmResp.items) ? algorithmResp.items : []);
      alarmLevels.value = [...(alarmLevelResp.items || [])].sort((a, b) => Number(a.severity || 0) - Number(b.severity || 0));

      const tasks = Array.isArray(taskResp.items) ? taskResp.items : [];
      boundTask.value = tasks.find((item) =>
        Array.isArray(item.device_configs) && item.device_configs.some((config) => String(config.device_id || "") === deviceID),
      ) || null;

      fillFormByTask(boundTask.value);
    } catch (error) {
      message.error((error as Error).message || "加载任务与算法配置失败");
      deviceDetail.value = null;
      boundTask.value = null;
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

  async function submit() {
    const name = String(form.name || "").trim();
    if (!name) {
      message.error("请输入任务名称");
      return;
    }
    syncAlgorithmIDs();
    if (form.algorithm_ids.length === 0) {
      message.error("请至少选择一个算法");
      return;
    }

    submitting.value = true;
    try {
      if (boundTask.value) {
        const resp = await taskAPI.quickUpdateDevice(boundTask.value.task.id, currentDeviceID.value, {
          name,
          // camera2 快捷配置页不再编辑备注，但要保留已有任务的备注内容，避免保存时被误清空。
          notes: String(boundTask.value.task.notes || "").trim(),
          recording_policy: form.recording_policy,
          algorithm_ids: form.algorithm_ids,
        }) as { message?: string };
        message.success(resp?.message || "当前设备任务配置已保存");
      } else {
        const defaultAlarmLevelID = String(taskDefaults.value.alarm_level_id_default || "").trim() || fallbackTaskDefaults.alarm_level_id_default;
        const createResp = await taskAPI.create({
          name,
          notes: "",
          device_configs: [
            {
              device_id: currentDeviceID.value,
              algorithm_configs: form.algorithm_ids.map((algorithmID) => ({
                algorithm_id: algorithmID,
                alarm_level_id: defaultAlarmLevelID,
                alert_cycle_seconds: taskDefaults.value.alert_cycle_seconds_default,
              })),
              frame_rate_mode: taskDefaults.value.frame_rate_mode_default,
              frame_rate_value: taskDefaults.value.frame_rate_value_default,
              recording_policy: form.recording_policy,
              recording_pre_seconds: taskDefaults.value.recording_pre_seconds_default,
              recording_post_seconds: taskDefaults.value.recording_post_seconds_default,
            },
          ],
        }) as { task?: { id?: string } };
        const taskID = String(createResp?.task?.id || "").trim();
        if (!taskID) {
          throw new Error("创建任务成功，但未返回任务 ID");
        }
        await taskAPI.start(taskID);
        message.success("设备任务已创建并启动");
      }

      await loadDialogData(currentDeviceID.value);
      emits("saved");
    } catch (error) {
      message.error((error as Error).message || "保存任务与算法配置失败");
    } finally {
      submitting.value = false;
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
      width: 100%;
      display: flex;
      flex-direction: column;
      gap: 12px;
    }

    .alert-tip {
      margin-top: -4px;
      margin-bottom: 4px;
    }

    .readonly-grid {
      width: 100%;
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 10px;
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

    .label {
      color: #a0c2e9;
      font-size: 14px;
      text-align: right;
      width: 100%;

      &.required::before {
        content: "*";
        color: #ff4d4f;
        margin-right: 2px;
      }
    }

    .readonly-label {
      color: #a0c2e9;
      font-size: 14px;
      text-align: right;
      width: 100%;
    }

    .readonly-value {
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

    .list {
      width: 100%;
      max-height: 380px;
      display: flex;
      flex-direction: column;
      overflow-y: auto;
      scrollbar-width: none;
      gap: 12px;

      .empty-text {
        width: 100%;
        padding: 48px 0;
        text-align: center;
        font-size: 14px;
        color: #7ba0ca;
        background: rgba(6, 92, 204, 0.2);
        box-shadow: 0 0 7.29px 0 #0e9bffbd inset;
        border-radius: 4px;
      }

      .item {
        width: 100%;
        min-height: 80px;
        background-image: url("../assets/images/list-item.png");
        background-size: 100% 100%;
        display: flex;
        align-items: center;
        padding: 20px;
        gap: 12px;
        box-sizing: border-box;

        .type-icon {
          background-image: url("../assets/images/type1.png");
          width: 48px;
          height: 48px;
          background-size: 100% 100%;
          flex-shrink: 0;
        }

        .info {
          width: 0;
          flex-grow: 1;
          display: flex;
          flex-direction: column;
          gap: 4px;

          .title {
            font-size: 16px;
            font-weight: 600;
            color: #fff;
          }

          .meta {
            font-size: 14px;
            font-weight: 400;
            color: #7ba0ca;
          }
        }

        .del-icon {
          width: 20px;
          height: 20px;
          background-image: url("../assets/images/del.png");
          background-size: 100% 100%;
          cursor: pointer;
          flex-shrink: 0;
        }
      }
    }

    .btn-group {
      width: 100%;
      display: flex;
      align-items: center;
      justify-content: flex-end;
      gap: 28px;
      margin-top: 12px;
      margin-bottom: 10px;
    }

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
</style>
