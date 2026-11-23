// Calls of the connection tools that change something: a production
// database asks for confirmation first, as a typed query does.

import { api, ApiError } from '@/lib/api/client';
import { confirmAsked } from '@/lib/stores/confirm.svelte';
import { connection } from '@/lib/stores/workspace.svelte';
import { askCredentials, needsCredentials, withCredentials } from '@/lib/stores/credentials.svelte';
import { readNdjson } from '@/lib/helper/ndjson';

// confirmed posts body to path; on the production prompt it shows what will
// happen and posts again with confirm set. null means the user declined.
// A shared session remembers the answer, so an import sent in several
// requests asks once.
export async function confirmed<T>(connId: string, path: string, body: Record<string, unknown>, what: string, session?: { ok: boolean }): Promise<T | null> {
  try {
    return await withCredentials(connId, () => api.post<T>(path, { ...body, confirm: !!session?.ok }));
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.data?.confirmation_required) {
      const ok = await confirmAsked(connection(connId)?.name ?? '', what, e.data);
      if (!ok) return null;
      if (session) session.ok = true;
      return withCredentials(connId, () => api.post<T>(path, { ...body, confirm: true }));
    }
    throw e;
  }
}

// confirmedStream is confirmed for a tool that answers with progress as
// newline-delimited JSON: each line goes to onLine as it arrives. It
// resolves with false when the user declined the production prompt or the
// account dialog. A refusal of the server throws an ApiError with its
// message. The prompt names confirmConn, which defaults to the connection
// asked for its account, connId.
export async function confirmedStream<T>(
  connId: string,
  path: string,
  body: (confirm: boolean) => Promise<Record<string, unknown>> | Record<string, unknown>,
  what: string,
  onLine: (line: T) => void,
  signal: AbortSignal,
  confirmConn = connId,
): Promise<boolean> {
  let confirm = false;
  for (;;) {
    const res = await fetch('./api/v1' + path, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ...(await body(confirm)), confirm }),
      signal,
    });
    if (res.ok && res.body) {
      await readNdjson<T>(res.body, onLine);
      return true;
    }
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    const err = new ApiError(res.status, String(data.message || res.statusText), data);
    if (!confirm && res.status === 409 && data.confirmation_required) {
      if (!(await confirmAsked(connection(confirmConn)?.name ?? '', what, data))) return false;
      confirm = true;
      continue;
    }
    if (needsCredentials(err)) {
      if (!(await askCredentials(connId, err.message))) throw err;
      continue;
    }
    throw err;
  }
}
