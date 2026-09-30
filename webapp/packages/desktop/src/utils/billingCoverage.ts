import type {BillingMonthlyCoverage} from '@ct/shared';

export function coverageRanges(ranges:{from:string;to:string}[]):string {
  return ranges.map(r=>r.from===r.to?r.from:`${r.from} 至 ${r.to}`).join('、');
}
export function coverageLabel(coverage?:BillingMonthlyCoverage):string {
  return coverage?.ranges.length?coverageRanges(coverage.ranges):'覆盖日期未确认';
}
export function coverageStatus(coverage?:BillingMonthlyCoverage):string {
  if(!coverage)return '尚未确认完整月份';
  const status=coverage.complete?'完整月账单':`部分月账单 · 已覆盖 ${coverage.covered_days}/${coverage.total_days} 天`;
  return status+(coverage.empty_days?` · 含 ${coverage.empty_days} 天无消费`:'');
}
export function coverageTooltip(coverage?:BillingMonthlyCoverage):string {
  if(!coverage)return '未保存覆盖信息，不能确认包含完整月份';
  return coverage.missing.length?`未覆盖日期：${coverageRanges(coverage.missing)}。未覆盖不代表无消费。`:coverageStatus(coverage);
}
