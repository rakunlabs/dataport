// The dialogs of the connection tools: export, import, transfer and test
// data. The tree opens them with what was right-clicked.

export type ToolDialog = 'export' | 'import' | 'transfer' | 'generate';

interface Request {
  kind: ToolDialog;
  connectionId: string;
  schema: string;
  // Tables chosen in the tree; empty lets the dialog offer all of them.
  tables: string[];
  // Whether the schema was chosen in the tree; otherwise the dialog asks.
  schemaFixed: boolean;
}

export const toolDialog = $state<{ current: Request | null }>({ current: null });

export function openToolDialog(kind: ToolDialog, connectionId: string, schema?: string, tables: string[] = []) {
  toolDialog.current = { kind, connectionId, schema: schema ?? '', tables, schemaFixed: schema !== undefined };
}

export function closeToolDialog() {
  toolDialog.current = null;
}
