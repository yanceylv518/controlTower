import { ref } from 'vue';
import { dashboard } from '../api';
import { useAsyncData } from './useAsyncData';

type Filters = Parameters<typeof dashboard.operationAudits>[0];
type Cursor = { before_time: string; before_id: string } | undefined;

export function useAuditHistory(filters: () => Filters, pageSize: () => number) {
  const page = ref(1);
  const total = ref<number>();
  const counting = ref(false);
  const countError = ref(false);
  const cursors: Cursor[] = [undefined];
  let countController: AbortController | undefined;
  let countVersion = 0;
  let countedAt = 0;
  let countKey = '';

  async function loadCount(force = false) {
    const params = filters();
    const key = JSON.stringify(params);
    if (!force && key === countKey && (counting.value || Date.now() - countedAt < 30_000)) return;
    countController?.abort();
    const version = ++countVersion;
    const controller = new AbortController();
    countController = controller;
    countKey = key;
    counting.value = true;
    countError.value = false;
    try {
      const response = await dashboard.operationAudits({ ...params, count_only: true }, controller.signal);
      if (version !== countVersion) return;
      total.value = response.total >= 0 ? response.total : undefined;
      countedAt = Date.now();
    } catch {
      if (version === countVersion) countError.value = true;
    } finally {
      if (version === countVersion) counting.value = false;
    }
  }

  const state = useAsyncData(async (signal) => {
    const response = await dashboard.operationAudits({
      ...filters(), list_only: true, limit: pageSize(), ...cursors[page.value - 1],
    }, signal);
    // Counts never block list rendering. Only the current list may start one.
    if (!signal?.aborted) void loadCount();
    return response;
  });

  function reset() {
    state.cancel();
    countController?.abort();
    ++countVersion;
    counting.value = false;
    countError.value = false;
    total.value = undefined;
    countedAt = 0;
    countKey = '';
    cursors.splice(0, cursors.length, undefined);
    page.value = 1;
    return state.reload();
  }

  function next() {
    if (state.loading.value || !state.data.value?.has_more) return;
    const last = state.data.value.items.at(-1);
    if (!last) return;
    // Keep the server's microseconds intact; Date would truncate them.
    cursors[page.value] = { before_time: last.created_at, before_id: last.id };
    page.value++;
    return state.reload();
  }

  function previous() {
    if (state.loading.value || page.value <= 1) return;
    page.value--;
    return state.reload();
  }

  function cancel() {
    state.cancel();
    countController?.abort();
    ++countVersion;
  }

  return { state, page, total, counting, countError, loadCount, reset, next, previous, cancel };
}
