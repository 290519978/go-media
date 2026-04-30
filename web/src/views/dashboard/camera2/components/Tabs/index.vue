<template>
  <div class="panel-tabs">
    <span
      v-for="tab in tabs"
      :key="tab.value"
      class="tab"
      :class="{ active: tab.value === activeTab }"
      @click="handleTabClick(tab.value)"
      >{{ tab.label }}</span
    >
  </div>
</template>
<script setup>
  const activeTab = defineModel();
  const props = defineProps({
    tabs: {
      type: Array,
      default: () => [
        { label: "今日", value: "today" },
        { label: "本周", value: "week" },
        { label: "自定义", value: "custom" },
      ],
    },
  });
  const emit = defineEmits(["change"]);
  const handleTabClick = tab => {
    activeTab.value = tab;
    emit("change", tab);
  };
</script>

<style scoped lang="less">
  .panel-tabs {
    display: flex;
    gap: 3px;
    align-items: center;
    min-height: 28px;
  }

  .tab {
    position: relative;
    min-width: 56px;
    height: 28px;
    padding: 0 10px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    line-height: 1;
    white-space: nowrap;
    color: #669ecc;
    background: linear-gradient(180deg, rgba(9, 45, 89, 0.82) 0%, rgba(7, 28, 54, 0.82) 100%);
    border: 1px solid rgba(65, 134, 203, 0.34);
    cursor: pointer;
    user-select: none;
    flex: 0 0 auto;
  }

  .tab::after {
    content: "";
    position: absolute;
    left: 50%;
    bottom: -1px;
    width: 10px;
    height: 1px;
    background: rgba(84, 173, 255, 0.55);
    transform: translateX(-50%);
  }

  .tab.active {
    color: #01f0ff;
    background: linear-gradient(180deg, rgba(18, 95, 186, 0.95) 0%, rgba(11, 64, 122, 0.9) 100%);
    border-color: rgba(86, 205, 255, 0.45);
    box-shadow: inset 0 0 18px rgba(17, 203, 255, 0.16);
  }

  .tab.active::after {
    background: #01f0ff;
  }
</style>
