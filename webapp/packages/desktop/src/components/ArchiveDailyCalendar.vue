<script setup lang="ts">
import { computed, ref } from 'vue'
import ArchiveStatusHelp from './ArchiveStatusHelp.vue'
import { formatArchiveCount, type ArchiveCalendarDay } from '../utils/logArchive'
const props = defineProps<{days: ArchiveCalendarDay[]; month: string; currentDate?: string; loading: boolean; paused: boolean; stale: boolean}>()
defineEmits<{select:[day:ArchiveCalendarDay]}>()
const helpVisible = ref(false)
const offset = computed(() => (new Date(`${props.month}-01T00:00:00+08:00`).getUTCDay() + 7) % 7)
const counts = computed(() => ({sealed:props.days.filter(d=>d.kind==='sealed').length, blocked:props.days.filter(d=>d.kind==='blocked').length, unknown:props.days.filter(d=>d.kind==='unknown').length}))
</script>

<template>
  <section class="panel daily-calendar" aria-label="每日日志状态日历" v-loading="loading">
    <div class="calendar-heading">
      <div class="calendar-title"><h3>每日日志状态</h3><span class="calendar-month">{{ month }} · 北京时间</span></div>
      <el-button text type="primary" size="small" @click="helpVisible = true">状态说明</el-button>
    </div>
    <div class="calendar-summary">
      <span>已封存 {{ counts.sealed }} 天</span><span>受阻 {{ counts.blocked }} 天</span>
      <el-tooltip v-if="counts.unknown > 0" content="待上报不代表没有日志。归档月表可能已有数据，正在等待每日处理状态上报。" placement="top" :show-after="200">
        <button type="button" class="unknown-help" @click="helpVisible = true">待上报 {{ counts.unknown }} 天 ⓘ</button>
      </el-tooltip>
      <span v-if="stale" class="paused-note">上报过期 / 读取失败，显示历史记录</span><span v-else-if="paused" class="paused-note">已暂停 / 等待暂停，保留最近记录</span>
      <span class="calendar-hint">点击日期查看数量与处理详情</span>
    </div>
    <el-dialog v-model="helpVisible" title="状态含义与流转" width="min(900px, 94vw)" append-to-body>
      <ArchiveStatusHelp expanded />
    </el-dialog>
    <div class="month-grid">
      <span v-for="label in ['一','二','三','四','五','六','日']" :key="label" class="weekday">{{ label }}</span>
      <span v-for="n in offset" :key="`empty-${n}`" />
      <button v-for="day in days" :key="day.date" type="button" :class="['date-box', `state-${day.kind}`, {processing:currentDate===day.date}]" :disabled="day.kind==='future'" :title="`${day.date}：${day.label}；${day.reason}`" :aria-label="`${day.date}，${day.label}，已存档 ${formatArchiveCount(day.raw?.rows)} 条。查看详情`" @click="$emit('select',day)">
        <b>{{ Number(day.date.slice(8)) }}<small v-if="currentDate===day.date">最近处理</small></b>
        <span>{{ day.label }}</span><small v-if="day.kind!=='future'">{{ day.raw?.rows !== undefined ? `已存档 ${formatArchiveCount(day.raw.rows)} 条` : day.raw?.error_code ? '数量读取失败' : '数量待统计' }}</small>
      </button>
    </div>
    <p class="calendar-note">格内展示最近上报的处理状态，数量来自归档月表的独立计数快照，与整理进度无关；点击日期查看数量更新时间。数量未上报不表示 0 条。已归档不等于已通过完整性校验，“已封存”表示最近上报的日期版本已封存。</p>
  </section>
</template>

<style scoped>
.daily-calendar{padding:22px;background:var(--el-bg-color);border:1px solid var(--el-border-color-light);border-radius:12px;min-width:0}.calendar-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}.calendar-title{display:flex;align-items:baseline;gap:12px;flex-wrap:wrap}.calendar-month,.calendar-hint{font-size:12px;color:var(--el-text-color-secondary)}.calendar-hint{margin-left:auto}.unknown-help{border:0;padding:0;background:none;font:inherit;color:var(--el-text-color-secondary);cursor:pointer}.unknown-help:focus-visible{outline:2px solid var(--el-color-primary);outline-offset:3px}h3{margin:0}p,.paused-note{font-size:12px;color:var(--el-text-color-secondary);line-height:1.7}.calendar-summary{display:flex;align-items:center;gap:8px 18px;flex-wrap:wrap;font-size:12px;margin:8px 0 14px}.month-grid{display:grid;grid-template-columns:repeat(7,minmax(0,1fr));gap:8px}.weekday{text-align:center;font-size:12px;color:var(--el-text-color-secondary);padding-bottom:5px}.date-box{display:flex;flex-direction:column;align-items:flex-start;gap:7px;min-height:96px;padding:10px;border:1px solid var(--el-border-color-light);border-radius:8px;background:var(--el-fill-color-lighter);font:inherit;color:var(--el-text-color-primary);cursor:pointer;min-width:0;text-align:left}.date-box b{display:flex;gap:7px;flex-wrap:wrap;font-size:15px}.date-box span{font-size:12px}.date-box small{font-size:11px;overflow-wrap:anywhere}.date-box b small{font-weight:normal}.date-box:focus-visible,.date-box:hover:not(:disabled){outline:2px solid var(--el-color-primary);outline-offset:1px}.state-sealed{background:var(--el-color-success-light-9);border-color:var(--el-color-success-light-5)}.state-blocked{background:var(--el-color-danger-light-9);border-color:var(--el-color-danger-light-5)}.state-rebuilding,.state-preparing{background:var(--el-color-warning-light-9)}.state-backfill,.state-verify,.state-seal{background:var(--el-color-primary-light-9)}.processing{border:2px solid var(--el-color-primary)}.state-future{opacity:.5;cursor:default}.calendar-note{margin-bottom:0}.report-notice{padding:12px 14px;border-radius:8px;background:var(--el-color-warning-light-9);color:var(--el-text-color-regular);border:1px solid var(--el-color-warning-light-5)}
@media(max-width:700px){.daily-calendar{padding:12px}.month-grid{gap:4px}.date-box{padding:6px 3px;min-height:86px;gap:5px}.date-box span{font-size:10px}.date-box b small{display:none}.date-box small{font-size:10px}}
</style>
