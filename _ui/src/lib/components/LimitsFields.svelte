<script lang="ts">
  import type { Limits } from '@/lib/api/types';
  import { t } from '@/lib/i18n/index.svelte';

  // Pool settings as four number fields. An empty field means "inherit":
  // the placeholder shows the value that applies then. Durations are edited
  // in minutes and stored in seconds.
  let {
    value = $bindable(),
    inherited,
    id,
  }: { value: Limits; inherited: Limits; id: string } = $props();

  const fields: { key: keyof Limits; unit: 'n' | 'min' }[] = [
    { key: 'max_open', unit: 'n' },
    { key: 'max_idle', unit: 'n' },
    { key: 'max_lifetime_sec', unit: 'min' },
    { key: 'max_idle_time_sec', unit: 'min' },
  ];

  function shown(key: keyof Limits, unit: 'n' | 'min'): number | '' {
    const v = value[key];
    if (!v) return '';
    return unit === 'min' ? Math.round((v / 60) * 100) / 100 : v;
  }

  function set(key: keyof Limits, unit: 'n' | 'min', raw: string) {
    const n = Number(raw);
    value = { ...value, [key]: raw === '' || !(n > 0) ? 0 : unit === 'min' ? Math.round(n * 60) : Math.round(n) };
  }

  function hint(key: keyof Limits, unit: 'n' | 'min'): string {
    const v = inherited[key];
    if (!v) return '';
    return unit === 'min' ? String(Math.round((v / 60) * 100) / 100) : String(v);
  }
</script>

<div class="grid grid-cols-4 gap-3">
  {#each fields as f (f.key)}
    <div>
      <label class="label" for="{id}-{f.key}">
        {t(`limits.${f.key}`)}
        {#if f.unit === 'min'}<span class="font-normal text-faint">{t('limits.minutes')}</span>{/if}
      </label>
      <input
        id="{id}-{f.key}"
        class="input tabular-nums"
        type="number"
        min="0"
        step={f.unit === 'min' ? 'any' : 1}
        placeholder={hint(f.key, f.unit)}
        value={shown(f.key, f.unit)}
        oninput={(e) => set(f.key, f.unit, (e.target as HTMLInputElement).value)}
        title={t(`limits.${f.key}Hint`)}
      />
    </div>
  {/each}
</div>
