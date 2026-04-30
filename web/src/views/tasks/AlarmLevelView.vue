<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import { alarmLevelAPI } from '@/api/modules'

type AlarmLevel = {
  id: string
  name: string
  severity: number
  color: string
  description: string
}

const loading = ref(false)
const levels = ref<AlarmLevel[]>([])
const levelModal = ref(false)
const editingLevelID = ref('')
const levelForm = reactive({
  name: '',
  severity: 1,
  color: '#faad14',
  description: '',
})

function normalizeHexColor(value: string): string {
  const raw = String(value || '').trim()
  if (/^#[0-9a-fA-F]{6}$/.test(raw)) return raw.toLowerCase()
  if (/^[0-9a-fA-F]{6}$/.test(raw)) return `#${raw.toLowerCase()}`
  return '#faad14'
}

async function loadLevels() {
  loading.value = true
  try {
    const data = await alarmLevelAPI.list() as { items: AlarmLevel[] }
    levels.value = (data.items || []).sort((x, y) => x.severity - y.severity)
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}

function openEditLevel(row: AlarmLevel) {
  editingLevelID.value = row.id
  Object.assign(levelForm, {
    name: row.name,
    severity: row.severity,
    color: normalizeHexColor(row.color),
    description: row.description || '',
  })
  levelModal.value = true
}

async function submitLevel() {
  try {
    if (!editingLevelID.value) return
    levelForm.color = normalizeHexColor(levelForm.color)
    await alarmLevelAPI.update(editingLevelID.value, {
      name: levelForm.name,
      color: levelForm.color,
      description: levelForm.description,
    })
    message.success('告警等级已更新')
    levelModal.value = false
    await loadLevels()
  } catch (err) {
    message.error((err as Error).message)
  }
}

onMounted(loadLevels)
</script>

<template>
  <div>
    <h2 class="page-title">报警等级管理</h2>
    <p class="page-subtitle">内置 3 级报警等级（低、中、高），仅支持编辑名称、颜色和描述。</p>

    <a-card class="glass-card">
      <div class="table-toolbar">
        <div />
        <a-space>
          <a-button @click="loadLevels">刷新</a-button>
        </a-space>
      </div>
      <a-table :data-source="levels" row-key="id" :loading="loading" :pagination="false">
        <a-table-column title="名称" data-index="name" />
        <a-table-column title="级别" data-index="severity" width="100" />
        <a-table-column title="颜色" width="120">
          <template #default="{ record }">
            <a-tag :color="record.color">{{ record.color }}</a-tag>
          </template>
        </a-table-column>
        <a-table-column title="描述" data-index="description" />
        <a-table-column title="操作" width="120">
          <template #default="{ record }">
            <a-button size="small" @click="openEditLevel(record)">编辑</a-button>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal v-model:open="levelModal" title="编辑告警等级" @ok="submitLevel">
      <a-form layout="vertical">
        <a-form-item label="名称"><a-input v-model:value="levelForm.name" /></a-form-item>
        <a-form-item label="级别"><a-input-number v-model:value="levelForm.severity" :min="1" :max="3" style="width: 100%" disabled /></a-form-item>
        <a-form-item label="颜色">
          <a-space>
            <input v-model="levelForm.color" class="native-color-input" type="color" />
            <a-input :value="levelForm.color.toUpperCase()" readonly style="width: 120px" />
          </a-space>
        </a-form-item>
        <a-form-item label="描述"><a-textarea v-model:value="levelForm.description" :rows="2" /></a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<style scoped>
.native-color-input {
  width: 44px;
  height: 32px;
  padding: 0;
  border: none;
  background: transparent;
  cursor: pointer;
}
</style>
