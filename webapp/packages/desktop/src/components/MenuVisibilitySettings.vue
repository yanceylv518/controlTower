<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { menuGroups, useMenuVisibilityStore } from '../stores/menuVisibility'
const menu = useMenuVisibilityStore()
const draft = ref<Record<string, boolean>>({})
const loaded = ref(false)
const saving = ref(false)
const paths = menuGroups.flatMap(group => group.items.map(item => item[0]))
const changed = computed(() => paths.some(path => draft.value[path] !== (menu.items[path] !== false)))
async function reload() {
  loaded.value = false
  await menu.load()
  if (!menu.error) { draft.value = Object.fromEntries(paths.map(path => [path, menu.items[path] !== false])); loaded.value = true }
}
async function save() {
  saving.value = true
  try { await menu.save(draft.value); ElMessage.success('已保存，对所有用户生效；其他页面将在 30 秒内同步。') }
  catch { ElMessage.error('保存未确认，请重新加载核对。') }
  finally { saving.value = false }
}
onMounted(reload)
</script>

<template>
  <section class="menu-settings" v-loading="menu.loading">
    <div class="menu-settings-heading">
      <div><h2>菜单显示</h2><p>控制所有用户的侧栏菜单。关闭后，管理员及拥有该菜单权限的用户也不可见；开启后仍按权限显示。</p></div>
      <el-button type="primary" :loading="saving" :disabled="!loaded || !changed || !!menu.error" @click="save">保存菜单设置</el-button>
    </div>
    <el-alert v-if="menu.error" :title="menu.error" type="warning" :closable="false" show-icon><el-button text @click="reload">重新加载</el-button></el-alert>
    <template v-if="loaded">
      <p class="menu-settings-note">仅控制菜单显示，不改变页面和接口权限。若隐藏“系统设置”，有权限的管理员仍可直接访问 /settings 恢复。</p>
      <div class="menu-settings-grid">
        <section v-for="group in menuGroups" :key="group.title" class="panel">
          <h3>{{ group.title }}</h3>
          <div v-for="[path, label] in group.items" :key="path" class="menu-settings-row">
            <span>{{ label }}</span><el-switch v-model="draft[path]" :aria-label="`${label}菜单显示`" :disabled="saving" inline-prompt active-text="显示" inactive-text="隐藏" />
          </div>
        </section>
      </div>
    </template>
  </section>
</template>

<style scoped>
.menu-settings-heading { display:flex; align-items:center; justify-content:space-between; gap:20px; margin-bottom:16px; }
h2 { margin:0 0 8px; font-size:16px; } p { margin:0; color:var(--ct-ink-2); font-size:13px; line-height:1.7; }
.menu-settings-note { margin-bottom:16px; }
.menu-settings-grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(260px,1fr)); gap:12px; align-items:start; }
.menu-settings-grid .panel { min-height:0; padding:16px; }
h3 { margin:0 0 8px; font-size:13px; color:var(--ct-ink-2); }
.menu-settings-row { display:flex; align-items:center; justify-content:space-between; gap:12px; padding:8px 0; border-top:1px solid var(--ct-line); font-size:13px; }
</style>
