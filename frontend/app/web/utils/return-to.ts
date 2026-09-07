const DEFAULT_RETURN_TO = '/';

const hasControlCharacter = (value: string) =>
  Array.from(value).some(character => {
    const codePoint = character.codePointAt(0) || 0;
    return codePoint <= 31 || codePoint === 127;
  });

const hasUnsafeDecodedForm = (value: string) => {
  let candidate = value;
  for (let index = 0; index < 8; index += 1) {
    if (!candidate.startsWith('/') || candidate.startsWith('//')) return true;
    if (candidate.includes('\\') || hasControlCharacter(candidate)) return true;
    try {
      const decoded = decodeURIComponent(candidate);
      if (decoded === candidate) return false;
      candidate = decoded;
    } catch {
      return true;
    }
  }
  return true;
};

/**
 * Accept only an absolute path on the current origin. Repeated decoding catches
 * values such as /%2f%2fevil.example before they reach either router or server.
 */
export const normalizeReturnTo = (value: unknown, fallback = DEFAULT_RETURN_TO): string => {
  if (typeof value !== 'string' || !value || hasUnsafeDecodedForm(value)) return fallback;

  try {
    const base = typeof window === 'undefined' ? 'http://localhost' : window.location.origin;
    const parsed = new URL(value, base);
    if (parsed.origin !== base || parsed.username || parsed.password) return fallback;
    // URL parsing resolves dot segments. Revalidate the normalized pathname so
    // an input such as /%2e%2e//evil.example cannot become a scheme-relative URL.
    if (!parsed.pathname.startsWith('/') || parsed.pathname.startsWith('//')) return fallback;
    if (parsed.pathname === '/login' || parsed.pathname.startsWith('/api/')) return fallback;
    const normalized = `${parsed.pathname}${parsed.search}${parsed.hash}`;
    // Dot-segment resolution can expose encoded separators that were not at
    // the beginning of the original input. Apply the same decoding checks to
    // the normalized output before returning it to router or OIDC code.
    if (!normalized || hasUnsafeDecodedForm(normalized)) return fallback;
    return normalized;
  } catch {
    return fallback;
  }
};
