import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '')
  const apiTarget = env.CT_DEV_API_TARGET || 'http://127.0.0.1:18081'
  return { base: '/', plugins: [vue()], build: { outDir: '../../../web/dist/desktop', emptyOutDir: true }, server: { host: '127.0.0.1', proxy: { '/api': { target: apiTarget, changeOrigin: true } } } }
})
