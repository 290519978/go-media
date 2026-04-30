<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import { systemAPI } from '@/api/modules'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/datetime'

type Role = {
  id: string
  name: string
  remark: string
  created_at: string
}

type Menu = {
  id: string
  name: string
  path: string
  menu_type: string
}

const authStore = useAuthStore()
const loading = ref(false)
const roles = ref<Role[]>([])
const menus = ref<Menu[]>([])

const roleModal = ref(false)
const editingRoleID = ref('')
const roleForm = reactive({ name: '', remark: '' })

const roleMenuModal = ref(false)
const assignRole = ref<Role | null>(null)
const selectedMenuIDs = ref<string[]>([])

const menuOptions = computed(() =>
  menus.value.map((item) => ({
    label: `${item.name} [${item.menu_type === 'directory' ? '目录' : '菜单'}] ${item.path ? `(${item.path})` : ''}`,
    value: item.id,
  })),
)

async function load() {
  loading.value = true
  try {
    const [r, ms] = await Promise.all([
      systemAPI.roles() as Promise<{ items: Role[] }>,
      systemAPI.menus() as Promise<{ items: Menu[] }>,
    ])
    roles.value = r.items || []
    menus.value = ms.items || []
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}

async function refreshCurrentUserMenus() {
  try {
    await authStore.refreshMe()
  } catch {
    // 忽略刷新失败
  }
}

function openCreateRole() {
  editingRoleID.value = ''
  Object.assign(roleForm, { name: '', remark: '' })
  roleModal.value = true
}

function openEditRole(row: Role) {
  editingRoleID.value = row.id
  Object.assign(roleForm, { name: row.name, remark: row.remark || '' })
  roleModal.value = true
}

async function submitRole() {
  try {
    if (editingRoleID.value) {
      await systemAPI.updateRole(editingRoleID.value, roleForm)
      message.success('角色已更新')
    } else {
      await systemAPI.createRole(roleForm)
      message.success('角色已创建')
    }
    roleModal.value = false
    await load()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function removeRole(id: string) {
  try {
    await systemAPI.removeRole(id)
    message.success('角色已删除')
    await load()
    await refreshCurrentUserMenus()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function openRoleMenus(row: Role) {
  assignRole.value = row
  try {
    const data = await systemAPI.roleMenus(row.id) as { menu_ids: string[] }
    selectedMenuIDs.value = data.menu_ids || []
    roleMenuModal.value = true
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function saveRoleMenus() {
  if (!assignRole.value) return
  try {
    await systemAPI.setRoleMenus(assignRole.value.id, selectedMenuIDs.value)
    message.success('角色菜单已更新')
    roleMenuModal.value = false
    await refreshCurrentUserMenus()
  } catch (err) {
    message.error((err as Error).message)
  }
}

onMounted(load)
</script>

<template>
  <div>
    <h2 class="page-title">角色管理</h2>
    <p class="page-subtitle">维护角色并分配可访问的目录/菜单权限。</p>

    <a-card class="glass-card">
      <div class="table-toolbar">
        <a-button type="primary" @click="openCreateRole">新增角色</a-button>
        <a-button @click="load">刷新</a-button>
      </div>
      <a-table :data-source="roles" row-key="id" :loading="loading" :pagination="{ pageSize: 10 }">
        <a-table-column title="名称" data-index="name" />
        <a-table-column title="备注" data-index="remark" />
        <a-table-column title="创建时间">
          <template #default="{ record }">{{ formatDateTime(record.created_at) }}</template>
        </a-table-column>
        <a-table-column title="操作" width="260">
          <template #default="{ record }">
            <a-space>
              <a-button size="small" @click="openEditRole(record)">编辑</a-button>
              <a-button size="small" @click="openRoleMenus(record)">分配菜单</a-button>
              <a-popconfirm title="确定删除该角色？" @confirm="removeRole(record.id)">
                <a-button size="small" danger>删除</a-button>
              </a-popconfirm>
            </a-space>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal v-model:open="roleModal" :title="editingRoleID ? '编辑角色' : '新增角色'" @ok="submitRole">
      <a-form layout="vertical">
        <a-form-item label="角色名"><a-input v-model:value="roleForm.name" /></a-form-item>
        <a-form-item label="备注"><a-input v-model:value="roleForm.remark" /></a-form-item>
      </a-form>
    </a-modal>

    <a-modal v-model:open="roleMenuModal" title="分配角色菜单" @ok="saveRoleMenus" width="720px">
      <a-form layout="vertical">
        <a-form-item label="角色">
          <a-input :value="assignRole?.name || ''" disabled />
        </a-form-item>
        <a-form-item label="菜单">
          <a-select v-model:value="selectedMenuIDs" mode="multiple" :options="menuOptions" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
