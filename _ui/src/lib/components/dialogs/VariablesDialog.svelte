<script lang="ts">
  import { Variable } from 'lucide-svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import { answerVariables, rememberedValue, variablesState } from '@/lib/stores/variables.svelte';
  import { t } from '@/lib/i18n/index.svelte';

  let values = $state<Record<string, string>>({});
  let seen: unknown = null;

  $effect(() => {
    const cur = variablesState.current;
    if (cur !== seen) {
      seen = cur;
      values = Object.fromEntries((cur?.items ?? []).map((p) => [p.name, rememberedValue(p.name)]));
      // The editor keeps focus otherwise, and typing would go there.
      if (cur) setTimeout(() => (document.getElementById('var-0') as HTMLInputElement | null)?.select(), 0);
    }
  });

  function submit(e: SubmitEvent) {
    e.preventDefault();
    answerVariables({ ...values });
  }
</script>

{#if variablesState.current}
  {@const req = variablesState.current}
  <Modal title={t('variables.title')} onclose={() => answerVariables(null)} width="max-w-lg">
    <form id="vars-form" class="space-y-3" onsubmit={submit}>
      <p class="text-xs text-muted">{t('variables.hint')}</p>
      {#each req.items as p, i (p.name)}
        <div class="grid grid-cols-[9rem_1fr] items-center gap-2">
          <label class="flex min-w-0 items-center gap-1.5 font-mono text-xs" for="var-{i}">
            <Variable size={12} class="shrink-0 text-type" /><span class="truncate" title={p.token}>{p.token}</span>
          </label>
          <input id="var-{i}" class="input font-mono" bind:value={values[p.name]} placeholder="'text' / 42 / NULL" />
        </div>
      {/each}
    </form>

    {#snippet footer()}
      <button class="btn-outline" onclick={() => answerVariables(null)}>{t('common.cancel')}</button>
      <button class="btn-primary" form="vars-form">{t('query.run')}</button>
    {/snippet}
  </Modal>
{/if}
