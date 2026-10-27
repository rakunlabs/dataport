<script lang="ts">
  import { untrack } from 'svelte';
  import { t, fmtNumber, serverMessage } from '@/lib/i18n/index.svelte';
  import { Sparkles, Info, LoaderCircle, TriangleAlert, RefreshCw, Dices, KeyRound, Link } from 'lucide-svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import { api } from '@/lib/api/client';
  import { connection, refreshSchema } from '@/lib/stores/workspace.svelte';
  import { closeToolDialog } from '@/lib/stores/dialogs.svelte';
  import { confirmProduction } from '@/lib/stores/confirm.svelte';
  import { askCredentials, withCredentials } from '@/lib/stores/credentials.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import type { GenColumn, GeneratePlan, GenerateProgress } from '@/lib/api/types';
  import { fakeLocales, fakePools, localizable } from '@/lib/helper/fakeLocale';

  let props: { connectionId: string; schema: string; tables: string[] } = $props();

  // The dialog is made anew for each request, so its first values are all
  // it needs from the props.
  const { connectionId, schema, tables } = untrack(() => ({ ...props }));
  const table = tables[0] ?? '';

  const conn = $derived(connection(connectionId));

  let rows = $state(100);
  let seed = $state<number | null>(null);
  let truncate = $state(false);

  // Language of names, places and text; empty is the server's English.
  const localeKey = 'dataport.generate.locale';
  let locale = $state(localStorage.getItem(localeKey) ?? '');
  $effect(() => {
    if (locale) localStorage.setItem(localeKey, locale);
    else localStorage.removeItem(localeKey);
  });
  let columns = $state<GenColumn[]>([]);
  let generators = $state<string[]>([]);
  let sample = $state<unknown[][]>([]);
  // Names of the columns in the sample, which leaves out skipped ones.
  let sampleCols = $state<string[]>([]);

  let loading = $state(false);
  let previewError = $state('');
  let running = $state(false);
  let done = $state(0);
  let error = $state('');
  let abort: AbortController | null = null;

  const groups: { key: string; names: string[] }[] = [
    { key: 'special', names: ['skip', 'null', 'constant', 'list', 'template', 'fk', 'sequence'] },
    { key: 'person', names: ['first_name', 'last_name', 'full_name', 'username', 'email', 'password', 'phone', 'gender', 'company', 'job_title'] },
    { key: 'place', names: ['street', 'city', 'state', 'country', 'country_code', 'postcode', 'address', 'latitude', 'longitude'] },
    { key: 'internet', names: ['url', 'domain', 'ipv4', 'ipv6', 'mac', 'user_agent', 'slug', 'color', 'currency_code', 'language', 'credit_card'] },
    { key: 'text', names: ['code', 'word', 'words', 'sentence', 'paragraph'] },
    { key: 'values', names: ['uuid', 'int', 'decimal', 'bool', 'date', 'timestamp', 'time', 'json', 'bytes'] },
  ];

  const genLabel = (g: string) => {
    const s = t(`generate.gen.${g}`);
    return s === `generate.gen.${g}` ? g : s;
  };

  // Generators the server knows that no group lists still show up.
  const grouped = $derived.by(() => {
    const known = new Set(generators);
    const out = groups.map((g) => ({ key: g.key, names: g.names.filter((n) => !generators.length || known.has(n)) }));
    const listed = new Set(groups.flatMap((g) => g.names));
    const rest = generators.filter((g) => !listed.has(g));
    if (rest.length) out.push({ key: 'values', names: rest });
    return out;
  });

  // What the server gets: the columns as the user left them, without what
  // only describes the column.
  function payload(): GenColumn[] {
    return columns.map((c) => {
      const out: GenColumn = { name: c.name, generator: c.generator };
      if (c.null_percent) out.null_percent = c.null_percent;
      switch (c.generator) {
        case 'int':
        case 'decimal':
        case 'words':
        case 'bytes':
          if (c.min !== undefined && c.min !== null && !Number.isNaN(c.min)) out.min = c.min;
          if (c.max !== undefined && c.max !== null && !Number.isNaN(c.max)) out.max = c.max;
          if (c.generator === 'decimal' && c.decimals !== undefined && c.decimals !== null) out.decimals = c.decimals;
          break;
        case 'sequence':
          if (c.min !== undefined && c.min !== null && !Number.isNaN(c.min)) out.min = c.min;
          break;
        case 'date':
        case 'timestamp':
          if (c.from) out.from = c.from;
          if (c.to) out.to = c.to;
          break;
        case 'constant':
        case 'list':
          out.values = c.values ?? [];
          break;
        case 'template':
          out.template = c.template ?? '';
          break;
        case 'fk':
          if (c.ref) out.ref = c.ref;
          break;
      }
      return out;
    });
  }

  // Values in the chosen language, made here for the generators in use; a
  // template may call any of them, and before the first preview the
  // generators are not known yet.
  async function pools(): Promise<Record<string, string[]> | undefined> {
    if (!locale) return undefined;
    // The pools and the server's picks both follow the seed.
    if (!seed) seed = Math.floor(Math.random() * 1_000_000_000) + 1;
    const all = !columns.length || columns.some((c) => c.generator === 'template');
    const used = columns.map((c) => (c.generator === 'words' ? 'word' : c.generator));
    return fakePools(locale, seed, all ? localizable : used.filter((g) => localizable.has(g)));
  }

  async function body(extra: Record<string, unknown> = {}) {
    const p = await pools();
    return { schema, table, rows, seed: seed || 0, truncate, columns: columns.length ? payload() : [], ...(p ? { pools: p } : {}), ...extra };
  }

  // What a new preview depends on besides the seed.
  const settings = () => JSON.stringify([payload(), locale]);

  let previewSeq = 0;
  // The column settings of the last preview, so that the echo of the
  // server does not start another one.
  let lastSent = '';

  async function preview() {
    const seq = ++previewSeq;
    loading = true;
    previewError = '';
    try {
      const req = await body({ rows: 10 });
      if (seq !== previewSeq) return;
      const plan = await withCredentials(connectionId, () => api.post<GeneratePlan>(`/connections/${connectionId}/generate/preview`, req));
      if (seq !== previewSeq) return;
      columns = plan.columns.map((c) => ({ ...c, null_percent: c.null_percent ?? 0 }));
      generators = plan.generators;
      sample = plan.rows;
      sampleCols = plan.columns.filter((c) => c.generator !== 'skip').map((c) => c.name);
      seed = plan.seed;
      lastSent = settings();
    } catch (e) {
      if (seq === previewSeq) {
        previewError = errorMessage(e);
        lastSent = settings();
      }
    } finally {
      if (seq === previewSeq) loading = false;
    }
  }

  $effect(() => {
    untrack(() => void preview());
  });

  // Changed settings show in the preview after a short pause.
  $effect(() => {
    const now = settings();
    if (!columns.length || now === untrack(() => lastSent) || running) return;
    const timer = setTimeout(() => void preview(), 500);
    return () => clearTimeout(timer);
  });

  function reroll() {
    seed = null;
    void preview();
  }

  function setGenerator(c: GenColumn, g: string) {
    c.generator = g;
    if (g === 'template' && !c.template) c.template = '{{ fake "word" }}';
    if ((g === 'list' || g === 'constant') && !c.values) c.values = [];
  }

  async function start(confirm = false) {
    running = true;
    error = '';
    done = 0;
    abort = new AbortController();
    try {
      const res = await fetch(`./api/v1/connections/${connectionId}/generate`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(await body({ confirm })),
        signal: abort.signal,
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        running = false;
        if (res.status === 409 && data.confirmation_required) {
          const what = t('generate.what', { rows: fmtNumber(rows), table });
          if (await confirmProduction({ connection: conn?.name ?? '', sql: what, risk: 'write' })) return start(true);
          return;
        }
        if (res.status === 428 && data.credentials_required) {
          if (await askCredentials(connectionId, serverMessage(data.message))) return start(confirm);
        }
        error = serverMessage(data.message || res.statusText);
        return;
      }

      const reader = res.body!.pipeThrough(new TextDecoderStream()).getReader();
      let buf = '';
      let final: GenerateProgress | null = null;
      for (;;) {
        const { value, done: end } = await reader.read();
        if (end) break;
        buf += value;
        let nl: number;
        while ((nl = buf.indexOf('\n')) >= 0) {
          const line = buf.slice(0, nl).trim();
          buf = buf.slice(nl + 1);
          if (!line) continue;
          const p = JSON.parse(line) as GenerateProgress;
          done = p.rows ?? done;
          if (p.finished) final = p;
        }
      }
      if (final?.error) {
        error = serverMessage(final.error);
      } else if (final) {
        toast(t('generate.done', { n: fmtNumber(final.rows), table }), 'success');
        closeToolDialog();
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') error = errorMessage(e);
    } finally {
      running = false;
      abort = null;
      void refreshSchema(connectionId, schema);
    }
  }

  function stop() {
    abort?.abort();
  }

  const show = (v: unknown) => (v === null || v === undefined ? 'NULL' : typeof v === 'object' ? JSON.stringify(v) : String(v));
  const numeric = (g: string) => g === 'int' || g === 'decimal' || g === 'words' || g === 'bytes';
  const valid = $derived(rows >= 1 && rows <= 1_000_000 && columns.some((c) => c.generator !== 'skip'));
</script>

<Modal title={t('generate.title', { table })} onclose={() => !running && closeToolDialog()} width="max-w-5xl">
  <div class="space-y-4">
    <div class="flex flex-wrap items-end gap-3">
      <div class="w-32">
        <label class="label" for="gen-rows">{t('generate.rows')}</label>
        <input id="gen-rows" class="input tabular-nums" type="number" min="1" max="1000000" bind:value={rows} />
      </div>
      <div class="w-36">
        <label class="label" for="gen-seed" title={t('generate.seedHint')}>{t('generate.seed')}</label>
        <input id="gen-seed" class="input font-mono tabular-nums" type="number" bind:value={seed} title={t('generate.seedHint')} onchange={preview} />
      </div>
      <div class="w-56">
        <label class="label" for="gen-locale" title={t('generate.languageHint')}>{t('generate.language')}</label>
        <select id="gen-locale" class="input" bind:value={locale} disabled={running} title={t('generate.languageHint')}>
          <option value="">{t('generate.languageDefault')}</option>
          {#each fakeLocales as l (l.value)}<option value={l.value}>{l.label}</option>{/each}
        </select>
      </div>
      <button class="btn-outline" onclick={reroll} disabled={loading || running}><Dices size={14} /> {t('generate.reroll')}</button>
      <label class="flex h-7 items-center gap-2 text-sm {truncate ? 'text-danger' : ''}"><input type="checkbox" bind:checked={truncate} /> {t('generate.truncate')}</label>
    </div>

    <div class="overflow-hidden rounded-md border border-line">
      <div class="grid grid-cols-[minmax(10rem,1.1fr)_minmax(11rem,1fr)_minmax(14rem,2fr)_4.5rem] gap-2 border-b border-line bg-panel px-3 py-1.5 text-xs font-medium text-fg-2">
        <span>{t('generate.column')}</span>
        <span>{t('generate.generator')}</span>
        <span>{t('generate.options')}</span>
        <span class="text-right">{t('generate.nulls')}</span>
      </div>
      <div class="max-h-[42vh] overflow-auto">
        {#if !columns.length && loading}
          <p class="flex items-center gap-2 px-3 py-3 text-xs text-muted"><LoaderCircle size={13} class="animate-spin" /> {t('common.loading')}</p>
        {/if}
        {#each columns as c (c.name)}
          {@const fixed = c.auto === 'generated'}
          <div class="grid grid-cols-[minmax(10rem,1.1fr)_minmax(11rem,1fr)_minmax(14rem,2fr)_4.5rem] items-start gap-2 border-b border-line-soft px-3 py-1.5 last:border-0 {c.generator === 'skip' ? 'opacity-60' : ''}">
            <div class="min-w-0 pt-1">
              <div class="flex items-center gap-1 truncate font-mono text-xs" title={c.name}>
                {#if c.generator === 'fk'}<Link size={11} class="shrink-0 text-accent" />{/if}
                {c.name}
              </div>
              <div class="truncate text-[11px] text-faint" title={c.type}>
                {c.type}{#if !c.nullable} · NOT NULL{/if}
                {#if c.auto === 'identity'}<span class="ml-1 inline-flex items-center gap-0.5"><KeyRound size={10} /> {t('generate.identity')}</span>{/if}
                {#if c.auto === 'generated'}<span class="ml-1">· {t('generate.computed')}</span>{/if}
              </div>
            </div>

            <select class="input h-7 text-xs" value={c.generator} disabled={fixed || running} onchange={(e) => setGenerator(c, e.currentTarget.value)}>
              {#each grouped as g (g.key)}
                <optgroup label={t(`generate.groups.${g.key}`)}>
                  {#each g.names as n (n)}
                    <option value={n} disabled={n === 'null' && !c.nullable}>{genLabel(n)}</option>
                  {/each}
                </optgroup>
              {/each}
            </select>

            <div class="min-w-0">
              {#if numeric(c.generator)}
                <div class="flex items-center gap-1.5 text-xs text-muted">
                  <input class="input h-7 w-24 text-xs tabular-nums" type="number" placeholder={t('generate.min')} bind:value={c.min} />
                  <span>–</span>
                  <input class="input h-7 w-24 text-xs tabular-nums" type="number" placeholder={t('generate.max')} bind:value={c.max} />
                  {#if c.generator === 'decimal'}
                    <input class="input h-7 w-16 text-xs tabular-nums" type="number" min="0" max="10" placeholder={t('generate.decimals')} title={t('generate.decimals')} bind:value={c.decimals} />
                  {/if}
                </div>
              {:else if c.generator === 'sequence'}
                <input class="input h-7 w-28 text-xs tabular-nums" type="number" placeholder={t('generate.startAt')} bind:value={c.min} />
              {:else if c.generator === 'date' || c.generator === 'timestamp'}
                <div class="flex items-center gap-1.5 text-xs text-muted">
                  <input class="input h-7 w-36 text-xs" type="date" title={t('generate.from')} bind:value={c.from} />
                  <span>–</span>
                  <input class="input h-7 w-36 text-xs" type="date" title={t('generate.to')} bind:value={c.to} />
                </div>
              {:else if c.generator === 'constant'}
                <input class="input h-7 font-mono text-xs" placeholder={t('generate.value')} value={c.values?.[0] ?? ''} oninput={(e) => (c.values = [e.currentTarget.value])} />
              {:else if c.generator === 'list'}
                <textarea
                  class="input h-14 resize-y py-1 font-mono text-xs"
                  placeholder={t('generate.values')}
                  value={(c.values ?? []).join('\n')}
                  oninput={(e) => (c.values = e.currentTarget.value.split('\n').filter((v) => v !== ''))}
                ></textarea>
              {:else if c.generator === 'template'}
                <input class="input h-7 font-mono text-xs" placeholder={t('generate.template')} title={t('generate.templateHint')} bind:value={c.template} />
              {:else if c.generator === 'fk'}
                <div class="flex items-center gap-1 pt-1 text-xs text-muted">
                  {t('generate.ref')}:
                  <span class="truncate font-mono text-fg-2">{c.ref ? `${c.ref.table}.${c.ref.column}` : '—'}</span>
                </div>
              {/if}
            </div>

            <input
              class="input h-7 text-right text-xs tabular-nums"
              type="number"
              min="0"
              max="100"
              disabled={!c.nullable || c.generator === 'skip' || c.generator === 'null' || running}
              bind:value={c.null_percent}
            />
          </div>
        {/each}
      </div>
    </div>

    {#if columns.some((c) => c.generator === 'template')}
      <p class="flex items-start gap-1.5 text-xs text-muted"><Info size={13} class="mt-0.5 shrink-0" /> {t('generate.templateHint')}</p>
    {/if}

    <div>
      <div class="mb-1 flex items-center gap-2">
        <span class="label mb-0">{t('generate.preview')}</span>
        <button class="icon-btn size-6" onclick={preview} disabled={loading || running} title={t('generate.refresh')} aria-label={t('generate.refresh')}>
          <RefreshCw size={12} class={loading ? 'animate-spin' : ''} />
        </button>
      </div>
      {#if previewError}
        <p class="flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger-soft p-2 text-xs text-danger"><TriangleAlert size={13} class="mt-0.5 shrink-0" /> {previewError}</p>
      {:else if sample.length}
        <div class="max-h-56 overflow-auto rounded-md border border-line {loading ? 'opacity-60' : ''}">
          <table class="w-full text-xs">
            <thead class="sticky top-0 bg-panel">
              <tr>{#each sampleCols as name (name)}<th class="px-2 py-1 text-left font-medium whitespace-nowrap">{name}</th>{/each}</tr>
            </thead>
            <tbody>
              {#each sample as r, n (n)}
                <tr class="border-t border-line-soft">
                  {#each r as v, i (i)}<td class="max-w-56 truncate px-2 py-1 font-mono {v === null ? 'text-faint' : ''}" title={show(v)}>{show(v)}</td>{/each}
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>

    <p class="flex items-start gap-1.5 text-xs text-muted"><Info size={13} class="mt-0.5 shrink-0" /> {t('generate.hint')}</p>

    {#if error}
      <p class="flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger-soft p-2 text-xs text-danger"><TriangleAlert size={13} class="mt-0.5 shrink-0" /> {error}</p>
    {/if}
  </div>

  {#snippet footer()}
    {#if running}
      <span class="mr-auto flex items-center gap-2 text-xs text-muted tabular-nums">
        <LoaderCircle size={13} class="animate-spin" />
        {t('generate.running', { n: fmtNumber(done), total: fmtNumber(rows) })}
      </span>
      <button class="btn-outline" onclick={stop}>{t('generate.stop')}</button>
    {:else}
      <button class="btn-outline" onclick={closeToolDialog}>{t('common.cancel')}</button>
      <button class="btn-primary" disabled={!valid || loading || !!previewError || conn?.read_only} onclick={() => start()}>
        <Sparkles size={14} />
        {t('generate.start')}
      </button>
    {/if}
  {/snippet}
</Modal>
