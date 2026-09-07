import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MockAdapter from 'axios-mock-adapter';
import {
  LOG_BUFFER_MAX_BYTES,
  LOG_BUFFER_MAX_LINES,
  LOG_CACHE_MAX_BYTES,
  LOG_QUEUE_MAX_BYTES,
  LOG_STREAM_MAX_AUTOMATIC_RECONNECTS,
  LOG_TRUNCATION_NOTICE,
  boundLogText,
  taskLogTargets,
  useLog,
  type StepTaskLogTarget,
} from './useLog';
import type { TaskRecord, TaskStepRecord } from '@/models/deploy';
import type { DeployingService } from '@/types/deploy';
import { useAuthStore } from '@/stores/auth';
import { PERMISSIONS } from '@/types/auth';
import type { ApiEnvelope, SessionSnapshot } from '@/types/auth';
import api from '@/config/api';
import type {
  LogStreamFailure,
  LogStreamTransport,
  LogStreamTransportFactory,
} from '@/services/log-stream';

class FakeEventSource extends EventTarget {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];

  readonly url: string;
  readonly withCredentials: boolean;
  readyState = FakeEventSource.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  constructor(url: string | URL, init?: EventSourceInit) {
    super();
    this.url = String(url);
    this.withCredentials = init?.withCredentials === true;
    this.addEventListener('open', event => this.onopen?.(event));
    this.addEventListener('message', event => this.onmessage?.(event as MessageEvent));
    this.addEventListener('error', event => this.onerror?.(event));
    FakeEventSource.instances.push(this);
  }

  close() {
    this.readyState = FakeEventSource.CLOSED;
  }

  fail(failure: LogStreamFailure) {
    const event = new Event('error') as Event & { failure: LogStreamFailure };
    event.failure = failure;
    this.dispatchEvent(event);
  }
}

const fakeStepTransportFactory: LogStreamTransportFactory = url =>
  new FakeEventSource(url, { withCredentials: true }) as LogStreamTransport;

const useTestLog = () => useLog({ stepTransportFactory: fakeStepTransportFactory });

const row = (taskId = 7): DeployingService => ({
  id: taskId,
  serviceName: 'api',
  branch: 'main',
  environment: 'prod',
  status: 'running',
  progress: 50,
  startTime: 'now',
  operator: 'server-user',
  taskId,
});

const step = (stepKey: string, position: number, logs: boolean | undefined): TaskStepRecord => ({
  step_record_id: position + 1,
  task_id: 7,
  workflow_version_id: 3,
  step_key: stepKey,
  name: `Step ${stepKey}`,
  uses: logs ? 'jenkins.job@v1' : 'builtin.noop@v1',
  position,
  timeout_seconds: 60,
  on_failure: 'stop',
  status: 'running',
  attempt: 1,
  ...(logs === undefined ? {} : { capabilities: { logs, cancel: false } }),
  created_at: '2026-09-07T00:00:00Z',
  updated_at: '2026-09-07T00:00:00Z',
});

const task = (overrides: Partial<TaskRecord> = {}): TaskRecord => ({
  task_id: 7,
  app_name: 'api',
  branch: 'main',
  env: 'prod',
  publisher: 'server-user',
  status: 'running',
  message: '',
  auto_deploy: 0,
  products: '',
  engine_version: 2,
  workflow_version_id: 3,
  steps: [],
  created_at: '2026-09-07T00:00:00Z',
  updated_at: '2026-09-07T00:00:00Z',
  deleted_at: null,
  ...overrides,
});

const target = (stepKey = 'build', taskId = 7): StepTaskLogTarget => ({
  kind: 'step',
  taskId,
  stepKey,
  label: `Step ${stepKey}`,
});

const authenticatedSession = (): ApiEnvelope<SessionSnapshot> => ({
  code: 1,
  message: 'ok',
  result: {
    user: {
      id: '1',
      username: 'reader',
      display_name: 'Reader',
      auth_source: 'oidc',
      roles: ['viewer'],
      permissions: [PERMISSIONS.TASKS_READ, PERMISSIONS.LOGS_READ],
    },
    csrf_token: 'csrf',
    expires_at: '2099-01-01T00:00:00Z',
  },
});

describe('task log target contract', () => {
  it('uses only ordered capabilities.logs steps for v2 and never falls back to legacy fields', () => {
    const targets = taskLogTargets(
      task({
        steps: [step('deploy', 2, true), step('noop', 1, false), step('unknown', 0, undefined)],
        ci_job_name: 'must-not-fallback',
        ci_build_id: 42,
      })
    );

    expect(targets).toEqual([{ kind: 'step', taskId: 7, stepKey: 'deploy', label: 'Step deploy' }]);
  });

  it('isolates v1 CI/CD compatibility without exposing Jenkins references in targets', () => {
    const targets = taskLogTargets(
      task({
        engine_version: 1,
        steps: [step('ignored', 0, true)],
        ci_job_name: 'folder/build_api',
        ci_build_id: 42,
        cd_job_name: 'folder/deploy_api',
        cd_build_id: 43,
      })
    );

    expect(targets).toEqual([
      { kind: 'legacy', taskId: 7, legacyType: 'ci', label: 'CI 日志' },
      { kind: 'legacy', taskId: 7, legacyType: 'cd', label: 'CD 日志' },
    ]);
    expect(JSON.stringify(targets)).not.toContain('folder');
    expect(JSON.stringify(targets)).not.toContain('buildId');
  });
});

describe('generic task-step log SSE', () => {
  let mock: MockAdapter;

  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
    mock = new MockAdapter(api);
    setActivePinia(createPinia());
    const auth = useAuthStore();
    auth.$patch({
      status: 'authenticated',
      user: authenticatedSession().result.user,
      csrfToken: 'csrf',
      expiresAt: '2099-01-01T00:00:00Z',
    });
  });

  afterEach(() => {
    mock.restore();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('opens the canonical cookie-authenticated URL and preserves an opaque cursor across step switches', () => {
    const logs = useTestLog();
    logs.setCurrentLog(row());

    logs.openLogTarget(target('folder.step'));
    const first = FakeEventSource.instances[0];
    expect(first.url).toBe('/api/v1/tasks/7/steps/folder.step/logs/stream');
    expect(first.withCredentials).toBe(true);

    first.dispatchEvent(
      new MessageEvent('log', {
        data: JSON.stringify({ content: 'first\n', cursor: 'next /+=雪', eof: false }),
        lastEventId: 'next /+=雪',
      })
    );
    logs.flushQueuedContent();
    expect(logs.activeLog.value).toBe('first\n');

    logs.openLogTarget(target('deploy'));
    expect(first.readyState).toBe(FakeEventSource.CLOSED);
    const second = FakeEventSource.instances[1];
    expect(second.url).toBe('/api/v1/tasks/7/steps/deploy/logs/stream');

    logs.openLogTarget(target('folder.step'));
    expect(second.readyState).toBe(FakeEventSource.CLOSED);
    const resumed = FakeEventSource.instances[2];
    const resumedUrl = new URL(resumed.url, 'http://ares.test');
    expect(resumedUrl.pathname).toBe('/api/v1/tasks/7/steps/folder.step/logs/stream');
    expect(resumedUrl.searchParams.get('cursor')).toBe('next /+=雪');
    expect(logs.activeLog.value).toBe('first\n');
    logs.cleanupLogsAndConnections();
  });

  it('evicts least-recently-used step buffers under the aggregate cache limit', () => {
    const logs = useTestLog();
    logs.setCurrentLog(row());
    const targetCount = Math.floor(LOG_CACHE_MAX_BYTES / LOG_BUFFER_MAX_BYTES) + 1;
    const payload = 'x'.repeat(LOG_BUFFER_MAX_BYTES);

    for (let index = 0; index < targetCount; index += 1) {
      const currentTarget = target(`step-${index}`);
      logs.openLogTarget(currentTarget);
      for (let part = 0; part < LOG_BUFFER_MAX_BYTES / LOG_QUEUE_MAX_BYTES; part += 1) {
        logs.queueLogContent(currentTarget, payload.slice(0, LOG_QUEUE_MAX_BYTES));
        logs.flushQueuedContent();
      }
    }

    logs.openLogTarget(target('step-0'));
    expect(logs.activeLog.value).toBe('');
    expect(FakeEventSource.instances).toHaveLength(targetCount + 1);
    logs.cleanupLogsAndConnections();
  });

  it('rejects a generic log frame whose SSE id and payload cursor do not match', () => {
    vi.useFakeTimers();
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());
    const source = FakeEventSource.instances[0];

    source.dispatchEvent(
      new MessageEvent('log', {
        data: JSON.stringify({ content: 'must-not-append', cursor: 'payload-cursor', eof: false }),
        lastEventId: 'sse-cursor',
      })
    );

    expect(source.readyState).toBe(FakeEventSource.CLOSED);
    expect(logs.activeLog.value).toBe('');
    expect(logs.activeLogError.value).toBe('日志数据格式异常');
    logs.cleanupLogsAndConnections();
  });

  it('closes on dialog pause and resumes the retained buffer and cursor', () => {
    vi.useFakeTimers();
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());
    const first = FakeEventSource.instances[0];
    first.dispatchEvent(
      new MessageEvent('log', {
        data: JSON.stringify({ content: 'before-close', cursor: 'opaque:42', eof: false }),
        lastEventId: 'opaque:42',
      })
    );

    logs.handleLogDialogClose();
    expect(first.readyState).toBe(FakeEventSource.CLOSED);
    expect(logs.activeLog.value).toBe('before-close');
    vi.advanceTimersByTime(120_000);
    expect(FakeEventSource.instances).toHaveLength(1);

    logs.handleLogDialogOpen();
    expect(FakeEventSource.instances).toHaveLength(2);
    const resumedUrl = new URL(FakeEventSource.instances[1].url, 'http://ares.test');
    expect(resumedUrl.searchParams.get('cursor')).toBe('opaque:42');
    expect(logs.activeLog.value).toBe('before-close');
    logs.cleanupLogsAndConnections();
  });

  it('fully releases state on task switch and ignores late events from the old task', () => {
    const logs = useTestLog();
    logs.setCurrentLog(row(7));
    logs.openLogTarget(target('build', 7));
    const oldSource = FakeEventSource.instances[0];

    logs.setCurrentLog(row(8));
    expect(oldSource.readyState).toBe(FakeEventSource.CLOSED);
    oldSource.dispatchEvent(
      new MessageEvent('log', {
        data: JSON.stringify({ content: 'late-secret', cursor: 'late', eof: false }),
        lastEventId: 'late',
      })
    );
    logs.openLogTarget(target('build', 8));

    expect(logs.activeLog.value).toBe('');
    expect(FakeEventSource.instances[1].url).toBe('/api/v1/tasks/8/steps/build/logs/stream');
    logs.cleanupLogsAndConnections();
  });

  it('treats eof as terminal and never reopens the completed target', () => {
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());
    const source = FakeEventSource.instances[0];
    source.dispatchEvent(
      new MessageEvent('log', {
        data: JSON.stringify({ content: 'done\n', cursor: 'final', eof: true }),
        lastEventId: 'final',
      })
    );

    expect(source.readyState).toBe(FakeEventSource.CLOSED);
    expect(logs.activeLog.value).toBe('done\n');
    logs.handleLogDialogClose();
    logs.handleLogDialogOpen();
    expect(FakeEventSource.instances).toHaveLength(1);
    logs.cleanupLogsAndConnections();
  });

  it('closes on forbidden, refreshes permissions, and does not invalidate the identity', async () => {
    const refreshed = authenticatedSession();
    refreshed.result.user.permissions = [PERMISSIONS.TASKS_READ];
    mock.onGet('/api/v1/auth/session').reply(200, refreshed);
    const auth = useAuthStore();
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());
    const source = FakeEventSource.instances[0];

    source.dispatchEvent(new MessageEvent('stream-error', { data: '{"code":"forbidden"}' }));
    await vi.waitFor(() => expect(logs.canReadTaskLogs.value).toBe(false));

    expect(source.readyState).toBe(FakeEventSource.CLOSED);
    expect(logs.activeLogError.value).toBe('没有读取该步骤日志的权限');
    expect(auth.status).toBe('authenticated');
    expect(auth.isAuthenticated).toBe(true);
    logs.retryActiveLogStream();
    expect(FakeEventSource.instances).toHaveLength(1);
    logs.cleanupLogsAndConnections();
  });

  it('invalidates the identity only after auth-expired', () => {
    const auth = useAuthStore();
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());
    const source = FakeEventSource.instances[0];

    source.dispatchEvent(
      new MessageEvent('auth-expired', { data: '{"reason":"session_expired"}' })
    );

    expect(source.readyState).toBe(FakeEventSource.CLOSED);
    expect(auth.status).toBe('anonymous');
    expect(auth.isAuthenticated).toBe(false);
    logs.cleanupLogsAndConnections();
  });

  it('stops delivery without logging out when task/log permission is revoked in the session', () => {
    const auth = useAuthStore();
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());
    const source = FakeEventSource.instances[0];

    auth.$patch({
      user: {
        ...authenticatedSession().result.user,
        permissions: [PERMISSIONS.TASKS_READ],
      },
    });

    expect(source.readyState).toBe(FakeEventSource.CLOSED);
    expect(auth.status).toBe('authenticated');
    expect(auth.isAuthenticated).toBe(true);
    expect(logs.canReadTaskLogs.value).toBe(false);
    logs.cleanupLogsAndConnections();
  });

  it.each([
    [400, 'invalid_request', '日志请求参数无效'],
    [404, 'task_or_step_not_found', '未找到任务或步骤'],
    [409, 'log_source_mismatch', '日志来源与任务快照不匹配'],
    [409, 'legacy_task', '旧版任务请使用兼容日志入口'],
    [422, 'logs_unsupported', '该步骤不支持日志'],
    [502, 'invalid_log_chunk', '日志服务返回了无效数据'],
  ])('does not retry terminal pre-stream HTTP %i failures', async (status, code, message) => {
    vi.useFakeTimers();
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());

    FakeEventSource.instances[0].fail({ kind: 'http', status, code });
    await vi.advanceTimersByTimeAsync(120_000);

    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.instances[0].readyState).toBe(FakeEventSource.CLOSED);
    expect(logs.activeLogError.value).toBe(message);
    logs.cleanupLogsAndConnections();
  });

  it('invalidates the identity immediately on a pre-stream HTTP 401', () => {
    const auth = useAuthStore();
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());

    FakeEventSource.instances[0].fail({ kind: 'http', status: 401, code: 'unauthenticated' });

    expect(auth.status).toBe('anonymous');
    expect(logs.activeLogError.value).toBe('登录状态已失效');
    expect(FakeEventSource.instances[0].readyState).toBe(FakeEventSource.CLOSED);
    logs.cleanupLogsAndConnections();
  });

  it('refreshes permissions without logging out on a pre-stream HTTP 403', async () => {
    const refreshed = authenticatedSession();
    refreshed.result.user.permissions = [PERMISSIONS.TASKS_READ];
    mock.onGet('/api/v1/auth/session').reply(200, refreshed);
    const auth = useAuthStore();
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());

    FakeEventSource.instances[0].fail({ kind: 'http', status: 403, code: 'forbidden' });
    await vi.waitFor(() => expect(logs.canReadTaskLogs.value).toBe(false));

    expect(auth.status).toBe('authenticated');
    expect(auth.isAuthenticated).toBe(true);
    expect(logs.activeLogError.value).toBe('没有读取该步骤日志的权限');
    expect(FakeEventSource.instances).toHaveLength(1);
    logs.cleanupLogsAndConnections();
  });

  it('retries logs_not_ready and upstream HTTP failures within the shared bounded budget', async () => {
    vi.useFakeTimers();
    mock.onGet('/api/v1/auth/session').reply(200, authenticatedSession());
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());

    FakeEventSource.instances[0].fail({
      kind: 'http',
      status: 409,
      code: 'logs_not_ready',
    });
    await vi.advanceTimersByTimeAsync(3000);
    expect(FakeEventSource.instances).toHaveLength(2);

    FakeEventSource.instances[1].fail({
      kind: 'http',
      status: 503,
      code: 'executor_unavailable',
    });
    await vi.advanceTimersByTimeAsync(6000);
    expect(FakeEventSource.instances).toHaveLength(3);
    logs.cleanupLogsAndConnections();
  });

  it('waits for Retry-After before reconnecting after HTTP 429', async () => {
    vi.useFakeTimers();
    mock.onGet('/api/v1/auth/session').reply(200, authenticatedSession());
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());

    FakeEventSource.instances[0].fail({
      kind: 'http',
      status: 429,
      code: 'stream_capacity_exceeded',
      retryAfterMs: 7000,
    });
    await vi.advanceTimersByTimeAsync(6999);
    expect(FakeEventSource.instances).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeEventSource.instances).toHaveLength(2);
    logs.cleanupLogsAndConnections();
  });

  it('probes the session after a typed fetch network failure and stops when it returns 401', async () => {
    mock.onGet('/api/v1/auth/session').reply(401);
    const auth = useAuthStore();
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());
    const source = FakeEventSource.instances[0];

    source.fail({ kind: 'network', code: 'network_error' });
    await vi.waitFor(() => expect(auth.status).toBe('anonymous'));

    expect(source.readyState).toBe(FakeEventSource.CLOSED);
    expect(FakeEventSource.instances).toHaveLength(1);
    logs.cleanupLogsAndConnections();
  });

  it('reconnects retryable semantic failures from the opaque cursor within one bounded budget', async () => {
    vi.useFakeTimers();
    mock.onGet('/api/v1/auth/session').reply(200, authenticatedSession());
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());

    for (let attempt = 0; attempt < LOG_STREAM_MAX_AUTOMATIC_RECONNECTS; attempt += 1) {
      const source = FakeEventSource.instances[attempt];
      source.dispatchEvent(
        new MessageEvent('log', {
          data: JSON.stringify({ content: '', cursor: `opaque/${attempt}`, eof: false }),
          lastEventId: `opaque/${attempt}`,
        })
      );
      source.dispatchEvent(new MessageEvent('stream-error', { data: '{"code":"upstream_error"}' }));
      await vi.advanceTimersByTimeAsync(3000 * (attempt + 1));
      expect(FakeEventSource.instances).toHaveLength(attempt + 2);
      const resumedUrl = new URL(FakeEventSource.instances[attempt + 1].url, 'http://ares.test');
      expect(resumedUrl.searchParams.get('cursor')).toBe(`opaque/${attempt}`);
    }

    FakeEventSource.instances[LOG_STREAM_MAX_AUTOMATIC_RECONNECTS].dispatchEvent(
      new MessageEvent('stream-error', { data: '{"code":"upstream_error"}' })
    );
    await vi.advanceTimersByTimeAsync(120_000);
    expect(FakeEventSource.instances).toHaveLength(LOG_STREAM_MAX_AUTOMATIC_RECONNECTS + 1);
    logs.cleanupLogsAndConnections();
  });

  it('clears a retryable stream error after the reconnected source opens', async () => {
    vi.useFakeTimers();
    mock.onGet('/api/v1/auth/session').reply(200, authenticatedSession());
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget(target());

    FakeEventSource.instances[0].dispatchEvent(
      new MessageEvent('stream-error', { data: '{"code":"upstream_error"}' })
    );
    expect(logs.activeLogError.value).toBe('上游日志服务暂时不可用');

    await vi.advanceTimersByTimeAsync(3000);
    const reconnected = FakeEventSource.instances[1];
    reconnected.dispatchEvent(new Event('open'));

    expect(logs.activeLogError.value).toBe('');
    expect(logs.isStreaming.value).toBe(true);
    logs.cleanupLogsAndConnections();
  });

  it('keeps only bounded UTF-8 output and the newest lines', () => {
    const largeLog = Array.from(
      { length: 12_000 },
      (_, index) => `line-${index}-${'界'.repeat(180)}\n`
    ).join('');
    const bounded = boundLogText(largeLog);

    expect(bounded.startsWith(LOG_TRUNCATION_NOTICE)).toBe(true);
    expect(bounded).toContain('line-11999');
    expect(new TextEncoder().encode(bounded).byteLength).toBeLessThanOrEqual(LOG_BUFFER_MAX_BYTES);
    expect(bounded.split('\n').filter(Boolean).length).toBeLessThanOrEqual(LOG_BUFFER_MAX_LINES);
  });

  it('keeps the legacy adapter task-scoped and resumes only a numeric legacy cursor', () => {
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget({ kind: 'legacy', taskId: 7, legacyType: 'ci', label: 'CI 日志' });
    const source = FakeEventSource.instances[0];
    const url = new URL(source.url, 'http://ares.test');

    expect(url.pathname).toBe('/api/v1/job/stream/log');
    expect(Object.fromEntries(url.searchParams)).toEqual({ task_id: '7', log_type: 'ci' });
    expect(source.url).not.toContain('jobName');
    expect(source.url).not.toContain('buildId');
    source.dispatchEvent(
      new MessageEvent('message', {
        data: JSON.stringify({ code: 1, result: ['legacy'] }),
        lastEventId: '42',
      })
    );
    logs.handleLogDialogClose();
    logs.handleLogDialogOpen();
    const resumed = new URL(FakeEventSource.instances[1].url, 'http://ares.test');
    expect(Object.fromEntries(resumed.searchParams)).toEqual({
      task_id: '7',
      log_type: 'ci',
      start: '42',
    });
    logs.cleanupLogsAndConnections();
  });

  it('does not advance a legacy cursor when the frame payload is malformed', async () => {
    vi.useFakeTimers();
    mock.onGet('/api/v1/auth/session').reply(200, authenticatedSession());
    const logs = useTestLog();
    logs.setCurrentLog(row());
    logs.openLogTarget({ kind: 'legacy', taskId: 7, legacyType: 'ci', label: 'CI 日志' });

    FakeEventSource.instances[0].dispatchEvent(
      new MessageEvent('message', { data: '{malformed', lastEventId: '42' })
    );
    await vi.advanceTimersByTimeAsync(3000);

    const resumed = new URL(FakeEventSource.instances[1].url, 'http://ares.test');
    expect(resumed.searchParams.has('start')).toBe(false);
    logs.cleanupLogsAndConnections();
  });
});
