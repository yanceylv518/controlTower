/** Format a recorded multiplier for display only; never use the label to calculate amounts. */
export function formatBillingDiscount(value?: string | number | null): string {
  const text = String(value ?? '').trim();
  if (!text) return '原价';
  if (text === 'mixed') return '多种折扣';
  const rate = Number(text);
  if (!Number.isFinite(rate)) return '—';
  if (rate === 1) return '原价';
  // Stored multipliers have six decimal places; bound display precision to hide binary tails.
  return `${Number((rate * 10).toFixed(6))} 折`;
}
