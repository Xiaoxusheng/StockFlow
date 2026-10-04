import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const dirname = path.dirname(fileURLToPath(import.meta.url))

// 后端地址可通过环境变量覆盖：VITE_API_PROXY=http://localhost:8080
const apiProxyTarget = process.env.VITE_API_PROXY ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(dirname, 'src'),
    },
  },
  server: {
    port: Number(process.env.VITE_PORT) || 5173,
    host: true,
    proxy: {
      '/api': {
        target: apiProxyTarget,
        changeOrigin: true,
        // 代理故障可见化（默认静默 500）：连接失败/转发异常时在 dev server 终端打印
        configure(proxy) {
          proxy.on('error', (err: unknown, req, res) => {
            const e = err as { code?: string; message?: string }
            console.error('[vite proxy]', req.url, '→', apiProxyTarget, e.code ?? e.message)
            if ('writeHead' in res && typeof res.writeHead === 'function') {
              res.writeHead(502, { 'Content-Type': 'application/json' })
              res.end(JSON.stringify({ code: 'DEV_PROXY_ERROR', message: e.message }))
            }
          })
        },
      },
    },
  },
  build: {
    chunkSizeWarningLimit: 1500,
  },
})
