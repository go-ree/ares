import { computed, nextTick, reactive, ref, shallowRef, watch } from 'vue';
import { ElMessage } from 'element-plus';
import {
  legacyTaskLogStreamUrl,
  queryPublishLogs,
  taskStepLogStreamUrl,
  type LegacyTaskLogType,
} from '@/services/deploy';
import type { TaskRecord } from '@/models/deploy';
import type { DeployingService, LogFilter, LogItem } from '@/types/deploy';
import { normalizeLegacyNullableText } from '@/utils/legacy-nullable-text';
import { useEnvironments } from '@/composables/useEnvironments';
import { useAuthStore } from '@/stores/auth';
import { PERMISSIONS } from '@/types/auth';
import {
  createFetchLogStreamTransport,
  createLegacyLogStreamTransport,
  logStreamFailureFromEvent,
  type LogStreamFailure,
  type LogStreamTransport,
  type LogStreamTransportFactory,
} from '@/services/log-stream';

const STREAM_ERROR_MESSAGES: Record<string, string> = {
  '404': '未找到日志信息',
  not_found: '未找到日志信息',
  step_not_found: '未找到任务步骤',
  task_or_step_not_found: '未找到任务或步骤',
  logs_not_supported: '该步骤不支持日志',
  logs_unsupported: '该步骤不支持日志',
  log_unavailable: '该步骤的日志尚不可用',
  logs_not_ready: '该步骤的日志尚未就绪',
  log_source_mismatch: '日志来源与任务快照不匹配',
  legacy_task: '旧版任务请使用兼容日志入口',
  executor_unavailable: '日志执行器暂时不可用',
  invalid_cursor: '日志读取游标无效',
  cursor_regression: '日志读取游标异常',
  cursor_conflict: '日志读取游标冲突',
  invalid_log_chunk: '日志服务返回了无效数据',
  stream_capacity_exceeded: '日志流连接过多，请稍后重试',
  forbidden: '没有读取该步骤日志的权限',
  session_revalidation_failed: '会话状态校验暂时失败',
  upstream_error: '上游日志服务暂时不可用',
  upstream_unavailable: '上游日志服务暂时不可用',
  invalid_request: '日志请求参数无效',
  unauthorized: '登录状态已失效',
  unauthenticated: '登录状态已失效',
  internal_error: '日志服务暂时不可用',
  invalid_stream_response: '日志服务返回了无效响应',
  unexpected_stream_end: '日志连接意外中断',
  network_error: '日志连接失败',
};

const TERMINAL_STREAM_ERRORS = new Set([
  '404',
  'not_found',
  'step_not_found',
  'task_or_step_not_found',
  'logs_not_supported',
  'logs_unsupported',
  'log_source_mismatch',
  'legacy_task',
  'invalid_cursor',
  'cursor_regression',
  'cursor_conflict',
  'invalid_log_chunk',
  'forbidden',
]);

export const LOG_BUFFER_MAX_BYTES = 2 * 1024 * 1024;
export const LOG_BUFFER_MAX_LINES = 10_000;
export const LOG_CACHE_MAX_BYTES = 8 * 1024 * 1024;
export const LOG_QUEUE_MAX_BYTES = 512 * 1024;
export const LOG_QUEUE_MAX_LINES = 2_000;
export const LOG_TRUNCATION_NOTICE = '[较早的构建日志已在浏览器中截断]';
export const LOG_STREAM_MAX_AUTOMATIC_RECONNECTS = 5;
export const LOG_STREAM_SILENCE_MS = 60_000;

const LOG_BATCH_FLUSH_MS = 200;
const logTextEncoder = new TextEncoder();
const logTextDecoder = new TextDecoder();
const encodedLogBytes = (value: string) => logTextEncoder.encode(value).byteLength;

const utf8Suffix = (value: string, maximumBytes: number) => {
  const encoded = logTextEncoder.encode(value);
  if (encoded.byteLength <= maximumBytes) return value;
  let start = encoded.byteLength - Math.max(0, maximumBytes);
  while (start < encoded.byteLength && (encoded[start] & 0xc0) === 0x80) start += 1;
  return logTextDecoder.decode(encoded.subarray(start));
};

const lineCount = (value: string) => {
  if (!value) return 0;
  const newlines = value.split('\n').length - 1;
  return newlines + (value.endsWith('\n') ? 0 : 1);
};

const stripTruncationNotice = (value: string) => {
  if (value === LOG_TRUNCATION_NOTICE) return '';
  const prefix = `${LOG_TRUNCATION_NOTICE}\n`;
  return value.startsWith(prefix) ? value.slice(prefix.length) : value;
};

const keepLastLines = (value: string, maximumLines: number) => {
  const lines = lineCount(value);
  if (lines <= maximumLines) return value;
  let linesToDrop = lines - maximumLines;
  let start = 0;
  while (linesToDrop > 0) {
    const newline = value.indexOf('\n', start);
    if (newline < 0) return '';
    start = newline + 1;
    linesToDrop -= 1;
  }
  return value.slice(start);
};

export const boundLogText = (
  value: string,
  maximumBytes = LOG_BUFFER_MAX_BYTES,
  maximumLines = LOG_BUFFER_MAX_LINES
) => {
  const wasTruncated =
    value === LOG_TRUNCATION_NOTICE || value.startsWith(`${LOG_TRUNCATION_NOTICE}\n`);
  const content = stripTruncationNotice(value);
  if (
    !wasTruncated &&
    encodedLogBytes(content) <= maximumBytes &&
    lineCount(content) <= maximumLines
  ) {
    return content;
  }

  const marker = `${LOG_TRUNCATION_NOTICE}\n`;
  if (maximumBytes <= encodedLogBytes(marker) || maximumLines <= 1) {
    return utf8Suffix(LOG_TRUNCATION_NOTICE, maximumBytes);
  }

  let suffix = utf8Suffix(content, maximumBytes - encodedLogBytes(marker));
  suffix = keepLastLines(suffix, maximumLines - 1);
  return marker + suffix;
};

export const appendBoundedLog = (
  existing: string,
  incoming: string,
  maximumBytes = LOG_BUFFER_MAX_BYTES,
  maximumLines = LOG_BUFFER_MAX_LINES
) => boundLogText(existing + incoming, maximumBytes, maximumLines);

export interface StepTaskLogTarget {
  kind: 'step';
  taskId: number;
  stepKey: string;
  label: string;
}

export interface LegacyTaskLogTarget {
  kind: 'legacy';
  taskId: number;
  legacyType: LegacyTaskLogType;
  label: string;
}

export type TaskLogTarget = StepTaskLogTarget | LegacyTaskLogTarget;

export const taskLogTargetKey = (target: TaskLogTarget) =>
  target.kind === 'step'
    ? `task:${target.taskId}:step:${target.stepKey}`
    : `task:${target.taskId}:legacy:${target.legacyType}`;

const hasLegacyReference = (jobName: unknown, buildId: unknown) =>
  typeof jobName === 'string' && jobName.trim() !== '' && Number(buildId) > 0;

export const taskLogTargets = (task: TaskRecord): TaskLogTarget[] => {
  const engineVersion = typeof task.engine_version === 'number' ? task.engine_version : 1;
  if (engineVersion >= 2) {
    return [...(task.steps || [])]
      .sort((left, right) => left.position - right.position)
      .filter(step => step.capabilities?.logs === true)
      .map(step => ({
        kind: 'step' as const,
        taskId: task.task_id,
        stepKey: step.step_key,
        label: step.name || step.step_key,
      }));
  }

  const targets: LegacyTaskLogTarget[] = [];
  if (hasLegacyReference(task.ci_job_name, task.ci_build_id)) {
    targets.push({ kind: 'legacy', taskId: task.task_id, legacyType: 'ci', label: 'CI 日志' });
  }
  if (hasLegacyReference(task.cd_job_name, task.cd_build_id)) {
    targets.push({ kind: 'legacy', taskId: task.task_id, legacyType: 'cd', label: 'CD 日志' });
  }
  return targets;
};

interface StreamCache {
  content: string;
  contentBytes: number;
  cursor: string;
  completed: boolean;
  reconnects: number;
  error: string;
}

interface GenericLogPayload {
  content: string;
  cursor: string;
  eof: boolean;
}

export interface UseLogOptions {
  stepTransportFactory?: LogStreamTransportFactory;
  legacyTransportFactory?: LogStreamTransportFactory;
}

const genericLogPayload = (raw: string): GenericLogPayload => {
  const parsed: unknown = JSON.parse(raw);
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
    throw new Error('invalid log payload');
  }
  const payload = parsed as Record<string, unknown>;
  if (
    typeof payload.content !== 'string' ||
    typeof payload.cursor !== 'string' ||
    typeof payload.eof !== 'boolean'
  ) {
    throw new Error('invalid log payload');
  }
  return {
    content: payload.content,
    cursor: payload.cursor,
    eof: payload.eof,
  };
};

const streamErrorDetails = (raw: string): { code: string; message: string } => {
  const parsed: unknown = JSON.parse(raw);
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
    throw new Error('invalid stream error payload');
  }
  const payload = parsed as Record<string, unknown>;
  const legacyError = typeof payload.error === 'string' ? payload.error.trim() : '';
  const code = typeof payload.code === 'string' ? payload.code.trim() : legacyError;
  return {
    code,
    message: STREAM_ERROR_MESSAGES[code] || '日志服务暂时不可用',
  };
};

const streamEndReason = (raw: string): string => {
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') return '';
    const reason = (parsed as Record<string, unknown>).reason;
    return typeof reason === 'string' ? reason.trim() : '';
  } catch {
    return '';
  }
};

const eventCursor = (raw: string): string => {
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') return '';
    const cursor = (parsed as Record<string, unknown>).cursor;
    return typeof cursor === 'string' ? cursor : '';
  } catch {
    return '';
  }
};

const legacyLogLines = (raw: string): { lines: string[]; errorCode: string } => {
  const parsed: unknown = JSON.parse(raw);
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
    throw new Error('invalid legacy log payload');
  }
  const payload = parsed as Record<string, unknown>;
  if (payload.code === 1 && Array.isArray(payload.result)) {
    return {
      lines: payload.result.filter((line): line is string => typeof line === 'string'),
      errorCode: '',
    };
  }
  return {
    lines: [],
    errorCode: typeof payload.error === 'string' ? payload.error.trim() : 'upstream_error',
  };
};

export function useLog(options: UseLogOptions = {}) {
  const authStore = useAuthStore();
  const { labelForEnvironment } = useEnvironments();
  const stepTransportFactory =
    options.stepTransportFactory || ((url: string) => createFetchLogStreamTransport(url));
  const legacyTransportFactory = options.legacyTransportFactory || createLegacyLogStreamTransport;

  const logFilter = reactive<LogFilter>({
    serviceName: '',
    environment: '',
    dateRange: [],
  });
  const logList = ref<LogItem[]>([]);
  const currentPage = ref(1);
  const pageSize = ref(10);
  const total = ref(0);
  const logLoading = ref(false);
  const isFirstLoad = ref(true);

  const logDialogVisible = ref(false);
  const currentLog = ref<DeployingService>({} as DeployingService);
  const activeLogTarget = ref<TaskLogTarget | null>(null);
  const activeLog = ref('');
  const activeLogError = ref('');
  const activeLogLoading = ref(false);
  const isStreaming = ref(false);
  const logContainer = ref<HTMLElement>();
  const activeEventSource = shallowRef<LogStreamTransport | null>(null);
  const streamCaches = new Map<string, StreamCache>();
  let totalCachedContentBytes = 0;

  let streamGeneration = 0;
  let streamPaused = true;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let silenceTimer: ReturnType<typeof setTimeout> | null = null;
  let updateTimer: ReturnType<typeof setTimeout> | null = null;
  let queuedContent = '';
  let queuedTargetKey = '';
  let sessionProbe: Promise<boolean> | null = null;
  let sseAuthBlocked =
    !authStore.isAuthenticated ||
    !authStore.can(PERMISSIONS.TASKS_READ) ||
    !authStore.can(PERMISSIONS.LOGS_READ);

  const canReadTaskLogs = computed(
    () =>
      authStore.isAuthenticated &&
      authStore.can(PERMISSIONS.TASKS_READ) &&
      authStore.can(PERMISSIONS.LOGS_READ)
  );

  const getStatusType = (status: string) => {
    const statusMap: Record<string, string> = {
      初始化: 'info',
      打包中: 'primary',
      打包成功: 'success',
      打包失败: 'danger',
      部署中: 'primary',
      部署成功: 'success',
      部署失败: 'danger',
      已取消: 'warning',
      超时: 'warning',
      执行超时: 'warning',
      执行结果待核查: 'warning',
      未知状态: 'info',
      排队中: 'info',
      执行中: 'primary',
      执行成功: 'success',
      执行失败: 'danger',
      成功但有警告: 'warning',
    };
    return statusMap[status] || 'info';
  };

  const getDeployStatus = (status: string): string => {
    const statusMap: Record<string, string> = {
      init: '初始化',
      packaging: '打包中',
      packaged: '打包成功',
      package_failed: '打包失败',
      deploying: '部署中',
      deployed: '部署成功',
      deploy_failed: '部署失败',
      cancelled: '已取消',
      timeout: '超时',
      timed_out: '执行超时',
      outcome_unknown: '执行结果待核查',
      unknown: '未知状态',
      queued: '排队中',
      running: '执行中',
      succeeded: '执行成功',
      failed: '执行失败',
      succeeded_with_warnings: '成功但有警告',
    };
    return statusMap[status] || status;
  };

  const getEnvLabel = (env: string): string => labelForEnvironment(env);

  const getEnvType = (env: string): string => {
    const tagTypes = ['info', 'warning', 'success', 'primary'] as const;
    const hash = Array.from(env).reduce((sum, character) => sum + character.charCodeAt(0), 0);
    return tagTypes[hash % tagTypes.length];
  };

  const formatDateTime = (dateTime: string): string => {
    if (!dateTime) return '';
    return new Date(dateTime).toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
  };

  const scrollToBottom = () => {
    nextTick(() => {
      if (logContainer.value) logContainer.value.scrollTop = logContainer.value.scrollHeight;
    });
  };

  const getDisplayLog = (logContent: string, maximumLines = 1000) => {
    if (!logContent) return '';
    const lines = logContent.split('\n');
    return lines.length <= maximumLines ? logContent : lines.slice(-maximumLines).join('\n');
  };

  const handleSearch = async () => {
    if (!canReadTaskLogs.value) return;
    logLoading.value = true;
    try {
      const response = await queryPublishLogs({
        page_num: currentPage.value,
        page_size: pageSize.value,
        app_name: logFilter.serviceName || undefined,
        env: logFilter.environment || undefined,
        start_time: logFilter.dateRange?.[0]?.toISOString(),
        end_time: logFilter.dateRange?.[1]?.toISOString(),
      });
      if (response.data.code !== 1) throw new Error(response.data.message || '查询失败');
      const result = response.data.result;
      if (result && Array.isArray(result.task_record)) {
        logList.value = result.task_record.map(item => ({
          task_id: item.task_id,
          serviceName: item.app_name,
          branch: item.branch,
          environment: item.env,
          status: getDeployStatus(item.status),
          deployTime: formatDateTime(item.created_at),
          operator: item.publisher,
          message: normalizeLegacyNullableText(item.message),
          auto_deploy: item.auto_deploy,
          products: normalizeLegacyNullableText(item.products),
        }));
        total.value = result.total || 0;
      } else {
        logList.value = [];
        total.value = 0;
      }

      if (
        isFirstLoad.value ||
        logFilter.serviceName ||
        logFilter.environment ||
        logFilter.dateRange.length > 0
      ) {
        if (logList.value.length > 0) ElMessage.success(`查询成功，共找到 ${total.value} 条记录`);
        else ElMessage.info('查询完成，未找到相关记录');
      }
      isFirstLoad.value = false;
    } catch (error) {
      console.error('查询日志失败:', error);
      ElMessage.error(error instanceof Error ? error.message : '查询失败');
      logList.value = [];
      total.value = 0;
    } finally {
      logLoading.value = false;
    }
  };

  const handleResetLogFilter = () => {
    logFilter.serviceName = '';
    logFilter.environment = '';
    logFilter.dateRange = [];
    currentPage.value = 1;
    pageSize.value = 10;
    isFirstLoad.value = true;
    void handleSearch();
  };

  const handleSizeChange = (value: number) => {
    pageSize.value = value;
    void handleSearch();
  };

  const handleCurrentChange = (value: number) => {
    currentPage.value = value;
    void handleSearch();
  };

  const ensureCache = (target: TaskLogTarget) => {
    const key = taskLogTargetKey(target);
    let cache = streamCaches.get(key);
    if (!cache) {
      cache = {
        content: '',
        contentBytes: 0,
        cursor: '',
        completed: false,
        reconnects: 0,
        error: '',
      };
    } else {
      // Map insertion order is the LRU order. Touching a cache keeps recently
      // viewed steps while allowing old multi-megabyte buffers to be evicted.
      streamCaches.delete(key);
    }
    streamCaches.set(key, cache);
    return cache;
  };

  const enforceAggregateCacheBound = (protectedKey: string) => {
    if (totalCachedContentBytes <= LOG_CACHE_MAX_BYTES) return;
    for (const [key, cache] of streamCaches) {
      if (key === protectedKey) continue;
      streamCaches.delete(key);
      totalCachedContentBytes -= cache.contentBytes;
      if (totalCachedContentBytes <= LOG_CACHE_MAX_BYTES) return;
    }
  };

  const activeTargetKey = () =>
    activeLogTarget.value ? taskLogTargetKey(activeLogTarget.value) : '';

  const syncActiveView = () => {
    if (!activeLogTarget.value) {
      activeLog.value = '';
      activeLogError.value = '';
      return;
    }
    const cache = ensureCache(activeLogTarget.value);
    activeLog.value = cache.content;
    activeLogError.value = cache.error;
  };

  const clearReconnectTimer = () => {
    if (reconnectTimer) clearTimeout(reconnectTimer);
    reconnectTimer = null;
  };

  const clearSilenceTimer = () => {
    if (silenceTimer) clearTimeout(silenceTimer);
    silenceTimer = null;
  };

  const clearUpdateTimer = () => {
    if (updateTimer) clearTimeout(updateTimer);
    updateTimer = null;
  };

  const flushQueuedContent = () => {
    clearUpdateTimer();
    if (!queuedContent || !queuedTargetKey) {
      queuedContent = '';
      queuedTargetKey = '';
      return;
    }
    const cache = streamCaches.get(queuedTargetKey);
    if (cache) {
      totalCachedContentBytes -= cache.contentBytes;
      cache.content = appendBoundedLog(cache.content, queuedContent);
      cache.contentBytes = encodedLogBytes(cache.content);
      totalCachedContentBytes += cache.contentBytes;
      streamCaches.delete(queuedTargetKey);
      streamCaches.set(queuedTargetKey, cache);
      enforceAggregateCacheBound(queuedTargetKey);
      if (activeTargetKey() === queuedTargetKey) {
        activeLog.value = cache.content;
        scrollToBottom();
      }
    }
    queuedContent = '';
    queuedTargetKey = '';
  };

  const queueLogContent = (target: TaskLogTarget, content: string) => {
    if (!content) return;
    const key = taskLogTargetKey(target);
    if (queuedTargetKey && queuedTargetKey !== key) flushQueuedContent();
    queuedTargetKey = key;
    queuedContent = appendBoundedLog(
      queuedContent,
      content,
      LOG_QUEUE_MAX_BYTES,
      LOG_QUEUE_MAX_LINES
    );
    activeLogLoading.value = false;
    clearUpdateTimer();
    updateTimer = setTimeout(flushQueuedContent, LOG_BATCH_FLUSH_MS);
  };

  const detachTransport = () => {
    streamGeneration += 1;
    clearSilenceTimer();
    activeEventSource.value?.close();
    activeEventSource.value = null;
    isStreaming.value = false;
    activeLogLoading.value = false;
  };

  const stopActiveLogStream = (flush = true) => {
    clearReconnectTimer();
    detachTransport();
    if (flush) flushQueuedContent();
    else {
      clearUpdateTimer();
      queuedContent = '';
      queuedTargetKey = '';
    }
  };

  const currentStreamMatches = (
    target: TaskLogTarget,
    source: LogStreamTransport,
    generation: number
  ) =>
    !sseAuthBlocked &&
    !streamPaused &&
    canReadTaskLogs.value &&
    generation === streamGeneration &&
    activeEventSource.value === source &&
    activeTargetKey() === taskLogTargetKey(target) &&
    currentLog.value.taskId === target.taskId;

  const rememberCursor = (target: TaskLogTarget, event: MessageEvent, payloadCursor = '') => {
    const cursor = payloadCursor || event.lastEventId;
    if (cursor !== '') ensureCache(target).cursor = cursor;
  };

  const blockSseForInvalidSession = (reason = 'session_expired', invalidateIdentity = true) => {
    if (!sseAuthBlocked) {
      sseAuthBlocked = true;
      stopActiveLogStream();
    }
    if (invalidateIdentity && authStore.status !== 'anonymous') authStore.invalidate(reason);
  };

  const sessionAllowsSseRetry = async () => {
    if (sseAuthBlocked) return false;
    if (sessionProbe) return sessionProbe;
    sessionProbe = (async () => {
      const authenticated = await authStore.refreshSession();
      const allowed =
        authenticated &&
        authStore.can(PERMISSIONS.TASKS_READ) &&
        authStore.can(PERMISSIONS.LOGS_READ);
      if (!allowed) blockSseForInvalidSession('logs_forbidden', false);
      return allowed;
    })().finally(() => {
      sessionProbe = null;
    });
    return sessionProbe;
  };

  const scheduleReconnect = (
    target: TaskLogTarget,
    message: string,
    transportDetached = false,
    minimumDelayMs = 0
  ) => {
    if (!transportDetached) detachTransport();
    flushQueuedContent();
    clearReconnectTimer();
    if (
      streamPaused ||
      sseAuthBlocked ||
      !canReadTaskLogs.value ||
      activeTargetKey() !== taskLogTargetKey(target)
    ) {
      return;
    }

    const cache = ensureCache(target);
    if (cache.reconnects >= LOG_STREAM_MAX_AUTOMATIC_RECONNECTS) {
      cache.error = message;
      activeLogError.value = message;
      return;
    }
    cache.reconnects += 1;
    const attempt = cache.reconnects;
    const expectedGeneration = streamGeneration;
    const expectedTargetKey = taskLogTargetKey(target);
    reconnectTimer = setTimeout(
      () => {
        reconnectTimer = null;
        void sessionAllowsSseRetry().then(allowed => {
          if (
            !allowed ||
            streamPaused ||
            expectedGeneration !== streamGeneration ||
            expectedTargetKey !== activeTargetKey()
          ) {
            return;
          }
          startStream(target);
        });
      },
      Math.max(3000 * attempt, minimumDelayMs)
    );
  };

  const completeStream = (target: TaskLogTarget) => {
    const cache = ensureCache(target);
    cache.completed = true;
    cache.error = '';
    activeLogError.value = '';
    detachTransport();
    flushQueuedContent();
  };

  const resetSilenceTimer = (target: TaskLogTarget) => {
    clearSilenceTimer();
    const expectedGeneration = streamGeneration;
    silenceTimer = setTimeout(() => {
      silenceTimer = null;
      if (expectedGeneration !== streamGeneration) return;
      scheduleReconnect(target, '日志流静默超时，已达到最大重试次数');
    }, LOG_STREAM_SILENCE_MS);
  };

  function startStream(target: TaskLogTarget) {
    if (
      streamPaused ||
      sseAuthBlocked ||
      !canReadTaskLogs.value ||
      currentLog.value.taskId !== target.taskId ||
      activeTargetKey() !== taskLogTargetKey(target)
    ) {
      return;
    }

    const cache = ensureCache(target);
    if (cache.completed) {
      syncActiveView();
      return;
    }

    detachTransport();
    const generation = streamGeneration;
    const url =
      target.kind === 'step'
        ? taskStepLogStreamUrl(target.taskId, target.stepKey, cache.cursor || undefined)
        : legacyTaskLogStreamUrl(target.taskId, target.legacyType, cache.cursor || undefined);
    const source = target.kind === 'step' ? stepTransportFactory(url) : legacyTransportFactory(url);
    activeEventSource.value = source;
    isStreaming.value = true;
    activeLogLoading.value = !cache.content;

    const isCurrent = () => currentStreamMatches(target, source, generation);

    const handlePayloadError = () => {
      if (!isCurrent()) return;
      const message = '日志数据格式异常，已达到最大重试次数';
      cache.error = '日志数据格式异常';
      activeLogError.value = cache.error;
      scheduleReconnect(target, message);
    };

    if (target.kind === 'step') {
      source.addEventListener('log', event => {
        if (!isCurrent()) return;
        resetSilenceTimer(target);
        try {
          const message = event as MessageEvent;
          const payload = genericLogPayload(message.data);
          if (!message.lastEventId || message.lastEventId !== payload.cursor) {
            throw new Error('log cursor mismatch');
          }
          cache.error = '';
          activeLogError.value = '';
          rememberCursor(target, message, payload.cursor);
          queueLogContent(target, payload.content);
          if (payload.eof) completeStream(target);
        } catch {
          handlePayloadError();
        }
      });
    } else {
      source.onmessage = event => {
        if (!isCurrent()) return;
        resetSilenceTimer(target);
        try {
          const payload = legacyLogLines(event.data);
          if (payload.errorCode) {
            const details = {
              code: payload.errorCode,
              message: STREAM_ERROR_MESSAGES[payload.errorCode] || '日志服务暂时不可用',
            };
            cache.error = details.message;
            activeLogError.value = details.message;
            if (TERMINAL_STREAM_ERRORS.has(details.code)) detachTransport();
            else scheduleReconnect(target, `${details.message}，已达到最大重试次数`);
            return;
          }
          cache.error = '';
          activeLogError.value = '';
          rememberCursor(target, event);
          if (payload.lines.length > 0) queueLogContent(target, `${payload.lines.join('\n')}\n`);
        } catch {
          handlePayloadError();
        }
      };
    }

    source.addEventListener('ping', event => {
      if (!isCurrent()) return;
      const message = event as MessageEvent;
      resetSilenceTimer(target);
      rememberCursor(target, message, eventCursor(message.data));
    });

    source.addEventListener('auth-expired', () => {
      if (!isCurrent()) return;
      blockSseForInvalidSession();
    });

    source.addEventListener('end', event => {
      if (!isCurrent()) return;
      const message = event as MessageEvent;
      rememberCursor(target, message, eventCursor(message.data));
      const reason = streamEndReason(message.data);
      if (reason === 'completed') {
        completeStream(target);
        return;
      }
      scheduleReconnect(target, `日志流结束（${reason || 'unknown'}），已达到最大重试次数`);
    });

    source.addEventListener('stream-error', event => {
      if (!isCurrent()) return;
      try {
        const details = streamErrorDetails((event as MessageEvent).data);
        cache.error = details.message;
        activeLogError.value = details.message;
        if (details.code === 'forbidden') {
          blockSseForInvalidSession('logs_forbidden', false);
          void authStore.refreshSession();
          return;
        }
        if (TERMINAL_STREAM_ERRORS.has(details.code)) {
          detachTransport();
          flushQueuedContent();
          return;
        }
        scheduleReconnect(target, `${details.message}，已达到最大重试次数`);
      } catch {
        handlePayloadError();
      }
    });

    source.onopen = () => {
      if (!isCurrent()) return;
      cache.error = '';
      activeLogError.value = '';
      isStreaming.value = true;
      activeLogLoading.value = false;
      resetSilenceTimer(target);
    };

    const handleTransportFailure = (failure: LogStreamFailure) => {
      const message = STREAM_ERROR_MESSAGES[failure.code] || '日志服务暂时不可用';
      cache.error = message;
      activeLogError.value = message;
      if (failure.status === 401) {
        blockSseForInvalidSession();
        return;
      }
      if (failure.status === 403) {
        blockSseForInvalidSession('logs_forbidden', false);
        void authStore.refreshSession();
        return;
      }
      if (TERMINAL_STREAM_ERRORS.has(failure.code)) {
        detachTransport();
        flushQueuedContent();
        return;
      }
      if (
        failure.status !== undefined &&
        failure.status >= 400 &&
        failure.status < 500 &&
        failure.status !== 429 &&
        !(failure.status === 409 && failure.code === 'logs_not_ready')
      ) {
        detachTransport();
        flushQueuedContent();
        return;
      }
      scheduleReconnect(
        target,
        `${message}，已达到最大重试次数`,
        false,
        failure.status === 429 ? failure.retryAfterMs || 0 : 0
      );
    };

    source.onerror = event => {
      if (!isCurrent()) return;
      const failure = logStreamFailureFromEvent(event);
      if (target.kind === 'step' && failure) {
        if (failure.kind === 'network') {
          const message = STREAM_ERROR_MESSAGES[failure.code] || '日志服务暂时不可用';
          cache.error = message;
          activeLogError.value = message;
          detachTransport();
          flushQueuedContent();
          const expectedGeneration = streamGeneration;
          const expectedTargetKey = taskLogTargetKey(target);
          void sessionAllowsSseRetry().then(allowed => {
            if (
              !allowed ||
              streamPaused ||
              expectedGeneration !== streamGeneration ||
              expectedTargetKey !== activeTargetKey()
            ) {
              return;
            }
            scheduleReconnect(target, `${message}，已达到最大重试次数`, true);
          });
          return;
        }
        handleTransportFailure(failure);
        return;
      }
      detachTransport();
      flushQueuedContent();
      const expectedGeneration = streamGeneration;
      const expectedTargetKey = taskLogTargetKey(target);
      void sessionAllowsSseRetry().then(allowed => {
        if (
          !allowed ||
          streamPaused ||
          expectedGeneration !== streamGeneration ||
          expectedTargetKey !== activeTargetKey()
        ) {
          return;
        }
        scheduleReconnect(
          target,
          `日志连接失败，已重试 ${LOG_STREAM_MAX_AUTOMATIC_RECONNECTS} 次`,
          true
        );
      });
    };

    resetSilenceTimer(target);
  }

  const openLogTarget = (target: TaskLogTarget) => {
    if (!canReadTaskLogs.value || sseAuthBlocked || currentLog.value.taskId !== target.taskId)
      return;
    const previousKey = activeTargetKey();
    const nextKey = taskLogTargetKey(target);
    if (previousKey !== nextKey) {
      stopActiveLogStream();
      activeLogTarget.value = target;
      syncActiveView();
    } else {
      activeLogTarget.value = target;
    }
    streamPaused = false;
    const cache = ensureCache(target);
    cache.reconnects = 0;
    cache.error = '';
    activeLogError.value = '';
    if (!cache.completed && !activeEventSource.value) startStream(target);
  };

  const pauseLogStream = () => {
    if (streamPaused) return;
    streamPaused = true;
    stopActiveLogStream();
  };

  const resumeLogStream = () => {
    if (!canReadTaskLogs.value || sseAuthBlocked) return;
    streamPaused = false;
    if (!activeLogTarget.value) return;
    const cache = ensureCache(activeLogTarget.value);
    cache.reconnects = 0;
    if (!cache.completed && !activeEventSource.value) startStream(activeLogTarget.value);
  };

  const retryActiveLogStream = () => {
    if (!activeLogTarget.value || !canReadTaskLogs.value || sseAuthBlocked) return;
    stopActiveLogStream();
    const cache = ensureCache(activeLogTarget.value);
    cache.completed = false;
    cache.reconnects = 0;
    cache.error = '';
    activeLogError.value = '';
    streamPaused = false;
    startStream(activeLogTarget.value);
  };

  const cleanupLogsAndConnections = () => {
    streamPaused = true;
    stopActiveLogStream(false);
    streamCaches.clear();
    totalCachedContentBytes = 0;
    activeLogTarget.value = null;
    activeLog.value = '';
    activeLogError.value = '';
  };

  const setCurrentLog = (value: DeployingService) => {
    if (currentLog.value.taskId && currentLog.value.taskId !== value.taskId) {
      cleanupLogsAndConnections();
    }
    currentLog.value = value;
  };

  const handleLogDialogClose = () => pauseLogStream();
  const handleLogDialogOpen = () => resumeLogStream();

  watch(
    [() => authStore.status, () => authStore.permissions],
    ([status]) => {
      const allowed =
        status === 'authenticated' &&
        authStore.can(PERMISSIONS.TASKS_READ) &&
        authStore.can(PERMISSIONS.LOGS_READ);
      if (allowed) {
        sseAuthBlocked = false;
        return;
      }
      if (status === 'anonymous' || status === 'authenticated') {
        blockSseForInvalidSession('logs_forbidden', false);
      }
    },
    { flush: 'sync' }
  );

  return {
    logFilter,
    logList,
    currentPage,
    pageSize,
    total,
    logLoading,
    logDialogVisible,
    currentLog,
    activeLogTarget,
    activeLog,
    activeLogError,
    activeLogLoading,
    isStreaming,
    logContainer,
    activeEventSource,
    canReadTaskLogs,
    getStatusType,
    getDeployStatus,
    getEnvLabel,
    getEnvType,
    formatDateTime,
    scrollToBottom,
    getDisplayLog,
    handleSearch,
    handleResetLogFilter,
    handleSizeChange,
    handleCurrentChange,
    setCurrentLog,
    openLogTarget,
    pauseLogStream,
    resumeLogStream,
    retryActiveLogStream,
    handleLogDialogClose,
    handleLogDialogOpen,
    cleanupLogsAndConnections,
    flushQueuedContent,
    queueLogContent,
  };
}
