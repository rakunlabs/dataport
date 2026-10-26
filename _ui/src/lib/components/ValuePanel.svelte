<script lang="ts">
  // The value of the selected cell, beside the grid, as DBeaver's value
  // panel: JSON is formatted and coloured, long text wraps, and in an
  // editable table the value can be changed here.
  import { t } from '@/lib/i18n/index.svelte';
  import { Copy, X, Braces, WrapText, Check, Undo2, ChevronsUpDown, ChevronsDownUp, FileText } from 'lucide-svelte';
  import { toast } from '@/lib/stores/toast.svelte';
  import JsonTree from '@/lib/components/json/JsonTree.svelte';
  import { isContainer, parseJson, type TreeControl } from '@/lib/components/json/jsonTree';

  let {
    column,
    type,
    value,
    editable = false,
    onchange,
    onclose,
  }: {
    column: string;
    type: string;
    value: unknown;
    editable?: boolean;
    onchange?: (v: unknown) => void;
    onclose: () => void;
  } = $props();

  let wrap = $state(true);
  // Raw shows the formatted text instead of the tree.
  let raw = $state(false);
  let control = $state<TreeControl>();

  function setAll(all: boolean) {
    if (!control) return;
    control.all = all;
    control.gen++;
  }
  let draft = $state<string | null>(null);

  const isNull = $derived(value === null || value === undefined);

  // Parsed JSON, when the value is JSON: objects and arrays only, so plain
  // numbers and strings stay as they are.
  const json = $derived.by(() => {
    if (isNull) return undefined;
    if (typeof value === 'object') return value;
    const s = String(value).trim();
    if (!/^[[{]/.test(s)) return undefined;
    const r = parseJson(s);
    return r.ok && isContainer(r.value) ? r.value : undefined;
  });

  const text = $derived.by(() => {
    if (isNull) return '';
    if (json !== undefined) {
      try {
        return JSON.stringify(typeof value === 'object' ? value : JSON.parse(String(value)), null, 2);
      } catch {
        return String(value);
      }
    }
    return String(value);
  });

  // A new cell starts a new draft.
  $effect(() => {
    void value;
    void column;
    draft = null;
  });

  async function copy() {
    try {
      await navigator.clipboard.writeText(draft ?? text);
      toast(t('grid.copied'), 'success', 1200);
    } catch {
      toast(t('grid.clipboardUnavailable'), 'error');
    }
  }

  // JSON is saved compact, as the database would store it; other text as
  // written.
  function apply() {
    if (draft === null) return;
    let v: unknown = draft;
    if (json !== undefined || /json/i.test(type)) {
      try {
        v = JSON.stringify(JSON.parse(draft));
      } catch (e) {
        toast(t('valuePanel.invalidJson', { error: e instanceof Error ? e.message : String(e) }), 'error');
        return;
      }
    }
    onchange?.(v);
    draft = null;
  }

  function format() {
    if (draft === null) return;
    try {
      draft = JSON.stringify(JSON.parse(draft), null, 2);
    } catch (e) {
      toast(t('valuePanel.invalidJson', { error: e instanceof Error ? e.message : String(e) }), 'error');
    }
  }
</script>

<aside class="flex h-full min-w-0 flex-col border-l border-line bg-bg">
  <div class="flex h-[30px] shrink-0 items-center gap-1.5 border-b border-line bg-panel pr-1 pl-3">
    <span class="min-w-0 truncate text-xs font-medium" title={column}>{column}</span>
    <span class="shrink-0 font-mono text-2xs text-type">{type}</span>
    <div class="flex-1"></div>
    {#if !isNull}<span class="shrink-0 text-2xs text-faint tabular-nums">{t('cell.chars', { count: text.length })}</span>{/if}
    {#if json !== undefined && draft === null}
      {#if !raw}
        <button class="icon-btn size-6" onclick={() => setAll(true)} title={t('json.expandAll')} aria-label={t('json.expandAll')}><ChevronsUpDown size={13} /></button>
        <button class="icon-btn size-6" onclick={() => setAll(false)} title={t('json.collapseAll')} aria-label={t('json.collapseAll')}><ChevronsDownUp size={13} /></button>
      {/if}
      <button class="icon-btn size-6" aria-pressed={raw} onclick={() => (raw = !raw)} title={raw ? t('json.tree') : t('json.raw')} aria-label={raw ? t('json.tree') : t('json.raw')}>
        {#if raw}<Braces size={13} />{:else}<FileText size={13} />{/if}
      </button>
    {/if}
    <button class="icon-btn size-6" aria-pressed={wrap} onclick={() => (wrap = !wrap)} title={t('valuePanel.wrap')} aria-label={t('valuePanel.wrap')}><WrapText size={13} /></button>
    <button class="icon-btn size-6" onclick={copy} title={t('common.copy')} aria-label={t('common.copy')}><Copy size={13} /></button>
    <button class="icon-btn size-6" onclick={onclose} title={t('valuePanel.close')} aria-label={t('valuePanel.close')}><X size={14} /></button>
  </div>

  <div class="min-h-0 flex-1 overflow-auto">
    {#if draft !== null}
      <textarea
        class="block h-full w-full resize-none bg-bg p-3 font-mono text-xs outline-none {wrap ? '' : 'whitespace-pre'}"
        bind:value={draft}
        spellcheck="false"
        onkeydown={(e) => {
          if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
            e.preventDefault();
            apply();
          } else if (e.key === 'Escape') {
            e.stopPropagation();
            draft = null;
          }
          e.stopPropagation();
        }}
      ></textarea>
    {:else if isNull}
      <p class="p-3 font-mono text-xs text-faint italic">NULL</p>
    {:else if json !== undefined && !raw}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div ondblclick={() => editable && (draft = text)}>
        <JsonTree value={json} bind:control />
      </div>
    {:else}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <pre
        class="p-3 font-mono text-xs leading-5 {wrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'}"
        ondblclick={() => editable && (draft = text)}>{text}</pre>
    {/if}
  </div>

  {#if editable}
    <div class="flex h-8 shrink-0 items-center gap-1.5 border-t border-line bg-panel px-2">
      {#if draft === null}
        <button class="btn h-6 text-xs" onclick={() => (draft = text)}>{t('common.edit')}</button>
        <span class="text-2xs text-faint">{t('valuePanel.editHint')}</span>
      {:else}
        {#if json !== undefined || /json/i.test(type)}
          <button class="btn h-6 text-xs" onclick={format}><Braces size={12} /> {t('valuePanel.format')}</button>
        {/if}
        <div class="flex-1"></div>
        <button class="btn h-6 text-xs" onclick={() => (draft = null)}><Undo2 size={12} /> {t('common.cancel')}</button>
        <button class="btn-primary h-6 text-xs" onclick={apply} title="Ctrl+Enter"><Check size={12} /> {t('valuePanel.apply')}</button>
      {/if}
    </div>
  {/if}
</aside>
