<template>
  <div class="panel-title">
    <div class="title-text">{{ title }}</div>
    <div class="extra-content">
      <slot></slot>
    </div>
  </div>
</template>

<script setup lang="ts">
// 定义组件属性
defineProps<{
  title: string
}>()
</script>

<style lang="less" scoped>
.panel-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 40px;
  background-image: url('@/assets/dashboard/panel-title-bg.png');
  background-repeat: no-repeat;
  background-size: 100% 100%;
  margin-bottom: 10px;
  padding-left: 50px;
  position: relative;
  
  .title-text {
    font-size: 20px;
    font-weight: bold;
    color: #fff;
    text-shadow: 0 0 10px rgba(0, 210, 255, 0.5);
    letter-spacing: 1px;
    line-height: 40px;
    padding-bottom: 12px;;
  }

  .extra-content {
    display: flex;
    align-items: center;
  }
}
</style>
