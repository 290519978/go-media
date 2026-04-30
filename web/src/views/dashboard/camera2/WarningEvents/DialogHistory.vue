<template>
  <Dialog title="历史报警" width="1237px" v-model="historyVisible">
    <div class="base-content">
      <div class="filter">
        <a-input style="width: 100%" v-model:value="searchValue" placeholder="设备名称">
          <template #suffix>
            <SearchOutlined />
          </template>
        </a-input>
        <div class="filter-item">
          <span class="label">区域：</span>
          <a-select v-model:value="selectedAreaID" style="width: 0px; flex-grow: 1" placeholder="全部">
            <a-select-option value="">全部</a-select-option>
            <a-select-option v-for="item in areaOptions" :key="item.id" :value="item.id">{{ item.name }}</a-select-option>
          </a-select>
        </div>
        <div class="filter-item">
          <span class="label">算法：</span>
          <a-select v-model:value="selectedAlgorithmID" style="width: 0px; flex-grow: 1" placeholder="全部">
            <a-select-option value="">全部</a-select-option>
            <a-select-option v-for="item in algorithmOptions" :key="item.id" :value="item.id">{{ item.name }}</a-select-option>
          </a-select>
        </div>
        <div class="filter-item">
          <span class="label">报警等级：</span>
          <a-select v-model:value="selectedLevelID" style="width: 0px; flex-grow: 1" placeholder="全部 ">
            <a-select-option value="">全部</a-select-option>
            <a-select-option v-for="item in levelOptions" :key="item.id" :value="item.id">{{ item.name }}</a-select-option>
          </a-select>
        </div>
        <div class="filter-item">
          <button class="reset-btn" @click="resetFilters">重置</button>
          <button class="confirm-btn" @click="handleQuery">确定</button>
        </div>
      </div>
      <div class="table">
        <div class="table-header">
          <div class="header-cell">图片</div>
          <div class="header-cell">时间</div>
          <div class="header-cell">区域</div>
          <div class="header-cell">类型</div>
          <div class="header-cell">等级</div>
          <div class="header-cell">状态</div>
          <div class="header-cell">操作</div>
        </div>
        <div class="table-body">
          <div class="table-row" v-for="item in eventList" :key="item.id">
            <div class="table-cell">
              <img class="event-image" :src="item.image" alt="" />
            </div>
            <div class="table-cell" style="display: flex; flex-direction: column">
              <div class="text">{{ item.date }}</div>
              <div class="text">{{ item.time }}</div>
            </div>
            <div class="table-cell">
              <div class="text">{{ item.location }}</div>
            </div>
            <div class="table-cell">
              <div class="text">{{ item.typeText }}</div>
            </div>
            <div class="table-cell">
              <div class="level" :class="item.level">{{ item.levelText }}</div>
            </div>
            <div class="table-cell">
              <div class="status" :class="item.status">{{ item.statusText }}</div>
            </div>
            <div class="table-cell">
              <button class="operation-btn" @click="handlerClick(item)">查看</button>
            </div>
          </div>
        </div>
      </div>
      <div class="pagination">
        <a-pagination
          :current="page"
          :page-size="pageSize"
          :show-quick-jumper="false"
          :show-size-changer="false"
          :total="total"
          @change="handlePageChange"
        />
      </div>
    </div>
  </Dialog>
  <DialogDetail ref="detailDialog" @handler="handleDetailUpdated" />
</template>
<script setup lang="ts">
  import { onMounted, ref } from "vue";
  import Dialog from "../components/Dialog/index.vue";
  import { SearchOutlined } from "@ant-design/icons-vue";
  import {
    buildCamera2SnapshotURL,
    fetchCamera2AlarmLevels,
    fetchCamera2Algorithms,
    fetchCamera2Areas,
    fetchCamera2HistoryEvents,
    mapCamera2Level,
    mapCamera2Status,
    splitCamera2DateTime,
  } from "../api";
  import DialogDetail from "./DialogDetail.vue";
  const emits = defineEmits(["updated"]);
  type DetailDialogInstance = {
    open: (eventItem?: { id?: string }) => void;
  };
  const areaOptions = ref<Array<{ id: string; name: string }>>([]);
  const algorithmOptions = ref<Array<{ id: string; name: string }>>([]);
  const levelOptions = ref<Array<{ id: string; name: string }>>([]);
  const eventList = ref<any[]>([]);
  const searchValue = ref("");
  const selectedAreaID = ref("");
  const selectedAlgorithmID = ref("");
  const selectedLevelID = ref("");
  const page = ref(1);
  const pageSize = 10;
  const total = ref(0);

  const historyVisible = ref(false);
  const currentSource = ref<"runtime" | "patrol">("runtime");
  const open = async (source: "runtime" | "patrol" = "runtime") => {
    currentSource.value = source;
    historyVisible.value = true;
    await Promise.all([loadFilterOptions(), loadHistory()]);
  };

  const loadFilterOptions = async () => {
    const [areas, algorithms, levels] = await Promise.all([
      fetchCamera2Areas().catch(() => []),
      fetchCamera2Algorithms().catch(() => []),
      fetchCamera2AlarmLevels().catch(() => []),
    ]);
    areaOptions.value = areas;
    algorithmOptions.value = algorithms;
    levelOptions.value = levels;
  };

  const loadHistory = async () => {
    const response = await fetchCamera2HistoryEvents({
      page: page.value,
      page_size: pageSize,
      source: currentSource.value,
      area_id: selectedAreaID.value || undefined,
      algorithm_id: selectedAlgorithmID.value || undefined,
      alarm_level_id: selectedLevelID.value || undefined,
      device_name: searchValue.value || undefined,
    });
    total.value = Number(response.total || 0);
    eventList.value = Array.isArray(response.items)
      ? response.items.map((item: any) => {
        const { dateText, timeText } = splitCamera2DateTime(item.occurred_at);
        const level = mapCamera2Level(item.alarm_level_severity, item.alarm_level_name);
        const status = mapCamera2Status(item.status);
        return {
          id: String(item.id || ""),
          image: buildCamera2SnapshotURL(item.snapshot_path),
          date: dateText,
          time: timeText,
          location: String(item.area_name || item.area_id || "未分配区域"),
          typeText: String(item.display_name || item.algorithm_name || item.algorithm_id || "未知巡查"),
          level: level.className,
          levelText: level.text,
          status: status.className,
          statusText: status.text,
        };
      })
      : [];
  };

  const handleQuery = async () => {
    page.value = 1;
    await loadHistory();
  };

  const resetFilters = async () => {
    searchValue.value = "";
    selectedAreaID.value = "";
    selectedAlgorithmID.value = "";
    selectedLevelID.value = "";
    page.value = 1;
    await loadHistory();
  };

  const detailDialog = ref<DetailDialogInstance | null>(null);

  const handlerClick = (item: { id?: string }) => {
    detailDialog.value?.open(item);
  }

  const handlePageChange = async (nextPage: number) => {
    page.value = nextPage;
    await loadHistory();
  };

  const handleDetailUpdated = async () => {
    await loadHistory();
    emits("updated");
  };

  onMounted(() => {
    void loadFilterOptions();
  });
  defineExpose({
    open,
  });
</script>

<style scoped lang="less">
  .base-content {
    display: flex;
    flex-direction: column;
    gap: 20px;
    .filter {
      width: 100%;
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 20px 40px;
      .filter-item {
        width: 100%;
        display: flex;
        align-items: center;
        .label {
          width: max-content;
          text-align: left;
          margin-right: 8px;
          font-size: 14px;
          color: #a0c2e9;
        }
        .reset-btn {
          background: linear-gradient(3.4deg, rgba(1, 44, 84, 0.502) 3.66%, rgba(0, 22, 42, 0.502) 98.05%);
          box-shadow: inset 0px 0px 9.2625px rgba(5, 134, 255, 0.56);
          border-radius: 4px;
          color: #fff;
          width: 78px;
          height: 36px;
          border: 0.79px solid;
          border-image-source: linear-gradient(180deg, #034f97 0%, #034f97 100%);
        }
        .confirm-btn {
          background: rgba(14, 86, 141, 0.502);
          box-shadow: inset 0px 0px 9.22292px #1d87ff;
          border-radius: 4px;
          width: 78px;
          color: #fff;
          height: 36px;
          margin-left: 10px;
        }
      }
    }
    .table {
      width: 100%;
      height: 700px;
      display: flex;
      flex-direction: column;
      .table-header {
        background-color: #0082e625;
        width: 100%;
        display: grid;
        grid-template-columns: 120px 120px 1fr 120px 120px 120px 120px;
        .header-cell {
          padding: 10px 10px;
          font-size: 14px;
          color: #b7d7fb;
          width: 100%;
          text-align: center;
        }
      }
      .table-body {
        width: 100%;
        height: 0;
        flex-grow: 1;
        display: flex;
        flex-direction: column;
        overflow-y: auto;
        scrollbar-width: none;
        .table-row {
          display: grid;
          grid-template-columns: 120px 120px 1fr 120px 120px 120px 120px;
          grid-template-rows: 62px;
          &:nth-child(2n) {
            background: #0082e625;
          }
          .table-cell {
            width: 100%;
            height: 100%;
            display: flex;
            align-items: center;
            justify-content: center;
            .event-image {
              width: 58px;
              height: 48px;
              border-radius: 4px;
              overflow: hidden;
              border: 1px solid rgba(255, 255, 255, 0.08);
            }
            .text {
              line-height: 1.35;
              color: #d8e8fb;
              text-align: center;
              padding: 4px 0;
              font-size: 14px;
            }
            .level {
              border-radius: 30px;
              text-align: center;
              font-size: 14px;
              width: 50px;
              &.high {
                color: #ff4d59;
                background: rgba(246, 111, 123, 0.35);
              }
              &.middle {
                color: #f19f45;
                background: rgba(246, 153, 75, 0.3);
              }
              &.low {
                color: #e5f03d;
                background: rgba(128, 140, 25, 0.3);
              }
              &.patrol {
                color: #44b7ff;
                background: rgba(28, 89, 158, 0.3);
              }
            }
            .status {
              border-radius: 4px;
              text-align: center;
              font-size: 14px;
              width: 50px;
              border: 1px solid transparent;
              &.pending {
                border-color: #ffbf3b;
                color: #ffbf3b;
                background: rgba(255, 191, 59, 0.3);
              }
              &.resolved {
                border-color: #36f7ff;
                color: #36f7ff;
                background: rgba(54, 247, 255, 0.2);
              }
            }
            .operation-btn {
              border-radius: 4px;
              padding: 4px 8px;
              font-size: 14px;
              color: #0082e6;
              border: none;
            }
          }
        }
      }
    }
    .pagination {
      width: 100%;
      display: flex;
      justify-content: flex-end;
    }
  }
</style>
