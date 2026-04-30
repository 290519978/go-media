<template>
  <div v-if="dialogVisible" class="dialog" :style="{ zIndex: zIndex }">
    <div class="dialog-wrapper" :style="{ width: width }">
      <i class="before"></i>

      <div class="dialog-container">
        <div class="dialog-header">
          <div class="dialog-title">{{ title }}</div>
          <div class="dialog-close" @click="handleCancel">
            <img :src="closeIcon" alt="close-icon" class="close-icon" />
          </div>
        </div>
        <div class="dialog-content">
          <slot></slot>
        </div>
        <div class="dialog-footer">

        </div>
      </div>
      <i class="after"></i>
    </div>
  </div>

  
  
  
</template>
<script setup>
  import { ref, watch } from 'vue';
import closeIcon from '../../assets/images/close.png'
import { useDialogZIndex } from '../../hooks/useDialogZIndex'
const { getNextZIndex, resetZIndex, currentZIndex } = useDialogZIndex()
  const props = defineProps({
    title: {
      type: String,
      default: "提示",
    },
    width: {
      type: String,
      default: "max-content",
    }
  });
  const zIndex = ref(0)
  const dialogVisible = defineModel({
    type: Boolean,
    default: false,
  })
  // 打开时获取最新 zIndex
  watch(dialogVisible, (val) => {
    if (val) {
      zIndex.value = getNextZIndex()
    }
  })

  const handleCancel = () => {
    dialogVisible.value = false;
  };
</script>
<style scoped lang="less">
  .dialog {
    position: fixed;
    width: 100%;
    height: 100%;
    top: 0;
    left: 0;
    background-color: rgba(0,0,0,0.5);
    overflow-y: auto;
    padding-top: 6%;
    padding-bottom: 6%;
    box-sizing: border-box;
    text-align: center;
    white-space: nowrap;
    &::before {
      content: '';
      display: inline-block;
      height: 100%;
      vertical-align: middle;
    }
    .dialog-wrapper {
      position: relative;
      display: inline-block;
      vertical-align: middle;
      text-align: left;
      white-space: normal;
      

      .before {
        width: 50px;
        height: 5px;
        background-color: #01acfc;
        margin-bottom: 5px;
      }
      .after {
        width: 50px;
        height: 5px;
        background-image: linear-gradient(45deg, #00ACFC00, #00ACFC00 20%,  #01acfc 20%);
        align-self: flex-end;
      }
      .dialog-container {
        width: 100%;
        display: flex;
        flex-direction: column;
        border: 1px solid #00ACFC;
        box-shadow: 0px -9px 35.8px 0px #1A56A5BF inset;


        .dialog-header {
          width: 100%;
          padding: 10px 16px;
          display: flex;
          align-items: center;
          justify-content: space-between;
          background-size: 100% 100%;
          background-position: center;
          background-color: #022566e0;
          background-image: url('../../assets/images/dialog-header.png');
          .dialog-title {
            font-size: 28px;
            font-weight: bold;
            color: #fff;
            font-family: 'YouSheBiaoTiHeiTi';
          }
          .dialog-close {
            width: 20px;
            height: 20px;
            cursor: pointer;
            img {
              width: 100%;
              height: 100%;
            }
          }
        }
        .dialog-content {
          padding: 20px;
          background-color: #022566e0;
          
        }
      }
    }
  }
</style>
