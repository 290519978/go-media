<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { message } from 'ant-design-vue'
import { systemAPI } from '@/api/modules'
import { useAuthStore } from '@/stores/auth'

type MenuType = 'directory' | 'menu'

type Menu = {
  id: string
  name: string
  path: string
  menu_type: MenuType
  view_path: string
  icon: string
  parent_id: string
  sort: number
}

type MenuTreeItem = Menu & {
  children?: MenuTreeItem[]
}

const MENU_TYPE_OPTIONS: Array<{ label: string; value: MenuType }> = [
  { label: '目录', value: 'directory' },
  { label: '菜单', value: 'menu' },
]

const MENU_ICON_OPTIONS = [
  { label: '默认', value: '' },
  { label: '应用', value: 'AppstoreOutlined' },
  { label: '区域', value: 'ClusterOutlined' },
  { label: '设备', value: 'CameraOutlined' },
  { label: '算法', value: 'NodeIndexOutlined' },
  { label: '任务', value: 'SafetyCertificateOutlined' },
  { label: '告警', value: 'AlertOutlined' },
  { label: '设置', value: 'SettingOutlined' },
]

const authStore = useAuthStore()
const loading = ref(false)
const menus = ref<Menu[]>([])

const menuModal = ref(false)
const editingMenuID = ref('')
const menuForm = reactive({
  name: '',
  path: '',
  menu_type: 'menu' as MenuType,
  view_path: '',
  icon: '',
  parent_id: '',
  sort: 0,
})

const parentMenuOptions = computed(() => [
  { label: '无', value: '' },
  ...menus.value
    .filter((item) => item.id !== editingMenuID.value)
    .map((item) => ({ label: `${item.name} [${menuTypeLabel(item.menu_type)}]`, value: item.id })),
])

const parentNameMap = computed(() => {
  const map = new Map<string, string>()
  for (const item of menus.value) {
    map.set(item.id, item.name)
  }
  return map
})

const menuTreeData = computed<MenuTreeItem[]>(() => {
  const nodeMap = new Map<string, MenuTreeItem>()
  for (const menu of menus.value) {
    nodeMap.set(menu.id, {
      ...menu,
      children: [],
    })
  }

  const roots: MenuTreeItem[] = []
  for (const node of nodeMap.values()) {
    const parent = nodeMap.get(String(node.parent_id || ''))
    if (parent && parent.id !== node.id) {
      parent.children!.push(node)
    } else {
      roots.push(node)
    }
  }

  const sortTree = (items: MenuTreeItem[]) => {
    items.sort((a, b) => a.sort - b.sort || a.name.localeCompare(b.name, 'zh-CN'))
    for (const item of items) {
      if (item.children?.length) {
        sortTree(item.children)
      }
    }
  }
  sortTree(roots)
  return roots
})

const menuNeedsPage = computed(() => menuForm.menu_type === 'menu')

watch(
  () => menuForm.menu_type,
  (value) => {
    if (value === 'directory') {
      menuForm.view_path = ''
    }
  },
)

function menuTypeLabel(v: string) {
  return v === 'directory' ? '目录' : '菜单'
}

function parentMenuName(parentID: string) {
  if (!parentID) return '-'
  return parentNameMap.value.get(parentID) || parentID
}

async function loadMenus() {
  loading.value = true
  try {
    const ms = await systemAPI.menus() as { items: Menu[] }
    menus.value = (ms.items || []).map((item) => ({
      ...item,
      menu_type: item.menu_type === 'directory' ? 'directory' : 'menu',
    }))
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

function openCreateMenu() {
  editingMenuID.value = ''
  Object.assign(menuForm, {
    name: '',
    path: '',
    menu_type: 'menu',
    view_path: '',
    icon: '',
    parent_id: '',
    sort: 0,
  })
  menuModal.value = true
}

function openEditMenu(row: Menu) {
  editingMenuID.value = row.id
  Object.assign(menuForm, {
    name: row.name,
    path: row.path || '',
    menu_type: row.menu_type === 'directory' ? 'directory' : 'menu',
    view_path: row.view_path || '',
    icon: row.icon || '',
    parent_id: row.parent_id || '',
    sort: row.sort ?? 0,
  })
  menuModal.value = true
}

async function submitMenu() {
  try {
    if (!String(menuForm.name || '').trim()) {
      message.warning('请输入菜单名称')
      return
    }
    if (editingMenuID.value && menuForm.parent_id === editingMenuID.value) {
      message.warning('父级菜单不能是自己')
      return
    }
    if (menuForm.menu_type === 'menu' && !String(menuForm.path || '').trim()) {
      message.warning('菜单类型必须填写浏览器路径')
      return
    }
    if (menuForm.menu_type === 'menu' && !String(menuForm.view_path || '').trim()) {
      message.warning('菜单类型必须填写 Vue 文件路径')
      return
    }

    const payload = {
      name: String(menuForm.name || '').trim(),
      path: String(menuForm.path || '').trim(),
      menu_type: menuForm.menu_type,
      view_path: menuForm.menu_type === 'menu' ? String(menuForm.view_path || '').trim() : '',
      icon: String(menuForm.icon || '').trim(),
      parent_id: String(menuForm.parent_id || '').trim(),
      sort: Number(menuForm.sort || 0),
    }

    if (editingMenuID.value) {
      await systemAPI.updateMenu(editingMenuID.value, payload)
      message.success('菜单已更新')
    } else {
      await systemAPI.createMenu(payload)
      message.success('菜单已创建')
    }
    menuModal.value = false
    await loadMenus()
    await refreshCurrentUserMenus()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function removeMenu(id: string) {
  try {
    await systemAPI.removeMenu(id)
    message.success('菜单已删除')
    await loadMenus()
    await refreshCurrentUserMenus()
  } catch (err) {
    message.error((err as Error).message)
  }
}

onMounted(loadMenus)
</script>

<template>
  <div>
    <h2 class="page-title">菜单管理</h2>
    <p class="page-subtitle">按目录/菜单维护导航结构、路径、页面文件和图标。</p>

    <a-card class="glass-card">
      <div class="table-toolbar">
        <a-button type="primary" @click="openCreateMenu">新增菜单</a-button>
        <a-button @click="loadMenus">刷新</a-button>
      </div>
      <a-table
        :data-source="menuTreeData"
        row-key="id"
        :loading="loading"
        :pagination="false"
        :default-expand-all-rows="true"
      >
        <a-table-column title="名称" data-index="name" />
        <a-table-column title="类型" width="90">
          <template #default="{ record }">
            <a-tag :color="record.menu_type === 'directory' ? 'blue' : 'green'">
              {{ menuTypeLabel(record.menu_type) }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="浏览器路径" data-index="path" />
        <a-table-column title="Vue文件路径" data-index="view_path" />
        <a-table-column title="图标" data-index="icon" width="160" />
        <a-table-column title="父级菜单" width="160">
          <template #default="{ record }">{{ parentMenuName(record.parent_id) }}</template>
        </a-table-column>
        <a-table-column title="排序" data-index="sort" width="80" />
        <a-table-column title="操作" width="180">
          <template #default="{ record }">
            <a-space>
              <a-button size="small" @click="openEditMenu(record)">编辑</a-button>
              <a-popconfirm title="确定删除该菜单？" @confirm="removeMenu(record.id)">
                <a-button size="small" danger>删除</a-button>
              </a-popconfirm>
            </a-space>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal v-model:open="menuModal" :title="editingMenuID ? '编辑菜单' : '新增菜单'" @ok="submitMenu">
      <a-form layout="vertical">
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="菜单名称" required><a-input v-model:value="menuForm.name" /></a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="类型" required>
              <a-select v-model:value="menuForm.menu_type" :options="MENU_TYPE_OPTIONS" />
            </a-form-item>
          </a-col>
        </a-row>
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="父级菜单">
              <a-select v-model:value="menuForm.parent_id" :options="parentMenuOptions" />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="图标">
              <a-select v-model:value="menuForm.icon" :options="MENU_ICON_OPTIONS" />
            </a-form-item>
          </a-col>
        </a-row>
        <a-form-item label="浏览器路径" :required="menuNeedsPage">
          <a-input v-model:value="menuForm.path" :placeholder="menuNeedsPage ? '/system/menus' : '目录可留空'" />
        </a-form-item>
        <a-form-item label="Vue文件路径" :required="menuNeedsPage">
          <a-input v-model:value="menuForm.view_path" placeholder="views/system/SystemMenusView.vue" :disabled="!menuNeedsPage" />
        </a-form-item>
        <a-form-item label="排序">
          <a-input-number v-model:value="menuForm.sort" :min="0" style="width: 100%" />
        </a-form-item>
        <a-alert type="info" show-icon message="目录可包含子目录和子菜单；菜单必须填写浏览器路径与Vue文件路径。" />
      </a-form>
    </a-modal>
  </div>
</template>
