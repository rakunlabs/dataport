<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { Info, RotateCcw, Variable } from 'lucide-svelte';
  import SqlEditor from '@/lib/components/editor/SqlEditor.svelte';
  import { fontFamilies, prefs, resetPrefs, savePrefs } from '@/lib/stores/prefs.svelte';
  import { editorOptions } from '@/lib/helper/editorOptions';
  import { placeholders, substitute } from '@/lib/helper/variables';

  // The preview shows what the settings do: statements with run buttons,
  // a placeholder and a declared variable.
  let sample = $state(`-- Preview: change a setting and see it here.
@set min_total = 100;

SELECT c.name, SUM(o.total) AS total
FROM customers c
JOIN orders o ON o.customer_id = c.id
WHERE o.created_at >= #{since}
GROUP BY c.name
HAVING SUM(o.total) > \${min_total}
ORDER BY total DESC;

SELECT COUNT(*) FROM orders;
`);

  const options = $derived(editorOptions(prefs));

  // What the first statement becomes when it runs, with a sample value for
  // what would be asked.
  const preview = $derived.by(() => {
    if (!prefs.variables) return '';
    const found = placeholders(sample, prefs.variableSyntaxes);
    const given = Object.fromEntries(found.filter((p) => p.value === undefined).map((p) => [p.name, `'2026-01-01'`]));
    return substitute(sample, prefs.variableSyntaxes, given);
  });

  const asked = $derived(prefs.variables ? placeholders(sample, prefs.variableSyntaxes).filter((p) => p.value === undefined) : []);

  const syntaxes: { key: keyof typeof prefs.variableSyntaxes; token: string; label: string }[] = [
    { key: 'atSet', token: '@set x = 1;  ${x}  @x', label: 'settings.syntaxAtSet' },
    { key: 'shell', token: '${name}', label: 'settings.syntaxShell' },
    { key: 'mybatis', token: '#{name}', label: 'settings.syntaxMybatis' },
    { key: 'named', token: ':name', label: 'settings.syntaxNamed' },
  ];

  function change() {
    savePrefs();
  }
</script>

<div class="flex items-center justify-between">
  <h2 class="text-base font-semibold">{t('settings.editor')}</h2>
  <button class="btn" onclick={resetPrefs}><RotateCcw size={13} /> {t('settings.reset')}</button>
</div>

<div class="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]">
  <div class="space-y-6">
    <section class="space-y-3">
      <h3 class="text-sm font-semibold">{t('settings.text')}</h3>
      <div class="grid grid-cols-2 gap-3">
        <div class="col-span-2">
          <label class="label" for="s-family">{t('settings.fontFamily')}</label>
          <select id="s-family" class="input" bind:value={prefs.editorFontFamily} onchange={change}>
            {#each fontFamilies as f (f.value)}<option value={f.value} style="font-family:{f.css}">{f.label}</option>{/each}
          </select>
        </div>
        <div>
          <label class="label" for="s-font">{t('settings.fontSize')}</label>
          <select id="s-font" class="input" bind:value={prefs.editorFontSize} onchange={change}>
            {#each [11, 12, 13, 14, 15, 16, 18, 20] as n (n)}<option value={n}>{n}px</option>{/each}
          </select>
        </div>
        <div>
          <label class="label" for="s-lh">{t('settings.lineHeight')}</label>
          <select id="s-lh" class="input" bind:value={prefs.lineHeight} onchange={change}>
            {#each [1.3, 1.45, 1.55, 1.7, 1.9] as n (n)}<option value={n}>{n}</option>{/each}
          </select>
        </div>
        <div>
          <label class="label" for="s-tab">{t('settings.tabSize')}</label>
          <select id="s-tab" class="input" bind:value={prefs.tabSize} onchange={change}>
            {#each [2, 4, 8] as n (n)}<option value={n}>{n}</option>{/each}
          </select>
        </div>
      </div>
    </section>

    <section class="space-y-2.5">
      <h3 class="text-sm font-semibold">{t('settings.behaviour')}</h3>
      {#each [
        { key: 'lineNumbers', label: 'settings.lineNumbers' },
        { key: 'wrap', label: 'settings.wrap' },
        { key: 'highlightStatement', label: 'settings.highlightStatement' },
        { key: 'closeBrackets', label: 'settings.closeBrackets' },
      ] as o (o.key)}
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" bind:checked={prefs[o.key as 'wrap']} onchange={change} />
          {t(o.label)}
        </label>
      {/each}
      <label class="flex items-start gap-2 text-sm">
        <input type="checkbox" class="mt-0.5" bind:checked={prefs.vim} onchange={change} />
        <span>
          <span class="block">{t('settings.vim')}</span>
          <span class="block text-xs text-muted">{t('settings.vimHint')}</span>
        </span>
      </label>
    </section>

    <section class="space-y-2.5">
      <h3 class="text-sm font-semibold">{t('settings.running')}</h3>
      <label class="flex items-start gap-2 text-sm">
        <input type="checkbox" class="mt-0.5" bind:checked={prefs.statementRun} onchange={change} />
        <span>
          <span class="block">{t('settings.statementRun')}</span>
          <span class="block text-xs text-muted">{t('settings.statementRunHint')}</span>
        </span>
      </label>
      <label class="flex items-start gap-2 text-sm">
        <input type="checkbox" class="mt-0.5" bind:checked={prefs.blankLineSplits} onchange={change} />
        <span>
          <span class="block">{t('settings.blankLineSplits')}</span>
          <span class="block text-xs text-muted">{t('settings.blankLineSplitsHint')}</span>
        </span>
      </label>
      <label class="flex items-start gap-2 text-sm">
        <input type="checkbox" class="mt-0.5" bind:checked={prefs.resultInNewTab} onchange={change} />
        <span>
          <span class="block">{t('settings.resultInNewTab')}</span>
          <span class="block text-xs text-muted">{t('settings.resultInNewTabHint')}</span>
        </span>
      </label>
    </section>

    <section class="space-y-2.5">
      <h3 class="text-sm font-semibold">{t('settings.variables')}</h3>
      <p class="text-xs text-muted">{t('settings.variablesHint')}</p>
      <label class="flex items-center gap-2 text-sm">
        <input type="checkbox" bind:checked={prefs.variables} onchange={change} />
        {t('settings.variablesEnabled')}
      </label>
      <div class="divide-y divide-line-soft rounded-md border border-line {prefs.variables ? '' : 'opacity-50'}">
        {#each syntaxes as sx (sx.key)}
          <label class="flex items-center gap-3 px-3 py-2 text-sm">
            <input type="checkbox" bind:checked={prefs.variableSyntaxes[sx.key]} onchange={change} disabled={!prefs.variables} />
            <span class="min-w-0 flex-1">{t(sx.label)}</span>
            <code class="shrink-0 rounded bg-sunken px-1.5 py-0.5 font-mono text-xs text-type">{sx.token}</code>
          </label>
        {/each}
      </div>
      <p class="flex items-start gap-1.5 text-xs text-muted"><Info size={13} class="mt-0.5 shrink-0" /> {t('settings.namedHint')}</p>
    </section>

    <p class="flex items-start gap-1.5 text-xs text-muted"><Info size={13} class="mt-0.5 shrink-0" /> {t('settings.localHint')}</p>
  </div>

  <div class="space-y-3 xl:sticky xl:top-0 xl:self-start">
    <h3 class="text-sm font-semibold">{t('settings.preview')}</h3>
    <div class="h-80 overflow-hidden rounded-md border border-line">
      {#key prefs.vim}
        <SqlEditor value={sample} dbType="postgres" {options} onchange={(v) => (sample = v)} onrunstatement={prefs.statementRun ? () => {} : undefined} />
      {/key}
    </div>
    {#if prefs.variables}
      <div>
        <p class="mb-1 flex items-center gap-1.5 text-xs text-muted">
          <Variable size={12} class="text-type" />
          {asked.length ? t('settings.previewAsks', { names: asked.map((p) => p.token).join(', ') }) : t('settings.previewNoAsk')}
        </p>
        <pre class="max-h-56 overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs whitespace-pre-wrap" style="font-family:{options.fontFamily}">{preview}</pre>
      </div>
    {/if}
  </div>
</div>
