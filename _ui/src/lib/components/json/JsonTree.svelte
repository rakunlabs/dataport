<script lang="ts">
  // A foldable JSON tree with expand and collapse all.
  import { setContext } from 'svelte';
  import JsonNode from './JsonNode.svelte';
  import type { TreeControl } from './jsonTree';

  let { value, depth = 3, control = $bindable() }: { value: unknown; depth?: number; control?: TreeControl } = $props();

  const ctl = $state<TreeControl>({ depth: 3, gen: 0, all: true });
  $effect.pre(() => {
    ctl.depth = depth;
  });
  setContext('json-tree', ctl);
  control = ctl;
</script>

<div class="tree"><JsonNode {value} /></div>

<style>
  .tree { padding: 8px 10px 8px 6px; font: 12px/1.6 var(--font-mono); }
</style>
