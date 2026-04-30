<template>
  <Dialog v-model="configVisible" width="720px" title="任务巡查配置">
    <div class="base-content">
      <div class="title-wrapper">
        <p class="title">区域与设备选择</p>
        <span class="summary">已选 {{ selectedDeviceIDs.length }} 路设备</span>
      </div>
      <div class="search">
        <AInput v-model:value="deviceKeyword" style="width: 100%; height: 100%" placeholder="搜索区域或设备">
          <template #suffix>
            <SearchOutlined />
          </template>
        </AInput>
      </div>
      <div class="area-select">
        <ATree
          v-model:checkedKeys="checkedTreeKeys"
          checkable
          :tree-data="filteredTreeData"
          :selectable="false"
          class="patrol-tree"
        />
      </div>

      <div class="title-wrapper">
        <p class="title">巡查算法</p>
        <span class="summary">选算法时自动使用算法当前启用提示词</span>
      </div>
      <ASelect
        v-model:value="selectedAlgorithmID"
        show-search
        allow-clear
        style="width: 100%"
        placeholder="请选择算法"
        :options="algorithmOptions.map((item) => ({ label: item.name, value: item.id }))"
        :filter-option="filterSelectOption"
      />

      <div class="title-wrapper">
        <p class="title">巡查提示词</p>
        <span class="summary">输入提示词后会自动清空算法选择</span>
      </div>
      <ATextarea
        v-model:value="customPrompt"
        placeholder="请输入任务巡查提示词，例如：检查画面里是否有人摔倒"
        :auto-size="{ minRows: 3, maxRows: 5 }"
      />

      <div class="title-wrapper">
        <p class="title">巡查时间</p>
        <span class="summary">任务巡查仅分析当前一帧</span>
      </div>
      <div class="radio-group">
        <div
          v-for="item in radioOptions"
          :key="item.value"
          class="radio-item"
          :class="{ selected: item.selected, disabled: item.disabled }"
        >
          <div class="circle"></div>
          <span class="text">{{ item.label }}</span>
        </div>
      </div>

      <div class="btn-group">
        <button class="btn cancel" :disabled="patrolSubmitting" @click="close">取消</button>
        <button class="btn" :disabled="patrolSubmitting" @click="handleSubmit">
          {{ patrolSubmitting ? '巡查中...' : '开始巡查' }}
        </button>
      </div>
    </div>
  </Dialog>
</template>

<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { Input as AInput, Select as ASelect, Textarea as ATextarea, Tree as ATree, message } from 'ant-design-vue'
import { SearchOutlined } from '@ant-design/icons-vue'

import Dialog from '../components/Dialog/index.vue'
import { fetchCamera2Algorithms, fetchCamera2AreaTree, fetchCamera2Channels, type Camera2AlgorithmOption, type Camera2AreaTreeNode, type Camera2ChannelEntity } from '../api'
import { usePatrolCenter } from '../hooks/usePatrolCenter'

type PatrolTreeNode = {
  title: any
  key: string
  label: string
  nodeType: 'area' | 'device'
  children?: PatrolTreeNode[]
}

const configVisible = ref(false)
const deviceKeyword = ref('')
const selectedAlgorithmID = ref('')
const customPrompt = ref('')
const checkedTreeKeys = ref<string[]>([])
const treeData = ref<PatrolTreeNode[]>([])
const algorithmOptions = ref<Camera2AlgorithmOption[]>([])
const cameraIconURL = new URL('../assets/images/icon-camera.png', import.meta.url).href

const { patrolSubmitting, startPatrol } = usePatrolCenter()

const radioOptions = [
  { label: '实时巡查', value: 'real_time', selected: true, disabled: false },
  { label: '近两小时', value: 'last_two_hours', selected: false, disabled: true },
  { label: '自定义', value: 'custom', selected: false, disabled: true },
]

watch(selectedAlgorithmID, (value) => {
  if (String(value || '').trim()) {
    customPrompt.value = ''
  }
})

watch(customPrompt, (value) => {
  if (String(value || '').trim()) {
    selectedAlgorithmID.value = ''
  }
})

const selectedDeviceIDs = computed(() => {
  const deviceSet = new Set<string>()
  for (const key of checkedTreeKeys.value) {
    const normalized = String(key || '').trim()
    if (normalized.startsWith('device:')) {
      deviceSet.add(normalized.slice('device:'.length))
    }
  }
  return Array.from(deviceSet)
})

const filteredTreeData = computed(() => {
  const keyword = String(deviceKeyword.value || '').trim().toLowerCase()
  if (!keyword) {
    return treeData.value
  }
  return filterTreeNodes(treeData.value, keyword)
})

async function loadOptions() {
  const [areas, nextAlgorithms, nextChannels] = await Promise.all([
    fetchCamera2AreaTree().catch(() => []),
    fetchCamera2Algorithms().catch(() => []),
    fetchCamera2Channels().catch(() => []),
  ])
  algorithmOptions.value = nextAlgorithms
  treeData.value = buildAreaDeviceTree(areas, nextChannels)
}

async function open() {
  configVisible.value = true
  deviceKeyword.value = ''
  selectedAlgorithmID.value = ''
  customPrompt.value = ''
  checkedTreeKeys.value = []
  await loadOptions()
}

function close() {
  configVisible.value = false
}

function filterSelectOption(input: string, option?: { label?: string | number }) {
  return String(option?.label || '').toLowerCase().includes(String(input || '').toLowerCase())
}

async function handleSubmit() {
  if (selectedDeviceIDs.value.length === 0) {
    message.error('请至少选择一路设备')
    return
  }
  const prompt = String(customPrompt.value || '').trim()
  const algorithmID = String(selectedAlgorithmID.value || '').trim()
  if (!algorithmID && !prompt) {
    message.error('请选择算法或输入巡查提示词')
    return
  }
  if (algorithmID && prompt) {
    message.error('算法和提示词只能二选一')
    return
  }
  try {
    await startPatrol({
      device_ids: selectedDeviceIDs.value,
      algorithm_id: algorithmID || undefined,
      prompt: prompt || undefined,
    })
    close()
  } catch (error) {
    message.error((error as Error).message || '创建任务巡查失败')
  }
}

function buildAreaDeviceTree(areas: Camera2AreaTreeNode[], nextChannels: Camera2ChannelEntity[]): PatrolTreeNode[] {
  const deviceNodesByArea = new Map<string, PatrolTreeNode[]>()
  const unassignedDevices: PatrolTreeNode[] = []

  nextChannels.forEach((item) => {
    const label = String(item.name || item.id || '未命名设备')
    const node: PatrolTreeNode = {
      title: buildTreeNodeTitle(label, 'device'),
      key: `device:${String(item.id || '').trim()}`,
      label,
      nodeType: 'device',
    }
    const areaID = String(item.area_id || '').trim()
    if (!areaID) {
      unassignedDevices.push(node)
      return
    }
    const current = deviceNodesByArea.get(areaID) || []
    current.push(node)
    deviceNodesByArea.set(areaID, current)
  })

  const normalizeNodes = (items: Camera2AreaTreeNode[]): PatrolTreeNode[] => {
    const out: PatrolTreeNode[] = []
    items.forEach((item) => {
      const areaID = String(item.id || '').trim()
      const areaChildren = normalizeNodes(Array.isArray(item.children) ? item.children : [])
      const deviceChildren = [...(deviceNodesByArea.get(areaID) || [])]
      const children = [...areaChildren, ...deviceChildren]
      if (item.is_root) {
        out.push(...children)
        return
      }
      const label = String(item.name || item.id || '未命名区域')
      out.push({
        title: buildTreeNodeTitle(label, 'area'),
        key: `area:${areaID}`,
        label,
        nodeType: 'area',
        children,
      })
    })
    return out
  }

  const areaNodes = normalizeNodes(areas)
  if (unassignedDevices.length > 0) {
    areaNodes.push({
      title: buildTreeNodeTitle('未分配区域', 'area'),
      key: 'area:__unassigned__',
      label: '未分配区域',
      nodeType: 'area',
      children: unassignedDevices,
    })
  }
  return areaNodes
}

function filterTreeNodes(nodes: PatrolTreeNode[], keyword: string): PatrolTreeNode[] {
  const matchedNodes: PatrolTreeNode[] = []
  nodes.forEach((node) => {
    const title = String(node.label || '').toLowerCase()
    const children = Array.isArray(node.children) ? filterTreeNodes(node.children, keyword) : []
    if (title.includes(keyword) || children.length > 0) {
      matchedNodes.push({
        ...node,
        children,
      })
    }
  })
  return matchedNodes
}

function buildTreeNodeTitle(label: string, nodeType: PatrolTreeNode['nodeType']) {
  return h(
    'span',
    { class: ['tree-node', `tree-node-${nodeType}`] },
    [
      h('span', { class: 'tree-node-badge' }, nodeType === 'area' ? '区域' : '设备'),
      h('span', { class: 'tree-node-label' }, label),
      nodeType === 'device'
        ? h('img', { class: 'tree-node-icon', src: cameraIconURL, alt: '摄像头' })
        : null,
    ],
  )
}

defineExpose({
  open,
  close,
})
</script>

<style scoped lang="less">
.base-content {
  display: flex;
  width: 100%;
  flex-direction: column;
  gap: 14px;

  .title-wrapper {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;

    .title {
      width: 320px;
      height: 29px;
      background-image: url('../assets/images/config-bar.png');
      background-size: cover;
      display: flex;
      align-items: center;
      font-size: 20px;
      font-weight: 600;
      color: #f1f8ff;
      padding-left: 32px;
    }

    .summary {
      color: #9bc7ef;
      font-size: 13px;
    }
  }

  .search {
    width: 100%;
    height: 36px;
  }

  .area-select {
    width: 100%;
    border: 1px solid #3b79bf;
    border-radius: 4px;
    padding: 14px;
    height: 180px;
    background-color: #021536;
    overflow: auto;
  }

  .radio-group {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: flex-start;
    gap: 18px;

    .radio-item {
      display: flex;
      align-items: center;
      justify-content: flex-start;
      gap: 8px;
      color: #fff;

      .circle {
        width: 16px;
        height: 16px;
        border-radius: 50%;
        background-color: transparent;
        position: relative;
        border: 1px solid #fff;
      }

      .text {
        font-size: 14px;
        font-weight: 400;
      }

      &.selected {
        color: #dff6ff;

        .circle {
          border-color: #59d5ff;
          box-shadow: 0 0 10px rgba(89, 213, 255, 0.32);

          &::after {
            content: '';
            position: absolute;
            inset: 3px;
            border-radius: 50%;
            background: #59d5ff;
          }
        }
      }

      &.disabled {
        opacity: 0.45;
        cursor: not-allowed;
      }
    }
  }

  .btn-group {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 28px;

    .btn {
      width: 120px;
      height: 38px;
      background-image: url('../assets/images/btn1.png');
      background-size: cover;
      border: none;
      cursor: pointer;
      color: #fff;
      font-size: 16px;

      &:disabled {
        cursor: not-allowed;
        opacity: 0.7;
      }

      &.cancel {
        background-image: url('../assets/images/btn2.png');
      }
    }
  }
}

:deep(.patrol-tree .ant-tree-checkbox + span) {
  color: #d8ebff;
}

:deep(.patrol-tree .ant-tree-treenode) {
  padding: 2px 0;
}

:deep(.patrol-tree .tree-node) {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

:deep(.patrol-tree .tree-node-badge) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 34px;
  height: 18px;
  padding: 0 6px;
  border-radius: 999px;
  font-size: 11px;
  line-height: 1;
  color: #dff4ff;
  background: rgba(67, 130, 196, 0.4);
  border: 1px solid rgba(118, 191, 255, 0.35);
}

:deep(.patrol-tree .tree-node-area .tree-node-badge) {
  background: rgba(47, 104, 175, 0.4);
}

:deep(.patrol-tree .tree-node-device .tree-node-badge) {
  background: rgba(24, 140, 198, 0.4);
}

:deep(.patrol-tree .tree-node-label) {
  color: #d8ebff;
}

:deep(.patrol-tree .tree-node-icon) {
  width: 14px;
  height: 14px;
  object-fit: contain;
  filter: drop-shadow(0 0 6px rgba(83, 196, 255, 0.35));
}
</style>
