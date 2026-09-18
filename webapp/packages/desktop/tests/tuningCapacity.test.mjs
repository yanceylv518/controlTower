import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
const source = readFileSync(new URL('../src/utils/tuningCapacity.ts', import.meta.url), 'utf8').replace(/export /g, '');
const code = ts.transpileModule(source, {compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText;
const {compactCapacity,parseCapacity,capacityInWan,parseCapacityWan} = new Function(code+'; return {compactCapacity,parseCapacity,capacityInWan,parseCapacityWan}')();
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
