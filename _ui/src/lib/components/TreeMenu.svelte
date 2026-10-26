<script lang="ts" module>
  import type { Component } from 'svelte';

  export interface MenuItem {
    label?: string;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    icon?: Component<any> | (new (...args: any[]) => any);
    action?: () => unknown;
    danger?: boolean;
    hidden?: boolean;
    sep?: boolean;
    // A submenu that opens beside the item, such as More.
    children?: MenuItem[];
  }
</script>

<script lang="ts">
  import { ChevronRight } from 'lucide-svelte';

  let { x, y, items, onclose }: { x: number; y: number; items: MenuItem[]; onclose: () => void } = $props();

  // Separators only between visible items, never first, last or doubled.
  const shown = $derived(visible(items).filter((it) => !it.children || visible(it.children).length));

  let el: HTMLDivElement | undefined = $state();
  // The open submenu: its item and where it shows.
  let sub = $state<{ index: number; x: number; y: number } | null>(null);
  let subEl: HTMLDivElement | undefined = $state();
  let subPos = $state({ left: 0, top: 0 });

  function openSub(e: MouseEvent | FocusEvent, i: number) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    sub = { index: i, x: r.right - 2, y: r.top - 4 };
  }

  // A submenu that does not fit on the right opens on the left.
  $effect(() => {
    if (!sub) return;
    const w = subEl?.offsetWidth ?? 0;
    const h = subEl?.offsetHeight ?? 0;
    const parent = el?.getBoundingClientRect();
    const left = sub.x + w > window.innerWidth - 8 && parent ? parent.left - w + 2 : sub.x;
    subPos = { left, top: Math.min(sub.y, window.innerHeight - h - 8) };
  });

  function visible(list: MenuItem[]) {
    const out: MenuItem[] = [];
    for (const it of list) {
      if (it.hidden) continue;
      if (it.sep && (!out.length || out[out.length - 1].sep)) continue;
      out.push(it);
    }
    while (out.length && out[out.length - 1].sep) out.pop();
    return out;
  }
  let pos = $state({ left: 0, top: 0 });

  // Keep the menu inside the window.
  $effect(() => {
    void shown;
    const h = el?.offsetHeight ?? 0;
    const w = el?.offsetWidth ?? 0;
    pos = { left: Math.min(x, window.innerWidth - w - 8), top: Math.min(y, window.innerHeight - h - 8) };
  });

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') onclose();
  }
</script>

<svelte:window onclick={onclose} oncontextmenu={onclose} {onkeydown} onblur={onclose} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div
  bind:this={el}
  class="popover fixed z-50 min-w-52"
  style="left:{pos.left}px; top:{pos.top}px"
  role="menu"
  tabindex="-1"
  onclick={(e) => e.stopPropagation()}
  oncontextmenu={(e) => {
    e.preventDefault();
    e.stopPropagation();
  }}
>
  {#each shown as it, i (i)}
    {#if it.sep}
      <div class="menu-sep"></div>
    {:else if it.children}
      <button
        class="menu-item {sub?.index === i ? 'bg-accent text-accent-fg' : ''}"
        role="menuitem"
        aria-haspopup="menu"
        aria-expanded={sub?.index === i}
        onmouseenter={(e) => openSub(e, i)}
        onfocus={(e) => openSub(e, i)}
        onclick={(e) => openSub(e, i)}
      >
        {#if it.icon}<it.icon size={13} />{:else}<span class="w-[13px]"></span>{/if}
        <span class="flex-1 text-left">{it.label}</span>
        <ChevronRight size={12} class="text-faint" />
      </button>
    {:else}
      <button
        class="menu-item {it.danger ? 'text-danger' : ''}"
        role="menuitem"
        onmouseenter={() => (sub = null)}
        onclick={() => {
          onclose();
          void it.action?.();
        }}
      >
        {#if it.icon}<it.icon size={13} />{:else}<span class="w-[13px]"></span>{/if}
        {it.label}
      </button>
    {/if}
  {/each}
</div>

{#if sub && shown[sub.index]?.children}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div
    bind:this={subEl}
    class="popover fixed z-50 min-w-48"
    style="left:{subPos.left}px; top:{subPos.top}px"
    role="menu"
    tabindex="-1"
    onclick={(e) => e.stopPropagation()}
    oncontextmenu={(e) => {
      e.preventDefault();
      e.stopPropagation();
    }}
  >
    {#each visible(shown[sub.index].children ?? []) as it, i (i)}
      {#if it.sep}
        <div class="menu-sep"></div>
      {:else}
        <button
          class="menu-item {it.danger ? 'text-danger' : ''}"
          role="menuitem"
          onclick={() => {
            onclose();
            void it.action?.();
          }}
        >
          {#if it.icon}<it.icon size={13} />{:else}<span class="w-[13px]"></span>{/if}
          {it.label}
        </button>
      {/if}
    {/each}
  </div>
{/if}
