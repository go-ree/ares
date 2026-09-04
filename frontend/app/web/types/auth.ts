export const PERMISSIONS = {
  APPLICATIONS_READ: 'applications:read',
  APPLICATIONS_WRITE: 'applications:write',
  APP_CONFIGS_READ: 'app-configs:read',
  APP_CONFIGS_WRITE: 'app-configs:write',
  DOMAINS_READ: 'domains:read',
  DOMAINS_WRITE: 'domains:write',
  WORKFLOWS_READ: 'workflows:read',
  WORKFLOWS_WRITE: 'workflows:write',
  RELEASES_READ: 'releases:read',
  RELEASES_CREATE: 'releases:create',
  TASKS_READ: 'tasks:read',
  TASKS_WRITE: 'tasks:write',
  LOGS_READ: 'logs:read',
  KUBERNETES_READ: 'kubernetes:read',
  KUBERNETES_DEBUG: 'kubernetes:debug',
  SYSTEM_SETTINGS_READ: 'system-settings:read',
  SYSTEM_SETTINGS_WRITE: 'system-settings:write',
  USERS_READ: 'users:read',
  USERS_WRITE: 'users:write',
  AUDIT_READ: 'audit:read',
} as const;

export type Permission = (typeof PERMISSIONS)[keyof typeof PERMISSIONS];
export type BuiltInRole = 'viewer' | 'developer' | 'releaser' | 'admin';
export type AuthSource = 'oidc' | 'bootstrap';
export type AuthStatus = 'unknown' | 'loading' | 'authenticated' | 'anonymous';

export interface AuthUser {
  id: string;
  username: string;
  display_name: string;
  email?: string;
  auth_source: AuthSource;
  roles: BuiltInRole[];
  permissions: Permission[];
}

export interface ManagedUser {
  id: string;
  username: string;
  display_name: string;
  email?: string;
  auth_source: AuthSource;
  role: BuiltInRole;
  enabled: boolean;
  last_login_at?: string;
  created_at: string;
  updated_at: string;
}

export interface ManagedUserList {
  items: ManagedUser[];
  next_offset: number;
}

export interface UpdateManagedUserRequest {
  role?: BuiltInRole;
  enabled?: boolean;
}

export interface SessionSnapshot {
  user: AuthUser;
  expires_at: string;
  csrf_token: string;
}

export interface AuthOptions {
  oidc_enabled: boolean;
  local_login_enabled: boolean;
  bootstrap_available: boolean;
}

export interface LoginRequest {
  username: string;
  password: string;
}

export interface ChangePasswordRequest {
  current_password: string;
  new_password: string;
}

export interface BootstrapRequest extends LoginRequest {
  bootstrap_token: string;
  display_name: string;
}

export interface ApiEnvelope<T> {
  code: number;
  message: string;
  result: T;
  error?: unknown;
  help?: string;
}
