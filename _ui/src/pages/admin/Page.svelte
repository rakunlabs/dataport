<script lang="ts">
  import type { Snippet } from 'svelte';

  // An admin page: a toolbar with the title and actions, an optional
  // one-line description and the content filling the rest.
  let { title, description, actions, children }: { title: string; description?: string; actions?: Snippet; children: Snippet } = $props();
</script>

<div class="flex h-full min-h-0 flex-col">
  <div class="toolbar h-11 gap-3 bg-bg px-4">
    <h1 class="text-sm font-semibold">{title}</h1>
    {#if description}<p class="min-w-0 flex-1 truncate text-xs text-muted" title={description}>{description}</p>{:else}<span class="flex-1"></span>{/if}
    {#if actions}<div class="flex shrink-0 items-center gap-2">{@render actions()}</div>{/if}
  </div>
  <div class="min-h-0 flex-1 overflow-auto bg-bg">
    {@render children()}
  </div>
</div>
