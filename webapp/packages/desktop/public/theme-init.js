// Applied before the first stylesheet/app render to avoid a flash of the wrong theme.
(() => {
  let preference = 'system';
  let palette = 'default';
  let font = 'system';
  try {
    preference = localStorage.getItem('ct.theme') || 'system';
    const saved = localStorage.getItem('ct.theme.palette');
    if (["default", "anthropic", "simple-large", "underground", "rose-garden", "lake-view", "sunset-glow", "forest-whisper", "ocean-breeze", "lavender-dream", "blue", "graphite", "jade", "violet"].includes(saved)) palette = saved;
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
  const backgrounds = {
"default": ["#ffffff", "#1e1e1e"],
"anthropic": ["#fafaf7", "#1b1918"],
"simple-large": ["#fcfcfc", "#191919"],
"underground": ["#ffffff", "#1e1e1e"],
"rose-garden": ["#ffffff", "#1e1e1e"],
"lake-view": ["#ffffff", "#1e1e1e"],
"sunset-glow": ["#ffffff", "#1e1e1e"],
"forest-whisper": ["#ffffff", "#1e1e1e"],
"ocean-breeze": ["#ffffff", "#1e1e1e"],
"lavender-dream": ["#ffffff", "#1e1e1e"],
 blue: ['#f3f5f8', '#14181f'], graphite: ['#f4f4f5', '#171717'], jade: ['#f3f6f4', '#151b19'], violet: ['#f6f4f8', '#19171d'] };
  root.style.backgroundColor = backgrounds[palette][dark ? 1 : 0];
})();
