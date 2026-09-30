import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { computed, nextTick, ref } from 'vue';
const source = readFileSync(new URL('../src/utils/tuningCapacity.ts', import.meta.url), 'utf8').replace(/export /g, '');
const code = ts.transpileModule(source, {compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText;
const {compactCapacity,parseCapacity,capacityInWan,parseCapacityWan,parseCapacityRpm} = new Function(code+'; return {compactCapacity,parseCapacity,capacityInWan,parseCapacityWan,parseCapacityRpm}')();
test('capacity display distinguishes missing, unlimited and large values',()=>{
 assert.equal(compactCapacity(undefined),'—');assert.equal(compactCapacity(0,true),'不限');assert.equal(compactCapacity(0),'0');assert.equal(compactCapacity(21402414),'2140.24万');assert.equal(compactCapacity(100000000),'1亿');
});
test('capacity edits preserve exact integers and support decimal unit input',()=>{
 for(const [input,expected] of [['21402414',21402414],['2000万',20000000],['0.5亿',50000000],['1.001万',10010],['0','0'],['不限',0]]) assert.equal(parseCapacity(input),Number(expected));
});
test('invalid fractional, negative or unsafe capacities cannot be saved',()=>{
 for(const input of ['', '-1', '0.1', '0.0000001', '1e6', '9007199254740992', '错误']) assert.equal(parseCapacity(input),null,input);
});

test('ten-thousand editor round trips existing limits without rounding',()=>{
 for(const value of [0,1,58,10000,21402414,100000000,Number.MAX_SAFE_INTEGER]) assert.equal(parseCapacityWan(capacityInWan(value)),value);
 assert.equal(parseCapacityWan('20'),200000);assert.equal(parseCapacityWan('0.0058'),58);assert.equal(parseCapacityWan(''),0);
});
test('ten-thousand input rejects ambiguous units, excessive decimals and unsafe counts',()=>{
 for(const text of ['20万','1亿','-1','1e4','0.00001','900719925474.0992']) assert.equal(parseCapacityWan(text),null,text);
});

test('RPM limits remain exact request counts while TPM retains ten-thousand conversion',()=>{
 for(const value of [0,1,58,10000,21402414,Number.MAX_SAFE_INTEGER]) assert.equal(parseCapacityRpm(String(value)),value);
 assert.equal(parseCapacityRpm('58'),58);assert.equal(parseCapacityWan('58'),580000);
 assert.equal(parseCapacityRpm(''),0);assert.equal(parseCapacityRpm('  '),0);assert.equal(parseCapacityRpm(' 100 '),100);
 for(const text of ['0.0058','1.0','20万','1亿','-1','1e4','9007199254740992','错误']) assert.equal(parseCapacityRpm(text),null,text);
});

test('capacity component uses metric-specific display, editing and saved values',async()=>{
 const sfc = readFileSync(new URL('../src/components/TuningCapacityMetric.vue', import.meta.url),'utf8');
 const script = sfc.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm,'');
 const compiled = ts.transpileModule(script,{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText;
 const create = new Function('defineProps','computed','nextTick','ref','compactCapacity','capacityInWan','parseCapacityWan','parseCapacityRpm',compiled+'; return {start,confirm,draft,error,display,unit}');
 for(const metric of ['RPM','TPM']) {
  const saved = [];
  const editor = create(()=>({metric,modelValue:58,channel:'test',persist:async value=>{saved.push(value)}}),computed,nextTick,ref,compactCapacity,capacityInWan,parseCapacityWan,parseCapacityRpm);
  await editor.start();
  assert.equal(editor.draft.value,metric === 'RPM' ? '58' : '0.0058');
  assert.equal(editor.unit.value,metric === 'RPM' ? '次/分钟' : '万');
  assert.equal(editor.display(10000),metric === 'RPM' ? '10,000' : '1万');
  assert.equal(editor.display(undefined),'—');assert.equal(editor.display(0),'0');
  editor.confirm();await nextTick();assert.deepEqual(saved,[58]);
  editor.draft.value='100';editor.confirm();await nextTick();assert.equal(saved.at(-1),metric === 'RPM' ? 100 : 1000000);
  if(metric === 'RPM') { editor.draft.value='0.0058';editor.confirm();assert.equal(saved.length,2);assert.match(editor.error.value,/非负整数/); }
  editor.draft.value='';editor.confirm();await nextTick();assert.equal(saved.at(-1),0);
 }
});
