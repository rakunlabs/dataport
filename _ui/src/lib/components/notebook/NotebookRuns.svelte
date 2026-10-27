<script lang="ts">
  import { onMount } from 'svelte';
  import { RefreshCw, Square, ChevronRight } from 'lucide-svelte';
  import * as nb from '@/lib/api/notebooks';
  import type { Notebook, NotebookRun } from '@/lib/api/notebooks';
  import { fmtDateTime, t } from '@/lib/i18n/index.svelte';
  import { errorMessage } from '@/lib/stores/toast.svelte';
  import { formatDuration } from '@/lib/helper/format';

  let { note, runnable, offset = $bindable(0), onerror }: { note: Notebook; runnable: boolean; offset?: number; onerror: (msg: string) => void } = $props();

  let runs = $state<NotebookRun[]>([]);

  onMount(() => {
    void loadRuns();
    const timer = setInterval(() => { if (runs.some((r) => r.status === 'running')) void loadRuns(); }, 2000);
    return () => clearInterval(timer);
  });

  export async function loadRuns() {
    if (!note.id) return;
    try { runs = await nb.listNotebookRuns(note.id, offset); }
    catch (e) { onerror(errorMessage(e)); }
  }
  async function cancelRun(r: NotebookRun) {
    try { await nb.cancelNotebookRun(note.id, r.id); await loadRuns(); } catch (e) { onerror(errorMessage(e)); }
  }
</script>

<div class="toolbar"><p class="min-w-0 flex-1 text-xs text-muted">{t('notebook.historyHint')}</p><button class="icon-btn" onclick={loadRuns} title={t('common.refresh')} aria-label={t('common.refresh')}><RefreshCw size={14} /></button></div>
{#if !runs.length}<p class="p-4 text-sm text-muted">{t('notebook.noRuns')}</p>{/if}
{#each runs as r (r.id)}
  <details class="border-b border-line-soft px-3 py-2 text-xs"><summary class="cursor-pointer"><span class={r.status === 'failed' ? 'text-danger' : 'text-fg'}>{t(`notebook.${r.status === 'preview' ? 'previewStatus' : r.status}`)}</span><span class="ml-3 text-muted">{fmtDateTime(r.created_at)} · v{r.version} · {formatDuration(r.info.duration_ms || 0)}</span></summary>
    <div class="mt-2 space-y-1 text-muted"><code class="font-mono">{r.id}</code>{#if r.info.error}<p class="text-danger">{r.info.error}</p>{/if}{#each r.info.cells || [] as c}<p><span class="font-mono">{c.path}</span> · {c.status} · {formatDuration(c.duration_ms)} {c.error || ''}</p>{/each}
      {#if r.status === 'running' && runnable}<button class="btn text-danger" onclick={() => cancelRun(r)}><Square size={12} />{t('notebook.cancel')}</button>{/if}
    </div>
  </details>
{/each}
<div class="flex gap-2 p-3"><button class="btn" disabled={offset === 0} onclick={() => { offset = Math.max(0, offset - 25); void loadRuns(); }} aria-label={t('notebook.previous')}><ChevronRight size={13} class="rotate-180" /></button><button class="btn" disabled={runs.length < 25} onclick={() => { offset += 25; void loadRuns(); }} aria-label={t('notebook.next')}><ChevronRight size={13} /></button></div>
