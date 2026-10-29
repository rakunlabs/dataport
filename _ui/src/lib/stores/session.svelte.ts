import { api, onUnauthorized } from '@/lib/api/client';
import type { Me } from '@/lib/api/types';

export const session = $state<{ user: Me | null; ready: boolean }>({ user: null, ready: false });

export async function loadSession() {
  try {
    session.user = await api.get<Me>('/me');
  } catch {
    session.user = null;
  } finally {
    session.ready = true;
  }
}

export async function login(username: string, password: string, remember = false) {
  session.user = await api.post<Me>('/auth/login', { username, password, remember });
}

export async function logout() {
  try {
    await api.post('/auth/logout');
  } finally {
    session.user = null;
    window.location.hash = '#/login';
  }
}

onUnauthorized(() => {
  if (session.user) {
    session.user = null;
    window.location.hash = '#/login';
  }
});

export const isAdmin = () => session.user?.role === 'admin';

// A page that needs sign-in is kept, so the user lands back on it after
// signing in, also through SSO, which returns to the start page.
const returnKey = 'dataport.returnTo';

export function keepReturn(to: string) {
  sessionStorage.setItem(returnKey, to);
}

// pendingReturn reads the kept page without taking it.
export function pendingReturn(): string {
  return sessionStorage.getItem(returnKey) ?? '';
}

export function takeReturn(): string {
  const to = sessionStorage.getItem(returnKey) ?? '';
  sessionStorage.removeItem(returnKey);
  // Only a page of the app, never another site.
  return to.startsWith('/') && !to.startsWith('//') ? to : '';
}
