// Invoke directly from a user click so HTTP fallback retains user activation.
export async function copyText(text: string): Promise<boolean> {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try { await navigator.clipboard.writeText(text); return true } catch { /* try selection copy */ }
  }
  const previous = document.activeElement as HTMLElement | null
  const selection = window.getSelection()
  const ranges: Range[] = []
  if (selection) for (let i = 0; i < selection.rangeCount; i++) ranges.push(selection.getRangeAt(i).cloneRange())
  const field = document.createElement('textarea')
  field.value = text
  field.readOnly = true
  field.style.cssText = 'position:fixed;left:-9999px;top:0;font-size:16px;'
  try {
    document.body.appendChild(field)
    field.focus({ preventScroll: true })
    field.select()
    return document.execCommand('copy')
  } catch { return false } finally {
    field.remove()
    previous?.focus({ preventScroll: true })
    if (selection) {
      selection.removeAllRanges()
      for (const range of ranges) selection.addRange(range)
    }
  }
}
