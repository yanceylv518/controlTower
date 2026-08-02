<script setup lang="ts">
import { computed, onMounted, watch } from "vue";
import { siteOf } from "@ct/shared";
import { useAuthStore } from "../stores/auth";
import { useFiltersStore } from "../stores/filters";

const auth = useAuthStore();
const filters = useFiltersStore();
const viewerSite = computed(() =>
  auth.user?.role === "viewer" ? auth.user.scope_site || "" : "",
);

function lockViewerSite() {
  if (viewerSite.value && filters.site_id !== viewerSite.value) {
    filters.selectSite(viewerSite.value);
  }
}

onMounted(async () => {
  await filters.loadInstances();
  lockViewerSite();
});
watch(viewerSite, lockViewerSite, { immediate: true });
const sites = computed(() => [
  ...new Set(
    filters.instances.filter((item) => item.enabled).map((item) => siteOf(item)),
  ),
]);
</script>

<template>
  <div
    v-if="auth.user?.role === 'viewer'"
    class="fixed-site"
    aria-label="固定站点"
    title="该站点由查看账号的授权范围固定"
  >
    {{ viewerSite || filters.site_id }}
  </div>
  <el-select
    v-else-if="sites.length > 1"
    :model-value="filters.site_id"
    aria-label="站点"
    style="width: 180px"
    @update:model-value="filters.selectSite(String($event))"
  >
    <el-option v-for="site in sites" :key="site" :label="site" :value="site" />
  </el-select>
</template>

<style scoped>
.fixed-site {
  box-sizing: border-box;
  width: 180px;
  height: 32px;
  padding: 0 11px;
  overflow: hidden;
  color: var(--el-text-color-regular);
  line-height: 30px;
  text-overflow: ellipsis;
  white-space: nowrap;
  background: var(--el-disabled-bg-color);
  border: 1px solid var(--el-disabled-border-color);
  border-radius: var(--el-border-radius-base);
  cursor: not-allowed;
}
</style>
