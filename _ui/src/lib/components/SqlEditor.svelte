<script lang="ts">
  import { t as tr } from '@/lib/i18n/index.svelte';
  import { onDestroy, onMount } from 'svelte';
  import { EditorState, Compartment, StateField, RangeSet, type Extension } from '@codemirror/state';
  import {
    EditorView,
    keymap,
    lineNumbers,
    highlightActiveLine,
    highlightActiveLineGutter,
    drawSelection,
    placeholder,
    gutter,
    GutterMarker,
    Decoration,
    type DecorationSet,
  } from '@codemirror/view';
  import { statements, type Statement } from '@/lib/helper/statements';
  import { vim } from '@replit/codemirror-vim';
  import { indentUnit } from '@codemirror/language';
  import { MatchDecorator, ViewPlugin, type ViewUpdate } from '@codemirror/view';
  import { defaultOptions, type EditorOptions } from '@/lib/helper/editorOptions';
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
  import { sql, PostgreSQL, MySQL, MariaSQL, MSSQL, PLSQL, SQLite, StandardSQL, type SQLNamespace } from '@codemirror/lang-sql';
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
    onrunstatement,
    readonly = false,
    options = {},
  }: {
    value: string;
    dbType: string;
    schema?: SQLNamespace;
    onchange?: (v: string) => void;
    // append is set for Ctrl+\\, which keeps the results already shown.
    onrun?: (sql: string, append?: boolean) => void;
    onsave?: () => void;
    // Set to show a run button next to every statement. The event is the
    // click, so the caller can offer a menu.
    onrunstatement?: (sql: string, e: MouseEvent) => void;
    readonly?: boolean;
    // Display preferences; missing ones use the defaults.
    options?: Partial<EditorOptions>;
  } = $props();

  const opts = $derived<EditorOptions>({ ...defaultOptions, ...options });

  let host: HTMLDivElement;
  let view: EditorView | undefined;
  const lang = new Compartment();
  const runGutter = new Compartment();
  const look = new Compartment();
  const behaviour = new Compartment();

  // ////////////////////////////////////////////
  // run buttons next to statements

  class RunMarker extends GutterMarker {
    stmt: Statement;

    constructor(stmt: Statement) {
      super();
      this.stmt = stmt;
    }

    eq(other: RunMarker) {
      return other.stmt.from === this.stmt.from && other.stmt.text === this.stmt.text;
    }

    toDOM() {
      const b = document.createElement('button');
      b.className = 'cm-run-stmt';
      b.type = 'button';
      b.title = tr('editor.runStatement');
      b.setAttribute('aria-label', tr('editor.runStatement'));
      b.innerHTML = '<svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M6 4l15 8-15 8z"/></svg>';
      return b;
    }
  }

  // The statements of the document, recomputed when it changes.
  const stmtField = StateField.define<Statement[]>({
    create: (st) => statements(st.doc.toString(), dbType),
    update: (v, tr) => (tr.docChanged ? statements(tr.state.doc.toString(), dbType) : v),
  });

  // The statement under the cursor is outlined, so it is clear what a
  // button or Ctrl+Enter without a selection runs.
  const activeMark = Decoration.mark({ class: 'cm-active-stmt' });
  const activeField = StateField.define<DecorationSet>({
    create: () => Decoration.none,
    update: (_, tr) => {
      const list = tr.state.field(stmtField);
      if (list.length < 2 || !opts.highlightStatement) return Decoration.none;
      const pos = tr.state.selection.main.head;
      const s = list.find((x) => pos >= x.from && pos <= x.to + 1);
      return s ? Decoration.set([activeMark.range(s.from, s.to)]) : Decoration.none;
    },
    provide: (f) => EditorView.decorations.from(f),
  });

  function runExtension(): Extension {
    if (!onrunstatement) return [];
    return [
      stmtField,
      activeField,
      gutter({
        class: 'cm-run-gutter',
        markers: (v) => {
          const list = v.state.field(stmtField);
          // One button per line: the first statement that starts there.
          const seen = new Set<number>();
          const marks = [];
          for (const s of list) {
            const at = v.state.doc.lineAt(s.from).from;
            if (seen.has(at)) continue;
            seen.add(at);
            marks.push(new RunMarker(s).range(at));
          }
          return RangeSet.of(marks, true);
        },
        domEventHandlers: {
          mousedown: (v, line, e) => {
            const target = e.target as HTMLElement;
            if (!target.closest('.cm-run-stmt')) return false;
            const s = v.state.field(stmtField).find((x) => v.state.doc.lineAt(x.from).from === line.from);
            if (s) {
              e.preventDefault();
              onrunstatement?.(s.text, e as MouseEvent);
            }
            return true;
          },
          contextmenu: (_v, _line, e) => {
            if (!(e.target as HTMLElement).closest('.cm-run-stmt')) return false;
            e.preventDefault();
            return true;
          },
        },
      }),
    ];
  }

  function lookExtension(): Extension {
    const o = opts;
    return [
      EditorView.theme({
        '&': { fontSize: o.fontSize + 'px' },
        '.cm-scroller': { fontFamily: o.fontFamily, lineHeight: Math.round(o.fontSize * o.lineHeight) + 'px' },
      }),
      o.wrap ? EditorView.lineWrapping : [],
      o.lineNumbers ? [lineNumbers(), highlightActiveLineGutter()] : [],
      EditorState.tabSize.of(o.tabSize),
      indentUnit.of(' '.repeat(o.tabSize)),
      o.placeholders ? placeholderMarks : [],
    ];
  }

  // Vim keys go first so they see every key; closing brackets are optional.
  function behaviourExtension(): Extension {
    return [opts.vim ? vim() : [], opts.closeBrackets ? [closeBrackets(), keymap.of(closeBracketsKeymap)] : []];
  }

  // Placeholders the run fills in (${name}, #{name}, @set) are marked, so
  // it is clear what will be asked for.
  const placeholderMark = Decoration.mark({ class: 'cm-placeholder-var' });
  const placeholderMatcher = new MatchDecorator({
    regexp: /\$\{\s*[\p{L}_][\p{L}\p{N}_]*\s*\}|#\{\s*[\p{L}_][\p{L}\p{N}_]*\s*\}|@set\b/giu,
    decoration: () => placeholderMark,
  });
  const placeholderMarks = ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      constructor(v: EditorView) {
        this.decorations = placeholderMatcher.createDeco(v);
      }
      update(u: ViewUpdate) {
        this.decorations = placeholderMatcher.updateDeco(u, this.decorations);
      }
    },
    { decorations: (p) => p.decorations },
  );

  // statementAt returns the statement under the cursor, for Ctrl+Enter
  // without a selection when the script holds several.
  function statementAt(v: EditorView): string | undefined {
    const list = statements(v.state.doc.toString(), dbType);
    if (list.length < 2) return undefined;
    const pos = v.state.selection.main.head;
    return list.find((x) => pos >= x.from && pos <= x.to + 1)?.text;
  }

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
    '.cm-scroller': { lineHeight: '20px' },
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
    '.cm-run-gutter .cm-gutterElement': { display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '0 0 0 6px' },
    '.cm-run-stmt': {
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      width: '16px',
      height: '16px',
      borderRadius: '3px',
      color: 'var(--color-ok)',
      cursor: 'pointer',
      opacity: '0.75',
    },
    '.cm-run-stmt:hover': { opacity: '1', backgroundColor: 'var(--color-ok-soft)' },
    '.cm-active-stmt': { backgroundColor: 'var(--cm-active-stmt)' },
    '.cm-placeholder-var': { color: 'var(--cm-type)', backgroundColor: 'var(--cm-match)', borderRadius: '2px' },
    '.cm-fat-cursor': { backgroundColor: 'var(--color-accent) !important', color: 'var(--color-accent-fg) !important' },
    '.cm-vim-panel': { fontFamily: 'var(--font-mono)', fontSize: '12px', padding: '0 6px' },
  });

  function dialect(type: string) {
    if (type === 'mysql' || type === 'clickhouse') return MySQL;
    if (type === 'mariadb') return MariaSQL;
    if (type === 'mssql') return MSSQL;
    if (type === 'oracle') return PLSQL;
    if (type === 'sqlite') return SQLite;
    if (type === 'trino' || type === 'odbc') return StandardSQL;
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

  // Ctrl+\\ runs like Ctrl+Enter into a new result, as in DBeaver.
  function runAppend(v: EditorView) {
    const sel = v.state.selection.main;
    const text = sel.empty ? v.state.doc.toString() : v.state.sliceDoc(sel.from, sel.to);
    onrun?.(text, true);
    return true;
  }

  // Ctrl+Shift+Enter runs only the statement under the cursor.
  function runCurrent(v: EditorView) {
    const sel = v.state.selection.main;
    const text = sel.empty ? (statementAt(v) ?? v.state.doc.toString()) : v.state.sliceDoc(sel.from, sel.to);
    onrun?.(text);
    return true;
  }

  onMount(() => {
    view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: value,
        extensions: [
          behaviour.of(behaviourExtension()),
          runGutter.of(runExtension()),
          highlightActiveLine(),
          drawSelection(),
          history(),
          indentOnInput(),
          bracketMatching(),
          autocompletion(),
          highlightSelectionMatches(),
          placeholder(tr('query.placeholder')),
          keymap.of([
            { key: 'Mod-Shift-Enter', run: runCurrent },
            { key: 'Mod-Enter', run: runSelection },
            { key: 'Mod-\\', run: runAppend },
            { key: 'Shift-Enter', run: runSelection },
            {
              key: 'Mod-s',
              run: () => {
                onsave?.();
                return true;
              },
            },
            ...defaultKeymap,
            ...searchKeymap,
            ...historyKeymap,
            ...completionKeymap,
            indentWithTab,
          ]),
          lang.of(langExt()),
          syntaxHighlighting(highlight),
          editorTheme,
          look.of(lookExtension()),
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
    view?.dispatch({ effects: [lang.reconfigure(langExt()), runGutter.reconfigure(runExtension())] });
  });

  $effect(() => {
    void opts;
    view?.dispatch({ effects: [look.reconfigure(lookExtension()), behaviour.reconfigure(behaviourExtension())] });
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
    /* Translucent: the selection is drawn beneath the lines. */
    --cm-active-line: #2f6be60a;
    --cm-selection: #a6d2ff;
    --cm-bracket: #93d9d9;
    --cm-match: #edebfc;
    --cm-active-stmt: #2f6be60d;
  }

  :global(.dark) .editor {
    --cm-active-stmt: #3574f014;
    --cm-keyword: #cf8e6d;
    --cm-string: #6aab73;
    --cm-number: #2aacb8;
    --cm-comment: #7a7e85;
    --cm-type: #c77dbb;
    --cm-punct: #bcbec4;
    --cm-ident: #bcbec4;
    --cm-active-line: #ffffff0a;
    --cm-selection: #214283;
    --cm-bracket: #43454a;
    --cm-match: #373b39;
  }
</style>
