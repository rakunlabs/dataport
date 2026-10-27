<script lang="ts">
  // Which connections MCP agents may use, and how: drag a connection between
  // the two lists, then choose read, write or schema changes for it.
  import { t } from '@/lib/i18n/index.svelte';
  import { GripVertical, ArrowRight, ArrowLeft, Factory } from 'lucide-svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import DbIcon from '@/lib/components/ui/DbIcon.svelte';

  type Mode = 'off' | 'read' | 'write' | 'ddl';

  interface AccessConnection {
    id: string;
    name: string;
    db_type: string;
    color: string;
    read_only: boolean;
    production: boolean;
    mode: Mode;
  }

  interface Access {
    saved: boolean;
    default_mode: Mode;
    connections: AccessConnection[];
  }

  let saved = $state<Access | null>(null);
  let modes = $state<Record<string, Mode>>({});
  let defaultMode = $state<Mode>('off');
  let saving = $state(false);
  let dragging = $state('');
  let over = $state<'on' | 'off' | ''>('');

  function apply(v: Access) {
    saved = v;
    defaultMode = v.default_mode;
    modes = Object.fromEntries(v.connections.map((c) => [c.id, c.mode]));
  }

  async function load() {
    try {
      apply(await api.get<Access>('/mcp/access'));
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  void load();

  const enabled = $derived(saved?.connections.filter((c) => modes[c.id] !== 'off') ?? []);
  const disabled = $derived(saved?.connections.filter((c) => modes[c.id] === 'off') ?? []);
  const dirty = $derived(
    !!saved && (!saved.saved || defaultMode !== saved.default_mode || saved.connections.some((c) => modes[c.id] !== c.mode)),
  );

  function move(id: string, on: boolean) {
    const c = saved?.connections.find((x) => x.id === id);
    if (!c) return;
    if (!on) modes[id] = 'off';
    else if (modes[id] === 'off') modes[id] = 'read';
  }

  function drop(e: DragEvent, on: boolean) {
    e.preventDefault();
    const id = e.dataTransfer?.getData('text/plain') || dragging;
    if (id) move(id, on);
    dragging = '';
    over = '';
  }

  async function save() {
    saving = true;
    try {
      apply(await api.put<Access>('/mcp/access', { default_mode: defaultMode, connections: modes }));
      toast(t('mcp.access.saved'), 'success');
    } catch (e) {
      toast(errorMessage(e), 'error');
    } finally {
      saving = false;
    }
  }

  const levels: { mode: Exclude<Mode, 'off'>; key: string }[] = [
    { mode: 'read', key: 'mcp.access.read' },
    { mode: 'write', key: 'mcp.access.write' },
    { mode: 'ddl', key: 'mcp.access.ddl' },
  ];
</script>

{#snippet item(c: AccessConnection, on: boolean)}
  <li
    class="flex items-center gap-2 rounded-md border border-line bg-bg px-2 py-1.5 text-sm {dragging === c.id ? 'opacity-50' : ''}"
    draggable="true"
    ondragstart={(e) => {
      dragging = c.id;
      e.dataTransfer?.setData('text/plain', c.id);
    }}
    ondragend={() => {
      dragging = '';
      over = '';
    }}
  >
    <GripVertical size={13} class="shrink-0 cursor-grab text-faint" />
    <DbIcon type={c.db_type} />
    <span class="min-w-0 flex-1 truncate" title={c.name}>{c.name}</span>
    {#if c.production}<span title={t('mcp.access.production')}><Factory size={12} class="shrink-0 text-warn" /></span>{/if}
    {#if on}
      <div class="segmented shrink-0" role="group" aria-label={t('mcp.access.modeOf', { name: c.name })}>
        {#each levels as l (l.mode)}
          <button
            type="button"
            aria-pressed={modes[c.id] === l.mode}
            disabled={c.read_only && l.mode !== 'read'}
            title={c.read_only && l.mode !== 'read' ? t('mcp.access.readOnlyGrant') : undefined}
            onclick={() => (modes[c.id] = l.mode)}
          >
            {t(l.key)}
          </button>
        {/each}
      </div>
      <button type="button" class="icon-btn size-6" onclick={() => move(c.id, false)} aria-label={t('mcp.access.disable', { name: c.name })}><ArrowRight size={13} /></button>
    {:else}
      <button type="button" class="icon-btn size-6" onclick={() => move(c.id, true)} aria-label={t('mcp.access.enable', { name: c.name })}><ArrowLeft size={13} /></button>
    {/if}
  </li>
{/snippet}

<section>
  <span class="label">{t('mcp.access.title')}</span>
  <p class="mb-2 text-xs text-muted">{t('mcp.access.intro')}</p>
  {#if saved && !saved.saved}<p class="mb-2 rounded-md bg-warn-soft px-3 py-2 text-xs text-warn">{t('mcp.access.unsaved')}</p>{/if}

  <div class="grid grid-cols-[3fr_2fr] gap-3">
    {#each [true, false] as on (on)}
      {@const list = on ? enabled : disabled}
      <div
        class="flex min-h-32 flex-col rounded-md border p-2 {over === (on ? 'on' : 'off') ? 'border-accent bg-accent-soft' : 'border-line bg-sunken'}"
        role="list"
        aria-label={t(on ? 'mcp.access.on' : 'mcp.access.off')}
        ondragover={(e) => {
          e.preventDefault();
          over = on ? 'on' : 'off';
        }}
        ondragleave={() => (over = '')}
        ondrop={(e) => drop(e, on)}
      >
        <p class="mb-1.5 px-1 text-xs font-medium text-fg-2">{t(on ? 'mcp.access.on' : 'mcp.access.off')} <span class="text-faint tabular-nums">{list.length}</span></p>
        <ul class="space-y-1">
          {#each list as c (c.id)}{@render item(c, on)}{/each}
        </ul>
        {#if !list.length}<p class="m-auto px-2 py-4 text-center text-xs text-faint">{t('mcp.access.dropHere')}</p>{/if}
      </div>
    {/each}
  </div>

  <div class="mt-3 flex flex-wrap items-center gap-3">
    <label class="flex items-center gap-2 text-sm">
      {t('mcp.access.newConnections')}
      <select class="input h-7 w-auto text-xs" bind:value={defaultMode}>
        <option value="off">{t('mcp.access.newOff')}</option>
        <option value="read">{t('mcp.access.newRead')}</option>
      </select>
    </label>
    <span class="flex-1"></span>
    <button type="button" class="btn-primary" onclick={save} disabled={!dirty || saving}>{saving ? t('common.loading') : t('common.save')}</button>
  </div>
  <p class="hint mt-1">{t('mcp.access.hint')}</p>
</section>
