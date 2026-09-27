export type ArchiveCurrency = { site_id: string; type: 'USD'|'CNY'|'CUSTOM'|'TOKENS'; symbol: string; raw_quota_per_unit: string; exchange_rate: string; observed_at: string }
type Fraction = { n: bigint; d: bigint }
function decimal(raw: string): Fraction {
  if (raw.length > 100) throw new Error('换算参数过长')
  const match = /^(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d{1,3}))?$/.exec(raw)
  if (!match) throw new Error('换算参数无效')
  const fraction=match[2]??'', exponent=Number(match[3]??'0')-fraction.length
  if(Math.abs(exponent)>100)throw new Error('换算参数超出范围')
  const n=BigInt(match[1]+fraction)
  return exponent>=0?{n:n*10n**BigInt(exponent),d:1n}:{n,d:10n**BigInt(-exponent)}
}
export function parseArchiveCurrency(raw: unknown, site: string): ArchiveCurrency {
  if(!raw || typeof raw!=='object')throw new Error('站点币种配置未加载')
  const c=raw as ArchiveCurrency
  if(c.site_id!==site || !['USD','CNY','CUSTOM','TOKENS'].includes(c.type) || typeof c.symbol!=='string' || c.symbol.length>32 || (c.type==='CUSTOM'&&!c.symbol.trim()) || typeof c.observed_at!=='string' || !Number.isFinite(Date.parse(c.observed_at)))throw new Error('站点币种配置或归属无效，请确认 Server 已更新')
  for(const value of [c.raw_quota_per_unit,c.exchange_rate])if(typeof value!=='string'||decimal(value).n<=0n)throw new Error('站点换算单位或汇率无效')
  return {site_id:c.site_id,type:c.type,symbol:c.symbol,raw_quota_per_unit:c.raw_quota_per_unit,exchange_rate:c.exchange_rate,observed_at:c.observed_at}
}
function format(n: bigint,d: bigint,digits=6){
  const scale=10n**BigInt(digits),rounded=(n*scale*2n+d)/(d*2n)
  return digits?`${rounded/scale}.${String(rounded%scale).padStart(digits,'0')}`:String(rounded)
}
export function currencyUnit(c?: ArchiveCurrency){return !c?'币种未加载':c.type==='TOKENS'?'额度':c.type==='CUSTOM'?c.symbol:`${c.type} ${c.symbol}`}
export function quotaAmount(quota: bigint,c?: ArchiveCurrency): string {
  if(!c || quota<0n)return '—'
  if(c.type==='TOKENS')return String(quota)
  const unit=decimal(c.raw_quota_per_unit),rate=decimal(c.exchange_rate)
  return format(quota*unit.d*rate.n,unit.n*rate.d)
}
// Display precision only; retain ten significant digits, including tiny prices.
function significant(n:bigint,d:bigint,digits=10):string {
 if(n===0n)return '0'
 let exponent=n.toString().length-d.toString().length
 if(exponent>=0 ? n<d*10n**BigInt(exponent) : n*10n**BigInt(-exponent)<d)exponent--
 const places=digits-1-exponent
 if(places<0){const scale=10n**BigInt(-places);return String(((n*2n+d*scale)/(2n*d*scale))*scale)}
 return format(n,d,places).replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'')
}
// ModelRatio is a quota/token coefficient. Display it with this query's site unit,
// not a hard-coded USD-per-million conversion. Request prices are USD references.
export function historicalPrice(c: ArchiveCurrency|undefined,kind:'token'|'request',...factors: unknown[]):string {
  if(!c)return '—'
  try{
    let n=1n,d=1n
    for(const factor of factors){if(typeof factor!=='string'&&typeof factor!=='number')return '—';const f=decimal(String(factor));n*=f.n;d*=f.d}
    const unit=decimal(c.raw_quota_per_unit),rate=decimal(c.exchange_rate)
    if(kind==='token'){
      n*=1000000n
      if(c.type!=='TOKENS'){n*=unit.d*rate.n;d*=unit.n*rate.d}
    }else if(c.type==='TOKENS'){n*=unit.n;d*=unit.d}else{n*=rate.n;d*=rate.d}
    return significant(n,d)
  }catch{return '—'}
}
export function moneyContext(c?: ArchiveCurrency){
  if(!c)return '站点币种配置不可用，金额暂不显示；原始 Quota 和请求统计仍可查看。'
  const time=new Date(c.observed_at).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false})
  return c.type==='TOKENS'?`站点配置为额度显示，保留原始 Quota；配置读取于 ${time}（北京时间）。`:`按当前站点配置折算：${c.raw_quota_per_unit} quota = 1 USD，1 USD = ${c.exchange_rate} ${currencyUnit(c)}。配置读取于 ${time}（北京时间），不是历史汇率或正式账单快照。`
}
