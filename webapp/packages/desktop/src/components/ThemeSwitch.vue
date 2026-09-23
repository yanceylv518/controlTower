<script setup lang="ts">
import { computed } from 'vue'
import { Sunny, Moon, Monitor, Check } from '@element-plus/icons-vue'
import { useTheme, themePresets, type ThemePreference, type ThemePalette, type ThemeFont } from '../composables/useTheme'
const { preference, palette, font, setTheme, setPalette, setFont } = useTheme()
const fonts = [
  { value: 'system', label: '系统默认' },
  { value: 'yahei', label: '微软雅黑' },
  { value: 'dengxian', label: '等线' },
  { value: 'simsun', label: '宋体' },
] as const
const currentFont = computed(() => fonts.find(option => option.value === font.value)!)
const palettes = [
  ...themePresets,
  { value: 'blue', label: '石墨蓝', color: '#3566cd', surface: '#1c222b' },
  { value: 'graphite', label: '中性黑白', color: '#a1a1aa', surface: '#202020' },
  { value: 'jade', label: '翡翠青', color: '#19806e', surface: '#1d2522' },
  { value: 'violet', label: '鸢尾紫', color: '#7956bd', surface: '#232027' },
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
  <el-dropdown trigger="click" popper-class="shell-menu-popper theme-panel-popper" max-height="calc(100dvh - 80px)" @command="select">
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
        <el-dropdown-item v-for="option in options" :key="option.value" :command="option.value" class="theme-mode-option" :class="{ 'theme-selected': preference === option.value }">
          <el-icon><component :is="option.icon" /></el-icon>
          <span>{{ option.label }}</span>
          <el-icon class="theme-check" :style="{ visibility: preference === option.value ? 'visible' : 'hidden' }"><Check /></el-icon>
        </el-dropdown-item>
        <el-dropdown-item disabled class="theme-section">配色方案</el-dropdown-item>
        <el-dropdown-item v-for="option in palettes" :key="option.value" :command="`palette:${option.value}`" :class="{ 'theme-selected': palette === option.value }">
          <span class="theme-swatch" aria-hidden="true" :style="{ background: `linear-gradient(135deg, ${option.surface} 50%, ${option.color} 50%)` }" />
          <span>{{ option.label }}</span>
          <el-icon class="theme-check" :style="{ visibility: palette === option.value ? 'visible' : 'hidden' }"><Check /></el-icon>
        </el-dropdown-item>
        <el-dropdown-item disabled class="theme-section">界面字体</el-dropdown-item>
        <el-dropdown-item v-for="option in fonts" :key="option.value" :command="`font:${option.value}`" class="theme-font-option" :class="{ 'theme-selected': font === option.value }" :style="{ '--ct-font-preview': `var(--ct-font-${option.value})` }">
          <span class="theme-font-sample" aria-hidden="true">文</span>
          <span>{{ option.label }}</span>
          <el-icon class="theme-check" :style="{ visibility: font === option.value ? 'visible' : 'hidden' }"><Check /></el-icon>
        </el-dropdown-item>
      </el-dropdown-menu>
    </template>
  </el-dropdown>
</template>

<style>
/* Keep the existing dropdown selection and keyboard behavior in a compact grid. */
.shell-menu-popper.theme-panel-popper .theme-menu {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  gap: 6px;
  box-sizing: border-box;
  width: min(420px, calc(100vw - 24px));
  padding: 12px;
}
.shell-menu-popper.theme-panel-popper .theme-menu .el-dropdown-menu__item {
  grid-column: span 3;
  min-width: 0;
  min-height: 36px;
  padding: 7px 9px;
  gap: 7px;
  border: 1px solid var(--ct-line);
  background: var(--ct-surface);
  color: var(--ct-ink-2);
  font-size: 13px;
  line-height: 20px;
  white-space: normal;
}
.shell-menu-popper.theme-panel-popper .theme-menu .theme-mode-option {
  grid-column: span 2;
  justify-content: center;
  padding-inline: 6px;
  gap: 5px;
  font-size: 12px;
}
.shell-menu-popper.theme-panel-popper .theme-menu .el-icon {
  flex: none;
  margin-right: 0;
}
.shell-menu-popper.theme-panel-popper .theme-menu .theme-check {
  width: 12px;
  margin-left: auto;
  font-size: 12px;
}
.shell-menu-popper.theme-panel-popper .theme-menu .theme-mode-option .theme-check {
  margin-left: 0;
}
.shell-menu-popper.theme-panel-popper .theme-menu .theme-section.is-disabled {
  grid-column: 1 / -1;
  min-height: 24px;
  padding: 0 2px 2px;
  border: 0;
  border-radius: 0;
  background: transparent;
  color: var(--ct-ink-3);
  font-size: 12px;
  letter-spacing: 0;
}
.shell-menu-popper.theme-panel-popper .theme-menu .theme-section:not(:first-child) {
  margin-top: 6px;
  padding-top: 10px;
  border-top: 1px solid var(--ct-line);
}
.shell-menu-popper.theme-panel-popper .theme-menu .theme-selected,
.shell-menu-popper.theme-panel-popper .theme-menu .el-dropdown-menu__item:not(.is-disabled):hover,
.shell-menu-popper.theme-panel-popper .theme-menu .el-dropdown-menu__item:not(.is-disabled):focus {
  border-color: color-mix(in srgb, var(--ct-accent) 50%, var(--ct-line));
  background: var(--ct-accent-weak);
  color: var(--ct-accent);
}
.shell-menu-popper.theme-panel-popper .theme-menu .el-dropdown-menu__item:focus-visible {
  outline: 2px solid var(--ct-accent);
  outline-offset: 1px;
}
@media (max-width: 420px) {
  .shell-menu-popper.theme-panel-popper .theme-menu { padding: 10px; }
  .shell-menu-popper.theme-panel-popper .theme-menu .el-dropdown-menu__item {
    min-height: 40px;
    padding-inline: 6px;
    gap: 5px;
  }
  .shell-menu-popper.theme-panel-popper .theme-menu .theme-section.is-disabled { min-height: 24px; }
}
</style>
