// The standalone HTML report of a schema document, and the SQL that fixes
// an anomaly where one statement can.

import type { Anomaly, AnomalyKind, DocTable, SchemaDoc } from '@/lib/api/types';
import { t, fmtNumber, i18n } from '@/lib/i18n/index.svelte';
import { quoteIdent } from '@/lib/helper/copy';
import { cardHeight, edges, extent, HEAD, layout, ROW, W } from '@/lib/helper/diagramLayout';

// Most pressing first, as the anomaly list shows them.
export const anomalyKinds: AnomalyKind[] = [
  'no_primary_key',
  'unindexed_foreign_key',
  'implied_relationship',
  'duplicate_index',
  'nullable_unique',
  'null_string_default',
  'incrementing_columns',
  'single_column',
  'orphan_table',
];

function qualified(db: string, schema: string, name: string) {
  const q = quoteIdent(db, name);
  // SQLite and DuckDB files have a schema of their own the user rarely writes.
  return schema && db !== 'sqlite' ? `${quoteIdent(db, schema)}.${q}` : q;
}

/**
 * The statement that removes an anomaly, for the user to review and run;
 * "" where the fix needs a decision a statement cannot make.
 */
export function fixSQL(db: string, schema: string, a: Anomaly): string {
  if (db === 'clickhouse' || db === 'trino' || db === 'odbc') return '';
  const cols = (a.columns ?? []).map((c) => quoteIdent(db, c)).join(', ');
  const short = (s: string) => s.replace(/[^A-Za-z0-9_]+/g, '_').slice(0, 50);
  switch (a.kind) {
    case 'unindexed_foreign_key': {
      const name = quoteIdent(db, short(`ix_${a.table}_${(a.columns ?? []).join('_')}`));
      const where = db === 'sqlite' && schema ? `${quoteIdent(db, schema)}.${name}` : name;
      const on = db === 'sqlite' ? quoteIdent(db, a.table) : qualified(db, schema, a.table);
      return `CREATE INDEX ${where} ON ${on} (${cols});`;
    }
    case 'implied_relationship': {
      if (db === 'sqlite' || !a.ref || !a.ref_columns?.length) return '';
      const name = quoteIdent(db, short(`fk_${a.table}_${(a.columns ?? []).join('_')}`));
      const refCols = a.ref_columns.map((c) => quoteIdent(db, c)).join(', ');
      return `ALTER TABLE ${qualified(db, schema, a.table)}\n  ADD CONSTRAINT ${name} FOREIGN KEY (${cols}) REFERENCES ${qualified(db, schema, a.ref)} (${refCols});`;
    }
    case 'duplicate_index': {
      const second = a.ref?.split(' / ')[1];
      if (!second) return '';
      if (db === 'mysql' || db === 'mariadb') return `DROP INDEX ${quoteIdent(db, second)} ON ${qualified(db, schema, a.table)};`;
      if (db === 'mssql') return `DROP INDEX ${quoteIdent(db, second)} ON ${qualified(db, schema, a.table)};`;
      return `DROP INDEX ${qualified(db, schema, second)};`;
    }
    default:
      return '';
  }
}

export function reportName(doc: SchemaDoc) {
  return `${doc.schema || 'schema'}-docs.html`.replace(/[^\w.-]+/g, '_');
}

const esc = (s: string | number | undefined | null) =>
  String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);

const anchor = (name: string) => 't-' + encodeURIComponent(name).replace(/%/g, '_');

function link(doc: SchemaDoc, name: string) {
  return doc.tables.some((x) => x.name === name) ? `<a href="#${anchor(name)}">${esc(name)}</a>` : esc(name);
}

function diagramSVG(doc: SchemaDoc) {
  const tables = doc.tables;
  const pos = layout(tables);
  const { width, height } = extent(tables, pos);
  const cut = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + '…' : s);
  const cards = tables
    .map((tb) => {
      const p = pos[tb.name];
      if (!p) return '';
      const fks = new Set(tb.foreign_keys.flatMap((f) => f.columns));
      const rows = tb.columns
        .map((c, i) => {
          const y = HEAD + i * ROW + 14;
          const mark = c.primary_key ? '<tspan class="pk">⚷ </tspan>' : fks.has(c.name) ? '<tspan class="fk">↗ </tspan>' : '';
          return `<text x="10" y="${y}" class="${c.primary_key ? 'cn pkn' : 'cn'}">${mark}${esc(cut(c.name, 20))}</text><text x="${W - 10}" y="${y}" class="ct" text-anchor="end">${esc(cut(c.type, 14))}</text>`;
        })
        .join('');
      return `<a href="#${anchor(tb.name)}"><g transform="translate(${p.x},${p.y})"><rect width="${W}" height="${cardHeight(tb)}" rx="6" class="card"/><rect width="${W}" height="${HEAD}" rx="6" class="${tb.kind === 'table' ? 'head' : 'head view'}"/><text x="10" y="18" class="tn">${esc(cut(tb.name, 26))}</text>${rows}</g></a>`;
    })
    .join('');
  const lines = edges(tables, pos)
    .map((e) => `<path d="${e.d}" class="edge" marker-end="url(#arrow)"/>`)
    .join('');
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-20 -20 ${width + 80} ${height + 40}" width="${width + 80}" height="${height + 40}"><defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="#2f6be6"/></marker></defs>${cards}${lines}</svg>`;
}

function tableSection(doc: SchemaDoc, tb: DocTable) {
  const refs = new Map<string, string>();
  for (const fk of tb.foreign_keys) fk.columns.forEach((c, i) => refs.set(c, `${link(doc, fk.ref_table)}.${esc(fk.ref_columns[i] ?? '')}`));
  const yes = esc(t('common.yes'));
  const no = esc(t('common.no'));
  const cols = tb.columns
    .map(
      (c) =>
        `<tr><td>${c.primary_key ? '⚷' : ''}${refs.has(c.name) ? '↗' : ''}</td><td class="${c.primary_key ? 'b' : ''}">${esc(c.name)}</td><td class="m">${esc(c.type)}${c.extra ? ' ' + esc(c.extra) : ''}</td><td>${c.nullable ? yes : no}</td><td class="m">${esc(c.default)}</td><td>${refs.get(c.name) ?? ''}</td><td>${esc(c.comment)}</td></tr>`,
    )
    .join('');
  const parents = tb.foreign_keys.map((fk) => `<li>(${esc(fk.columns.join(', '))}) → ${link(doc, fk.ref_table)} (${esc(fk.ref_columns.join(', '))})</li>`).join('');
  const children = tb.referenced_by.map((r) => `<li>${link(doc, r.table)} (${esc(r.columns.join(', '))}) → (${esc(r.ref_columns.join(', '))})</li>`).join('');
  const idx = tb.indexes
    .map((ix) => `<tr><td>${ix.primary ? '⚷ ' : ''}${esc(ix.name)}</td><td class="m">${esc(ix.columns.join(', '))}</td><td>${ix.unique ? yes : ''}</td></tr>`)
    .join('');
  const issues = doc.anomalies.filter((a) => a.table === tb.name).map((a) => `<li>${esc(t(`docs.anomaly.${a.kind}.title`))}${a.columns?.length ? ` (${esc(a.columns.join(', '))})` : ''}</li>`).join('');
  const none = `<p class="muted">${esc(t('docs.none'))}</p>`;
  return `<section id="${anchor(tb.name)}" class="table">
<h3>${esc(tb.name)} <span class="chip">${esc(t(`docs.kinds.${tb.kind}`))}</span>${tb.rows !== undefined ? ` <span class="muted">~${esc(fmtNumber(tb.rows))} ${esc(t('docs.col.rows').toLowerCase())}</span>` : ''}</h3>
${tb.comment ? `<p>${esc(tb.comment)}</p>` : ''}
${issues ? `<ul class="warn">${issues}</ul>` : ''}
<table><thead><tr><th></th><th>${esc(t('docs.col.column'))}</th><th>${esc(t('docs.col.type'))}</th><th>${esc(t('docs.col.nullable'))}</th><th>${esc(t('docs.col.default'))}</th><th>${esc(t('docs.col.parents'))}</th><th>${esc(t('docs.col.comment'))}</th></tr></thead><tbody>${cols}</tbody></table>
<div class="two"><div><h4>${esc(t('docs.col.parents'))}</h4>${parents ? `<ul>${parents}</ul>` : none}</div><div><h4>${esc(t('docs.col.children'))}</h4>${children ? `<ul>${children}</ul>` : none}</div></div>
${tb.kind !== 'view' ? `<h4>${esc(t('docs.col.indexes'))}</h4>${idx ? `<table><thead><tr><th>${esc(t('docs.col.name'))}</th><th>${esc(t('docs.col.columns'))}</th><th>${esc(t('docs.col.unique'))}</th></tr></thead><tbody>${idx}</tbody></table>` : none}` : ''}
${tb.definition ? `<h4>${esc(t('docs.definition'))}</h4><pre>${esc(tb.definition)}</pre>` : ''}
<p class="top"><a href="#top">↑ ${esc(t('docs.report.contents'))}</a></p>
</section>`;
}

/** A single self-contained HTML file: overview, diagram, anomalies and a section per table. */
export function reportHTML(doc: SchemaDoc, connectionName: string, dbLabel: string): string {
  const tables = doc.tables;
  const rowsTotal = tables.some((x) => x.rows !== undefined) ? tables.reduce((n, x) => n + (x.rows ?? 0), 0) : undefined;
  const stats: [string, number | undefined][] = [
    ['tables', tables.filter((x) => x.kind === 'table').length],
    ['views', tables.filter((x) => x.kind !== 'table').length],
    ['columns', tables.reduce((n, x) => n + x.columns.length, 0)],
    ['foreignKeys', tables.reduce((n, x) => n + x.foreign_keys.length, 0)],
    ['indexes', tables.reduce((n, x) => n + x.indexes.length, 0)],
    ['anomalies', doc.anomalies.length],
    ['routines', doc.routines.length],
    ['rows', rowsTotal],
  ];
  const statHTML = stats
    .filter(([, v]) => v !== undefined)
    .map(([k, v]) => `<div class="stat"><div class="muted">${esc(t(`docs.stats.${k}`))}</div><div class="n">${esc(fmtNumber(v!))}</div></div>`)
    .join('');

  const index = tables
    .map(
      (tb) =>
        `<tr><td>${link(doc, tb.name)}</td><td>${esc(t(`docs.kinds.${tb.kind}`))}</td><td class="r">${tb.rows === undefined ? '—' : esc(fmtNumber(tb.rows))}</td><td class="r">${tb.columns.length}</td><td class="r">${tb.foreign_keys.length || ''}</td><td class="r">${tb.referenced_by.length || ''}</td><td>${esc(tb.comment)}</td></tr>`,
    )
    .join('');

  const groups = anomalyKinds
    .map((k) => {
      const items = doc.anomalies.filter((a) => a.kind === k);
      if (!items.length) return '';
      const li = items
        .map((a) => `<li>${link(doc, a.table)}${a.columns?.length ? ` <span class="m">(${esc(a.columns.join(', '))})</span>` : ''}${a.ref ? ` → ${a.kind === 'duplicate_index' || a.kind === 'nullable_unique' ? esc(a.ref) : link(doc, a.ref)}${a.ref_columns?.length ? ` <span class="m">(${esc(a.ref_columns.join(', '))})</span>` : ''}` : ''}</li>`)
        .join('');
      return `<h3>${esc(t(`docs.anomaly.${k}.title`))} <span class="muted">${items.length}</span></h3><p class="muted">${esc(t(`docs.anomaly.${k}.hint`))}</p><ul>${li}</ul>`;
    })
    .join('');

  const routines = doc.routines.length
    ? `<h2 id="routines">${esc(t('docs.sections.routines'))}</h2><table><thead><tr><th>${esc(t('docs.col.routine'))}</th><th>${esc(t('docs.col.kind'))}</th></tr></thead><tbody>${doc.routines.map((r) => `<tr><td>${esc(r.name)}<span class="m muted">${esc(r.signature)}</span></td><td>${esc(t(`docs.kinds.${r.kind}`))}</td></tr>`).join('')}</tbody></table>`
    : '';

  const title = t('docs.report.title', { schema: doc.schema });
  return `<!doctype html>
<html lang="${esc(document.documentElement.lang || 'en')}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="generator" content="dataport">
<title>${esc(title)}</title>
<style>
:root{color-scheme:light dark;--bg:#fff;--fg:#1f2329;--muted:#646b77;--line:#d8dbe2;--soft:#f3f4f6;--accent:#2f6be6;--warn:#b25e00;--type:#0b7a75}
@media (prefers-color-scheme:dark){:root{--bg:#1e1f22;--fg:#dfe1e5;--muted:#9a9ea6;--line:#393b40;--soft:#2b2d30;--accent:#548af7;--warn:#e5a54b;--type:#4fc4bc}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:13px/1.5 system-ui,-apple-system,'Segoe UI',Roboto,sans-serif}
main{max-width:1200px;margin:0 auto;padding:24px}h1{font-size:20px;margin:0 0 4px}h2{font-size:16px;margin:32px 0 8px;padding-top:8px;border-top:1px solid var(--line)}h3{font-size:14px;margin:18px 0 4px}h4{font-size:13px;margin:14px 0 4px}
a{color:var(--accent);text-decoration:none}a:hover{text-decoration:underline}.muted{color:var(--muted)}.m{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:12px}.b{font-weight:600}.r{text-align:right}
table{border-collapse:collapse;width:100%;margin:4px 0}th,td{border-bottom:1px solid var(--line);padding:5px 8px;text-align:left;vertical-align:top}th{background:var(--soft);font-weight:500;font-size:12px}
td.m,td:nth-child(3).m{color:var(--type)}.stats{display:grid;grid-template-columns:repeat(auto-fill,minmax(150px,1fr));gap:8px;margin:16px 0}.stat{border:1px solid var(--line);border-radius:6px;padding:10px}.stat .n{font-size:18px;font-weight:600}
.chip{font-size:11px;font-weight:400;border:1px solid var(--line);border-radius:4px;padding:0 5px;color:var(--muted)}.warn{color:var(--warn)}.two{display:grid;grid-template-columns:1fr 1fr;gap:16px}
.table{margin-top:28px;padding-top:4px;border-top:1px dashed var(--line)}pre{background:var(--soft);border:1px solid var(--line);border-radius:6px;padding:10px;overflow:auto}.top{font-size:12px}
.diagram{overflow:auto;border:1px solid var(--line);border-radius:6px;background:var(--soft);max-height:80vh}
.diagram .card{fill:var(--bg);stroke:var(--line)}.diagram .head{fill:#3574f01f}.diagram .head.view{fill:var(--soft)}.diagram .tn{font-size:12px;font-weight:600;fill:var(--fg)}.diagram .cn{font-size:11px;fill:var(--fg)}.diagram .pkn{font-weight:600}.diagram .pk{fill:var(--warn)}.diagram .fk{fill:var(--accent)}.diagram .ct{font:10px ui-monospace,Menlo,monospace;fill:var(--muted)}.diagram .edge{fill:none;stroke:#2f6be6;stroke-width:1.4;opacity:.6}
nav ul{columns:3;padding-left:18px}@media print{.diagram{max-height:none}}
</style>
</head>
<body>
<main id="top">
<h1>${esc(title)}</h1>
<p class="muted">${esc(t('docs.report.generatedBy', { connection: connectionName, db: dbLabel, time: new Date(doc.generated_at).toLocaleString(i18n.locale, { dateStyle: 'long', timeStyle: 'short' }) }))}${doc.truncated ? ' · ' + esc(t('docs.truncated', { n: tables.length, total: doc.table_count })) : ''}</p>
<div class="stats">${statHTML}</div>
<nav><ul><li><a href="#diagram">${esc(t('docs.report.diagram'))}</a></li><li><a href="#tables">${esc(t('docs.sections.tables'))}</a></li><li><a href="#anomalies">${esc(t('docs.sections.anomalies'))}</a></li>${doc.routines.length ? `<li><a href="#routines">${esc(t('docs.sections.routines'))}</a></li>` : ''}</ul></nav>
<h2 id="diagram">${esc(t('docs.report.diagram'))}</h2>
<div class="diagram">${diagramSVG(doc)}</div>
<h2 id="tables">${esc(t('docs.sections.tables'))}</h2>
<table><thead><tr><th>${esc(t('docs.col.table'))}</th><th>${esc(t('docs.col.kind'))}</th><th class="r">${esc(t('docs.col.rows'))}</th><th class="r">${esc(t('docs.col.columns'))}</th><th class="r">${esc(t('docs.col.parents'))}</th><th class="r">${esc(t('docs.col.children'))}</th><th>${esc(t('docs.col.comment'))}</th></tr></thead><tbody>${index}</tbody></table>
<h2 id="anomalies">${esc(t('docs.sections.anomalies'))}</h2>
${!doc.keys ? `<p class="muted">${esc(t('docs.noKeys'))}</p>` : ''}${groups || `<p class="muted">${esc(t('docs.noAnomalies'))}</p>`}
${routines}
${tables.map((tb) => tableSection(doc, tb)).join('\n')}
</main>
</body>
</html>
`;
}
