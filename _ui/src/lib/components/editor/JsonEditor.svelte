<script lang="ts">
  // A small CodeMirror editor for JSON values: coloured as the value panel
  // shows them, and checked as it is typed, with the first mistake marked
  // in place and spelled out below.
  import { onDestroy, onMount } from 'svelte';
  import { Compartment, EditorState } from '@codemirror/state';
  import { EditorView, keymap, drawSelection, highlightActiveLine } from '@codemirror/view';
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
  import { syntaxHighlighting, bracketMatching, indentOnInput, indentUnit } from '@codemirror/language';
  import { closeBrackets, closeBracketsKeymap } from '@codemirror/autocomplete';
  import { linter, lintGutter, type Diagnostic } from '@codemirror/lint';
  import { json } from '@codemirror/lang-json';
  import { editorTheme, jsonHighlight } from '@/lib/components/editor/editorSetup';
  import { t } from '@/lib/i18n/index.svelte';

  let {
    value = $bindable(),
    wrap = true,
    // Where the cursor starts: 'end' after typing into a cell, 'all' selects
    // the text, as a cell's editor does.
    cursor = 'start',
    autofocus = true,
    onapply,
    oncancel,
    onblur,
  }: {
    value: string;
    wrap?: boolean;
    cursor?: 'start' | 'end' | 'all';
    autofocus?: boolean;
    onapply?: () => void;
    oncancel?: () => void;
    onblur?: () => void;
  } = $props();

  let host: HTMLDivElement;
  let view: EditorView | undefined;
  const wrapping = new Compartment();
  let error = $state<{ message: string; line: number; col: number } | null>(null);

  // The check waits until typing pauses, so half-typed values are not
  // flagged on every key.
  const lintDelay = 350;

  function check(v: EditorView): Diagnostic[] {
    const doc = v.state.doc;
    const text = doc.toString();
    if (!text.trim()) {
      error = null;
      return [];
    }
    try {
      JSON.parse(text);
      error = null;
      return [];
    } catch (e) {
      if (!(e instanceof SyntaxError)) throw e;
      const pos = errorPosition(e.message, text);
      const line = doc.lineAt(pos);
      error = { message: cleanMessage(e.message), line: line.number, col: pos - line.from + 1 };
      // An empty range is hard to see; the character at the mistake is
      // marked, or the one before it at the end of a line.
      const from = pos < line.to ? pos : Math.max(line.from, pos - 1);
      return [{ from, to: Math.min(from + 1, doc.length), severity: 'error', message: error.message }];
    }
  }

  // Engines tell where parsing stopped in different words.
  function errorPosition(message: string, text: string): number {
    let m = message.match(/at position (\d+)/);
    if (m) return Math.min(+m[1], text.length);
    m = message.match(/line (\d+) column (\d+)/);
    if (m) {
      const lines = text.split('\n');
      let pos = 0;
      for (let i = 0; i < +m[1] - 1 && i < lines.length; i++) pos += lines[i].length + 1;
      return Math.min(pos + +m[2] - 1, text.length);
    }
    return text.length;
  }

  function cleanMessage(message: string): string {
    return message
      .replace(/^JSON\.parse: /, '')
      .replace(/\s*\(line \d+ column \d+\)/, '')
      .replace(/\s*at line \d+ column \d+ of the JSON data/, '')
      .replace(/\s*in JSON at position \d+/, '')
      .replace(/^"[^"]*"\s+is not valid JSON$/, 'Unexpected text');
  }

  function place(state: EditorState) {
    if (cursor === 'all') return { anchor: 0, head: state.doc.length };
    return { anchor: cursor === 'end' ? state.doc.length : 0 };
  }

  onMount(() => {
    const state = EditorState.create({
      doc: value,
      extensions: [
        keymap.of([
          {
            key: 'Mod-Enter',
            run: () => {
              onapply?.();
              return true;
            },
          },
          {
            key: 'Escape',
            run: () => {
              if (!oncancel) return false;
              oncancel();
              return true;
            },
          },
          ...closeBracketsKeymap,
          ...defaultKeymap,
          ...historyKeymap,
          indentWithTab,
        ]),
        history(),
        drawSelection(),
        highlightActiveLine(),
        indentOnInput(),
        bracketMatching(),
        closeBrackets(),
        indentUnit.of('  '),
        EditorState.tabSize.of(2),
        json(),
        syntaxHighlighting(jsonHighlight),
        linter(check, { delay: lintDelay }),
        lintGutter(),
        wrapping.of(wrap ? EditorView.lineWrapping : []),
        editorTheme,
        EditorView.theme({
          '&': { fontSize: '12px' },
          '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '18px' },
          '.cm-content': { padding: '8px 0' },
          '.cm-gutters': { paddingRight: '0' },
        }),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) value = u.state.doc.toString();
        }),
        EditorView.domEventHandlers({
          blur: (_, v) => {
            // Clicks on the editor's own tooltips do not count as leaving it.
            setTimeout(() => {
              if (!destroyed && !v.hasFocus) onblur?.();
            }, 0);
            return false;
          },
          // Keys stay with the editor, not the grid or panel around it.
          keydown: (e) => {
            e.stopPropagation();
            return false;
          },
        }),
      ],
    });
    view = new EditorView({ parent: host, state });
    view.dispatch({ selection: place(view.state) });
    if (autofocus) view.focus();
  });

  // Changes from outside, such as Format or keys typed before the editor
  // took the focus, replace only the part that differs.
  $effect(() => {
    const v = value;
    if (!view) return;
    const cur = view.state.doc.toString();
    if (v === cur) return;
    let a = 0;
    while (a < cur.length && a < v.length && cur[a] === v[a]) a++;
    let b = 0;
    while (b < cur.length - a && b < v.length - a && cur[cur.length - 1 - b] === v[v.length - 1 - b]) b++;
    // A cursor at the end stays there, after the keys added to it.
    const sel = view.state.selection.main;
    const atEnd = sel.empty && sel.head === cur.length;
    view.dispatch({
      changes: { from: a, to: cur.length - b, insert: v.slice(a, v.length - b) },
      ...(atEnd ? { selection: { anchor: v.length } } : {}),
    });
  });

  $effect(() => {
    const w = wrap;
    view?.dispatch({ effects: wrapping.reconfigure(w ? EditorView.lineWrapping : []) });
  });

  // Set once closed, so a blur from closing is not taken as leaving.
  let destroyed = false;

  onDestroy(() => {
    destroyed = true;
    view?.destroy();
  });

  export function focus() {
    view?.focus();
  }
</script>

<div class="flex h-full min-h-0 flex-col">
  <div class="json-editor min-h-0 flex-1" bind:this={host}></div>
  {#if error}
    <div class="flex shrink-0 items-start gap-1.5 border-t border-danger/30 bg-danger-soft px-2 py-1 font-mono text-2xs text-danger" role="status">
      <span class="shrink-0 tabular-nums">{t('valuePanel.errorAt', { line: error.line, col: error.col })}</span>
      <span class="min-w-0 break-words">{error.message}</span>
    </div>
  {/if}
</div>

<style>
  .json-editor {
    --cm-active-line: #2f6be60a;
    --cm-selection: #a6d2ff;
    --cm-bracket: #93d9d9;
    --cm-match: #edebfc;
  }

  :global(.dark) .json-editor {
    --cm-active-line: #ffffff0a;
    --cm-selection: #214283;
    --cm-bracket: #43454a;
    --cm-match: #373b39;
  }
</style>
