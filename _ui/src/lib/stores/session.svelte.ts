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

export async function login(username: string, password: string) {
  session.user = await api.post<Me>('/auth/login', { username, password });
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
