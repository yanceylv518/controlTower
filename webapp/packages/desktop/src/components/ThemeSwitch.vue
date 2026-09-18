<script setup lang="ts">
import { computed } from 'vue'
import { Sunny, Moon, Monitor, Check } from '@element-plus/icons-vue'
import { useTheme, type ThemePreference, type ThemePalette, type ThemeFont } from '../composables/useTheme'
const { preference, palette, font, setTheme, setPalette, setFont } = useTheme()
const fonts = [
  { value: 'system', label: '系统默认' },
  { value: 'yahei', label: '微软雅黑' },
  { value: 'dengxian', label: '等线' },
  { value: 'simsun', label: '宋体' },
] as const
const currentFont = computed(() => fonts.find(option => option.value === font.value)!)
const palettes = [
  { value: 'blue', label: '石墨蓝', color: '#3566cd', surface: '#192332' },
  { value: 'graphite', label: '中性黑白', color: '#a1a1aa', surface: '#191919' },
  { value: 'jade', label: '翡翠青', color: '#19806e', surface: '#172724' },
  { value: 'violet', label: '鸢尾紫', color: '#7956bd', surface: '#231e30' },
] as const
const currentPalette = computed(() => palettes.find(option => option.value === palette.value)!)
function select(command: string) {
  if (command.startsWith('palette:')) setPalette(command.slice(8) as ThemePalette)
  else if (command.startsWith('font:')) setFont(command.slice(5) as ThemeFont)
  else setTheme(command as ThemePreference)
}
const options = [
  { value: 'light', label: '浅色', icon: Sunny },
  { value: 'dark', label: '深色', icon: Moon },
  { value: 'system', label: '跟随系统', icon: Monitor },
] as const
const current = computed(() => options.find(option => option.value === preference.value)!)
</script>

<template>
  <el-dropdown trigger="click" popper-class="shell-menu-popper" max-height="min(560px, calc(100vh - 90px))" @command="select">
    <button type="button" class="theme-switch" :aria-label="`主题设置，当前${current.label} · ${currentPalette.label} · ${currentFont.label}`" :title="`主题设置：${current.label} · ${currentPalette.label} · ${currentFont.label}`">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
        <path d="M12 3a9 9 0 1 0 0 18h1.2a2.3 2.3 0 0 0 1.6-3.9 1.3 1.3 0 0 1 .9-2.2H17a4 4 0 0 0 4-4c0-4.4-4-7.9-9-7.9Z" />
        <circle cx="7.5" cy="10" r="1" />
        <circle cx="11" cy="7" r="1" />
        <circle cx="15.5" cy="8" r="1" />
      </svg>
    </button>
    <template #dropdown>
      <el-dropdown-menu class="theme-menu">
        <el-dropdown-item disabled class="theme-section">明暗模式</el-dropdown-item>
        <el-dropdown-item v-for="option in options" :key="option.value" :command="option.value" :class="{ 'theme-selected': preference === option.value }">
          <el-icon><component :is="option.icon" /></el-icon>
          <span>{{ option.label }}</span>
          <el-icon class="theme-check" :style="{ visibility: preference === option.value ? 'visible' : 'hidden' }"><Check /></el-icon>
        </el-dropdown-item>
        <el-dropdown-item disabled divided class="theme-section">配色方案</el-dropdown-item>
        <el-dropdown-item v-for="option in palettes" :key="option.value" :command="`palette:${option.value}`" :class="{ 'theme-selected': palette === option.value }">
          <span class="theme-swatch" aria-hidden="true" :style="{ background: `linear-gradient(135deg, ${option.surface} 50%, ${option.color} 50%)` }" />
          <span>{{ option.label }}</span>
          <el-icon class="theme-check" :style="{ visibility: palette === option.value ? 'visible' : 'hidden' }"><Check /></el-icon>
        </el-dropdown-item>
        <el-dropdown-item disabled divided class="theme-section">界面字体</el-dropdown-item>
        <el-dropdown-item v-for="option in fonts" :key="option.value" :command="`font:${option.value}`" class="theme-font-option" :class="{ 'theme-selected': font === option.value }" :style="{ '--ct-font-preview': `var(--ct-font-${option.value})` }">
          <span class="theme-font-sample" aria-hidden="true">文</span>
          <span>{{ option.label }}</span>
          <el-icon class="theme-check" :style="{ visibility: font === option.value ? 'visible' : 'hidden' }"><Check /></el-icon>
        </el-dropdown-item>
      </el-dropdown-menu>
    </template>
  </el-dropdown>
</template>
