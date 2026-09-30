import type { BillingWorkspaceBill } from '@ct/shared';

export interface BillingWorkspaceRow extends BillingWorkspaceBill {
  key: string;
  row_kind: 'bill' | 'model';
  model_name?: string;
  children?: BillingWorkspaceRow[];
}

// Keep bill totals separate from display children so KPI totals never count models twice.
export function billingWorkspaceRows(bills: BillingWorkspaceBill[], groupModels: boolean): BillingWorkspaceRow[] {
  return bills.map(bill => ({
    ...bill,
    key: JSON.stringify([bill.job.id]),
    row_kind: 'bill',
    children: groupModels && bill.models?.length ? bill.models.map(model => ({
      ...bill,
      ...model,
      models: undefined,
      key: JSON.stringify([bill.job.id, model.model]),
      row_kind: 'model',
      model_name: model.model,
    })) : undefined,
  }));
}
