<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { FilePlus2, Lock, ChevronsRight } from 'lucide-svelte';
  import TopBar from '@/lib/components/TopBar.svelte';
  import Sidebar from '@/lib/components/Sidebar.svelte';
  import TabBar from '@/lib/components/TabBar.svelte';
  import QueryTab from '@/lib/components/QueryTab.svelte';
  import TableTab from '@/lib/components/TableTab.svelte';
  import RightPanel from '@/lib/components/RightPanel.svelte';
  import Settings from '@/pages/Settings.svelte';
  import DiagramTab from '@/lib/components/DiagramTab.svelte';
  import DocsTab from '@/lib/components/DocsTab.svelte';
  import ProcessesTab from '@/lib/components/ProcessesTab.svelte';
  import UsersTab from '@/lib/components/UsersTab.svelte';
  import DbIcon from '@/lib/components/DbIcon.svelte';
  import { session } from '@/lib/stores/session.svelte';
  import { activeTab, openQueryTab, startWorkspace, ws } from '@/lib/stores/workspace.svelte';

  let sidebarWidth = $state(Number(localStorage.getItem('dataport.sidebar')) || 264);
  // A collapsed sidebar leaves a thin strip with the button that opens it.
  let collapsed = $state(localStorage.getItem('dataport.sidebar.collapsed') === '1');

  function setCollapsed(v: boolean) {
    collapsed = v;
    try {
      localStorage.setItem('dataport.sidebar.collapsed', v ? '1' : '0');
    } catch {
      // The choice then lasts for this page only.
    }
  }

  $effect(() => {
    if (session.user) void startWorkspace(session.user.id);
  });

  const tab = $derived(activeTab());

  function resize(e: PointerEvent) {
    e.preventDefault();
    const x0 = e.clientX;
    const w0 = sidebarWidth;
    const move = (ev: PointerEvent) => (sidebarWidth = Math.min(560, Math.max(200, w0 + ev.clientX - x0)));
    const up = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
      localStorage.setItem('dataport.sidebar', String(sidebarWidth));
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }
</script>

<div class="flex h-full flex-col">
  <TopBar />

  <div class="flex min-h-0 flex-1">
    {#if collapsed}
      <div class="flex w-9 shrink-0 flex-col items-center border-r border-line bg-panel pt-1.5">
        <button class="icon-btn" onclick={() => setCollapsed(false)} title={t('sidebar.expand')} aria-label={t('sidebar.expand')} aria-expanded="false">
          <ChevronsRight size={15} />
        </button>
      </div>
    {:else}
      <div class="shrink-0 border-r border-line" style="width:{sidebarWidth}px">
        <Sidebar oncollapse={() => setCollapsed(true)} />
      </div>
      <div class="relative z-10 -ml-[3px] w-[5px] shrink-0 cursor-col-resize transition-colors hover:bg-accent/50" onpointerdown={resize} role="separator" aria-orientation="vertical"></div>
    {/if}

    <main class="flex min-w-0 flex-1 flex-col bg-bg">
      {#if ws.tabs.length}
        <TabBar />
      {/if}

      <div class="min-h-0 flex-1">
        {#if tab}
          {#key tab.id}
            {#if tab.kind === 'query'}
              <QueryTab {tab} />
            {:else if tab.kind === 'settings'}
              <Settings />
            {:else if tab.kind === 'diagram'}
              <DiagramTab {tab} />
            {:else if tab.kind === 'docs'}
              <DocsTab {tab} />
            {:else if tab.kind === 'processes'}
              <ProcessesTab {tab} />
            {:else if tab.kind === 'users'}
              <UsersTab {tab} />
            {:else}
              <TableTab {tab} />
            {/if}
          {/key}
        {:else}
          <div class="flex h-full items-center justify-center bg-bg px-6">
            <div class="w-full max-w-[460px]">
              <h2 class="text-base font-semibold">
                {session.user?.display_name ? t('welcome.titleName', { name: session.user.display_name }) : t('welcome.title')}
              </h2>
              <p class="mt-1 text-sm text-muted">{t('welcome.body')}</p>

              {#if ws.connections.length}
                <div class="mt-5 overflow-hidden rounded-lg border border-line">
                  {#each ws.connections as c (c.id)}
                    <button
                      class="flex h-9 w-full items-center gap-2.5 border-b border-line-soft px-3 text-left text-sm last:border-0 hover:bg-hover"
                      onclick={() => openQueryTab(c.id)}
                    >
                      <DbIcon type={c.db_type} />
                      <span class="min-w-0 flex-1 truncate">{c.name}</span>
                      {#if c.production}<span class="pill-prod">{t('conn.prodShort')}</span>{/if}
                      {#if c.read_only}<span class="pill-ro"><Lock size={10} /> {t('conn.readOnlyShort')}</span>{/if}
                      <span class="flex items-center gap-1 text-xs text-muted"><FilePlus2 size={13} /> {t('app.newQuery')}</span>
                    </button>
                  {/each}
                </div>
              {/if}

              <dl class="mt-5 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-xs text-muted">
                <dt><span class="kbd">Ctrl</span> <span class="kbd">Enter</span></dt>
                <dd>{t('welcome.keyRun')}</dd>
                <dt><span class="kbd">Ctrl</span> <span class="kbd">\</span></dt>
                <dd>{t('welcome.keyRunNewResult')}</dd>
                <dt><span class="kbd">Ctrl</span> <span class="kbd">Shift</span> <span class="kbd">Enter</span></dt>
                <dd>{t('welcome.keyRunCurrent')}</dd>
                <dt><span class="kbd">Ctrl</span> <span class="kbd">S</span></dt>
                <dd>{t('welcome.keySave')}</dd>
                <dt><span class="kbd">Enter</span> / <span class="kbd">F2</span></dt>
                <dd>{t('welcome.keyEdit')}</dd>
                <dt><span class="kbd">Ctrl</span> <span class="kbd">Space</span></dt>
                <dd>{t('welcome.keySuggest')}</dd>
              </dl>
            </div>
          </div>
        {/if}
      </div>
    </main>

    {#if ws.rightPanel}
      <div class="w-80 shrink-0 border-l border-line">
        <RightPanel />
      </div>
    {/if}
  </div>
</div>
