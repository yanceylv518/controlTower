<script setup lang="ts">
import { computed } from 'vue'
import { modelProviderIcons } from '../utils/modelProviderIcons'
const props = defineProps<{ name: string }>()
// Only bundled, trusted SVG assets are rendered; never inject API-provided markup.
const svg = computed(() => {
  const source = Object.prototype.hasOwnProperty.call(modelProviderIcons, props.name) ? modelProviderIcons[props.name] : undefined
  return source ? decodeURIComponent(source.slice('data:image/svg+xml,'.length)) : ''
})
</script>
<template><span class="model-provider-icon" role="img" :aria-label="name" v-html="svg" /></template>
<style scoped>
.model-provider-icon{display:inline-flex;align-items:center;justify-content:center;width:18px;height:18px;flex:none;color:var(--ct-ink);vertical-align:middle}
.model-provider-icon :deep(svg){display:block;width:100%;height:100%}
</style>
