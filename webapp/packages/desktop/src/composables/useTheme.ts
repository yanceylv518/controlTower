import { computed, readonly, ref } from 'vue'

export type ThemePreference = 'light' | 'dark' | 'system'
export type ThemePalette = 'blue' | 'graphite' | 'jade' | 'violet'
export type ThemeFont = 'system' | 'yahei' | 'dengxian' | 'simsun'
const fontKey = 'ct.theme.font'
const font = ref<ThemeFont>('system')
const key = 'ct.theme'
const paletteKey = 'ct.theme.palette'
const palette = ref<ThemePalette>('blue')
const preference = ref<ThemePreference>('system')
const resolved = ref<'light' | 'dark'>('light')
let media: MediaQueryList | undefined
let initialized = false
const valid = (value: unknown): value is ThemePreference => value === 'light' || value === 'dark' || value === 'system'
const validPalette = (value: unknown): value is ThemePalette => ['blue', 'graphite', 'jade', 'violet'].includes(String(value))
const validFont = (value: unknown): value is ThemeFont => ['system', 'yahei', 'dengxian', 'simsun'].includes(String(value))
const signature = computed(() => `${resolved.value}:${palette.value}:${font.value}`)

function apply() {
  resolved.value = preference.value === 'system' ? (media?.matches ? 'dark' : 'light') : preference.value
  document.documentElement.dataset.theme = resolved.value
  document.documentElement.dataset.palette = palette.value
  document.documentElement.dataset.font = font.value
  document.documentElement.classList.toggle('dark', resolved.value === 'dark')
  document.documentElement.style.colorScheme = resolved.value
  document.documentElement.style.backgroundColor = ''
}

export function initializeTheme() {
  if (initialized) return
  initialized = true
  media = window.matchMedia('(prefers-color-scheme: dark)')
  try {
    const saved = localStorage.getItem(key)
    if (valid(saved)) preference.value = saved
    const savedPalette = localStorage.getItem(paletteKey)
    if (validPalette(savedPalette)) palette.value = savedPalette
    const savedFont = localStorage.getItem(fontKey)
    if (validFont(savedFont)) font.value = savedFont
  } catch { /* Private browsing can disable storage; switching still works. */ }
  apply()
  media.addEventListener('change', () => { if (preference.value === 'system') apply() })
  window.addEventListener('storage', event => {
    if (event.key !== key && event.key !== paletteKey && event.key !== fontKey && event.key !== null) return
    if (event.key === key || event.key === null) preference.value = valid(event.newValue) ? event.newValue : 'system'
    if (event.key === paletteKey || event.key === null) palette.value = validPalette(event.newValue) ? event.newValue : 'blue'
    if (event.key === fontKey || event.key === null) font.value = validFont(event.newValue) ? event.newValue : 'system'
    apply()
  })
}

export function setTheme(value: ThemePreference) {
  if (!valid(value)) return
  preference.value = value
  apply()
  try { localStorage.setItem(key, value) } catch { /* Keep this session's choice. */ }
}

export function useTheme() {
  return { preference: readonly(preference), palette: readonly(palette), font: readonly(font), resolvedTheme: readonly(resolved), themeSignature: signature, setTheme, setPalette, setFont }
}

export function setPalette(value: ThemePalette) {
  if (!validPalette(value)) return
  palette.value = value
  apply()
  try { localStorage.setItem(paletteKey, value) } catch { /* Keep this session's choice. */ }
}

export function setFont(value: ThemeFont) {
  if (!validFont(value)) return
  font.value = value
  apply()
  try { localStorage.setItem(fontKey, value) } catch { /* Keep this session's choice. */ }
}
