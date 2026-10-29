<script lang="ts">
  import { t, fmtDateTime } from '@/lib/i18n/index.svelte';
  import { RefreshCw, Server, Unplug, ShieldCheck, ShieldAlert, CircleCheck, CircleAlert, LoaderCircle } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import DbIcon from '@/lib/components/ui/DbIcon.svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  interface Pool {
    key: string;
    connection_id: string;
    user_id?: string;
    db_type: string;
    open: number;
    in_use: number;
    idle: number;
    max_open: number;
    running: number;
    txs: number;
    last_used: number;
  }

  interface Node {
    id: string;
    name: string;
    addr: string;
    self: boolean;
    version: string;
    commit: string;
    started_at: number;
    pools: Pool[] | null;
    owned: string[] | null;
  }

  interface Overview {
    enabled: boolean;
    health: { addr: string; replicas: number; peers: number; quorum_size: number; has_quorum: boolean; secure: boolean };
    nodes: Node[];
    unreachable: string[];
    connections: Record<string, { name: string; db_type: string; color: string }>;
    users: Record<string, string>;
  }

  let view = $state<Overview | null>(null);
  let loading = $state(false);
  let releasing = $state('');

  async function load() {
    loading = true;
    try {
      view = await api.get<Overview>('/admin/cluster');
    } catch (e) {
      toast(errorMessage(e), 'error');
    } finally {
      loading = false;
    }
  }

  void load();

  async function release(key: string) {
    releasing = key;
    try {
      const res = await api.post<{ released: boolean }>('/admin/cluster/release', { key });
      toast(res.released ? t('admin.cluster.released') : t('admin.cluster.notOpen'), res.released ? 'success' : 'info');
      await load();
    } catch (e) {
      toast(errorMessage(e), 'error');
    } finally {
      releasing = '';
    }
  }

  const seen = $derived(view ? view.nodes.length + view.unreachable.length : 0);
  const expected = $derived(view?.health.replicas || seen);
  const versions = $derived(view ? new Set(view.nodes.map((n) => n.version + n.commit)).size : 0);

  function uptime(ms: number): string {
    const s = Math.max(0, Math.floor((Date.now() - ms) / 1000));
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    const m = Math.floor((s % 3600) / 60);
    if (d) return `${d}d ${h}h`;
    if (h) return `${h}h ${m}m`;
    return `${m}m`;
  }

  function connName(p: Pool): string {
    return view?.connections[p.connection_id]?.name ?? p.connection_id;
  }
</script>

<Page title={t('admin.cluster.title')} description={t('admin.cluster.description')}>
  {#snippet actions()}
    <button class="btn-outline" onclick={load} disabled={loading}><RefreshCw size={14} class={loading ? 'animate-spin' : ''} /> {t('admin.cluster.refresh')}</button>
  {/snippet}

  {#if !view}
    <p class="flex items-center gap-2 px-6 py-5 text-sm text-muted"><LoaderCircle size={14} class="animate-spin" /> {t('common.loading')}</p>
  {:else}
    <div class="space-y-6 px-6 py-5">
      {#if !view.enabled}
        <div class="max-w-3xl rounded-md border border-line bg-panel px-4 py-3 text-sm">
          <p class="font-medium">{t('admin.cluster.standalone')}</p>
          <p class="mt-1 text-fg-2">{t('admin.cluster.standaloneHint')}</p>
          <pre class="mt-2 overflow-x-auto rounded bg-sunken px-3 py-2 font-mono text-xs leading-5">DATAPORT_CLUSTER_ENABLED=true
DATAPORT_CLUSTER_DNS_ADDR=dataport-headless.default.svc.cluster.local
DATAPORT_CLUSTER_REPLICAS=3
DATAPORT_CLUSTER_SECURITY_KEY=…</pre>
        </div>
      {:else}
        <div class="grid max-w-4xl grid-cols-2 gap-3 md:grid-cols-4">
          <div class="rounded-md border border-line bg-panel px-3 py-2.5">
            <p class="text-xs text-muted">{t('admin.cluster.replicas')}</p>
            <p class="mt-0.5 text-lg font-semibold tabular-nums">{view.nodes.length}<span class="ml-1 text-sm font-normal text-muted">/ {expected}</span></p>
          </div>
          <div class="rounded-md border border-line bg-panel px-3 py-2.5">
            <p class="text-xs text-muted">{t('admin.cluster.quorum')}</p>
            <p class="mt-0.5 flex items-center gap-1.5 text-sm font-medium {view.health.has_quorum ? 'text-ok' : 'text-danger'}">
              {#if view.health.has_quorum}<CircleCheck size={15} /> {t('admin.cluster.quorumOk')}{:else}<CircleAlert size={15} /> {t('admin.cluster.quorumLost')}{/if}
            </p>
            {#if view.health.replicas}<p class="text-2xs text-muted">{t('admin.cluster.quorumNeeds', { n: view.health.quorum_size + 1 })}</p>{/if}
          </div>
          <div class="rounded-md border border-line bg-panel px-3 py-2.5">
            <p class="text-xs text-muted">{t('admin.cluster.security')}</p>
            <p class="mt-0.5 flex items-center gap-1.5 text-sm font-medium {view.health.secure ? 'text-ok' : 'text-warn'}">
              {#if view.health.secure}<ShieldCheck size={15} /> {t('admin.cluster.psk')}{:else}<ShieldAlert size={15} /> {t('admin.cluster.noPsk')}{/if}
            </p>
          </div>
          <div class="rounded-md border border-line bg-panel px-3 py-2.5">
            <p class="text-xs text-muted">{t('admin.cluster.versions')}</p>
            <p class="mt-0.5 text-sm font-medium {versions > 1 ? 'text-warn' : ''}">{versions > 1 ? t('admin.cluster.mixed') : view.nodes[0]?.version}</p>
          </div>
        </div>
        {#if view.unreachable.length}
          <p role="alert" class="max-w-4xl rounded-md bg-warn-soft px-3 py-2 text-xs text-warn">{t('admin.cluster.unreachable', { list: view.unreachable.join(', ') })}</p>
        {/if}
      {/if}

      {#each view.nodes as node (node.id || node.addr)}
        {@const pools = node.pools ?? []}
        <section class="overflow-hidden rounded-md border border-line">
          <header class="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-line bg-panel px-3 py-2">
            <span class="flex items-center gap-2 text-sm font-semibold"><Server size={14} class="text-muted" /> {node.name || node.addr}</span>
            {#if node.self}<span class="chip bg-accent-soft text-accent">{t('admin.cluster.thisReplica')}</span>{/if}
            {#if node.addr}<span class="font-mono text-xs text-fg-2">{node.addr}</span>{/if}
            <span class="text-xs text-muted">{node.version}{#if node.commit && node.commit !== '-' && !node.version.includes(node.commit)} · <span class="font-mono">{node.commit}</span>{/if}</span>
            {#if node.started_at}<span class="text-xs text-muted" title={fmtDateTime(node.started_at)}>{t('admin.cluster.up', { time: uptime(node.started_at) })}</span>{/if}
            <span class="flex-1"></span>
            <span class="text-xs text-muted">{t('admin.cluster.poolCount', { n: pools.length })}</span>
          </header>
          {#if pools.length}
            <table class="table">
              <thead>
                <tr>
                  <th>{t('admin.cluster.connection')}</th>
                  <th class="w-32">{t('admin.cluster.sessions')}</th>
                  <th class="w-24">{t('admin.cluster.running')}</th>
                  <th class="w-28">{t('admin.cluster.txs')}</th>
                  <th class="w-40">{t('admin.cluster.lastUsed')}</th>
                  <th class="w-24"></th>
                </tr>
              </thead>
              <tbody>
                {#each pools as p (p.key)}
                  <tr>
                    <td>
                      <div class="flex items-center gap-2">
                        <DbIcon type={p.db_type} size={14} />
                        <span class="font-medium">{connName(p)}</span>
                        {#if p.user_id}<span class="chip">{view.users[p.user_id] ?? p.user_id}</span>{/if}
                        {#if node.owned?.includes(p.key)}<span class="pill-own">{t('admin.cluster.owner')}</span>{/if}
                      </div>
                    </td>
                    <td class="tabular-nums text-fg-2">{p.in_use} / {p.open} <span class="text-muted">({t('admin.cluster.max', { n: p.max_open })})</span></td>
                    <td class="tabular-nums {p.running ? 'font-medium text-accent' : 'text-muted'}">{p.running}</td>
                    <td class="tabular-nums {p.txs ? 'font-medium text-warn' : 'text-muted'}">{p.txs}</td>
                    <td class="text-xs text-fg-2">{fmtDateTime(p.last_used)}</td>
                    <td class="text-right">
                      <button class="btn hover:text-danger" disabled={releasing === p.key} onclick={() => release(p.key)} title={t('admin.cluster.releaseHint')}>
                        <Unplug size={13} /> {t('admin.cluster.release')}
                      </button>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {:else}
            <p class="px-3 py-4 text-center text-xs text-muted">{t('admin.cluster.noPools')}</p>
          {/if}
        </section>
      {/each}
    </div>
  {/if}
</Page>
