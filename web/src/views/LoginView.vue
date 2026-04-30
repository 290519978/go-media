<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { message } from 'ant-design-vue'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()
const loading = ref(false)
const form = reactive({
  username: 'admin',
  password: 'admin',
})

async function submit() {
  if (!String(form.username || '').trim() || !String(form.password || '').trim()) {
    message.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    await authStore.login({ username: form.username, password: form.password })
    message.success('登录成功')
    router.push('/dashboard')
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <div class="login-glow" />
    <a-card class="login-card glass-card" :bordered="false">
      <h1 class="login-title">鸿眸多模态感知平台</h1>
      <p class="login-sub">边缘 AI 视频平台</p>
      <a-form layout="vertical" :model="form" @submit.prevent="submit">
        <a-form-item label="用户名" name="username">
          <a-input v-model:value="form.username" allow-clear />
        </a-form-item>
        <a-form-item label="密码" name="password">
          <a-input-password v-model:value="form.password" />
        </a-form-item>
        <a-button block type="primary" :loading="loading" @click="submit">登录</a-button>
      </a-form>
    </a-card>
  </div>
</template>

<style scoped>
.login-wrap {
  min-height: 100vh;
  display: grid;
  place-items: center;
  position: relative;
  overflow: hidden;
  padding: 24px;
  background:
    radial-gradient(circle at top left, rgba(37, 99, 235, 0.22), transparent 28%),
    radial-gradient(circle at bottom right, rgba(56, 189, 248, 0.16), transparent 26%),
    linear-gradient(160deg, #08111f 0%, #0f1d35 38%, #12294d 100%);
}

.login-glow {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(circle at 18% 22%, rgba(96, 165, 250, 0.36), transparent 22%),
    radial-gradient(circle at 82% 74%, rgba(37, 99, 235, 0.26), transparent 20%);
  filter: blur(22px);
}

.login-card {
  width: min(100%, 420px);
  position: relative;
  overflow: hidden;
}

.login-card::before {
  content: '';
  position: absolute;
  inset: 0 0 auto;
  height: 120px;
  background: linear-gradient(180deg, rgba(37, 99, 235, 0.1), transparent);
  pointer-events: none;
}

.login-card :deep(.ant-card-body) {
  padding: 34px 34px 30px;
}

.login-title {
  margin: 0;
  font-size: 36px;
  line-height: 1.1;
  font-weight: 700;
  letter-spacing: -0.04em;
  color: #0f172a;
}

.login-sub {
  margin: 10px 0 24px;
  color: #5b6475;
  line-height: 1.7;
}

.login-card :deep(.ant-form-item) {
  margin-bottom: 18px;
}

.login-card :deep(.ant-btn) {
  margin-top: 6px;
}

@media (max-width: 520px) {
  .login-card :deep(.ant-card-body) {
    padding: 28px 22px 24px;
  }

  .login-title {
    font-size: 32px;
  }
}
</style>
