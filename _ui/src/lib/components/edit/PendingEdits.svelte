<script lang="ts">
  // The controls of grid edits not saved yet: how many, a preview of the
  // SQL, discard and save. Production connections save with a red button.
  import { t } from '@/lib/i18n/index.svelte';
  import { Eye, LoaderCircle, Save, Undo2 } from '@lucide/svelte';

  let {
    pending,
    saving,
    production = false,
    compact = false,
    onpreview,
    ondiscard,
    onsave,
  }: {
    pending: number;
    saving: boolean;
    production?: boolean;
    // The smaller size of a result footer.
    compact?: boolean;
    onpreview: () => void;
    ondiscard: () => void;
    onsave: () => void;
  } = $props();

  const icon = $derived(compact ? 13 : 14);
</script>

<div class="toolbar-sep"></div>
<span class="mr-1 flex shrink-0 items-center gap-1.5 {compact ? '' : 'text-xs'} font-medium whitespace-nowrap text-row-dirty-fg" title={t('edit.pending', { count: pending })}><span class="size-2 rounded-full bg-warn"></span>{pending}</span>
<button class="icon-btn {compact ? 'size-6' : ''}" onclick={onpreview} title={t('edit.preview')} aria-label={t('edit.preview')}><Eye size={icon} /></button>
<button class="icon-btn {compact ? 'size-6' : ''}" onclick={ondiscard} title={t('edit.discard')} aria-label={t('edit.discard')}><Undo2 size={icon} /></button>
<button class="{production ? 'btn-danger' : 'btn-primary'} {compact ? 'h-6' : ''}" onclick={onsave} disabled={saving} title="Ctrl+S">
  {#if saving}<LoaderCircle size={icon - 1} class="animate-spin" />{:else}<Save size={icon - 1} />{/if}
  {t('edit.save', { count: pending })}
</button>
