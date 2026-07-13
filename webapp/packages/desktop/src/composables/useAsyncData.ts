import { ref, shallowRef } from 'vue'
export function useAsyncData<T>(loader: () => Promise<T>) { const data = shallowRef<T>(); const loading = ref(false); const error = ref(''); async function reload() { loading.value = true; error.value = ''; try { data.value = await loader() } catch { error.value = '数据加载失败，请稍后重试' } finally { loading.value = false } } return { data, loading, error, reload } }
