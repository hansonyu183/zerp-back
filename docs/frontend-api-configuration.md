# ZERP 前端 API 配置说明

本文供 ZERP 前端开发、Cloudflare Pages 构建和联调使用。后端 API 地址为：

```text
https://zerp-api.bytesucceed.com
```

## 1. Cloudflare Pages 生产配置

在 Cloudflare Pages 项目的生产环境变量中配置：

```env
VITE_API_BASE_URL=https://zerp-api.bytesucceed.com
```

修改环境变量后必须重新部署前端。Vite 的 `VITE_*` 变量在构建时写入静态资源，仅修改 Pages 变量而不重新构建不会生效。

当前生产前端 Origin 为：

```text
https://zerp.bytesucceed.com
```

后端已精确允许该 Origin。Origin 不包含路径，也不能带结尾 `/`。

切换期间后端也暂时允许原始 Pages 域名 `https://zerp-4gu.pages.dev`。稳定运行后可以从白名单移除该兼容 Origin。

Cloudflare Pages 的其他预览部署域名不在生产白名单内。需要联调预览部署时，应向后端提供完整的预览 Origin，不能假设 `*.pages.dev` 会被统一放行。

自定义前端域名和 API 都位于 `https://*.bytesucceed.com`，因此属于同站部署。当前后端的 `SameSite=Lax` 会话 Cookie 可以用于正式前端，不需要为了生产环境改成第三方 Cookie。

## 2. 本地开发配置

后端当前允许以下两个本地 Origin：

```text
http://localhost:5173
http://127.0.0.1:5173
```

推荐开发者统一在端口 `5173` 启动 Vite：

```bash
npm run dev -- --host localhost --port 5173
```

本地直连后端时配置：

```env
VITE_API_BASE_URL=https://zerp-api.bytesucceed.com
```

协议、主机或端口任一不同都会产生不同的 Origin。例如以下地址当前不会被放行：

```text
http://localhost:5174
http://192.168.1.10:5173
```

如必须使用其他地址，需要提前把完整 Origin 加入后端白名单。

### 推荐：使用 Vite 开发代理

涉及登录 Cookie 时，推荐让浏览器请求本地同源 `/api`，再由 Vite 转发到正式 API。前端开发环境配置：

```env
VITE_API_BASE_URL=/api
```

`vite.config.ts` 示例：

```ts
import { defineConfig } from 'vite'

export default defineConfig({
  server: {
    host: 'localhost',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: 'https://zerp-api.bytesucceed.com',
        changeOrigin: true,
        secure: true,
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
})
```

`strictPort: true` 可以避免 `5173` 被占用后 Vite 自动切换到未加入 CORS 白名单的其他端口。

## 3. 请求封装

所有业务 API 使用 `POST application/json`。前端必须启用 Cookie 凭证：

```ts
type ApiEnvelope<T> = {
  code: number
  message: string
  data: T | null
  requestId: string
}

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL

export async function postApi<T>(
  path: string,
  body: unknown,
  csrfToken?: string,
): Promise<ApiEnvelope<T>> {
  if (!API_BASE_URL) {
    throw new Error('VITE_API_BASE_URL is not configured')
  }

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }
  if (csrfToken) {
    headers['X-CSRF-Token'] = csrfToken
  }

  const response = await fetch(`${API_BASE_URL}${path}`, {
    method: 'POST',
    headers,
    credentials: 'include',
    body: JSON.stringify(body),
  })

  if (!response.ok) {
    throw new Error(`API transport error: HTTP ${response.status}`)
  }

  return (await response.json()) as ApiEnvelope<T>
}
```

注意：

- `credentials: 'include'` 必须用于登录、恢复会话和所有受保护请求。
- 登录和恢复会话成功后，把响应中的 `csrfToken` 保存在当前应用会话内。
- 受保护请求及退出登录必须发送 `X-CSRF-Token`。
- 不要把密码、Cookie 或 CSRF Token 写入日志、监控事件或错误上报。
- 建议仅在内存状态中保存 CSRF Token，刷新页面后通过会话恢复接口重新取得。

## 4. 登录与会话流程

以下示例使用的会话类型：

```ts
type SessionData = {
  user: {
    id: string
    username: string
    displayName: string
  }
  csrfToken: string
  permissions: string[]
}
```

### 登录

```ts
const result = await postApi<SessionData>('/app/user/signin', {
  username,
  password,
})
```

登录成功时：

- 后端通过 `Set-Cookie` 写入 HttpOnly 会话 Cookie；
- `data.csrfToken` 供后续受保护请求使用；
- `data.permissions` 是当前用户可调用的 API 路径数组。

### 恢复会话

应用启动或页面刷新后调用：

```ts
const result = await postApi<SessionData>('/app/user/session', {})
```

业务码 `1001` 表示未登录或会话已失效，前端应清理用户状态并进入登录页。

### 受保护请求

```ts
const result = await postApi<unknown>(
  '/app/user/query',
  { page: 1, pageSize: 20, filters: {}, sort: [] },
  csrfToken,
)
```

### 退出登录

```ts
await postApi('/app/user/signout', {}, csrfToken)
```

无论退出请求结果如何，前端都应在最终清理本地用户资料、权限和 CSRF Token。

## 5. 响应与错误处理

业务请求通常返回 HTTP 200，前端必须根据响应包络的 `code` 判断业务结果：

```json
{
  "code": 0,
  "message": "ok",
  "data": {},
  "requestId": "01J..."
}
```

当前公共业务码：

| `code` | 含义 | 前端建议 |
| ---: | --- | --- |
| `0` | 成功 | 使用 `data` |
| `1001` | 未登录或会话失效 | 清理会话并进入登录页 |
| `1002` | 无操作权限 | 显示无权限提示，不自动重试 |
| `2001` | 参数校验失败或数据不存在 | 显示校验提示 |
| `3001` | 并发更新或数据冲突 | 提示刷新后重试 |
| `5000` | 服务内部错误 | 显示通用错误并保留 `requestId` |

以下属于传输层错误，不保证返回业务包络：

- CORS 拒绝；
- TLS 或网络连接失败；
- Cloudflare 或上游服务不可用；
- HTTP 404、502、503、504。

前端错误上报应包含 `requestId`、API 路径和业务码，但不得包含密码、Cookie、CSRF Token 或完整敏感请求体。

## 6. 健康检查

无需登录即可检查：

```text
GET https://zerp-api.bytesucceed.com/healthz
GET https://zerp-api.bytesucceed.com/readyz
```

- `/healthz` 表示 API 进程存活；
- `/readyz` 表示 API 已连接数据库。

健康检查成功不代表业务 API 已部署，也不代表当前用户具有业务权限。

## 7. 当前联调状态

截至 2026-07-22：

- HTTPS、`/healthz` 和 `/readyz` 正常；
- `zerp.bytesucceed.com`、原始 Pages 域名、`localhost:5173` 和 `127.0.0.1:5173` 的 CORS 预检已放行；
- 自定义域名已经可访问，但当前前端构建仍未设置 `VITE_API_BASE_URL`，需要配置后重新部署；
- 线上 APP 业务路由仍返回 404，需等待后端部署当前版本并执行 APP 数据库迁移；
- 线上 BOB 路由尚未实现；
- 自定义前端域名与 API 同属 `bytesucceed.com`，当前 `SameSite=Lax` 会话 Cookie 可以用于生产登录链路。

前端在收到后端“业务 API 已部署并完成 Cookie 联调”的通知前，可以先完成环境变量、请求封装、业务码处理和登录页面交互，但不能把健康检查成功视为登录链路验收通过。
