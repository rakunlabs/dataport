import { push, replace } from 'svelte-spa-router';
import { wrap } from 'svelte-spa-router/wrap';
import Login from '@/pages/Login.svelte';
import Workspace from '@/pages/Workspace.svelte';
import { session } from '@/lib/stores/session.svelte';
import { openSettings } from '@/lib/stores/workspace.svelte';

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

const toSettings = (detail: { location: string }) => {
  const section = detail.location.split('/')[2] || 'connections';
  openSettings(section);
  void replace('/');
  return true;
};

export const routes = {
  '/login': Login,
  '/': wrap({ component: Workspace, conditions: [loggedIn] }),
  // Administration lives in the settings tab of the workspace; old links
  // land there.
  '/admin': wrap({ component: Workspace, conditions: [admin, toSettings] }),
  '/admin/*': wrap({ component: Workspace, conditions: [admin, toSettings] }),
  '*': wrap({ component: Workspace, conditions: [loggedIn] }),
};
