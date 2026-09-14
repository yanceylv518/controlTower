interface ChartBounds { left: number; top: number; width: number; height: number }

// Axis tooltips reuse HTML while moving vertically within the same time bucket.
// Update the existing rows directly so model focus never waits for a new bucket.
export function highlightCustomerTooltip(root: HTMLElement | undefined, key?: string) {
  if (!root) return;
  const rows = Array.from(root.querySelectorAll<HTMLElement>("[data-traffic-key]"));
  const hasActive = key !== undefined && rows.some(row => row.dataset.trafficKey === key);
  rows.forEach(row => {
    const active = hasActive && row.dataset.trafficKey === key;
    row.style.fontWeight = active ? "700" : "400";
    row.style.opacity = hasActive && !active ? "0.45" : "1";
    row.style.backgroundColor = active ? "rgba(65,112,205,.10)" : "transparent";
    row.style.borderLeftColor = active ? row.dataset.trafficColor || "#4170cd" : "transparent";
    row.dataset.active = String(active);
    if (active) {
      const top = row.offsetTop, bottom = top + row.offsetHeight;
      if (top < root.scrollTop) root.scrollTop = top;
      else if (bottom > root.scrollTop + root.clientHeight) root.scrollTop = bottom - root.clientHeight;
    }
  });
}

// Return chart-local coordinates (ECharts converts these to the body portal).
// Prefer stable positions outside the active chart, not a box chasing the cursor.
export function customerTooltipPosition(bounds: ChartBounds, pointer: number[], size: number[], viewport: number[]) {
  const gap = 12, edge = 8;
  const [width, height] = size, [vw, vh] = viewport;
  const clampX = (x: number) => Math.max(edge, Math.min(x, vw - width - edge));
  const clampY = (y: number) => Math.max(edge, Math.min(y, vh - height - edge));
  const right = bounds.left + bounds.width, bottom = bounds.top + bounds.height;
  let x: number, y: number;
  if (right + gap + width <= vw - edge) {
    x = right + gap; y = clampY(bounds.top);
  } else if (bounds.left - gap - width >= edge) {
    x = bounds.left - gap - width; y = clampY(bounds.top);
  } else if (bounds.top - gap - height >= edge) {
    x = clampX(bounds.left + (bounds.width - width) / 2); y = bounds.top - gap - height;
  } else if (bottom + gap + height <= vh - edge) {
    x = clampX(bounds.left + (bounds.width - width) / 2); y = bottom + gap;
  } else {
    // A large expanded chart may leave no external space. Use the opposite
    // corner and let pointer events pass through so it cannot trap inspection.
    x = clampX(pointer[0] < bounds.width / 2 ? right - width : bounds.left);
    y = clampY(pointer[1] < bounds.height / 2 ? bottom - height : bounds.top);
  }
  const outside = x >= right || x + width <= bounds.left || y >= bottom || y + height <= bounds.top;
  return { position: [x - bounds.left, y - bounds.top], outside };
}
