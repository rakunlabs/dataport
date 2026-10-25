// Connections set to "users sign in with their own account" answer with
// credentials_required until the user saved one. A single app-wide dialog
// asks for it, and the request that needed it runs again.

import { api, ApiError } from '@/lib/api/client';
import type { Limits } from '@/lib/api/types';
import { ws } from '@/lib/stores/workspace.svelte';

interface Request {
  connectionId: string;
  reason: string;
  resolve: (ok: boolean) => void;
}

export const credentialState = $state<{ current: Request | null }>({ current: null });

// Several requests can fail at once (schemas, columns, the query); they all
// wait for the same dialog.
const pending = new Map<string, Promise<boolean>>();

export function askCredentials(connectionId: string, reason = ''): Promise<boolean> {
  const existing = pending.get(connectionId);
  if (existing) return existing;
  const p = new Promise<boolean>((resolve) => {
    credentialState.current?.resolve(false);
    credentialState.current = { connectionId, reason, resolve };
  }).finally(() => pending.delete(connectionId));
  pending.set(connectionId, p);
  return p;
}

export function closeCredentials(ok: boolean) {
  const cur = credentialState.current;
  credentialState.current = null;
  cur?.resolve(ok);
}

export function needsCredentials(e: unknown): boolean {
  return e instanceof ApiError && e.status === 428 && !!e.data?.credentials_required;
}

// withCredentials runs fn and, when the connection asks for the user's own
// account, shows the dialog and runs fn once more after it was saved.
export async function withCredentials<T>(connectionId: string, fn: () => Promise<T>): Promise<T> {
  try {
    return await fn();
  } catch (e) {
    if (!needsCredentials(e)) throw e;
    if (!(await askCredentials(connectionId, (e as ApiError).message))) throw e;
    return fn();
  }
}

// saveCredentials stores the user's account. A null password keeps the saved
// one, so only the username or the limits change.
export async function saveCredentials(connectionId: string, username: string, password: string | null, limits: Limits) {
  await api.put(`/connections/${connectionId}/credentials`, { username, password, limits });
  const c = ws.connections.find((c) => c.id === connectionId);
  if (c) {
    c.credential_user = username;
    c.my_limits = limits;
  }
}

export async function forgetCredentials(connectionId: string) {
  await api.del(`/connections/${connectionId}/credentials`);
  const c = ws.connections.find((c) => c.id === connectionId);
  if (c) {
    c.credential_user = '';
    c.my_limits = undefined;
  }
}
