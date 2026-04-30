<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { message, notification } from 'ant-design-vue'
import {
  AlertOutlined,
  AppstoreOutlined,
  CameraOutlined,
  ClusterOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
  NodeIndexOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  UserOutlined,
} from '@ant-design/icons-vue'
import screenfull from 'screenfull'
import { useAuthStore, type CleanupNotice } from '@/stores/auth'
import {
  llmQuotaNoticeTargetPath,
  markLLMQuotaNoticeShown,
  normalizeLLMQuotaNotice,
  resolveLLMQuotaNoticeContent,
  type LLMQuotaNotice,
} from '@/utils/llmQuotaNotice'
import brandLogo from '@/assets/logo.ico'

type StaticMenuItem = {
  key: string
  label: string
  icon: any
}

type RenderMenuItem = {
  key: string
  label: string
  icon: any
  menuType: 'directory' | 'menu'
  routePath: string
  sort: number
  children: RenderMenuItem[]
}

const authStore = useAuthStore()
const route = useRoute()
const router = useRouter()
const alertCount = ref(0)
const isFullscreen = ref(false)
const openKeys = ref<string[]>([])

const routeMenu: StaticMenuItem[] = [
  { key: '/dashboard', label: '数据看板', icon: AppstoreOutlined },
  { key: '/devices', label: '摄像头设备', icon: CameraOutlined },
  { key: '/areas', label: '区域管理', icon: ClusterOutlined },
  { key: '/algorithms', label: '算法中心', icon: NodeIndexOutlined },
  { key: '/algorithms/manage', label: '算法管理', icon: NodeIndexOutlined },
  { key: '/algorithms/llm-usage', label: 'LLM用量统计', icon: NodeIndexOutlined },
  { key: '/tasks', label: '任务管理', icon: SafetyCertificateOutlined },
  { key: '/tasks/video', label: '视频任务', icon: SafetyCertificateOutlined },
  { key: '/tasks/levels', label: '报警等级', icon: SafetyCertificateOutlined },
  { key: '/events', label: '报警记录', icon: AlertOutlined },
  { key: '/system', label: '系统管理', icon: SettingOutlined },
  { key: '/system/users', label: '用户管理', icon: SettingOutlined },
  { key: '/system/roles', label: '角色管理', icon: SettingOutlined },
  { key: '/system/menus', label: '菜单管理', icon: SettingOutlined },
]
const staticMenuMap = new Map(routeMenu.map((item) => [item.key, item]))
const iconMap: Record<string, any> = {
  AppstoreOutlined,
  ClusterOutlined,
  CameraOutlined,
  NodeIndexOutlined,
  SafetyCertificateOutlined,
  AlertOutlined,
  SettingOutlined,
}

function buildDefaultMenus(): RenderMenuItem[] {
  return [
    {
      key: '/dashboard',
      label: '数据看板',
      icon: AppstoreOutlined,
      menuType: 'menu',
      routePath: '/dashboard',
      sort: 1,
      children: [],
    },
    {
      key: 'default_menu_device_dir',
      label: '设备管理',
      icon: CameraOutlined,
      menuType: 'directory',
      routePath: '',
      sort: 2,
      children: [
        {
          key: '/devices',
          label: '摄像头设备',
          icon: CameraOutlined,
          menuType: 'menu',
          routePath: '/devices',
          sort: 1,
          children: [],
        },
        {
          key: '/areas',
          label: '区域管理',
          icon: ClusterOutlined,
          menuType: 'menu',
          routePath: '/areas',
          sort: 2,
          children: [],
        },
      ],
    },
    {
      key: 'default_menu_algorithms_dir',
      label: '算法中心',
      icon: NodeIndexOutlined,
      menuType: 'directory',
      routePath: '',
      sort: 3,
      children: [
        {
          key: '/algorithms/manage',
          label: '算法管理',
          icon: NodeIndexOutlined,
          menuType: 'menu',
          routePath: '/algorithms/manage',
          sort: 1,
          children: [],
        },
        {
          key: '/algorithms/llm-usage',
          label: 'LLM用量统计',
          icon: NodeIndexOutlined,
          menuType: 'menu',
          routePath: '/algorithms/llm-usage',
          sort: 2,
          children: [],
        },
      ],
    },
    {
      key: 'default_menu_tasks_dir',
      label: '任务管理',
      icon: SafetyCertificateOutlined,
      menuType: 'directory',
      routePath: '',
      sort: 4,
      children: [
        {
          key: '/tasks/video',
          label: '视频任务',
          icon: SafetyCertificateOutlined,
          menuType: 'menu',
          routePath: '/tasks/video',
          sort: 1,
          children: [],
        },
        {
          key: '/tasks/levels',
          label: '报警等级',
          icon: SafetyCertificateOutlined,
          menuType: 'menu',
          routePath: '/tasks/levels',
          sort: 2,
          children: [],
        },
      ],
    },
    {
      key: 'default_menu_events_dir',
      label: '事件中心',
      icon: AlertOutlined,
      menuType: 'directory',
      routePath: '',
      sort: 5,
      children: [
        {
          key: '/events',
          label: '报警记录',
          icon: AlertOutlined,
          menuType: 'menu',
          routePath: '/events',
          sort: 1,
          children: [],
        },
      ],
    },
    {
      key: 'default_menu_system_dir',
      label: '系统管理',
      icon: SettingOutlined,
      menuType: 'directory',
      routePath: '',
      sort: 6,
      children: [
        {
          key: '/system/users',
          label: '用户管理',
          icon: SettingOutlined,
          menuType: 'menu',
          routePath: '/system/users',
          sort: 1,
          children: [],
        },
        {
          key: '/system/roles',
          label: '角色管理',
          icon: SettingOutlined,
          menuType: 'menu',
          routePath: '/system/roles',
          sort: 2,
          children: [],
        },
        {
          key: '/system/menus',
          label: '菜单管理',
          icon: SettingOutlined,
          menuType: 'menu',
          routePath: '/system/menus',
          sort: 3,
          children: [],
        },
      ],
    },
  ]
}
function sortTree(items: RenderMenuItem[]) {
  items.sort((a, b) => a.sort - b.sort || a.label.localeCompare(b.label, 'zh-CN'))
  for (const item of items) {
    if (item.children.length) {
      sortTree(item.children)
    }
  }
}

const renderedMenus = computed<RenderMenuItem[]>(() => {
  if (!authStore.menus.length) return buildDefaultMenus()

  const nodeMap = new Map<string, RenderMenuItem & { parentID: string }>()
  for (const menu of authStore.menus) {
    const path = String(menu.path || '').trim()
    const staticMeta = staticMenuMap.get(path)
    const menuType = String(menu.menu_type || '').toLowerCase() === 'directory' ? 'directory' : 'menu'
    const backendIcon = String(menu.icon || '').trim()
    const icon = iconMap[backendIcon] || staticMeta?.icon || AppstoreOutlined
    nodeMap.set(menu.id, {
      key: menu.id,
      label: String(menu.name || staticMeta?.label || path || '未命名菜单'),
      icon,
      menuType,
      routePath: menuType === 'menu' ? path : '',
      sort: Number(menu.sort || 0),
      children: [],
      parentID: String(menu.parent_id || ''),
    })
  }

  const roots: Array<RenderMenuItem & { parentID: string }> = []
  for (const node of nodeMap.values()) {
    const parent = nodeMap.get(node.parentID)
    if (parent && parent.key !== node.key) {
      parent.children.push(node)
    } else {
      roots.push(node)
    }
  }

  sortTree(roots)
  return roots
})

const keyToRoutePath = computed(() => {
  const map = new Map<string, string>()
  const walk = (items: RenderMenuItem[]) => {
    for (const item of items) {
      if (item.routePath) {
        map.set(item.key, item.routePath)
      }
      if (item.children.length) {
        walk(item.children)
      }
    }
  }
  walk(renderedMenus.value)
  return map
})

const routePathToKey = computed(() => {
  const map = new Map<string, string>()
  const walk = (items: RenderMenuItem[]) => {
    for (const item of items) {
      if (item.routePath && !map.has(item.routePath)) {
        map.set(item.routePath, item.key)
      }
      if (item.children.length) {
        walk(item.children)
      }
    }
  }
  walk(renderedMenus.value)
  return map
})

const selectedKeys = computed(() => {
  const key = routePathToKey.value.get(route.path)
  return key ? [key] : []
})

const isDashboardRoute = computed(() => route.path === '/dashboard')

const menuItems = computed(() => {
  const walk = (items: RenderMenuItem[]): Array<Record<string, unknown>> =>
    items.map((item) => ({
      key: item.key,
      icon: h(item.icon),
      label: item.label,
      children: item.children.length ? walk(item.children) : undefined,
    }))
  return walk(renderedMenus.value)
})

function findAncestorKeys(
  items: RenderMenuItem[],
  targetRoutePath: string,
  parents: string[] = [],
): string[] | null {
  for (const item of items) {
    if (item.menuType === 'menu' && item.routePath === targetRoutePath) {
      return parents
    }
    if (item.children.length) {
      const hit = findAncestorKeys(item.children, targetRoutePath, [...parents, item.key])
      if (hit) return hit
    }
  }
  return null
}

function syncOpenKeysByRoute() {
  openKeys.value = findAncestorKeys(renderedMenus.value, route.path) || []
}

let ws: WebSocket | null = null
const shownCleanupNotices = new Set<string>()
const shownLLMQuotaNotices = new Set<string>()

function goTo(path: string) {
  void router.push(path)
}

function onMenuClick(key: string) {
  const path = keyToRoutePath.value.get(key)
  if (path) {
    goTo(path)
  }
}

function onOpenChange(keys: Array<string | number>) {
  openKeys.value = keys.map((key) => String(key))
}

function logout() {
  authStore.logout()
  void router.push('/login')
}

function toggleFullscreen() {
  if (!screenfull.isEnabled) return
  if (screenfull.isFullscreen) {
    void screenfull.exit()
  } else {
    void screenfull.request()
  }
}

function onFullscreenChange() {
  if (!screenfull.isEnabled) return
  isFullscreen.value = screenfull.isFullscreen
}

function bindFullscreenState() {
  if (!screenfull.isEnabled) return
  isFullscreen.value = screenfull.isFullscreen
  screenfull.on('change', onFullscreenChange)
}

function handleAlert(payload: Record<string, unknown>) {
  window.dispatchEvent(new CustomEvent('maas-alarm', { detail: payload }))
  alertCount.value += 1
  const eventID = String(payload.event_id || '')
  const deviceID = String(payload.device_id || '')
  const taskText = String(payload.task_name || payload.task_id || '-')
  const deviceText = String(payload.device_name || payload.device_id || '-')
  const algorithmCode = String(payload.algorithm_code || '').trim()
  const algorithmName = String(payload.algorithm_name || '').trim()
  const algorithmID = String(payload.algorithm_id || '').trim()
  const algorithmText = algorithmCode && algorithmName
    ? `${algorithmCode} / ${algorithmName}`
    : algorithmCode || algorithmName || algorithmID || '-'

  notification.warning({
    message: '实时告警',
    description: `任务：${taskText} | 设备：${deviceText} | 算法：${algorithmText}`,
    placement: 'topRight',
    duration: 5,
    onClick: () => {
      void router.push({
        path: '/dashboard',
        query: {
          event: eventID,
          device: deviceID,
        },
      })
    },
  })
}
function normalizeCleanupNotice(payload: Record<string, unknown>): CleanupNotice | null {
  const noticeID = String(payload.notice_id || '').trim()
  const executeAfter = String(payload.execute_after || '').trim()
  if (!noticeID) return null
  const noticeKindRaw = String(payload.notice_kind || '').trim()
  const noticeKind = noticeKindRaw || 'retention_risk'
  const hardReachedRaw = payload.hard_reached
  const hardReached = hardReachedRaw === true || hardReachedRaw === 'true' || hardReachedRaw === 1 || hardReachedRaw === '1'
  const usedPercent = Number(payload.used_percent ?? 0)
  const freeGB = Number(payload.free_gb ?? 0)
  const softWatermark = Number(payload.soft_watermark ?? 0)
  return {
    notice_kind: noticeKind,
    notice_id: noticeID,
    issued_at: String(payload.issued_at || '').trim(),
    execute_after: executeAfter || undefined,
    event_snapshot_count: Number(payload.event_snapshot_count || 0),
    alarm_clip_count: Number(payload.alarm_clip_count || 0),
    hard_reached: hardReached,
    used_percent: Number.isFinite(usedPercent) ? usedPercent : 0,
    free_gb: Number.isFinite(freeGB) ? freeGB : 0,
    soft_watermark: Number.isFinite(softWatermark) ? softWatermark : 0,
    title: String(payload.title || '').trim(),
    message: String(payload.message || '').trim(),
  }
}

function cleanupNoticeKey(notice: CleanupNotice) {
  return `${notice.notice_kind || 'retention_risk'}|${notice.notice_id}|${notice.issued_at || ''}`
}

function upsertCleanupNotice(notice: CleanupNotice) {
  const list = [...authStore.cleanupNotices]
  const key = cleanupNoticeKey(notice)
  const idx = list.findIndex((item) => cleanupNoticeKey(item) === key)
  if (idx >= 0) {
    list[idx] = notice
  } else {
    list.push(notice)
  }
  authStore.cleanupNotices = list
  authStore.cleanupNotice = list[0] || null
}

function upsertLLMQuotaNotice(notice: LLMQuotaNotice) {
  authStore.llmQuotaNotice = notice
}

function showCleanupNotice(notice: CleanupNotice) {
  const key = cleanupNoticeKey(notice)
  if (!key || shownCleanupNotices.has(key)) return
  shownCleanupNotices.add(key)

  const noticeKind = String(notice.notice_kind || 'retention_risk').toLowerCase()
  if (noticeKind === 'soft_pressure') {
    const used = Number(notice.used_percent || 0).toFixed(2)
    const freeGB = Number(notice.free_gb || 0).toFixed(2)
    const softWatermark = Number(notice.soft_watermark || 0).toFixed(2)
    const fallbackTitle = '存储压力提醒'
    const fallbackMessage = `磁盘使用率 ${used}%，可用空间 ${freeGB}GB，已进入 Soft 阈值 ${softWatermark}%。请优先处理事件并导出报警片段。`
    const description = String(notice.message || '').trim() || fallbackMessage
    notification.warning({
      message: String(notice.title || '').trim() || fallbackTitle,
      description,
      placement: 'topRight',
      duration: 8,
      onClick: () => {
        void router.push('/events')
      },
    })
    return
  }

  const eventCount = Number(notice.event_snapshot_count || 0)
  const clipCount = Number(notice.alarm_clip_count || 0)
  const fallbackTitle = '存储清理提醒'
  const stageText = notice.hard_reached
    ? '当前已进入 Hard 阶段，本轮将执行清理。'
    : '若进入 Hard 阶段将自动清理。'
  const fallbackMessage = `检测到超保留期事件快照 ${eventCount} 个、报警片段目录 ${clipCount} 个。请先处理事件并导出报警片段；${stageText}`
  const description = String(notice.message || '').trim() || fallbackMessage

  notification.warning({
    message: String(notice.title || '').trim() || fallbackTitle,
    description,
    placement: 'topRight',
    duration: 8,
    onClick: () => {
      void router.push('/events')
    },
  })
}

function showLLMQuotaNotice(notice: LLMQuotaNotice) {
  const noticeID = String(notice.notice_id || '').trim()
  if (!noticeID) return
  const key = `app|${noticeID}`
  if (shownLLMQuotaNotices.has(key)) return
  if (!markLLMQuotaNoticeShown('app', notice)) return
  shownLLMQuotaNotices.add(key)

  const { title, description } = resolveLLMQuotaNoticeContent(notice)
  notification.warning({
    message: title,
    description,
    placement: 'topRight',
    duration: 8,
    onClick: () => {
      void router.push(llmQuotaNoticeTargetPath)
    },
  })
}

onMounted(() => {
  syncOpenKeysByRoute()
  bindFullscreenState()
  if (authStore.cleanupNotices.length > 0) {
    authStore.cleanupNotices.forEach((item) => showCleanupNotice(item))
  } else if (authStore.cleanupNotice) {
    showCleanupNotice(authStore.cleanupNotice)
  }
  if (authStore.llmQuotaNotice) {
    showLLMQuotaNotice(authStore.llmQuotaNotice)
  }
  const wsURL = `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/ws/alerts`
  ws = new WebSocket(wsURL)
  ws.onopen = () => message.success('告警通道已连接')
  ws.onclose = () => message.warning('告警通道已断开')
  ws.onmessage = (ev) => {
    try {
      const payload = JSON.parse(ev.data)
      if (payload.type === 'alarm') {
        handleAlert(payload)
      } else if (payload.type === 'storage_cleanup_notice') {
        const notice = normalizeCleanupNotice(payload)
        if (notice) {
          upsertCleanupNotice(notice)
          showCleanupNotice(notice)
        }
      } else if (payload.type === 'llm_quota_notice') {
        const notice = normalizeLLMQuotaNotice(payload)
        if (notice) {
          upsertLLMQuotaNotice(notice)
          showLLMQuotaNotice(notice)
        }
      }
    } catch {
      // 忽略无效的告警消息
    }
  }
})

watch(
  () => route.path,
  () => {
    syncOpenKeysByRoute()
  },
)

watch(
  renderedMenus,
  () => {
    syncOpenKeysByRoute()
  },
)

onBeforeUnmount(() => {
  if (ws) ws.close()
  if (screenfull.isEnabled) {
    screenfull.off('change', onFullscreenChange)
  }
})
</script>

<template>
  <a-layout class="shell">
    <a-layout-header class="shell-header">
      <div class="shell-brand">
        <div class="brand-mark">
          <img :src="brandLogo" alt="鸿眸多模态感知平台" class="brand-logo" />
        </div>
        <div class="brand-copy">
          <div class="brand-name">鸿眸多模态感知平台</div>
          <div class="brand-sub">统一感知与分析中枢</div>
        </div>
      </div>
      <div class="shell-right">
        <a-badge :count="alertCount" :number-style="{ backgroundColor: '#d9363e' }">
          <a-button class="shell-action-btn" type="text" @click="goTo('/events')">
            <template #icon>
              <AlertOutlined />
            </template>
          </a-button>
        </a-badge>
        <a-button class="shell-action-btn" type="text" @click="toggleFullscreen">
          <template #icon>
            <component :is="isFullscreen ? FullscreenExitOutlined : FullscreenOutlined" />
          </template>
        </a-button>
        <a-dropdown>
          <a class="user-link shell-user-trigger">
            <UserOutlined />
            <span>{{ authStore.username || '用户' }}</span>
          </a>
          <template #overlay>
            <a-menu>
              <a-menu-item @click="goTo('/system/users')">系统管理</a-menu-item>
              <a-menu-item @click="logout">退出登录</a-menu-item>
            </a-menu>
          </template>
        </a-dropdown>
      </div>
    </a-layout-header>

    <a-layout class="shell-main">
      <a-layout-sider class="shell-sidebar" :width="248">
        <a-menu
          mode="inline"
          :selected-keys="selectedKeys"
          :open-keys="openKeys"
          :items="menuItems"
          @click="(menuInfo: { key: string | number }) => onMenuClick(String(menuInfo.key))"
          @openChange="(keys: Array<string | number>) => onOpenChange(keys)"
        />
      </a-layout-sider>

      <a-layout-content class="shell-content">
        <div :class="['shell-view', { 'backoffice-surface': !isDashboardRoute }]">
          <router-view />
        </div>
      </a-layout-content>
    </a-layout>
  </a-layout>
</template>

<style scoped>
.shell {
  min-height: 100vh;
  background:
    radial-gradient(circle at top right, rgba(37, 99, 235, 0.12), transparent 24%),
    linear-gradient(180deg, #f8fbff 0%, #eef3fa 100%);
}

.shell-header {
  position: sticky;
  top: 0;
  z-index: 20;
  height: 78px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 0 24px;
  background: rgba(255, 255, 255, 0.96);
  border-bottom: 1px solid rgba(15, 23, 42, 0.08);
  box-shadow: 0 10px 24px rgba(15, 23, 42, 0.04);
  backdrop-filter: blur(18px);
}

.shell-brand {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12px;
}

.brand-mark {
  width: 40px;
  height: 40px;
  display: grid;
  place-items: center;
  overflow: hidden;
}

.brand-logo {
  width: 100%;
  height: 100%;
  display: block;
  object-fit: cover;
}

.brand-copy {
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 4px;
}

.brand-name {
  line-height: 1.1;
  color: #0f172a;
  font-size: 20px;
  font-weight: 700;
  letter-spacing: -0.03em;
}

.brand-sub {
  line-height: 1.2;
  color: #64748b;
  font-size: 12px;
  font-weight: 500;
}

.shell-right {
  display: flex;
  align-items: center;
  gap: 10px;
}

.shell-action-btn {
  box-sizing: border-box;
  width: 40px;
  height: 40px;
  border-radius: 14px;
  color: #334155;
  background: rgba(148, 163, 184, 0.12);
  transition:
    background-color 180ms ease,
    color 180ms ease,
    transform 180ms ease,
    box-shadow 180ms ease;
}

.shell-action-btn:hover,
.shell-action-btn:focus-visible {
  color: #1d4ed8;
  background: rgba(37, 99, 235, 0.12);
  box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.12);
}

.shell-action-btn:active {
  transform: scale(0.98);
}

.user-link {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: #1e293b;
  text-decoration: none;
}

.shell-user-trigger {
  box-sizing: border-box;
  height: 40px;
  min-height: 40px;
  padding: 0 14px;
  border-radius: 14px;
  background: rgba(255, 255, 255, 0.48);
  border: 1px solid rgba(148, 163, 184, 0.18);
  line-height: 1;
  transition:
    border-color 180ms ease,
    background-color 180ms ease,
    color 180ms ease,
    box-shadow 180ms ease;
}

.shell-user-trigger:hover,
.shell-user-trigger:focus-visible {
  color: #1d4ed8;
  border-color: rgba(37, 99, 235, 0.2);
  background: rgba(255, 255, 255, 0.78);
  box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.08);
}

.shell-main {
  flex: 1;
  min-height: calc(100vh - 78px);
  background: transparent;
  padding: 18px;
}

.shell-sidebar {
  background: rgba(255, 255, 255, 0.84);
  border-inline-end: none !important;
  margin-right: 18px;
  overflow: hidden;
}

:deep(.shell-sidebar .ant-layout-sider-children) {
  background: rgba(255, 255, 255, 0.84);
  border: 1px solid rgba(15, 23, 42, 0.08);
  border-radius: 24px;
  box-shadow: 0 18px 40px rgba(15, 23, 42, 0.08);
  backdrop-filter: blur(16px);
  min-height: 100%;
  padding: 14px 12px;
}

.shell-content {
  min-width: 0;
  background: transparent;
}

.shell-view {
  min-height: calc(100vh - 114px);
}

:deep(.shell-sidebar .ant-menu-inline) {
  border-inline-end: none;
  background: transparent;
}

:deep(.shell-sidebar .ant-menu-submenu) {
  margin: 4px 8px;
  border-radius: 14px;
}

:deep(.shell-sidebar .ant-menu-submenu-title) {
  height: 46px;
  margin: 0;
  border-radius: 14px;
  color: #475569;
  transition:
    background-color 180ms ease,
    color 180ms ease,
    box-shadow 180ms ease;
}

:deep(.shell-sidebar .ant-menu-submenu-title:hover) {
  color: #1d4ed8;
  background: rgba(37, 99, 235, 0.08);
}

:deep(.shell-sidebar .ant-menu-sub.ant-menu-inline) {
  background: transparent;
}

:deep(.shell-sidebar .ant-menu-submenu-selected > .ant-menu-submenu-title) {
  color: #1d4ed8;
}

:deep(.shell-sidebar .ant-menu .ant-menu-item) {
  height: 46px;
  margin: 4px 8px;
  border-radius: 14px;
  color: #475569;
  transition:
    background-color 180ms ease,
    color 180ms ease,
    box-shadow 180ms ease;
}

:deep(.shell-sidebar .ant-menu .ant-menu-item:hover) {
  color: #1d4ed8;
  background: rgba(37, 99, 235, 0.08);
}

:deep(.shell-sidebar .ant-menu .ant-menu-item-selected) {
  position: relative;
  color: #1d4ed8;
  background: linear-gradient(90deg, rgba(37, 99, 235, 0.14) 0%, rgba(37, 99, 235, 0.04) 100%);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.7);
}

:deep(.shell-sidebar .ant-menu .ant-menu-item-selected::before) {
  content: '';
  position: absolute;
  left: 0;
  top: 10px;
  bottom: 10px;
  width: 3px;
  border-radius: 999px;
  background: linear-gradient(180deg, #93c5fd 0%, #3b82f6 100%);
}

:deep(.shell-sidebar .ant-menu .ant-menu-item .ant-menu-title-content) {
  font-weight: 500;
}

@media (max-width: 1200px) {
  .shell-header {
    padding: 0 16px;
    gap: 16px;
  }

  .brand-name {
    font-size: 18px;
  }
}

@media (max-width: 960px) {
  .shell-header {
    height: auto;
    flex-wrap: wrap;
    padding: 14px 16px;
  }

  .shell-right {
    margin-left: auto;
  }

  .shell-brand {
    gap: 10px;
  }

  .brand-sub {
    font-size: 12px;
  }

  .brand-name {
    font-size: 16px;
  }

  .shell-main {
    padding: 12px;
  }

  .shell-sidebar {
    margin-right: 0;
    margin-bottom: 12px;
  }
}
</style>
