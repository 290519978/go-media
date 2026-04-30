<template>
  <Dialog v-model="configVisible" width="648px" title="删除算法确认">
    <div class="base-content">
      
      <div class="tooltip">
        <img src="../assets/images/warning-big.png" class="icon" alt="">
        <p class="title">确定要删除该算法吗?</p>
        <p class="desc">删除后将停止对该摄像头的烟火识别算法分析，且无法恢复。</p>
      </div>
      <div class="btn-group">
        <button class="btn cancel">取消</button>
        <button class="btn">确认删除</button>
      </div>

    </div>
  </Dialog>
</template>
<script setup>
  import { ref } from "vue";
  import Dialog from "../components/Dialog/index.vue";
   const emits = defineEmits(["handler"]);

  const configVisible = ref(false);
  const open = () => {
    configVisible.value = true;
  };
  const close = () => {
    configVisible.value = false;
  };
  defineExpose({
    open,
    close,
  });

  const list = ref([
    {
      title: "烟火识别算法",
      meta: "检测频率:500ms/次 | 灵敏度:中",
    },
    {
      title: "算法2",
      meta: "检测频率:500ms/次 | 灵敏度:中",
    },
    {
      title: "算法3",
      meta: "检测频率:500ms/次 | 灵敏度:中",
    },
    {
      title: "算法4",
      meta: "检测频率:500ms/次 | 灵敏度:中",
    },
    {
      title: "算法5",
      meta: "检测频率:500ms/次 | 灵敏度:中",
    },
    {
      title: "算法6",
      meta: "检测频率:500ms/次 | 灵敏度:中",
    },
  ])

</script>

<style scoped lang="less">
  .base-content {
    display: flex;
    width: 100%;
    flex-direction: column;
    gap: 14px;
   
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
        &.cancel {
          background-image: url("../assets/images/btn2.png");
        }
      }
    }
    .tooltip {
      width: 100%;
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: 12px;
      margin-bottom: 24px;
      .icon {
        width: 88px;
        height: 82px;
      }
      .title {
        font-size: 16px;
        font-weight: 600;
        color: #fff;
      }
      .desc {
        font-size: 14px;
        font-weight: 400;
        color: #7BA0CA;
      }
    }
  }
</style>
