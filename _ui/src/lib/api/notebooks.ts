import { api, qs } from './client';
import type { ExecResult } from './types';

export interface NotebookCell {
  transfer?: NotebookTransfer;
  id: string;
  path: string;
  description: string;
  connection_id: string;
  sql: string;
  enabled: boolean;
  output: boolean;
  collapsed: boolean;
  template: boolean;
  write: boolean;
  max_rows: number;
  timeout_sec?: number;
  dependencies: string[];
}
export interface NotebookMapping { source: string; target: string; type: string; nullable: boolean; template: string; encoding: string }
export interface NotebookTransfer { connection_id: string; schema: string; table: string; wipe: boolean; batch: number; skip_error: string; mappings: NotebookMapping[]; keep_unmapped?: boolean }
export interface NotebookParameter { name: string; type: 'string' | 'number' | 'boolean' | 'json'; required: boolean; default?: unknown }
export interface NotebookDocument { cells: NotebookCell[]; parameters: NotebookParameter[] }
export interface Notebook {
  id: string;
  owner_id: string;
  name: string;
  path: string;
  document: NotebookDocument;
  version: number;
  published_version: number;
  permission: 'owner' | 'edit' | 'run' | 'view';
  created_at: number;
  updated_at: number;
}
export interface NotebookCellResult {
  path: string;
  status: string;
  error?: string;
  duration_ms: number;
  result?: ExecResult;
  sql?: string;
  args?: unknown[];
  confirmation?: string;
}
export interface NotebookResult { id: string; status: string; error?: string; cells: NotebookCellResult[]; duration_ms: number }
export interface NotebookRun {
  id: string;
  status: string;
  version: number;
  source: string;
  created_at: number;
  updated_at: number;
  info: { cells?: NotebookCellResult[]; error?: string; duration_ms?: number };
}
export interface NotebookAccess { subject_type: string; subject_id: string; permission: string }
export interface NotebookSubject { id: string; name: string; type: string }
export interface NotebookContent { name: string; path: string; document: NotebookDocument }
export interface NotebookRunRequest { version: number; cell: string; background: boolean; data: Record<string, unknown>; preview: boolean; confirmations: Record<string, string> }

export const listNotebooks = () => api.get<Notebook[]>('/notebooks');
export const getNotebook = (id: string) => api.get<Notebook>(`/notebooks/${id}`);
export const createNotebook = (body: NotebookContent) => api.post<Notebook>('/notebooks', body);
export const updateNotebook = (id: string, body: NotebookContent & { version: number }) => api.put<Notebook>(`/notebooks/${id}`, body);
export const deleteNotebook = (id: string) => api.del(`/notebooks/${id}`);
export const publishNotebook = (id: string, version: number, publish: boolean) => api.post<Notebook>(`/notebooks/${id}/publish`, { version, publish });
export const runNotebook = (id: string, body: NotebookRunRequest, signal?: AbortSignal) => api.post<NotebookResult | NotebookRun>(`/notebooks/${id}/run`, body, signal);
export const listNotebookRuns = (id: string, offset: number, limit = 25) => api.get<NotebookRun[]>(`/notebooks/${id}/runs${qs({ limit, offset })}`);
export const cancelNotebookRun = (id: string, runId: string) => api.post(`/notebooks/${id}/runs/${runId}/cancel`);
export const getNotebookAccess = (id: string) => api.get<NotebookAccess[]>(`/notebooks/${id}/access`);
export const setNotebookAccess = (id: string, access: NotebookAccess[]) => api.put<NotebookAccess[]>(`/notebooks/${id}/access`, access);
export const getNotebookSubjects = (id: string) => api.get<NotebookSubject[]>(`/notebooks/${id}/subjects`);

export function newCell(connectionId = '', index = 1, sql = ''): NotebookCell {
  return { id: crypto.randomUUID(), path: `cell${index}`, description: '', connection_id: connectionId, sql: sql || 'SELECT 1;', enabled: true, output: true, collapsed: false, template: false, write: false, max_rows: 0, dependencies: [] };
}
