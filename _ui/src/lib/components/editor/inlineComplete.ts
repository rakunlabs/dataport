// Inline completion: after a pause in typing, the text the model suggests
// for the cursor is shown greyed out. Tab takes it, Ctrl/Cmd+Right takes
// its next word, Escape or typing something else drops it; typing what it
// starts with keeps the rest. Alt+\ (Option+\ on macOS) asks at once,
// anywhere in a line, for a fresh suggestion.

import { Prec, StateEffect, StateField, type EditorState, type Extension, type Transaction } from '@codemirror/state';
import { completionStatus } from '@codemirror/autocomplete';
import { Decoration, EditorView, ViewPlugin, WidgetType, keymap, type DecorationSet, type ViewUpdate } from '@codemirror/view';

export interface InlineRequest {
  prefix: string;
  suffix: string;
}

// What completion is doing, for the toolbar: waiting for a pause in
// typing, asking, and how the last answer went. ms is how long the model
// took; cached answers say so.
export type InlineState =
  | { kind: 'idle' }
  | { kind: 'waiting' }
  | { kind: 'asking'; forced: boolean }
  | { kind: 'shown'; ms: number; cached: boolean }
  | { kind: 'empty'; ms: number }
  | { kind: 'error'; message: string }
  | { kind: 'off'; message: string };

export interface InlineOptions {
  // Asks for the text at the cursor; empty for none.
  fetch: (req: InlineRequest, signal: AbortSignal) => Promise<string>;
  // Read before each request, so it can follow settings without a reconfigure.
  enabled: () => boolean;
  delay?: number;
  onstate?: (s: InlineState) => void;
}

interface Ghost {
  pos: number;
  text: string;
}

const setGhost = StateEffect.define<Ghost | null>();
// Asks for a suggestion now, as the shortcut does.
const askNow = StateEffect.define<null>();

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
// Asked by the shortcut, any place in a line will do.
function askable(state: EditorState, forced = false): boolean {
  if (state.readOnly) return false;
  const sel = state.selection.main;
  if (!sel.empty || state.selection.ranges.length > 1) return false;
  if (forced) return true;
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
  // The state goes out after the editor's update: a listener that redraws
  // the toolbar must not reach back into the editor in the middle of one,
  // which would cancel what was just started. The last state wins.
  let next: InlineState | undefined;
  const report = (s: InlineState) => {
    if (!opts.onstate) return;
    const queued = next !== undefined;
    next = s;
    if (queued) return;
    queueMicrotask(() => {
      const out = next!;
      next = undefined;
      opts.onstate?.(out);
    });
  };

  const plugin = ViewPlugin.fromClass(
    class {
      timer: ReturnType<typeof setTimeout> | undefined;
      abort: AbortController | undefined;
      // A service that refuses (not set up, no access) is not asked again.
      off = false;

      constructor(readonly view: EditorView) {}

      update(u: ViewUpdate) {
        if (u.transactions.some((tr) => tr.effects.some((e) => e.is(askNow)))) {
          this.cancel();
          void this.ask(true);
          return;
        }
        if (!u.docChanged && !u.selectionSet) return;
        this.cancel();
        if (!u.docChanged || this.off) return;
        const typed = u.transactions.some((tr) => tr.isUserEvent('input.type') || tr.isUserEvent('delete') || tr.isUserEvent('input.paste'));
        if (!typed || u.state.field(ghostField, false)) return;
        if (!opts.enabled() || !askable(u.state)) return;
        this.timer = setTimeout(() => void this.ask(), delay);
        report({ kind: 'waiting' });
      }

      // Stops what is pending; one that never got an answer reads as idle.
      cancel() {
        const pending = !!this.timer || !!this.abort;
        clearTimeout(this.timer);
        this.timer = undefined;
        this.abort?.abort();
        this.abort = undefined;
        if (pending) report({ kind: 'idle' });
      }

      // forced: the shortcut asked, past the place rules, the cache and a
      // service that refused before; it asks again for something new.
      async ask(forced = false) {
        const view = this.view;
        const state = view.state;
        if (forced) this.off = false;
        // The completion list has the keys while it is open.
        this.timer = undefined;
        if (!opts.enabled() || this.off || !askable(state, forced) || !view.hasFocus || completionStatus(state) === 'active') {
          report({ kind: 'idle' });
          return;
        }
        const pos = state.selection.main.head;
        const req = { prefix: state.sliceDoc(Math.max(0, pos - prefixChars), pos), suffix: state.sliceDoc(pos, pos + suffixChars) };
        const key = req.prefix.slice(-400) + '\u0000' + req.suffix.slice(0, 200);

        let text = forced ? undefined : cache.get(key);
        const cached = text !== undefined;
        const started = performance.now();
        if (text === undefined) {
          const abort = (this.abort = new AbortController());
          report({ kind: 'asking', forced });
          try {
            text = await opts.fetch(req, abort.signal);
          } catch (e) {
            if (abort.signal.aborted) return;
            this.abort = undefined;
            const message = e instanceof Error ? e.message : String(e);
            if ((e as { status?: number }).status === 403) {
              this.off = true;
              report({ kind: 'off', message });
            } else report({ kind: 'error', message });
            return;
          }
          if (abort.signal.aborted) return;
          this.abort = undefined;
          cache.set(key, text);
          if (cache.size > 50) cache.delete(cache.keys().next().value!);
        }
        const ms = cached ? 0 : Math.round(performance.now() - started);

        // The editor may have moved on while the answer came.
        if (!text) {
          report({ kind: 'empty', ms });
          return;
        }
        if (view.state !== state || completionStatus(state) === 'active') {
          report({ kind: 'idle' });
          return;
        }
        view.dispatch({ effects: setGhost.of({ pos, text }) });
        report({ kind: 'shown', ms, cached });
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

  // Alt+\ asks now. The key is matched by its place: Option+\ types a
  // character on macOS keyboards (« on US, for one), and the key gives
  // another character on many layouts.
  const askKey = EditorView.domEventHandlers({
    keydown: (e, v) => {
      if (e.code !== 'Backslash' || !e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return false;
      if (!opts.enabled() || !askable(v.state, true)) return false;
      e.preventDefault();
      v.dispatch({ effects: [setGhost.of(null), askNow.of(null)] });
      return true;
    },
  });

  return [ghostField, plugin, keys, askKey, theme, EditorView.focusChangeEffect.of((_s, focus) => (focus ? null : setGhost.of(null)))];
}
