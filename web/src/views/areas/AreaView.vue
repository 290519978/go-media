<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import { areaAPI } from '@/api/modules'
import { useAuthStore } from '@/stores/auth'

type Area = {
  id: string
  parent_id: string
  name: string
  is_root: boolean
  sort: number
  children?: Area[]
}

const loading = ref(false)
const treeAreas = ref<Area[]>([])
const flatAreas = ref<Area[]>([])
const authStore = useAuthStore()
const isDevelopmentMode = computed(() => authStore.developmentMode)
const modalOpen = ref(false)
const editingID = ref('')
const form = reactive({
  name: '',
  parent_id: 'root',
  sort: 0,
})

const parentOptions = computed(() => flatAreas.value.map((item) => ({ label: item.name, value: item.id })))

function findArea(id: string, items: Area[]): Area | null {
  for (const item of items) {
    if (item.id === id) return item
    const hit = findArea(id, item.children || [])
    if (hit) return hit
  }
  return null
}

function parentName(parentID: string) {
  if (!parentID) return '-'
  if (parentID === 'root') return '根节点'
  const hit = flatAreas.value.find((item) => item.id === parentID)
  return hit?.name || parentID
}

async function load() {
  loading.value = true
  try {
    const data = await areaAPI.list() as { items: Area[]; flat: Area[] }
    treeAreas.value = data.items || []
    flatAreas.value = data.flat || []
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}

function openCreate(parentID = 'root') {
  editingID.value = ''
  form.name = ''
  form.parent_id = parentID || 'root'
  form.sort = 0
  modalOpen.value = true
}

function openEdit(item: Area) {
  editingID.value = item.id
  form.name = item.name
  form.parent_id = item.parent_id || 'root'
  form.sort = item.sort
  modalOpen.value = true
}

async function submit() {
  try {
    if (!form.name.trim()) {
      message.warning('区域名称不能为空')
      return
    }
    if (editingID.value && form.parent_id === editingID.value) {
      message.warning('父区域不能选择自己')
      return
    }
    if (editingID.value) {
      await areaAPI.update(editingID.value, form)
      message.success('区域已更新')
    } else {
      await areaAPI.create(form)
      message.success('区域已创建')
    }
    modalOpen.value = false
    await load()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function remove(id: string) {
  try {
    await areaAPI.remove(id)
    message.success('区域已删除')
    await load()
  } catch (err) {
    message.error((err as Error).message)
  }
}

function openCreateChild(id: string) {
  const node = findArea(id, treeAreas.value)
  if (!node) {
    openCreate('root')
    return
  }
  openCreate(node.id)
}

onMounted(load)
</script>

<template>
  <div>
    <h2 class="page-title">区域管理</h2>
    <p class="page-subtitle">树形区域结构。根节点可编辑但不可删除。</p>

    <a-card class="glass-card">
      <div class="table-toolbar">
        <a-space>
          <a-button type="primary" @click="openCreate('root')">新增区域</a-button>
        </a-space>
        <a-space>
          <a-button @click="load">刷新</a-button>
        </a-space>
      </div>

      <a-table
        :loading="loading"
        :data-source="treeAreas"
        :pagination="false"
        row-key="id"
        size="middle"
        :default-expand-all-rows="true"
      >
        <a-table-column title="名称" data-index="name" />
        <a-table-column v-if="isDevelopmentMode" title="ID" data-index="id" />
        <a-table-column title="父级区域">
          <template #default="{ record }">{{ parentName(record.parent_id) }}</template>
        </a-table-column>
        <a-table-column title="排序" data-index="sort" width="90" />
        <a-table-column title="根节点" width="90">
          <template #default="{ record }">
            <a-tag :color="record.is_root ? 'green' : 'default'">{{ record.is_root ? '是' : '否' }}</a-tag>
          </template>
        </a-table-column>
        <a-table-column title="操作" width="260">
          <template #default="{ record }">
            <a-space>
              <a-button size="small" @click="openCreateChild(record.id)">新增子区域</a-button>
              <a-button size="small" @click="openEdit(record)">编辑</a-button>
              <a-popconfirm
                title="确定删除该区域？"
                ok-text="删除"
                cancel-text="取消"
                @confirm="remove(record.id)"
                :disabled="record.is_root"
              >
                <a-button size="small" danger :disabled="record.is_root">删除</a-button>
              </a-popconfirm>
            </a-space>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal v-model:open="modalOpen" :title="editingID ? '编辑区域' : '新增区域'" @ok="submit">
      <a-form layout="vertical">
        <a-form-item label="名称" required>
          <a-input v-model:value="form.name" />
        </a-form-item>
        <a-form-item label="父级区域">
          <a-select v-model:value="form.parent_id" :options="parentOptions" />
        </a-form-item>
        <a-form-item label="排序">
          <a-input-number v-model:value="form.sort" :min="0" style="width: 100%" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
