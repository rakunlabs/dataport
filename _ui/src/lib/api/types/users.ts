import type { Role } from '@/lib/api/types/auth';

export interface User {
  id: string;
  username: string;
  display_name: string;
  email: string;
  role: Role;
  disabled: boolean;
  created_at: number;
  updated_at: number;
  last_login_at: number;
  group_ids: string[];
  providers?: string[];
}

export interface Group {
  id: string;
  name: string;
  description: string;
  created_at: number;
  updated_at: number;
  member_ids: string[];
}
