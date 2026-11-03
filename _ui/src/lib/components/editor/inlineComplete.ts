// Inline completion: after a pause in typing, the text the model suggests
// for the cursor is shown greyed out. Tab takes it, Ctrl/Cmd+Right takes
// its next word, Escape or typing something else drops it; typing what it
// starts with keeps the rest.

import { Prec, StateEffect, StateField, type EditorState, type Extension, type Transaction } from '@codemirror/state';
import { completionStatus } from '@codemirror/autocomplete';
import { Decoration, EditorView, ViewPlugin, WidgetType, keymap, type DecorationSet, type ViewUpdate } from '@codemirror/view';

export interface InlineRequest {
  prefix: string;
  suffix: string;
}

export interface InlineOptions {
  // Asks for the text at the cursor; empty for none.
  fetch: (req: InlineRequest, signal: AbortSignal) => Promise<string>;
  // Read before each request, so it can follow settings without a reconfigure.
  enabled: () => boolean;
  delay?: number;
}

interface Ghost {
  pos: number;
  text: string;
}

const setGhost = StateEffect.define<Ghost | null>();

const ghostField = StateField.define<Ghost | null>({
  create: () => null,
  update(g, tr) {
    for (const e of tr.effects) if (e.is(setGhost)) return e.value;
    if (!g) return null;
    if (tr.docChanged) return typedThrough(g, tr);
    if (tr.selection && (!tr.newSelection.main.empty || tr.newSelection.main.head !== g.pos)) return null;
    return g;
  },
  provide: (f) => EditorView.decorations.from(f, (g): DecorationSet => (g ? Decoration.set([Decoration.widget({ widget: new GhostWidget(g.text), side: 1 }).range(g.pos)]) : Decoration.none)),
});

// Typing the start of the suggestion keeps the rest of it.
function typedThrough(g: Ghost, tr: Transaction): Ghost | null {
  let out: Ghost | null = null;
  let changes = 0;
  tr.changes.iterChanges((fromA, toA, _fromB, _toB, inserted) => {
    changes++;
    const text = inserted.toString();
    if (fromA === g.pos && toA === g.pos && text && g.text.startsWith(text) && text.length < g.text.length) {
      out = { pos: g.pos + text.length, text: g.text.slice(text.length) };
    }
  });
  return changes === 1 ? out : null;
}

class GhostWidget extends WidgetType {
  constructor(readonly text: string) {
    super();
  }
  eq(other: GhostWidget) {
    return other.text === this.text;
  }
  toDOM() {
    const span = document.createElement('span');
    span.className = 'cm-ghost-text';
    span.textContent = this.text;
    span.setAttribute('aria-hidden', 'true');
    return span;
  }
  ignoreEvent() {
    return false;
  }
}

// clearGhost drops the suggestion shown, as when completion is turned off.
export function clearGhost(view: EditorView) {
  if (view.state.field(ghostField, false)) view.dispatch({ effects: setGhost.of(null) });
}

function accept(view: EditorView, part?: (text: string) => string): boolean {
  const g = view.state.field(ghostField, false);
  if (!g) return false;
  const text = part ? part(g.text) : g.text;
  if (!text) return false;
  view.dispatch({
    changes: { from: g.pos, insert: text },
    selection: { anchor: g.pos + text.length },
    effects: setGhost.of(text === g.text ? null : { pos: g.pos + text.length, text: g.text.slice(text.length) }),
    userEvent: 'input.complete',
    scrollIntoView: true,
  });
  return true;
}

// The next word of the suggestion with the spaces before it.
const nextWord = (text: string) => /^\s*[\p{L}\p{N}_$]+|^\s*[^\s\p{L}\p{N}_$]+|^\s+/u.exec(text)?.[0] ?? text;

// A suggestion is asked for at the end of a line, or before what only
// closes it, as Copilot does.
function askable(state: EditorState): boolean {
  if (state.readOnly) return false;
  const sel = state.selection.main;
  if (!sel.empty || state.selection.ranges.length > 1) return false;
  const line = state.doc.lineAt(sel.head);
  const after = state.sliceDoc(sel.head, line.to);
  if (!/^[\s)\]}'"`;,]*$/.test(after)) return false;
  return state.sliceDoc(Math.max(0, sel.head - 2000), sel.head).trim().length > 0;
}

const prefixChars = 6000;
const suffixChars = 2000;

export function inlineCompletion(opts: InlineOptions): Extension {
  const delay = opts.delay ?? 350;
  const cache = new Map<string, string>();

  const plugin = ViewPlugin.fromClass(
    class {
      timer: ReturnType<typeof setTimeout> | undefined;
      abort: AbortController | undefined;
      // A service that refuses (not set up, no access) is not asked again.
      off = false;

      constructor(readonly view: EditorView) {}

      update(u: ViewUpdate) {
        if (!u.docChanged && !u.selectionSet) return;
        this.cancel();
        if (!u.docChanged || this.off) return;
        const typed = u.transactions.some((tr) => tr.isUserEvent('input.type') || tr.isUserEvent('delete') || tr.isUserEvent('input.paste'));
        if (!typed || u.state.field(ghostField, false)) return;
        this.timer = setTimeout(() => void this.ask(), delay);
      }

      cancel() {
        clearTimeout(this.timer);
        this.timer = undefined;
        this.abort?.abort();
        this.abort = undefined;
      }

      async ask() {
        const view = this.view;
        const state = view.state;
        // The completion list has the keys while it is open.
        if (!opts.enabled() || !askable(state) || !view.hasFocus || completionStatus(state) === 'active') return;
        const pos = state.selection.main.head;
        const req = { prefix: state.sliceDoc(Math.max(0, pos - prefixChars), pos), suffix: state.sliceDoc(pos, pos + suffixChars) };
        const key = req.prefix.slice(-400) + '\u0000' + req.suffix.slice(0, 200);

        let text = cache.get(key);
        if (text === undefined) {
          const abort = (this.abort = new AbortController());
          try {
            text = await opts.fetch(req, abort.signal);
          } catch (e) {
            if ((e as { status?: number }).status === 403) this.off = true;
            return;
          }
          if (abort.signal.aborted) return;
          cache.set(key, text);
          if (cache.size > 50) cache.delete(cache.keys().next().value!);
        }

        // The editor may have moved on while the answer came.
        if (!text || view.state !== state || completionStatus(state) === 'active') return;
        view.dispatch({ effects: setGhost.of({ pos, text }) });
      }

      destroy() {
        this.cancel();
      }
    },
  );

  const keys = Prec.highest(
    keymap.of([
      { key: 'Tab', run: (v) => accept(v) },
      { key: 'Mod-ArrowRight', run: (v) => accept(v, nextWord) },
      {
        key: 'Escape',
        // Escape goes on to what else uses it, such as Vim's normal mode.
        run: (v) => {
          if (v.state.field(ghostField, false)) v.dispatch({ effects: setGhost.of(null) });
          return false;
        },
      },
    ]),
  );

  const theme = EditorView.baseTheme({
    '.cm-ghost-text': { color: 'var(--cm-comment)', opacity: '0.85', pointerEvents: 'none' },
  });

  return [ghostField, plugin, keys, theme, EditorView.focusChangeEffect.of((_s, focus) => (focus ? null : setGhost.of(null)))];
}
