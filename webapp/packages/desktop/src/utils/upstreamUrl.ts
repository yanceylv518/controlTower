// Preview only; the server validates URLs and resolves ownership again on save.
export function normalizeUpstreamUrl(raw?: string): string {
  const value = (raw || '').trim();
  if (!value || value.length > 2048 || /[\\\s]/.test(value)) return '';
  const parts = /^(https?):\/\/([^/?#]+)(\/[^?#]*)?$/i.exec(value);
  if (!parts) return '';
  try {
    const parsed = new URL(value);
    if (!parsed.hostname || parsed.username || parsed.password || parts[2].includes('@')) return '';
    return `${parsed.protocol}//${parsed.host.toLowerCase()}${(parts[3] || '').replace(/\/+$/, '')}`;
  } catch { return ''; }
}
