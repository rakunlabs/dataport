export type ThemeMode = 'system' | 'light' | 'dark';

const key = 'dataport.theme';
const media = window.matchMedia('(prefers-color-scheme: dark)');

function stored(): ThemeMode {
  try {
    const v = localStorage.getItem(key);
    if (v === 'light' || v === 'dark') return v;
  } catch {
    // Storage may be disabled.
  }
  return 'system';
}

export const theme = $state({ mode: stored(), dark: false });

function apply() {
  theme.dark = theme.mode === 'dark' || (theme.mode === 'system' && media.matches);
  document.documentElement.classList.toggle('dark', theme.dark);
  // The browser chrome takes the panel colour of the active theme.
  const panel = getComputedStyle(document.documentElement).getPropertyValue('--color-panel').trim();
  for (const meta of document.querySelectorAll('meta[name=theme-color]')) meta.setAttribute('content', panel);
}

media.addEventListener('change', apply);
apply();

export function setTheme(mode: ThemeMode) {
  theme.mode = mode;
  try {
    if (mode === 'system') localStorage.removeItem(key);
    else localStorage.setItem(key, mode);
  } catch {
    // The choice then lasts for this page only.
  }
  apply();
}

const order: ThemeMode[] = ['system', 'light', 'dark'];

export function cycleTheme() {
  setTheme(order[(order.indexOf(theme.mode) + 1) % order.length]);
}
