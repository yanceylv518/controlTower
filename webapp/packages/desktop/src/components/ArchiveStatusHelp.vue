<script setup lang="ts">
import { computed } from 'vue'
import { archiveDayGuide, archiveDayGuides } from '../utils/archiveStatusGuide'
const props = defineProps<{kind?: string; expanded?: boolean}>()
const selected = computed(() => props.kind ? archiveDayGuide(props.kind) : undefined)
</script>

<template>
  <div class="status-help">
    <section v-if="selected" class="selected-state" aria-label="当前日期状态说明">
      <h4>{{ selected.label }}是什么意思</h4>
      <dl><div><dt>含义</dt><dd>{{ selected.meaning }}</dd></div><div><dt>为什么显示</dt><dd>{{ selected.entry }}</dd></div><div><dt>接下来</dt><dd>{{ selected.next }}</dd></div><div><dt>需要做什么</dt><dd>{{ selected.action }}</dd></div></dl>
    </section>
    <details class="status-reference" :open="expanded">
      <summary v-show="!expanded">查看全部状态含义与流转</summary>
      <div class="transition-paths" aria-label="日期状态流转">
        <p><b>数据状态：</b>采集中 → 待处理 → 处理中 → 已封存。</p>
        <p><b>处理失败：</b>保留原因，重试后继续；其他日期可继续推进。</p>
        <p><b>迟到数据：</b>已封存日期有新写入，需要重新处理。</p>
        <p>校验、整理与封存是处理步骤。状态待确认表示尚无可信上报，不代表无数据或采集完成。暂停和上报过期不会抹去最近日期结果。</p>
      </div>
      <details><summary>查看内部阶段与兼容流程</summary>
      <div class="state-definitions">
        <section v-for="(guide, key) in archiveDayGuides" :key="key" :class="{'is-selected':kind===key}">
          <h4>{{ guide.label }}</h4><p>{{ guide.meaning }}</p>
          <dl><div><dt>出现条件</dt><dd>{{ guide.entry }}</dd></div><div><dt>下一步</dt><dd>{{ guide.next }}</dd></div><div><dt>如何处理</dt><dd>{{ guide.action }}</dd></div></dl>
        </section>
      </div>
      </details>
      <div class="task-state-note">
        <h4>任务状态与日期结果分开看</h4>
        <p><b>等待配置 / 授权：</b>运行要求已提交，但执行条件尚未确认。</p>
        <p><b>运行中：</b>任务已启用，不等于每一刻都在执行 SQL；结合当前操作和成功提交时间判断。</p>
        <p><b>等待暂停确认 → 已暂停：</b>当前批次可能尚未结束，必须等 Agent 确认。暂停保留日期结果，恢复从已提交位置继续。</p>
        <p><b>异常 / 等待重试：</b>查看具体操作、原因和重试时间；它不代表这一天已丢失数据。</p>
        <p><b>上报过期 / 读取失败：</b>页面保留历史快照，不能确认此刻是否正在执行；格内旧状态不代表任务仍在运行。</p>
      </div>
    </details>
  </div>
</template>

<style scoped>
.status-help{font-size:12px;line-height:1.8;min-width:0;margin:14px 0;color:var(--el-text-color-regular)}h4{margin:0 0 8px;font-size:14px;color:var(--el-text-color-primary)}p{margin:6px 0}.status-reference>summary{cursor:pointer;color:var(--el-color-primary);padding:8px 0;font-weight:600}.status-reference>summary:focus-visible{outline:2px solid var(--el-color-primary);outline-offset:2px}.transition-paths,.task-state-note,.selected-state{padding:14px;background:var(--el-fill-color-lighter);border-radius:8px;margin:10px 0;overflow-wrap:anywhere}.state-definitions{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.state-definitions section{padding:14px;border:1px solid var(--el-border-color-light);border-radius:8px;min-width:0}.state-definitions .is-selected{border-color:var(--el-color-primary)}dl{margin:0}dl>div{display:grid;grid-template-columns:76px minmax(0,1fr);gap:8px;margin:6px 0}dt{color:var(--el-text-color-secondary)}dd{margin:0;overflow-wrap:anywhere}@media(max-width:700px){.state-definitions{grid-template-columns:minmax(0,1fr)}dl>div{grid-template-columns:66px minmax(0,1fr);gap:6px}}
</style>
