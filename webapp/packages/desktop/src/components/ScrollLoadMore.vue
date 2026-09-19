<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
const props = defineProps<{ loading: boolean; hasMore: boolean; error?: string; disabled?: boolean }>()
const emit = defineEmits<{ load: [] }>()
const sentinel = ref<HTMLElement>()
let observer: IntersectionObserver | undefined
function observe() {
  observer?.disconnect()
  if (sentinel.value) observer?.observe(sentinel.value)
}
onMounted(() => {
  observer = new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting) && !props.loading && !props.disabled && !props.error && props.hasMore) emit('load')
  }, { rootMargin: '0px 0px 180px 0px' })
  observe()
})
watch(() => [props.loading, props.hasMore, props.disabled], () => { void nextTick(observe) })
onBeforeUnmount(() => observer?.disconnect())
</script>
<template>
  <div ref="sentinel" class="scroll-load-more" aria-live="polite">
    <span v-if="loading">加载中…</span>
    <button v-else-if="error" type="button" :disabled="disabled" @click="emit('load')">{{ error }} · 点击重试</button>
    <button v-else-if="hasMore" type="button" :disabled="disabled" @click="emit('load')">继续下滑加载更多</button>
    <span v-else>已全部加载</span>
  </div>
</template>
<style scoped>
.scroll-load-more { min-height:56px;display:flex;align-items:center;justify-content:center;color:var(--ct-ink-3);font-size:13px;padding:8px; }
button { min-height:44px;padding:8px 16px;border:0;background:transparent;color:var(--ct-accent);font:inherit;cursor:pointer; }
</style>
