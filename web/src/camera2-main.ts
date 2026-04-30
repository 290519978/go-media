import { createApp } from 'vue'
import Antd from 'ant-design-vue'

import Camera2App from '@/views/dashboard/camera2/index.vue'
import 'ant-design-vue/dist/reset.css'
import '@/views/dashboard/camera2/assets/main.css'

const app = createApp(Camera2App)
app.use(Antd)
app.mount('#app')
