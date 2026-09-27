export type StatRow = { date:string; dimensions:Record<string,unknown>; amounts:Record<string,unknown> }
export const metrics=['requests','quota','prompt_tokens','completion_tokens','cache_tokens'] as const
export type Totals=Record<typeof metrics[number] | `${typeof metrics[number]}_missing`,bigint>
function integer(value:unknown):bigint {
 if(!/^\d+$/.test(String(value)))throw new Error('归档汇总包含无效数值')
 return BigInt(String(value))
}
export function totals(rows:StatRow[]):Totals {
 const result={} as Totals
 for(const key of metrics){result[key]=0n;result[`${key}_missing`]=0n}
 for(const row of rows){if(String(row.dimensions.type)!=='2')continue
  for(const key of metrics){const raw=row.amounts[key];if(raw!==undefined&&raw!==null){result[key]+=integer(raw)}
   const missing=row.amounts[key+'_missing'];result[`${key}_missing`]+=missing==null?(raw==null?integer(row.amounts.log_rows??'1'):0n):integer(missing)
  }
 }return result
}
export function usd(quota:bigint){const value=quota*2n;return `${value/1000000n}.${String(value%1000000n).padStart(6,'0')}`}
export function groupStats(rows:StatRow[],dimension:string){const groups=new Map<string,StatRow[]>()
 for(const row of rows){if(String(row.dimensions.type)!=='2')continue;const key=String(row.dimensions[dimension]??(dimension==='channel_id'?row.dimensions.channel:undefined)??'未知');const items=groups.get(key);if(items)items.push(row);else groups.set(key,[row])}
 return [...groups].map(([name,items])=>({name,...totals(items)})).sort((a,b)=>a.quota>b.quota?-1:a.quota<b.quota?1:0)
}
export function csvCell(value:unknown){const text=String(value??'');return '"'+(/^[=+@\-\t\r]/.test(text)?"'"+text:text).replaceAll('"','""')+'"'}
