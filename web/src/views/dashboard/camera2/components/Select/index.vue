<template>
  <div class="select-wrapper">
    <select class="filter-select" v-model="value" @change="handleChange">
      <option v-for="item in options" :key="item.value" :value="item.value">{{ item.label }}</option>
    </select>
    <span class="custom-arrow">▼</span>
  </div>
</template>
<script setup>
  const value = defineModel();
  const props = defineProps({
    options: {
      type: Array,
      default: () => [],
    }
  });
  const emit = defineEmits(["change"]);
  const handleChange = (e) => {
    emit("change", e.target.value);
  }
</script>

<style scoped lang="less">
  .select-wrapper {
    position: relative;
    display: inline-block;
    .custom-arrow {
      position: absolute;
      right: 10px;
      top: 50%;
      transform: translateY(-50%);
      pointer-events: none; /* 让点击穿透到 select */
      color: #9CC0E8;
      font-size: 10px;
    }
    .filter-select {
      min-width: 124px;
      height: 28px;
      color: #a0c2e9;
      font-size: 14px;
      outline: none;
      border-radius: 4px;
      border: 1px solid #A0C2E9;
      background-color: transparent;
      padding: 0 8px;
      appearance: none;
      -webkit-appearance: none;
      -moz-appearance: none;
      /* 样式化option元素 */
      option {
        color: #A0C2E9;
        background-color: #1a1a2e;
        padding: 8px 12px;
        font-size: 14px;
      }

      /* 样式化选中的option */
      option:checked {
        background-color: #16213e;
      }

      /* 样式化hover状态 */
      option:hover {
        background-color: #0f3460;
      }
    }
  }
</style>
