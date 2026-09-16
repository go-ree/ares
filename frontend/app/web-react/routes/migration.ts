/**
 * Transition gate for the React stack (ADR-0008).
 *
 * Until a batch lands, the corresponding route exists only in the Vue stack, so
 * the shell keeps rendering the full permission structure — that is what the
 * permission parity test asserts — while marking not-yet-migrated destinations
 * as disabled instead of offering a dead link. Entries move out of this list as
 * each batch lands, and it disappears entirely once every route is migrated.
 */
const MIGRATED_ROUTES = new Set<string>(['/', '/forbidden']);

export const isMigrated = (path: string): boolean => MIGRATED_ROUTES.has(path);
