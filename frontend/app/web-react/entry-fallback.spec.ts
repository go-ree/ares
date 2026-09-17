import { describe, expect, it } from 'vitest';
import { reactEntryTarget } from '../../config/react-entry';

describe('React dev entry fallback', () => {
  it.each(['/', '/login', '/forbidden?from=%2Fsystem', '/next'])(
    'serves the React entry for browser navigation to %s',
    path => {
      expect(reactEntryTarget('GET', path, 'text/html,application/xhtml+xml')).toBe(
        `/react.html${path.includes('?') ? `?${path.split('?')[1]}` : ''}`
      );
    }
  );

  it('does not rewrite API, module, asset, or mutation requests', () => {
    expect(reactEntryTarget('GET', '/api/v1/auth/options', 'text/html')).toBeNull();
    expect(reactEntryTarget('GET', '/app/web-react/main.tsx', '*/*')).toBeNull();
    expect(reactEntryTarget('GET', '/ares.svg', 'image/svg+xml')).toBeNull();
    expect(reactEntryTarget('POST', '/login', 'text/html')).toBeNull();
  });
});
