import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { title: '登录' },
  },
  {
    path: '/',
    component: () => import('@/layouts/AppShell.vue'),
    children: [
      { path: '', redirect: '/dashboard' },
      { path: '/dashboard', name: 'dashboard', component: () => import('@/views/DashboardView.vue'), meta: { title: '数据看板' } },
      { path: '/areas', name: 'areas', component: () => import('@/views/areas/AreaView.vue'), meta: { title: '区域管理' } },
      { path: '/devices', name: 'devices', component: () => import('@/views/devices/DeviceView.vue'), meta: { title: '摄像头配置' } },
      { path: '/devices/gb28181', name: 'devices_gb28181', component: () => import('@/views/devices/GB28181View.vue'), meta: { title: 'GB28181接入详情' } },
      { path: '/algorithms', name: 'algorithms', redirect: '/algorithms/manage', meta: { title: '算法中心' } },
      { path: '/algorithms/manage', name: 'algorithms_manage', component: () => import('@/views/algorithms/AlgorithmManageView.vue'), meta: { title: '算法列表' } },
      { path: '/algorithms/llm-usage', name: 'algorithms_llm_usage', component: () => import('@/views/algorithms/LLMUsageView.vue'), meta: { title: 'LLM用量统计' } },
      { path: '/tasks', name: 'tasks', redirect: '/tasks/video', meta: { title: '任务中心' } },
      { path: '/tasks/video', name: 'tasks_video', component: () => import('@/views/tasks/TaskManageView.vue'), meta: { title: '视频任务配置' } },
      { path: '/tasks/levels', name: 'tasks_levels', component: () => import('@/views/tasks/AlarmLevelView.vue'), meta: { title: '报警等级管理' } },
      { path: '/events', name: 'events', component: () => import('@/views/events/EventView.vue'), meta: { title: '视频告警事件' } },
      { path: '/system', name: 'system', redirect: '/system/users', meta: { title: '系统管理' } },
      { path: '/system/users', name: 'system_users', component: () => import('@/views/system/SystemUsersView.vue'), meta: { title: '用户管理' } },
      { path: '/system/roles', name: 'system_roles', component: () => import('@/views/system/SystemRolesView.vue'), meta: { title: '角色管理' } },
      { path: '/system/menus', name: 'system_menus', component: () => import('@/views/system/SystemMenusView.vue'), meta: { title: '菜单管理' } },
    ],
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  const authStore = useAuthStore()
  const isLoginRoute = to.path === '/login'
  if (!authStore.isLoggedIn && !isLoginRoute) {
    return '/login'
  }
  if (authStore.isLoggedIn && isLoginRoute) {
    return '/dashboard'
  }
  if (authStore.isLoggedIn && authStore.menus.length === 0) {
    try {
      await authStore.refreshMe()
    } catch {
      authStore.logout()
      return '/login'
    }
  }
  return true
})

export default router

