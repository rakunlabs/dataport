<script lang="ts">
  import { t as tr } from '@/lib/i18n/index.svelte';
  import { onDestroy, onMount } from 'svelte';
  import { EditorState, Compartment } from '@codemirror/state';
  import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection, placeholder } from '@codemirror/view';
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
  import { sql, PostgreSQL, MySQL, MariaSQL, type SQLNamespace } from '@codemirror/lang-sql';
  import { syntaxHighlighting, HighlightStyle, bracketMatching, indentOnInput } from '@codemirror/language';
  import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap } from '@codemirror/autocomplete';
  import { highlightSelectionMatches, searchKeymap } from '@codemirror/search';
  import { tags as t } from '@lezer/highlight';

  let {
    value,
    dbType,
    schema = {},
    onchange,
    onrun,
    onsave,
    readonly = false,
  }: {
    value: string;
    dbType: string;
    schema?: SQLNamespace;
    onchange?: (v: string) => void;
    onrun?: (sql: string) => void;
    onsave?: () => void;
    readonly?: boolean;
  } = $props();

  let host: HTMLDivElement;
  let view: EditorView | undefined;
  const lang = new Compartment();

  const highlight = HighlightStyle.define([
    { tag: t.keyword, color: 'var(--cm-keyword)' },
    { tag: [t.string, t.special(t.string)], color: 'var(--cm-string)' },
    { tag: [t.number, t.bool, t.null], color: 'var(--cm-number)' },
    { tag: [t.lineComment, t.blockComment], color: 'var(--cm-comment)' },
    { tag: [t.typeName, t.standard(t.name)], color: 'var(--cm-type)', fontStyle: 'italic' },
    { tag: [t.operator, t.punctuation], color: 'var(--cm-punct)' },
    { tag: t.special(t.name), color: 'var(--cm-ident)' },
  ]);

  const editorTheme = EditorView.theme({
    '&': { height: '100%', fontSize: '13px', backgroundColor: 'var(--color-bg)', color: 'var(--color-fg)' },
    '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '20px' },
    '.cm-content': { caretColor: 'var(--color-accent)', padding: '6px 0' },
    '.cm-gutters': { backgroundColor: 'var(--color-bg)', color: 'var(--color-faint)', border: 'none', paddingRight: '4px' },
    '.cm-lineNumbers .cm-gutterElement': { padding: '0 8px 0 14px', minWidth: '40px' },
    '.cm-activeLineGutter': { backgroundColor: 'transparent', color: 'var(--color-fg-2)' },
    '.cm-activeLine': { backgroundColor: 'var(--cm-active-line)' },
    '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': { backgroundColor: 'var(--cm-selection) !important' },
    '.cm-cursor': { borderLeftColor: 'var(--color-fg)', borderLeftWidth: '2px' },
    '&.cm-focused': { outline: 'none' },
    '.cm-tooltip': {
      backgroundColor: 'var(--color-raised)',
      border: '1px solid var(--color-line)',
      borderRadius: '6px',
      boxShadow: 'var(--shadow-pop)',
      overflow: 'hidden',
    },
    '.cm-tooltip-autocomplete ul': { fontFamily: 'var(--font-mono)', fontSize: '12px' },
    '.cm-tooltip-autocomplete ul li': { padding: '2px 8px !important', lineHeight: '20px' },
    '.cm-tooltip-autocomplete ul li[aria-selected]': { backgroundColor: 'var(--color-accent)', color: 'var(--color-accent-fg)' },
    '.cm-completionDetail': { color: 'var(--color-faint)', fontStyle: 'normal', marginLeft: '12px' },
    '.cm-matchingBracket': { backgroundColor: 'var(--cm-bracket)', outline: 'none', color: 'inherit !important' },
    '.cm-selectionMatch': { backgroundColor: 'var(--cm-match)' },
    '.cm-placeholder': { color: 'var(--color-faint)' },
    '.cm-panels': { backgroundColor: 'var(--color-panel)', color: 'var(--color-fg)', borderColor: 'var(--color-line)' },
    '.cm-search input, .cm-search button': { fontSize: '12px' },
  });

  function dialect(type: string) {
    if (type === 'mysql') return MySQL;
    if (type === 'mariadb') return MariaSQL;
    return PostgreSQL;
  }

  function langExt() {
    return sql({ dialect: dialect(dbType), schema, upperCaseKeywords: true });
  }

  // Run the selection if there is one, otherwise the whole editor.
  function runSelection(v: EditorView) {
    const sel = v.state.selection.main;
    const text = sel.empty ? v.state.doc.toString() : v.state.sliceDoc(sel.from, sel.to);
    onrun?.(text);
    return true;
  }

  onMount(() => {
    view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          highlightActiveLineGutter(),
          highlightActiveLine(),
          drawSelection(),
          history(),
          indentOnInput(),
          bracketMatching(),
          closeBrackets(),
          autocompletion(),
          highlightSelectionMatches(),
          placeholder(tr('query.placeholder')),
          keymap.of([
            { key: 'Mod-Enter', run: runSelection },
            { key: 'Shift-Enter', run: runSelection },
            {
              key: 'Mod-s',
              run: () => {
                onsave?.();
                return true;
              },
            },
            ...closeBracketsKeymap,
            ...defaultKeymap,
            ...searchKeymap,
            ...historyKeymap,
            ...completionKeymap,
            indentWithTab,
          ]),
          lang.of(langExt()),
          syntaxHighlighting(highlight),
          editorTheme,
          EditorState.readOnly.of(readonly),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) onchange?.(u.state.doc.toString());
          }),
        ],
      }),
    });
  });

  // Keep the editor in sync when the value changes outside of it.
  $effect(() => {
    const v = value;
    if (view && v !== view.state.doc.toString()) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } });
    }
  });

  $effect(() => {
    void dbType;
    void schema;
    view?.dispatch({ effects: lang.reconfigure(langExt()) });
  });

  onDestroy(() => view?.destroy());

  export function focus() {
    view?.focus();
  }

  export function currentSQL(): string {
    if (!view) return value;
    const sel = view.state.selection.main;
    return sel.empty ? view.state.doc.toString() : view.state.sliceDoc(sel.from, sel.to);
  }
</script>

<div class="editor h-full min-h-0" bind:this={host}></div>

<style>
  /* Syntax colours after the JetBrains Light and Darcula schemes. */
  .editor {
    --cm-keyword: #0033b3;
    --cm-string: #067d17;
    --cm-number: #1750eb;
    --cm-comment: #8c8c8c;
    --cm-type: #871094;
    --cm-punct: #080808;
    --cm-ident: #000000;
    --cm-active-line: #f5f8fe;
    --cm-selection: #a6d2ff;
    --cm-bracket: #93d9d9;
    --cm-match: #edebfc;
  }

  :global(.dark) .editor {
    --cm-keyword: #cf8e6d;
    --cm-string: #6aab73;
    --cm-number: #2aacb8;
    --cm-comment: #7a7e85;
    --cm-type: #c77dbb;
    --cm-punct: #bcbec4;
    --cm-ident: #bcbec4;
    --cm-active-line: #26282e;
    --cm-selection: #214283;
    --cm-bracket: #43454a;
    --cm-match: #373b39;
  }
</style>
