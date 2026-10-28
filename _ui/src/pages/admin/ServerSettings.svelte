<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { RotateCcw, TriangleAlert } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import NotebookAccess from '@/pages/admin/NotebookAccess.svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { loadSession } from '@/lib/stores/session.svelte';
  import { loadConnections } from '@/lib/stores/workspace.svelte';

  // Query, pool, notebook, connection and MCP settings. Each starts from its
  // built-in default; saved values win until reset.
  interface Setting {
    key: string;
    section: 'query' | 'pool' | 'notebook' | 'connections' | 'mcp';
    kind: 'int' | 'duration' | 'bool';
    unit?: 's' | 'm' | 'h' | 'd' | 'MiB';
    min: number;
    max: number;
    restart?: boolean;
    value: number | boolean;
    config: number | boolean;
    saved: boolean;
    running?: number | boolean;
  }

  const sections = ['query', 'pool', 'notebook', 'connections', 'mcp'] as const;
  const factor: Record<string, number> = { s: 1, m: 60, h: 3600, d: 86400, MiB: 1 << 20 };

  let list = $state<Setting[]>([]);
  // Form values in display units; a key missing here is unchanged.
  let draft = $state<Record<string, number | boolean>>({});
  // Keys to go back to the default on save.
  let reset = $state<Record<string, boolean>>({});
  let error = $state('');
  let saving = $state(false);

  const toUnit = (s: Setting, v: number | boolean) => (typeof v === 'number' && s.unit ? v / factor[s.unit] : v);
  const fromUnit = (s: Setting, v: number | boolean) => (typeof v === 'number' && s.unit ? Math.round(v * factor[s.unit]) : v);

  function apply(v: Setting[]) {
    list = v;
    draft = Object.fromEntries(v.map((s) => [s.key, toUnit(s, s.value)]));
    reset = {};
  }

  async function load() {
    try {
      apply(await api.get<Setting[]>('/admin/settings'));
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  void load();

  const changes = $derived.by(() => {
    const out: Record<string, number | boolean | null> = {};
    for (const s of list) {
      if (reset[s.key]) {
        if (s.saved) out[s.key] = null;
        continue;
      }
      const v = fromUnit(s, draft[s.key]);
      if (v !== s.value) out[s.key] = v;
    }
    return out;
  });
  const dirty = $derived(Object.keys(changes).length > 0);
  const pendingRestart = $derived(list.some((s) => s.restart && s.running !== undefined && s.running !== s.value));

  function backToConfig(s: Setting) {
    reset[s.key] = true;
    draft[s.key] = toUnit(s, s.config);
  }

  function edited(s: Setting) {
    reset[s.key] = false;
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    error = '';
    try {
      const personal = Object.keys(changes).some((k) => k.startsWith('connections.'));
      apply(await api.put<Setting[]>('/admin/settings', changes));
      toast(t('admin.server.saved'), 'success');
      // Personal connections appear or go with the setting.
      if (personal) void Promise.all([loadSession(), loadConnections()]);
    } catch (e) {
      error = errorMessage(e);
    } finally {
      saving = false;
    }
  }

  const fmtConfig = (s: Setting) => {
    const v = toUnit(s, s.config);
    if (typeof v === 'boolean') return v ? t('admin.server.on') : t('admin.server.off');
    return s.unit ? `${v} ${t(`admin.server.units.${s.unit}`)}` : String(v);
  };
  const label = (s: Setting) => t(`admin.server.fields.${s.key.replace('.', '_')}`);
  const hint = (s: Setting) => {
    const k = `admin.server.hints.${s.key.replace('.', '_')}`;
    const h = t(k);
    return h === k ? '' : h;
  };
</script>

<Page title={t('admin.server.title')} description={t('admin.server.description')}>
  <form class="max-w-3xl space-y-6 px-6 py-5" onsubmit={save}>
    {#if pendingRestart}
      <p class="flex items-start gap-1.5 rounded-md border border-warn/40 bg-warn-soft p-2 text-xs text-fg-2"><TriangleAlert size={13} class="mt-0.5 shrink-0 text-warn" /> {t('admin.server.restartPending')}</p>
    {/if}

    {#each sections as section (section)}
      {@const items = list.filter((s) => s.section === section)}
      {#if items.length}
        <section>
          <h2 class="mb-2 text-sm font-semibold">{t(`admin.server.sections.${section}`)}</h2>
          <div class="divide-y divide-line-soft rounded-md border border-line">
            {#each items as s (s.key)}
              {@const h = hint(s)}
              <div class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-0.5 px-3 py-2">
                <div class="min-w-0">
                  <label class="text-sm" for="set-{s.key}">{label(s)}</label>
                  {#if s.restart}<span class="chip ml-1.5">{t('admin.server.restart')}</span>{/if}
                  {#if h}<p class="hint mt-0.5">{h}</p>{/if}
                  <p class="mt-0.5 font-mono text-2xs text-faint">
                    {t('admin.server.fromDefault', { value: fmtConfig(s) })}
                    {#if s.saved && !reset[s.key]}<span class="text-accent"> · {t('admin.server.overridden')}</span>{/if}
                  </p>
                </div>
                <div class="flex items-center gap-1.5">
                  {#if s.kind === 'bool'}
                    <input id="set-{s.key}" type="checkbox" bind:checked={draft[s.key] as boolean} onchange={() => edited(s)} disabled={saving} />
                  {:else}
                    <input
                      id="set-{s.key}"
                      class="input w-28 text-right tabular-nums"
                      type="number"
                      required
                      min={toUnit(s, s.min) as number}
                      max={toUnit(s, s.max) as number}
                      step={s.unit === 'MiB' || s.kind === 'int' || s.unit === 's' ? 1 : 'any'}
                      bind:value={draft[s.key] as number}
                      oninput={() => edited(s)}
                      disabled={saving}
                    />
                    <span class="w-12 text-xs text-muted">{s.unit ? t(`admin.server.units.${s.unit}`) : ''}</span>
                  {/if}
                  <button
                    type="button"
                    class="icon-btn size-6"
                    title={t('admin.server.reset')}
                    aria-label={t('admin.server.reset')}
                    disabled={saving || (!s.saved && !reset[s.key] && draft[s.key] === toUnit(s, s.config)) || !!reset[s.key]}
                    onclick={() => backToConfig(s)}
                  >
                    <RotateCcw size={13} />
                  </button>
                </div>
              </div>
            {/each}
          </div>
        </section>
      {/if}
    {/each}

    <p class="hint">{t('admin.server.applies')}</p>
    {#if error}<p role="alert" class="text-xs text-danger">{error}</p>{/if}
    <button type="submit" class="btn-primary" disabled={!dirty || saving}>{saving ? t('common.loading') : t('common.save')}</button>
  </form>

  <NotebookAccess />
</Page>
