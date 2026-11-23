<script lang="ts" module>
  import { defaultListOptions, type ListOptions } from '@/lib/helper/textMorph';

  const storeKey = 'dataport.delimitedList';

  function load(): ListOptions {
    try {
      return { ...defaultListOptions, ...JSON.parse(localStorage.getItem(storeKey) ?? '{}') };
    } catch {
      return { ...defaultListOptions };
    }
  }

  function keep(o: ListOptions) {
    try {
      localStorage.setItem(storeKey, JSON.stringify(o));
    } catch {
      // The choice then lasts for this page only.
    }
  }
</script>

<script lang="ts">
  // The selection made into a delimited list, as DBeaver's "Morph to
  // delimited list": each item quoted and joined, with a live preview.
  // The choices are remembered in this browser.
  import Modal from '@/lib/components/ui/Modal.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { listItems, morphList } from '@/lib/helper/textMorph';

  let { text, onapply, onclose }: { text: string; onapply: (out: string) => void; onclose: () => void } = $props();

  let o = $state<ListOptions>(load());

  // The split characters offered as boxes; the rest are typed in.
  const known = [
    { ch: '\n', key: 'newline' },
    { ch: '\t', key: 'tab' },
    { ch: ',', key: 'comma' },
    { ch: ';', key: 'semicolon' },
    { ch: ' ', key: 'space' },
  ] as const;
  let other = $state([...o.split].filter((c) => !known.some((k) => k.ch === c)).join(''));

  function toggle(ch: string, on: boolean) {
    const rest = [...o.split].filter((c) => c !== ch).join('');
    o.split = on ? rest + ch : rest;
  }

  const split = $derived([...o.split].filter((c) => known.some((k) => k.ch === c)).join('') + other);
  const opts = $derived({ ...o, split, wrap: Math.max(0, Math.floor(Number(o.wrap) || 0)) });
  const count = $derived(listItems(text, split).length);
  const out = $derived(morphList(text, opts));

  // Ready-made shapes for the common cases.
  const presets: { key: string; set: Partial<ListOptions> }[] = [
    { key: 'inList', set: { quote: "'", join: ', ', prefix: '(', suffix: ')' } },
    { key: 'numbers', set: { quote: '', join: ', ', prefix: '(', suffix: ')' } },
    { key: 'idents', set: { quote: '"', join: ', ', prefix: '', suffix: '' } },
    { key: 'lines', set: { quote: "'", join: ',\n', prefix: '', suffix: '' } },
  ];

  function apply() {
    // Ctrl+Enter follows the button: nothing to make is nothing to apply.
    if (!count) return;
    keep(opts);
    onapply(out);
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      apply();
    }
  }
</script>

<svelte:window {onkeydown} />

<Modal title={t('morph.title')} {onclose} width="max-w-2xl">
  <div class="mb-3 flex flex-wrap gap-1.5">
    {#each presets as p (p.key)}
      <button class="btn h-7 text-xs" onclick={() => Object.assign(o, p.set)}>{t(`morph.preset.${p.key}`)}</button>
    {/each}
  </div>

  <div class="grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
    <div class="col-span-2">
      <span class="label">{t('morph.split')}</span>
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-xs">
        {#each known as k (k.key)}
          <label class="flex items-center gap-1.5">
            <input type="checkbox" checked={o.split.includes(k.ch)} onchange={(e) => toggle(k.ch, e.currentTarget.checked)} />
            {t(`morph.char.${k.key}`)}
          </label>
        {/each}
        <label class="flex items-center gap-1.5">
          {t('morph.other')}
          <input class="input h-7 w-20 font-mono text-xs" bind:value={other} />
        </label>
      </div>
    </div>

    <label>
      <span class="label">{t('morph.quote')}</span>
      <select class="input h-8 w-full text-xs" bind:value={o.quote}>
        <option value="'">' {t('morph.single')}</option>
        <option value={'"'}>" {t('morph.double')}</option>
        <option value="`">` {t('morph.backtick')}</option>
        <option value="">{t('morph.none')}</option>
      </select>
    </label>
    <label>
      <span class="label">{t('morph.join')}</span>
      <input class="input h-8 w-full font-mono text-xs" value={o.join.replace(/\n/g, '\\n')} oninput={(e) => (o.join = e.currentTarget.value.replace(/\\n/g, '\n'))} />
    </label>
    <label>
      <span class="label">{t('morph.prefix')}</span>
      <input class="input h-8 w-full font-mono text-xs" bind:value={o.prefix} />
    </label>
    <label>
      <span class="label">{t('morph.suffix')}</span>
      <input class="input h-8 w-full font-mono text-xs" bind:value={o.suffix} />
    </label>
    <label>
      <span class="label">{t('morph.wrap')}</span>
      <input class="input h-8 w-full text-xs" type="number" min="0" step="10" bind:value={o.wrap} />
    </label>
    <div class="flex flex-col justify-end gap-1.5 text-xs">
      <label class="flex items-center gap-1.5"><input type="checkbox" bind:checked={o.escape} disabled={!o.quote} /> {t('morph.escape')}</label>
      <label class="flex items-center gap-1.5"><input type="checkbox" bind:checked={o.unique} /> {t('morph.unique')}</label>
    </div>
  </div>

  <div class="mt-4">
    <span class="label">{t('morph.preview', { count })}</span>
    <pre class="max-h-48 overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs whitespace-pre-wrap break-all">{out}</pre>
  </div>

  {#snippet footer()}
    <button class="btn" onclick={onclose}>{t('common.cancel')}</button>
    <button class="btn-primary" onclick={apply} disabled={!count} title="Ctrl+Enter">{t('morph.apply')}</button>
  {/snippet}
</Modal>
