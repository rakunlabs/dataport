<script lang="ts">
  import { t as tr } from '@/lib/i18n/index.svelte';
  import { onDestroy, onMount } from 'svelte';
  import { EditorState, EditorSelection, Compartment, type Extension } from '@codemirror/state';
  import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection, placeholder } from '@codemirror/view';
  import { statementAt, statements, type Statement } from '@/lib/helper/statements';

  type Span = { from: number; to: number };
  import type { ResultSet } from '@/lib/api/types';
  import { vim } from '@replit/codemirror-vim';
  import { indentUnit } from '@codemirror/language';
  import { defaultOptions, type EditorOptions } from '@/lib/helper/editorOptions';
  import { formatSql } from '@/lib/helper/formatSql';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
  import { sql, schemaCompletionSource, type SQLNamespace } from '@codemirror/lang-sql';
  import type { CompletionContext } from '@codemirror/autocomplete';
  import { quoteIdent } from '@/lib/helper/copy';
  import { syntaxHighlighting, bracketMatching, indentOnInput } from '@codemirror/language';
  import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap } from '@codemirror/autocomplete';
  import { highlightSelectionMatches, searchKeymap } from '@codemirror/search';
  import { catalogCompletion, dialect, editorTheme, highlight, placeholderMarks, runGutterExtension, setRunStatus, statementFields, statementText, type SqlCatalog } from '@/lib/components/editor/editorSetup';

  let {
    value,
    dbType,
    schema = {},
    catalog,
    onchange,
    onrun,
    onsave,
    onrunstatement,
    onexplain,
    onwrap,
    runsWhole = false,
    readonly = false,
    options = {},
  }: {
    value: string;
    dbType: string;
    schema?: SQLNamespace;
    // Tables and columns for completion without a schema or alias in front.
    catalog?: SqlCatalog;
    onchange?: (v: string) => void;
    // append is set for Ctrl+\\, which keeps the results already shown.
    // The results it returns mark each statement's run button with how it
    // went, as DBX does.
    onrun?: (sql: string, append?: boolean) => Promise<ResultSet[] | undefined> | void;
    onsave?: () => void;
    // Ctrl+Shift+E explains the selection or the statement under the cursor.
    onexplain?: (sql: string) => void;
    // Set to show a run button next to every statement. The event is the
    // click, so the caller can offer a menu.
    onrunstatement?: (sql: string, e: MouseEvent) => Promise<ResultSet[] | undefined> | void;
    // Alt+Z switches line wrapping; the caller keeps the preference.
    onwrap?: () => void;
    // Set where a run always takes the whole text, as a notebook cell does;
    // then no statement is framed.
    runsWhole?: boolean;
    readonly?: boolean;
    // Display preferences; missing ones use the defaults.
    options?: Partial<EditorOptions>;
  } = $props();

  const opts = $derived<EditorOptions>({ ...defaultOptions, ...options });
  const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  // The shortcut that runs the whole script, as it reads on this system.
  const scriptKey = isMac ? 'Ctrl+Option+X' : 'Alt+X';

  let host: HTMLDivElement;
  let view: EditorView | undefined;
  const lang = new Compartment();
  const runGutter = new Compartment();
  const look = new Compartment();
  const behaviour = new Compartment();
  const access = new Compartment();

  const fields = statementFields(
    () => dbType,
    () => opts.highlightStatement && !!onrun && !runsWhole,
    // A notebook cell goes to the server whole, which splits it at ";"
    // only, so blank lines do not end its statements.
    () => opts.blankLineSplits && !runsWhole,
  );

  function runExtension(): Extension {
    return [fields.extension, runGutterExtension(fields, onrunstatement && ((span, e) => view && runSpan(view, span, (text) => onrunstatement?.(text, e))))];
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

  // target is what a run takes, as in DBX and DBeaver: the selection as it
  // is, every statement in it running in turn, or else the statement under
  // the cursor, the one the green frame shows. A script of one statement
  // runs whole wherever the cursor is; between statements nothing runs.
  function target(v: EditorView): Span | undefined {
    const sel = v.state.selection.main;
    if (!sel.empty) return { from: sel.from, to: sel.to };
    const doc = v.state.doc.toString();
    if (runsWhole) return { from: 0, to: doc.length };
    const list = v.state.field(fields.stmtField, false) ?? statements(doc, dbType, opts.blankLineSplits);
    if (list.length <= 1) return { from: 0, to: doc.length };
    const s = statementAt(list, doc, sel.head);
    return s && { from: s.from, to: s.to };
  }

  function runTarget(v: EditorView, append = false) {
    const span = target(v);
    if (!span) toast(tr('editor.noStatement', { script: scriptKey }), 'info', 3000);
    else runSpan(v, span, (text) => onrun?.(text, append));
    return true;
  }

  // runSpan runs a part of the document and marks its statements with how
  // they went.
  function runSpan(v: EditorView, span: Span, run: (text: string) => Promise<ResultSet[] | undefined> | void) {
    const text = v.state.sliceDoc(span.from, span.to);
    track(v, span.from, text, run(text));
  }

  // track marks the statements of a run as running, then each as done or
  // failed once the results come; a run that did not start leaves no marks.
  // The statements are found where they are in the editor, as the server
  // will split them; @set declarations are filled in on the client and send
  // nothing. Results come back in the order the statements ran, one each,
  // until the first that failed.
  async function track(v: EditorView, offset: number, text: string, pending: Promise<ResultSet[] | undefined> | void) {
    if (!pending || runsWhole) return;
    const ranges = statements(text, dbType, opts.blankLineSplits)
      .filter((s) => !/^\s*@set\s/i.test(s.text))
      .map((s) => ({ from: offset + s.from, to: offset + s.to }));
    if (!ranges.length) return;
    // Marks appear once the run has gone on for a moment, so a prompt that
    // is cancelled quickly leaves none; they are cleared if it is.
    const timer = setTimeout(() => view === v && v.dispatch({ effects: setRunStatus.of({ ranges, status: ranges.map(() => ({ state: 'running' })) }) }), 150);
    let results: ResultSet[] | undefined;
    try {
      results = await pending;
    } finally {
      clearTimeout(timer);
    }
    if (view !== v) return;
    if (!results) {
      v.dispatch({ effects: setRunStatus.of({ ranges, status: [] }) });
      return;
    }
    // A failing COMMIT comes as a result after the last statement; it
    // fails them all. Statements after a failing one did not run.
    const extra = results.slice(ranges.length).find((r) => r.error);
    v.dispatch({
      effects: setRunStatus.of({
        ranges,
        status: ranges.map((_, i) => {
          const failed = results!.findIndex((r) => r.error);
          if (failed >= 0 && i > failed) return undefined;
          const r = results![i];
          if (r?.error) return { state: 'error', error: r.error };
          if (extra) return { state: 'error', error: extra.error };
          return r ? { state: 'ok' } : undefined;
        }),
      }),
    });
  }

  // ////////////////////////////////////////////
  // moving between statements, as in DBX and DBeaver

  function stmtList(v: EditorView): Statement[] {
    return v.state.field(fields.stmtField, false) ?? statements(v.state.doc.toString(), dbType, opts.blankLineSplits && !runsWhole);
  }

  function gotoStatement(v: EditorView, dir: 1 | -1) {
    const list = stmtList(v);
    const pos = v.state.selection.main.head;
    const s = dir > 0 ? list.find((x) => x.from > pos) : [...list].reverse().find((x) => x.to < pos && !(pos >= x.from && pos <= x.to));
    // Going back from inside a statement first goes to its start.
    const here = list.find((x) => pos > x.from && pos <= x.to);
    const to = dir < 0 && here ? here : s;
    if (!to) return true;
    v.dispatch({ selection: { anchor: to.from }, scrollIntoView: true });
    return true;
  }

  function selectStatement(v: EditorView) {
    const list = stmtList(v);
    const sel = v.state.selection.main;
    const s = statementAt(list, v.state.doc.toString(), sel.head);
    if (!s) return true;
    v.dispatch({ selection: EditorSelection.range(s.from, s.to), scrollIntoView: true });
    return true;
  }

  // The dotted completion is built again only when the tables or the
  // default schema change, not on every key.
  let dotted: { schema: SQLNamespace; def: string; dialect: unknown; source: ReturnType<typeof schemaCompletionSource> } | undefined;

  function dottedSource(d: ReturnType<typeof dialect>) {
    const def = catalog?.defaultSchema() ?? '';
    if (!dotted || dotted.schema !== schema || dotted.def !== def || dotted.dialect !== d) {
      dotted = { schema, def, dialect: d, source: schemaCompletionSource({ dialect: d, schema, defaultSchema: def || undefined }) };
    }
    return dotted.source;
  }

  function langExt() {
    const d = dialect(dbType);
    return [
      sql({ dialect: d, upperCaseKeywords: true }),
      // Names after a dot (schema.table, alias.column) come from the SQL
      // language's schema completion; the rest from the catalog. Both may
      // load what is missing first.
      d.language.data.of({
        autocomplete: async (ctx: CompletionContext) => {
          const word = ctx.matchBefore(/[\p{L}\p{N}_$]*$/u);
          const at = word ? word.from : ctx.pos;
          if (!/\.\s*$/.test(ctx.state.sliceDoc(Math.max(0, at - 2), at))) return null;
          await catalog?.prepare(statementText(ctx.state.doc.toString(), ctx.pos, dbType));
          if (ctx.aborted) return null;
          return dottedSource(d)(ctx);
        },
      }),
      catalog ? d.language.data.of({ autocomplete: catalogCompletion(catalog, () => dbType, (n) => quoteIdent(dbType, n)) }) : [],
    ];
  }

  // Alt+X runs the selection or, without one, the whole script, as
  // DBeaver's Execute script does.
  function runScript(v: EditorView) {
    const sel = v.state.selection.main;
    runSpan(v, sel.empty ? { from: 0, to: v.state.doc.length } : { from: sel.from, to: sel.to }, (text) => onrun?.(text));
    return true;
  }

  function explainCurrent(v: EditorView) {
    if (!onexplain) return false;
    const text = currentStatementOf(v);
    if (text.trim()) onexplain(text);
    else toast(tr('editor.noStatement', { script: scriptKey }), 'info', 3000);
    return true;
  }

  function currentStatementOf(v: EditorView): string {
    const span = target(v);
    return span ? v.state.sliceDoc(span.from, span.to) : '';
  }

  onMount(() => {
    view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: value,
        extensions: [
          // Alt+X runs the script, Alt+Z wraps, Alt+S selects the
          // statement. On macOS they are Ctrl+Option+X/Z/S: Option+letter
          // types characters there (Polish ś, ź, ż; ß, Ω on US) and must
          // keep doing so. Keys are matched by their place, as the
          // character they give changes with Option.
          EditorView.domEventHandlers({
            keydown: (e, v) => {
              const mod = isMac ? e.altKey && e.ctrlKey : e.altKey && !e.ctrlKey;
              if (!mod || e.metaKey || e.shiftKey) return false;
              if (e.code === 'KeyX' && onrun && !runsWhole) runScript(v);
              else if (e.code === 'KeyZ' && onwrap) onwrap();
              else if (e.code === 'KeyS' && onrun && !runsWhole) selectStatement(v);
              else return false;
              e.preventDefault();
              return true;
            },
          }),
          behaviour.of(behaviourExtension()),
          runGutter.of(runExtension()),
          highlightActiveLine(),
          drawSelection(),
          history(),
          indentOnInput(),
          bracketMatching(),
          autocompletion({ icons: true }),
          highlightSelectionMatches(),
          placeholder(tr('query.placeholder', { script: scriptKey })),
          keymap.of([
            // Ctrl+Shift+Enter is kept from earlier versions; it does what
            // Ctrl+Enter does.
            { key: 'Mod-Shift-Enter', run: (v) => runTarget(v) },
            { key: 'Mod-Shift-e', run: explainCurrent },
            {
              key: 'Mod-Shift-f',
              run: () => {
                void format();
                return true;
              },
            },
            { key: 'Mod-Enter', run: (v) => runTarget(v) },
            // DBX's keys; Alt+Up/Down stay with moving lines.
            // DBX's keys. On macOS Ctrl+Shift+Up/Down select by page, and
            // Ctrl+arrows may go to Mission Control: there Ctrl+Option+Up/Down.
            { key: 'Ctrl-Shift-ArrowUp', mac: 'Ctrl-Alt-ArrowUp', run: (v) => gotoStatement(v, -1) },
            { key: 'Ctrl-Shift-ArrowDown', mac: 'Ctrl-Alt-ArrowDown', run: (v) => gotoStatement(v, 1) },
            { key: 'Mod-\\', run: (v) => runTarget(v, true) },
            { key: 'Shift-Enter', run: (v) => runTarget(v) },
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
          access.of(EditorState.readOnly.of(readonly)),
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

  // schema and catalog are read when completing, so they need no
  // reconfiguring when tables load.
  $effect(() => {
    void dbType;
    void catalog;
    view?.dispatch({ effects: [lang.reconfigure(langExt()), runGutter.reconfigure(runExtension())] });
  });

  // The statements are read again when the blank-line rule changes, as
  // the run gutter's reconfiguring recomputes them.
  $effect(() => {
    void opts;
    view?.dispatch({ effects: [look.reconfigure(lookExtension()), behaviour.reconfigure(behaviourExtension()), runGutter.reconfigure(runExtension())] });
  });

  $effect(() => {
    view?.dispatch({ effects: access.reconfigure(EditorState.readOnly.of(readonly)) });
  });

  onDestroy(() => view?.destroy());

  export function focus() {
    view?.focus();
  }

  // currentStatement is the selection, or the statement under the cursor
  // when the script has several; empty between statements, after telling
  // the user.
  export function currentStatement(): string {
    if (!view) return value;
    const text = currentStatementOf(view);
    if (!text.trim()) toast(tr('editor.noStatement', { script: scriptKey }), 'info', 3000);
    return text;
  }

  // format formats the selection, or the whole script, as one undoable
  // change. SQL the formatter cannot read is left as it is.
  export async function format() {
    const v = view;
    if (!v || readonly) return;
    const sel = v.state.selection.main;
    const from = sel.empty ? 0 : sel.from;
    const to = sel.empty ? v.state.doc.length : sel.to;
    const text = v.state.sliceDoc(from, to);
    if (!text.trim()) return;
    let out: string;
    try {
      out = await formatSql(text, dbType, { tabWidth: opts.tabSize, blankLines: opts.blankLineSplits });
    } catch (e) {
      toast(tr('editor.formatFailed', { error: errorMessage(e) }), 'error', 6000);
      return;
    }
    // The script may have changed while the formatter loaded.
    if (view !== v || v.state.sliceDoc(from, to) !== text) return;
    if (/\n\s*$/.test(text)) out += '\n';
    if (out === text) return;
    const selection = sel.empty ? { anchor: Math.min(sel.head, out.length) } : { anchor: from, head: from + out.length };
    v.dispatch({ changes: { from, to, insert: out }, selection, scrollIntoView: true, userEvent: 'input.format' });
    v.focus();
  }

  // selectCurrent selects the statement under the cursor (Alt+S).
  export function selectCurrent() {
    if (view) {
      selectStatement(view);
      view.focus();
    }
  }

  // run runs what Ctrl+Enter would; script runs what Alt+X would.
  export function run(append = false) {
    if (view) runTarget(view, append);
  }

  export function script() {
    if (view) runScript(view);
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
    --cm-stmt-frame: rgb(34 197 94 / 0.75);
  }

  :global(.dark) .editor {
    --cm-stmt-frame: rgb(74 222 128 / 0.6);
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
