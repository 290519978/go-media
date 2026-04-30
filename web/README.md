# maas-box Web（Vue 3）

## 技术栈
- Vue 3 + TypeScript
- Vite
- Vue Router 4
- Pinia
- Ant Design Vue
- Axios

## 本地运行
```bash
cd web
npm install
npm run dev
```

默认开发地址：`http://127.0.0.1:5173`

## 后端目标地址
- 开发代理默认在 `vite.config.ts` 指向 `http://127.0.0.1:15123`
- 可通过环境变量覆盖：
  - `VITE_API_BASE_URL=http://127.0.0.1:15123`

## 构建
```bash
npm run build
```
