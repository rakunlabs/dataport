<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { TriangleAlert } from 'lucide-svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import type { ExplainOptions, ExplainSupport } from '@/lib/api/types';

  // The options of an execution plan, as DBeaver asks before explaining:
  // only the ones the database supports are shown.
  let {
    support,
    options,
    sql,
    readOnly = false,
    onexplain,
    onclose,
  }: {
    support: ExplainSupport;
    options: ExplainOptions;
    sql: string;
    readOnly?: boolean;
    onexplain: (o: ExplainOptions) => void;
    onclose: () => void;
  } = $props();

  // A format the database does not have falls back to its first.
  function initial(): ExplainOptions {
    const f = support.formats ?? ['text'];
    return { ...options, format: f.includes(options.format) ? options.format : (f[0] ?? 'text') };
  }

  let o = $state<ExplainOptions>(initial());

  const offered = $derived(new Set(support.options ?? []));
  const formats = $derived(support.formats ?? ['text']);

  // Options in the order of PostgreSQL's EXPLAIN; some need ANALYZE.
  const all: { key: keyof ExplainOptions; needsAnalyze?: boolean }[] = [
    { key: 'analyze' },
    { key: 'verbose' },
    { key: 'costs' },
    { key: 'buffers' },
    { key: 'timing', needsAnalyze: true },
    { key: 'settings' },
    { key: 'wal', needsAnalyze: true },
    { key: 'summary' },
  ];

  const shown = $derived(all.filter((x) => offered.has(x.key)));
  const writes = $derived(/^\s*(insert|update|delete|merge|upsert|replace)\b/i.test(sql.replace(/^\s*(--[^\n]*\n|\/\*[\s\S]*?\*\/)*/g, '')));

  function submit(e: SubmitEvent) {
    e.preventDefault();
    onexplain({ ...o });
  }
</script>

<Modal title={t('explain.title')} {onclose} width="max-w-lg">
  <form id="explain-form" class="space-y-4" onsubmit={submit}>
    <pre class="max-h-28 overflow-auto rounded-md border border-line bg-sunken px-3 py-2 font-mono text-xs whitespace-pre-wrap text-fg-2">{sql.trim()}</pre>

    {#if shown.length}
      <fieldset class="grid grid-cols-2 gap-x-4 gap-y-2.5">
        <legend class="label mb-2">{t('explain.options')}</legend>
        {#each shown as x (x.key)}
          {@const off = !!x.needsAnalyze && !o.analyze}
          <label class="flex items-start gap-2 text-sm {off ? 'opacity-50' : ''}" title={t(`explain.hints.${x.key}`)}>
            <input type="checkbox" class="mt-0.5" bind:checked={o[x.key] as boolean} disabled={off} />
            <span>
              <span class="font-mono text-xs font-semibold tracking-wide">{x.key.toUpperCase()}</span>
              <span class="block text-xs text-muted">{t(`explain.hints.${x.key}`)}</span>
            </span>
          </label>
        {/each}
      </fieldset>
    {:else}
      <p class="hint">{t('explain.noOptions')}</p>
    {/if}

    {#if formats.length > 1}
      <div>
        <span class="label">{t('explain.format')}</span>
        <div class="flex gap-2">
          {#each formats as f (f)}
            <label class="flex cursor-pointer items-center gap-2 rounded-md border px-3 py-1.5 text-sm {o.format === f ? 'border-accent bg-accent/5' : 'border-line hover:bg-hover'}">
              <input type="radio" name="explain-format" value={f} checked={o.format === f} onchange={() => (o.format = f)} />
              {f.toUpperCase()}
            </label>
          {/each}
        </div>
      </div>
    {/if}

    {#if o.analyze && support.analyze}
      <p class="flex items-start gap-2 rounded-md bg-warn-soft px-3 py-2 text-xs text-warn">
        <TriangleAlert size={14} class="mt-px shrink-0" />
        <span>{writes && readOnly ? t('explain.analyzeReadOnly') : writes ? t('explain.analyzeWrite') : t('explain.analyzeRuns')}</span>
      </p>
    {/if}
  </form>

  {#snippet footer()}
    <button class="btn" type="button" onclick={onclose}>{t('common.cancel')}</button>
    <button class="btn-primary" type="submit" form="explain-form">{t('explain.run')}</button>
  {/snippet}
</Modal>
