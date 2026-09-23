<script setup lang="ts">
defineProps<{ name: string }>();
const emit = defineEmits<{ detail: [] }>();
</script>

<template>
  <button type="button" class="monitor-name" :title="name" :aria-label="`查看 ${name} 详情`" @click.stop="emit('detail')">{{ name }}</button>
</template>

<style scoped>
.monitor-name { min-width: 0; padding: 0; border: 0; background: transparent; color: var(--ct-ink); font: inherit; font-size: 13px; font-weight: 600; text-align: left; cursor: pointer; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.monitor-name:hover { color: var(--ct-primary); text-decoration: underline; text-underline-offset: 3px; }
.monitor-name:focus-visible { outline: 2px solid var(--ct-primary); outline-offset: 2px; border-radius: 2px; }
@media (max-width: 900px) { .monitor-name { min-height: 36px; } }
</style>
