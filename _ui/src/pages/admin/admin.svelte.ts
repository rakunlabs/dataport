import { api } from '@/lib/api/client';
import type { Connection, DriverInfo, DriverList, Group, Limits, User } from '@/lib/api/types';
import { loadConnections as reloadWorkspace } from '@/lib/stores/workspace.svelte';

export const admin = $state({
  users: [] as User[],
  groups: [] as Group[],
  connections: [] as Connection[],
  drivers: [] as DriverInfo[],
  filesEnabled: false,
  odbcDialects: [] as string[],
  poolDefaults: {} as Limits,
});

export async function loadUsers() {
  admin.users = await api.get<User[]>('/admin/users');
}

export async function loadGroups() {
  admin.groups = await api.get<Group[]>('/admin/groups');
}

// The workspace sidebar is reloaded too, so a connection added or removed
// here shows up there without a page reload.
export async function loadConnections() {
  admin.connections = await api.get<Connection[]>('/admin/connections');
  void reloadWorkspace();
}

export async function loadDrivers() {
  if (admin.drivers.length) return;
  const [list, defaults] = await Promise.all([api.get<DriverList>('/admin/drivers'), api.get<Limits>('/admin/pool-defaults')]);
  admin.drivers = list.drivers.sort((a, b) => a.type.localeCompare(b.type));
  admin.filesEnabled = list.files_enabled;
  admin.odbcDialects = list.odbc_dialects;
  admin.poolDefaults = defaults;
}

export function userName(id: string): string {
  const u = admin.users.find((u) => u.id === id);
  return u ? u.display_name || u.username : id;
}

export function groupName(id: string): string {
  return admin.groups.find((g) => g.id === id)?.name ?? id;
}
