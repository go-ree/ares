import { describe, expect, it } from 'vitest';
import { legacyTaskLogStreamUrl, taskStepLogStreamUrl } from './deploy';

describe('deploy log stream URLs', () => {
  it('encodes the canonical task step and opaque cursor without executor-controlled fields', () => {
    const raw = taskStepLogStreamUrl(12, 'folder.build_1-log', 'opaque /+=雪');
    const url = new URL(raw, 'http://ares.test');

    expect(url.pathname).toBe('/api/v1/tasks/12/steps/folder.build_1-log/logs/stream');
    expect(url.searchParams.get('cursor')).toBe('opaque /+=雪');
    expect([...url.searchParams.keys()]).toEqual(['cursor']);
    expect(raw).not.toContain('job');
    expect(raw).not.toContain('build_id');
    expect(raw).not.toContain('address');
  });

  it('keeps the deprecated v1 request task-scoped and only forwards numeric offsets', () => {
    expect(legacyTaskLogStreamUrl(12, 'ci', '42')).toBe(
      '/api/v1/job/stream/log?task_id=12&log_type=ci&start=42'
    );
    expect(legacyTaskLogStreamUrl(12, 'cd', 'opaque')).toBe(
      '/api/v1/job/stream/log?task_id=12&log_type=cd'
    );
  });
});
