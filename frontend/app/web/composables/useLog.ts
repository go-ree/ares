import { ref, reactive, watch, nextTick } from 'vue';
import { ElMessage } from 'element-plus';
import { queryPublishLogs, queryTaskLogs } from '@/services/deploy';
import api from '@/config/api';
import type { LogItem, DeployingService, LogFilter } from '@/types/deploy';
import { normalizeLegacyNullableText } from '@/utils/legacy-nullable-text';
import { useEnvironments } from '@/composables/useEnvironments';
import { useAuthStore } from '@/stores/auth';
import { PERMISSIONS } from '@/types/auth';

const STREAM_ERROR_MESSAGES: Record<string, string> = {
  '404': '未找到日志信息',
  cursor_regression: '日志读取游标异常',
  session_revalidation_failed: '会话状态校验暂时失败',
  upstream_error: '上游日志服务暂时不可用',
};

export const LOG_BUFFER_MAX_BYTES = 2 * 1024 * 1024;
export const LOG_BUFFER_MAX_LINES = 10_000;
export const LOG_QUEUE_MAX_BYTES = 512 * 1024;
export const LOG_QUEUE_MAX_LINES = 2_000;
export const LOG_TRUNCATION_NOTICE = '[较早的构建日志已在浏览器中截断]';
export const LOG_STREAM_MAX_AUTOMATIC_RECONNECTS = 5;

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

const boundedLogLines = (
  values: readonly unknown[],
  maximumBytes: number,
  maximumLines: number
): string[] => {
  let wasTruncated = false;
  const lines: string[] = [];
  let totalBytes = 0;
  values.forEach(value => {
    if (value === LOG_TRUNCATION_NOTICE) {
      wasTruncated = true;
      return;
    }
    if (typeof value !== 'string') return;
    lines.push(value);
    totalBytes += encodedLogBytes(value + '\n');
  });
  if (!wasTruncated && lines.length <= maximumLines && totalBytes <= maximumBytes) {
    return lines;
  }

  const markerBytes = encodedLogBytes(LOG_TRUNCATION_NOTICE + '\n');
  let remainingBytes = Math.max(0, maximumBytes - markerBytes);
  const maximumDataLines = Math.max(0, maximumLines - 1);
  const reversed: string[] = [];
  for (let index = lines.length - 1; index >= 0 && reversed.length < maximumDataLines; index--) {
    const line = lines[index];
    const lineBytes = encodedLogBytes(line + '\n');
    if (lineBytes <= remainingBytes) {
      reversed.push(line);
      remainingBytes -= lineBytes;
      continue;
    }
    if (reversed.length === 0 && remainingBytes > 1) {
      reversed.push(utf8Suffix(line, remainingBytes - 1));
    }
    break;
  }
  return [LOG_TRUNCATION_NOTICE, ...reversed.reverse()];
};

const appendBoundedLog = (
  existing: string,
  incoming: readonly unknown[],
  maximumBytes = LOG_BUFFER_MAX_BYTES,
  maximumLines = LOG_BUFFER_MAX_LINES
) => {
  const existingLines = existing ? existing.split('\n') : [];
  if (existingLines[existingLines.length - 1] === '') existingLines.pop();
  const bounded = boundedLogLines([...existingLines, ...incoming], maximumBytes, maximumLines);
  return bounded.length > 0 ? bounded.join('\n') + '\n' : '';
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
    // Unknown upstream text may contain credentials, URLs, or implementation
    // details. Only display the stable allow-listed messages in the browser.
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

export function useLog() {
  const authStore = useAuthStore();
  const { labelForEnvironment } = useEnvironments();
  const LOG_BATCH_FLUSH_MS = 200; // 100~300ms 批量追加，避免频繁重排

  // 日志筛选条件
  const logFilter = reactive<LogFilter>({
    serviceName: '',
    environment: '',
    dateRange: [],
  });

  // 日志列表数据
  const logList = ref<LogItem[]>([]);
  const currentPage = ref(1);
  const pageSize = ref(10);
  const total = ref(0);
  const logLoading = ref(false);
  const isFirstLoad = ref(true); // 添加首次加载标志

  // 日志对话框相关
  const logDialogVisible = ref(false);
  const currentLog = ref<DeployingService>({} as DeployingService);
  const activeLogTab = ref('ci');
  const ciLog = ref('');
  const cdLog = ref('');
  const ciLogLoading = ref(false);
  const cdLogLoading = ref(false);

  // 日志容器引用
  const ciLogContainer = ref<HTMLElement>();
  const cdLogContainer = ref<HTMLElement>();

  // SSE连接状态 - 改为Map管理多个连接
  const eventSourceMap = ref<Map<string, EventSource>>(new Map());
  const streamingStatus = ref<Map<string, boolean>>(new Map());
  // A completed event is terminal for this log lifecycle. Keep this outside
  // the transient connection map so periodic health checks cannot reopen it.
  const completedConnections = new Set<string>();
  // One budget covers every automatic reconnect in an open-dialog lifecycle,
  // including planned max-duration rotations and health-check recovery.
  const automaticReconnects = new Map<string, number>();
  const scheduledReconnects = new Set<string>();
  const connectionSettlers = new Map<string, () => void>();
  // SSE断线续传 offset（服务端会在每条消息带 id，浏览器会自动用 Last-Event-ID 重连；
  // 这里额外缓存 last offset，用于“手动重建连接/重试”时可带 start 提升体验）
  const lastOffsetMap = ref<Map<string, number>>(new Map());
  const connectionTimeouts = new Set<ReturnType<typeof setTimeout>>();
  const connectionIntervals = new Set<ReturnType<typeof setInterval>>();
  let logSessionGeneration = 0;
  let logDialogPaused = false;
  let sseAuthBlocked = !authStore.isAuthenticated || !authStore.can(PERMISSIONS.LOGS_READ);
  let sessionProbe: Promise<boolean> | null = null;

  const isLogSessionCurrent = (generation: number, taskId: number) =>
    !sseAuthBlocked &&
    authStore.isAuthenticated &&
    authStore.can(PERMISSIONS.LOGS_READ) &&
    generation === logSessionGeneration &&
    currentLog.value.taskId === taskId;

  const setConnectionTimeout = (
    callback: () => void,
    delay: number,
    generation: number,
    taskId: number
  ) => {
    const timer = setTimeout(() => {
      connectionTimeouts.delete(timer);
      if (isLogSessionCurrent(generation, taskId)) callback();
    }, delay);
    connectionTimeouts.add(timer);
    return timer;
  };

  const clearConnectionTimeout = (timer: ReturnType<typeof setTimeout>) => {
    clearTimeout(timer);
    connectionTimeouts.delete(timer);
  };

  const setConnectionInterval = (
    callback: () => void,
    delay: number,
    generation: number,
    taskId: number
  ) => {
    const timer = setInterval(() => {
      if (!isLogSessionCurrent(generation, taskId)) {
        clearInterval(timer);
        connectionIntervals.delete(timer);
        return;
      }
      callback();
    }, delay);
    connectionIntervals.add(timer);
    return timer;
  };

  const clearConnectionInterval = (timer: ReturnType<typeof setInterval>) => {
    clearInterval(timer);
    connectionIntervals.delete(timer);
  };

  const clearConnectionTimers = () => {
    connectionTimeouts.forEach(timer => clearTimeout(timer));
    connectionIntervals.forEach(timer => clearInterval(timer));
    connectionTimeouts.clear();
    connectionIntervals.clear();
  };

  // 生成连接ID
  const generateConnectionId = (type: 'ci' | 'cd', jobName: string, buildId: number) => {
    return `${type}_${jobName}_${buildId}`;
  };

  const rememberStreamOffset = (connectionId: string, event: MessageEvent) => {
    if (!event.lastEventId || !/^\d+$/.test(event.lastEventId)) return;
    const offset = Number(event.lastEventId);
    if (Number.isSafeInteger(offset) && offset >= 0) {
      lastOffsetMap.value.set(connectionId, offset);
    }
  };

  const closeConnectionPreservingCursor = (connectionId: string, expectedSource?: EventSource) => {
    const eventSource = eventSourceMap.value.get(connectionId);
    if (expectedSource && eventSource !== expectedSource) {
      expectedSource.close();
      return;
    }
    eventSource?.close();
    eventSourceMap.value.delete(connectionId);
    streamingStatus.value.delete(connectionId);
  };

  const claimAutomaticReconnect = (connectionId: string) => {
    const used = automaticReconnects.get(connectionId) || 0;
    if (used >= LOG_STREAM_MAX_AUTOMATIC_RECONNECTS) return null;
    const attempt = used + 1;
    automaticReconnects.set(connectionId, attempt);
    return attempt;
  };

  // 清理特定连接及其完整生命周期状态（切换任务时使用）。
  const cleanupConnection = (connectionId: string) => {
    connectionSettlers.get(connectionId)?.();
    connectionSettlers.delete(connectionId);
    closeConnectionPreservingCursor(connectionId);
    lastOffsetMap.value.delete(connectionId);
    completedConnections.delete(connectionId);
    automaticReconnects.delete(connectionId);
    scheduledReconnects.delete(connectionId);
    console.log(`清理SSE连接: ${connectionId}`);
  };

  // 清理所有连接
  const cleanupAllConnections = () => {
    [...connectionSettlers.values()].forEach(settle => settle());
    connectionSettlers.clear();
    eventSourceMap.value.forEach((eventSource, connectionId) => {
      eventSource.close();
      console.log(`清理SSE连接: ${connectionId}`);
    });
    eventSourceMap.value.clear();
    streamingStatus.value.clear();
    lastOffsetMap.value.clear();
    completedConnections.clear();
    automaticReconnects.clear();
    scheduledReconnects.clear();
    clearConnectionTimers();
  };

  // 对话框关闭时真正终止网络与定时器，但保留日志、游标及完成状态，
  // 以便再次打开后从已有 buffer/cursor 继续。
  const pauseAllConnections = () => {
    [...connectionSettlers.values()].forEach(settle => settle());
    connectionSettlers.clear();
    eventSourceMap.value.forEach(eventSource => eventSource.close());
    eventSourceMap.value.clear();
    streamingStatus.value.clear();
    scheduledReconnects.clear();
    clearConnectionTimers();
  };

  const blockSseForInvalidSession = (reason = 'session_expired', invalidateIdentity = true) => {
    if (!sseAuthBlocked) {
      sseAuthBlocked = true;
      logSessionGeneration += 1;
      cleanupAllConnections();
      ciLogLoading.value = false;
      cdLogLoading.value = false;
    }
    // Permission loss must stop log delivery, but it is a 403-style
    // authorization change—not a reason to destroy an otherwise valid login.
    if (invalidateIdentity && authStore.status !== 'anonymous') authStore.invalidate(reason);
  };

  const sessionAllowsSseRetry = async () => {
    if (sseAuthBlocked) return false;
    if (sessionProbe) return sessionProbe;
    sessionProbe = (async () => {
      const authenticated = await authStore.refreshSession();
      const allowed = authenticated && authStore.can(PERMISSIONS.LOGS_READ);
      if (!allowed) blockSseForInvalidSession('logs_forbidden', false);
      return allowed;
    })().finally(() => {
      sessionProbe = null;
    });
    return sessionProbe;
  };

  const runSseRetry = (
    reconnect: () => Promise<void>,
    resolveOnce: (value?: unknown) => void,
    rejectOnce: (error: unknown) => void
  ) => {
    void sessionAllowsSseRetry()
      .then(allowed => {
        if (!allowed) {
          resolveOnce();
          return;
        }
        return reconnect().then(resolveOnce).catch(rejectOnce);
      })
      .catch(rejectOnce);
  };

  watch(
    [() => authStore.status, () => authStore.permissions],
    ([status]) => {
      if (status === 'authenticated' && authStore.can(PERMISSIONS.LOGS_READ)) {
        sseAuthBlocked = false;
        return;
      }
      if (
        status === 'anonymous' ||
        (status === 'authenticated' && !authStore.can(PERMISSIONS.LOGS_READ))
      ) {
        blockSseForInvalidSession('logs_forbidden', false);
      }
    },
    { flush: 'sync' }
  );

  // 工具函数
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
    const hash = Array.from(env).reduce((sum, char) => sum + char.charCodeAt(0), 0);
    return tagTypes[hash % tagTypes.length];
  };

  const formatDateTime = (dateTime: string): string => {
    if (!dateTime) return '';
    const date = new Date(dateTime);
    return date.toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
  };

  // 自动滚动到底部
  const scrollToBottom = (container: HTMLElement | undefined) => {
    if (container) {
      nextTick(() => {
        container.scrollTop = container.scrollHeight;
      });
    }
  };

  const manualScrollToBottom = () => {
    if (activeLogTab.value === 'ci') {
      scrollToBottom(ciLogContainer.value);
    } else {
      scrollToBottom(cdLogContainer.value);
    }
  };

  // 查询日志
  const handleSearch = async () => {
    if (!authStore.isAuthenticated || !authStore.can(PERMISSIONS.LOGS_READ)) return;
    logLoading.value = true;
    try {
      const params = {
        page_num: currentPage.value,
        page_size: pageSize.value,
        app_name: logFilter.serviceName || undefined,
        env: logFilter.environment || undefined,
        start_time:
          logFilter.dateRange && logFilter.dateRange[0]
            ? logFilter.dateRange[0].toISOString()
            : undefined,
        end_time:
          logFilter.dateRange && logFilter.dateRange[1]
            ? logFilter.dateRange[1].toISOString()
            : undefined,
      };

      const response = await queryPublishLogs(params);

      if (response.data.code === 1) {
        const result = response.data.result;
        // 转换数据格式，添加空值检查
        if (result && result.task_record && Array.isArray(result.task_record)) {
          logList.value = result.task_record.map((item: any) => ({
            task_id: item.task_id,
            serviceName: item.app_name,
            branch: item.branch,
            environment: item.env,
            status: getDeployStatus(item.status),
            deployTime: formatDateTime(item.created_at),
            operator: item.publisher,
            message: normalizeLegacyNullableText(item.message),
            auto_deploy: item.auto_deploy,
            ci_job_name: normalizeLegacyNullableText(item.ci_job_name),
            cd_job_name: normalizeLegacyNullableText(item.cd_job_name),
            ci_build_id: item.ci_build_id || 0,
            cd_build_id: item.cd_build_id || 0,
            products: normalizeLegacyNullableText(item.products),
          }));
          total.value = result.total || 0;
        } else {
          // 处理空结果的情况
          logList.value = [];
          total.value = 0;
        }

        // 显示查询结果提示
        if (
          isFirstLoad.value ||
          logFilter.serviceName ||
          logFilter.environment ||
          logFilter.dateRange.length > 0
        ) {
          if (logList.value.length > 0) {
            ElMessage.success(`查询成功，共找到 ${total.value} 条记录`);
          } else {
            ElMessage.info('查询完成，未找到相关记录');
          }
        }

        // 标记已不是首次加载
        isFirstLoad.value = false;
      } else {
        throw new Error(response.data.message || '查询失败');
      }
    } catch (error) {
      console.error('查询日志失败:', error);
      const errorMessage = error instanceof Error ? error.message : '查询失败';
      ElMessage.error(errorMessage);

      // 清空数据
      logList.value = [];
      total.value = 0;
    } finally {
      logLoading.value = false;
    }
  };

  // 重置日志筛选条件
  const handleResetLogFilter = () => {
    logFilter.serviceName = '';
    logFilter.environment = '';
    logFilter.dateRange = [];
    currentPage.value = 1;
    pageSize.value = 10;
    isFirstLoad.value = true; // 重置时重新设置首次加载标志
    // 重置后立即查询
    handleSearch();
  };

  // 查看日志详情
  const viewLogDetail = (row: LogItem) => {
    // 将LogItem转换为DeployingService格式
    const deployingService: DeployingService = {
      id: row.task_id,
      serviceName: row.serviceName,
      branch: row.branch,
      environment: row.environment,
      status: row.status,
      progress: 100, // 已完成的任务进度为100
      startTime: row.deployTime,
      operator: row.operator,
      message: row.message,
      taskId: row.task_id,
      ciJobName: row.ci_job_name,
      cdJobName: row.cd_job_name,
      ciBuildId: row.ci_build_id,
      cdBuildId: row.cd_build_id,
      products: row.products,
      auto_deploy: row.auto_deploy,
    };

    currentLog.value = deployingService;
    logDialogVisible.value = true;
    activeLogTab.value = 'ci';
    fetchLogs(deployingService);
  };

  // 分页处理
  const handleSizeChange = (val: number) => {
    pageSize.value = val;
    handleSearch();
  };

  const handleCurrentChange = (val: number) => {
    currentPage.value = val;
    handleSearch();
  };

  // 获取日志
  const fetchLogs = async (row: DeployingService) => {
    if (sseAuthBlocked || !authStore.isAuthenticated || !authStore.can(PERMISSIONS.LOGS_READ)) {
      return;
    }
    console.log('=== 开始获取日志 ===');
    console.log('服务信息:', row);
    console.log('当前标签页:', activeLogTab.value);
    console.log('CI日志内容长度:', ciLog.value?.length || 0);
    console.log('CD日志内容长度:', cdLog.value?.length || 0);
    console.log('CI日志loading:', ciLogLoading.value);
    console.log('CD日志loading:', cdLogLoading.value);

    // 检查并恢复日志状态
    if (checkAndRestoreLogState(row)) {
      console.log('日志状态已恢复，跳过重新获取');
      return;
    }

    console.log('需要获取日志，继续执行...');

    // 只有在真正需要重新获取时才重置性能指标
    // 避免在复用连接时重置firstDataTime
    if (
      !isConnectionActive('ci', row.ciJobName || '', row.ciBuildId || 0) &&
      !isConnectionActive('cd', row.cdJobName || '', row.cdBuildId || 0)
    ) {
      resetPerformanceMetrics();
    }

    // 显示当前活跃连接
    const activeConnections = getActiveConnections();
    if (activeConnections.length > 0) {
      console.log('当前活跃连接:', activeConnections);
    }

    if (activeLogTab.value === 'ci') {
      // 只有在没有日志内容且没有活跃连接时才显示loading
      if (!ciLog.value && !isConnectionActive('ci', row.ciJobName || '', row.ciBuildId || 0)) {
        ciLogLoading.value = true;
      }
      // 不清空现有日志，避免突然消失
      if (!ciLog.value) {
        ciLog.value = '';
      }
      try {
        if (row.ciJobName && row.ciBuildId) {
          await fetchCiLogsStream(row.ciJobName, row.ciBuildId);
        } else {
          ciLog.value = '暂无CI日志信息';
          if (row.ciJobName && row.ciBuildId) {
            streamingStatus.value.set(
              generateConnectionId('ci', row.ciJobName, row.ciBuildId),
              false
            );
          }
        }
      } catch (error) {
        console.error('获取CI日志失败:', error);
        if (!ciLog.value) {
          ciLog.value = '获取CI日志失败: ' + (error instanceof Error ? error.message : '未知错误');
        }
        // 不抛出错误，避免界面崩溃
      } finally {
        ciLogLoading.value = false;
      }
    } else if (activeLogTab.value === 'cd') {
      // 只有在没有日志内容且没有活跃连接时才显示loading
      if (!cdLog.value && !isConnectionActive('cd', row.cdJobName || '', row.cdBuildId || 0)) {
        cdLogLoading.value = true;
      }
      // 不清空现有日志，避免突然消失
      if (!cdLog.value) {
        cdLog.value = '';
      }
      try {
        if (row.cdJobName && row.cdBuildId) {
          await fetchCdLogsStream(row.cdJobName, row.cdBuildId);
        } else {
          cdLog.value = '暂无CD日志信息';
          if (row.cdJobName && row.cdBuildId) {
            streamingStatus.value.set(
              generateConnectionId('cd', row.cdJobName, row.cdBuildId),
              false
            );
          }
        }
      } catch (error) {
        console.error('获取CD日志失败:', error);
        if (!cdLog.value) {
          cdLog.value = '获取CD日志失败: ' + (error instanceof Error ? error.message : '未知错误');
        }
        // 不抛出错误，避免界面崩溃
      } finally {
        cdLogLoading.value = false;
      }
    }
  };

  // 使用SSE获取CI日志
  const fetchCiLogsStream = async (
    jobName: string,
    buildId: number,
    taskId = currentLog.value.taskId,
    sessionGeneration = logSessionGeneration
  ) => {
    if (!taskId || !isLogSessionCurrent(sessionGeneration, taskId)) return;

    return new Promise<void>((resolve, reject) => {
      // 检查是否已经存在相同的活跃连接
      if (isConnectionActive('ci', jobName, buildId)) {
        console.log(`复用现有CI日志SSE连接: ${jobName}_${buildId}`);
        resolve();
        return;
      }

      const connectionId = generateConnectionId('ci', jobName, buildId);
      // 清掉失效 transport，但保留断线续传游标和生命周期预算。
      closeConnectionPreservingCursor(connectionId);
      if (completedConnections.has(connectionId)) {
        resolve();
        return;
      }
      const start = lastOffsetMap.value.get(connectionId);
      const url =
        `/api/v1/job/stream/log?task_id=${taskId}&log_type=ci` +
        (typeof start === 'number' && !Number.isNaN(start) ? `&start=${start}` : '');

      console.log('开始获取CI日志:', { jobName, buildId, url });

      const eventSource = new EventSource(url, { withCredentials: true });
      eventSourceMap.value.set(connectionId, eventSource);
      streamingStatus.value.set(connectionId, true);

      let hasReceivedData = false;
      let isResolved = false;
      let is404Error = false; // 添加404错误标志
      let reconnectScheduled = false;

      const resolveOnce = (value?: any) => {
        if (!isResolved) {
          isResolved = true;
          if (connectionSettlers.get(connectionId) === resolveOnce) {
            connectionSettlers.delete(connectionId);
          }
          resolve(value);
        }
      };

      const rejectOnce = (error: any) => {
        if (!isResolved) {
          isResolved = true;
          if (connectionSettlers.get(connectionId) === resolveOnce) {
            connectionSettlers.delete(connectionId);
          }
          reject(error);
        }
      };
      connectionSettlers.set(connectionId, resolveOnce);

      let silenceTimeout: ReturnType<typeof setTimeout> | null = null;
      const clearSilenceTimeout = () => {
        if (silenceTimeout) {
          clearConnectionTimeout(silenceTimeout);
          silenceTimeout = null;
        }
      };
      const retryCiStream = (message: string) => {
        if (isResolved || reconnectScheduled) return;
        reconnectScheduled = true;
        clearSilenceTimeout();
        closeConnectionPreservingCursor(connectionId, eventSource);
        cleanupCiHeartbeat();
        const attempt = claimAutomaticReconnect(connectionId);
        if (attempt !== null) {
          scheduledReconnects.add(connectionId);
          setConnectionTimeout(
            () => {
              scheduledReconnects.delete(connectionId);
              runSseRetry(
                () => fetchCiLogsStream(jobName, buildId, taskId, sessionGeneration),
                resolveOnce,
                rejectOnce
              );
            },
            3000 * attempt,
            sessionGeneration,
            taskId
          );
          return;
        }
        rejectOnce(new Error(message));
      };
      const resetSilenceTimeout = () => {
        clearSilenceTimeout();
        silenceTimeout = setConnectionTimeout(
          () => {
            silenceTimeout = null;
            console.log('CI日志SSE静默超时，准备从最后游标重连');
            retryCiStream('CI日志获取静默超时，已达到最大重试次数');
          },
          60000,
          sessionGeneration,
          taskId
        );
      };

      eventSource.onmessage = (event: MessageEvent) => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        resetSilenceTimeout();
        rememberStreamOffset(connectionId, event);
        try {
          console.log('CI日志SSE收到数据，字符数:', event.data.length);
          hasReceivedData = true;

          // 记录第一个数据的时间
          if (performanceMetrics.value.firstDataTime === 0) {
            performanceMetrics.value.firstDataTime = Date.now();
            const connectionDelay =
              performanceMetrics.value.firstDataTime - performanceMetrics.value.connectionTime;
            console.log(`SSE第一个数据延迟: ${connectionDelay}ms`);
          }

          performanceMetrics.value.totalDataCount++;

          // 解析JSON数据（只关心 result: string[]）
          const data = JSON.parse(event.data);
          if (data.code === 1 && data.result && Array.isArray(data.result)) {
            const logLines = data.result.filter(
              (line: unknown): line is string => typeof line === 'string'
            );
            // 空数组直接忽略（后端心跳通过 event: ping 推送）
            if (logLines.length === 0) return;

            // 如果是首次收到数据，立即显示历史日志
            if (performanceMetrics.value.totalDataCount === 1) {
              console.log('首次收到CI日志数据，立即显示历史日志:', logLines.length, '行');
              ciLog.value = appendBoundedLog('', logLines);
              // 首次数据到达，立即关闭 loading，避免一直转圈
              if (ciLogLoading.value) {
                ciLogLoading.value = false;
              }
              // 立即滚动到底部
              setTimeout(() => scrollToBottomIfActive('ci', ciLogContainer.value), 100);
            } else {
              // 后续数据使用批量更新，提升性能
              addToUpdateQueue('ci', logLines);
            }
            performanceMetrics.value.updateCount++;
          } else if (data.code === 0) {
            const code = typeof data.error === 'string' ? data.error.trim() : '';
            console.error('CI日志SSE返回旧版错误:', code || 'unknown');
            if (code === '404') {
              is404Error = true;
              if (!ciLog.value) ciLog.value = '未找到日志信息';
              clearSilenceTimeout();
              cleanupCiHeartbeat();
              closeConnectionPreservingCursor(connectionId, eventSource);
              rejectOnce(new Error('任务不存在: 404'));
              return;
            }
            if (!ciLog.value) {
              ciLog.value = '获取CI日志失败: 日志服务暂时不可用';
            }
            retryCiStream('CI日志服务暂时不可用，已达到最大重试次数');
          }
        } catch (error) {
          console.error('解析CI日志SSE数据失败:', error);
          if (!ciLog.value) {
            ciLog.value = '解析CI日志数据失败';
          }
          retryCiStream('CI日志数据格式异常，已达到最大重试次数');
        }
      };

      // 服务端心跳证明连接仍有活动，并携带当前可续传游标。
      eventSource.addEventListener('ping', event => {
        resetSilenceTimeout();
        rememberStreamOffset(connectionId, event as MessageEvent);
      });

      eventSource.addEventListener('auth-expired', () => {
        eventSource.close();
        resolveOnce();
        blockSseForInvalidSession();
      });

      eventSource.onerror = async error => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        // Native EventSource retries indefinitely unless explicitly closed.
        // Stop that transport before the asynchronous session probe so every
        // subsequent connection is accounted for by our lifecycle budget.
        clearSilenceTimeout();
        cleanupCiHeartbeat();
        closeConnectionPreservingCursor(connectionId, eventSource);
        if (!(await sessionAllowsSseRetry())) {
          resolveOnce();
          return;
        }
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        console.error('CI日志SSE连接错误:', error);
        console.log(
          'CI日志SSE连接错误详情 - hasReceivedData:',
          hasReceivedData,
          'automaticReconnects:',
          automaticReconnects.get(connectionId) || 0,
          'readyState:',
          eventSource.readyState,
          'is404Error:',
          is404Error
        );

        // 尝试解析错误数据，检查是否为404错误
        try {
          if (error instanceof MessageEvent && error.data) {
            const errorData = JSON.parse(error.data);
            console.log('CI日志SSE原生错误处理器解析到错误数据:', errorData);

            if (errorData.error === '404') {
              console.log('CI日志SSE原生错误处理器检测到404错误，不再重试');
              is404Error = true;
              if (!ciLog.value) {
                ciLog.value = '未找到日志信息';
              }
              eventSource.close();
              eventSourceMap.value.delete(connectionId);
              streamingStatus.value.set(connectionId, false);
              clearSilenceTimeout();
              cleanupCiHeartbeat();
              rejectOnce(new Error(`任务不存在: ${errorData.error}`));
              return;
            }
          }
        } catch (parseError) {
          console.log('CI日志SSE原生错误处理器无法解析错误数据:', parseError);
        }

        // 如果是404错误，不再重试
        if (is404Error) {
          console.log('CI日志SSE 404错误已处理，不再重试');
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          clearSilenceTimeout();
          cleanupCiHeartbeat();
          return;
        }

        console.log('CI日志SSE连接失败，准备有界重试');
        retryCiStream('CI日志SSE连接失败，已重试' + LOG_STREAM_MAX_AUTOMATIC_RECONNECTS + '次');
      };

      eventSource.onopen = () => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        console.log('=== CI日志SSE连接已建立 ===');
        console.log('连接ID:', connectionId);
        console.log('URL:', url);
        performanceMetrics.value.connectionTime = Date.now();
        console.log('SSE连接建立时间:', new Date().toISOString());
        resetSilenceTimeout();
        // 确保连接状态为活跃
        streamingStatus.value.set(connectionId, true);
        console.log('CI连接状态已设置为活跃');
        // 连接已建立就结束“获取中”的 loading，让界面至少显示“实时获取中/等待输出”
        // 注意：真正的日志追加仍依赖服务端正确 flush SSE event（以 \n\n 结束事件）
        if (ciLogLoading.value) {
          ciLogLoading.value = false;
        }
      };

      // 只把长时间没有任何 message/ping 的连接视为失活；健康流量会滑动续期。
      resetSilenceTimeout();

      // 监听日志流结束事件（event: end）
      eventSource.addEventListener('end', (event: MessageEvent) => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        rememberStreamOffset(connectionId, event);
        const reason = streamEndReason(event.data);
        console.log('收到SSE end事件:', reason || 'unknown');
        if (updateTimer.value) {
          clearTimeout(updateTimer.value);
          updateTimer.value = null;
        }
        batchUpdateLogs();
        if (reason === 'completed') {
          completedConnections.add(connectionId);
          clearSilenceTimeout();
          cleanupCiHeartbeat();
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          resolveOnce();
          return;
        }
        retryCiStream(`CI日志流结束（${reason || 'unknown'}），已达到最大重试次数`);
      });

      // EventSource 的 error 事件保留给传输失败；服务端语义错误使用独立事件名。
      eventSource.addEventListener('stream-error', (event: MessageEvent) => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        console.log('收到SSE stream-error事件，处理错误');
        try {
          const errorData = streamErrorDetails(event.data);
          console.error('CI日志SSE错误事件:', errorData.code);

          // 显示错误信息
          if (!ciLog.value) {
            ciLog.value = `获取CI日志失败: ${errorData.message}`;
          }

          // 清理资源
          clearSilenceTimeout();
          cleanupCiHeartbeat();
          if (updateTimer.value) {
            clearTimeout(updateTimer.value);
            updateTimer.value = null;
          }

          // 关闭连接
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);

          // 根据错误类型决定是否重试
          if (errorData.code === '404') {
            console.log('CI日志SSE 404错误，任务可能不存在，不再重试');
            is404Error = true; // 设置404错误标志
            if (!ciLog.value) {
              ciLog.value = '未找到日志信息';
            }
            rejectOnce(new Error('任务不存在: 404'));
          } else {
            console.log('CI日志SSE其他错误，尝试重试');
            retryCiStream(`CI日志获取失败: ${errorData.message}`);
          }
        } catch (parseError) {
          console.error('解析CI日志SSE错误事件失败:', parseError);
          // 如果解析失败，按普通错误处理
          clearSilenceTimeout();
          cleanupCiHeartbeat();
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          rejectOnce(new Error('解析错误事件失败'));
        }
      });

      // 监听连接关闭事件
      eventSource.addEventListener('close', event => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        console.log('CI日志SSE连接关闭事件触发');
        rememberStreamOffset(connectionId, event as MessageEvent);
        retryCiStream('CI日志流意外关闭，已达到最大重试次数');
      });

      // 添加心跳检测机制，在日志流暂停期间保持连接
      let ciHeartbeatCount = 0;
      const ciHeartbeatInterval = setConnectionInterval(
        () => {
          ciHeartbeatCount++;
          console.log(
            `CI日志SSE心跳检测 #${ciHeartbeatCount} - 连接状态: ${eventSource.readyState}`
          );

          // 如果连接已关闭，清理心跳定时器
          if (eventSource.readyState === EventSource.CLOSED) {
            console.log('CI日志SSE连接已关闭，停止心跳检测');
            clearConnectionInterval(ciHeartbeatInterval);
            return;
          }

          // 如果长时间没有收到数据，可能是连接问题
          if (ciHeartbeatCount > 60) {
            // 60秒没有数据
            console.log('CI日志SSE心跳检测超时，连接可能有问题');
            clearConnectionInterval(ciHeartbeatInterval);
            // 不主动关闭连接，让错误处理逻辑处理
          }
        },
        1000,
        sessionGeneration,
        taskId
      ); // 每秒检测一次

      // 在连接关闭时清理心跳定时器
      const cleanupCiHeartbeat = () => {
        clearConnectionInterval(ciHeartbeatInterval);
      };
    });
  };

  // 使用SSE获取CD日志
  const fetchCdLogsStream = async (
    jobName: string,
    buildId: number,
    taskId = currentLog.value.taskId,
    sessionGeneration = logSessionGeneration
  ) => {
    if (!taskId || !isLogSessionCurrent(sessionGeneration, taskId)) return;

    return new Promise<void>((resolve, reject) => {
      // 检查是否已经存在相同的活跃连接
      if (isConnectionActive('cd', jobName, buildId)) {
        console.log(`复用现有CD日志SSE连接: ${jobName}_${buildId}`);
        resolve();
        return;
      }

      const connectionId = generateConnectionId('cd', jobName, buildId);
      // 清掉失效 transport，但保留断线续传游标和生命周期预算。
      closeConnectionPreservingCursor(connectionId);
      if (completedConnections.has(connectionId)) {
        resolve();
        return;
      }
      const start = lastOffsetMap.value.get(connectionId);
      const url =
        `/api/v1/job/stream/log?task_id=${taskId}&log_type=cd` +
        (typeof start === 'number' && !Number.isNaN(start) ? `&start=${start}` : '');

      console.log('开始获取CD日志:', { jobName, buildId, url });

      const eventSource = new EventSource(url, { withCredentials: true });
      eventSourceMap.value.set(connectionId, eventSource);
      streamingStatus.value.set(connectionId, true);

      let hasReceivedData = false;
      let isResolved = false;
      let messageCount = 0; // 添加消息计数器
      let is404Error = false; // 添加404错误标志
      let reconnectScheduled = false;

      const resolveOnce = (value?: any) => {
        if (!isResolved) {
          isResolved = true;
          if (connectionSettlers.get(connectionId) === resolveOnce) {
            connectionSettlers.delete(connectionId);
          }
          resolve(value);
        }
      };

      const rejectOnce = (error: any) => {
        if (!isResolved) {
          isResolved = true;
          if (connectionSettlers.get(connectionId) === resolveOnce) {
            connectionSettlers.delete(connectionId);
          }
          reject(error);
        }
      };
      connectionSettlers.set(connectionId, resolveOnce);

      let silenceTimeout: ReturnType<typeof setTimeout> | null = null;
      const clearSilenceTimeout = () => {
        if (silenceTimeout) {
          clearConnectionTimeout(silenceTimeout);
          silenceTimeout = null;
        }
      };
      const retryCdStream = (message: string) => {
        if (isResolved || reconnectScheduled) return;
        reconnectScheduled = true;
        clearSilenceTimeout();
        closeConnectionPreservingCursor(connectionId, eventSource);
        cleanupHeartbeat();
        const attempt = claimAutomaticReconnect(connectionId);
        if (attempt !== null) {
          scheduledReconnects.add(connectionId);
          setConnectionTimeout(
            () => {
              scheduledReconnects.delete(connectionId);
              runSseRetry(
                () => fetchCdLogsStream(jobName, buildId, taskId, sessionGeneration),
                resolveOnce,
                rejectOnce
              );
            },
            3000 * attempt,
            sessionGeneration,
            taskId
          );
          return;
        }
        rejectOnce(new Error(message));
      };
      const resetSilenceTimeout = () => {
        clearSilenceTimeout();
        silenceTimeout = setConnectionTimeout(
          () => {
            silenceTimeout = null;
            console.log('CD日志SSE静默超时，准备从最后游标重连');
            retryCdStream('CD日志获取静默超时，已达到最大重试次数');
          },
          120000,
          sessionGeneration,
          taskId
        );
      };

      eventSource.onmessage = (event: MessageEvent) => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        resetSilenceTimeout();
        rememberStreamOffset(connectionId, event);
        try {
          messageCount++;
          console.log(`CD日志SSE收到第${messageCount}条数据，字符数:`, event.data.length);
          hasReceivedData = true;

          // 记录第一个数据的时间
          if (performanceMetrics.value.cdFirstDataTime === 0) {
            performanceMetrics.value.cdFirstDataTime = Date.now();
            const connectionDelay =
              performanceMetrics.value.cdFirstDataTime - performanceMetrics.value.cdConnectionTime;
            console.log(`CD日志SSE第一个数据延迟: ${connectionDelay}ms`);
          }

          performanceMetrics.value.cdDataCount++;
          console.log(`CD日志数据计数: ${performanceMetrics.value.cdDataCount}`);

          // 解析JSON数据（只关心 result: string[]）
          const data = JSON.parse(event.data);
          if (data.code === 1 && data.result && Array.isArray(data.result)) {
            const logLines = data.result.filter(
              (line: unknown): line is string => typeof line === 'string'
            );
            // 空数组直接忽略（后端心跳通过 event: ping 推送）
            if (logLines.length === 0) return;

            // 如果是首次收到数据，立即显示历史日志
            if (performanceMetrics.value.cdDataCount === 1) {
              console.log('首次收到CD日志数据，立即显示历史日志:', logLines.length, '行');
              cdLog.value = appendBoundedLog('', logLines);
              // 首次数据到达，立即关闭 loading，避免一直转圈
              if (cdLogLoading.value) {
                cdLogLoading.value = false;
              }
              // 立即滚动到底部
              setTimeout(() => scrollToBottomIfActive('cd', cdLogContainer.value), 100);
            } else {
              // 后续数据使用批量更新，提升性能
              console.log(`CD日志后续数据，添加到更新队列: ${logLines.length} 行`);
              addToUpdateQueue('cd', logLines);
            }
            performanceMetrics.value.updateCount++;
          } else if (data.code === 0) {
            const code = typeof data.error === 'string' ? data.error.trim() : '';
            console.error('CD日志SSE返回旧版错误:', code || 'unknown');
            if (code === '404') {
              is404Error = true;
              if (!cdLog.value) cdLog.value = '未找到日志信息';
              clearSilenceTimeout();
              cleanupHeartbeat();
              closeConnectionPreservingCursor(connectionId, eventSource);
              rejectOnce(new Error('任务不存在: 404'));
              return;
            }
            if (!cdLog.value) {
              cdLog.value = '获取CD日志失败: 日志服务暂时不可用';
            }
            retryCdStream('CD日志服务暂时不可用，已达到最大重试次数');
          } else {
            console.log('CD日志SSE收到未知数据格式:', data);
          }
        } catch (error) {
          console.error('解析CD日志SSE数据失败:', error);
          if (!cdLog.value) {
            cdLog.value = '解析CD日志数据失败';
          }
          retryCdStream('CD日志数据格式异常，已达到最大重试次数');
        }
      };

      // 服务端心跳证明连接仍有活动，并携带当前可续传游标。
      eventSource.addEventListener('ping', event => {
        resetSilenceTimeout();
        rememberStreamOffset(connectionId, event as MessageEvent);
      });

      eventSource.addEventListener('auth-expired', () => {
        eventSource.close();
        resolveOnce();
        blockSseForInvalidSession();
      });

      eventSource.onerror = async error => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        // Stop EventSource's implicit unbounded retry before probing auth.
        clearSilenceTimeout();
        cleanupHeartbeat();
        closeConnectionPreservingCursor(connectionId, eventSource);
        if (!(await sessionAllowsSseRetry())) {
          resolveOnce();
          return;
        }
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        console.error('CD日志SSE连接错误:', error);
        console.log(
          'CD日志SSE连接错误详情 - hasReceivedData:',
          hasReceivedData,
          'automaticReconnects:',
          automaticReconnects.get(connectionId) || 0,
          'readyState:',
          eventSource.readyState,
          'is404Error:',
          is404Error
        );

        // 尝试解析错误数据，检查是否为404错误
        try {
          if (error instanceof MessageEvent && error.data) {
            const errorData = JSON.parse(error.data);
            console.log('CD日志SSE原生错误处理器解析到错误数据:', errorData);

            if (errorData.error === '404') {
              console.log('CD日志SSE原生错误处理器检测到404错误，不再重试');
              is404Error = true;
              if (!cdLog.value) {
                cdLog.value = '未找到日志信息';
              }
              eventSource.close();
              eventSourceMap.value.delete(connectionId);
              streamingStatus.value.set(connectionId, false);
              clearSilenceTimeout();
              cleanupHeartbeat();
              rejectOnce(new Error(`任务不存在: ${errorData.error}`));
              return;
            }
          }
        } catch (parseError) {
          console.log('CD日志SSE原生错误处理器无法解析错误数据:', parseError);
        }

        // 如果是404错误，不再重试
        if (is404Error) {
          console.log('CD日志SSE 404错误已处理，不再重试');
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          clearSilenceTimeout();
          cleanupHeartbeat();
          return;
        }

        console.log('CD日志SSE连接失败，准备有界重试');
        retryCdStream('CD日志SSE连接失败，已重试' + LOG_STREAM_MAX_AUTOMATIC_RECONNECTS + '次');
      };

      eventSource.onopen = () => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        console.log('=== CD日志SSE连接已建立 ===');
        console.log('连接ID:', connectionId);
        console.log('URL:', url);
        performanceMetrics.value.cdConnectionTime = Date.now();
        console.log('CD日志SSE连接建立时间:', new Date().toISOString());
        resetSilenceTimeout();
        // 确保连接状态为活跃
        streamingStatus.value.set(connectionId, true);
        console.log('CD连接状态已设置为活跃');
        // 连接已建立就结束“获取中”的 loading，让界面至少显示“实时获取中/等待输出”
        if (cdLogLoading.value) {
          cdLogLoading.value = false;
        }
      };

      // 只把长时间没有任何 message/ping 的连接视为失活；健康流量会滑动续期。
      resetSilenceTimeout();

      // 监听日志流结束事件（event: end）
      eventSource.addEventListener('end', (event: MessageEvent) => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        rememberStreamOffset(connectionId, event);
        const reason = streamEndReason(event.data);
        console.log('收到SSE end事件:', reason || 'unknown');
        if (updateTimer.value) {
          clearTimeout(updateTimer.value);
          updateTimer.value = null;
        }
        batchUpdateLogs();
        if (reason === 'completed') {
          completedConnections.add(connectionId);
          clearSilenceTimeout();
          cleanupHeartbeat();
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          resolveOnce();
          return;
        }
        retryCdStream(`CD日志流结束（${reason || 'unknown'}），已达到最大重试次数`);
      });

      // EventSource 的 error 事件保留给传输失败；服务端语义错误使用独立事件名。
      eventSource.addEventListener('stream-error', (event: MessageEvent) => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        console.log('收到SSE stream-error事件，处理错误');
        try {
          const errorData = streamErrorDetails(event.data);
          console.error('CD日志SSE错误事件:', errorData.code);

          // 显示错误信息
          if (!cdLog.value) {
            cdLog.value = `获取CD日志失败: ${errorData.message}`;
          }

          // 清理资源
          clearSilenceTimeout();
          cleanupHeartbeat();
          if (updateTimer.value) {
            clearTimeout(updateTimer.value);
            updateTimer.value = null;
          }

          // 关闭连接
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);

          // 根据错误类型决定是否重试
          if (errorData.code === '404') {
            console.log('CD日志SSE 404错误，任务可能不存在，不再重试');
            is404Error = true; // 设置404错误标志
            if (!cdLog.value) {
              cdLog.value = '未找到日志信息';
            }
            rejectOnce(new Error('任务不存在: 404'));
          } else {
            console.log('CD日志SSE其他错误，尝试重试');
            retryCdStream(`CD日志获取失败: ${errorData.message}`);
          }
        } catch (parseError) {
          console.error('解析CD日志SSE错误事件失败:', parseError);
          // 如果解析失败，按普通错误处理
          clearSilenceTimeout();
          cleanupHeartbeat();
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          rejectOnce(new Error('解析错误事件失败'));
        }
      });

      // 监听连接关闭事件
      eventSource.addEventListener('close', event => {
        if (!isLogSessionCurrent(sessionGeneration, taskId)) {
          eventSource.close();
          resolveOnce();
          return;
        }
        console.log('CD日志SSE连接关闭事件触发');
        rememberStreamOffset(connectionId, event as MessageEvent);
        retryCdStream('CD日志流意外关闭，已达到最大重试次数');
      });

      // 添加心跳检测机制，在日志流暂停期间保持连接
      let heartbeatCount = 0;
      const heartbeatInterval = setConnectionInterval(
        () => {
          heartbeatCount++;
          console.log(`CD日志SSE心跳检测 #${heartbeatCount} - 连接状态: ${eventSource.readyState}`);

          // 如果连接已关闭，清理心跳定时器
          if (eventSource.readyState === EventSource.CLOSED) {
            console.log('CD日志SSE连接已关闭，停止心跳检测');
            clearConnectionInterval(heartbeatInterval);
            return;
          }

          // 如果长时间没有收到数据，可能是连接问题
          if (heartbeatCount > 60) {
            // 60秒没有数据
            console.log('CD日志SSE心跳检测超时，连接可能有问题');
            clearConnectionInterval(heartbeatInterval);
            // 不主动关闭连接，让错误处理逻辑处理
          }
        },
        1000,
        sessionGeneration,
        taskId
      ); // 每秒检测一次

      // 在连接关闭时清理心跳定时器
      const cleanupHeartbeat = () => {
        clearConnectionInterval(heartbeatInterval);
      };
    });
  };

  // 监听CI日志内容变化，自动滚动到底部
  watch(ciLog, () => {
    scrollToBottom(ciLogContainer.value);
  });

  // 监听CD日志内容变化，自动滚动到底部
  watch(cdLog, () => {
    scrollToBottom(cdLogContainer.value);
  });

  // 定期检查连接状态
  const connectionCheckTimer = ref<ReturnType<typeof setInterval> | null>(null);

  const reconnectFromHealthCheck = (type: 'ci' | 'cd', jobName: string, buildId: number) => {
    const connectionId = generateConnectionId(type, jobName, buildId);
    const connection = eventSourceMap.value.get(connectionId);
    if (
      completedConnections.has(connectionId) ||
      scheduledReconnects.has(connectionId) ||
      (connection && connection.readyState !== EventSource.CLOSED)
    ) {
      return;
    }

    const attempt = claimAutomaticReconnect(connectionId);
    if (attempt === null) return;

    const generation = logSessionGeneration;
    const taskId = currentLog.value.taskId;
    scheduledReconnects.add(connectionId);
    console.log(`定期检查发现${type.toUpperCase()}连接已关闭，尝试第 ${attempt} 次自动重连`);
    void sessionAllowsSseRetry()
      .then(allowed => {
        scheduledReconnects.delete(connectionId);
        if (
          !allowed ||
          !logDialogVisible.value ||
          activeLogTab.value !== type ||
          !isLogSessionCurrent(generation, taskId)
        ) {
          return;
        }
        return type === 'ci'
          ? fetchCiLogsStream(jobName, buildId, taskId, generation)
          : fetchCdLogsStream(jobName, buildId, taskId, generation);
      })
      // Periodic checks are fire-and-forget; consume a terminal stream failure
      // here so browsers do not report an unhandled promise rejection.
      .catch(error => {
        scheduledReconnects.delete(connectionId);
        console.error(`${type.toUpperCase()}日志健康检查重连失败:`, error);
      });
  };

  // 开始定期检查连接状态
  const startConnectionCheck = () => {
    if (sseAuthBlocked || !authStore.isAuthenticated || !authStore.can(PERMISSIONS.LOGS_READ)) {
      return;
    }
    if (connectionCheckTimer.value) {
      clearInterval(connectionCheckTimer.value);
    }

    connectionCheckTimer.value = setInterval(() => {
      if (
        sseAuthBlocked ||
        !authStore.isAuthenticated ||
        !authStore.can(PERMISSIONS.LOGS_READ) ||
        !logDialogVisible.value ||
        !currentLog.value.taskId
      ) {
        return;
      }

      // Only the visible tab owns a live stream. Hidden CI/CD tabs can be
      // resumed on demand without consuming retry budget in the background.
      if (activeLogTab.value === 'ci' && currentLog.value.ciJobName && currentLog.value.ciBuildId) {
        reconnectFromHealthCheck('ci', currentLog.value.ciJobName, currentLog.value.ciBuildId);
      } else if (
        activeLogTab.value === 'cd' &&
        currentLog.value.cdJobName &&
        currentLog.value.cdBuildId
      ) {
        reconnectFromHealthCheck('cd', currentLog.value.cdJobName, currentLog.value.cdBuildId);
      }
    }, 30000); // 每30秒检查一次
  };

  // 停止定期检查连接状态
  const stopConnectionCheck = () => {
    if (connectionCheckTimer.value) {
      clearInterval(connectionCheckTimer.value);
      connectionCheckTimer.value = null;
    }
  };

  // 日志对话框关闭处理
  const handleLogDialogClose = () => {
    if (logDialogPaused) return;
    logDialogPaused = true;
    console.log('日志对话框关闭，终止SSE连接并保留日志游标');
    // 先使当前 transport 的事件及异步鉴权探针失效。
    logSessionGeneration += 1;
    stopConnectionCheck();

    // 把关闭前已经接收的队列落入 buffer，避免再次打开时丢日志。
    if (updateTimer.value) {
      clearTimeout(updateTimer.value);
      updateTimer.value = null;
    }
    batchUpdateLogs();
    pauseAllConnections();

    ciLogLoading.value = false;
    cdLogLoading.value = false;
  };

  // 检测SSE连接是否真的还在工作
  const isConnectionReallyActive = (type: 'ci' | 'cd', jobName: string, buildId: number) => {
    const connectionId = generateConnectionId(type, jobName, buildId);
    const eventSource = eventSourceMap.value.get(connectionId);
    const isStreaming = streamingStatus.value.get(connectionId);

    if (!eventSource || !isStreaming) {
      return false;
    }

    // 检查EventSource的状态
    if (eventSource.readyState === EventSource.CLOSED) {
      console.log(`${type}连接已关闭，清理状态`);
      closeConnectionPreservingCursor(connectionId);
      return false;
    }

    // 检查EventSource的状态是否为CONNECTING（连接中）或OPEN（已连接）
    if (eventSource.readyState === EventSource.CONNECTING) {
      console.log(`${type}连接正在建立中...`);
      return true; // 连接正在建立，认为是活跃的
    }

    if (eventSource.readyState === EventSource.OPEN) {
      console.log(`${type}连接状态正常`);
      return true;
    }

    // 如果状态不是CONNECTING或OPEN，认为连接已失效
    console.log(`${type}连接状态异常: ${eventSource.readyState}`);
    closeConnectionPreservingCursor(connectionId);
    return false;
  };

  // 日志对话框重新打开处理
  const handleLogDialogOpen = () => {
    if (sseAuthBlocked || !authStore.isAuthenticated || !authStore.can(PERMISSIONS.LOGS_READ)) {
      return;
    }
    logDialogPaused = false;
    console.log('日志对话框重新打开，从保留游标恢复当前标签页');
    // 用户主动重新打开代表新的、有界自动重连生命周期。
    automaticReconnects.clear();
    scheduledReconnects.clear();

    startConnectionCheck();

    // 恢复批量更新定时器（如果队列中有数据）
    if (updateQueue.value.length > 0 && !updateTimer.value) {
      console.log('恢复批量更新定时器，队列中有数据:', updateQueue.value.length);
      batchUpdateLogs();
    }

    if (currentLog.value.taskId && activeLogTab.value !== 'steps') {
      void fetchLogs(currentLog.value);
    }
  };

  // 重试获取日志
  const retryFetchLogs = async () => {
    if (sseAuthBlocked || !authStore.isAuthenticated || !authStore.can(PERMISSIONS.LOGS_READ)) {
      return;
    }
    if (currentLog.value) {
      console.log('重试获取日志:', currentLog.value);
      const type = activeLogTab.value === 'cd' ? 'cd' : 'ci';
      const jobName = type === 'ci' ? currentLog.value.ciJobName : currentLog.value.cdJobName;
      const buildId = type === 'ci' ? currentLog.value.ciBuildId : currentLog.value.cdBuildId;
      if (!jobName || !buildId) return;

      const connectionId = generateConnectionId(type, jobName, buildId);
      automaticReconnects.delete(connectionId);
      scheduledReconnects.delete(connectionId);
      completedConnections.delete(connectionId);
      closeConnectionPreservingCursor(connectionId);

      try {
        if (type === 'ci') {
          await fetchCiLogsStream(jobName, buildId);
        } else {
          await fetchCdLogsStream(jobName, buildId);
        }
      } catch (error) {
        console.error(`${type.toUpperCase()}日志手动重试失败:`, error);
        const message = type === 'ci' ? ciLog : cdLog;
        if (!message.value) message.value = `获取${type.toUpperCase()}日志失败，请稍后重试`;
      } finally {
        if (type === 'ci') ciLogLoading.value = false;
        else cdLogLoading.value = false;
      }
    }
  };

  // 获取当前连接状态
  const getCurrentStreamingStatus = (type: 'ci' | 'cd') => {
    if (!currentLog.value) return false;

    if (type === 'ci' && currentLog.value.ciJobName && currentLog.value.ciBuildId) {
      return (
        streamingStatus.value.get(
          generateConnectionId('ci', currentLog.value.ciJobName, currentLog.value.ciBuildId)
        ) || false
      );
    } else if (type === 'cd' && currentLog.value.cdJobName && currentLog.value.cdBuildId) {
      return (
        streamingStatus.value.get(
          generateConnectionId('cd', currentLog.value.cdJobName, currentLog.value.cdBuildId)
        ) || false
      );
    }
    return false;
  };

  // 获取当前活跃连接列表
  const getActiveConnections = () => {
    const connections: Array<{ id: string; type: string; jobName: string; buildId: number }> = [];
    eventSourceMap.value.forEach((_, connectionId) => {
      const parts = connectionId.split('_');
      if (parts.length >= 3) {
        const type = parts[0] as 'ci' | 'cd';
        const jobName = parts[1];
        const buildId = parseInt(parts[2]);
        connections.push({ id: connectionId, type, jobName, buildId });
      }
    });
    return connections;
  };

  // 批量更新相关
  const updateQueue = ref<{ type: 'ci' | 'cd'; data: string[] }[]>([]);
  const updateTimer = ref<ReturnType<typeof setTimeout> | null>(null);

  // 精确滚动控制
  const scrollToBottomIfActive = (type: 'ci' | 'cd', container: HTMLElement | undefined) => {
    // 只有在当前激活的标签页匹配时才滚动
    if (activeLogTab.value === type && container) {
      console.log(`执行滚动操作: ${type} 标签页，当前激活: ${activeLogTab.value}`);
      nextTick(() => {
        container.scrollTop = container.scrollHeight;
      });
    } else {
      console.log(`跳过滚动操作: ${type} 标签页，当前激活: ${activeLogTab.value}`);
    }
  };

  // 精确滚动到底部（用于手动按钮）
  const scrollToBottomPrecise = (type: 'ci' | 'cd') => {
    if (type === 'ci') {
      scrollToBottomIfActive('ci', ciLogContainer.value);
    } else if (type === 'cd') {
      scrollToBottomIfActive('cd', cdLogContainer.value);
    }
  };

  // 批量更新日志内容
  const batchUpdateLogs = () => {
    if (updateQueue.value.length === 0) return;

    console.log('开始批量更新日志，队列长度:', updateQueue.value.length);

    const ciUpdates: string[] = [];
    const cdUpdates: string[] = [];

    // 收集所有更新
    updateQueue.value.forEach(update => {
      if (update.type === 'ci') {
        ciUpdates.push(...update.data);
      } else {
        cdUpdates.push(...update.data);
      }
    });

    console.log('收集到的更新 - CI:', ciUpdates.length, '行, CD:', cdUpdates.length, '行');

    // 批量更新 - 分别处理，避免冲突
    if (ciUpdates.length > 0) {
      const beforeLength = ciLog.value.length;
      ciLog.value = appendBoundedLog(ciLog.value, ciUpdates);
      const afterLength = ciLog.value.length;
      console.log(`CI日志更新: ${beforeLength} -> ${afterLength} 字符`);
      // 使用精确滚动控制
      setTimeout(() => scrollToBottomIfActive('ci', ciLogContainer.value), 100);
    }

    if (cdUpdates.length > 0) {
      const beforeLength = cdLog.value.length;
      cdLog.value = appendBoundedLog(cdLog.value, cdUpdates);
      const afterLength = cdLog.value.length;
      console.log(`CD日志更新: ${beforeLength} -> ${afterLength} 字符`);
      // 使用精确滚动控制
      setTimeout(() => scrollToBottomIfActive('cd', cdLogContainer.value), 100);
    }

    // 清空队列
    updateQueue.value = [];
    console.log('批量更新完成，队列已清空');
  };

  // 添加更新到队列
  const addToUpdateQueue = (type: 'ci' | 'cd', data: string[]) => {
    console.log(`添加${type}日志到更新队列:`, data.length, '行');
    const pending = updateQueue.value
      .filter(update => update.type === type)
      .flatMap(update => update.data);
    const bounded = boundedLogLines(
      [...pending, ...data],
      LOG_QUEUE_MAX_BYTES,
      LOG_QUEUE_MAX_LINES
    );
    updateQueue.value = [
      ...updateQueue.value.filter(update => update.type !== type),
      { type, data: bounded },
    ];
    console.log('当前队列长度:', updateQueue.value.length);

    // 收到数据时立即隐藏loading
    if (type === 'ci' && ciLogLoading.value) {
      ciLogLoading.value = false;
    } else if (type === 'cd' && cdLogLoading.value) {
      cdLogLoading.value = false;
    }

    // 清除之前的定时器
    if (updateTimer.value) {
      clearTimeout(updateTimer.value);
      console.log('清除之前的更新定时器');
    }

    // 设置新的定时器，批量更新
    updateTimer.value = setTimeout(() => {
      console.log('执行批量更新定时器');
      batchUpdateLogs();
      updateTimer.value = null;
    }, LOG_BATCH_FLUSH_MS);
  };

  // 获取显示的日志内容（限制行数提升性能）
  const getDisplayLog = (logContent: string, maxLines: number = 1000) => {
    if (!logContent) return '';

    const lines = logContent.split('\n');
    if (lines.length <= maxLines) {
      return logContent;
    }

    // 只显示最后maxLines行
    const displayLines = lines.slice(-maxLines);
    return displayLines.join('\n');
  };

  // 性能监控
  const performanceMetrics = ref<{
    connectionTime: number;
    firstDataTime: number;
    totalDataCount: number;
    updateCount: number;
    cdDataCount: number; // CD日志独立计数器
    cdConnectionTime: number; // CD日志独立连接时间
    cdFirstDataTime: number; // CD日志独立首次数据时间
  }>({
    connectionTime: 0,
    firstDataTime: 0,
    totalDataCount: 0,
    updateCount: 0,
    cdDataCount: 0,
    cdConnectionTime: 0,
    cdFirstDataTime: 0,
  });

  // 重置性能指标
  const resetPerformanceMetrics = () => {
    performanceMetrics.value = {
      connectionTime: 0,
      firstDataTime: 0,
      totalDataCount: 0,
      updateCount: 0,
      cdDataCount: 0,
      cdConnectionTime: 0,
      cdFirstDataTime: 0,
    };
  };

  // 检查连接是否存在且活跃
  const isConnectionActive = (type: 'ci' | 'cd', jobName: string, buildId: number) => {
    const connectionId = generateConnectionId(type, jobName, buildId);
    const eventSource = eventSourceMap.value.get(connectionId);
    const isStreaming = streamingStatus.value.get(connectionId);
    if (!eventSource || !isStreaming || eventSource.readyState === EventSource.CLOSED) {
      if (eventSource?.readyState === EventSource.CLOSED) {
        closeConnectionPreservingCursor(connectionId, eventSource);
      }
      return false;
    }
    return true;
  };

  // 获取现有连接
  const getExistingConnection = (type: 'ci' | 'cd', jobName: string, buildId: number) => {
    const connectionId = generateConnectionId(type, jobName, buildId);
    return eventSourceMap.value.get(connectionId);
  };

  // 真正的清理函数（用于切换不同服务时）
  const cleanupLogsAndConnections = () => {
    console.log('清理所有日志和连接');
    // 先使旧回调失效，避免 close 后排队的事件或重试污染下一任务。
    logSessionGeneration += 1;
    // 清理SSE连接
    cleanupAllConnections();
    // 清空日志内容
    ciLog.value = '';
    cdLog.value = '';
    // 重置loading状态
    ciLogLoading.value = false;
    cdLogLoading.value = false;
    // 清空更新队列
    updateQueue.value = [];
    if (updateTimer.value) {
      clearTimeout(updateTimer.value);
      updateTimer.value = null;
    }
  };

  // 检查日志状态并恢复
  const checkAndRestoreLogState = (row: DeployingService) => {
    console.log('检查并恢复日志状态:', row);
    const type = activeLogTab.value === 'cd' ? 'cd' : activeLogTab.value === 'ci' ? 'ci' : null;
    if (!type) return true;

    const jobName = type === 'ci' ? row.ciJobName : row.cdJobName;
    const buildId = type === 'ci' ? row.ciBuildId : row.cdBuildId;
    if (!jobName || !buildId) return false;

    const connectionId = generateConnectionId(type, jobName, buildId);
    if (completedConnections.has(connectionId)) {
      if (type === 'ci') ciLogLoading.value = false;
      else cdLogLoading.value = false;
      return true;
    }

    // Existing text alone is not proof that a live stream is still attached.
    // A reopened dialog must reconnect from its saved cursor while retaining it.
    return isConnectionActive(type, jobName, buildId);
  };

  return {
    // 响应式数据
    logFilter,
    logList,
    currentPage,
    pageSize,
    total,
    logLoading,
    logDialogVisible,
    currentLog,
    activeLogTab,
    ciLog,
    cdLog,
    ciLogLoading,
    cdLogLoading,
    ciLogContainer,
    cdLogContainer,

    // 工具函数
    getStatusType,
    getDeployStatus,
    getEnvLabel,
    getEnvType,
    formatDateTime,
    scrollToBottom,
    manualScrollToBottom,
    scrollToBottomPrecise,

    // 事件处理函数
    handleSearch,
    handleResetLogFilter,
    viewLogDetail,
    handleSizeChange,
    handleCurrentChange,
    fetchLogs,
    handleLogDialogClose,

    // 清理函数
    cleanupAllConnections,
    retryFetchLogs,

    // 获取当前连接状态
    getCurrentStreamingStatus,

    // 获取当前活跃连接列表
    getActiveConnections,

    // 批量更新相关
    updateQueue,
    updateTimer,

    // 批量更新日志内容
    batchUpdateLogs,

    // 添加更新到队列
    addToUpdateQueue,

    // 获取显示的日志内容（限制行数提升性能）
    getDisplayLog,

    // 性能监控
    performanceMetrics,

    // 重置性能指标
    resetPerformanceMetrics,

    // 连接管理
    isConnectionActive,
    getExistingConnection,
    isConnectionReallyActive,

    // 真正的清理函数（用于切换不同服务时）
    cleanupLogsAndConnections,

    // 检查日志状态并恢复
    checkAndRestoreLogState,

    // 日志对话框重新打开处理
    handleLogDialogOpen,

    // 连接检查相关
    startConnectionCheck,
    stopConnectionCheck,
  };
}
