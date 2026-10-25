import { push } from 'svelte-spa-router';
import { wrap } from 'svelte-spa-router/wrap';
import Login from '@/pages/Login.svelte';
import Workspace from '@/pages/Workspace.svelte';
import { session } from '@/lib/stores/session.svelte';

const loggedIn = () => {
  if (session.user) return true;
  void push('/login');
  return false;
};

const admin = () => {
  if (session.user?.role === 'admin') return true;
  void push(session.user ? '/' : '/login');
  return false;
};

export const routes = {
  '/login': Login,
  '/': wrap({ component: Workspace, conditions: [loggedIn] }),
  '/admin': wrap({ asyncComponent: () => import('@/pages/admin/AdminLayout.svelte'), conditions: [admin] }),
  '/admin/*': wrap({ asyncComponent: () => import('@/pages/admin/AdminLayout.svelte'), conditions: [admin] }),
  '*': wrap({ component: Workspace, conditions: [loggedIn] }),
};
