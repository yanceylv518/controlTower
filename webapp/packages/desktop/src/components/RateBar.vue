<script setup lang="ts">defineProps<{ value: number | null; tone?: 'primary'|'danger'|'success' }>();</script>
<template><div class="rate"><el-progress :percentage="Math.round((value || 0) * 100)" :status="tone === 'danger' ? 'exception' : tone === 'success' ? 'success' : undefined" :stroke-width="8" /><span>{{ value == null ? '—' : `${(value*100).toFixed(1)}%` }}</span></div></template>
