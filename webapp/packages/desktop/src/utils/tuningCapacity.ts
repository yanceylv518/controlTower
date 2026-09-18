export function compactCapacity(value?: number, unlimited = false): string {
  if (value == null || !Number.isFinite(value)) return '—';
  if (value === 0 && unlimited) return '不限';
  const unit = value >= 100000000 ? 100000000 : value >= 10000 ? 10000 : 1;
  return (value / unit).toLocaleString('zh-CN', { useGrouping: unit === 1, maximumFractionDigits: unit === 1 ? 0 : 2 }) + (unit === 100000000 ? '亿' : unit === 10000 ? '万' : '');
}
export function parseCapacity(text: string): number | null {
  const input = text.trim();
  if (input === '不限') return 0;
  const match = /^(\d+(?:\.\d+)?)\s*(万|亿)?$/.exec(input);
  if (!match) return null;
  const [whole, fraction = ''] = match[1].split('.');
  const denominator = 10n ** BigInt(fraction.length);
  const numerator = BigInt(whole + fraction) * (match[2] === '亿' ? 100000000n : match[2] === '万' ? 10000n : 1n);
  if (numerator % denominator !== 0n) return null;
  const value = numerator / denominator;
  return value <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(value) : null;
}

// Editor values use ten-thousands; transport values remain integer counts.
export function capacityInWan(value: number): string {
  if (!Number.isSafeInteger(value) || value < 0) return '';
  const count = BigInt(value);
  const fraction = String(count % 10000n).padStart(4, '0').replace(/0+$/, '');
  return String(count / 10000n) + (fraction ? '.' + fraction : '');
}
export function parseCapacityWan(text: string): number | null {
  const value = text.trim();
  if (!value) return 0;
  if (!/^\d+(?:\.\d{1,4})?$/.test(value)) return null;
  return parseCapacity(value + '万');
}
