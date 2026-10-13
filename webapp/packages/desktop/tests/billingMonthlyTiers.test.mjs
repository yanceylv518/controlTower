import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
import {computed,ref} from 'vue';
const source=readFileSync(new URL('../src/components/BillingMonthlyPreview.vue',import.meta.url),'utf8');
const script=source.split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*;\r?\n/gm,'');
const code=ts.transpile(script,{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None});
function view(){return new Function('computed','ref','watch','onBeforeUnmount','defineExpose','useBillingDownload',code+';return {tableRows,data,dimension,page,format};')(computed,ref,()=>{},()=>{},()=>{},()=>({pending:ref([]),download:()=>{}}));}
test('daily model rows expand saved tiers without duplicating parent summary',()=>{
 const v=view();v.dimension.value='daily';v.data.value={rows:[['2026-09-01','m','2'],['2026-09-02','normal','1']],tier_rows:{0:[['2026-09-01','高峰','1'],['2026-09-01','低谷','1']]},totals:['合计','','3']};
 assert.equal(v.tableRows.value.length,2);assert.equal(v.tableRows.value[0].children.length,2);assert.equal(v.tableRows.value[1].children,undefined);assert.equal(v.tableRows.value[0].children[0].tier,true);assert.equal(v.data.value.totals[2],'3');
 assert.equal(new Set([v.tableRows.value[0].key,...v.tableRows.value[0].children.map(c=>c.key)]).size,3);
 v.dimension.value='month';assert.equal(v.tableRows.value[0].children,undefined);
 v.dimension.value='token';assert.equal(v.tableRows.value[0].children,undefined);
});
test('old month snapshots have no fabricated tier children',()=>{
 const v=view();v.dimension.value='daily';v.data.value={rows:[['2026-09-01','m','2']]};assert.equal(v.tableRows.value[0].children,undefined);
 v.page.value=2;assert.equal(v.tableRows.value[0].key,'2:0');
});
