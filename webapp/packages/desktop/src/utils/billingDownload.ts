import {ref} from 'vue';
import {ElMessage} from 'element-plus';
import {prepareBillingFileDownload} from './httpError';

const pending = ref<string[]>([]);
export function useBillingDownload() {
 async function download(key:string,url:string) {
  if(pending.value.includes(key)) return;
  pending.value=[...pending.value,key];
  const notice=ElMessage({message:'正在准备下载，请稍候…',type:'info',duration:0,showClose:true});
  try { await prepareBillingFileDownload(url); ElMessage.success('已开始下载，请查看浏览器下载列表'); }
  catch(e) { ElMessage.error(e instanceof Error?e.message:'下载失败，请重试'); }
  finally { notice.close(); pending.value=pending.value.filter(v=>v!==key); }
 }
 return {pending,download};
}
