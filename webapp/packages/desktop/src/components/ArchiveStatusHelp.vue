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
        <p><b>日期处理流程：</b>采集中 → 采集完成 → 历史整理 → 整理完成 → 校验与封存 → 已封存 / 需关注。然后进入下一日。采集使用独立游标；校验运行时采集等待。</p><p><b>旧版已有归档接入：</b>准备中 → 重建统计中 → 待逐日处理</p>
        <p><b>通常的逐日流程：</b>待逐日处理 → 补齐并比较 → 归档库核验 → 封存中 → 已封存</p>
        <p><b>条件不足或有差异：</b>补齐 / 核验 / 封存 → 需关注 → 满足条件或修复后重试 → 重新调度处理</p>
        <p><b>数据再次变化：</b>已封存 → 待逐日处理；校验证据失效时也可能退回补齐。涉及关联日期时，要共同满足封存条件。</p>
        <p>升级前后分别按对应流程执行，并非每个日期都必须经过所有状态。未知和未来日期不是处理步骤；轮询可能看不到持续很短的中间状态。当天还需等待日期结束、延迟窗口过去并完成收尾补齐。</p>
      </div>
      <div class="state-definitions">
        <section v-for="(guide, key) in archiveDayGuides" :key="key" :class="{'is-selected':kind===key}">
          <h4>{{ guide.label }}</h4><p>{{ guide.meaning }}</p>
          <dl><div><dt>出现条件</dt><dd>{{ guide.entry }}</dd></div><div><dt>下一步</dt><dd>{{ guide.next }}</dd></div><div><dt>如何处理</dt><dd>{{ guide.action }}</dd></div></dl>
        </section>
      </div>
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
