<script lang="ts">
  import { copyText } from '@/lib/helper/clipboard';
  import { t } from '@/lib/i18n/index.svelte';
  import { Copy } from 'lucide-svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';

  let { column, type, value, onclose }: { column: string; type: string; value: unknown; onclose: () => void } = $props();

  const text = $derived.by(() => {
    if (value === null || value === undefined) return '';
    if (typeof value === 'object') return JSON.stringify(value, null, 2);
    const s = String(value);
    if ((s.startsWith('{') && s.endsWith('}')) || (s.startsWith('[') && s.endsWith(']'))) {
      try {
        return JSON.stringify(JSON.parse(s), null, 2);
      } catch {
        return s;
      }
    }
    return s;
  });

  function copy() {
    void copyText(text);
  }
</script>

<Modal title={column} {onclose} width="max-w-2xl">
  <div class="mb-2 flex items-center gap-2 text-xs text-muted">
    <span class="font-mono text-type">{type || 'unknown'}</span>
    {#if value === null || value === undefined}<span class="italic">NULL</span>{:else}<span>{t('cell.chars', { count: text.length })}</span>{/if}
  </div>
  <pre class="max-h-[60vh] overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs whitespace-pre-wrap break-all">{text}</pre>

  {#snippet footer()}
    <button class="btn-outline" onclick={copy}><Copy size={14} /> {t('common.copy')}</button>
    <button class="btn-primary" onclick={onclose}>{t('common.close')}</button>
  {/snippet}
</Modal>
