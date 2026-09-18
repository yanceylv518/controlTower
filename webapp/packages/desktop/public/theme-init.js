// Applied before the first stylesheet/app render to avoid a flash of the wrong theme.
(() => {
  let preference = 'system';
  let palette = 'blue';
  let font = 'system';
  try {
    preference = localStorage.getItem('ct.theme') || 'system';
    const saved = localStorage.getItem('ct.theme.palette');
    if (['blue', 'graphite', 'jade', 'violet'].includes(saved)) palette = saved;
    const savedFont = localStorage.getItem('ct.theme.font');
    if (['system', 'yahei', 'dengxian', 'simsun'].includes(savedFont)) font = savedFont;
  } catch {}
  const dark = preference === 'dark' || (preference !== 'light' && preference !== 'dark' && matchMedia('(prefers-color-scheme: dark)').matches);
  const root = document.documentElement;
  root.dataset.theme = dark ? 'dark' : 'light';
  root.dataset.palette = palette;
  root.dataset.font = font;
  root.classList.toggle('dark', dark);
  root.style.colorScheme = dark ? 'dark' : 'light';
  const backgrounds = { blue: ['#f2f4f8', '#101722'], graphite: ['#f4f4f5', '#101010'], jade: ['#f1f5f4', '#101b18'], violet: ['#f5f3f8', '#17131f'] };
  root.style.backgroundColor = backgrounds[palette][dark ? 1 : 0];
})();
