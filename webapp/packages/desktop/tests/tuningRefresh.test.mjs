import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, reactive, ref } from 'vue'

// Exercise the actual page script with controlled API promises. Lifecycle
// timers and unrelated components are excluded so request ordering is explicit.
const sfc = readFileSync(new URL('../src/views/ContinuousTuningView.vue', import.meta.url), 'utf8')
const script = sfc.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
const row = { channel_id: 1, model_name: 'm', base_weight: 100, base_priority: 1, current_weight: 80, current_priority: 1, group_name: 'default,vip', models: ['m'] }
const state = (requests, weight = 80) => ({ channel_id: 1, model_name: 'm', last_observed_requests: requests, proposed_weight: weight, speed_stats_version: 1, phase: 'normal', metric_ready: true, baseline_ready: true, updated_at: '2026-09-14T00:00:00Z' })
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }

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
  }
  const names = ['computed', 'reactive', 'ref', 'watch', 'onMounted', 'onBeforeUnmount', 'useFiltersStore', 'dashboard', 'formatTime', 'ApiError', 'ElMessage', 'ElMessageBox', 'useMobileViewport', 'splitChannelGroups', 'matchesChannelGroup']
  const create = new Function(...names, `${compiled}\nreturn { refreshCurrentRates, ratesError, saveCapacity, saving, mobileEditRow, stageMobileEdit, mobileChanges, mobilePriorityChanges, mobileRuleChanges, mobileSaveOpen, load, refreshRuntime, loadChannelDirectory, applyGroupLocally, acceptStates, stateFor, sampleText, evaluationText, states, refreshError, bases, channels, policy, channelQuery, selectedGroupFilter, channelStatusFilter, displayedRows, activeModel, dirty, selectModel, fieldChanged, savedBases, originalBase, calculatedWeight, displayedSpeedFactor, coefficientCell, overallEvaluationStatus, coefficientEmptyText, coefficientSpan, displayedPriority, editPriority, priorityLocked, cancelChanges, limitReason, currentRates, ratesReady, rowStatus, eventResult, eventResultClass, events, filteredEvents, eventDateRange };`)
  const groupSource = readFileSync(new URL('../src/utils/channelGroup.ts', import.meta.url), 'utf8').replace(/export /g, '')
  const groupCode = ts.transpileModule(groupSource, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
  const { splitChannelGroups, matchesChannelGroup } = new Function(groupCode + '; return { splitChannelGroups, matchesChannelGroup };')()
  const view = create(computed, reactive, ref, () => {}, () => {}, () => {}, () => filters, dashboard, String, ApiError, {info() {}, success() {}}, {}, () => ref(false), splitChannelGroups, matchesChannelGroup)
  return { ...view, filters, dashboard }
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

test('channel search and attention filter retain circuit precedence over output substitution', async () => {
  const p = page(); await p.load(); p.activeModel.value='m';
  p.bases.value = [{...row, channel_name:'north', max_rpm:0,max_tpm:0}];
  p.acceptStates('a',[{...state(100), phase:'circuit',otps_ready:true,otps_stats_version:1,metric_ready:false}]);
  assert.equal(p.rowStatus(p.bases.value[0]).label,'熔断');
  p.channelStatusFilter.value='attention'; assert.equal(p.displayedRows.value.length,1);
  p.channelQuery.value='south'; assert.equal(p.displayedRows.value.length,0);
  p.channelQuery.value='1'; assert.equal(p.displayedRows.value.length,1);
})

test('runtime overview filters channels by a matching group item', async () => {
  assert.match(sfc, /matchesChannelGroup\(row.group_name, selectedGroupName.value\)/)
  assert.match(sfc, /:data="displayedRows"/)
  const p = page(); await p.load(); p.activeModel.value = 'm';
  p.bases.value = [
    { ...row, channel_id: 1, channel_name: 'primary', group_name: 'default,vip' },
    { ...row, channel_id: 2, channel_name: 'fast', group_name: 'default,fast' },
  ];
  p.selectedGroupFilter.value = { kind: 'group', name: 'vip' };
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [1]);
  p.selectedGroupFilter.value = { kind: 'group', name: 'fast' };
  assert.deepEqual(p.displayedRows.value.map(item => item.channel_id), [2]);
  p.selectedGroupFilter.value = { kind: 'group', name: 'missing' };
  assert.equal(p.displayedRows.value.length, 0);
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
  assert.equal(p.calculatedWeight({...row, base_weight:0}), null);
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
