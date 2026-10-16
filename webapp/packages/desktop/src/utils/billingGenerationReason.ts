import type {BillingGenerationProgress} from '@ct/shared';

export function billingGenerationReason(item: BillingGenerationProgress): string {
  if (item.outcome === 'failed') return item.error || item.monthly?.error || item.days?.find(day => day.error)?.error || '生成失败，请展开查看日期和阶段。';
  if (['complete', 'no_consumption', 'cancelled'].includes(item.outcome)) return '';
  const b = item.blocker;
  if (!b) return item.outcome === 'awaiting_monthly' ? '日账已处理，等待调度月账汇总。' : '';
  const task = b.kind === 'report' ? '报表' : b.period === 'monthly' ? '月账汇总' : '账单';
  if (b.reason === 'source_timeout') return `本站点${task}源查询超时${b.attempts ? `，当前第 ${b.attempts} 轮尝试` : ''}；等待当前查询结束后继续调度。`;
  if (b.reason === 'retrying') return `本站点${task}遇到错误，正在重试；等待本轮处理结束。`;
  if (b.status === 'publishing') return `本站点正在${b.period === 'monthly' ? '汇总月账' : '写入账单文件'}。`;
  if (item.outcome === 'awaiting_monthly' && b.period !== 'monthly') return `日账已处理，等待本站点${task}到达调度检查点后汇总月账。`;
  return `本站点${task}${b.status === 'pending' ? '已入队，等待执行' : '正在执行'}。`;
}
