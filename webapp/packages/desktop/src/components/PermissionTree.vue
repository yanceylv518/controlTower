<script setup lang="ts">
import { computed, ref } from 'vue'
import { ArrowRight, Search } from '@element-plus/icons-vue'

interface PermissionOption { key: string; label: string; description: string }
const props = defineProps<{ modelValue: string[]; options: PermissionOption[]; allowFull: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const keyword = ref('')
const collapsed = ref<string[]>([])
const full = computed(() => props.modelValue.includes('*'))
const selectedCount = computed(() => props.options.filter(option => selected(option.key)).length)
const groupDefinitions = [
  { label: '总览与分析', keys: ['overview.read', 'monitor.samples', 'monitor.latency'] },
  { label: '监控分析', keys: ['monitor.customers', 'monitor.channels', 'monitor.models', 'monitor.runtime'] },
  { label: '数据查询', keys: ['data.usage', 'data.users', 'data.logs', 'logs.query'] },
  { label: '账单管理', keys: ['billing.users', 'billing.channels', 'billing.tasks', 'discounts.manage'] },
  { label: '系统管理', keys: ['tuning.manage', 'alerts.manage', 'notifications.manage', 'instances.manage', 'archive.manage', 'accounts.manage', 'models.manage', 'upstreams.manage', 'settings.manage', 'audits.read'] },
]
const groups = computed(() => {
  const options = new Map(props.options.map(option => [option.key, option]))
  const result: Array<{ label: string; children: PermissionOption[] }> = []
  for (const group of groupDefinitions) {
    const children = group.keys.flatMap(key => { const option = options.get(key); options.delete(key); return option ? [option] : [] })
    if (children.length) result.push({ label: group.label, children })
  }
  if (options.size) result.push({ label: '其他功能', children: [...options.values()] })
  return result
})
const visibleGroups = computed(() => {
  const query = keyword.value.trim().toLowerCase()
  return groups.value.map(group => ({ ...group, visible: group.children.filter(child => !query || `${group.label} ${child.label} ${child.description}`.toLowerCase().includes(query)) })).filter(group => group.visible.length)
})
function selected(key: string) { return full.value || props.modelValue.includes(key) }
function count(children: PermissionOption[]) { return children.filter(child => selected(child.key)).length }
function change(keys: string[], checked: boolean | string | number) {
  if (full.value) return
  const next = new Set(props.modelValue)
  keys.forEach(key => checked ? next.add(key) : next.delete(key))
  emit('update:modelValue', [...next])
}
function toggleFull(value: boolean | string | number) {
  const next = props.modelValue.filter(key => key !== '*')
  emit('update:modelValue', value ? ['*', ...next] : next)
}
function toggleGroup(label: string) { collapsed.value = collapsed.value.includes(label) ? collapsed.value.filter(item => item !== label) : [...collapsed.value, label] }
</script>

<template>
  <div class="permission-picker">
    <div v-if="allowFull" :class="['all-access', { active: full }]">
      <el-checkbox :model-value="full" @change="toggleFull"><span class="all-title">全部权限</span></el-checkbox>
      <span class="all-description">可访问所有功能，包含后续新增功能</span>
      <span v-if="full" class="all-badge">已开启</span>
    </div>
    <div class="permission-toolbar">
      <el-input v-model="keyword" :prefix-icon="Search" clearable placeholder="搜索功能权限" aria-label="搜索功能权限" size="small" />
      <span class="selection-count"><b>{{ selectedCount }}</b> / {{ options.length }} 已选</span>
      <el-button text size="small" @click="collapsed = collapsed.length ? [] : groups.map(group => group.label)">{{ collapsed.length ? '展开全部' : '收起全部' }}</el-button>
    </div>
    <div class="permission-groups" aria-label="功能权限分组">
      <section v-for="group in visibleGroups" :key="group.label" :class="['permission-card', { wide: group.children.length > 1, chosen: count(group.children) > 0 }]">
        <div class="group-header">
          <el-checkbox :model-value="count(group.children) === group.children.length" :indeterminate="count(group.children) > 0 && count(group.children) < group.children.length" :disabled="full" :aria-label="`全选${group.label}`" @change="(value: boolean | string | number) => change(group.children.map(child => child.key), value)" />
          <button type="button" class="group-toggle" :aria-expanded="!collapsed.includes(group.label) || Boolean(keyword)" @click="toggleGroup(group.label)">
            <span>{{ group.label }}</span><span class="group-meta">{{ count(group.children) }}/{{ group.children.length }}</span>
            <el-icon :class="{ expanded: !collapsed.includes(group.label) || Boolean(keyword) }"><ArrowRight /></el-icon>
          </button>
        </div>
        <div v-show="!collapsed.includes(group.label) || Boolean(keyword)" class="group-children">
          <el-checkbox v-for="option in group.visible" :key="option.key" :model-value="selected(option.key)" :disabled="full" :title="option.description" :class="['permission-option', { selected: selected(option.key) }]" @change="(value: boolean | string | number) => change([option.key], value)">{{ option.label }}</el-checkbox>
        </div>
      </section>
      <div v-if="!visibleGroups.length" class="permission-empty">没有找到匹配的权限</div>
    </div>
  </div>
</template>

<style scoped>
.permission-picker{width:100%;color:var(--el-text-color-primary)}
.all-access{display:flex;align-items:center;gap:12px;padding:10px 14px;border:1px solid var(--el-border-color-lighter);border-radius:8px;background:var(--el-fill-color-extra-light);transition:background .15s,border-color .15s}
.all-access.active{background:var(--el-color-primary-light-9);border-color:var(--el-color-primary-light-5)}
.all-access .el-checkbox{margin-right:0;height:24px}.all-title{font-weight:600;font-size:13px}.all-description{font-size:12px;color:var(--el-text-color-secondary);line-height:1.5}.all-badge{margin-left:auto;font-size:11px;white-space:nowrap;color:var(--el-color-primary)}
.permission-toolbar{display:flex;align-items:center;gap:12px;margin:14px 0 10px}.permission-toolbar>.el-input{flex:1;min-width:100px}.permission-toolbar :deep(.el-input__wrapper){background:var(--el-fill-color-extra-light);box-shadow:none;min-height:30px}.permission-toolbar :deep(.el-input__wrapper.is-focus){box-shadow:0 0 0 1px var(--el-color-primary) inset}
.selection-count{font-size:12px;white-space:nowrap;color:var(--el-text-color-secondary)}.selection-count b{font-weight:600;color:var(--el-color-primary)}.permission-toolbar>.el-button{padding:6px 0;height:28px;font-size:12px}
.permission-groups{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));align-content:start;gap:10px;max-height:min(48vh,430px);overflow:auto;padding:1px 3px 1px 1px;scrollbar-width:thin}
.permission-card{border:1px solid var(--el-border-color-lighter);border-radius:8px;overflow:hidden;background:var(--el-bg-color)}.permission-card.wide{grid-column:1/-1}.permission-card.chosen{border-color:var(--el-color-primary-light-7)}
.group-header{display:flex;align-items:center;gap:8px;padding:7px 12px;background:var(--el-fill-color-extra-light)}.group-header>.el-checkbox{margin:0;height:24px}.group-toggle{display:flex;align-items:center;gap:8px;width:100%;padding:0;border:0;background:none;text-align:left;color:inherit;cursor:pointer;font:inherit;min-height:24px}.group-toggle>span:first-child{font-size:13px;font-weight:600}.group-toggle:focus-visible{outline:2px solid var(--el-color-primary);outline-offset:2px;border-radius:3px}.group-meta{margin-left:auto;font-size:11px;font-weight:400;color:var(--el-text-color-secondary)}.group-toggle .el-icon{font-size:11px;color:var(--el-text-color-placeholder);transition:transform .15s}.group-toggle .expanded{transform:rotate(90deg)}
.group-children{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:2px 8px;padding:8px 10px 8px 20px}.permission-card:not(.wide) .group-children{grid-template-columns:1fr}
.permission-option{margin:0;padding:0 7px;min-width:0;height:32px;border-radius:5px;transition:background .15s}.permission-option:hover{background:var(--el-fill-color-light)}.permission-option.selected{background:var(--el-color-primary-light-9)}.permission-option :deep(.el-checkbox__label){font-size:12px;overflow:hidden;text-overflow:ellipsis}.permission-empty{grid-column:1/-1;padding:32px 12px;text-align:center;font-size:13px;color:var(--el-text-color-secondary)}
@media(max-width:480px){.all-access{flex-wrap:wrap;gap:4px 10px}.all-description{flex-basis:100%;padding-left:22px}.permission-groups{grid-template-columns:1fr}.permission-toolbar{gap:8px}.group-children{grid-template-columns:1fr}.permission-card.wide{grid-column:auto}}
</style>
