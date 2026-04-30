# AI 服务 LLM 故障排查

更新时间：2026-03-27  
本文只覆盖两类链路：

- Go -> 本地 AI HTTP（例如 `127.0.0.1:50052`）
- AI 容器 -> DashScope HTTPS（例如 `https://dashscope.aliyuncs.com`）

如果只是算法测试图片/视频里出现 `大模型判定失败`、`Connection error.`、`SSLEOFError`，优先按本文排查。

## 1. 先看 AI 日志字段

AI 日志文件：

- `configs/logs/ai/analysis.log`

LLM 失败日志现在会带这些字段：

- `failure_type`：`connect | timeout | tls | provider_status | empty_content | unknown`
- `exception_type`
- `provider_host`
- `base_url`
- `call_id`
- `context`

先用 `call_id` 或时间窗口在日志里定位：

```powershell
Select-String -Path .\configs\logs\ai\analysis.log -Pattern 'call_id='
```

如果已经拿到某次调用的 `call_id`，可直接缩小范围：

```powershell
Select-String -Path .\configs\logs\ai\analysis.log -Pattern 'call_id=<替换为真实 call_id>'
```

## 2. 宿主机与容器内分别验证 DNS / TCP 443

以下示例默认容器名是 `maas-box-ai`，如实际名称不同请替换。

### 2.1 宿主机验证

```powershell
python -c "import socket; host='dashscope.aliyuncs.com'; addrs=sorted({info[4][0] for info in socket.getaddrinfo(host, 443, type=socket.SOCK_STREAM)}); print('dns=', addrs); s=socket.create_connection((host, 443), timeout=5); print('tcp_ok=', s.getpeername()); s.close()"
```

### 2.2 AI 容器内验证

```powershell
docker exec maas-box-ai python -c "import socket; host='dashscope.aliyuncs.com'; addrs=sorted({info[4][0] for info in socket.getaddrinfo(host, 443, type=socket.SOCK_STREAM)}); print('dns=', addrs); s=socket.create_connection((host, 443), timeout=5); print('tcp_ok=', s.getpeername()); s.close()"
```

观察点：

- 宿主机通、容器不通：优先怀疑 Docker 出网、容器 DNS、宿主机防火墙或代理配置没有传进容器
- 宿主机和容器都不通：优先怀疑本机网络、公司代理、防火墙或供应商网络波动

## 3. 容器内验证 TLS 握手

TCP 能通不代表 TLS 一定正常，`SSLEOFError`、证书链错误、代理拦截通常出在这一层。

```powershell
docker exec maas-box-ai python -c "import socket, ssl; host='dashscope.aliyuncs.com'; raw=socket.create_connection((host, 443), timeout=5); tls=ssl.create_default_context().wrap_socket(raw, server_hostname=host); print('tls_version=', tls.version()); print('peer_subject=', tls.getpeercert().get('subject')); tls.close()"
```

观察点：

- TCP 通但 TLS 握手失败：优先怀疑 TLS/SSL、代理中间层、证书链、深度包检测
- TLS 成功：说明至少基础 HTTPS 链路是通的，继续看供应商返回状态和业务日志

## 4. 用日志与 LLM 用量记录交叉定位

### 4.1 查数据库里的 LLM 用量调用

```powershell
python -c "import sqlite3; conn=sqlite3.connect(r'configs/data.db'); cur=conn.execute(\"select call_id, call_status, error_message, request_context, created_at from mb_llm_usage_calls order by created_at desc limit 20\"); [print(row) for row in cur.fetchall()]"
```

如果已经知道某个 `call_id`：

```powershell
python -c "import sqlite3; conn=sqlite3.connect(r'configs/data.db'); cur=conn.execute(\"select call_id, call_status, error_message, request_context, created_at from mb_llm_usage_calls where call_id = ?\", ('<替换为真实 call_id>',)); [print(row) for row in cur.fetchall()]"
```

### 4.2 和页面联动看

也可以直接到系统里的 LLM 用量页面，按时间窗口或 `call_id` 过滤，再回到 `analysis.log` 对同一个 `call_id` 做交叉定位。

推荐顺序：

1. 先在算法测试结果里确认失败发生时间
2. 再到 `analysis.log` 找对应 `call_id`
3. 再查 `mb_llm_usage_calls` 或 LLM 用量页确认 `call_status / error_message / request_context`

## 5. 判定矩阵

| 现象 | 更可能的问题层 | 说明 |
| --- | --- | --- |
| Go 调 AI 报 `127.0.0.1:50052 connectex refused`，AI 日志里没有对应请求 | Go -> 本地 AI HTTP | 本地端口、Docker host 网络或 AI HTTP 监听层问题 |
| 宿主机访问 DashScope 正常，容器内 DNS/443 不通 | Docker 出网 / DNS | 容器网络、DNS、代理、宿主机防火墙传递问题 |
| 容器内 TCP 443 可达，但 TLS 握手失败 | TLS / 证书链 / 代理 | 常见于 `SSLEOFError`、证书校验失败、中间代理截断 |
| TLS 正常，但日志 `failure_type=provider_status` 且有 `status_code>=400` | DashScope / 鉴权 / 配额 / 供应商状态 | 重点看 API Key、模型、额度、供应商返回信息 |
| 日志 `failure_type=empty_content` | 供应商返回空内容 | 不是网络层问题，优先看供应商返回体和模型行为 |
| 日志 `failure_type=connect` 或 `timeout` | 出网链路不稳定 | 优先查容器网络、DNS、代理和瞬时波动 |

## 6. 常见结论

- 图片算法测试里看到 `person x1 / person(0.90)` 这类摘要，若同时 `llm_usage.call_status=error/empty_content`，现在会被 Go 统一收敛成失败，不再误显示为成功结论
- `provider_status` 更像供应商明确返回了错误
- `tls` 更像 HTTPS 握手层异常
- `connect` / `timeout` 更像容器出网、DNS 或外部网络波动
