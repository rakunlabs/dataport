<script lang="ts">
  import { toolDialog } from '@/lib/stores/dialogs.svelte';
  import ExportDialog from '@/lib/components/ExportDialog.svelte';
  import ImportDialog from '@/lib/components/ImportDialog.svelte';
  import TransferDialog from '@/lib/components/TransferDialog.svelte';
</script>

{#if toolDialog.current}
  {@const d = toolDialog.current}
  {#key d}
    {#if d.kind === 'export'}
      <ExportDialog connectionId={d.connectionId} schema={d.schema} tables={d.tables} schemaFixed={d.schemaFixed} />
    {:else if d.kind === 'import'}
      <ImportDialog connectionId={d.connectionId} schema={d.schema} tables={d.tables} schemaFixed={d.schemaFixed} />
    {:else}
      <TransferDialog connectionId={d.connectionId} schema={d.schema} tables={d.tables} schemaFixed={d.schemaFixed} />
    {/if}
  {/key}
{/if}
