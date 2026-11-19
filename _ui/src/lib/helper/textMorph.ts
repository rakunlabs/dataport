// Text changes of the editor's Format menu, after DBeaver's: a selection
// turned into a delimited list (a column pasted from a sheet becomes
// ('a','b','c')), and spaces trimmed from the ends of lines.

export interface ListOptions {
  // Characters any of which ends an item; \n stands for every line break.
  split: string;
  // Put around each item; empty for none.
  quote: string;
  // A quote inside an item is doubled, as SQL writes it.
  escape: boolean;
  // Put between items.
  join: string;
  // Put before the first and after the last item.
  prefix: string;
  suffix: string;
  // Each item once, in the order first seen.
  unique: boolean;
  // A new line starts once a line would pass this column; 0 never.
  wrap: number;
}

export const defaultListOptions: ListOptions = {
  split: '\n\t,',
  quote: "'",
  escape: true,
  join: ', ',
  prefix: '(',
  suffix: ')',
  unique: false,
  wrap: 0,
};

// listItems splits text at any of the split characters; items are trimmed
// and empty ones dropped.
export function listItems(text: string, split: string): string[] {
  const chars = new Set([...split]);
  if (chars.has('\n')) chars.add('\r');
  const out: string[] = [];
  let cur = '';
  for (const ch of text) {
    if (chars.has(ch)) {
      if (cur.trim()) out.push(cur.trim());
      cur = '';
    } else cur += ch;
  }
  if (cur.trim()) out.push(cur.trim());
  return out;
}

export function morphList(text: string, o: ListOptions): string {
  let items = listItems(text, o.split);
  if (o.unique) items = [...new Set(items)];
  const q = o.quote;
  const parts = items.map((it) => (q ? q + (o.escape ? it.split(q).join(q + q) : it) + q : it));
  let out = o.prefix;
  let col = out.length;
  parts.forEach((p, i) => {
    if (i > 0) {
      // The break goes after the delimiter, with its trailing space dropped.
      if (o.wrap > 0 && col + o.join.length + p.length > o.wrap) {
        out += o.join.trimEnd() + '\n';
        col = 0;
      } else {
        out += o.join;
        col += o.join.length;
      }
    }
    out += p;
    col += p.length;
  });
  return out + o.suffix;
}

export type TrimMode = 'both' | 'leading' | 'trailing';

// trimSpans gives the spaces and tabs to remove from one line, as offsets
// into it; a line of spaces only goes whole.
export function trimSpans(line: string, mode: TrimMode): { from: number; to: number }[] {
  if (!/[^ \t]/.test(line)) return line ? [{ from: 0, to: line.length }] : [];
  const out: { from: number; to: number }[] = [];
  const lead = /^[ \t]+/.exec(line);
  if (lead && mode !== 'trailing') out.push({ from: 0, to: lead[0].length });
  const trail = /[ \t]+$/.exec(line);
  if (trail && mode !== 'leading') out.push({ from: trail.index, to: line.length });
  return out;
}
