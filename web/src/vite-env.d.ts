/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** 生产环境 API 地址；开发环境走 Vite 代理，无需配置 */
  readonly VITE_API_BASE_URL?: string
  /** 开发环境后端代理目标，默认 http://localhost:8080 */
  readonly VITE_API_PROXY?: string
  /** 开发模式登录旁路：后端未接入时置 1 启用（仅 DEV 构建生效） */
  readonly VITE_AUTH_BYPASS?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
