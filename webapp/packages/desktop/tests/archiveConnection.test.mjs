import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import {computed,reactive,ref} from 'vue'

const source=readFileSync(new URL('../src/components/ArchiveConnectionSettings.vue',import.meta.url),'utf8').split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const js=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText
function setup(){
 const props={siteId:'a'},requests=[]
 const client={request:(url,opts)=>new Promise((resolve,reject)=>requests.push({url,opts,resolve,reject}))}
 const instance=new Function('computed','reactive','ref','watch','onUnmounted','defineProps','client','ElMessage','ApiError',js+';return {form,password,configured,ready,busy,tested,confirmed,signature,validTest,load,submit,error}')
 return {...instance(computed,reactive,ref,()=>{},()=>{},()=>props,client,{success(){}},class extends Error{}),props,requests}
}
test('switching site rejects old connection and clears credentials',async()=>{
 const c=setup(),a=c.load();c.password.value='old-secret';c.props.siteId='b';const b=c.load()
 c.requests[1].resolve({configured:false});await b
 c.requests[0].resolve({configured:true,connection:{host:'old-host',version:2}});await a
 assert.equal(c.form.host,'');assert.equal(c.password.value,'');assert.equal(c.configured.value,false)
})
test('changed draft invalidates test and prevents save until confirmed',async()=>{
 const c=setup(),load=c.load();c.requests[0].resolve({configured:false});await load
 Object.assign(c.form,{host:'archive',database:'logs',username:'reader'});c.password.value='secret'
 await c.submit(true);assert.equal(c.requests.length,1)
 const check=c.submit(false);c.requests[1].resolve({connection:{...c.form,source_hash:'a'.repeat(64),password_set:true}});await check
 assert.equal(c.validTest.value,true);await c.submit(true);assert.equal(c.requests.length,2)
 c.confirmed.value=true;c.form.host='another';assert.equal(c.validTest.value,false);await c.submit(true);assert.equal(c.requests.length,2)
})
test('save clears password and uses tested site version',async()=>{
 const c=setup(),load=c.load();c.requests[0].resolve({configured:false});await load
 Object.assign(c.form,{host:'archive',database:'logs',username:'reader'});c.password.value='secret'
 const check=c.submit(false);c.requests[1].resolve({connection:{...c.form,source_hash:'a'.repeat(64),password_set:true}});await check
 c.confirmed.value=true;const save=c.submit(true)
 assert.equal(c.requests[2].opts.method,'PUT');assert.equal(JSON.parse(c.requests[2].opts.body).version,0)
 c.requests[2].resolve({saved:true,connection:{...c.form,version:1}});await save
 assert.equal(c.password.value,'');assert.equal(c.configured.value,true);assert.equal(c.form.version,1)
})
test('load failure cannot be mistaken for an empty writable configuration',async()=>{
 const c=setup(),load=c.load();c.requests[0].reject(new Error('offline'));await load
 assert.equal(c.ready.value,false);await c.submit(false);assert.equal(c.requests.length,1)
})
