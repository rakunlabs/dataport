import type { Notebook, NotebookCell, NotebookMapping } from '@/lib/api/notebooks';
import type { ConnectionView } from '@/lib/api/types';

// Import deliberately refuses Saz conversion semantics that cannot be
// represented losslessly. The user can then migrate those cells explicitly.
export function importNotebook(value: unknown, connections: Pick<ConnectionView, 'id' | 'name'>[]): Notebook {
  const parsed = value as Record<string, any>;
  const raw = parsed?.data?.content ? parsed.data : parsed;
  const source = raw?.document?.cells ?? raw?.content?.cells;
  if (!Array.isArray(source) || source.length > 100) throw new Error('Expected a dataport or Saz notebook with at most 100 cells.');
  const saz = !!raw.content;
  const cells: NotebookCell[] = source.map((c, i) => {
    if (!c || typeof c !== 'object') throw new Error(`Invalid cell ${i + 1}.`);
    const dependencies = saz ? c.dependency?.enabled ? c.dependency.names || [] : [] : c.dependencies || [];
    if (!Array.isArray(dependencies) || dependencies.some((d) => typeof d !== 'string')) throw new Error(`Cell ${i + 1}: dependencies must be an array of paths.`);
    if (c.timeout_sec != null && (typeof c.timeout_sec !== 'number' || c.timeout_sec < 0 || c.timeout_sec > 86400)) throw new Error(`Cell ${i + 1}: invalid timeout.`);
    const cell: NotebookCell = {
      id: crypto.randomUUID(), path: String(c.path || `cell${i + 1}`), description: String(c.description || ''),
      connection_id: saz ? connections.find((x) => x.name === c.db_type)?.id || '' : connections.find((x) => x.id === c.connection_id)?.id || '',
      sql: String(saz ? c.content || '' : c.sql || ''), enabled: c.enabled !== false, output: saz ? !!c.result : c.output !== false,
      collapsed: !!c.collapsed, template: saz ? !!c.template?.enabled : !!c.template,
      write: saz ? !!c.mode?.enabled || !c.result : !!c.write,
      max_rows: saz ? c.limit === 0 ? -1 : Number(c.limit || 0) : Number(c.max_rows || 0),
      dependencies,
      ...(c.timeout_sec == null ? {} : { timeout_sec: c.timeout_sec }),
    };
    if (!saz) {
      if (c.transfer) {
        const mappings = c.transfer.mappings || [];
        if (!Array.isArray(mappings) || mappings.some((m) => !m || typeof m.source !== 'string' || typeof m.target !== 'string')) throw new Error(`Cell ${i + 1}: invalid transfer mappings.`);
        cell.transfer = { ...c.transfer, mappings, connection_id: connections.find((x) => x.id === c.transfer.connection_id)?.id || '' };
      }
      return cell;
    }
    if (c.mode?.enabled) {
      if (c.mode.name !== 'transfer') throw new Error(`Cell ${cell.path}: unsupported Saz mode.`);
      const mode = c.mode;
      const mappings: NotebookMapping[] = [];
      if (mode.map_type?.enabled) {
        const columns = mode.map_type.column || {}, destinations = mode.map_type.destination || {};
        for (const name of new Set([...Object.keys(columns), ...Object.keys(destinations)])) {
          if (columns[name]) throw new Error(`Cell ${cell.path}, column ${name}: Saz source scan conversions require manual migration; they cannot be combined into a destination mapping.`);
          const dest = destinations[name];
          if (dest.encoding?.enabled && !['ISO 8859-1', 'ISO-8859-1'].includes(dest.encoding.coding)) throw new Error(`Cell ${cell.path}, column ${name}: unsupported source encoding ${dest.encoding.coding}.`);
          mappings.push({ source: name, target: name, type: dest.type || '', nullable: !!dest.nullable, template: dest.template?.enabled ? dest.template.value || '' : '', encoding: dest.encoding?.enabled ? 'ISO-8859-1' : '' });
        }
      }
      const table = String(mode.table || '').split('.');
      cell.transfer = {
        connection_id: connections.find((x) => x.name === mode.db_type)?.id || '', schema: table.slice(0, -1).join('.'), table: table.at(-1) || '',
        wipe: !!mode.wipe, batch: Math.max(1, Math.min(500, Number(mode.batch || 1))), keep_unmapped: true,
        skip_error: mode.skip_error?.enabled ? mode.skip_error.message || '' : '', mappings,
      };
      if (mode.skip_error?.enabled && !mode.skip_error.message) throw new Error(`Cell ${cell.path}: Saz's skip-all-errors mode needs an explicit error matcher before migration.`);
    }
    return cell;
  });
  const parameters = raw.document?.parameters || [];
  if (!Array.isArray(parameters) || parameters.some((p) => !p || typeof p.name !== 'string' || !['string', 'number', 'boolean', 'json'].includes(p.type))) throw new Error('Invalid parameter definitions.');
  return {
    id: '', owner_id: '', name: String(raw.name || 'Imported notebook'), path: String(raw.path || `notebook-${Date.now()}`),
    version: 0, published_version: 0, permission: 'owner', created_at: 0, updated_at: 0,
    document: { cells, parameters },
  };
}
