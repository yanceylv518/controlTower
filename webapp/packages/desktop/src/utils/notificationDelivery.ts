import type { NotificationDeliveryItem } from '@ct/shared'
export function deliveryStatus(status: string) {
  return ({sent:'已送达',failed:'等待重试',exhausted:'投递失败',expired:'已结束',pending:'待投递'} as Record<string,string>)[status] || '未知状态'
}
export function deliveryTone(status: string): 'success'|'warning'|'danger'|'info' {
  return status==='sent'?'success':status==='failed'?'warning':status==='exhausted'?'danger':'info'
}
export function retryTime(item: NotificationDeliveryItem): string | undefined {
  if (!['failed','pending'].includes(item.status)) return undefined
  const date=new Date(item.next_attempt_at)
  return Number.isFinite(date.getTime()) && date.getUTCFullYear()>1970 && date.getUTCFullYear()<9999 ? item.next_attempt_at : undefined
}
