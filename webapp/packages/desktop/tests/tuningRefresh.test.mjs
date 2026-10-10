import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, reactive, ref } from 'vue'

// Exercise the actual page script with controlled API promises. Lifecycle
// timers and unrelated components are excluded so request ordering is explicit.
const sfc = readFileSync(new URL('../src/views/ContinuousTuningView.vue', import.meta.url), 'utf8')
const groupFilterSfc = readFileSync(new URL('../src/components/TuningGroupFilter.vue', import.meta.url), 'utf8')
const script = sfc.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
const groupUtilsSource = readFileSync(new URL('../src/utils/channelGroup.ts', import.meta.url), 'utf8')
const groupUtilsCode = ts.transpileModule(groupUtilsSource, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const groupUtils = new Function('exports', `${groupUtilsCode}\nreturn exports;`)({})
const row = { channel_id: 1, model_name: 'm', base_weight: 100, base_priority: 1, current_weight: 80, current_priority: 1, group_name: 'default,vip', models: ['m'] }
const state = (requests, weight = 80) => ({ channel_id: 1, model_name: 'm', last_observed_requests: requests, proposed_weight: weight, speed_stats_version: 1, phase: 'normal', metric_ready: true, baseline_ready: true, updated_at: '2026-09-14T00:00:00Z', evaluation: {base_weight:100, evaluated_at:'2026-09-14T00:00:00Z', params:{min_samples:20,combined_min_factor:0.1,combined_max_factor:1.5}} })
const cloneFixture = value => JSON.parse(JSON.stringify(value));
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }

test('healthy performance writes with a configured cap have no capacity warning', async () => {
  const p = page(); await p.load(); p.ratesReady.value = true;
  const limited = { ...row, max_tpm: 1000 };
  p.currentRates.value.set(1, { rpm: 0, tpm: 200 });
  for (const weight of [55, 60, 66, 72]) {
    p.states.value = [{ ...state(100, weight), capacity_limited: false, capacity: { initialized: true, active: false, phase: 'normal', applied_at: '2026-10-02T00:00:00Z' } }];
    assert.equal(p.limitReason(limited), '');
    assert.doesNotMatch(p.evaluationText(limited), /限升|等待.*反馈/);
  }
});

test('capacity feedback and hysteresis remain visible below the live limit and without performance samples', async () => {
  const p = page(); await p.load(); p.ratesReady.value = true;
  const limited = { ...row, max_tpm: 1000 };
  p.currentRates.value.set(1, { rpm: 0, tpm: 900 });
  for (const [phase, text] of [['holding', /保持容量限升/], ['waiting_feedback', /完整60秒/], ['awaiting_write', /执行回执/], ['reducing', /按比例降权/], ['no_headroom', /分流空间不足/], ['minimum_weight', /最低权重/], ['recovering', /恢复观察/]]) {
    p.states.value = [{ ...state(0, 75), capacity: { initialized: true, active: true, phase } }];
    assert.match(p.limitReason(limited), text);
    assert.match(p.evaluationText(limited), text);
    assert.doesNotMatch(p.evaluationText(limited), /本轮不调权/);
  }
  p.events.value = [{ id: 'cap', rule: 'capacity_reduce', channel_id: 1, channel_name: 'a', evidence: { model: 'm' }, status: 'pending' }];
  assert.equal(p.filteredEvents.value.length, 1);
  p.ratesReady.value = false;
  assert.match(p.limitReason(limited), /实时负载不可用/);
  p.ratesReady.value = true; p.currentRates.value.set(1, {rpm: 0, tpm: 1500}); p.policy.dispatch_modes.m = 'off';
  assert.match(p.limitReason(limited), /未参与自动容量控制/);
});

class ApiError extends Error {
  constructor(status, code) { super(code); this.status = status; this.code = code }
}

function page() {
  const filters = reactive({ site_id: 'a', loadInstances: async () => {} })
  const dashboard = {
    tuningPolicy: async () => ({ mode: 'observe', policy: { dispatch_modes: { m: 'auto' } } }),
    tuningBaseValues: async () => ({ items: [{ ...row }] }),
    tuningRecommendations: async () => ({ items: [] }),
    tuningContinuousStates: async () => ({ items: [state(42)] }),
    tuningChannels: async () => { throw new Error('channel directory unavailable') },
  }
  const names = ['computed', 'reactive', 'ref', 'watch', 'onMounted', 'onBeforeUnmount', 'useFiltersStore', 'dashboard', 'formatTime', 'ApiError', 'ElMessage', 'ElMessageBox', 'useMobileViewport', 'hiddenChannelGroupCount', 'matchesChannelGroup', 'MAX_VISIBLE_CHANNEL_GROUPS', 'normalizeChannelGroups', 'splitChannelGroups', 'visibleChannelGroups']
  const create = new Function(...names, `${compiled}\nreturn { phaseText, watchChannelChanges, stopWatching: () => changesAbort?.abort(), sync, save, savedPolicy, mode, policyConflict, factorExplanation, refreshCurrentRates, ratesError, saveCapacity, saving, mobileEditRow, stageMobileEdit, mobileChanges, mobilePriorityChanges, mobileRuleChanges, mobileSaveOpen, load, refreshRuntime, loadChannelDirectory, applyGroupLocally, acceptStates, stateFor, sampleText, evaluationText, states, refreshError, bases, channels, channelDirectorySite, channelDirectoryLoading, policy, channelQuery, channelSwitchFilter, selectedGroupFilter, toggleGroupFilter, selectedGroupName, displayedRows, activeRows, modelChannelRows, models, modelChannelCount, modelChannelCountLabel, activeModel, dirty, selectModel, fieldChanged, savedBases, originalBase, calculatedWeight, executionWeightText, displayedSpeedFactor, coefficientCell, overallEvaluationStatus, coefficientEmptyText, coefficientSpan, displayedPriority, editPriority, priorityLocked, cancelChanges, limitReason, currentRates, ratesReady, rowStatus, channelStatusFor, channelStatusLabel, isDirectoryOnlyRow, eventResult, eventResultClass, events, filteredEvents, eventDateRange };`)
  const messages = [];
  const view = create(computed, reactive, ref, () => {}, () => {}, () => {}, () => filters, dashboard, String, ApiError, {info() {}, success() {}, error(message) {messages.push(message)}}, {confirm: async () => {}}, () => ref(false), groupUtils.hiddenChannelGroupCount, groupUtils.matchesChannelGroup, groupUtils.MAX_VISIBLE_CHANNEL_GROUPS, groupUtils.normalizeChannelGroups, groupUtils.splitChannelGroups, groupUtils.visibleChannelGroups)
  const initialPolicy = JSON.parse(JSON.stringify({...view.policy, dispatch_modes:{m:'auto'}}));
  dashboard.tuningPolicy = async () => ({mode:'observe', policy: JSON.parse(JSON.stringify(initialPolicy))});
  return { ...view, filters, dashboard, messages }
}

test('rate failures distinguish transport, permissions and server availability; recovery clears the warning', async () => {
  const p = page()
  for (const [error, expected] of [
    [new Error('network down'), /网络连接/],
    [new ApiError(403, 'forbidden'), /站点权限/],
    [new ApiError(503, 'current_rates_unavailable'), /服务端尚无法提供完整数据/],
    [new ApiError(500, 'query_failed'), /HTTP 500/],
    [new ApiError(401, 'unauthorized'), /^$/],
  ]) {
    p.dashboard.tuningCurrentRates = async () => { throw error }
    await p.refreshCurrentRates()
    assert.equal(p.ratesReady.value, false)
    assert.match(p.ratesError.value, expected)
    assert.doesNotMatch(p.ratesError.value, /Agent 已升级/)
  }
  p.dashboard.tuningCurrentRates = async () => ({ items: [{ channel_id: 1, rpm: 12, tpm: 100 }], as_of: '', window_start: '', delay_seconds: 0 })
  await p.refreshCurrentRates()
  assert.equal(p.ratesReady.value, true)
  assert.equal(p.ratesError.value, '')
  assert.equal(p.currentRates.value.get(1).rpm, 12)
})

test('initial unavailable state is unknown, and retry loads real samples', async () => {
  const p = page()
  p.dashboard.tuningContinuousStates = async () => { throw new Error('network down') }
  await p.load()
  assert.equal(p.sampleText(row), '—')
  assert.equal(p.evaluationText(row), '等待评估数据')
  assert.match(p.refreshError.value, /network down/)
  p.dashboard.tuningContinuousStates = async () => ({ items: [state(42)] })
  await p.refreshRuntime()
  assert.equal(p.sampleText(row), '42/20')
  assert.equal(p.refreshError.value, '')
})

test('same-site reload failure and empty refresh preserve samples, while actual zero is accepted', async () => {
  const p = page()
  await p.load()
  p.dashboard.tuningContinuousStates = async () => { throw new Error('temporary') }
  await p.load()
  assert.equal(p.stateFor(row).last_observed_requests, 42)
  p.dashboard.tuningContinuousStates = async () => ({ items: [] })
  await p.refreshRuntime()
  assert.equal(p.stateFor(row).last_observed_requests, 42)
  assert.match(p.refreshError.value, /上次成功结果/)
  p.dashboard.tuningContinuousStates = async () => ({ items: [state(0, 0)] })
  await p.refreshRuntime()
  assert.equal(p.stateFor(row).proposed_weight, 0)
  assert.equal(p.sampleText(row), '0/20')
  assert.equal(p.refreshError.value, '')
})

test('a failed second site cannot reuse matching channel IDs from the first', async () => {
  const p = page()
  await p.load()
  p.filters.site_id = 'b'
  assert.equal(p.stateFor(row), undefined)
  p.dashboard.tuningContinuousStates = async () => { throw new Error('site b failed') }
  await p.load()
  assert.equal(p.stateFor(row), undefined)
  assert.equal(p.sampleText(row), '—')
})

test('late refresh results cannot overwrite a newer completed refresh', async () => {
  const p = page()
  await p.load()
  const old = deferred(), latest = deferred()
  let call = 0
  p.dashboard.tuningContinuousStates = () => (++call === 1 ? old.promise : latest.promise)
  const first = p.refreshRuntime(), second = p.refreshRuntime()
  latest.resolve({ items: [state(90)] }); await second
  old.resolve({ items: [state(10)] }); await first
  assert.equal(p.stateFor(row).last_observed_requests, 90)
})

test('a refresh started before a confirmed group write cannot restore the old group', async () => {
  const p = page()
  await p.load()
  const old = deferred()
  p.dashboard.tuningBaseValues = () => old.promise
  const refresh = p.refreshRuntime()
  p.applyGroupLocally(1, 'default')
  old.resolve({ items: [{ ...row, group_name: 'default,vip' }] })
  await refresh
  assert.equal(p.bases.value[0].group_name, 'default')
})

test('a stale channel directory cannot restore the old group after a confirmed write', async () => {
  const p = page()
  await p.load()
  p.channels.value = [{ channel_id: 1, channel_name: 'primary', group_name: 'default,vip', status: 'enabled', weight: 1, priority: 1, models: ['m'] }]
  const old = deferred()
  p.dashboard.tuningChannels = () => old.promise
  const load = p.loadChannelDirectory('a')
  p.applyGroupLocally(1, 'default')
  old.resolve({ items: [{ channel_id: 1, channel_name: 'primary', group_name: 'default,vip', status: 'enabled', weight: 1, priority: 1, models: ['m'] }] })
  await load
  assert.equal(p.channels.value[0].group_name, 'default')
})

test('a refresh started before a full reload cannot overwrite the reload', async () => {
  const p = page()
  await p.load()
  const old = deferred()
  p.dashboard.tuningContinuousStates = () => old.promise
  const first = p.refreshRuntime()
  p.dashboard.tuningContinuousStates = async () => ({ items: [state(90)] })
  await p.load()
  old.resolve({ items: [state(10)] }); await first
  assert.equal(p.stateFor(row).last_observed_requests, 90)
})

test('slow polling still displays completed results while a newer request is pending', async () => {
  const p = page()
  await p.load()
  const firstResponse = deferred(), secondResponse = deferred()
  let call = 0
  p.dashboard.tuningContinuousStates = () => (++call === 1 ? firstResponse.promise : secondResponse.promise)
  const first = p.refreshRuntime(), second = p.refreshRuntime()
  firstResponse.resolve({ items: [state(60)] }); await first
  assert.equal(p.stateFor(row).last_observed_requests, 60, 'polls taking over 30 seconds must not starve updates')
  secondResponse.resolve({ items: [state(90)] }); await second
  assert.equal(p.stateFor(row).last_observed_requests, 90)
})

test('late failures cannot replace a newer successful result with an error', async () => {
  const p = page()
  await p.load()
  const old = deferred(), latest = deferred()
  let call = 0
  p.dashboard.tuningContinuousStates = () => (++call === 1 ? old.promise : latest.promise)
  const first = p.refreshRuntime(), second = p.refreshRuntime()
  latest.resolve({ items: [state(90)] }); await second
  old.reject(new Error('old timeout')); await first
  assert.equal(p.stateFor(row).last_observed_requests, 90)
  assert.equal(p.refreshError.value, '')
})

test('late errors from the previous site cannot affect the current site', async () => {
  const p = page()
  await p.load()
  const old = deferred()
  p.dashboard.tuningContinuousStates = () => old.promise
  const first = p.refreshRuntime()
  p.filters.site_id = 'b'
  p.dashboard.tuningContinuousStates = async () => ({ items: [state(90)] })
  await p.load()
  old.reject(new Error('site a timeout')); await first
  assert.equal(p.stateFor(row).last_observed_requests, 90)
  assert.equal(p.refreshError.value, '')
})


test('speed readiness uses direct samples even with abundant total requests', async () => {
  const p = page()
  p.dashboard.tuningContinuousStates = async () => ({ items: [{ ...state(500), metric_ready: false, baseline_ready: false, speed_sample_count: 3, speed_retry_count: 497 }] })
  await p.load()
  assert.match(p.evaluationText(row), /TTFT 样本不足 3\//)
  assert.equal(p.sampleText(row), `500/${p.policy.continuous.min_samples}`)
})

test('legacy speed evidence never appears ready solely from request volume', async () => {
  const p = page()
  p.dashboard.tuningContinuousStates = async () => ({ items: [{ ...state(500), metric_ready: false, baseline_ready: false, speed_legacy_count: 500 }] })
  await p.load()
  assert.match(p.evaluationText(row), /TTFT 样本不足 0\//)
})


test('pre-upgrade cached readiness is not presented as filtered speed evidence', async () => {
  const p = page()
  p.dashboard.tuningContinuousStates = async () => ({ items: [{ ...state(500), speed_stats_version: 0 }] })
  await p.load()
  assert.equal(p.evaluationText(row), '等待新口径速度评估')
})

test('eligible output is visible without TTFT and old output readiness is not reused', async () => {
  const p = page()
  const outputState = { ...state(500), metric_ready: false, baseline_ready: false, otps_ready: true, otps_stats_version: 1 }
  p.dashboard.tuningContinuousStates = async () => ({ items: [outputState] })
  await p.load()
  assert.equal(p.evaluationText(row), 'TTFT 样本不足，使用本轮输出系数')
  outputState.otps_stats_version = 0
  await p.refreshRuntime()
  assert.match(p.evaluationText(row), /TTFT 样本不足/)
  assert.doesNotMatch(p.evaluationText(row), /使用本轮输出系数/)
})

test('missing peer TTFT baseline also uses current output and sparse windows hold', async () => {
  const p = page()
  const s = { ...state(500), metric_ready: true, baseline_ready: false, otps_ready: true, otps_stats_version: 1 }
  p.dashboard.tuningContinuousStates = async () => ({items:[s]})
  await p.load()
  assert.equal(p.evaluationText(row), 'TTFT 基线不足，使用本轮输出系数')
  s.last_observed_requests = 1
  await p.refreshRuntime()
  assert.match(p.evaluationText(row), /样本不足.*本轮不调权/)
  s.phase = 'circuit'
  await p.refreshRuntime()
  assert.match(p.evaluationText(row), /已熔断/)
})


test('switching models preserves pending edits and evaluation still uses its saved base', async () => {
  const p = page(); await p.load();
  p.savedBases.value = [{ ...row, base_weight: 100 }];
  p.bases.value[0].base_weight = 120; p.dirty.value = true;
  p.selectModel('other'); p.selectModel('m');
  assert.equal(p.bases.value[0].base_weight, 120);
  assert.equal(p.dirty.value, true);
  assert.equal(p.fieldChanged(p.bases.value[0], 'base_weight'), true);
  assert.equal(p.originalBase(p.bases.value[0]), 100);
})

test('capacity distinguishes unavailable rates from zero and reports the exceeded limit', async () => {
  const p = page(); await p.load();
  const b = { ...row, max_rpm: 100, max_tpm: 1000 };
  assert.match(p.limitReason(b), /不可用/);
  p.ratesReady.value = true;
  p.currentRates.value.set(1, {rpm:0,tpm:0});
  assert.equal(p.limitReason(b), '');
  p.currentRates.value.set(1, {rpm:99,tpm:1000});
  assert.match(p.limitReason(b), /^TPM/);
  p.currentRates.value.set(1, {rpm:100,tpm:1000});
  assert.match(p.limitReason(b), /^RPM \/ TPM/);
  assert.equal(p.limitReason({...b,max_rpm:0,max_tpm:0}), '');
})

test('channel name and ID search compose with group and status filters', async () => {
  assert.doesNotMatch(sfc, /搜索渠道或分组|搜索渠道 \/ ID \/ 分组/)
  assert.match(script, /const channelQuery = ref\(/)
  const p = page(); await p.load(); p.activeModel.value='m';
  p.bases.value = [
    {...row, channel_id:1, channel_name:'north', max_rpm:0,max_tpm:0},
    {...row, channel_id:2, channel_name:'south', max_rpm:0,max_tpm:0},
  ];
  p.channels.value = p.bases.value.map(item => ({ ...item, status: 'enabled', models: ['m'] }));
  p.channelDirectorySite.value = 'a';
  p.acceptStates('a',[
    {...state(100), channel_id:1, phase:'circuit',otps_ready:true,otps_stats_version:1,metric_ready:false},
    {...state(100), channel_id:2, phase:'normal',otps_ready:true,otps_stats_version:1,metric_ready:true},
  ]);
  assert.equal(p.rowStatus(p.bases.value[0]).label,'熔断');
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [1,2]);
  p.channelQuery.value = ' NORTH ';
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [1]);
  p.channelQuery.value = '#2';
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [2]);
  p.selectedGroupFilter.value = {kind:'group',name:'missing'};
  assert.equal(p.displayedRows.value.length, 0);
  p.selectedGroupFilter.value = null;
  p.channelSwitchFilter.value = 'disabled';
  assert.equal(p.displayedRows.value.length, 0);
  p.channelSwitchFilter.value = 'enabled';
  p.channelQuery.value = '1';
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [1]);
  p.channelQuery.value = 'missing';
  assert.equal(p.displayedRows.value.length, 0);
  p.channelQuery.value = '';
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [1,2]);
})

test('runtime overview filters channels by a matching group item', async () => {
  assert.match(sfc, /<TuningGroupFilter/)
  assert.match(script, /matchesChannelGroup\(row\.group_name, selectedGroupName\.value\)/)
  assert.match(sfc, /:data="displayedRows"/)
  const p = page(); await p.load(); p.activeModel.value = 'm';
  p.bases.value = [
    { ...row, channel_id: 1, channel_name: 'primary', group_name: 'default,vip' },
    { ...row, channel_id: 2, channel_name: 'fast', group_name: 'default,fast' },
  ];
  p.channels.value = p.bases.value.map(item => ({ ...item, status: 'enabled', models: ['m'] }));
  p.channelDirectorySite.value = 'a';
  p.toggleGroupFilter('vip');
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [1]);
  p.selectedGroupFilter.value = {kind:'group',name:'fast'};
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [2]);
  p.selectedGroupFilter.value = {kind:'group',name:'missing'};
  assert.equal(p.displayedRows.value.length, 0);
})

test('group filter stays on the second filter row and keeps selection until its text is cleared', () => {
  const desktopFilters = sfc.match(/<div v-if="!mobile" class="desktop-channel-filters"[\s\S]*?<div v-if="mobile"/)?.[0] ?? ''
  const mobileFilters = sfc.match(/<div class="mobile-channel-filters"[\s\S]*?<el-empty/)?.[0] ?? ''

  assert.match(desktopFilters, /<TuningGroupFilter/)
  assert.match(mobileFilters, /<TuningGroupFilter/)
  assert.ok(desktopFilters.indexOf('<TuningGroupFilter') < desktopFilters.indexOf('<el-select'))
  assert.ok(mobileFilters.indexOf('<TuningGroupFilter') < mobileFilters.indexOf('<el-select'))
  assert.ok(desktopFilters.indexOf('<el-select') < desktopFilters.indexOf('channel-filter-summary'))
  assert.ok(mobileFilters.indexOf('<el-select') < mobileFilters.indexOf('channel-filter-summary'))
  assert.ok(desktopFilters.indexOf('<TuningGroupFilter') < desktopFilters.indexOf('channel-filter-summary'))
  assert.ok(mobileFilters.indexOf('<TuningGroupFilter') < mobileFilters.indexOf('channel-filter-summary'))
  assert.match(groupFilterSfc, /if \(props\.modelValue && nextValue\.length === 0\) emit\("clear"\)/)
  assert.match(groupFilterSfc, /hasSearchInput\.value \? search\.value : selectedLabel\.value/)
})

test('channel directory keeps disabled-only and multi-model channels visible under every associated model', () => {
  const p = page()
  const primary = { ...row, channel_id: 1, channel_name: 'primary' }
  p.bases.value = [primary]
  p.channels.value = [
    { channel_id: 1, channel_name: 'primary', status: 'enabled', weight: 80, priority: 10, models: ['m'], group_name: 'default' },
    { channel_id: 2, channel_name: 'manual-off', status: 'disabled', weight: 0, priority: 9, models: ['m'], group_name: 'vip' },
    { channel_id: 3, channel_name: 'auto-off', status: 'auto_disabled', weight: 0, priority: 8, models: ['m', 'n'], group_name: 'fast' },
    { channel_id: 4, channel_name: 'only-off', status: 'disabled', weight: 0, priority: 7, models: ['closed-only'], group_name: 'legacy' },
  ]
  p.channelDirectorySite.value = 'a'
  p.activeModel.value = 'm'

  assert.deepEqual(p.modelChannelRows.value.map(item => item.channel_id), [2, 3, 1])
  assert.equal(p.modelChannelRows.value[2], p.bases.value[0], 'eligible base rows remain the editable source objects')
  const manualOff = p.modelChannelRows.value.find(item => item.channel_id === 2)
  assert.equal(p.isDirectoryOnlyRow(manualOff), true)
  assert.equal(manualOff.current_weight, 0)
  assert.equal(manualOff.group_name, 'vip')
  assert.equal('base_weight' in manualOff, false, 'directory-only rows do not fabricate tuning values')
  assert.equal(p.models.value.includes('closed-only'), true)
  assert.equal(p.modelChannelCount('closed-only'), 1)

  p.activeModel.value = 'n'
  assert.deepEqual(p.modelChannelRows.value.map(item => item.channel_id), [3])
  p.activeModel.value = 'closed-only'
  assert.deepEqual(p.modelChannelRows.value.map(item => item.channel_id), [4])
})

test('loading a directory with only closed channels selects its model and ends loading state', async () => {
  const p = page()
  p.dashboard.tuningChannels = async () => ({ items: [
    { channel_id: 9, channel_name: 'legacy-off', status: 'disabled', weight: 0, priority: 3, models: ['legacy-model'], group_name: 'legacy' },
  ] })
  await p.loadChannelDirectory('a')

  assert.equal(p.channelDirectoryLoading.value, false)
  assert.equal(p.activeModel.value, 'legacy-model')
  assert.deepEqual(p.modelChannelRows.value.map(item => item.channel_id), [9])
  assert.equal(p.models.value.includes('legacy-model'), true)
})

test('channel switch status filters compose with the group filter without tuning status filtering', () => {
  assert.doesNotMatch(sfc, /调权状态筛选|全部调权状态/)
  assert.doesNotMatch(script, /channelTuningFilter/)
  const p = page()
  p.bases.value = [{ ...row, channel_id: 1, channel_name: 'enabled', group_name: 'alpha' }]
  p.channels.value = [
    { channel_id: 1, channel_name: 'enabled', status: 'enabled', weight: 80, priority: 1, models: ['m'], group_name: 'alpha' },
    { channel_id: 2, channel_name: 'disabled', status: 'disabled', weight: 0, priority: 2, models: ['m'], group_name: 'alpha' },
    { channel_id: 3, channel_name: 'automatic', status: 'auto_disabled', weight: 0, priority: 3, models: ['m'], group_name: 'beta' },
    { channel_id: 4, channel_name: 'unknown', status: 'other', weight: 0, priority: 4, models: ['m'], group_name: 'beta' },
  ]
  p.channelDirectorySite.value = 'a'
  p.activeModel.value = 'm'

  assert.equal(p.channelSwitchFilter.value, 'enabled')
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [1])
  assert.match(script, /watch\(\(\) => filters\.site_id, \(\) => \{[^\n]*channelSwitchFilter\.value = "enabled"/)
  p.channelSwitchFilter.value = 'disabled'
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [3, 2])
  p.channelSwitchFilter.value = 'enabled'
  p.acceptStates('a', [{ ...state(100), proposed_weight: 90 }])
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [1])
  p.selectedGroupFilter.value = {kind:'group',name:'beta'}
  assert.deepEqual(p.displayedRows.value, [])
  p.channelSwitchFilter.value = ''
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [4, 3])
})

test('model counts show status totals only for the selected model and update on selection or channel refresh', () => {
  const p = page()
  p.channels.value = [
    { channel_id: 1, channel_name: 'shared', status: '1', weight: 0, priority: 1, models: ['m', 'n'], group_name: 'alpha' },
    { channel_id: 2, channel_name: 'closed', status: 'disabled', weight: 0, priority: 1, models: ['m'], group_name: 'beta' },
    { channel_id: 3, channel_name: 'auto-closed', status: 'auto_disabled', weight: 0, priority: 1, models: ['m', 'n'], group_name: 'beta' },
    { channel_id: 4, channel_name: 'active', status: 'enabled', weight: 80, priority: 1, models: ['n'], group_name: 'alpha' },
    { channel_id: 5, channel_name: 'unknown', status: 'other', weight: 0, priority: 1, models: ['m'], group_name: 'beta' },
  ]
  p.channelDirectorySite.value = 'a'
  p.activeModel.value = 'm'
  assert.equal(p.modelChannelCountLabel('m'), '已启用 1 个渠道')
  assert.equal(p.modelChannelCountLabel('n'), '共 3 个渠道')
  p.selectModel('n')
  assert.equal(p.modelChannelCountLabel('m'), '共 4 个渠道')
  assert.equal(p.modelChannelCountLabel('n'), '已启用 2 个渠道')
  p.channelSwitchFilter.value = 'disabled'
  assert.equal(p.modelChannelCountLabel('n'), '手动关闭 1 个渠道')
  assert.equal(p.modelChannelCountLabel('m'), '共 4 个渠道')
  p.selectModel('m')
  assert.equal(p.modelChannelCountLabel('m'), '手动关闭 2 个渠道')
  assert.equal(p.modelChannelCountLabel('n'), '共 3 个渠道')
  p.selectedGroupFilter.value = { kind: 'group', name: 'missing' }
  assert.equal(p.modelChannelCountLabel('m'), '手动关闭 2 个渠道')
  p.channels.value = p.channels.value.map(item => item.channel_id === 1 ? { ...item, status: 'disabled' } : item)
  assert.equal(p.modelChannelCountLabel('m'), '手动关闭 3 个渠道')
  p.channelSwitchFilter.value = 'enabled'
  assert.equal(p.modelChannelCountLabel('m'), '已启用 0 个渠道')
  p.channelSwitchFilter.value = ''
  assert.equal(p.modelChannelCountLabel('m'), '共 4 个渠道')
  assert.equal(p.modelChannelCount('m'), 4)
  assert.doesNotMatch(sfc, /<small v-if="!mobile">\{\{ activeRows\.length \}\} 个渠道<\/small>/)
})

test('recorded events are not presented as successful writes and dates include the last day', async () => {
  const p = page(); await p.load();
  assert.equal(p.eventResult({status:'recorded'}),'已记录');
  assert.equal(p.eventResult({status:'succeeded'}),'执行成功');
  assert.equal(p.eventResultClass({status:'failed'}),'danger');
  p.events.value = ['2026-09-16T23:59:59','2026-09-17T00:00:00','2026-09-17T23:59:59','2026-09-18T00:00:00'].map((created_at,i)=>({id:String(i),created_at,rule:'weight_write',channel_name:'north',channel_id:1}));
  p.eventDateRange.value=['2026-09-17','2026-09-17'];
  assert.deepEqual(p.filteredEvents.value.map(e=>e.id),['1','2']);
})

test('formula target is independent of execution limits and unsaved base edits', async () => {
  const p = page(); await p.load();
  p.states.value = [{ ...state(42, 88), base_weight: 100, k_speed: 1.2, k_cache: 1, k_otps: 1.2, k_error: 1 }];
  assert.equal(p.calculatedWeight(row), 144);
  assert.equal(p.calculatedWeight({ ...row, base_weight: 300 }), 144);
  p.states.value[0].k_speed = 2;
  assert.equal(p.calculatedWeight(row), 150);
  p.states.value[0].last_observed_requests = 1;
  assert.equal(p.calculatedWeight(row), null);
  p.states.value[0].last_observed_requests = 42;
  p.states.value[0].phase = 'circuit';
  assert.equal(p.calculatedWeight(row), 150);
  p.policy.dispatch_modes.m = 'off';
  assert.equal(p.calculatedWeight(row), 150);
  Object.assign(p.states.value[0], {metric_ready:false,baseline_ready:false,paused_reason:'mixed_channel',speed_stats_version:0});
  assert.equal(p.calculatedWeight(row), 150);
  assert.equal(p.calculatedWeight({...row, base_weight:0}), 150, 'an unsaved zero base cannot rewrite the past evaluation');
});

test('priority editor shows saved target and allows editing during circuit', async () => {
  const p = page(); await p.load();
  p.bases.value[0].base_priority = 11;
  p.bases.value.push({ ...row, model_name: 'other', base_priority: 11 });
  assert.equal(p.displayedPriority(p.bases.value[0]), 11);
  p.editPriority(p.bases.value[0], 12);
  assert.equal(p.displayedPriority(p.bases.value[0]), 12);
  assert.equal(p.bases.value[1].base_priority, 12);
  assert.equal(p.bases.value[0].current_priority, 1);
  p.cancelChanges(false);
  assert.equal(p.displayedPriority(p.bases.value[0]), 1);
  p.states.value[0].phase = 'circuit';
  p.editPriority(p.bases.value[0], 13);
  assert.equal(p.displayedPriority(p.bases.value[0]), 13);
});

test('speed display suppresses stale factors and labels only valid output fallback', async () => {
  const p = page(); await p.load();
  p.states.value = [{ ...state(2), k_speed: 1.985, k_otps: 1.2 }];
  assert.equal(p.displayedSpeedFactor(row), null);
  p.states.value[0].last_observed_requests = 42;
  p.states.value[0].metric_ready = false;
  assert.equal(p.displayedSpeedFactor(row), null);
  Object.assign(p.states.value[0], {otps_ready:true, otps_stats_version:1});
  assert.equal(p.displayedSpeedFactor(row), null);
  p.states.value[0].k_speed = 1.2;
  assert.equal(p.displayedSpeedFactor(row), 1.2);
  p.states.value[0].metric_ready = true;
  p.states.value[0].k_speed = 0.9;
  assert.equal(p.displayedSpeedFactor(row), 0.9);
});

test('coefficient cells keep values with explicit availability and provenance', async () => {
  const p = page(); await p.load();
  p.states.value = [{...state(1),k_speed:1.9,k_cache:0.9,k_otps:1,k_error:0.8,smoothed_error_rate:0.05}];
  assert.equal(p.coefficientCell(row,'speed').value,null);
  assert.equal(p.coefficientCell(row,'speed').status,'');
  assert.equal(p.rowStatus(row).label,'窗口样本不足 1/20');
  assert.equal(p.coefficientCell(row,'cache').status,'保留值');
  assert.equal(p.coefficientCell(row,'error').status,'平滑错误率');
  Object.assign(p.states.value[0],{last_observed_requests:42,metric_ready:false,otps_ready:true,otps_stats_version:1,k_speed:1.1,k_otps:1.1,k_cache:1,cache_ready:false});
  assert.equal(p.coefficientCell(row,'speed').status,'输出替代');
  assert.equal(p.coefficientCell(row,'cache').status,'中性回退');
  assert.equal(p.coefficientCell(row,'otps').status,'有效');
});

test('window shortage precedes TTFT provenance while circuit retains precedence', async () => {
 const p=page(); await p.load();
 p.states.value=[{...state(3),speed_stats_version:0,metric_ready:false,baseline_ready:false}];
 assert.equal(p.rowStatus(row).label,'窗口样本不足 3/20');
 assert.equal(p.coefficientCell(row,'speed').status,'');
 Object.assign(p.states.value[0],{last_observed_requests:25,speed_stats_version:1});
 assert.equal(p.coefficientCell(row,'speed').status,'TTFT 样本不足');
 p.states.value[0].metric_ready=true;
 assert.equal(p.coefficientCell(row,'speed').status,'基线不足');
 Object.assign(p.states.value[0],{phase:'circuit',last_observed_requests:0});
 assert.equal(p.rowStatus(row).label,'熔断');
});

test('overall evaluation occupies a shared coefficient row without warning glyphs', async () => {
 const p=page(); await p.load();
 p.states.value=[{...state(3)}];
 assert.equal(p.overallEvaluationStatus(row),'窗口样本不足 3/20');
 Object.assign(p.states.value[0],{last_observed_requests:25,metric_ready:false});
 assert.equal(p.overallEvaluationStatus(row),'');
 p.states.value[0].phase='circuit';
 assert.equal(p.overallEvaluationStatus(row),'熔断');
 assert.equal(p.rowStatus(row).icon,'');
 assert.deepEqual(p.coefficientSpan({column:{property:'coefficient_speed'}}),[1,4]);
 assert.deepEqual(p.coefficientSpan({column:{property:'coefficient_cache'}}),[0,0]);
 assert.deepEqual(p.coefficientSpan({column:{}}),[1,1]);
});

test('coefficient empty labels distinguish nonparticipation from circuit', async () => {
 const p=page(); await p.load();
 p.states.value=[state(2)];
 assert.equal(p.coefficientEmptyText(row),'窗口样本不足');
 assert.equal(p.coefficientEmptyText({...row,base_weight:0}),'未参与调权');
 p.policy.dispatch_modes.m='off';
 assert.equal(p.coefficientEmptyText(row),'未参与调权');
 p.policy.dispatch_modes.m='auto';p.states.value[0].phase='circuit';
 assert.equal(p.coefficientEmptyText(row),'');
 assert.equal(p.overallEvaluationStatus(row),'熔断');
});

test('mobile preview applies edits to the current row after a refresh; cancel restores saved values', async () => {
 const p=page(); await p.load();
 p.mobileEditRow.value=p.bases.value[0];
 p.bases.value=p.bases.value.map(item=>({...item}));
 p.stageMobileEdit({base_weight:120,priority:7,max_rpm:30,max_tpm:4000});
 assert.equal(p.bases.value[0].base_weight,120);
 assert.equal(p.mobileSaveOpen.value,true);
 assert.deepEqual(p.mobileChanges.value.find(change=>change.label==='基础权重')?.after,120);
 assert.equal(p.mobilePriorityChanges.value[0].after,7);
 p.cancelChanges();
 assert.equal(p.bases.value[0].base_weight,100);
 assert.equal(p.displayedPriority(p.bases.value[0]),1);
 assert.equal(p.dirty.value,false);
});

test('mobile staging edits saved base priority during circuit and lists changed rule values', async () => {
 const p=page(); await p.load();
 p.states.value=[{...state(42),phase:'circuit'}];
 p.mobileEditRow.value=p.bases.value[0];
 p.stageMobileEdit({base_weight:120,priority:7,max_rpm:30,max_tpm:4000});
 assert.equal(p.mobilePriorityChanges.value.length,1);
 assert.equal(p.displayedPriority(p.bases.value[0]),7);
 assert.equal(p.bases.value[0].base_priority,7);
 p.policy.continuous.min_samples=50;
 assert.deepEqual(p.mobileRuleChanges.value.find(change=>change.key==='min_samples'),{key:'min_samples',label:'每渠道最少请求数',before:20,after:50});
});


test('capacity confirmation saves only the selected field and preserves other drafts', async () => {
  const p = page(); await p.load();
  p.bases.value[0].base_weight = 999; p.dirty.value = true;
  const latest = { ...row, max_tpm: 10, max_rpm: 20 };
  p.dashboard.tuningBaseValues = async () => ({ items: [latest] });
  let submitted;
  p.dashboard.saveTuningBaseValues = async (site, items) => {
    assert.equal(site, 'a'); submitted = items; return { items };
  };
  await p.saveCapacity(p.bases.value[0], 'max_tpm', 300);
  assert.deepEqual(submitted, [{ ...latest, max_tpm: 300 }]);
  assert.equal(p.bases.value[0].base_weight, 999);
  assert.equal(p.bases.value[0].max_tpm, 300);
  assert.equal(p.savedBases.value[0].max_tpm, 300);
  assert.equal(p.dirty.value, true);
  p.cancelChanges(false); assert.equal(p.bases.value[0].max_tpm, 300);
});

test('capacity save failure preserves the original value and allows retry', async () => {
  const p = page(); await p.load();
  p.bases.value[0].max_rpm = 20;
  p.dashboard.saveTuningBaseValues = async () => { throw new Error('network down'); };
  await assert.rejects(p.saveCapacity(p.bases.value[0], 'max_rpm', 0), /network down/);
  assert.equal(p.bases.value[0].max_rpm, 20);
  assert.equal(p.saving.value, false);
  assert.equal(p.dirty.value, false);
});

test('capacity save ignores a result after switching sites', async () => {
  const p = page(); await p.load(); const pending = deferred();
  p.dashboard.saveTuningBaseValues = () => pending.promise;
  const request = p.saveCapacity(p.bases.value[0], 'max_tpm', 500);
  await Promise.resolve(); p.filters.site_id = 'b';
  pending.resolve({ items: [{ ...row, max_tpm: 500 }] }); await request;
  assert.notEqual(p.bases.value[0].max_tpm, 500);
});


test('syncing base values preserves the unsaved policy and its subsequent save', async () => {
  const p = page(); await p.load();
  p.policy.dispatch_modes.m = 'off'; p.policy.continuous.sensitivity = 1.25; p.dirty.value = true;
  p.dashboard.refreshTuningChannels = async () => ({});
  p.dashboard.syncTuningBaseValues = async () => ({items:[{...row}]});
  p.dashboard.saveTuningBaseValues = async (_site, rows) => ({items:rows.map(x=>({...x}))});
  await p.sync('weight');
  assert.equal(p.dirty.value,true);
  assert.equal(p.savedPolicy.value.dispatch_modes.m,'auto');
  assert.equal(p.policy.dispatch_modes.m,'off');
  let saved;
  p.dashboard.saveTuningPolicy = async (_site, policy, mode, _preflight, expected) => {
    saved={policy:cloneFixture(policy), mode, expected:cloneFixture(expected)};
    p.dashboard.tuningPolicy=async()=>saved;
    return saved;
  };
  await p.save();
  assert.equal(saved.policy.dispatch_modes.m,'off');
  assert.equal(saved.policy.continuous.sensitivity,1.25);
  assert.equal(saved.expected.dispatch_modes.m,'auto');
  assert.equal(p.dirty.value,false);
});

test('runtime refresh adopts external policy changes and preserves conflicting drafts', async () => {
  const p = page(); await p.load();
  const remote=cloneFixture(p.savedPolicy.value); remote.dispatch_modes.m='observe';
  p.dashboard.tuningPolicy=async()=>({mode:'observe',policy:cloneFixture(remote)});
  await p.refreshRuntime();
  assert.equal(p.policy.dispatch_modes.m,'observe');
  assert.equal(p.savedPolicy.value.dispatch_modes.m,'observe');
  p.policy.continuous.sensitivity=1.75; p.dirty.value=true;
  remote.dispatch_modes.m='off'; remote.continuous.min_samples=60;
  await p.refreshRuntime();
  assert.equal(p.policy.continuous.sensitivity,1.75);
  assert.equal(p.policyConflict.value,true);
  let writes=0; p.dashboard.saveTuningPolicy=async()=>{writes++};
  await p.save(); assert.equal(writes,0);
  p.cancelChanges();
  assert.equal(p.policy.dispatch_modes.m,'off');
  assert.equal(p.policy.continuous.min_samples,60);
  assert.equal(p.policyConflict.value,false);
});

test('explanation and calculated target use the evaluation snapshot despite drafts or saved policy changes', async () => {
  const p = page(); await p.load();
  const params=cloneFixture(p.savedPolicy.value.continuous);
  p.states.value=[{...state(100), evaluation:{base_weight:100, evaluated_at:'2026-10-02T00:00:00Z',params},
    k_speed:1.2,k_otps:1,k_cache:1,k_error:1,metric_ttft_p50:1,metric_ttft_p90:1,metric_ttft_p95:1,
    baseline_ttft_p50:2,baseline_ttft_p90:2,baseline_ttft_p95:2,smoothed_error_rate:0}];
  const explanation=p.factorExplanation(row), target=p.calculatedWeight(row);
  p.policy.continuous.sensitivity=2; p.policy.continuous.speed_p50_weight=.9;
  p.savedPolicy.value.continuous.combined_max_factor=1;
  assert.equal(p.factorExplanation(row),explanation);
  assert.equal(p.calculatedWeight(row),target);
  assert.match(explanation,/当轮已保存参数/);
  p.policy.dispatch_modes.m='off';
  assert.equal(p.factorExplanation({...row,base_weight:0}),explanation);
  p.states.value[0].evaluation.performance_evaluated=false;
  assert.match(p.factorExplanation(row),/本轮未重新计算性能系数/);
  delete p.states.value[0].evaluation;
  assert.equal(p.calculatedWeight(row),null);
  assert.match(p.factorExplanation(row),/缺少参数快照/);
});

test('event outcomes distinguish dispatch, confirmation and unavailable legacy receipts', async () => {
  const p=page(); await p.load();
  assert.equal(p.eventResult({status:'pending'}),'等待执行');
  assert.equal(p.eventResult({status:'delivered'}),'已下发，待确认');
  assert.equal(p.eventResult({status:'succeeded'}),'执行成功');
  assert.equal(p.eventResult({status:'failed'}),'执行失败');
  assert.equal(p.eventResult({status:'unknown'}),'结果未知');
  assert.equal(p.eventResult({status:'auto_executed'}),'结果未知');
});


test('queued circuit zero is displayed as awaiting confirmation', async () => {
  const p=page(); await p.load();
  p.states.value=[{...state(100,0),phase:'circuit',capacity:{initialized:true,phase:'awaiting_write',pending_command_id:'queued-zero',confirmed_weight:50,pending_weight:0}}];
  assert.equal(p.rowStatus(row).label,'等待执行确认');
  assert.match(p.evaluationText(row),/等待执行回执/);
  assert.doesNotMatch(p.evaluationText(row),/已熔断/);
});


test('channel notification refreshes evaluation and execution result without the periodic timer', async () => {
  const p = page(); await p.load();
  p.policy.dispatch_modes.m = 'off'; p.dirty.value = true;
  p.dashboard.tuningContinuousStates = async () => ({items:[state(99, 0)]});
  p.dashboard.tuningRecommendations = async () => ({items:[{id:'circuit',status:'succeeded',channel_id:1}]});
  p.dashboard.tuningBaseValues = async () => ({items:[{...row,current_weight:0}]});
  const refreshed = deferred(); let polls = 0;
  p.dashboard.tuningChannelChanges = async (_site, revision, signal) => {
    if (++polls === 1) return {revision:'new-result'};
    assert.equal(revision, 'new-result');
    refreshed.resolve();
    return new Promise((_resolve,reject) => signal.addEventListener('abort', () => reject(new Error('aborted')), {once:true}));
  };
  const watching = p.watchChannelChanges();
  try {
    await refreshed.promise;
    assert.equal(p.states.value[0].last_observed_requests, 99);
    assert.equal(p.bases.value[0].current_weight, 0);
    assert.equal(p.events.value[0].status, 'succeeded');
    assert.equal(p.policy.dispatch_modes.m, 'off');
    assert.equal(p.dirty.value, true);
  } finally { p.stopWatching(); await watching; }
});

test('automatic closure is a confirmed CT origin, independent of NewAPI numeric status', async () => {
  const p = page(); await p.load();
  p.channels.value = [{channel_id:1,channel_name:'ct',status:'disabled',weight:0,priority:1,models:['m'],group_name:'default'}];
  p.channelDirectorySite.value='a'; p.activeModel.value='m';
  const channel = p.modelChannelRows.value[0];
  p.acceptStates('a',[{...state(0),circuit_disabled:true,circuit_status_target:0,phase:'circuit'}]);
  assert.equal(p.channelStatusFor(channel),'auto_disabled');
  p.channelSwitchFilter.value='auto_disabled'; assert.equal(p.displayedRows.value.length,1);
  p.channelSwitchFilter.value='disabled'; assert.equal(p.displayedRows.value.length,0);
  for (const pending of [2,3]) {
   p.acceptStates('a',[{...state(0),circuit_disabled:true,circuit_status_target:pending}]);
   assert.equal(p.channelStatusFor(channel),'disabled','unconfirmed intent does not claim origin');
  }
  p.acceptStates('a',[{...state(0),circuit_disabled:false}]);
  for (const status of ['disabled','2','auto_disabled','3']) {
   p.channels.value[0].status=status;
   assert.equal(p.channelStatusFor(channel),'disabled','NewAPI closure is manual in CT');
  }
  p.acceptStates('other-site',[{...state(0),circuit_disabled:true,circuit_status_target:0}]);
  assert.equal(p.channelStatusFor(channel),'disabled','another site cannot own this closure');
  p.channels.value[0].status='enabled';
  assert.equal(p.channelStatusFor(channel),'enabled','a live enabled channel is never shown closed');
});

test('external enable followed by manual disable releases the previous CT label', async () => {
  const p = page(); await p.load();
  p.channels.value = [{channel_id:1,channel_name:'ct',status:'disabled',weight:0,priority:1,models:['m'],group_name:'default'}];
  p.channelDirectorySite.value='a'; p.activeModel.value='m';
  const channel = p.modelChannelRows.value[0];
  p.acceptStates('a',[{...state(0),circuit_disabled:true,circuit_status_target:0,phase:'circuit'}]);
  assert.equal(p.channelStatusFor(channel),'auto_disabled');
  p.channels.value[0].status='enabled';
  p.acceptStates('a',[{...state(0),circuit_disabled:false,circuit_status_target:0,phase:'normal'}]);
  assert.equal(p.channelStatusFor(channel),'enabled');
  p.channels.value[0].status='disabled';
  assert.equal(p.channelStatusFor(channel),'disabled');
  p.channelSwitchFilter.value='auto_disabled';
  assert.equal(p.displayedRows.value.length,0);
  p.channelSwitchFilter.value='disabled';
  assert.equal(p.displayedRows.value.length,1);
});

test('write failures describe bounded task retries without an operator-only state', async () => {
  const p=page(); await p.load();
  const failed={...state(100),paused_reason:'write_failed',write_failure_streak:5,last_write_error:'Invalid parameters'};
  assert.match(p.phaseText(failed),/连续失败5次后结束本次任务并重新评估/);
  assert.doesNotMatch(p.phaseText(failed),/人工|只观察|开启自动执行/);
  p.acceptStates('a',[{...state(100),paused_reason:'',write_failure_streak:0,phase:'normal'}]);
  assert.doesNotMatch(p.rowStatus(row).label,/重试已停止|写入失败/);
});

test('execution wording distinguishes preserved weights from real targets and pending writes', async () => {
  const p = page(); await p.load();
  p.savedPolicy.value.dispatch_modes.m = 'auto';
  const online = { ...row, current_weight: 500 };
  p.states.value = [{ ...state(148, 500), baseline_ready: false, otps_ready: false }];
  assert.equal(p.executionWeightText(online), '本轮不调权，保持当前权重：500');
  p.policy.dispatch_modes.m = 'off';
  assert.equal(p.executionWeightText({ ...online, base_weight: 0 }), '本轮不调权，保持当前权重：500', 'unsaved edits do not rewrite evaluation');
  Object.assign(p.states.value[0], { last_written_weight: 500, last_write_at: '2026-10-10T01:00:00Z' });
  assert.equal(p.executionWeightText({ ...row, snapshot_at: '2026-10-10T00:59:00Z' }), '本轮不调权，保持当前权重：500');
  p.states.value[0].proposed_weight = 375;
  p.states.value[0].capacity = { initialized: true, phase: 'reducing' };
  assert.equal(p.executionWeightText(online), '本轮目标权重：375', 'capacity can reduce even without performance evidence');
  p.states.value[0].capacity = { pending_command_id: 'cmd', pending_weight: 375 };
  assert.equal(p.executionWeightText(online), '待确认权重：375（等待执行回执）');
  p.states.value[0].capacity = undefined;
  p.states.value[0].paused_reason = 'write_failed';
  assert.equal(p.executionWeightText(online), '写入失败，目标权重：375（未确认生效）');
  p.states.value[0] = state(0, 0);
  p.states.value[0].phase = 'circuit';
  assert.equal(p.executionWeightText(online), '本轮目标权重：0');
  p.states.value[0] = state(0, 500);
  assert.equal(p.executionWeightText(online), '本轮不调权，保持当前权重：500');
  p.states.value[0] = state(148, 100);
  assert.equal(p.executionWeightText(online), '本轮目标权重：100');
  p.savedPolicy.value.dispatch_modes.m = 'observe';
  assert.equal(p.executionWeightText(online), '观察目标权重：100（不执行）');
  p.savedPolicy.value.dispatch_modes.m = 'off';
  assert.equal(p.executionWeightText(online), '未参与调权');
  p.states.value = [];
  assert.equal(p.executionWeightText(online), '等待首次评估');
  assert.doesNotMatch(sfc, /安全限制后拟执行/);
  assert.equal((sfc.match(/\{\{ executionWeightText\(row\) \}\}/g) ?? []).length, 2, 'desktop and mobile share wording');
});

test('capacity decrease setting defaults for old policies and survives save and reload', async () => {
  const p = page();
  const old = cloneFixture(p.policy);
  old.dispatch_modes = {m:'auto'};
  delete old.continuous.capacity_max_decrease_percent;
  p.dashboard.tuningPolicy = async () => ({mode:'observe', policy:cloneFixture(old)});
  await p.load();
  assert.equal(p.policy.continuous.capacity_max_decrease_percent, 25);
  let saved;
  p.dashboard.saveTuningPolicy = async (_site, next) => {
    saved = cloneFixture(next);
    p.dashboard.tuningPolicy = async () => ({mode:'observe', policy:cloneFixture(saved)});
  };
  p.policy.continuous.capacity_max_decrease_percent = 40;
  p.dirty.value = true;
  assert.ok(p.mobileRuleChanges.value.some(item => JSON.stringify(item).includes('单次容量最大降幅')));
  await p.save();
  assert.equal(saved.continuous.capacity_max_decrease_percent, 40);
  assert.equal(p.policy.continuous.capacity_max_decrease_percent, 40);
  assert.equal(p.savedPolicy.value.continuous.capacity_max_decrease_percent, 40);
  const evidence = {...state(100), capacity:{initialized:true,phase:'normal',utilization:0.2}};
  evidence.evaluation.params = {...evidence.evaluation.params, capacity_max_decrease_percent:10};
  p.states.value = [evidence];
  assert.match(p.factorExplanation(row), /单次容量最大降幅 10%/);
  p.policy.continuous.capacity_max_decrease_percent = 60;
  assert.match(p.factorExplanation(row), /单次容量最大降幅 10%/, 'draft and saved rules cannot rewrite evaluation evidence');
  delete p.states.value[0].evaluation.params.capacity_max_decrease_percent;
  assert.match(p.factorExplanation(row), /单次容量最大降幅 25%/);
  assert.match(sfc, /v-model="policy\.continuous\.capacity_max_decrease_percent" :min="1" :max="100"/);
});
