import { ref, watch, onUnmounted } from 'vue'
import { client } from '../api'
import { parseArchiveCurrency, type ArchiveCurrency } from '../utils/archiveMoney'
export function useArchiveCurrency(site: () => string) {
  const money=ref<ArchiveCurrency>(),moneyError=ref(''),moneyBusy=ref(false)
  let sequence=0
  function clear(){sequence++;money.value=undefined;moneyError.value='';moneyBusy.value=false}
  async function refreshMoney(){
    const id=site(),ticket=++sequence;money.value=undefined;moneyError.value='';moneyBusy.value=!!id
    if(!id)return
    try{
      const raw=await client.request<unknown>(`/api/dashboard/passthrough/currency?${new URLSearchParams({site:id})}`)
      if(ticket===sequence&&site()===id)money.value=parseArchiveCurrency(raw,id)
    }catch{if(ticket===sequence&&site()===id)moneyError.value='站点币种配置读取失败，金额不可用。请检查站点只读连接和 Server 版本。'}
    finally{if(ticket===sequence)moneyBusy.value=false}
  }
  watch(site,clear);onUnmounted(clear)
  return {money,moneyError,moneyBusy,refreshMoney}
}
