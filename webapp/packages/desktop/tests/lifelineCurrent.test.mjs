import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
const source=readFileSync(new URL('../src/utils/lifelineCurrent.ts',import.meta.url),'utf8');
const code=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText;
const u=new Function('exports',`${code};return exports;`)({});
const now=Date.now(),iso=new Date(now).toISOString();
const row=(id,state='routine',models=['m'])=>({channel_id:id,channel_name:'Channel',models,switch:'enabled',state,confirmed_at:iso,pending:0,accelerated:0});
test('current scope counts channels once even with multiple models and combines independent filters',()=>{
 const rows=[row(1,'routine',['a','b']),row(2,'stopped',['b'])];rows[1].switch='manual_disabled';
 assert.equal(u.currentScope(rows,'','b','').length,2);
 assert.deepEqual(u.currentScope(rows,'2','b','manual_disabled').map(r=>r.channel_id),[2]);
 assert.equal(u.currentScope(rows,'','a','manual_disabled').length,0);
});
test('attention order distinguishes in-flight requests, unknown, recovery, waiting and error confirmation',()=>{
 const rows=[row(1),row(2,'stopped'),row(3,'waiting'),row(4,'recovery'),row(5,'unknown'),{...row(6,'stopped'),pending:1},{...row(7),accelerated:1}];
 assert.deepEqual(u.currentOrder(rows,'all','attention').map(r=>r.channel_id),[5,6,4,3,7,1,2]);
 assert.deepEqual(u.currentOrder(rows,'routine','id').map(r=>r.channel_id),[1,7]);assert.equal(rows[0].channel_id,1);
});
test('snapshot expiry is separate from channel state',()=>{
 assert.equal(u.isCurrentSnapshotStale(iso,now+59000),false);
 assert.equal(u.isCurrentSnapshotStale(iso,now+61000),true);
 assert.equal(u.isCurrentSnapshotStale(undefined,now),false);
 assert.equal(u.lifelineStates.stopping,undefined);
});
