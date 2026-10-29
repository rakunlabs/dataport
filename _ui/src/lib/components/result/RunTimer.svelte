<script lang="ts">
  // The time a run has taken so far, ticking while it runs, with a button
  // to stop it. It follows a tab's run, or one given by its start, as for a
  // run into a new result.
  import { LoaderCircle, Square } from 'lucide-svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { formatElapsed } from '@/lib/helper/format';
  import { cancel, type Tab } from '@/lib/stores/workspace.svelte';

  let {
    tab,
    run,
    large = false,
  }: {
    tab?: Tab;
    run?: { startedAt: number; cancelling: boolean; oncancel: () => void };
    large?: boolean;
  } = $props();

  const running = $derived(run ? true : !!tab?.running);
  const startedAt = $derived(run ? run.startedAt : (tab?.runStartedAt ?? 0));
  const cancelling = $derived(run ? run.cancelling : !!tab?.cancelling);
  const canCancel = $derived(run ? true : !!tab?.queryId);

  function stop() {
    if (run) run.oncancel();
    else if (tab) void cancel(tab.id);
  }

  let now = $state(Date.now());

  $effect(() => {
    if (!running) return;
    now = Date.now();
    const timer = setInterval(() => (now = Date.now()), 250);
    return () => clearInterval(timer);
  });

  const elapsed = $derived(startedAt ? formatElapsed(Math.max(0, now - startedAt)) : '');
</script>

{#if large}
  <div class="flex flex-col items-center gap-3" role="status" aria-live="off">
    <div class="flex items-center gap-2 text-sm text-muted">
      <LoaderCircle size={15} class="animate-spin" />
      {cancelling ? t('result.cancelling') : t('result.running')}
    </div>
    <div class="font-mono text-2xl text-fg tabular-nums" aria-label={t('result.runningFor', { time: elapsed })}>{elapsed}</div>
    {#if canCancel}
      <button class="btn-outline" onclick={stop} disabled={cancelling}>
        <Square size={11} fill="currentColor" class="text-danger" /> {t('query.stop')}
      </button>
    {/if}
  </div>
{:else}
  <span class="flex items-center gap-1.5 text-xs text-muted tabular-nums" title={t('result.runningFor', { time: elapsed })}>
    <LoaderCircle size={12} class="animate-spin" />
    {cancelling ? t('result.cancelling') : elapsed}
  </span>
{/if}
