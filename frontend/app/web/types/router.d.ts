import 'vue-router';
import type { Permission } from '@/types/auth';

declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth?: boolean;
    publicOnly?: boolean;
    requiredPermissions?: Permission[];
  }
}
