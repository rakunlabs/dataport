import { t } from '@/lib/i18n/index.svelte';
import { toast } from '@/lib/stores/toast.svelte';

export async function copyText(text: string, okMsg = t('grid.copied')): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    toast(okMsg, 'success', 1200);
    return true;
  } catch {
    toast(t('grid.clipboardUnavailable'), 'error');
    return false;
  }
}
