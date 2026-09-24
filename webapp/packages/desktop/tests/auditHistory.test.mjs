import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { ref, shallowRef } from 'vue';

function compile(path) {
  const source = readFileSync(new URL(path, import.meta.url), 'utf8').replace(/^import .*\r?\n/gm, '').replace(/export function/g, 'function');
  return ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
}
const useAsyncData = new Function('ref', 'shallowRef', 'billingReadErrorMessage', `${compile('../src/composables/useAsyncData.ts')}; return useAsyncData;`)(ref, shallowRef, () => 'failed');
const makeHistory = new Function('ref', 'dashboard', 'useAsyncData', `${compile('../src/composables/useAuditHistory.ts')}; return useAuditHistory;`);
function setup() {
  const calls = [];
  const filters = { actor: 'a' };
  let size = 2;
  const dashboard = { operationAudits(params, signal) {
    let resolve, reject;
    const promise = new Promise((a, b) => { resolve = a; reject = b; });
    calls.push({ params, signal, resolve, reject });
    return promise;
  } };
  return { history: makeHistory(ref, dashboard, useAsyncData)(() => ({ ...filters }), () => size), calls, filters, setSize: v => { size = v; } };
}
const rows = (more = true) => ({items: [{id: 'b', created_at: '2026-09-24T01:00:00.123457Z'}, {id: 'a', created_at: '2026-09-24T01:00:00.123456Z'}], has_more: more});
const tick = () => new Promise(resolve => setImmediate(resolve));

test('list renders before slow total; cursor keeps microseconds and page changes reuse total', async () => {
  const {history:h, calls} = setup();
  const first = h.state.reload(); calls[0].resolve(rows()); await first;
  assert.equal(h.state.loading.value, false);
  assert.equal(h.counting.value, true);
  assert.equal(h.total.value, undefined);
  assert.equal(calls[0].params.list_only, true);
  const next = h.next();
  assert.equal(calls[2].params.before_time, '2026-09-24T01:00:00.123456Z');
  assert.equal(calls[2].params.before_id, 'a');
  assert.equal(calls[2].params.offset, undefined);
  calls[2].resolve(rows(false)); await next;
  assert.equal(calls.length, 3);
  assert.equal(h.next(), undefined);
  calls[1].resolve({total: 5}); await tick();
  const prev = h.previous(); calls[3].resolve(rows()); await prev;
  assert.equal(calls[3].params.before_time, undefined);
  assert.equal(h.total.value, 5);
  assert.equal(calls.length, 4);
});

test('filter/size reset cancels old list/count and ignores late responses', async () => {
  const {history:h,calls,filters,setSize} = setup();
  const first = h.state.reload(); calls[0].resolve(rows()); await first;
  const next = h.next();
  filters.actor = 'b'; setSize(50);
  const reset = h.reset();
  assert.equal(calls[1].signal.aborted, true);
  assert.equal(calls[2].signal.aborted, true);
  assert.equal(h.page.value, 1);
  assert.equal(calls[3].params.actor, 'b');
  assert.equal(calls[3].params.limit, 50);
  assert.equal(calls[3].params.before_id, undefined);
  calls[1].resolve({total:999}); calls[2].resolve(rows()); await next;
  calls[3].resolve({items:[],has_more:false}); await reset;
  assert.equal(h.total.value, undefined);
  calls[4].resolve({total:0}); await tick();
  assert.equal(h.total.value, 0);
  assert.deepEqual(h.state.data.value.items, []);
});

test('count failure leaves list usable and retries independently', async () => {
  const {history:h,calls} = setup();
  const first = h.state.reload(); calls[0].resolve(rows()); await first;
  calls[1].reject(new Error('timeout')); await tick();
  assert.equal(h.countError.value, true);
  assert.equal(h.state.error.value, '');
  const retry = h.loadCount(true); calls[2].resolve({total:4}); await retry;
  assert.equal(h.total.value, 4);
  assert.equal(h.countError.value, false);
  const pending = h.loadCount(true); h.cancel();
  assert.equal(calls[3].signal.aborted, true);
  calls[3].resolve({total:99}); await pending;
  assert.equal(h.total.value, 4);
});

test('default local today request includes midnight and excludes next midnight', () => {
  const source = readFileSync(new URL('../src/views/AuditsView.vue', import.meta.url),'utf8');
  const fragment = source.slice(source.indexOf('function todayTimeRange'),source.indexOf('const history ='));
  const compiled = ts.transpileModule(fragment,{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText;
  const {draftTimeRange,appliedTimeRange,appliedTimeParams} = new Function('ref','reactive','computed', `${compiled};return {draftTimeRange,appliedTimeRange,appliedTimeParams};`)(ref,v=>v,fn=>({get value(){return fn();}}));
  assert.equal(draftTimeRange.value[0].getHours(),0);
  assert.equal(draftTimeRange.value[1].getHours(),23);
  assert.equal(draftTimeRange.value[1].getSeconds(),59);
  const tomorrow = new Date(draftTimeRange.value[0]); tomorrow.setDate(tomorrow.getDate()+1);
  assert.equal(appliedTimeParams.value.to,tomorrow.toISOString());
  draftTimeRange.value[0].setDate(1);
  assert.notEqual(draftTimeRange.value[0],appliedTimeRange.value[0]);
});
