// Reading CSV and JSON files in the browser for an import.

export interface ParsedFile {
  columns: string[];
  rows: unknown[][];
}

// detectDelimiter picks the separator that splits the first lines most
// evenly.
export function detectDelimiter(text: string): string {
  const lines = text.split(/\r?\n/, 6).filter((l) => l.trim());
  let best = ',';
  let score = 0;
  for (const d of [',', ';', '\t', '|']) {
    const counts = lines.map((l) => l.split(d).length - 1);
    const min = Math.min(...counts);
    if (min > score) {
      score = min;
      best = d;
    }
  }
  return best;
}

// parseCSV reads RFC 4180 CSV: quoted fields may hold the separator, line
// breaks and doubled quotes.
export function parseCSV(text: string, delimiter: string): string[][] {
  if (text.charCodeAt(0) === 0xfeff) text = text.slice(1);
  const out: string[][] = [];
  let row: string[] = [];
  let field = '';
  let quoted = false;
  let i = 0;
  const n = text.length;

  while (i < n) {
    const c = text[i];
    if (quoted) {
      if (c === '"') {
        if (text[i + 1] === '"') {
          field += '"';
          i += 2;
          continue;
        }
        quoted = false;
        i++;
        continue;
      }
      field += c;
      i++;
      continue;
    }
    if (c === '"' && field === '') {
      quoted = true;
      i++;
    } else if (c === delimiter) {
      row.push(field);
      field = '';
      i++;
    } else if (c === '\n' || c === '\r') {
      row.push(field);
      field = '';
      if (row.length > 1 || row[0] !== '') out.push(row);
      row = [];
      i += c === '\r' && text[i + 1] === '\n' ? 2 : 1;
    } else {
      field += c;
      i++;
    }
  }
  if (field !== '' || row.length) {
    row.push(field);
    out.push(row);
  }
  return out;
}

export function fromCSV(text: string, delimiter: string, header: boolean): ParsedFile {
  const all = parseCSV(text, delimiter);
  const width = Math.max(0, ...all.map((r) => r.length));
  const columns = header && all.length ? all[0].map((c, i) => c.trim() || `column${i + 1}`) : Array.from({ length: width }, (_, i) => `column${i + 1}`);
  while (columns.length < width) columns.push(`column${columns.length + 1}`);
  const rows = (header ? all.slice(1) : all).map((r) => columns.map((_, i) => r[i] ?? ''));
  return { columns, rows };
}

// fromJSON reads an array of objects, or of arrays with a first row of
// names; objects nested in a value are kept as JSON text.
export function fromJSON(text: string): ParsedFile {
  let data = JSON.parse(text);
  if (!Array.isArray(data)) {
    const inner = data && typeof data === 'object' ? Object.values(data).find(Array.isArray) : undefined;
    if (!inner) throw new Error('expected an array of rows');
    data = inner;
  }
  if (!data.length) return { columns: [], rows: [] };
  if (Array.isArray(data[0])) {
    const [head, ...rest] = data as unknown[][];
    const columns = head.map((c, i) => String(c ?? `column${i + 1}`));
    return { columns, rows: rest.map((r) => columns.map((_, i) => r[i] ?? null)) };
  }
  const columns: string[] = [];
  const seen = new Set<string>();
  for (const o of data.slice(0, 1000)) {
    for (const k of Object.keys(o ?? {})) {
      if (!seen.has(k)) {
        seen.add(k);
        columns.push(k);
      }
    }
  }
  return { columns, rows: data.map((o: Record<string, unknown>) => columns.map((c) => o?.[c] ?? null)) };
}

// guessType names the generic type of a column from its values, as the
// server maps it for every database.
export function guessType(values: unknown[]): string {
  let kind = '';
  // Numbers widen to decimal and dates to timestamps; anything else mixed
  // is text.
  const widen = (k: string) => {
    if (!kind || kind === k) kind = k;
    else if (['bigint', 'decimal'].includes(kind) && ['bigint', 'decimal'].includes(k)) kind = 'decimal';
    else if (['date', 'timestamp'].includes(kind) && ['date', 'timestamp'].includes(k)) kind = 'timestamp';
    else kind = 'text';
  };
  for (const v of values.slice(0, 2000)) {
    if (v === null || v === undefined || v === '') continue;
    if (typeof v === 'boolean') widen('bool');
    else if (typeof v === 'number') widen(Number.isInteger(v) ? 'bigint' : 'decimal');
    else if (typeof v === 'object') widen('text');
    else {
      const s = String(v).trim();
      if (/^-?\d{1,18}$/.test(s)) widen('bigint');
      else if (/^-?\d+\.\d+$/.test(s)) widen('decimal');
      else if (/^(true|false)$/i.test(s)) widen('bool');
      else if (/^\d{4}-\d{2}-\d{2}$/.test(s)) widen('date');
      else if (/^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?$/.test(s)) widen('timestamp');
      else widen('text');
    }
    if (kind === 'text') break;
  }
  return kind || 'text';
}

export const genericTypes = ['text', 'bigint', 'decimal', 'double', 'bool', 'date', 'timestamp', 'timestamptz', 'json'];
