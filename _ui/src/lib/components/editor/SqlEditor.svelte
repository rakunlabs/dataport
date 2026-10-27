<script lang="ts">
  import { t as tr } from '@/lib/i18n/index.svelte';
  import { onDestroy, onMount } from 'svelte';
  import { EditorState, Compartment, type Extension } from '@codemirror/state';
  import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection, placeholder } from '@codemirror/view';
  import { statements } from '@/lib/helper/statements';
  import { vim } from '@replit/codemirror-vim';
  import { indentUnit } from '@codemirror/language';
  import { defaultOptions, type EditorOptions } from '@/lib/helper/editorOptions';
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
  import { sql, type SQLNamespace } from '@codemirror/lang-sql';
  import { syntaxHighlighting, bracketMatching, indentOnInput } from '@codemirror/language';
  import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap } from '@codemirror/autocomplete';
  import { highlightSelectionMatches, searchKeymap } from '@codemirror/search';
  import { dialect, editorTheme, highlight, placeholderMarks, runGutterExtension, statementFields } from '@/lib/components/editor/editorSetup';

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
  const access = new Compartment();

  const fields = statementFields(
    () => dbType,
    () => opts.highlightStatement,
  );

  function runExtension(): Extension {
    return runGutterExtension(fields, onrunstatement && ((sql, e) => onrunstatement?.(sql, e)));
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

  // statementAt returns the statement under the cursor, for Ctrl+Enter
  // without a selection when the script holds several.
  function statementAt(v: EditorView): string | undefined {
    const list = statements(v.state.doc.toString(), dbType);
    if (list.length < 2) return undefined;
    const pos = v.state.selection.main.head;
    return list.find((x) => pos >= x.from && pos <= x.to + 1)?.text;
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

  $effect(() => {
    void dbType;
    void schema;
    view?.dispatch({ effects: [lang.reconfigure(langExt()), runGutter.reconfigure(runExtension())] });
  });

  $effect(() => {
    void opts;
    view?.dispatch({ effects: [look.reconfigure(lookExtension()), behaviour.reconfigure(behaviourExtension())] });
  });

  $effect(() => {
    view?.dispatch({ effects: access.reconfigure(EditorState.readOnly.of(readonly)) });
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
