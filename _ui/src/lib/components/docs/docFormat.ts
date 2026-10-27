import { t, fmtNumber } from '@/lib/i18n/index.svelte';

export const th = '[&>th]:h-8 [&>th]:border-b [&>th]:border-line [&>th]:px-2 [&>th]:text-left [&>th]:font-medium [&>th]:whitespace-nowrap';
export const td = 'border-b border-line-soft [&>td]:h-8 [&>td]:px-2 [&>td]:align-middle hover:bg-hover/60';

export const kindLabel = (k: string) => t(`docs.kinds.${k}`);
export const rows = (n: number | undefined) => (n === undefined ? '—' : fmtNumber(n));
