<template>
  <div class="input-wrapper" :style="{ width: width }">
    <input
      :value="modelValue"
      :placeholder="placeholder"
      @input="$emit('update:modelValue', $event.target.value)"
      @keyup.enter="onSearch"
      class="my-input"
      v-bind="$attrs" />
    <slot>
      <img :src="icon" alt="search-icon" class="search-icon2" />
    </slot>
  </div>
</template>
<script setup>
  import icon from './icon.svg'
  const props = defineProps({
    modelValue: String,
    placeholder: String,
    width: {
      type: String,
      default: '100%',
    },
  });
  const emit = defineEmits(["update:modelValue", "search"]);
  const onSearch = () => {
    emit("search", props.modelValue);
  };
</script>
<style scoped lang="less">
  .input-wrapper {
    display: flex;
    align-items: center;
    background-color: transparent;
    border: 1px solid #A0C2E9;
    border-radius: 4px;
    height: 28px;
    min-width: 120px;
    input {
      width: 0;
      flex-grow: 1;
      background-color: transparent;
      border: none;
      outline: none;
      color: #A0C2E9;
      font-size: 14px;
      padding: 0 8px;
      height: 26px;
      &::placeholder {
        color: #a0c2e9;
      }
    }
    .search-icon2 {
      width: 14px;
      height: 14px;
      flex-shrink: 0;
      margin-right: 8px;
    }
  }
</style>
