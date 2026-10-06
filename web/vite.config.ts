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
    rollupOptions: {
      output: {
        /**
         * 生产拆包（2026-10-06）：拆前 react/react-dom/antd/react-router/echarts 全部并入
         * 首屏 index chunk（raw 1.17MB / gzip 383KB），任一依赖升级即让全量缓存失效。
         *
         * ⚠️ 实测教训（两次构建对比，勿再试）：**不要**把 antd / @ant-design / rc-* 列进
         * manualChunks。antd 的 `export *` 聚合入口一旦被固定成 chunk 边界，Rollup 就不能
         * 再剔除未被引用的组件导出，tree-shaking 失效：
         *   - 只拆 antd 本体       → vendor-antd 1208KB，index 179KB
         *   - 连 icons/rc-* 一起拆 → vendor-antd 1261KB，index 156KB
         *   - 不拆（本配置）       → index 933KB（antd 已 tree-shake 到约 730KB）
         * 三种拆法的 antd 代码总量 1208KB > 933KB > 1261KB，故 antd 留给默认策略最省。
         *
         * 只拆真正"全站共享、与业务无关、版本极少变动"的两组，收益是**缓存**而非首屏字节：
         * 业务发版时用户只需重下 index，react 与 echarts 命中长缓存。
         */
        manualChunks(id: string) {
          const p = id.replace(/\\/g, '/')
          if (!p.includes('/node_modules/')) return undefined
          // zrender 是 echarts 的渲染内核，必须同 chunk，否则跨 chunk 循环引用
          if (p.includes('/echarts/') || p.includes('/zrender/')) return 'vendor-charts'
          // react 运行时：版本几乎不变，独立成 chunk 让业务发版不使其缓存失效
          if (/\/node_modules\/(react|react-dom|react-router|react-router-dom|scheduler)\//.test(p)) {
            return 'vendor-react'
          }
          return undefined
        },
      },
    },
    // 阈值按实际最大 chunk（含 antd 的业务 index，约 933KB）设定，不再用 1500 掩盖问题
    chunkSizeWarningLimit: 1000,
  },
})
