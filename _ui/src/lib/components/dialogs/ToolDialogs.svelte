<script lang="ts">
  import { toolDialog } from '@/lib/stores/dialogs.svelte';
  import ExportDialog from '@/lib/components/dialogs/ExportDialog.svelte';
  import ImportDialog from '@/lib/components/dialogs/ImportDialog.svelte';
  import TransferDialog from '@/lib/components/dialogs/TransferDialog.svelte';
  import GenerateDialog from '@/lib/components/dialogs/GenerateDialog.svelte';
</script>

{#if toolDialog.current}
  {@const d = toolDialog.current}
  {#key d}
    {#if d.kind === 'export'}
      <ExportDialog connectionId={d.connectionId} schema={d.schema} tables={d.tables} schemaFixed={d.schemaFixed} />
    {:else if d.kind === 'import'}
      <ImportDialog connectionId={d.connectionId} schema={d.schema} tables={d.tables} schemaFixed={d.schemaFixed} />
    {:else if d.kind === 'generate'}
      <GenerateDialog connectionId={d.connectionId} schema={d.schema} tables={d.tables} />
    {:else}
      <TransferDialog connectionId={d.connectionId} schema={d.schema} tables={d.tables} schemaFixed={d.schemaFixed} />
    {/if}
  {/key}
{/if}
