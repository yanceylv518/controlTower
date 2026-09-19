import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'

interface Page<T> { items: T[]; total?: number; has_more?: boolean }

/** Append only successful pages; a new base response invalidates all older requests. */
export function useAppendPages<T, P extends Page<T>>(
  source: () => P | undefined,
  blocked: () => boolean,
  fetchPage: (base: P, offset: number, signal: AbortSignal) => Promise<Page<T>>,
  key: (item: T) => string | number,
  baseOffset: (base: P) => number = () => 0,
) {
  const extra = shallowRef<T[]>([])
  const loading = ref(false)
  const error = ref('')
  const exhausted = ref(false)
  const nextOffset = ref(0)
  let generation = 0
  let controller: AbortController | undefined
  function cancel() { generation++; controller?.abort(); controller = undefined; loading.value = false }
  watch(source, base => {
    cancel(); extra.value = []; error.value = ''; exhausted.value = !base?.items.length
    nextOffset.value = base ? baseOffset(base) + base.items.length : 0
  }, { immediate: true, flush: 'sync' })
  watch(blocked, busy => { if (busy) cancel() }, { flush: 'sync' })
  onScopeDispose(cancel)
  const items = computed(() => {
    const seen = new Set<string | number>()
    return [...(source()?.items || []), ...extra.value].filter(item => {
      const id = key(item); if (seen.has(id)) return false; seen.add(id); return true
    })
  })
  const hasMore = computed(() => {
    const base = source()
    return Boolean(base && !exhausted.value && (base.has_more ?? (nextOffset.value < (base.total || 0))))
  })
  async function loadMore() {
    const base = source()
    if (!base || blocked() || loading.value || !hasMore.value) return
    const token = ++generation
    controller = new AbortController(); loading.value = true; error.value = ''
    try {
      const result = await fetchPage(base, nextOffset.value, controller.signal)
      if (token !== generation) return
      extra.value = [...extra.value, ...result.items]
      nextOffset.value += result.items.length
      exhausted.value = !result.items.length || result.has_more === false || (result.has_more == null && nextOffset.value >= (result.total ?? base.total ?? 0))
    } catch {
      if (token === generation) error.value = '加载失败，请重试'
    } finally {
      if (token === generation) { loading.value = false; controller = undefined }
    }
  }
  return { items, loading, error, hasMore, loadMore }
}
