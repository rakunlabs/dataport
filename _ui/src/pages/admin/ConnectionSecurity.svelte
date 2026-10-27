<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import type { DriverTarget } from '@/lib/api/types';

  // The transport security setting of the database type.
  let { options = $bindable(), dbType, target }: { options: Record<string, string>; dbType: string; target: DriverTarget } = $props();
</script>

{#if target !== 'network'}
  <span class="label">{t('admin.conn.location')}</span>
  <p class="flex h-7 items-center text-xs text-muted">{target === 'file' ? t('admin.conn.onServer') : t('admin.conn.viaOdbc')}</p>
{:else if dbType === 'postgres'}
  <label class="label" for="c-ssl">{t('admin.conn.sslMode')}</label>
  <select id="c-ssl" class="input" bind:value={options.sslmode}>
    <option value="">{t('admin.conn.default', { value: 'prefer' })}</option>
    {#each ['disable', 'allow', 'require', 'verify-ca', 'verify-full'] as m (m)}<option value={m}>{m}</option>{/each}
  </select>
{:else if dbType === 'mssql'}
  <label class="label" for="c-enc">{t('admin.conn.encryption')}</label>
  <div class="flex gap-2">
    <select id="c-enc" class="input" bind:value={options.encrypt}>
      <option value="">{t('admin.conn.encRequired')}</option>
      <option value="disable">{t('admin.conn.encDisabled')}</option>
      <option value="false">{t('admin.conn.encLogin')}</option>
      <option value="strict">{t('admin.conn.encStrict')}</option>
    </select>
    <label class="flex shrink-0 items-center gap-1.5 text-xs" title={t('admin.conn.trustCertHint')}>
      <input type="checkbox" checked={options.trust_server_certificate === 'true'}
        onchange={(e) => (options.trust_server_certificate = (e.target as HTMLInputElement).checked ? 'true' : '')} />
      {t('admin.conn.trustCert')}
    </label>
  </div>
{:else if dbType === 'oracle' || dbType === 'trino'}
  <label class="label" for="c-ossl">{t('admin.conn.tls')}</label>
  <select id="c-ossl" class="input" bind:value={options.ssl}>
    <option value="">{t('admin.conn.tlsOff')}</option>
    <option value="true">{t('admin.conn.tlsOn')}</option>
    <option value="skip-verify">{t('admin.conn.chSkip')}</option>
  </select>
{:else if dbType === 'clickhouse'}
  <label class="label" for="c-sec">{t('admin.conn.tlsNative')}</label>
  <select id="c-sec" class="input" bind:value={options.secure}>
    <option value="">{t('admin.conn.chOff')}</option>
    <option value="true">{t('admin.conn.chOn')}</option>
    <option value="skip-verify">{t('admin.conn.chSkip')}</option>
  </select>
{:else}
  <label class="label" for="c-tls">{t('admin.conn.tls')}</label>
  <select id="c-tls" class="input" bind:value={options.tls}>
    <option value="">{t('admin.conn.default', { value: 'preferred' })}</option>
    {#each ['false', 'true', 'skip-verify'] as m (m)}<option value={m}>{m}</option>{/each}
  </select>
{/if}
