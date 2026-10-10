import type { BillingWorkspaceBill } from '@ct/shared';

export interface BillingWorkspaceRow extends BillingWorkspaceBill {
  key: string;
  row_kind: 'bill' | 'model' | 'tier';
  model_name?: string;
  tier_name?: string;
  children?: BillingWorkspaceRow[];
}

// Only explicit peak/off-peak labels qualify. Never infer from price or request time.
export function isPeakOffpeakTier(tier: string): boolean {
  return /^(高峰(?:时段|期)?|低谷(?:时段|期)?|空闲(?:时段|期)?|peak(?:[_ -]hours)?|off[_ -]?peak(?:[_ -]hours)?|valley)$/i.test(tier.trim());
}

// Display children are not included again in daily KPI totals.
export function billingWorkspaceRows(bills: BillingWorkspaceBill[], groupModels: boolean): BillingWorkspaceRow[] {
  return bills.map(bill => ({
    ...bill,
    key: JSON.stringify([bill.job.id]),
    row_kind: 'bill',
    children: groupModels && bill.models?.length ? bill.models.map(model => {
      const tiers=(bill.tiers||[]).filter(t=>t.model===model.model && isPeakOffpeakTier(t.tier));
      return {
        ...bill, ...model, models: undefined, tiers: undefined,
        key: JSON.stringify([bill.job.id, model.model]),
        row_kind: 'model',
        model_name: model.model,
        children: tiers.length ? tiers.map(tier=>({
          ...bill,...tier,models:undefined,tiers:undefined,
          key:JSON.stringify([bill.job.id,model.model,tier.tier,tier.discount]),
          row_kind:'tier',model_name:model.model,tier_name:tier.tier,
          empty_count:0,empty_amount:''
        })) : undefined,
      };
    }) : undefined,
  }));
}