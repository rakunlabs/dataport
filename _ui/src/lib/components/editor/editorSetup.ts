import { t as tr } from '@/lib/i18n/index.svelte';
import { StateField, StateEffect, RangeSet, type Extension, type EditorState } from '@codemirror/state';
import { EditorView, gutter, GutterMarker, Decoration, MatchDecorator, ViewPlugin, RectangleMarker, layer, type DecorationSet, type ViewUpdate } from '@codemirror/view';
import { HighlightStyle, syntaxTree } from '@codemirror/language';
import { PostgreSQL, MySQL, MariaSQL, MSSQL, PLSQL, SQLite, StandardSQL, type SQLDialect } from '@codemirror/lang-sql';
import type { Completion, CompletionContext, CompletionResult } from '@codemirror/autocomplete';
import { tags as t } from '@lezer/highlight';
import { statementAt, statements, type Statement } from '@/lib/helper/statements';
import { tableRefs } from '@/lib/helper/sqlTables';

// ////////////////////////////////////////////
// run buttons next to statements

// How the last run of a statement went, as DBX shows it on the statement's
// run button: running, done, or failed with its error.
export interface RunStatus {
  state: 'running' | 'ok' | 'error';
  error?: string;
}

// A run marks the statements it ran, by where they are in the editor: all
// as running when it starts, then each as done or failed from the server's
// answer. ranges are [from, to) offsets in the document as it was when the
// run started; marks move with later edits.
export interface RunMarks {
  ranges: { from: number; to: number }[];
  status: (RunStatus | undefined)[];
}

export const setRunStatus = StateEffect.define<RunMarks | null>({
  map: (v, change) => v && { ...v, ranges: v.ranges.map((r) => ({ from: change.mapPos(r.from, 1), to: change.mapPos(r.to, -1) })) },
});

interface StatusEntry {
  from: number;
  to: number;
  status: RunStatus;
}

// Marks move with the text and go when their statement is edited, so they
// never point at a statement that is not the one that ran.
const runStatusField = StateField.define<StatusEntry[]>({
  create: () => [],
  update: (list, tr) => {
    let out = list;
    if (tr.docChanged) {
      out = [];
      for (const e of list) {
        let touched = false;
        tr.changes.iterChangedRanges((a, b) => {
          if (a <= e.to && b >= e.from) touched = true;
        });
        if (!touched) out.push({ ...e, from: tr.changes.mapPos(e.from, 1), to: tr.changes.mapPos(e.to, -1) });
      }
    }
    for (const effect of tr.effects) {
      if (!effect.is(setRunStatus)) continue;
      if (!effect.value) {
        out = [];
        continue;
      }
      const { ranges, status } = effect.value;
      const found: StatusEntry[] = [];
      ranges.forEach((r, i) => {
        if (status[i]) found.push({ from: r.from, to: r.to, status: status[i]! });
      });
      // A new run replaces the marks of the statements it ran.
      out = [...out.filter((e) => !ranges.some((f) => f.from < e.to && f.to > e.from)), ...found];
    }
    return out;
  },
});

// A mark belongs to the statement it lies in: the statement may have
// grown since, as long as it was not edited where the run was.
function statusAt(state: EditorState, s: Statement): RunStatus | undefined {
  return state.field(runStatusField, false)?.find((e) => e.from >= s.from && e.to <= s.to)?.status;
}

class RunMarker extends GutterMarker {
  stmt: Statement;
  status?: RunStatus;

  constructor(stmt: Statement, status?: RunStatus) {
    super();
    this.stmt = stmt;
    this.status = status;
  }

  eq(other: RunMarker) {
    return other.stmt.from === this.stmt.from && other.stmt.text === this.stmt.text && other.status?.state === this.status?.state && other.status?.error === this.status?.error;
  }

  toDOM() {
    const b = document.createElement('button');
    b.className = 'cm-run-stmt';
    b.type = 'button';
    let label = tr('editor.runStatement');
    if (this.status?.state === 'ok') label += ' · ' + tr('editor.statusOk');
    else if (this.status?.state === 'running') label += ' · ' + tr('editor.statusRunning');
    else if (this.status?.state === 'error') label += ' · ' + tr('editor.statusError', { error: this.status.error ?? '' });
    b.title = label;
    b.setAttribute('aria-label', label);
    b.innerHTML = '<svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M6 4l15 8-15 8z"/></svg>';
    if (this.status) {
      const badge = document.createElement('span');
      badge.className = 'cm-run-badge cm-run-badge-' + this.status.state;
      badge.setAttribute('aria-hidden', 'true');
      badge.innerHTML =
        this.status.state === 'running'
          ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.5" stroke-linecap="round"><path d="M21 12a9 9 0 1 1-6.22-8.56"/></svg>'
          : `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.5" stroke-linecap="round" stroke-linejoin="round"><path d="${this.status.state === 'error' ? 'm16 8-8 8m0-8 8 8' : 'm7 12 3 3 7-7'}"/></svg>`;
      b.appendChild(badge);
    }
    return b;
  }
}

export interface StatementFields {
  stmtField: StateField<Statement[]>;
  activeField: StateField<Statement | null>;
}

// statementFields tracks the statements of the document and the one a run
// without a selection takes. dbType and highlight are read when the
// document or selection changes, so they follow the editor's current props.
export function statementFields(dbType: () => string, highlight: () => boolean, blankLines: () => boolean = () => true): StatementFields & { extension: Extension } {
  // The statements of the document, recomputed when it changes.
  const stmtField = StateField.define<Statement[]>({
    create: (st) => statements(st.doc.toString(), dbType(), blankLines()),
    update: (v, tr) => (tr.docChanged || tr.reconfigured ? statements(tr.state.doc.toString(), dbType(), blankLines()) : v),
  });

  // The statement under the cursor, framed as DBX does, so it is clear what
  // Ctrl+Enter or the Run button takes. A selection runs as it is, so it
  // has no frame.
  const activeField = StateField.define<Statement | null>({
    create: () => null,
    update: (prev, tr) => {
      const sel = tr.state.selection.main;
      if (!highlight() || !sel.empty) return null;
      const list = tr.state.field(stmtField);
      // Inside the statement already framed nothing changes; this keeps
      // moving the cursor cheap in a long script.
      if (prev && !tr.docChanged && !tr.reconfigured && sel.head >= prev.from && sel.head <= prev.to) return prev;
      const inside = list.find((x) => sel.head >= x.from && sel.head <= x.to);
      if (inside) return inside;
      // Only between statements is the text around the cursor read.
      const line = tr.state.doc.lineAt(sel.head);
      return statementAt(list, tr.state.doc.sliceString(0, line.to), sel.head) ?? null;
    },
  });

  return { stmtField, activeField, extension: [stmtField, activeField, runStatusField, statementFrame(activeField)] };
}

// The frame stands a little off the text. Only lines in the viewport can be
// measured, so a long statement is as wide as its visible lines.
const frameInset = 2;

function statementFrame(activeField: StateField<Statement | null>): Extension {
  return layer({
    above: true,
    class: 'cm-stmt-frame-layer',
    update: (u) =>
      u.docChanged || u.selectionSet || u.viewportChanged || u.geometryChanged || u.startState.field(activeField) !== u.state.field(activeField),
    markers: (v) => {
      const s = v.state.field(activeField);
      if (!s) return [];
      const doc = v.state.doc;
      const first = doc.lineAt(s.from);
      const last = doc.lineAt(s.to);
      const base = v.scrollDOM.getBoundingClientRect();
      const baseLeft = base.left - v.scrollDOM.scrollLeft;
      const baseTop = base.top - v.scrollDOM.scrollTop;
      const top = v.documentTop + v.lineBlockAt(first.from).top - baseTop;
      const bottom = v.documentTop + v.lineBlockAt(last.from).bottom - baseTop;

      let left = Infinity;
      let right = -Infinity;
      const from = Math.max(first.number, doc.lineAt(v.viewport.from).number);
      const to = Math.min(last.number, doc.lineAt(v.viewport.to).number);
      for (let n = from; n <= to; n++) {
        const line = doc.line(n);
        const start = v.coordsAtPos(n === first.number ? s.from : line.from, 1);
        const end = v.coordsAtPos(n === last.number ? s.to : line.to, -1);
        if (start) left = Math.min(left, start.left);
        if (end) right = Math.max(right, end.right);
      }
      if (v.lineWrapping || !Number.isFinite(right)) {
        const content = v.contentDOM.getBoundingClientRect();
        right = Math.max(Number.isFinite(right) ? right : -Infinity, content.right - frameInset * 2);
      }
      if (!Number.isFinite(left)) left = v.contentDOM.getBoundingClientRect().left;

      return [
        new RectangleMarker(
          'cm-stmt-frame',
          left - baseLeft - frameInset,
          top,
          Math.max(8, right - left + frameInset * 2),
          Math.max(4, bottom - top),
        ),
      ];
    },
  });
}

// onrunstatement gets the part of the document to run.
export function runGutterExtension({ stmtField }: StatementFields, onrunstatement?: (span: { from: number; to: number }, e: MouseEvent) => void): Extension {
  if (!onrunstatement) return [];
  return [
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
          marks.push(new RunMarker(s, statusAt(v.state, s)).range(at));
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
            // A selection that covers part of the statement runs instead, as
            // in DBX, so a few selected lines can be run from their button.
            const sel = v.state.selection.main;
            const overlaps = !sel.empty && sel.from < s.to && sel.to > s.from;
            onrunstatement(overlaps ? { from: sel.from, to: sel.to } : { from: s.from, to: s.to }, e as MouseEvent);
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

// Placeholders the run fills in (${name}, #{name}, @set) are marked, so
// it is clear what will be asked for.
const placeholderMark = Decoration.mark({ class: 'cm-placeholder-var' });
const placeholderMatcher = new MatchDecorator({
  regexp: /\$\{\s*[\p{L}_][\p{L}\p{N}_]*\s*\}|#\{\s*[\p{L}_][\p{L}\p{N}_]*\s*\}|@set\b/giu,
  decoration: () => placeholderMark,
});
export const placeholderMarks = ViewPlugin.fromClass(
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

export const highlight = HighlightStyle.define([
  { tag: t.keyword, color: 'var(--cm-keyword)' },
  { tag: [t.string, t.special(t.string)], color: 'var(--cm-string)' },
  { tag: [t.number, t.bool, t.null], color: 'var(--cm-number)' },
  { tag: [t.lineComment, t.blockComment], color: 'var(--cm-comment)' },
  { tag: [t.typeName, t.standard(t.name)], color: 'var(--cm-type)', fontStyle: 'italic' },
  { tag: [t.operator, t.punctuation], color: 'var(--cm-punct)' },
  { tag: t.special(t.name), color: 'var(--cm-ident)' },
]);

// JSON in the colours of the value panel's tree.
export const jsonHighlight = HighlightStyle.define([
  { tag: t.propertyName, color: 'var(--color-json-key)' },
  { tag: t.string, color: 'var(--color-json-str)' },
  { tag: t.number, color: 'var(--color-json-num)' },
  { tag: [t.bool, t.null], color: 'var(--color-json-kw)' },
  { tag: [t.separator, t.brace, t.squareBracket], color: 'var(--color-muted)' },
]);

export const editorTheme = EditorView.theme({
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
  '.cm-run-stmt:has(.cm-run-badge)': { opacity: '1', position: 'relative' },
  '.cm-run-badge': {
    position: 'absolute',
    right: '-5px',
    bottom: '-4px',
    display: 'flex',
    width: '11px',
    height: '11px',
    borderRadius: '9999px',
    padding: '1.5px',
    color: '#fff',
    boxShadow: '0 0 0 1.5px var(--color-bg)',
    pointerEvents: 'none',
  },
  '.cm-run-badge svg': { width: '100%', height: '100%' },
  '.cm-run-badge-ok': { backgroundColor: 'var(--color-ok)' },
  '.cm-run-badge-error': { backgroundColor: 'var(--color-danger)' },
  '.cm-run-badge-running': { backgroundColor: 'var(--color-accent)' },
  '.cm-run-badge-running svg': { animation: 'cm-run-spin 0.9s linear infinite' },
  '@keyframes cm-run-spin': { to: { transform: 'rotate(360deg)' } },
  '.cm-stmt-frame-layer': { pointerEvents: 'none' },
  '.cm-stmt-frame': { boxSizing: 'border-box', border: '1px solid var(--cm-stmt-frame)', borderRadius: '2px', pointerEvents: 'none' },
  '.cm-placeholder-var': { color: 'var(--cm-type)', backgroundColor: 'var(--cm-match)', borderRadius: '2px' },
  '.cm-fat-cursor': { backgroundColor: 'var(--color-accent) !important', color: 'var(--color-accent-fg) !important' },
  '.cm-vim-panel': { fontFamily: 'var(--font-mono)', fontSize: '12px', padding: '0 6px' },
});

// ////////////////////////////////////////////
// completion of tables and columns

// What the editor knows of the connection's tables, as far as loaded.
export interface SqlCatalog {
  // The schema names without one are looked for in first.
  defaultSchema: () => string;
  // Tables of the default schema, offered where a table is expected.
  tables: () => { name: string; kind: string }[];
  // Schemas, offered with the tables.
  schemas: () => string[];
  // The columns of a table, once loaded; schema is empty when not written.
  columns: (schema: string, table: string) => { name: string; type: string }[] | undefined;
  // Loads what the script names and is not loaded yet: the default
  // schema's tables, the schemas written before a dot and the columns of
  // the tables used.
  prepare: (sql: string) => Promise<void>;
}

// statementText is the statement under the cursor, or the one before it,
// so completion reads and loads only what that statement names.
export function statementText(doc: string, pos: number, dbType: string): string {
  const list = statements(doc, dbType);
  const s = list.find((x) => pos >= x.from && pos <= x.to + 1) ?? [...list].reverse().find((x) => x.to < pos);
  return s?.text ?? doc;
}

const wordBefore = /[\p{L}_][\p{L}\p{N}_$]*$/u;
// Where the statement expects a table: after FROM, JOIN and the like, or
// after a comma in a FROM list.
const tablePlace = /\b(from|join|update|into|table)\s+(?:[^\s,;()]+(?:\s+(?:as\s+)?[\p{L}_][\p{L}\p{N}_]*)?\s*,\s*)*$/iu;
const skipNodes = new Set(['String', 'LineComment', 'BlockComment', 'QuotedIdentifier', 'SpecialVar']);

// catalogCompletion completes table names where a table is expected, even
// before a letter is typed, and the columns of the tables the statement
// under the cursor names, without a table in front. Names after a dot are
// left to the SQL language's own completion.
export function catalogCompletion(catalog: SqlCatalog, dbType: () => string, quote: (name: string) => string) {
  const plain = (name: string) => (dbType() === 'postgres' ? /^[a-z_][a-z0-9_]*$/ : /^[A-Za-z_][A-Za-z0-9_]*$/).test(name);
  const nameApply = (name: string) => (plain(name) ? undefined : quote(name));

  return async (ctx: CompletionContext): Promise<CompletionResult | null> => {
    const node = syntaxTree(ctx.state).resolveInner(ctx.pos, -1);
    if (skipNodes.has(node.name)) return null;
    const word = ctx.matchBefore(wordBefore);
    const from = word ? word.from : ctx.pos;
    if (ctx.state.sliceDoc(from - 1, from) === '.') return null;
    const before = ctx.state.sliceDoc(Math.max(0, from - 400), from);
    const atTable = tablePlace.test(before);
    if (!atTable && !word && !ctx.explicit) return null;

    const stmt = statementText(ctx.state.doc.toString(), ctx.pos, dbType());
    await catalog.prepare(stmt);
    if (ctx.aborted) return null;

    if (atTable) {
      const options: Completion[] = [
        ...catalog.tables().map((t) => ({ label: t.name, type: t.kind === 'table' ? 'type' : 'class', detail: t.kind.replace('_', ' '), apply: nameApply(t.name), boost: 2 })),
        ...catalog.schemas().map((s) => ({ label: s, type: 'namespace', detail: 'schema', apply: nameApply(s), boost: 1 })),
      ];
      return options.length ? { from, options, validFor: wordBefore } : null;
    }

    const refs = tableRefs(stmt);
    const seen = new Set<string>();
    const options: Completion[] = [];
    for (const ref of refs) {
      const key = `${ref.schema}.${ref.table}`;
      if (seen.has(key)) continue;
      seen.add(key);
      for (const c of catalog.columns(ref.schema, ref.table) ?? []) {
        options.push({ label: c.name, type: 'property', detail: `${ref.alias || ref.table} · ${c.type}`, apply: nameApply(c.name), boost: 1 });
      }
    }
    return options.length ? { from, options, validFor: wordBefore } : null;
  };
}

export function dialect(type: string): SQLDialect {
  if (type === 'mysql' || type === 'clickhouse') return MySQL;
  if (type === 'mariadb') return MariaSQL;
  if (type === 'mssql') return MSSQL;
  if (type === 'oracle') return PLSQL;
  if (type === 'sqlite') return SQLite;
  if (type === 'trino' || type === 'odbc') return StandardSQL;
  return PostgreSQL;
}
