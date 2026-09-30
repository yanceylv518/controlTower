<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

type GroupSelection = { kind: "all" } | { kind: "group"; name: string };
type GroupOption = { key: string; label: string; value: string | null };

const props = defineProps<{
  groups: string[];
  modelValue: GroupSelection | null;
  open: boolean;
}>();
const emit = defineEmits<{
  select: [value: string | null];
  clear: [];
  "update:open": [value: boolean];
}>();

const control = ref<HTMLElement | null>(null);
const search = ref("");
const hasSearchInput = ref(false);
const activeIndex = ref(-1);
const selectedLabel = computed(() => {
  if (props.modelValue?.kind === "all") return "所有分组";
  return props.modelValue?.kind === "group" ? props.modelValue.name : "";
});
const inputValue = computed(() => props.open && hasSearchInput.value ? search.value : selectedLabel.value);
const options = computed<GroupOption[]>(() => {
  const query = hasSearchInput.value ? search.value.trim().toLocaleLowerCase() : "";
  const groups = props.groups.filter(group => !query || group.toLocaleLowerCase().includes(query));
  return [
    { key: "all", label: "所有分组", value: null },
    ...groups.map(group => ({ key: `group-${group}`, label: group, value: group })),
  ];
});
const activeOptionID = computed(() => activeIndex.value < 0 ? undefined : optionID(activeIndex.value));

function optionID(index: number) {
  return `tuning-group-filter-option-${index}`;
}

function openOptions() {
  if (props.open) return;
  search.value = "";
  hasSearchInput.value = false;
  activeIndex.value = -1;
  emit("update:open", true);
}

function closeOptions() {
  if (!props.open) return;
  search.value = "";
  hasSearchInput.value = false;
  activeIndex.value = -1;
  emit("update:open", false);
}

function updateSearch(value: string | number) {
  const nextValue = String(value);
  search.value = nextValue;
  hasSearchInput.value = true;
  if (props.modelValue && nextValue.length === 0) emit("clear");
  activeIndex.value = -1;
  if (!props.open) emit("update:open", true);
}

function focusInput(event: FocusEvent) {
  openOptions();
  void nextTick(() => {
    if (event.target instanceof HTMLInputElement) event.target.select();
  });
}

function clickInput(event: MouseEvent) {
  openOptions();
  if (event.target instanceof HTMLInputElement) event.target.select();
}

function isSelected(value: string | null) {
  return value === null
    ? props.modelValue?.kind === "all"
    : props.modelValue?.kind === "group" && props.modelValue.name === value;
}

function selectOption(option: GroupOption) {
  emit("select", option.value);
  closeOptions();
}

function moveActive(direction: 1 | -1) {
  if (!props.open) openOptions();
  if (!options.value.length) return;
  if (activeIndex.value < 0) {
    activeIndex.value = direction > 0 ? 0 : options.value.length - 1;
  } else {
    activeIndex.value = (activeIndex.value + direction + options.value.length) % options.value.length;
  }
  void nextTick(() => control.value?.querySelector(`#${optionID(activeIndex.value)}`)?.scrollIntoView({ block: "nearest" }));
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === "ArrowDown") {
    event.preventDefault();
    moveActive(1);
    return;
  }
  if (event.key === "ArrowUp") {
    event.preventDefault();
    moveActive(-1);
    return;
  }
  if (event.key === "Home" && props.open && options.value.length) {
    event.preventDefault();
    activeIndex.value = 0;
    return;
  }
  if (event.key === "End" && props.open && options.value.length) {
    event.preventDefault();
    activeIndex.value = options.value.length - 1;
    return;
  }
  if (event.key === "Escape" && props.open) {
    event.preventDefault();
    closeOptions();
    return;
  }
  if (event.key === "Enter" && props.open) {
    const exactMatch = search.value.trim()
      ? options.value.findIndex(option => option.value?.toLocaleLowerCase() === search.value.trim().toLocaleLowerCase())
      : -1;
    const index = activeIndex.value >= 0 ? activeIndex.value : exactMatch;
    if (index >= 0) {
      event.preventDefault();
      selectOption(options.value[index]);
    }
    return;
  }
  if ((event.key === " " || event.code === "Space") && props.open && activeIndex.value >= 0) {
    event.preventDefault();
    selectOption(options.value[activeIndex.value]);
  }
}

function clearSelection() {
  emit("clear");
  search.value = "";
  hasSearchInput.value = true;
  activeIndex.value = -1;
  emit("update:open", true);
}

function closeOnOutsidePointer(event: PointerEvent) {
  if (props.open && event.target instanceof Node && !control.value?.contains(event.target)) closeOptions();
}

function closeOnBlur(event: FocusEvent) {
  if (!props.open) return;
  if (event.relatedTarget instanceof Node && control.value?.contains(event.relatedTarget)) return;
  closeOptions();
}

watch(() => props.open, open => {
  if (!open) {
    search.value = "";
    hasSearchInput.value = false;
    activeIndex.value = -1;
  }
});
onMounted(() => document.addEventListener("pointerdown", closeOnOutsidePointer, true));
onBeforeUnmount(() => document.removeEventListener("pointerdown", closeOnOutsidePointer, true));
</script>

<template>
  <div ref="control" class="tuning-group-filter">
    <el-input
      :model-value="inputValue"
      clearable
      role="combobox"
      aria-label="搜索分组"
      aria-autocomplete="list"
      aria-controls="tuning-group-filter-list"
      :aria-expanded="open"
      :aria-activedescendant="activeOptionID"
      placeholder="分组"
      @focus="focusInput"
      @click="clickInput"
      @input="updateSearch"
      @keydown="handleKeydown"
      @blur="closeOnBlur"
      @clear="clearSelection"
    />
    <div
      v-if="open"
      id="tuning-group-filter-list"
      class="tuning-group-filter-list"
      role="listbox"
      aria-label="分组筛选选项"
    >
      <button
        v-for="(option, index) in options"
        :id="optionID(index)"
        :key="option.key"
        type="button"
        class="tuning-group-filter-option"
        :class="{ 'is-active': activeIndex === index, 'is-selected': isSelected(option.value) }"
        role="option"
        :aria-selected="isSelected(option.value)"
        tabindex="-1"
        @mousedown.prevent
        @mouseenter="activeIndex = index"
        @click="selectOption(option)"
      >
        <span class="tuning-group-filter-check" aria-hidden="true">{{ isSelected(option.value) ? "✓" : "" }}</span>
        <span>{{ option.label }}</span>
      </button>
      <p v-if="search.trim() && options.length === 1" class="tuning-group-filter-empty">没有匹配分组</p>
    </div>
  </div>
</template>

<style scoped>
.tuning-group-filter { position: relative; width: 210px; max-width: calc(100vw - 32px); }
.tuning-group-filter-list {
  position: absolute;
  z-index: 40;
  inset: calc(100% + 4px) auto auto 0;
  width: max(100%, 248px);
  max-width: calc(100vw - 24px);
  max-height: min(320px, calc(100vh - 140px));
  overflow-x: hidden;
  overflow-y: auto;
  padding: 4px;
  border: 1px solid var(--ct-line);
  border-radius: 5px;
  background: var(--ct-surface);
  box-shadow: 0 8px 24px rgb(0 0 0 / 18%);
  overscroll-behavior: contain;
}
.tuning-group-filter-option {
  display: flex;
  width: 100%;
  min-height: 36px;
  align-items: center;
  gap: 8px;
  padding: 0 8px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--ct-ink-2);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.tuning-group-filter-option:hover,
.tuning-group-filter-option.is-active { background: var(--ct-surface-2); color: var(--ct-ink); }
.tuning-group-filter-option.is-selected { color: var(--ct-accent); }
.tuning-group-filter-option:focus-visible { outline: 2px solid var(--ct-accent); outline-offset: -2px; }
.tuning-group-filter-check { display: grid; width: 16px; height: 16px; flex: 0 0 16px; place-items: center; border: 1px solid var(--ct-line-strong); border-radius: 3px; color: var(--ct-accent); font-size: 12px; line-height: 1; }
.is-selected .tuning-group-filter-check { border-color: var(--ct-accent); }
.tuning-group-filter-empty { margin: 4px 0; color: var(--ct-ink-3); font-size: 12px; text-align: center; }
</style>
