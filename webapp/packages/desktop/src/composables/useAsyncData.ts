import { ref, shallowRef } from 'vue'
import { billingReadErrorMessage } from '../utils/httpError'

/**
 * 管理一组异步数据的加载状态，并保证同一数据源始终以最后一次请求为准。
 *
 * 后台刷新保留当前数据，不切换阻塞式加载层，适合筛选重置等需要快速反馈的操作；
 * 旧请求即使晚于新请求返回，也不能覆盖用户刚刚得到的结果，调用 cancel 可主动终止正在执行的请求。
 */
export function useAsyncData<T>(loader: (signal?: AbortSignal) => Promise<T>) {
  const data = shallowRef<T>()
  const loading = ref(false)
  const error = ref('')
  const lastRefreshError = shallowRef<unknown>()
  let requestSequence = 0
  let activeController: AbortController | undefined

  // 新请求或调用方销毁组件时中止旧 fetch，让服务端也能尽快释放对应的查询资源。
  function cancel() {
    requestSequence += 1
    activeController?.abort()
    activeController = undefined
    loading.value = false
  }

  async function reload(silent = false) {
    const requestId = ++requestSequence
    const background = silent && data.value !== undefined
    activeController?.abort()
    const controller = new AbortController()
    activeController = controller

    if (background) {
      // 后台刷新不再显示阻塞式加载层；若它取代了旧的前台请求，也要清除旧状态。
      loading.value = false
    } else {
      loading.value = true
      error.value = ''
    }

    try {
      const nextData = await loader(controller.signal)
      if (requestId !== requestSequence) return
      data.value = nextData
      lastRefreshError.value = undefined
    } catch (cause) {
      if (requestId !== requestSequence) return
      lastRefreshError.value = cause
      if (!background) error.value = billingReadErrorMessage(cause)
    } finally {
      if (requestId === requestSequence) {
        activeController = undefined
        if (!background) loading.value = false
      }
    }
  }

  const refresh = () => reload(true)
  return { data, loading, error, lastRefreshError, reload, refresh, cancel }
}
