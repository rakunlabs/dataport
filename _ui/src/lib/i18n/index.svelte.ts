import en from '@/lib/i18n/en';

// Every translation has every message: a missing one fails the type check
// instead of falling back to English unseen. t() still falls back, for a
// key looked up by a computed name.
// serverErrors is keyed by the server's messages, which change on their own.
type Deep<T> = { [P in keyof T]: T[P] extends string ? string : Deep<T[P]> };
export type Messages = Deep<Omit<typeof en, 'serverErrors'>> & { serverErrors: Record<string, string> };
export type Locale = 'en' | 'de' | 'es' | 'fr' | 'nl' | 'pl' | 'ru' | 'tr' | 'ja' | 'ko' | 'zh-CN' | 'zh-TW';

// Each language names itself, so it can be found by someone who does not
// read the current one.
export const locales: { value: Locale; label: string }[] = [
  { value: 'en', label: 'English' },
  { value: 'de', label: 'Deutsch' },
  { value: 'es', label: 'Español' },
  { value: 'fr', label: 'Français' },
  { value: 'nl', label: 'Nederlands' },
  { value: 'pl', label: 'Polski' },
  { value: 'ru', label: 'Русский' },
  { value: 'tr', label: 'Türkçe' },
  { value: 'ja', label: '日本語' },
  { value: 'ko', label: '한국어' },
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁體中文' },
];

const key = 'dataport.locale';
// Languages other than English load on demand.
const loaders: Record<Locale, () => Promise<{ default: Messages }>> = {
  en: async () => ({ default: en }),
  de: () => import('@/lib/i18n/de'),
  es: () => import('@/lib/i18n/es'),
  fr: () => import('@/lib/i18n/fr'),
  nl: () => import('@/lib/i18n/nl'),
  pl: () => import('@/lib/i18n/pl'),
  ru: () => import('@/lib/i18n/ru'),
  tr: () => import('@/lib/i18n/tr'),
  ja: () => import('@/lib/i18n/ja'),
  ko: () => import('@/lib/i18n/ko'),
  'zh-CN': () => import('@/lib/i18n/zh-CN'),
  'zh-TW': () => import('@/lib/i18n/zh-TW'),
};

const isLocale = (v: string | null): v is Locale => !!v && v in loaders;

function detect(): Locale {
  try {
    const stored = localStorage.getItem(key);
    if (isLocale(stored)) return stored;
  } catch {
    // Storage may be disabled.
  }

  for (const lang of navigator.languages ?? [navigator.language]) {
    const tag = lang.toLowerCase();
    // Chinese splits by script: Taiwan, Hong Kong and Macau write
    // Traditional, the rest Simplified.
    if (tag.startsWith('zh')) return /^zh-(tw|hk|mo|hant)/.test(tag) ? 'zh-TW' : 'zh-CN';
    const prefix = tag.split('-')[0];
    if (isLocale(prefix)) return prefix;
  }

  return 'en';
}

export const i18n = $state({ locale: 'en' as Locale, messages: en as Messages });

// The language asked for last wins, even when an earlier one loads later.
let wanted: Locale | undefined;

export async function setLocale(locale: Locale, persist = true) {
  wanted = locale;
  const mod = await loaders[locale]();
  if (wanted !== locale) return;
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

// The plural forms a language writes, in the order messages list them:
// English "one | other", Polish and Russian "one | few | many".
const categoryOrder = ['zero', 'one', 'two', 'few', 'many', 'other'];
const pluralCache = new Map<string, { rules: Intl.PluralRules; order: string[] }>();

function pluralIndex(locale: string, count: number): number {
  let p = pluralCache.get(locale);
  if (!p) {
    const rules = new Intl.PluralRules(locale);
    const order = [...rules.resolvedOptions().pluralCategories].sort((a, b) => categoryOrder.indexOf(a) - categoryOrder.indexOf(b));
    p = { rules, order };
    pluralCache.set(locale, p);
  }
  return Math.max(0, p.order.indexOf(p.rules.select(count)));
}

/**
 * Translates a dotted key. `{name}` placeholders are filled from params. A
 * message with forms separated by " | " picks one by params.count, in the
 * language's plural categories (one | other, or one | few | many); a
 * message with fewer forms uses its last one for the rest.
 */
export function t(path: string, params: Record<string, string | number> = {}): string {
  const own = lookup(i18n.messages, path);
  let msg = own ?? lookup(en, path) ?? path;

  if ('count' in params && msg.includes(' | ')) {
    const forms = msg.split(' | ');
    const i = pluralIndex(own !== undefined ? i18n.locale : 'en', Number(params.count));
    msg = forms[Math.min(i, forms.length - 1)];
  }

  // A numeric count shows in the language's digits; the plural above is
  // picked from the number itself, as "1.000" would read as one.
  if (typeof params.count === 'number') params = { ...params, count: fmtNumber(params.count) };

  return msg.replace(/\{(\w+)\}/g, (m, name) => (name in params ? String(params[name]) : m));
}

/**
 * Formats numbers and dates for the current locale. maxDecimals raises the
 * default of three, for statistics of small values.
 */
export function fmtNumber(n: number, maxDecimals?: number): string {
  return new Intl.NumberFormat(i18n.locale, maxDecimals === undefined ? undefined : { maximumFractionDigits: maxDecimals }).format(n);
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
