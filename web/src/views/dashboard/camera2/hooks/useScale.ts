import { computed, onMounted, onUnmounted, ref } from "vue";

export const useScale = (key: string = "--screen-scale") => {
  // 设计稿宽度
  const DESIGN_WIDTH = 1920;
  // 设计稿高度
  const DESIGN_HEIGHT = 1080;
  // 安全区域边距
  const SAFE_MARGIN = 0;

  // 视口宽度
  const viewportWidth = ref(window.innerWidth);
  // 视口高度
  const viewportHeight = ref(window.innerHeight);

  // 更新视口宽度和高度
  const updateViewport = () => {
    viewportWidth.value = window.innerWidth;
    viewportHeight.value = window.innerHeight;
  };

  // 计算缩放比例
  const scale = computed(() => {
    const availableWidth = Math.max(viewportWidth.value - SAFE_MARGIN, 320);
    const availableHeight = Math.max(viewportHeight.value - SAFE_MARGIN, 320);

    return Math.min(availableWidth / DESIGN_WIDTH, availableHeight / DESIGN_HEIGHT);
  });

  // 计算舞台样式
  const stageStyle = computed(() => ({
    width: `${Math.round(DESIGN_WIDTH * scale.value)}px`,
    height: `${Math.round(DESIGN_HEIGHT * scale.value)}px`,
    [key]: `${scale.value}`,
  }));

  onMounted(() => {
    updateViewport();
    window.addEventListener("resize", updateViewport);
  });

  onUnmounted(() => {
    window.removeEventListener("resize", updateViewport);
  });

  return {
    stageStyle,
  }
};
