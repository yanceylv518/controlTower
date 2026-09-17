import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { computed, ref, watch, reactive, nextTick, effectScope } from 'vue';

const source = readFileSync(new URL('../src/components/ChannelGroupEditor.vue', import.meta.url), 'utf8');
const utility = readFileSync(new URL('../src/utils/channelGroup.ts', import.meta.url), 'utf8');
const compile = text => ts.transpileModule(text, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { normalizeChannelGroups, splitChannelGroups } = await import('data:text/javascript;base64,' + Buffer.from(compile(utility)).toString('base64'));
const script = compile(source.match(/<script setup lang="ts">([^]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm, ''));
const factory = new Function('computed','ref','watch','defineProps','defineEmits','dashboard','ElMessage','ElMessageBox','normalizeChannelGroups','splitChannelGroups', script + '\nreturn { groups, selected, presets, revision, modified, confirmed, editorGroups, editorName, editorOpen, loadError, storing, usePresets, openEditor, savePreset, removePreset, saveChannel, restore };');
const flush = async () => { await nextTick(); await new Promise(resolve => setImmediate(resolve)); await nextTick(); };
function setup(api = {}) {
  const props = reactive({ site:'a', current:'old,vip', options:['old','vip'], saving:false });
  const calls = [], events = [], messages = [];
  const dashboard = {
    tuningGroupPresets: async () => ({ revision:1, items:[{id:'one',name:'主力',groups:['vip','new']},{id:'two',name:'测试',groups:['new','test']}] }),
    saveTuningGroupPresets: async (site, value) => { calls.push({site,value}); return {...value,revision:value.revision+1}; }, ...api,
  };
  const notice = text => messages.push(text);
  const scope = effectScope();
  const editor = scope.run(() => factory(computed,ref,watch,()=>props,()=>((...args)=>events.push(args)),dashboard,{warning:notice,error:notice,success:notice},{confirm:async()=>{}},normalizeChannelGroups,splitChannelGroups));
  return { editor, props, calls, events, messages, stop:()=>scope.stop() };
}
test('multiple presets merge without duplicates; manual changes do not mutate stored presets', async () => {
  const s=setup(); await flush(); s.editor.selected.value=['one','two']; s.editor.usePresets();
  assert.deepEqual(s.editor.groups.value,['vip','new','test']);
  s.editor.groups.value.push('custom');
  assert.deepEqual(s.editor.presets.value[0].groups,['vip','new']);
  s.editor.confirmed.value=true; await flush(); assert.equal(s.editor.confirmed.value,false);
  s.editor.confirmed.value=true;s.editor.saveChannel();assert.deepEqual(s.events,[['save',['vip','new','test','custom']]]);s.stop();
});
test('saving and editing templates persists to the site without applying channel changes', async () => {
  const s=setup();await flush();s.editor.openEditor();s.editor.editorName.value='新组合';s.editor.editorGroups.value=['a','a','b'];
  await s.editor.savePreset(); assert.equal(s.calls[0].site,'a');assert.equal(s.calls[0].value.revision,1);
  assert.deepEqual(s.calls[0].value.items.at(-1).groups,['a','b']);assert.deepEqual(s.events,[]);
  s.editor.openEditor(s.editor.presets.value[0]);s.editor.editorName.value='主力新版';await s.editor.savePreset();
  assert.equal(s.calls[1].value.items.length,3);assert.equal(s.calls[1].value.items[0].name,'主力新版');
  await s.editor.removePreset(s.editor.presets.value[0]);assert.equal(s.calls[2].value.items.length,2);assert.deepEqual(s.events,[]);s.stop();
});
test('duplicate names and overlong groups never reach persistence', async () => {
  const s=setup();await flush();s.editor.openEditor();s.editor.editorName.value='主力';s.editor.editorGroups.value=['vip'];await s.editor.savePreset();
  s.editor.editorName.value='new';s.editor.editorGroups.value=['x'.repeat(129)];await s.editor.savePreset();
  assert.equal(s.calls.length,0);assert.equal(s.messages.length,2);s.stop();
});
test('concurrent changes reload presets without losing the draft', async () => {
  const s=setup({saveTuningGroupPresets:async()=>{throw {code:'presets_changed'};}});await flush();
  s.editor.openEditor();s.editor.editorName.value='mine';s.editor.editorGroups.value=['custom'];await s.editor.savePreset();
  assert.equal(s.editor.editorOpen.value,true);assert.equal(s.editor.editorName.value,'mine');assert.deepEqual(s.editor.editorGroups.value,['custom']);s.stop();
});
test('stale site requests cannot overwrite a newly selected site', async () => {
  let resolveA;const s=setup({tuningGroupPresets:site=>site==='a'?new Promise(resolve=>resolveA=resolve):Promise.resolve({revision:9,items:[]})});
  s.props.site='b';await flush();resolveA({revision:3,items:[{id:'a',name:'a',groups:['a']}]});await flush();
  assert.equal(s.editor.revision.value,9);assert.deepEqual(s.editor.presets.value,[]);assert.deepEqual(s.editor.groups.value,['old','vip']);s.stop();
});
