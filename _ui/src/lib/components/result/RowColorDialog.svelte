<script lang="ts">
  // The row colour rules of one table or query, to add, change, turn off
  // and remove, as DBeaver's "Row colors…" window.
  import { untrack } from 'svelte';
  import { Plus, Trash2 } from 'lucide-svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { ops, palette, type ColorRule } from '@/lib/helper/rowColors';
  import { colorRules, setColorRules } from '@/lib/stores/rowColors.svelte';

  let { scope, columns, column = '', onclose }: { scope: string; columns: string[]; column?: string; onclose: () => void } = $props();

  // Edited apart and saved on Apply.
  let rules = $state<ColorRule[]>(untrack(() => colorRules(scope).map((r) => ({ ...r }))));

  function add() {
    rules = [...rules, { id: Math.random().toString(36).slice(2, 10), column: column || columns[0] || '', op: '=', value: '', color: palette[rules.length % palette.length], enabled: true }];
  }

  function remove(id: string) {
    rules = rules.filter((r) => r.id !== id);
  }

  function apply() {
    setColorRules(scope, rules.filter((r) => r.column));
    onclose();
  }

  const noValue = (op: string) => op === 'null' || op === 'notnull';
</script>

<Modal title={t('rowColors.title')} {onclose} width="max-w-3xl">
  <p class="mb-3 text-xs text-muted">{t('rowColors.hint')}</p>
  {#if !rules.length}
    <p class="rounded-md border border-dashed border-line px-3 py-6 text-center text-xs text-faint">{t('rowColors.empty')}</p>
  {:else}
    <div class="flex flex-col gap-1.5">
      {#each rules as r (r.id)}
        <div class="flex flex-wrap items-center gap-1.5 rounded-md border border-line bg-panel px-2 py-1.5 text-xs {r.enabled ? '' : 'opacity-55'}">
          <input type="checkbox" bind:checked={r.enabled} title={t('rowColors.enabled')} aria-label={t('rowColors.enabled')} />
          <select class="input h-7 w-36 text-xs" bind:value={r.column} aria-label={t('record.column')}>
            {#each columns.includes(r.column) ? columns : [r.column, ...columns] as c (c)}<option value={c}>{c}</option>{/each}
          </select>
          <select class="input h-7 w-28 text-xs" bind:value={r.op} aria-label={t('rowColors.operator')}>
            {#each ops as op (op)}<option value={op}>{t(`rowColors.op.${op}`)}</option>{/each}
          </select>
          {#if !noValue(r.op)}
            <input class="input h-7 w-28 font-mono text-xs" bind:value={r.value} placeholder={r.op === 'range' ? t('rowColors.from') : t('record.value')} />
          {/if}
          {#if r.op === 'range'}
            <input class="input h-7 w-24 font-mono text-xs" bind:value={r.value2} placeholder={t('rowColors.to')} />
          {/if}
          <input type="color" class="h-7 w-8 cursor-pointer rounded border border-line bg-transparent" bind:value={r.color} title={t('rowColors.color')} aria-label={t('rowColors.color')} />
          {#if r.op === 'range'}
            <input type="color" class="h-7 w-8 cursor-pointer rounded border border-line bg-transparent" value={r.color2 || r.color} oninput={(e) => (r.color2 = e.currentTarget.value)} title={t('rowColors.color2')} aria-label={t('rowColors.color2')} />
          {:else}
            <label class="flex items-center gap-1 text-muted"><input type="checkbox" bind:checked={r.cellOnly} /> {t('rowColors.cellOnly')}</label>
          {/if}
          <div class="flex-1"></div>
          <button class="icon-btn size-6 hover:text-danger" onclick={() => remove(r.id)} title={t('common.delete')} aria-label={t('common.delete')}><Trash2 size={13} /></button>
        </div>
      {/each}
    </div>
  {/if}
  <button class="btn mt-3 h-7" onclick={add}><Plus size={13} /> {t('rowColors.add')}</button>
  {#snippet footer()}
    {#if rules.length}<button class="btn mr-auto" onclick={() => (rules = [])}>{t('rowColors.clearAll')}</button>{/if}
    <button class="btn" onclick={onclose}>{t('common.cancel')}</button>
    <button class="btn-primary" onclick={apply}>{t('valuePanel.apply')}</button>
  {/snippet}
</Modal>
