import { t as tr } from '@/lib/i18n/index.svelte';
import { StateField, RangeSet, type Extension } from '@codemirror/state';
import { EditorView, gutter, GutterMarker, Decoration, MatchDecorator, ViewPlugin, type DecorationSet, type ViewUpdate } from '@codemirror/view';
import { HighlightStyle } from '@codemirror/language';
import { PostgreSQL, MySQL, MariaSQL, MSSQL, PLSQL, SQLite, StandardSQL } from '@codemirror/lang-sql';
import { tags as t } from '@lezer/highlight';
import { statements, type Statement } from '@/lib/helper/statements';

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

export interface StatementFields {
  stmtField: StateField<Statement[]>;
  activeField: StateField<DecorationSet>;
}

// statementFields tracks the statements of the document and outlines the
// one under the cursor. dbType and highlight are read when the document or
// selection changes, so they follow the editor's current props.
export function statementFields(dbType: () => string, highlight: () => boolean): StatementFields {
  // The statements of the document, recomputed when it changes.
  const stmtField = StateField.define<Statement[]>({
    create: (st) => statements(st.doc.toString(), dbType()),
    update: (v, tr) => (tr.docChanged ? statements(tr.state.doc.toString(), dbType()) : v),
  });

  // The statement under the cursor is outlined, so it is clear what a
  // button or Ctrl+Enter without a selection runs.
  const activeMark = Decoration.mark({ class: 'cm-active-stmt' });
  const activeField = StateField.define<DecorationSet>({
    create: () => Decoration.none,
    update: (_, tr) => {
      const list = tr.state.field(stmtField);
      if (list.length < 2 || !highlight()) return Decoration.none;
      const pos = tr.state.selection.main.head;
      const s = list.find((x) => pos >= x.from && pos <= x.to + 1);
      return s ? Decoration.set([activeMark.range(s.from, s.to)]) : Decoration.none;
    },
    provide: (f) => EditorView.decorations.from(f),
  });

  return { stmtField, activeField };
}

export function runGutterExtension({ stmtField, activeField }: StatementFields, onrunstatement?: (sql: string, e: MouseEvent) => void): Extension {
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
            onrunstatement(s.text, e as MouseEvent);
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
  '.cm-active-stmt': { backgroundColor: 'var(--cm-active-stmt)' },
  '.cm-placeholder-var': { color: 'var(--cm-type)', backgroundColor: 'var(--cm-match)', borderRadius: '2px' },
  '.cm-fat-cursor': { backgroundColor: 'var(--color-accent) !important', color: 'var(--color-accent-fg) !important' },
  '.cm-vim-panel': { fontFamily: 'var(--font-mono)', fontSize: '12px', padding: '0 6px' },
});

export function dialect(type: string) {
  if (type === 'mysql' || type === 'clickhouse') return MySQL;
  if (type === 'mariadb') return MariaSQL;
  if (type === 'mssql') return MSSQL;
  if (type === 'oracle') return PLSQL;
  if (type === 'sqlite') return SQLite;
  if (type === 'trino' || type === 'odbc') return StandardSQL;
  return PostgreSQL;
}
