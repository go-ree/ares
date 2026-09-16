import { redirect } from 'react-router';
import type { LoaderFunctionArgs } from 'react-router';
import { normalizeReturnTo } from '@shared/utils/return-to';
import type { Permission } from '@shared/types/auth';
import { can, isAuthenticated, useAuthStore } from '../stores/auth';

const currentPath = (request: Request) => {
  const url = new URL(request.url);
  return `${url.pathname}${url.search}`;
};

/**
 * Only an unresolved session is probed. A confirmed anonymous state is reused so
 * ordinary navigation does not re-issue `/auth/session` on every route change;
 * this matches the Vue router guard, which awaited only `unknown`/`loading`.
 */
const settleSession = async () => {
  const { status } = useAuthStore.getState();
  if (status === 'unknown' || status === 'loading') {
    await useAuthStore.getState().ensureSession();
  }
};

// `redirect` only takes ResponseInit; a data-router loader redirect already
// replaces the history entry, matching the Vue guard's `replace: true`.
const loginRedirect = (request: Request) =>
  redirect(`/login?redirect=${encodeURIComponent(normalizeReturnTo(currentPath(request)))}`);

export const requireSession = async ({ request }: LoaderFunctionArgs) => {
  await settleSession();
  if (!isAuthenticated()) throw loginRedirect(request);
  return null;
};

export const requirePermissions =
  (required: Permission[]) =>
  async ({ request }: LoaderFunctionArgs) => {
    await settleSession();
    if (!isAuthenticated()) throw loginRedirect(request);
    if (required.some(permission => !can(permission))) {
      throw redirect(
        `/forbidden?from=${encodeURIComponent(normalizeReturnTo(currentPath(request)))}`
      );
    }
    return null;
  };

export const publicOnly = async ({ request }: LoaderFunctionArgs) => {
  await settleSession();
  if (!isAuthenticated()) return null;
  const url = new URL(request.url);
  return redirect(normalizeReturnTo(url.searchParams.get('redirect')));
};
