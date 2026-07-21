<script setup lang="ts">
import { computed } from "vue";
const props = withDefaults(defineProps<{ values: Array<number | null | undefined>; color?: string; bars?: boolean }>(), { color: "#2f6fed", bars: false });
const clean = computed(() => props.values.map(value => value == null ? 0 : value));
const max = computed(() => Math.max(...clean.value, 1));
const points = computed(() => clean.value.map((value, index) => `${(index / Math.max(clean.value.length - 1, 1)) * 100},${28 - (value / max.value) * 24}`).join(" "));
</script>
<template>
  <svg class="mini-spark" viewBox="0 0 100 30" preserveAspectRatio="none" aria-hidden="true">
    <template v-if="bars">
      <rect v-for="(value, index) in clean" :key="index" :x="index * (100 / clean.length) + 1" :y="28 - (value / max) * 24" :width="Math.max(1, 100 / clean.length - 2)" :height="Math.max(1, (value / max) * 24)" rx="1" :fill="color" opacity=".8" />
    </template>
    <polyline v-else :points="points" fill="none" :stroke="color" stroke-width="2" vector-effect="non-scaling-stroke" />
  </svg>
</template>
