<script lang="ts">
  import { untrack } from 'svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { Download, Info } from 'lucide-svelte';
  import Modal from '@/lib/components/Modal.svelte';
  import TablePicker from '@/lib/components/TablePicker.svelte';
  import { connection } from '@/lib/stores/workspace.svelte';
  import { closeToolDialog } from '@/lib/stores/dialogs.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { api, ApiError, qs } from '@/lib/api/client';
  import { askCredentials, needsCredentials } from '@/lib/stores/credentials.svelte';

  let props: { connectionId: string; schema: string; tables: string[]; schemaFixed: boolean } = $props();

  // The dialog is made anew for each request, so its first values are all
  // it needs from the props.
  const { connectionId, schema: initialSchema, tables, schemaFixed } = untrack(() => ({ ...props }));

  const conn = $derived(connection(connectionId));

  let schema = $state(initialSchema);
  let selected = $state<string[]>([...tables]);
  let format = $state<'csv' | 'json' | 'sql'>('sql');
  let structure = $state(true);
  let data = $state(true);
  let checking = $state(false);

  const many = $derived(selected.length > 1 && format !== 'sql');

  function url() {
    const p = new URLSearchParams();
    if (schema) p.set('schema', schema);
    for (const tb of selected) p.append('table', tb);
    p.set('format', format);
    if (format === 'sql') {
      p.set('structure', structure ? '1' : '0');
      p.set('data', data ? '1' : '0');
    }
    return `./api/v1/connections/${connectionId}/export?${p}`;
  }

  // The download goes straight from the server to the browser, without
  // holding the file in this page. A quick check first turns sign-in and
  // permission problems into a message instead of a broken download.
  async function start() {
    checking = true;
    try {
      await api.get(`/connections/${connectionId}/columns${qs({ schema, table: selected[0] })}`);
    } catch (e) {
      checking = false;
      if (needsCredentials(e) && (await askCredentials(connectionId, (e as ApiError).message))) return start();
      toast(errorMessage(e), 'error');
      return;
    }
    checking = false;
    const a = document.createElement('a');
    a.href = url();
    a.rel = 'noopener';
    document.body.appendChild(a);
    a.click();
    a.remove();
    toast(t('export.started'), 'success');
    closeToolDialog();
  }
</script>

<Modal title={t('export.title', { name: conn?.name ?? '' })} onclose={closeToolDialog} width="max-w-xl">
  <div class="space-y-4">
    <TablePicker {connectionId} bind:schema bind:selected {schemaFixed} />

    <div>
      <span class="label">{t('export.format')}</span>
      <div class="flex gap-2">
        {#each [['sql', 'SQL'], ['csv', 'CSV'], ['json', 'JSON']] as [v, label] (v)}
          <label class="flex flex-1 cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm {format === v ? 'border-accent bg-accent/5' : 'border-line hover:bg-hover'}">
            <input type="radio" name="fmt" value={v} bind:group={format} />
            {label}
          </label>
        {/each}
      </div>
    </div>

    {#if format === 'sql'}
      <div class="flex gap-5">
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={structure} /> {t('export.structure')}</label>
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={data} /> {t('export.data')}</label>
      </div>
    {/if}

    <p class="flex items-start gap-1.5 text-xs text-muted">
      <Info size={13} class="mt-0.5 shrink-0" />
      {many ? t('export.zipHint') : t('export.hint')}
    </p>
  </div>

  {#snippet footer()}
    <button class="btn-outline" onclick={closeToolDialog}>{t('common.cancel')}</button>
    <button class="btn-primary" disabled={!selected.length || checking || (format === 'sql' && !structure && !data)} onclick={start}>
      <Download size={14} />
      {t('export.download')}
    </button>
  {/snippet}
</Modal>
