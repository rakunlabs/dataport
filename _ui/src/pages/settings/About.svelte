<script lang="ts">
  import { t, i18n } from '@/lib/i18n/index.svelte';
  import { Copy, Check, ExternalLink, LoaderCircle } from 'lucide-svelte';
  import Logo from '@/lib/components/ui/Logo.svelte';
  import { api } from '@/lib/api/client';
  import type { BuildInfo } from '@/lib/api/types';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let info = $state<BuildInfo | null>(null);
  let copied = $state(false);

  api
    .get<BuildInfo>('/about')
    .then((b) => (info = b))
    .catch((e) => toast(errorMessage(e), 'error'));

  // Release dates are RFC 3339; anything else is shown as it came.
  function when(d: string) {
    const ts = Date.parse(d);
    return Number.isNaN(ts) ? d || '—' : new Date(ts).toLocaleString(i18n.locale, { dateStyle: 'long', timeStyle: 'short' });
  }

  const os: Record<string, string> = { linux: 'Linux', darwin: 'macOS', windows: 'Windows', freebsd: 'FreeBSD' };
  const arch: Record<string, string> = { amd64: 'x86-64 (amd64)', arm64: 'ARM64', '386': 'x86 (386)', arm: 'ARM' };

  const rows = $derived(
    info
      ? [
          { label: t('about.version'), value: info.version },
          { label: t('about.commit'), value: info.commit || '—', mono: true },
          { label: t('about.built'), value: when(info.date) },
          { label: t('about.os'), value: os[info.os] ?? info.os },
          { label: t('about.arch'), value: arch[info.arch] ?? info.arch },
          { label: t('about.go'), value: info.go_version, mono: true },
          { label: t('about.drivers'), value: info.cgo ? t('about.cgo') : t('about.pure') },
        ]
      : [],
  );

  async function copy() {
    if (!info) return;
    const text = [
      `dataport ${info.version}`,
      `commit ${info.commit}`,
      `built ${info.date}`,
      `${info.os}/${info.arch}`,
      info.go_version,
      info.cgo ? 'cgo (odbc, duckdb)' : 'pure go',
    ].join('\n');
    try {
      await navigator.clipboard.writeText(text);
      copied = true;
      setTimeout(() => (copied = false), 1500);
    } catch {
      toast(t('grid.clipboardUnavailable'), 'error');
    }
  }
</script>

<h2 class="text-base font-semibold">{t('about.title')}</h2>

<div class="flex items-center gap-3">
  <Logo size={40} />
  <div>
    <p class="text-lg font-semibold tracking-[-0.01em]">dataport</p>
    <p class="text-sm text-muted">{t('about.tagline')}</p>
  </div>
</div>

{#if !info}
  <p class="flex items-center gap-2 text-sm text-muted"><LoaderCircle size={14} class="animate-spin" /> {t('common.loading')}</p>
{:else}
  <dl class="grid max-w-xl grid-cols-[10rem_1fr] overflow-hidden rounded-md border border-line text-sm">
    {#each rows as r, i (r.label)}
      <dt class="px-3 py-2 text-muted {i ? 'border-t border-line-soft' : ''} bg-panel">{r.label}</dt>
      <dd class="px-3 py-2 {i ? 'border-t border-line-soft' : ''} {r.mono ? 'font-mono text-xs leading-5' : ''}">{r.value}</dd>
    {/each}
  </dl>

  <div class="flex flex-wrap items-center gap-2">
    <a class="btn-outline" href={info.repository} target="_blank" rel="noopener noreferrer">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"
        ><path
          d="M12 .5C5.65.5.5 5.65.5 12a11.5 11.5 0 0 0 7.86 10.92c.58.1.79-.25.79-.56v-2c-3.2.7-3.87-1.37-3.87-1.37-.53-1.33-1.28-1.69-1.28-1.69-1.05-.71.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.7 1.26 3.36.96.1-.75.4-1.26.73-1.55-2.55-.29-5.24-1.28-5.24-5.69 0-1.26.45-2.29 1.19-3.1-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.18 1.18a11 11 0 0 1 5.79 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.81 1.19 1.84 1.19 3.1 0 4.42-2.7 5.4-5.26 5.68.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0 0 23.5 12C23.5 5.65 18.35.5 12 .5Z"
        /></svg
      >
      {info.repository.replace(/^https:\/\//, '')}
      <ExternalLink size={12} class="text-faint" />
    </a>
    <a class="btn" href="{info.repository}/releases" target="_blank" rel="noopener noreferrer">{t('about.releases')} <ExternalLink size={12} class="text-faint" /></a>
    <a class="btn" href="{info.repository}/issues" target="_blank" rel="noopener noreferrer">{t('about.issues')} <ExternalLink size={12} class="text-faint" /></a>
    <button class="btn" onclick={copy}>{#if copied}<Check size={13} />{:else}<Copy size={13} />{/if} {t('about.copy')}</button>
  </div>

  <p class="text-xs text-muted">{t('about.license')}</p>
{/if}
