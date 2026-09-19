import type { ObjectDirective } from 'vue'
const observers = new WeakMap<HTMLElement, MutationObserver>()
function labelCells(root: HTMLElement) {
  const labels = new Map<string, string>()
  root.querySelectorAll<HTMLTableCellElement>('.el-table__header th').forEach(header => {
    if (header.closest('.el-table') !== root || header.colSpan > 1) return
    const column = [...header.classList].find(value => /^el-table_.*_column_/.test(value))
    if (column) labels.set(column, header.querySelector('.cell')?.textContent?.trim() || '选择')
  })
  root.querySelectorAll<HTMLTableCellElement>('.el-table__body td').forEach(cell => {
    if (cell.closest('.el-table') !== root) return
    const column = [...cell.classList].find(value => labels.has(value))
    if (column) cell.dataset.mobileLabel = labels.get(column)
  })
}
// Labels are presentation metadata. Card styles apply only on small admin screens.
export const mobileCards: ObjectDirective<HTMLElement> = {
  mounted(root) {
    // Element Plus refreshes its root class on resize/scroll. A data attribute
    // survives those internal renders, unlike a class added by this directive.
    root.dataset.mobileCards = ''; labelCells(root)
    let queued = false
    const observer = new MutationObserver(() => {
      if (queued) return
      queued = true
      queueMicrotask(() => { queued = false; if (root.isConnected) labelCells(root) })
    })
    observer.observe(root, { childList:true, subtree:true, characterData:true }); observers.set(root, observer)
  },
  unmounted(root) { observers.get(root)?.disconnect(); observers.delete(root) },
}
