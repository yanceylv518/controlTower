type RenderToken = object;

const queue = new Map<RenderToken, () => void>();
let frame: number | undefined;

function requestFlush() {
  if (frame !== undefined) return;
  frame = window.requestAnimationFrame(flushOne);
}

function flushOne() {
  frame = undefined;
  const next = queue.entries().next();
  if (next.done) return;
  const [token, render] = next.value;
  queue.delete(token);
  try {
    render();
  } finally {
    if (queue.size) requestFlush();
  }
}

export function scheduleChartRender(token: RenderToken, render: () => void) {
  queue.set(token, render);
  requestFlush();
}

export function cancelChartRender(token: RenderToken) {
  queue.delete(token);
}
