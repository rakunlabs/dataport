<script lang="ts">
  import Router from 'svelte-spa-router';
  import { routes } from '@/routes';
  import { loadSession, session } from '@/lib/stores/session.svelte';
  import Toasts from '@/lib/components/ui/Toasts.svelte';
  import ConfirmDialog from '@/lib/components/dialogs/ConfirmDialog.svelte';
  import CredentialDialog from '@/lib/components/dialogs/CredentialDialog.svelte';
  import ReviewDialog from '@/lib/components/dialogs/ReviewDialog.svelte';
  import VariablesDialog from '@/lib/components/dialogs/VariablesDialog.svelte';
  import ToolDialogs from '@/lib/components/dialogs/ToolDialogs.svelte';
  import { t } from '@/lib/i18n/index.svelte';

  const ready = loadSession();
</script>

{#await ready}
  <div class="flex h-full items-center justify-center text-muted">{t('common.loading')}</div>
{:then}
  {#if session.ready}
    <Router {routes} />
  {/if}
{/await}

<Toasts />
<ConfirmDialog />
<CredentialDialog />
<ReviewDialog />
<VariablesDialog />
<ToolDialogs />
