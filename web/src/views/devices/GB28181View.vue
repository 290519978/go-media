<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { message } from 'ant-design-vue'
import { deviceAPI } from '@/api/modules'

type VerifyCheck = {
  key: string
  result: 'pass' | 'fail' | 'warn'
  message: string
}

const props = withDefaults(
  defineProps<{
    embedded?: boolean
  }>(),
  {
    embedded: false,
  },
)

const router = useRouter()
const loading = ref(false)
const verifying = ref(false)
const checks = ref<VerifyCheck[]>([])
const valid = ref<boolean | null>(null)

const info = reactive({
  note: '',
  tips: [] as string[],
  sip_server_id: '34020000002000000001',
  sip_domain: '3402000000',
  sip_ip: '127.0.0.1',
  sip_port: 15060,
  sip_password: '',
  transport_options: ['tcp', 'udp'],
  recommended_transport: 'udp',
  register_expires: 3600,
  keepalive_interval: 60,
  media: {
    ip: '127.0.0.1',
    rtp_port: 11000,
    port_range: '21000-21100',
  },
  sample_device_id: '34020000001320000001',
})

const form = reactive({
  sip_server_id: info.sip_server_id,
  sip_domain: info.sip_domain,
  sip_ip: info.sip_ip,
  sip_port: info.sip_port,
  transport: info.recommended_transport,
  device_id: info.sample_device_id,
  password: info.sip_password,
  media_ip: info.media.ip,
  media_port: info.media.rtp_port,
  register_expires: info.register_expires,
  keepalive_interval: info.keepalive_interval,
})

function resultLabel(result: VerifyCheck['result']) {
  if (result === 'pass') return '通过'
  if (result === 'warn') return '警告'
  return '失败'
}

function resetFormByInfo() {
  Object.assign(form, {
    sip_server_id: info.sip_server_id,
    sip_domain: info.sip_domain,
    sip_ip: info.sip_ip,
    sip_port: info.sip_port,
    transport: info.recommended_transport,
    device_id: info.sample_device_id,
    password: info.sip_password,
    media_ip: info.media.ip,
    media_port: info.media.rtp_port,
    register_expires: info.register_expires,
    keepalive_interval: info.keepalive_interval,
  })
  checks.value = []
  valid.value = null
}

async function loadInfo() {
  loading.value = true
  try {
    const data = (await deviceAPI.gb28181Info()) as {
      note?: string
      tips?: string[]
      sip_server_id?: string
      sip_domain?: string
      sip_ip?: string
      sip_port?: number
      sip_password?: string
      transport_options?: string[]
      recommended_transport?: string
      register_expires?: number
      keepalive_interval?: number
      media?: { ip?: string; rtp_port?: number; port_range?: string }
      sample_device_id?: string
    }
    info.note = String(data.note || '')
    info.tips = Array.isArray(data.tips) ? data.tips : []
    info.sip_server_id = String(data.sip_server_id || info.sip_server_id)
    info.sip_domain = String(data.sip_domain || info.sip_domain)
    info.sip_ip = String(data.sip_ip || info.sip_ip)
    info.sip_port = Number(data.sip_port || info.sip_port)
    info.sip_password = String(data.sip_password || '')
    info.transport_options = Array.isArray(data.transport_options) ? data.transport_options : info.transport_options
    info.recommended_transport = String(data.recommended_transport || info.recommended_transport)
    info.register_expires = Number(data.register_expires || info.register_expires)
    info.keepalive_interval = Number(data.keepalive_interval || info.keepalive_interval)
    info.media.ip = String(data.media?.ip || info.media.ip)
    info.media.rtp_port = Number(data.media?.rtp_port || info.media.rtp_port)
    info.media.port_range = String(data.media?.port_range || info.media.port_range)
    info.sample_device_id = String(data.sample_device_id || info.sample_device_id)
    resetFormByInfo()
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}

async function verify() {
  verifying.value = true
  try {
    const result = (await deviceAPI.verifyGB28181(form as unknown as Record<string, unknown>)) as {
      valid: boolean
      checks: VerifyCheck[]
    }
    checks.value = result.checks || []
    valid.value = !!result.valid
    if (result.valid) {
      message.success('参数预检通过')
    } else {
      message.warning('参数预检发现问题，请按检查项修正')
    }
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    verifying.value = false
  }
}

onMounted(loadInfo)
</script>

<template>
  <div>
    <template v-if="!props.embedded">
      <h2 class="page-title">GB28181 接入配置</h2>
      <p class="page-subtitle">用于查看平台参数并做接入前参数预检，不会触发真实设备注册。</p>
    </template>

    <a-card class="glass-card" :loading="loading">
      <div class="table-toolbar">
        <a-space>
          <a-button v-if="!props.embedded" @click="router.push('/devices')">返回</a-button>
          <a-button @click="loadInfo">刷新</a-button>
        </a-space>
        <a-tag color="blue">GB28181</a-tag>
      </div>

      <a-alert v-if="info.note" :message="info.note" type="info" show-icon style="margin-bottom: 12px" />

      <a-descriptions title="平台参数" bordered :column="2" size="small">
        <a-descriptions-item label="SIP 服务ID">{{ info.sip_server_id }}</a-descriptions-item>
        <a-descriptions-item label="SIP 域">{{ info.sip_domain }}</a-descriptions-item>
        <a-descriptions-item label="SIP IP">{{ info.sip_ip }}</a-descriptions-item>
        <a-descriptions-item label="SIP 端口">{{ info.sip_port }}</a-descriptions-item>
        <a-descriptions-item label="SIP 密码">{{ info.sip_password || '-' }}</a-descriptions-item>
        <a-descriptions-item label="推荐传输">{{ info.recommended_transport.toUpperCase() }}</a-descriptions-item>
        <a-descriptions-item label="传输选项">{{ info.transport_options.join(', ') }}</a-descriptions-item>
        <a-descriptions-item label="媒体 IP">{{ info.media.ip }}</a-descriptions-item>
        <a-descriptions-item label="媒体 RTP 端口">{{ info.media.rtp_port }}</a-descriptions-item>
        <a-descriptions-item label="媒体端口范围">{{ info.media.port_range }}</a-descriptions-item>
        <a-descriptions-item label="示例设备ID">{{ info.sample_device_id }}</a-descriptions-item>
      </a-descriptions>

      <a-divider />

      <a-form layout="vertical">
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="SIP 服务ID">
              <a-input v-model:value="form.sip_server_id" />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="SIP 域">
              <a-input v-model:value="form.sip_domain" />
            </a-form-item>
          </a-col>
        </a-row>
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="SIP IP">
              <a-input v-model:value="form.sip_ip" />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="SIP 端口">
              <a-input-number v-model:value="form.sip_port" :min="1" :max="65535" style="width: 100%" />
            </a-form-item>
          </a-col>
        </a-row>
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="传输方式">
              <a-select
                v-model:value="form.transport"
                :options="info.transport_options.map((item) => ({ label: item.toUpperCase(), value: item }))"
              />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="设备ID">
              <a-input v-model:value="form.device_id" />
            </a-form-item>
          </a-col>
        </a-row>
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="认证密码">
              <a-input-password v-model:value="form.password" />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="媒体 IP">
              <a-input v-model:value="form.media_ip" />
            </a-form-item>
          </a-col>
        </a-row>
        <a-row :gutter="12">
          <a-col :span="8">
            <a-form-item label="媒体 RTP 端口">
              <a-input-number v-model:value="form.media_port" :min="1" :max="65535" style="width: 100%" />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="注册有效期(s)">
              <a-input-number v-model:value="form.register_expires" :min="60" :max="86400" style="width: 100%" />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="心跳间隔(s)">
              <a-input-number v-model:value="form.keepalive_interval" :min="5" :max="600" style="width: 100%" />
            </a-form-item>
          </a-col>
        </a-row>
      </a-form>

      <a-space>
        <a-button type="primary" :loading="verifying" @click="verify">参数预检（非真实注册）</a-button>
        <a-button @click="resetFormByInfo">重置</a-button>
      </a-space>
    </a-card>

    <a-card v-if="checks.length" class="glass-card verify-card">
      <a-alert :type="valid ? 'success' : 'error'" :message="valid ? '预检通过' : '预检失败'" show-icon />
      <a-table :data-source="checks" row-key="key" size="small" :pagination="false" style="margin-top: 12px">
        <a-table-column title="检查项" data-index="key" />
        <a-table-column title="结果" width="120">
          <template #default="{ record }">
            <a-tag :color="record.result === 'pass' ? 'green' : record.result === 'fail' ? 'red' : 'orange'">
              {{ resultLabel(record.result) }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="说明" data-index="message" />
      </a-table>

      <a-divider v-if="info.tips.length > 0" />
      <a-space v-if="info.tips.length > 0" direction="vertical" size="small">
        <a-tag v-for="(tip, idx) in info.tips" :key="idx" color="blue">{{ tip }}</a-tag>
      </a-space>
    </a-card>
  </div>
</template>

<style scoped>
.verify-card {
  margin-top: 14px;
}
</style>
