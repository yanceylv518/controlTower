import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
const source=readFileSync(new URL('../src/utils/billingGenerationReason.ts',import.meta.url),'utf8');
const code=ts.transpile(source.replace(/^import.*$/gm,'').replace('export function','function'),{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None});
const reason=new Function(`${code};return billingGenerationReason;`)();
test('98 percent waiting shows source timeout even when batch daily counters are zero',()=>{
  const item={outcome:'awaiting_monthly',pending:0,running:0,blocker:{kind:'billing',period:'daily',status:'running',reason:'source_timeout',attempts:3}};
  assert.match(reason(item),/源查询超时/);assert.match(reason(item),/第 3 轮/);
  assert.match(reason({...item,blocker:undefined}),/等待调度月账汇总/);
  assert.equal(reason({...item,outcome:'complete'}),'');
  assert.equal(reason({outcome:'failed',days:[{error:'source query timeout'}]}),'source query timeout');
});
test('publication and reports give distinct queue reasons',()=>{
  assert.match(reason({outcome:'awaiting_monthly',blocker:{kind:'billing',period:'monthly',status:'publishing'}}),/汇总月账/);
  assert.match(reason({outcome:'awaiting_monthly',blocker:{kind:'report',status:'running'}}),/报表/);
});
