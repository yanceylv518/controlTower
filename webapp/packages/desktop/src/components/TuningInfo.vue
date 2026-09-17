<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
const props = withDefaults(defineProps<{ label: string; width?: number; triggerText?: string }>(), { width: 460 });
const open = ref(false), pinned = ref(false);
const host = ref<HTMLElement>(), body = ref<HTMLElement>();
let timer: ReturnType<typeof setTimeout> | undefined;
const clear = () => { if (timer) clearTimeout(timer); };
const show = () => { clear(); open.value = true; };
const leave = () => { clear(); timer = setTimeout(() => { if (!pinned.value) open.value = false; }, 200); };
const close = () => { clear(); open.value = false; pinned.value = false; };
const toggle = () => { if (pinned.value) close(); else { pinned.value = true; show(); } };
const outside = (event: Event) => { if (!host.value?.contains(event.target as Node) && !body.value?.contains(event.target as Node)) close(); };
const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') close(); };
const width = computed(() => props.width);
onMounted(() => { document.addEventListener('pointerdown', outside); document.addEventListener('focusin', outside); document.addEventListener('keydown', escape); });
onBeforeUnmount(() => { clear(); document.removeEventListener('pointerdown', outside); document.removeEventListener('focusin', outside); document.removeEventListener('keydown', escape); });
</script>
<template>
  <span ref="host" class="tuning-info-host">
    <el-popover :visible="open" :width="width" placement="bottom-start" popper-class="tuning-info-popover" :show-after="0">
      <template #reference><button type="button" class="tuning-info-button" :class="{'text-trigger':triggerText != null}" :aria-label="label" :aria-expanded="open" @mouseenter="show" @mouseleave="leave" @focus="show" @blur="leave" @click="toggle">{{ triggerText ?? 'i' }}</button></template>
      <div ref="body" class="tuning-info-body" @mouseenter="show" @mouseleave="leave" @focusin="show" @focusout="leave">
        <header><b>{{ label }}</b><button type="button" aria-label="关闭提示" @click="close">×</button></header>
        <slot />
        <small class="pin-hint">{{ pinned ? '已固定 · 点击外部或 Esc 关闭' : '点击可固定查看' }}</small>
      </div>
    </el-popover>
  </span>
</template>
<style scoped>
.tuning-info-host{display:inline-flex;vertical-align:middle}.tuning-info-button{display:grid;place-items:center;width:17px;height:17px;padding:0;border:1px solid #8b9cb4;border-radius:50%;color:#557499;background:transparent;font:600 11px Georgia,serif;cursor:pointer}.tuning-info-button:hover,.tuning-info-button:focus-visible{color:#3168e8;border-color:#3168e8;outline:2px solid #e7efff;outline-offset:2px}.tuning-info-body{max-height:65vh;overflow:auto;color:#465367;line-height:1.65}.tuning-info-body header{display:flex;justify-content:space-between;align-items:center;margin-bottom:12px;color:#18283d}.tuning-info-body header button{border:0;background:none;color:#74849a;font-size:21px;cursor:pointer}.pin-hint{display:block;margin-top:14px;color:#8591a4;font-size:11px}:global(.tuning-info-popover){max-width:calc(100vw - 32px);border-radius:8px!important;box-shadow:0 10px 32px #2036581c!important;padding:16px!important}
.tuning-info-button.text-trigger{display:inline-block;width:auto;height:auto;min-height:20px;border:0;border-radius:3px;font:inherit;color:inherit}.tuning-info-button.text-trigger:hover{color:#3168e8}
</style>
