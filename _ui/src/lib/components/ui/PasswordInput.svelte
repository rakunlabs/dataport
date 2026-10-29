<script lang="ts">
  import type { HTMLInputAttributes } from 'svelte/elements';
  import { Eye, EyeOff } from 'lucide-svelte';
  import { t } from '@/lib/i18n/index.svelte';

  // A password input with a button that shows or hides what was typed.
  let { value = $bindable(''), class: cls = 'input', ...rest }: Omit<HTMLInputAttributes, 'type'> & { value?: string } = $props();

  let shown = $state(false);
</script>

<div class="relative">
  <input {...rest} class="{cls} pr-8" type={shown ? 'text' : 'password'} bind:value spellcheck="false" autocapitalize="off" />
  <button
    type="button"
    class="absolute inset-y-0 right-0 flex w-8 cursor-pointer items-center justify-center text-faint transition-colors hover:text-fg disabled:pointer-events-none disabled:opacity-40"
    onclick={() => (shown = !shown)}
    disabled={rest.disabled}
    title={shown ? t('common.hidePassword') : t('common.showPassword')}
    aria-label={shown ? t('common.hidePassword') : t('common.showPassword')}
    aria-pressed={shown}
  >
    {#if shown}<EyeOff size={14} />{:else}<Eye size={14} />{/if}
  </button>
</div>
