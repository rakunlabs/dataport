<script lang="ts" module>
  // Open modals, newest last: only the top one answers Escape and keeps the
  // focus, so a confirmation over a preview closes alone.
  const stack: symbol[] = [];
</script>

<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { onMount, type Snippet } from 'svelte';
  import { X } from '@lucide/svelte';

  let {
    title,
    onclose,
    children,
    footer,
    width = 'max-w-md',
  }: { title: string; onclose: () => void; children: Snippet; footer?: Snippet; width?: string } = $props();

  const id = Symbol('modal');
  let dialog = $state<HTMLDivElement>();

  const focusable = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"]), [contenteditable="true"]';

  // Focus moves into the dialog, so keys stop going to what is behind it
  // (an editor would take Enter and Tab), and goes back when it closes.
  onMount(() => {
    stack.push(id);
    const before = document.activeElement as HTMLElement | null;
    const first =
      dialog?.querySelector<HTMLElement>('[autofocus]') ??
      dialog?.querySelector<HTMLElement>('input:not([disabled]):not([type="hidden"]), textarea:not([disabled]), select:not([disabled])') ??
      dialog?.querySelector<HTMLElement>('[data-modal-footer] .btn-primary:not([disabled]), [data-modal-footer] .btn-danger:not([disabled])') ??
      dialog;
    // A child that focuses itself on mount keeps it.
    if (!dialog?.contains(document.activeElement)) first?.focus();
    return () => {
      stack.splice(stack.indexOf(id), 1);
      if (before?.isConnected) before.focus();
    };
  });

  function onkeydown(e: KeyboardEvent) {
    if (stack.at(-1) !== id) return;
    if (e.key === 'Escape') {
      e.stopPropagation();
      onclose();
      return;
    }
    // Tab stays among the dialog's own controls.
    if (e.key === 'Tab' && dialog) {
      const items = [...dialog.querySelectorAll<HTMLElement>(focusable)].filter((el) => el.offsetParent !== null);
      if (!items.length) {
        e.preventDefault();
        return;
      }
      const at = items.indexOf(document.activeElement as HTMLElement);
      if (e.shiftKey && at <= 0) {
        e.preventDefault();
        items.at(-1)!.focus();
      } else if (!e.shiftKey && (at === -1 || at === items.length - 1)) {
        e.preventDefault();
        items[0].focus();
      }
    }
  }
</script>

<svelte:window {onkeydown} />

<div class="fixed inset-0 z-50 flex items-start justify-center bg-black/45 px-4 py-[6vh]" role="presentation">
  <div bind:this={dialog} tabindex="-1" class="flex max-h-[88vh] w-full {width} flex-col rounded-lg border border-line bg-raised shadow-dialog outline-none" role="dialog" aria-modal="true" aria-label={title}>
    <div class="flex h-11 shrink-0 items-center justify-between border-b border-line pr-2 pl-4">
      <h2 class="text-sm font-semibold">{title}</h2>
      <button class="icon-btn" onclick={onclose} aria-label={t('common.close')}><X size={15} /></button>
    </div>
    <div class="min-h-0 flex-1 overflow-y-auto px-4 py-4">
      {@render children()}
    </div>
    {#if footer}
      <div data-modal-footer class="flex shrink-0 items-center justify-end gap-2 rounded-b-lg border-t border-line bg-panel px-4 py-2.5">
        {@render footer()}
      </div>
    {/if}
  </div>
</div>
