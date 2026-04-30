<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import { systemAPI } from '@/api/modules'
import { formatDateTime } from '@/utils/datetime'

type User = {
  id: string
  username: string
  enabled: boolean
  created_at: string
}

type Role = {
  id: string
  name: string
}

const loading = ref(false)
const users = ref<User[]>([])
const roles = ref<Role[]>([])

const userModal = ref(false)
const editingUserID = ref('')
const userForm = reactive({ username: '', password: '', enabled: true })

const userRoleModal = ref(false)
const assignUser = ref<User | null>(null)
const selectedRoleIDs = ref<string[]>([])

const roleOptions = computed(() => roles.value.map((item) => ({ label: item.name, value: item.id })))

async function load() {
  loading.value = true
  try {
    const [u, r] = await Promise.all([
      systemAPI.users() as Promise<{ items: User[] }>,
      systemAPI.roles() as Promise<{ items: Role[] }>,
    ])
    users.value = u.items || []
    roles.value = r.items || []
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}

function openCreateUser() {
  editingUserID.value = ''
  Object.assign(userForm, { username: '', password: '', enabled: true })
  userModal.value = true
}

function openEditUser(row: User) {
  editingUserID.value = row.id
  Object.assign(userForm, { username: row.username, password: '', enabled: row.enabled })
  userModal.value = true
}

async function submitUser() {
  try {
    if (editingUserID.value) {
      await systemAPI.updateUser(editingUserID.value, userForm)
      message.success('用户已更新')
    } else {
      if (!userForm.password) {
        message.warning('新建用户必须填写密码')
        return
      }
      await systemAPI.createUser(userForm)
      message.success('用户已创建')
    }
    userModal.value = false
    await load()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function removeUser(id: string) {
  try {
    await systemAPI.removeUser(id)
    message.success('用户已删除')
    await load()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function openUserRoles(row: User) {
  assignUser.value = row
  try {
    const data = await systemAPI.userRoles(row.id) as { role_ids: string[] }
    selectedRoleIDs.value = data.role_ids || []
    userRoleModal.value = true
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function saveUserRoles() {
  if (!assignUser.value) return
  try {
    await systemAPI.setUserRoles(assignUser.value.id, selectedRoleIDs.value)
    message.success('用户角色已更新')
    userRoleModal.value = false
  } catch (err) {
    message.error((err as Error).message)
  }
}

onMounted(load)
</script>

<template>
  <div>
    <h2 class="page-title">用户管理</h2>
    <p class="page-subtitle">维护用户账号、启用状态和角色分配。</p>

    <a-card class="glass-card">
      <div class="table-toolbar">
        <a-button type="primary" @click="openCreateUser">新增用户</a-button>
        <a-button @click="load">刷新</a-button>
      </div>
      <a-table :data-source="users" row-key="id" :loading="loading" :pagination="{ pageSize: 10 }">
        <a-table-column title="用户名" data-index="username" />
        <a-table-column title="启用">
          <template #default="{ record }">
            <a-tag :color="record.enabled ? 'green' : 'default'">{{ record.enabled ? '是' : '否' }}</a-tag>
          </template>
        </a-table-column>
        <a-table-column title="创建时间">
          <template #default="{ record }">{{ formatDateTime(record.created_at) }}</template>
        </a-table-column>
        <a-table-column title="操作" width="260">
          <template #default="{ record }">
            <a-space>
              <a-button size="small" @click="openEditUser(record)">编辑</a-button>
              <a-button size="small" @click="openUserRoles(record)">分配角色</a-button>
              <a-popconfirm title="确定删除该用户？" @confirm="removeUser(record.id)">
                <a-button size="small" danger>删除</a-button>
              </a-popconfirm>
            </a-space>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal v-model:open="userModal" :title="editingUserID ? '编辑用户' : '新增用户'" @ok="submitUser">
      <a-form layout="vertical">
        <a-form-item label="用户名"><a-input v-model:value="userForm.username" /></a-form-item>
        <a-form-item :label="editingUserID ? '密码（留空则不修改）' : '密码'">
          <a-input-password v-model:value="userForm.password" />
        </a-form-item>
        <a-form-item label="启用"><a-switch v-model:checked="userForm.enabled" /></a-form-item>
      </a-form>
    </a-modal>

    <a-modal v-model:open="userRoleModal" title="分配用户角色" @ok="saveUserRoles">
      <a-form layout="vertical">
        <a-form-item label="用户">
          <a-input :value="assignUser?.username || ''" disabled />
        </a-form-item>
        <a-form-item label="角色">
          <a-select v-model:value="selectedRoleIDs" mode="multiple" :options="roleOptions" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
