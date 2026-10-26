// Placement of the cards and lines of a schema diagram, shared by the
// diagram tab and the documentation export.

import type { DiagramTable } from '@/lib/api/types';

// Card geometry, in diagram units.
export const W = 220;
export const HEAD = 28;
export const ROW = 20;
const GAP_X = 70;
const GAP_Y = 50;

export type Positions = Record<string, { x: number; y: number }>;

export function cardHeight(tb: DiagramTable) {
  return HEAD + Math.max(1, tb.columns.length) * ROW + 6;
}

// Places tables in columns of similar height. Tables linked by keys are
// kept next to each other: a breadth-first walk from the most connected
// table gives the order.
export function layout(tables: DiagramTable[]): Positions {
  const byName = new Map(tables.map((x) => [x.name, x]));
  const links = new Map<string, Set<string>>();
  for (const tb of tables) links.set(tb.name, new Set());
  for (const tb of tables)
    for (const fk of tb.foreign_keys)
      if (byName.has(fk.ref_table) && fk.ref_table !== tb.name) {
        links.get(tb.name)!.add(fk.ref_table);
        links.get(fk.ref_table)!.add(tb.name);
      }

  const order: string[] = [];
  const seen = new Set<string>();
  const start = [...tables].sort((a, b) => links.get(b.name)!.size - links.get(a.name)!.size || a.name.localeCompare(b.name));
  for (const s of start) {
    if (seen.has(s.name)) continue;
    const queue = [s.name];
    seen.add(s.name);
    while (queue.length) {
      const n = queue.shift()!;
      order.push(n);
      for (const m of [...links.get(n)!].sort()) {
        if (!seen.has(m)) {
          seen.add(m);
          queue.push(m);
        }
      }
    }
  }

  const cols = Math.max(1, Math.ceil(Math.sqrt(order.length * 1.4)));
  const colY = Array(cols).fill(0);
  const next: Positions = {};
  for (const name of order) {
    // The shortest column takes the next table.
    let c = 0;
    for (let i = 1; i < cols; i++) if (colY[i] < colY[c]) c = i;
    next[name] = { x: c * (W + GAP_X), y: colY[c] };
    colY[c] += cardHeight(byName.get(name)!) + GAP_Y;
  }
  return next;
}

export interface Edge {
  d: string;
  key: string;
  from: string;
  to: string;
}

// A line from the referencing column to the referenced one, leaving and
// entering the cards on the facing sides.
export function edges(tables: DiagramTable[], pos: Positions): Edge[] {
  const byName = new Map(tables.map((x) => [x.name, x]));
  const out: Edge[] = [];
  for (const tb of tables) {
    for (const fk of tb.foreign_keys) {
      const ref = byName.get(fk.ref_table);
      const a = pos[tb.name];
      const b = ref && pos[ref.name];
      if (!ref || !a || !b) continue;
      const ci = Math.max(0, tb.columns.findIndex((c) => c.name === fk.columns[0]));
      const ri = Math.max(0, ref.columns.findIndex((c) => c.name === fk.ref_columns[0]));
      const y1 = a.y + HEAD + ci * ROW + ROW / 2;
      const y2 = b.y + HEAD + ri * ROW + ROW / 2;
      let x1: number, x2: number, d: string;
      if (ref === tb) {
        x1 = a.x + W;
        d = `M${x1},${y1} C${x1 + 50},${y1} ${x1 + 50},${y2} ${x1},${y2}`;
      } else {
        const right = b.x >= a.x + W / 2;
        x1 = right ? a.x + W : a.x;
        x2 = right ? b.x : b.x + W;
        if (Math.abs(a.x - b.x) < W / 2) {
          x1 = a.x + W;
          x2 = b.x + W;
          const bend = Math.max(x1, x2) + 40;
          d = `M${x1},${y1} C${bend},${y1} ${bend},${y2} ${x2},${y2}`;
        } else {
          const mid = (x1 + x2) / 2;
          d = `M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`;
        }
      }
      out.push({ d, key: `${tb.name}.${fk.name}`, from: tb.name, to: ref.name });
    }
  }
  return out;
}

// Extent of the placed cards, for fitting them in a view.
export function extent(tables: DiagramTable[], pos: Positions) {
  let maxX = 0;
  let maxY = 0;
  for (const tb of tables) {
    const p = pos[tb.name];
    if (!p) continue;
    maxX = Math.max(maxX, p.x + W);
    maxY = Math.max(maxY, p.y + cardHeight(tb));
  }
  return { width: maxX, height: maxY };
}
