import en from '@/lib/i18n/en';

export type Messages = typeof en;
export type Locale = 'en' | 'tr';

export const locales: { value: Locale; label: string }[] = [
  { value: 'en', label: 'English' },
  { value: 'tr', label: 'Türkçe' },
];

const key = 'dataport.locale';
const loaders: Record<Locale, () => Promise<{ default: Messages }>> = {
  en: async () => ({ default: en }),
  tr: () => import('@/lib/i18n/tr'),
};

function detect(): Locale {
  try {
    const stored = localStorage.getItem(key);
    if (stored === 'en' || stored === 'tr') return stored;
  } catch {
    // Storage may be disabled.
  }

  for (const lang of navigator.languages ?? [navigator.language]) {
    const prefix = lang.toLowerCase().split('-')[0];
    if (prefix === 'tr' || prefix === 'en') return prefix;
  }

  return 'en';
}

export const i18n = $state({ locale: 'en' as Locale, messages: en as Messages });

export async function setLocale(locale: Locale, persist = true) {
  const mod = await loaders[locale]();
  i18n.messages = mod.default;
  i18n.locale = locale;
  document.documentElement.lang = locale;
  if (persist) {
    try {
      localStorage.setItem(key, locale);
    } catch {
      // The choice then lasts for this page only.
    }
  }
}

export const ready = setLocale(detect(), false);

function lookup(messages: unknown, path: string): string | undefined {
  let node = messages as Record<string, unknown> | undefined;
  for (const part of path.split('.')) {
    if (node === undefined || node === null || typeof node !== 'object') return undefined;
    node = node[part] as Record<string, unknown>;
  }
  return typeof node === 'string' ? node : undefined;
}

/**
 * Translates a dotted key. `{name}` placeholders are filled from params; a
 * "one | many" message picks the form from params.count.
 */
export function t(path: string, params: Record<string, string | number> = {}): string {
  let msg = lookup(i18n.messages, path) ?? lookup(en, path) ?? path;

  if (msg.includes(' | ')) {
    const forms = msg.split(' | ');
    const count = Number(params.count ?? 0);
    msg = forms[count === 1 ? 0 : Math.min(1, forms.length - 1)];
  }

  return msg.replace(/\{(\w+)\}/g, (m, name) => (name in params ? String(params[name]) : m));
}

/** Formats numbers and dates for the current locale. */
export function fmtNumber(n: number): string {
  return new Intl.NumberFormat(i18n.locale).format(n);
}

export function fmtDateTime(ts: number): string {
  if (!ts) return '—';
  const d = new Date(ts);
  const sameDay = d.toDateString() === new Date().toDateString();
  return sameDay
    ? d.toLocaleTimeString(i18n.locale)
    : d.toLocaleString(i18n.locale, { dateStyle: 'medium', timeStyle: 'short' });
}

/**
 * Translates a message from the server. Known messages are mapped by exact
 * text, as are the prefixes the engine adds ("this connection is read-only: ...").
 */
export function serverMessage(msg: string): string {
  const table = (i18n.messages as Messages).serverErrors as Record<string, string>;
  if (table[msg]) return table[msg];
  const i = msg.indexOf(': ');
  if (i > 0 && table[msg.slice(0, i)]) return `${table[msg.slice(0, i)]}: ${msg.slice(i + 2)}`;
  return msg;
}
