const hasUnsafeRepositoryCharacter = (value: string) =>
  Array.from(value).some(character => {
    const codePoint = character.codePointAt(0) || 0;
    return codePoint <= 31 || codePoint === 127 || character === '\\' || /\s/u.test(character);
  });

const normalizedRepositoryPath = (value: string): string | null => {
  if (
    !value ||
    value === '/' ||
    !value.startsWith('/') ||
    value.startsWith('//') ||
    hasUnsafeRepositoryCharacter(value)
  ) {
    return null;
  }

  let decoded = value;
  let settled = false;
  for (let index = 0; index < 4; index += 1) {
    let next: string;
    try {
      next = decodeURIComponent(decoded);
    } catch {
      return null;
    }
    if (
      !next.startsWith('/') ||
      next.startsWith('//') ||
      next.includes('?') ||
      next.includes('#') ||
      hasUnsafeRepositoryCharacter(next)
    ) {
      return null;
    }
    if (next.split('/').some(segment => segment === '.' || segment === '..')) return null;
    if (next === decoded) {
      settled = true;
      break;
    }
    decoded = next;
  }
  if (!settled) return null;

  const withoutSuffix = value.endsWith('.git') ? value.slice(0, -4) : value;
  return withoutSuffix && withoutSuffix !== '/' ? withoutSuffix : null;
};

const normalizedHTTPSRepository = (raw: string): string | null => {
  const schemeEnd = raw.indexOf('://');
  const rawPathStart = schemeEnd < 0 ? -1 : raw.indexOf('/', schemeEnd + 3);
  if (rawPathStart < 0 || !normalizedRepositoryPath(raw.slice(rawPathStart))) return null;

  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    return null;
  }
  if (
    parsed.protocol !== 'https:' ||
    !parsed.hostname ||
    parsed.username ||
    parsed.password ||
    parsed.search ||
    parsed.hash
  ) {
    return null;
  }
  const pathname = normalizedRepositoryPath(parsed.pathname);
  if (!pathname) return null;
  parsed.pathname = pathname;
  return parsed.toString();
};

/**
 * Converts common Git clone addresses to a browser-safe HTTPS repository URL.
 * Unknown schemes and ambiguous inputs remain plain text instead of becoming
 * a navigation target.
 */
export const repositoryWebURL = (value: unknown): string | null => {
  if (typeof value !== 'string') return null;
  const raw = value.trim();
  if (!raw || hasUnsafeRepositoryCharacter(raw)) return null;

  if (raw.toLowerCase().startsWith('https://')) return normalizedHTTPSRepository(raw);

  const scpLike = raw.match(/^git@([A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?):(.+)$/);
  if (scpLike && !scpLike[1].includes('..')) {
    const pathname = normalizedRepositoryPath(`/${scpLike[2]}`);
    if (pathname) return normalizedHTTPSRepository(`https://${scpLike[1]}${pathname}`);
  }

  let ssh: URL;
  try {
    ssh = new URL(raw);
  } catch {
    return null;
  }
  if (
    ssh.protocol !== 'ssh:' ||
    ssh.username !== 'git' ||
    ssh.password ||
    ssh.port ||
    !ssh.hostname ||
    ssh.search ||
    ssh.hash
  ) {
    return null;
  }
  const pathname = normalizedRepositoryPath(ssh.pathname);
  if (!pathname) return null;
  return normalizedHTTPSRepository(`https://${ssh.hostname}${pathname}`);
};
