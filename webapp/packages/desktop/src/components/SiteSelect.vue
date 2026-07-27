<script setup lang="ts">
import { computed, onMounted } from "vue";
import { siteOf } from "@ct/shared";
import { useFiltersStore } from "../stores/filters";

const filters = useFiltersStore();
onMounted(() => void filters.loadInstances());
const sites = computed(() => [
  ...new Set(
    filters.instances.filter((item) => item.enabled).map((item) => siteOf(item)),
  ),
]);
</script>

<template>
  <el-select
    v-if="sites.length > 1"
    :model-value="filters.site_id"
    aria-label="站点"
    style="width: 180px"
    @update:model-value="filters.selectSite(String($event))"
  >
    <el-option v-for="site in sites" :key="site" :label="site" :value="site" />
  </el-select>
</template>
