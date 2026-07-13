import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig({ base: '/next/', plugins: [vue()], build: { outDir: '../../../web/dist/desktop', emptyOutDir: true }, server: { proxy: { '/api': 'http://127.0.0.1:8080' } } })
