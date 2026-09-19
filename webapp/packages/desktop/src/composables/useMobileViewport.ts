import { onBeforeUnmount, ref } from 'vue'
export function useMobileViewport() {
  const query = window.matchMedia('(max-width: 900px)')
  const mobile = ref(query.matches)
  const sync = () => { mobile.value = query.matches }
  query.addEventListener('change', sync)
  onBeforeUnmount(() => query.removeEventListener('change', sync))
  return mobile
}
