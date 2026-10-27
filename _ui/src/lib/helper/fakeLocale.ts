// Test data in another language. The server makes the rows, but its faker
// knows English only; faker.js knows many languages, so the browser makes a
// pool of values per generator and the server picks from those.
import type { Faker } from '@faker-js/faker';

// faker.js locales, each named as it names itself and loaded on demand.
const locales: Record<string, [string, () => Promise<{ faker: Faker }>]> = {
  af_ZA: ['Afrikaans (Suid-Afrika)', () => import('@faker-js/faker/locale/af_ZA')],
  ar: ['العربية', () => import('@faker-js/faker/locale/ar')],
  az: ['Azərbaycan dili', () => import('@faker-js/faker/locale/az')],
  bn_BD: ['বাংলা (বাংলাদেশ)', () => import('@faker-js/faker/locale/bn_BD')],
  cs_CZ: ['Čeština', () => import('@faker-js/faker/locale/cs_CZ')],
  cy: ['Cymraeg', () => import('@faker-js/faker/locale/cy')],
  da: ['Dansk', () => import('@faker-js/faker/locale/da')],
  de: ['Deutsch', () => import('@faker-js/faker/locale/de')],
  de_AT: ['Deutsch (Österreich)', () => import('@faker-js/faker/locale/de_AT')],
  de_CH: ['Deutsch (Schweiz)', () => import('@faker-js/faker/locale/de_CH')],
  el: ['Ελληνικά', () => import('@faker-js/faker/locale/el')],
  en_AU: ['English (Australia)', () => import('@faker-js/faker/locale/en_AU')],
  en_CA: ['English (Canada)', () => import('@faker-js/faker/locale/en_CA')],
  en_GB: ['English (Great Britain)', () => import('@faker-js/faker/locale/en_GB')],
  en_IE: ['English (Ireland)', () => import('@faker-js/faker/locale/en_IE')],
  en_IN: ['English (India)', () => import('@faker-js/faker/locale/en_IN')],
  en_NG: ['English (Nigeria)', () => import('@faker-js/faker/locale/en_NG')],
  en_US: ['English (United States)', () => import('@faker-js/faker/locale/en_US')],
  en_ZA: ['English (South Africa)', () => import('@faker-js/faker/locale/en_ZA')],
  es: ['Español', () => import('@faker-js/faker/locale/es')],
  es_MX: ['Español (México)', () => import('@faker-js/faker/locale/es_MX')],
  fa: ['فارسی', () => import('@faker-js/faker/locale/fa')],
  fi: ['Suomi', () => import('@faker-js/faker/locale/fi')],
  fr: ['Français', () => import('@faker-js/faker/locale/fr')],
  fr_BE: ['Français (Belgique)', () => import('@faker-js/faker/locale/fr_BE')],
  fr_CA: ['Français (Canada)', () => import('@faker-js/faker/locale/fr_CA')],
  fr_CH: ['Français (Suisse)', () => import('@faker-js/faker/locale/fr_CH')],
  he: ['עברית', () => import('@faker-js/faker/locale/he')],
  hr: ['Hrvatski', () => import('@faker-js/faker/locale/hr')],
  hu: ['Magyar', () => import('@faker-js/faker/locale/hu')],
  hy: ['Հայերեն', () => import('@faker-js/faker/locale/hy')],
  id_ID: ['Bahasa Indonesia', () => import('@faker-js/faker/locale/id_ID')],
  it: ['Italiano', () => import('@faker-js/faker/locale/it')],
  ja: ['日本語', () => import('@faker-js/faker/locale/ja')],
  ka_GE: ['ქართული', () => import('@faker-js/faker/locale/ka_GE')],
  ko: ['한국어', () => import('@faker-js/faker/locale/ko')],
  lv: ['Latviešu valoda', () => import('@faker-js/faker/locale/lv')],
  mk: ['Македонски јазик', () => import('@faker-js/faker/locale/mk')],
  nb_NO: ['Norsk bokmål', () => import('@faker-js/faker/locale/nb_NO')],
  ne: ['नेपाली', () => import('@faker-js/faker/locale/ne')],
  nl: ['Nederlands', () => import('@faker-js/faker/locale/nl')],
  nl_BE: ['Nederlands (België)', () => import('@faker-js/faker/locale/nl_BE')],
  pl: ['Polski', () => import('@faker-js/faker/locale/pl')],
  pt_BR: ['Português (Brasil)', () => import('@faker-js/faker/locale/pt_BR')],
  pt_PT: ['Português (Portugal)', () => import('@faker-js/faker/locale/pt_PT')],
  ro: ['Română', () => import('@faker-js/faker/locale/ro')],
  ru: ['Русский', () => import('@faker-js/faker/locale/ru')],
  sk: ['Slovenčina', () => import('@faker-js/faker/locale/sk')],
  sl_SI: ['Slovenščina', () => import('@faker-js/faker/locale/sl_SI')],
  sr_RS_latin: ['Srpski (latinica)', () => import('@faker-js/faker/locale/sr_RS_latin')],
  sv: ['Svenska', () => import('@faker-js/faker/locale/sv')],
  th: ['ไทย', () => import('@faker-js/faker/locale/th')],
  tr: ['Türkçe', () => import('@faker-js/faker/locale/tr')],
  uk: ['Українська', () => import('@faker-js/faker/locale/uk')],
  ur: ['اردو', () => import('@faker-js/faker/locale/ur')],
  uz_UZ_latin: ["O'zbekcha", () => import('@faker-js/faker/locale/uz_UZ_latin')],
  vi: ['Tiếng Việt', () => import('@faker-js/faker/locale/vi')],
  zh_CN: ['中文 (中国)', () => import('@faker-js/faker/locale/zh_CN')],
  zh_TW: ['中文 (臺灣)', () => import('@faker-js/faker/locale/zh_TW')],
  zu_ZA: ['isiZulu', () => import('@faker-js/faker/locale/zu_ZA')],
};

export const fakeLocales = Object.entries(locales).map(([value, [label]]) => ({ value, label }));

async function load(locale: string): Promise<Faker> {
  const entry = locales[locale];
  if (!entry) throw new Error(`unknown locale ${locale}`);
  return (await entry[1]()).faker;
}

// Names without a title such as "Frau" or "Bayan", in the locale's order.
function fullName(f: Faker): string {
  const patterns = (f.rawDefinitions.person?.name ?? []) as (string | { value: string })[];
  const plain = patterns.map((p) => (typeof p === 'string' ? p : p.value)).filter((p) => !/prefix|suffix/.test(p));
  return plain.length ? f.helpers.fake(plain) : `${f.person.firstName()} ${f.person.lastName()}`;
}

const makers: Record<string, (f: Faker) => string> = {
  first_name: (f) => f.person.firstName(),
  last_name: (f) => f.person.lastName(),
  full_name: fullName,
  gender: (f) => f.person.sex(),
  phone: (f) => f.phone.number({ style: 'international' }),
  company: (f) => f.company.name(),
  job_title: (f) => f.person.jobTitle(),
  street: (f) => f.location.streetAddress(),
  city: (f) => f.location.city(),
  state: (f) => f.location.state(),
  country: (f) => f.location.country(),
  postcode: (f) => f.location.zipCode(),
  address: (f) => `${f.location.streetAddress()}, ${f.location.zipCode()} ${f.location.city()}`,
  word: (f) => f.lorem.word(),
  sentence: (f) => f.lorem.sentence(),
  paragraph: (f) => f.lorem.paragraph(),
};

const poolSize = 1000;

// fakePools makes values for the generators asked, the same for the same
// seed. A generator the locale has no data for is left out, so the server
// falls back to English for it.
export async function fakePools(locale: string, seed: number, names: Iterable<string>): Promise<Record<string, string[]>> {
  const f = await load(locale);
  const out: Record<string, string[]> = {};
  for (const name of new Set(names)) {
    const make = makers[name];
    if (!make) continue;
    f.seed(seed);
    try {
      out[name] = Array.from({ length: poolSize }, () => make(f));
    } catch {
      // The locale lacks this kind of data.
    }
  }
  return out;
}

export const localizable = new Set(Object.keys(makers));
