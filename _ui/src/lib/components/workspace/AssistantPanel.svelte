<script lang="ts">
  import { tick } from 'svelte';
  import { t, i18n, fmtDateTime } from '@/lib/i18n/index.svelte';
  import { X, ChevronDown, Plus, History, Send, Square, Trash2, LoaderCircle, Check, CircleX, Wrench, Copy, Replace, TextCursorInput, Sparkles, ChevronLeft, RotateCcw, Table2, ListTree, BookOpen, Network, FilePlus2, MessageCircleQuestion } from 'lucide-svelte';
  import {
    assistant,
    closeAssistant,
    minimizeAssistant,
    deleteAllConversations,
    deleteConversation,
    loadConversations,
    newConversation,
    openConversation,
    canRetry,
    retry,
    send,
    stop,
    type AIProposal,
    type AINavigate,
  } from '@/lib/stores/assistant.svelte';
  import { activeTab, ws, persist, workspaceConnections, openTableTab, openDocs, openDiagram, openQueryTab, loadSchemas } from '@/lib/stores/workspace.svelte';
  import { session } from '@/lib/stores/session.svelte';
  import { editorOf } from '@/lib/stores/editors.svelte';
  import { markdown } from '@/lib/helper/markdown';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { copyText } from '@/lib/helper/clipboard';

  let input = $state('');
  let scroller: HTMLDivElement | undefined = $state();
  let box: HTMLTextAreaElement | undefined = $state();

  const tab = $derived(activeTab());
  const canEdit = $derived(!!tab && (tab.kind === 'query' || tab.kind === 'notebook'));

  // Follow the answer while it is written, unless the user scrolled up.
  let follow = true;
  $effect(() => {
    void assistant.entries.length;
    const last = assistant.entries[assistant.entries.length - 1];
    void last?.text;
    void last?.steps.length;
    void last?.proposals.length;
    if (follow) void tick().then(() => scroller && (scroller.scrollTop = scroller.scrollHeight));
  });

  function onscroll() {
    if (scroller) follow = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 40;
  }

  async function submit(e?: Event) {
    e?.preventDefault();
    const text = input;
    if (!text.trim() || assistant.busy) return;
    input = '';
    follow = true;
    await send(text);
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      void submit();
    }
  }

  // apply puts a proposal into the editor of the tab it was made for, or
  // the open one.
  function apply(p: AIProposal, mode: 'replace' | 'insert' | 'statement') {
    const target = ws.tabs.find((x) => x.id === p.tabId && (x.kind === 'query' || x.kind === 'notebook')) ?? (canEdit ? tab : undefined);
    if (!target) {
      toast(t('assistant.noEditor'), 'info');
      return;
    }
    ws.activeId = target.id;
    const ed = editorOf(target.id);
    // A notebook's text is its cells: changes go into the cell's editor.
    const text = ed?.cell ? ed.cell.sql : target.sql;
    if (target.kind === 'notebook' && !ed) {
      toast(t('assistant.noEditor'), 'info');
      return;
    }
    // The statement is replaced where it was, if the user has not changed it since.
    if (mode === 'statement' && p.range && text.slice(p.range.from, p.range.to) === p.range.text && ed?.replaceRange(p.range.from, p.range.to, p.sql)) {
      // The editor reports the change itself.
    } else if (mode === 'statement' && p.range) {
      toast(t('assistant.statementMoved'), 'info');
      return;
    } else if (mode === 'insert' && ed?.insert(p.sql)) {
      // The editor reports the change itself.
    } else if (ed?.cell) {
      ed.replaceRange(0, text.length, p.sql);
    } else {
      target.sql = p.sql;
      target.dirty = true;
      persist();
    }
    p.state = 'applied';
  }

  async function remove(id: string, title: string) {
    if (!confirm(t('assistant.deleteConfirm', { title: title || t('assistant.untitled') }))) return;
    try {
      await deleteConversation(id);
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  async function removeAll() {
    if (!confirm(t('assistant.deleteAllConfirm'))) return;
    try {
      await deleteAllConversations();
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  async function open(id: string) {
    try {
      await openConversation(id);
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  // What the assistant will see from the editor, shown under the input.
  // Read when the panel redraws, which is often enough for a hint.
  let seen = $state<{ what: string; detail: string }>({ what: '', detail: '' });
  function readFocus() {
    const f = tab ? editorOf(tab.id)?.focus() : undefined;
    if (!f?.statement.trim()) seen = { what: tab?.result ? t('assistant.seesResult') : '', detail: '' };
    else seen = { what: f.selected ? t('assistant.seesSelection') : t('assistant.seesStatement', { line: f.line }), detail: f.statement.slice(0, 400) };
  }
  $effect(() => {
    void tab?.id;
    void tab?.sql;
    readFocus();
    const id = setInterval(readFocus, 1000);
    return () => clearInterval(id);
  });

  // When a message was written: the time today, the date before.
  function when(at: number) {
    if (!at) return '';
    const d = new Date(at);
    const today = d.toDateString() === new Date().toDateString();
    return today ? d.toLocaleTimeString(i18n.locale, { hour: '2-digit', minute: '2-digit' }) : d.toLocaleString(i18n.locale, { dateStyle: 'medium', timeStyle: 'short' });
  }

  function took(from: number, to?: number) {
    if (!from || !to || to < from) return '';
    const s = Math.round((to - from) / 1000);
    return s < 60 ? t('assistant.tookSeconds', { n: Math.max(1, s) }) : t('assistant.tookMinutes', { n: Math.floor(s / 60), s: s % 60 });
  }

  const navIcon = { table: Table2, structure: ListTree, docs: BookOpen, diagram: Network, query: FilePlus2 } as const;

  // go opens what the model offered, if the connection is still the user's.
  function go(l: AINavigate) {
    const c = workspaceConnections().find((x) => x.name === l.connection || x.id === l.connection) ?? workspaceConnections().find((x) => x.name.toLowerCase() === l.connection.toLowerCase());
    if (!c) {
      toast(t('assistant.noConnection', { name: l.connection }), 'error');
      return;
    }
    if ((l.target === 'docs' || l.target === 'diagram') && !l.schema) {
      toast(t('assistant.noSchema'), 'error');
      return;
    }
    void loadSchemas(c.id);
    switch (l.target) {
      case 'query':
        openQueryTab(c.id, l.sql ?? '');
        break;
      case 'docs':
        if (session.user?.docs_off) toast(t('assistant.docsOff'), 'info');
        else openDocs(c.id, l.schema!, l.table ?? '');
        break;
      case 'diagram':
        openDiagram(c.id, l.schema!);
        break;
      default:
        if (!l.table || !l.schema) {
          toast(t('assistant.noTable'), 'error');
          return;
        }
        openTableTab(c.id, l.schema, l.table, l.target === 'structure' ? 'structure' : 'data');
    }
  }

  function navLabel(l: AINavigate) {
    if (l.label) return l.label;
    const name = l.table ? `${l.schema ? l.schema + '.' : ''}${l.table}` : (l.schema ?? l.connection);
    return t(`assistant.nav.${l.target}`, { name });
  }

  function toolLabel(name: string) {
    const key = `assistant.tools.${name.replace(/^dataport_/, '')}`;
    const v = t(key);
    return v === key ? name.replace(/^dataport_/, '').replace(/_/g, ' ') : v;
  }

  function startList() {
    assistant.listing = true;
    void loadConversations();
  }

  $effect(() => {
    if (!assistant.listing) void tick().then(() => box?.focus());
  });
</script>

<aside class="flex h-full min-w-0 flex-col bg-panel" aria-label={t('assistant.title')}>
  <div class="pane-head items-center gap-1 pr-1.5 pl-3">
    {#if assistant.listing}
      <button class="icon-btn size-6 -ml-1.5" onclick={() => (assistant.listing = false)} aria-label={t('assistant.back')}><ChevronLeft size={14} /></button>
      <span class="pane-title flex-1">{t('assistant.conversations')}</span>
      {#if assistant.conversations.length}
        <button class="icon-btn size-6 hover:text-danger" onclick={removeAll} title={t('assistant.deleteAll')} aria-label={t('assistant.deleteAll')}><Trash2 size={13} /></button>
      {/if}
    {:else}
      <Sparkles size={13} class="text-accent" />
      <span class="pane-title flex-1">{t('assistant.title')}</span>
      <button class="icon-btn size-6" onclick={startList} title={t('assistant.conversations')} aria-label={t('assistant.conversations')}><History size={13} /></button>
      <button class="icon-btn size-6" onclick={newConversation} title={t('assistant.new')} aria-label={t('assistant.new')}><Plus size={14} /></button>
    {/if}
    <button class="icon-btn size-6" onclick={minimizeAssistant} title={t('assistant.minimize')} aria-label={t('assistant.minimize')}><ChevronDown size={14} /></button>
    <button class="icon-btn size-6" onclick={closeAssistant} title={t('common.close')} aria-label={t('common.close')}><X size={14} /></button>
  </div>

  {#if assistant.listing}
    <div class="min-h-0 flex-1 overflow-auto">
      {#each assistant.conversations as c (c.id)}
        <div class="group flex items-start gap-2 border-b border-line-soft px-3 py-2 hover:bg-hover {c.id === assistant.current ? 'bg-row-active' : ''}">
          <button class="min-w-0 flex-1 text-left" onclick={() => open(c.id)}>
            <div class="truncate text-sm">{c.title || t('assistant.untitled')}</div>
            <div class="text-2xs text-muted">{fmtDateTime(c.updated_at)}</div>
          </button>
          <button class="icon-btn size-6 opacity-0 group-hover:opacity-100 hover:text-danger focus:opacity-100" onclick={() => remove(c.id, c.title)} aria-label={t('common.delete')}><Trash2 size={13} /></button>
        </div>
      {:else}
        <p class="px-4 py-8 text-center text-xs text-muted">{t('assistant.noConversations')}</p>
      {/each}
      <p class="px-3 py-3 text-2xs text-faint">{t('assistant.keptHint')}</p>
    </div>
  {:else}
    <div class="min-h-0 min-w-0 flex-1 space-y-3 overflow-x-hidden overflow-y-auto px-3 py-3" bind:this={scroller} {onscroll}>
      {#if !assistant.entries.length}
        <div class="px-1 pt-6 text-center">
          <Sparkles size={20} class="mx-auto mb-2 text-accent" />
          <p class="text-sm font-medium">{t('assistant.emptyTitle')}</p>
          <p class="mt-1 text-xs text-muted">{t('assistant.emptyBody')}</p>
          <div class="mt-4 flex flex-col gap-1.5">
            {#each ['explain', 'fix', 'write'] as k (k)}
              <button class="rounded-md border border-line bg-bg px-2.5 py-1.5 text-left text-xs text-fg-2 hover:bg-hover" onclick={() => send(t(`assistant.suggest.${k}`))}>{t(`assistant.suggest.${k}`)}</button>
            {/each}
          </div>
        </div>
      {/if}

      {#each assistant.entries as e, i (i)}
        {#if e.role === 'user'}
          <div class="ml-6">
            <div class="rounded-lg bg-accent-soft px-3 py-2 text-sm whitespace-pre-wrap [overflow-wrap:anywhere]">{e.text}</div>
            {#if e.at}<div class="mt-0.5 text-right text-2xs text-faint tabular-nums" title={fmtDateTime(e.at)}>{when(e.at)}</div>{/if}
          </div>
        {:else}
          <div class="min-w-0 space-y-2 text-sm">
            {#if e.steps.length}
              <ul class="space-y-0.5">
                {#each e.steps as s, k (k)}
                  <li class="flex items-start gap-1.5 text-xs text-muted" title={s.arguments}>
                    {#if !s.done}<LoaderCircle size={11} class="mt-0.5 shrink-0 animate-spin" />{:else if s.error}<CircleX size={11} class="mt-0.5 shrink-0 text-danger" />{:else}<Wrench size={11} class="mt-0.5 shrink-0" />{/if}
                    <span class="min-w-0 [overflow-wrap:anywhere]">{toolLabel(s.name)}</span>
                  </li>
                {/each}
              </ul>
            {/if}

            {#if e.text}
              <div class="md min-w-0 space-y-2 break-words whitespace-normal [overflow-wrap:anywhere]">
                {#each markdown(e.text) as b, j (j)}
                  {#if b.kind === 'code'}
                    <div class="group relative">
                      <pre class="max-h-80 overflow-auto rounded-md border border-line bg-sunken p-2 pr-8 font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere]">{b.text}</pre>
                      <div class="absolute top-1 right-1 flex gap-0.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100">
                        {#if canEdit && (b.lang === 'sql' || b.lang === '')}
                          <button class="icon-btn size-6 bg-panel" onclick={() => apply({ id: '', sql: b.text, mode: 'insert', tabId: tab?.id ?? '', state: '' }, 'insert')} title={t('assistant.insert')} aria-label={t('assistant.insert')}><TextCursorInput size={12} /></button>
                        {/if}
                        <button class="icon-btn size-6 bg-panel" onclick={() => copyText(b.text)} title={t('common.copy')} aria-label={t('common.copy')}><Copy size={12} /></button>
                      </div>
                    </div>
                  {:else}
                    <!-- Escaped before formatting, in helper/markdown. -->
                    <!-- eslint-disable-next-line svelte/no-at-html-tags -->
                    {@html b.text}
                  {/if}
                {/each}
              </div>
            {:else if assistant.busy && i === assistant.entries.length - 1 && !e.error}
              <p class="flex items-center gap-1.5 text-xs text-muted"><LoaderCircle size={12} class="animate-spin" /> {t('assistant.thinking')}</p>
            {/if}

            {#each e.questions ?? [] as q, k (k)}
              <!-- The assistant asks before guessing; a choice answers it. -->
              <div class="rounded-md border border-accent/50 bg-bg p-2">
                <p class="flex items-start gap-1.5 text-sm [overflow-wrap:anywhere]"><MessageCircleQuestion size={14} class="mt-0.5 shrink-0 text-accent" /> {q.question}</p>
                {#if q.answered === undefined}
                  {#if q.choices.length}
                    <div class="mt-2 flex flex-wrap gap-1.5">
                      {#each q.choices as c, k (k)}
                        <button class="btn h-auto min-h-6 max-w-full py-0.5 text-left text-xs [overflow-wrap:anywhere]" onclick={() => send(c)} disabled={assistant.busy}>{c}</button>
                      {/each}
                    </div>
                  {/if}
                  <p class="mt-1.5 text-2xs text-faint">{t('assistant.orWrite')}</p>
                {:else}
                  <p class="mt-1 text-xs text-muted [overflow-wrap:anywhere]">→ {q.answered}</p>
                {/if}
              </div>
            {/each}

            {#if e.links.length}
              <div class="flex flex-wrap gap-1.5">
                {#each e.links as l, k (k)}
                  {@const Icon = navIcon[l.target]}
                  <button class="btn h-auto min-h-6 max-w-full py-0.5 text-left text-xs" onclick={() => go(l)} title={l.connection}><Icon size={12} class="shrink-0" /> <span class="[overflow-wrap:anywhere]">{navLabel(l)}</span></button>
                {/each}
              </div>
            {/if}

            {#each e.proposals as p, k (k)}
              <div class="rounded-md border border-accent/50 bg-bg">
                <div class="border-b border-line-soft px-2 py-1 text-xs">
                  <span class="font-medium text-accent">{t('assistant.proposal')}</span>
                  {#if p.explanation}<span class="text-muted [overflow-wrap:anywhere]"> · {p.explanation}</span>{/if}
                </div>
                <pre class="max-h-60 overflow-auto p-2 font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere]">{p.sql}</pre>
                <div class="flex flex-wrap items-center gap-1.5 border-t border-line-soft px-2 py-1.5">
                  {#if p.state === 'applied'}
                    <span class="flex items-center gap-1 text-xs text-ok"><Check size={12} /> {t('assistant.applied')}</span>
                  {:else if p.state === 'dismissed'}
                    <span class="text-xs text-muted">{t('assistant.dismissed')}</span>
                  {:else}
                    {#if p.mode === 'statement' && p.range}
                      <button class="btn-primary h-6 text-xs" onclick={() => apply(p, 'statement')} title={p.range.text}><Replace size={12} /> {t('assistant.replaceStatement')}</button>
                      <button class="btn h-6 text-xs" onclick={() => apply(p, 'replace')}>{t('assistant.replaceAll')}</button>
                    {:else}
                      <button class="btn-primary h-6 text-xs" onclick={() => apply(p, 'replace')}><Replace size={12} /> {t('assistant.replace')}</button>
                    {/if}
                    <button class="btn h-6 text-xs" onclick={() => apply(p, 'insert')}><TextCursorInput size={12} /> {t('assistant.insert')}</button>
                    <button class="btn h-6 text-xs" onclick={() => (p.state = 'dismissed')}>{t('assistant.dismiss')}</button>
                  {/if}
                  <button class="icon-btn ml-auto size-6" onclick={() => copyText(p.sql)} aria-label={t('common.copy')}><Copy size={12} /></button>
                </div>
              </div>
            {/each}

            {#if e.error}<p class="rounded-md bg-danger-soft px-2.5 py-1.5 text-xs text-danger [overflow-wrap:anywhere]" role="alert">{e.error}</p>{/if}

            {#if e.doneAt && !(assistant.busy && i === assistant.entries.length - 1)}
              <div class="text-2xs text-faint tabular-nums" title={fmtDateTime(e.doneAt)}>
                {t('assistant.answeredAt', { time: when(e.doneAt) })}{#if took(assistant.entries[i - 1]?.at ?? e.at, e.doneAt)} · {took(assistant.entries[i - 1]?.at ?? e.at, e.doneAt)}{/if}
              </div>
            {/if}

            {#if i === assistant.entries.length - 1 && canRetry()}
              <!-- The last answer can be written again, with the tab as it is now. -->
              <div class="flex items-center gap-1">
                <button class="btn h-6 text-xs {e.error ? '' : 'text-muted'}" onclick={() => retry()} title={t('assistant.retryHint')}>
                  <RotateCcw size={12} /> {t('assistant.retry')}
                </button>
                {#if e.text}
                  <button class="icon-btn size-6" onclick={() => copyText(e.text)} title={t('common.copy')} aria-label={t('common.copy')}><Copy size={12} /></button>
                {/if}
              </div>
            {/if}
          </div>
        {/if}
      {/each}
    </div>

    <form class="border-t border-line p-2" onsubmit={submit}>
      {#if tab}
        <p class="mb-1 flex items-center gap-1 truncate px-0.5 text-2xs text-faint" title={seen.detail}>
          <span class="truncate">{t('assistant.context', { tab: tab.title || t('settings.title') })}</span>
          {#if seen.what}<span class="shrink-0">· {seen.what}</span>{/if}
        </p>
      {/if}
      <div class="relative">
        <textarea
          bind:this={box}
          bind:value={input}
          {onkeydown}
          rows="3"
          class="input h-auto min-h-16 resize-none py-1.5 pr-9 text-sm"
          placeholder={t('assistant.placeholder')}
          aria-label={t('assistant.placeholder')}
        ></textarea>
        {#if assistant.busy}
          <button type="button" class="icon-btn absolute right-1.5 bottom-1.5 size-7" onclick={stop} title={t('assistant.stop')} aria-label={t('assistant.stop')}><Square size={13} /></button>
        {:else}
          <button type="submit" class="icon-btn absolute right-1.5 bottom-1.5 size-7 text-accent" disabled={!input.trim()} title={t('assistant.send')} aria-label={t('assistant.send')}><Send size={14} /></button>
        {/if}
      </div>
      <p class="mt-1 px-0.5 text-2xs text-faint">{t('assistant.readOnlyHint')}</p>
    </form>
  {/if}
</aside>

<style>
  .md :global(p),
  .md :global(li) {
    line-height: 1.5;
    white-space: normal;
    overflow-wrap: anywhere;
  }
  .md :global(code) {
    font-family: var(--font-mono);
    font-size: 0.85em;
    padding: 0 0.25em;
    border-radius: 3px;
    background: var(--color-sunken);
  }
  .md :global(ul) {
    list-style: disc;
    padding-left: 1.2em;
  }
  .md :global(ol) {
    list-style: decimal;
    padding-left: 1.4em;
  }
  .md :global(h4) {
    font-weight: 600;
  }
  .md :global(a) {
    color: var(--color-accent);
    text-decoration: underline;
  }
</style>
