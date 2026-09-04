import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MockAdapter from 'axios-mock-adapter';
import {
  LOG_BUFFER_MAX_BYTES,
  LOG_BUFFER_MAX_LINES,
  LOG_QUEUE_MAX_BYTES,
  LOG_QUEUE_MAX_LINES,
  LOG_STREAM_MAX_AUTOMATIC_RECONNECTS,
  LOG_TRUNCATION_NOTICE,
  useLog,
} from './useLog';
import { useAuthStore } from '@/stores/auth';
import { PERMISSIONS } from '@/types/auth';
import type { DeployingService } from '@/types/deploy';
import api from '@/config/api';

class FakeEventSource extends EventTarget {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];

  readonly url: string;
  readonly withCredentials: boolean;
  readyState = FakeEventSource.CONNECTING;
  transportErrorEvents = 0;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  constructor(url: string | URL, init?: EventSourceInit) {
    super();
    this.url = String(url);
    this.withCredentials = init?.withCredentials === true;
    this.addEventListener('open', event => this.onopen?.(event));
    this.addEventListener('message', event => this.onmessage?.(event as MessageEvent));
    this.addEventListener('error', event => {
      this.transportErrorEvents += 1;
      this.onerror?.(event);
    });
    FakeEventSource.instances.push(this);
  }

  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
}

const row: DeployingService = {
  id: 7,
  serviceName: 'api',
  branch: 'main',
  environment: 'prod',
  status: 'running',
  progress: 50,
  startTime: 'now',
  operator: 'server-user',
  taskId: 7,
  ciJobName: 'ci-api',
  ciBuildId: 11,
};

describe('log SSE authentication', () => {
  let mock: MockAdapter;

  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
    mock = new MockAdapter(api);
    setActivePinia(createPinia());
    const auth = useAuthStore();
    auth.$patch({
      status: 'authenticated',
      user: {
        id: '1',
        username: 'reader',
        display_name: 'Reader',
        auth_source: 'oidc',
        roles: ['viewer'],
        permissions: [PERMISSIONS.LOGS_READ],
      },
      csrfToken: 'csrf',
      expiresAt: '2099-01-01T00:00:00Z',
    });
  });

  afterEach(() => {
    mock.restore();
    vi.unstubAllGlobals();
  });

  it('uses cookies and permanently stops reconnecting after auth-expired', async () => {
    const auth = useAuthStore();
    const logs = useLog();
    logs.currentLog.value = row;

    const pending = logs.fetchLogs(row);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    const source = FakeEventSource.instances[0];
    expect(source.withCredentials).toBe(true);

    source.dispatchEvent(new MessageEvent('auth-expired', { data: '{"reason":"expired"}' }));
    await pending;

    expect(source.readyState).toBe(FakeEventSource.CLOSED);
    expect(auth.status).toBe('anonymous');
    await logs.retryFetchLogs();
    logs.startConnectionCheck();
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it('stops log delivery without logging out a user whose log permission is revoked', async () => {
    const auth = useAuthStore();
    const logs = useLog();
    logs.currentLog.value = row;

    const pending = logs.fetchLogs(row);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    const source = FakeEventSource.instances[0];
    auth.$patch({
      user: {
        id: '1',
        username: 'reader',
        display_name: 'Reader',
        auth_source: 'oidc',
        roles: ['viewer'],
        permissions: [],
      },
    });
    await pending;

    expect(source.readyState).toBe(FakeEventSource.CLOSED);
    expect(auth.status).toBe('authenticated');
    expect(auth.isAuthenticated).toBe(true);
    await logs.retryFetchLogs();
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it('probes the session and stops after a transport error reveals a 401', async () => {
    mock.onGet('/api/v1/auth/session').reply(401);
    const auth = useAuthStore();
    const logs = useLog();
    logs.currentLog.value = row;

    const pending = logs.fetchLogs(row);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    const source = FakeEventSource.instances[0];
    source.dispatchEvent(new Event('error'));
    expect(source.transportErrorEvents).toBe(1);
    await vi.waitFor(() => expect(auth.status).toBe('anonymous'));
    await pending;

    expect(source.readyState).toBe(FakeEventSource.CLOSED);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it('dispatches a server stream-error without also probing the transport session path', async () => {
    const logs = useLog();
    logs.currentLog.value = row;

    const pending = logs.fetchLogs(row);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    const source = FakeEventSource.instances[0];
    source.dispatchEvent(
      new MessageEvent('stream-error', { data: '{"code":"404","message":"not found"}' })
    );
    await pending;

    expect(source.readyState).toBe(FakeEventSource.CLOSED);
    expect(source.transportErrorEvents).toBe(0);
    expect(logs.ciLog.value).toContain('未找到日志信息');
    expect(mock.history.get).toHaveLength(0);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it('maps the backend upstream_error code and retries only through the semantic path', async () => {
    vi.useFakeTimers();
    mock.onGet('/api/v1/auth/session').reply(200, {
      code: 1,
      message: 'ok',
      result: {
        user: {
          id: '1',
          username: 'reader',
          display_name: 'Reader',
          auth_source: 'oidc',
          roles: ['viewer'],
          permissions: [PERMISSIONS.LOGS_READ],
        },
        csrf_token: 'csrf',
        expires_at: '2099-01-01T00:00:00Z',
      },
    });
    const logs = useLog();
    logs.currentLog.value = row;

    try {
      const pending = logs.fetchLogs(row);
      expect(FakeEventSource.instances).toHaveLength(1);
      const source = FakeEventSource.instances[0];
      source.dispatchEvent(
        new MessageEvent('message', {
          data: JSON.stringify({ code: 1, result: [] }),
          lastEventId: '262144',
        })
      );
      source.dispatchEvent(new MessageEvent('stream-error', { data: '{"code":"upstream_error"}' }));

      expect(source.readyState).toBe(FakeEventSource.CLOSED);
      expect(source.transportErrorEvents).toBe(0);
      expect(logs.ciLog.value).toContain('上游日志服务暂时不可用');
      expect(mock.history.get).toHaveLength(0);

      await vi.advanceTimersByTimeAsync(3000);
      expect(mock.history.get).toHaveLength(1);
      expect(FakeEventSource.instances).toHaveLength(2);
      expect(FakeEventSource.instances[1].url).toContain('start=262144');
      FakeEventSource.instances[1].dispatchEvent(
        new MessageEvent('end', { data: '{"reason":"completed"}' })
      );
      await pending;
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps a healthy CI stream alive when message and ping activity reset the silence timer', async () => {
    vi.useFakeTimers();
    const logs = useLog();
    logs.currentLog.value = row;

    try {
      const pending = logs.fetchLogs(row);
      expect(FakeEventSource.instances).toHaveLength(1);
      const source = FakeEventSource.instances[0];
      source.readyState = FakeEventSource.OPEN;
      source.dispatchEvent(new Event('open'));

      await vi.advanceTimersByTimeAsync(59_000);
      source.dispatchEvent(new MessageEvent('ping', { data: '{}', lastEventId: '7' }));
      await vi.advanceTimersByTimeAsync(59_000);
      expect(FakeEventSource.instances).toHaveLength(1);

      source.dispatchEvent(
        new MessageEvent('message', {
          data: JSON.stringify({ code: 1, result: ['healthy'] }),
          lastEventId: '42',
        })
      );
      await vi.advanceTimersByTimeAsync(59_000);
      expect(FakeEventSource.instances).toHaveLength(1);

      source.dispatchEvent(
        new MessageEvent('end', {
          data: '{"reason":"completed"}',
          lastEventId: '42',
        })
      );
      await pending;
    } finally {
      logs.cleanupLogsAndConnections();
      vi.useRealTimers();
    }
  });

  it('does not let the periodic connection check reopen a completed stream', async () => {
    vi.useFakeTimers();
    const logs = useLog();
    logs.currentLog.value = row;
    logs.logDialogVisible.value = true;

    try {
      const pending = logs.fetchLogs(row);
      expect(FakeEventSource.instances).toHaveLength(1);
      logs.startConnectionCheck();
      FakeEventSource.instances[0].dispatchEvent(
        new MessageEvent('end', { data: '{"reason":"completed"}', lastEventId: '42' })
      );
      await pending;

      await vi.advanceTimersByTimeAsync(31_000);
      expect(FakeEventSource.instances).toHaveLength(1);
    } finally {
      logs.cleanupLogsAndConnections();
      logs.stopConnectionCheck();
      vi.useRealTimers();
    }
  });

  it('closes immediately and resumes from the retained buffer and cursor when reopened', async () => {
    vi.useFakeTimers();
    const logs = useLog();
    logs.currentLog.value = row;
    logs.logDialogVisible.value = true;

    try {
      const pending = logs.fetchLogs(row);
      const first = FakeEventSource.instances[0];
      first.dispatchEvent(
        new MessageEvent('message', {
          data: JSON.stringify({ code: 1, result: ['before-close'] }),
          lastEventId: '42',
        })
      );
      first.dispatchEvent(
        new MessageEvent('end', { data: '{"reason":"max_duration"}', lastEventId: '42' })
      );

      logs.logDialogVisible.value = false;
      logs.handleLogDialogClose();
      await pending;
      expect(first.readyState).toBe(FakeEventSource.CLOSED);
      expect(logs.getActiveConnections()).toHaveLength(0);
      expect(logs.ciLog.value).toContain('before-close');

      await vi.advanceTimersByTimeAsync(120_000);
      expect(FakeEventSource.instances).toHaveLength(1);

      logs.logDialogVisible.value = true;
      logs.activeLogTab.value = 'ci';
      logs.handleLogDialogOpen();
      await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
      expect(FakeEventSource.instances[1].url).toContain('start=42');
      expect(logs.ciLog.value).toContain('before-close');
      FakeEventSource.instances[1].dispatchEvent(
        new MessageEvent('end', { data: '{"reason":"completed"}', lastEventId: '42' })
      );
    } finally {
      logs.cleanupLogsAndConnections();
      logs.stopConnectionCheck();
      vi.useRealTimers();
    }
  });

  it('catches health-check stream failures and shares the bounded reconnect budget', async () => {
    vi.useFakeTimers();
    mock.onGet('/api/v1/auth/session').reply(200, {
      code: 1,
      message: 'ok',
      result: {
        user: {
          id: '1',
          username: 'reader',
          display_name: 'Reader',
          auth_source: 'oidc',
          roles: ['viewer'],
          permissions: [PERMISSIONS.LOGS_READ],
        },
        csrf_token: 'csrf',
        expires_at: '2099-01-01T00:00:00Z',
      },
    });
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const logs = useLog();
    logs.currentLog.value = row;
    logs.logDialogVisible.value = true;
    logs.activeLogTab.value = 'ci';

    try {
      logs.startConnectionCheck();
      await vi.advanceTimersByTimeAsync(30_000);
      expect(FakeEventSource.instances).toHaveLength(1);

      for (let attempt = 2; attempt <= LOG_STREAM_MAX_AUTOMATIC_RECONNECTS; attempt += 1) {
        FakeEventSource.instances[attempt - 2].dispatchEvent(
          new MessageEvent('end', { data: '{"reason":"upstream_idle"}' })
        );
        await vi.advanceTimersByTimeAsync(3000 * attempt);
        expect(FakeEventSource.instances).toHaveLength(attempt);
      }

      FakeEventSource.instances[LOG_STREAM_MAX_AUTOMATIC_RECONNECTS - 1].dispatchEvent(
        new MessageEvent('end', { data: '{"reason":"upstream_idle"}' })
      );
      await vi.advanceTimersByTimeAsync(60_000);
      expect(FakeEventSource.instances).toHaveLength(LOG_STREAM_MAX_AUTOMATIC_RECONNECTS);
      expect(consoleError).toHaveBeenCalledWith('CI日志健康检查重连失败:', expect.any(Error));
    } finally {
      logs.cleanupLogsAndConnections();
      logs.stopConnectionCheck();
      consoleError.mockRestore();
      vi.useRealTimers();
    }
  });

  it.each(['max_duration', 'upstream_idle'])(
    'reconnects a CI stream from its cursor after %s',
    async reason => {
      vi.useFakeTimers();
      mock.onGet('/api/v1/auth/session').reply(200, {
        code: 1,
        message: 'ok',
        result: {
          user: {
            id: '1',
            username: 'reader',
            display_name: 'Reader',
            auth_source: 'oidc',
            roles: ['viewer'],
            permissions: [PERMISSIONS.LOGS_READ],
          },
          csrf_token: 'csrf',
          expires_at: '2099-01-01T00:00:00Z',
        },
      });
      const logs = useLog();
      logs.currentLog.value = row;

      try {
        const pending = logs.fetchLogs(row);
        expect(FakeEventSource.instances).toHaveLength(1);
        const first = FakeEventSource.instances[0];
        first.dispatchEvent(
          new MessageEvent('message', {
            data: JSON.stringify({ code: 1, result: [] }),
            lastEventId: '262144',
          })
        );
        first.dispatchEvent(
          new MessageEvent('end', {
            data: JSON.stringify({ reason }),
            lastEventId: '262144',
          })
        );

        expect(first.readyState).toBe(FakeEventSource.CLOSED);
        await vi.advanceTimersByTimeAsync(3000);
        expect(FakeEventSource.instances).toHaveLength(2);
        expect(FakeEventSource.instances[1].url).toContain('start=262144');
        FakeEventSource.instances[1].dispatchEvent(
          new MessageEvent('end', { data: '{"reason":"completed"}' })
        );
        await pending;
      } finally {
        logs.cleanupLogsAndConnections();
        vi.useRealTimers();
      }
    }
  );

  it('bounds planned max-duration rotations across the full open-dialog lifecycle', async () => {
    vi.useFakeTimers();
    mock.onGet('/api/v1/auth/session').reply(200, {
      code: 1,
      message: 'ok',
      result: {
        user: {
          id: '1',
          username: 'reader',
          display_name: 'Reader',
          auth_source: 'oidc',
          roles: ['viewer'],
          permissions: [PERMISSIONS.LOGS_READ],
        },
        csrf_token: 'csrf',
        expires_at: '2099-01-01T00:00:00Z',
      },
    });
    const logs = useLog();
    logs.currentLog.value = row;

    try {
      const pending = logs.fetchLogs(row);
      expect(FakeEventSource.instances).toHaveLength(1);
      for (let rotation = 0; rotation < LOG_STREAM_MAX_AUTOMATIC_RECONNECTS; rotation += 1) {
        const cursor = String(100 + rotation);
        FakeEventSource.instances[rotation].dispatchEvent(
          new MessageEvent('end', {
            data: '{"reason":"max_duration"}',
            lastEventId: cursor,
          })
        );
        await vi.advanceTimersByTimeAsync(3000 * (rotation + 1));
        expect(FakeEventSource.instances).toHaveLength(rotation + 2);
        expect(FakeEventSource.instances[rotation + 1].url).toContain(`start=${cursor}`);
      }

      const finalSource = FakeEventSource.instances[LOG_STREAM_MAX_AUTOMATIC_RECONNECTS];
      finalSource.dispatchEvent(
        new MessageEvent('end', {
          data: '{"reason":"max_duration"}',
          lastEventId: '999',
        })
      );
      await pending;
      await vi.advanceTimersByTimeAsync(120_000);
      expect(FakeEventSource.instances).toHaveLength(LOG_STREAM_MAX_AUTOMATIC_RECONNECTS + 1);
    } finally {
      logs.cleanupLogsAndConnections();
      vi.useRealTimers();
    }
  });

  it('does not reset the lifecycle budget for empty arrays or real log messages', async () => {
    vi.useFakeTimers();
    mock.onGet('/api/v1/auth/session').reply(200, {
      code: 1,
      message: 'ok',
      result: {
        user: {
          id: '1',
          username: 'reader',
          display_name: 'Reader',
          auth_source: 'oidc',
          roles: ['viewer'],
          permissions: [PERMISSIONS.LOGS_READ],
        },
        csrf_token: 'csrf',
        expires_at: '2099-01-01T00:00:00Z',
      },
    });
    const logs = useLog();
    const cdRow = { ...row, cdJobName: 'cd-api', cdBuildId: 12 };
    logs.currentLog.value = cdRow;
    logs.activeLogTab.value = 'cd';

    try {
      const pending = logs.fetchLogs(cdRow);
      for (let failure = 0; failure < LOG_STREAM_MAX_AUTOMATIC_RECONNECTS; failure += 1) {
        FakeEventSource.instances[failure].dispatchEvent(
          new MessageEvent('message', {
            data: JSON.stringify({
              code: 1,
              result: failure % 2 === 0 ? [] : [`recovered-${failure}`],
            }),
            lastEventId: String(88 + failure),
          })
        );
        FakeEventSource.instances[failure].dispatchEvent(
          new MessageEvent('end', {
            data: '{"reason":"upstream_idle"}',
            lastEventId: String(88 + failure),
          })
        );
        await vi.advanceTimersByTimeAsync(3000 * (failure + 1));
        expect(FakeEventSource.instances).toHaveLength(failure + 2);
      }

      const finalSource = FakeEventSource.instances[LOG_STREAM_MAX_AUTOMATIC_RECONNECTS];
      expect(finalSource.url).toContain(`start=${87 + LOG_STREAM_MAX_AUTOMATIC_RECONNECTS}`);
      finalSource.dispatchEvent(
        new MessageEvent('message', {
          data: JSON.stringify({ code: 1, result: [] }),
          lastEventId: '999',
        })
      );
      finalSource.dispatchEvent(
        new MessageEvent('end', { data: '{"reason":"upstream_idle"}', lastEventId: '999' })
      );
      await pending;
      await vi.advanceTimersByTimeAsync(120_000);
      expect(FakeEventSource.instances).toHaveLength(LOG_STREAM_MAX_AUTOMATIC_RECONNECTS + 1);
    } finally {
      logs.cleanupLogsAndConnections();
      vi.useRealTimers();
    }
  });

  it('applies sliding activity and cursor-resuming rotation to CD streams', async () => {
    vi.useFakeTimers();
    mock.onGet('/api/v1/auth/session').reply(200, {
      code: 1,
      message: 'ok',
      result: {
        user: {
          id: '1',
          username: 'reader',
          display_name: 'Reader',
          auth_source: 'oidc',
          roles: ['viewer'],
          permissions: [PERMISSIONS.LOGS_READ],
        },
        csrf_token: 'csrf',
        expires_at: '2099-01-01T00:00:00Z',
      },
    });
    const logs = useLog();
    const cdRow = { ...row, cdJobName: 'cd-api', cdBuildId: 12 };
    logs.currentLog.value = cdRow;
    logs.activeLogTab.value = 'cd';

    try {
      const pending = logs.fetchLogs(cdRow);
      expect(FakeEventSource.instances).toHaveLength(1);
      const first = FakeEventSource.instances[0];
      first.readyState = FakeEventSource.OPEN;
      first.dispatchEvent(new Event('open'));

      await vi.advanceTimersByTimeAsync(119_000);
      first.dispatchEvent(new MessageEvent('ping', { data: '{}', lastEventId: '17' }));
      await vi.advanceTimersByTimeAsync(119_000);
      expect(FakeEventSource.instances).toHaveLength(1);

      first.dispatchEvent(
        new MessageEvent('end', {
          data: '{"reason":"upstream_idle"}',
          lastEventId: '17',
        })
      );
      await vi.advanceTimersByTimeAsync(3000);
      expect(FakeEventSource.instances).toHaveLength(2);
      expect(FakeEventSource.instances[1].url).toContain('log_type=cd');
      expect(FakeEventSource.instances[1].url).toContain('start=17');
      FakeEventSource.instances[1].dispatchEvent(
        new MessageEvent('end', { data: '{"reason":"completed"}' })
      );
      await pending;
    } finally {
      logs.cleanupLogsAndConnections();
      vi.useRealTimers();
    }
  });

  it('bounds queued and retained logs while keeping the newest output', () => {
    const logs = useLog();
    const largeBatch = Array.from(
      { length: 5000 },
      (_, index) => `line-${index}-${'界'.repeat(180)}`
    );

    try {
      logs.addToUpdateQueue('ci', largeBatch);
      const queued = logs.updateQueue.value.find(update => update.type === 'ci')?.data ?? [];
      expect(queued.length).toBeLessThanOrEqual(LOG_QUEUE_MAX_LINES);
      expect(queued[0]).toBe(LOG_TRUNCATION_NOTICE);
      expect(new TextEncoder().encode(queued.join('\n') + '\n').byteLength).toBeLessThanOrEqual(
        LOG_QUEUE_MAX_BYTES
      );

      for (let batch = 0; batch < 8; batch += 1) {
        logs.batchUpdateLogs();
        logs.addToUpdateQueue(
          'ci',
          largeBatch.map(line => `${batch}-${line}`)
        );
      }
      logs.batchUpdateLogs();

      const retained = logs.ciLog.value;
      expect(retained.startsWith(LOG_TRUNCATION_NOTICE)).toBe(true);
      expect(retained).toContain('7-line-4999');
      expect(retained.split('\n').filter(Boolean).length).toBeLessThanOrEqual(LOG_BUFFER_MAX_LINES);
      expect(new TextEncoder().encode(retained).byteLength).toBeLessThanOrEqual(
        LOG_BUFFER_MAX_BYTES
      );
    } finally {
      logs.cleanupLogsAndConnections();
    }
  });
});
