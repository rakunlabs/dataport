// Calls of the connection tools that change something: a production
// database asks for confirmation first, as a typed query does.

import { api, ApiError } from '@/lib/api/client';
import { confirmAsked } from '@/lib/stores/confirm.svelte';
import { connection } from '@/lib/stores/workspace.svelte';
import { withCredentials } from '@/lib/stores/credentials.svelte';

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
