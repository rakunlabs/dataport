<script lang="ts">
  import { tick } from 'svelte';
  import type { ColumnInfo } from '@/lib/api/types';
  import { complete, type Completion } from '@/lib/helper/condition';

  let {
    value = $bindable(),
    kind,
    columns,
    dbType,
    quote,
    placeholder = '',
    onsubmit,
  }: {
    value: string;
    kind: 'where' | 'order';
    columns: ColumnInfo[];
    dbType: string;
    quote: (name: string) => string;
    placeholder?: string;
    onsubmit: () => void;
  } = $props();

  let input: HTMLInputElement;
  let list = $state<Completion | null>(null);
  let active = $state(-1);
  let pos = $state({ left: 0, top: 0 });

  function refresh() {
    const cursor = input.selectionStart ?? value.length;
    list = complete(kind, value, cursor, columns, dbType, quote);
    active = list?.preselect ? 0 : -1;
    if (list) place(list.from);
  }

  // Place the list under the start of the word being completed.
  function place(from: number) {
    const style = getComputedStyle(input);
    const ctx = document.createElement('canvas').getContext('2d');
    let x = 0;
    if (ctx) {
      ctx.font = `${style.fontSize} ${style.fontFamily}`;
      x = ctx.measureText(value.slice(0, from)).width;
    }
    const r = input.getBoundingClientRect();
    pos = { left: Math.min(r.left + x - input.scrollLeft, window.innerWidth - 300), top: r.bottom + 2 };
  }

  function accept(i: number) {
    if (!list) return;
    const item = list.items[i];
    const { from, to } = list;
    value = value.slice(0, from) + item.insert + value.slice(to);
    const at = from + item.insert.length;
    list = null;
    // Place the cursor as soon as the value is in the input, before the
    // next key press can land at the old position.
    void tick().then(() => {
      input.setSelectionRange(at, at);
      // The next step usually has suggestions too: column → operator.
      refresh();
    });
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.isComposing) return;
    if (list) {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        active = (active + 1) % list.items.length;
        return;
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        active = active <= 0 ? list.items.length - 1 : active - 1;
        return;
      }
      if ((e.key === 'Enter' || e.key === 'Tab') && active >= 0) {
        e.preventDefault();
        accept(active);
        return;
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        list = null;
        return;
      }
    }
    if (e.key === ' ' && e.ctrlKey) {
      e.preventDefault();
      refresh();
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      // The tab would reload on Ctrl+Enter too; this submit is the one.
      e.stopPropagation();
      list = null;
      onsubmit();
    }
  }
</script>

<input
  bind:this={input}
  class="h-full min-w-0 flex-1 bg-transparent font-mono text-xs outline-none placeholder:text-faint"
  {placeholder}
  bind:value
  oninput={refresh}
  onfocus={() => value && refresh()}
  onclick={refresh}
  onblur={() => setTimeout(() => (list = null), 120)}
  {onkeydown}
  spellcheck="false"
  autocomplete="off"
  role="combobox"
  aria-expanded={!!list}
  aria-autocomplete="list"
  aria-controls="cond-list-{kind}"
/>

{#if list}
  <ul id="cond-list-{kind}" class="popover fixed z-50 max-h-72 min-w-56 overflow-auto" style="left:{pos.left}px; top:{pos.top}px" role="listbox">
    {#each list.items as item, i (item.label)}
      <li role="option" aria-selected={i === active}>
        <button
          type="button"
          class="mx-1 flex h-6 w-[calc(100%-0.5rem)] items-center gap-2.5 rounded-sm px-2 text-left font-mono text-xs {i === active ? 'bg-accent text-accent-fg' : 'text-fg hover:bg-hover'}"
          onmousedown={(e) => {
            e.preventDefault();
            accept(i);
          }}
          onmouseenter={() => (active = i)}
        >
          <span
            class="flex size-4 shrink-0 items-center justify-center rounded-sm text-[10px] font-semibold {i === active ? 'bg-white/20' : item.kind === 'column' ? 'bg-accent-soft text-accent' : item.kind === 'operator' ? 'bg-warn-soft text-warn' : 'bg-sunken text-muted'}"
          >
            {item.kind === 'column' ? 'C' : item.kind === 'operator' ? 'O' : item.kind === 'value' ? 'V' : 'K'}
          </span>
          <span class="flex-1 truncate">{item.label}</span>
          {#if item.detail}<span class="truncate text-2xs {i === active ? 'opacity-75' : 'text-faint'}">{item.detail}</span>{/if}
        </button>
      </li>
    {/each}
  </ul>
{/if}
