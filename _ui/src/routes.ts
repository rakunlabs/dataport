import { push, replace } from 'svelte-spa-router';
import { wrap } from 'svelte-spa-router/wrap';
import Login from '@/pages/Login.svelte';
import Workspace from '@/pages/Workspace.svelte';
import OAuthAuthorize from '@/pages/OAuthAuthorize.svelte';
import { keepReturn, session, takeReturn } from '@/lib/stores/session.svelte';
import { openSettings } from '@/lib/stores/workspace.svelte';

const loggedIn = (detail?: { location: string; querystring: string }) => {
  if (session.user) {
    // Back from an SSO sign-in started on a kept page.
    const to = detail?.location === '/' ? takeReturn() : '';
    if (to) void replace(to);
    return true;
  }
  if (detail && detail.location !== '/') keepReturn(detail.location + (detail.querystring ? '?' + detail.querystring : ''));
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
  // An MCP client asks the user to approve it (OAuth).
  '/oauth/authorize': wrap({ component: OAuthAuthorize, conditions: [loggedIn] }),
  '/': wrap({ component: Workspace, conditions: [loggedIn] }),
  // Administration lives in the settings tab of the workspace; old links
  // land there.
  '/admin': wrap({ component: Workspace, conditions: [admin, toSettings] }),
  '/admin/*': wrap({ component: Workspace, conditions: [admin, toSettings] }),
  '*': wrap({ component: Workspace, conditions: [loggedIn] }),
};
