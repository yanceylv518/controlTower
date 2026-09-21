<script lang="ts">
export type UserPickerOption = {
  id: number
  username: string
  display_name: string
}
</script>
<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import type { ReadonlyUser } from '@ct/shared'
import { passthrough } from '../api'

type UserPickerOption = Pick<ReadonlyUser, 'id' | 'username' | 'display_name'>

const props = withDefaults(defineProps<{
  modelValue: string
  site?: string
  placeholder?: string
  ariaLabel?: string
  disabled?: boolean
}>(), {
  placeholder: '用户名称',
  ariaLabel: '用户名称',
  disabled: false,
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
  select: [value: UserPickerOption | null]
  submit: []
}>()

const root = ref<HTMLElement | null>(null)
const input = ref<HTMLInputElement | null>(null)
const dropdown = ref<HTMLElement | null>(null)
const inputValue = ref(props.modelValue)
const options = ref<UserPickerOption[]>([])
const open = ref(false)
const loading = ref(false)
const highlightedIndex = ref(-1)
const dropdownStyle = ref<Record<string, string>>({})
let debounceTimer: number | undefined
let activeController: AbortController | undefined
let searchSequence = 0

const highlightedID = computed(() => {
  const option = options.value[highlightedIndex.value]
  return option ? `user-picker-option-${option.id}` : undefined
})

// 统一使用显示名、用户名和 ID，避免同名用户在下拉列表中无法区分。
function primaryLabel(option: UserPickerOption) {
  return option.display_name || option.username || `用户 ${option.id}`
}
function secondaryLabel(option: UserPickerOption) {
  const username = option.username && option.username !== primaryLabel(option) ? `@${option.username}` : ''
  return username ? `${username} · ID ${option.id}` : `ID ${option.id}`
}

// Teleport 下拉菜单到 body，避免日志筛选行的横向滚动容器裁剪候选列表。
function updateDropdownPosition() {
  if (!open.value || !input.value || typeof window === 'undefined') return
  const rect = input.value.getBoundingClientRect()
  const margin = 8
  const viewportWidth = Math.max(0, window.innerWidth - margin * 2)
  const width = Math.min(Math.max(rect.width, 220), viewportWidth)
  const estimatedHeight = Math.min(320, Math.max(72, (options.value.length || 1) * 52 + 40))
  const height = dropdown.value?.offsetHeight || estimatedHeight
  const maxHeight = Math.min(320, Math.max(72, window.innerHeight - margin * 2))
  const below = window.innerHeight - rect.bottom - margin
  const above = rect.top - margin
  const top = below >= height || below >= above
    ? rect.bottom + margin
    : Math.max(margin, rect.top - height - margin)
  const left = Math.min(Math.max(margin, rect.left), Math.max(margin, window.innerWidth - width - margin))
  dropdownStyle.value = { left: `${left}px`, top: `${top}px`, width: `${width}px`, maxHeight: `${maxHeight}px` }
}

function cancelSearch() {
  searchSequence += 1
  activeController?.abort()
  activeController = undefined
  if (debounceTimer !== undefined) {
    window.clearTimeout(debounceTimer)
    debounceTimer = undefined
  }
  loading.value = false
}

// 防抖并取消过期查询，保证快速输入时最后一次关键词拥有结果优先级。
function scheduleSearch(value: string) {
  cancelSearch()
  const keyword = value.trim()
  if (!keyword) {
    options.value = []
    open.value = false
    return
  }
  open.value = true
  loading.value = true
  debounceTimer = window.setTimeout(() => { void searchUsers(keyword) }, 240)
  void nextTick(updateDropdownPosition)
}

async function searchUsers(keyword: string) {
  const sequence = ++searchSequence
  const controller = new AbortController()
  activeController = controller
  try {
    const result = await passthrough.users({ site: props.site || undefined, keyword, status: 1, limit: 20, offset: 0 }, controller.signal)
    if (sequence !== searchSequence || controller.signal.aborted) return
    options.value = result.items.map(({ id, username, display_name }) => ({ id, username, display_name }))
    highlightedIndex.value = options.value.length ? 0 : -1
    open.value = true
    await nextTick(updateDropdownPosition)
  } catch {
    // 搜索失败时保持输入框可继续编辑，不用错误提示打断日志筛选流程。
    if (sequence === searchSequence && !controller.signal.aborted) options.value = []
  } finally {
    if (sequence === searchSequence) {
      loading.value = false
      activeController = undefined
    }
  }
}

function handleInput(event: Event) {
  const value = (event.target as HTMLInputElement).value
  inputValue.value = value
  emit('update:modelValue', value)
  // 手动修改后不再沿用上一次选中的精确用户 ID。
  emit('select', null)
  highlightedIndex.value = -1
  scheduleSearch(value)
}

function handleFocus() {
  if (props.disabled) return
  if (inputValue.value.trim() && (options.value.length || loading.value)) {
    open.value = true
    void nextTick(updateDropdownPosition)
  }
}

function closeDropdown() {
  open.value = false
  highlightedIndex.value = -1
}

function handleClear(event: MouseEvent) {
  event.preventDefault()
  event.stopPropagation()
  inputValue.value = ''
  emit('update:modelValue', '')
  emit('select', null)
  options.value = []
  cancelSearch()
  closeDropdown()
  input.value?.focus()
}

function selectOption(option: UserPickerOption) {
  const value = option.username || option.display_name || String(option.id)
  inputValue.value = value
  emit('update:modelValue', value)
  emit('select', option)
  options.value = []
  cancelSearch()
  closeDropdown()
}

function moveHighlight(delta: number) {
  if (!options.value.length) return
  const count = options.value.length
  highlightedIndex.value = (highlightedIndex.value + delta + count) % count
  void nextTick(updateDropdownPosition)
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    if (!open.value && options.value.length) open.value = true
    if (options.value.length) {
      event.preventDefault()
      moveHighlight(1)
    }
    return
  }
  if (event.key === 'ArrowUp') {
    if (options.value.length) {
      event.preventDefault()
      if (!open.value) open.value = true
      moveHighlight(-1)
    }
    return
  }
  if (event.key === 'Enter') {
    if (open.value && highlightedIndex.value >= 0 && options.value[highlightedIndex.value]) {
      event.preventDefault()
      selectOption(options.value[highlightedIndex.value])
    } else {
      emit('submit')
    }
    return
  }
  if (event.key === 'Escape' && open.value) {
    event.preventDefault()
    closeDropdown()
  }
}

function handleDocumentPointerdown(event: PointerEvent) {
  const target = event.target
  if (target instanceof Node && (root.value?.contains(target) || dropdown.value?.contains(target))) return
  closeDropdown()
}

watch(() => props.modelValue, value => {
  if (value !== inputValue.value) inputValue.value = value
})
watch(() => props.site, () => {
  cancelSearch()
  options.value = []
  closeDropdown()
})

onMounted(() => {
  document.addEventListener('pointerdown', handleDocumentPointerdown)
  window.addEventListener('resize', updateDropdownPosition)
  window.addEventListener('scroll', updateDropdownPosition, true)
})
onUnmounted(() => {
  cancelSearch()
  document.removeEventListener('pointerdown', handleDocumentPointerdown)
  window.removeEventListener('resize', updateDropdownPosition)
  window.removeEventListener('scroll', updateDropdownPosition, true)
})
</script>

<template>
  <div ref="root" class="user-name-picker">
    <input
      ref="input"
      class="user-name-picker-input"
      :value="inputValue"
      :placeholder="placeholder"
      :aria-label="ariaLabel"
      :disabled="disabled"
      :aria-expanded="open"
      aria-autocomplete="list"
      aria-haspopup="listbox"
      :aria-controls="open ? 'user-name-picker-options' : undefined"
      :aria-activedescendant="open ? highlightedID : undefined"
      role="combobox"
      autocomplete="off"
      @input="handleInput"
      @focus="handleFocus"
      @keydown="handleKeydown"
    />
    <button v-if="inputValue" type="button" class="user-name-picker-clear" aria-label="清空用户名称" title="清空" @pointerdown="handleClear">×</button>
    <Teleport to="body">
      <div v-if="open" ref="dropdown" id="user-name-picker-options" class="user-name-picker-dropdown" role="listbox" :style="dropdownStyle">
        <div v-if="loading" class="user-name-picker-state">正在搜索用户…</div>
        <template v-else-if="options.length">
          <button
            v-for="(option, index) in options"
            :id="`user-picker-option-${option.id}`"
            :key="option.id"
            type="button"
            role="option"
            class="user-name-picker-option"
            :class="{ 'is-highlighted': index === highlightedIndex }"
            :aria-selected="index === highlightedIndex"
            @mouseenter="highlightedIndex = index"
            @pointerdown.prevent="selectOption(option)"
          >
            <span class="user-name-picker-option-primary">{{ primaryLabel(option) }}</span>
            <span class="user-name-picker-option-secondary">{{ secondaryLabel(option) }}</span>
          </button>
        </template>
        <div v-else class="user-name-picker-state">未找到匹配用户</div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.user-name-picker {
  position: relative;
  display: block;
  height: 34px;
  min-width: 0;
  width: 100%;
  overflow: hidden;
  border-radius: 8px;
  background: var(--rc35-surface-2, #f3f6fb);
  box-shadow: 0 0 0 1px var(--rc35-line, #e5e9f0) inset;
  transition: box-shadow .16s ease, background .16s ease;
}
.user-name-picker-input {
  width: 100%;
  height: 34px;
  min-width: 0;
  padding: 0 28px 0 11px;
  border: 0;
  border-radius: 8px;
  outline: 0;
  background: transparent;
  color: var(--rc35-ink, #202b3d);
  font: inherit;
  font-size: 14px;
  line-height: 34px;
}
.user-name-picker-input::placeholder { color: var(--rc35-ink-3, #98a2b3); }
.user-name-picker:focus-within { border-radius: 8px; box-shadow: 0 0 0 1px var(--rc35-blue, #4676e8) inset; }
.user-name-picker-input:disabled { cursor: not-allowed; opacity: .62; }
.user-name-picker-clear {
  position: absolute;
  top: 50%;
  right: 6px;
  width: 22px;
  height: 22px;
  transform: translateY(-50%);
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--rc35-ink-3, #98a2b3);
  cursor: pointer;
  font-size: 18px;
  line-height: 20px;
}
.user-name-picker-clear:hover { background: var(--rc35-line, #e5e9f0); color: var(--rc35-ink, #202b3d); }
.user-name-picker-dropdown {
  position: fixed;
  box-sizing: border-box;
  z-index: 3100;
  max-height: min(320px, calc(100vh - 16px));
  overflow-y: auto;
  padding: 4px;
  border: 1px solid var(--ct-line, #dfe5ee);
  border-radius: 8px;
  background: var(--ct-surface, #fff);
  box-shadow: var(--ct-shadow-popover, 0 8px 28px rgb(20 35 56 / 14%));
  color: var(--ct-ink, #202b3d);
}
.user-name-picker-option {
  display: flex;
  width: 100%;
  min-height: 44px;
  flex-direction: column;
  align-items: flex-start;
  justify-content: center;
  gap: 2px;
  padding: 6px 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  cursor: pointer;
  text-align: left;
}
.user-name-picker-option:hover,
.user-name-picker-option.is-highlighted { background: var(--ct-accent-weak, #eaf0fd); }
.user-name-picker-option-primary { max-width: 100%; overflow: hidden; font-size: 13px; font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
.user-name-picker-option-secondary { max-width: 100%; overflow: hidden; color: var(--ct-ink-3, #5c6d84); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.user-name-picker-state { padding: 12px 10px; color: var(--ct-ink-3, #5c6d84); font-size: 12px; text-align: center; }

/* 下拉层脱离日志页局部容器后仍使用全局主题，并在窄视口内保持可见。 */
@media (max-width: 560px) {
  .user-name-picker-dropdown { border-radius: 7px; }
  .user-name-picker-option { min-height: 42px; padding-inline: 8px; }
}
</style>
