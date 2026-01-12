import { ref, reactive, watch, nextTick } from 'vue';
import { ElMessage } from 'element-plus';
import { queryPublishLogs, queryTaskLogs } from '@/services/deploy';
import api from '@/config/api';
import type { LogItem, DeployingService, LogFilter } from '@/types/deploy';

export function useLog() {
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
  // SSE断线续传 offset（服务端会在每条消息带 id，浏览器会自动用 Last-Event-ID 重连；
  // 这里额外缓存 last offset，用于“手动重建连接/重试”时可带 start 提升体验）
  const lastOffsetMap = ref<Map<string, number>>(new Map());

  // 生成连接ID
  const generateConnectionId = (type: 'ci' | 'cd', jobName: string, buildId: number) => {
    return `${type}_${jobName}_${buildId}`;
  };

  // 清理特定连接
  const cleanupConnection = (connectionId: string) => {
    const eventSource = eventSourceMap.value.get(connectionId);
    if (eventSource) {
      eventSource.close();
      eventSourceMap.value.delete(connectionId);
      streamingStatus.value.delete(connectionId);
      // 不删除 lastOffsetMap：用于“手动重建连接/重试”时携带 start=offset 增强续传体验
      console.log(`清理SSE连接: ${connectionId}`);
    }
  };

  // 清理所有连接
  const cleanupAllConnections = () => {
    eventSourceMap.value.forEach((eventSource, connectionId) => {
      eventSource.close();
      console.log(`清理SSE连接: ${connectionId}`);
    });
    eventSourceMap.value.clear();
    streamingStatus.value.clear();
    lastOffsetMap.value.clear();
  };

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
    };
    return statusMap[status] || status;
  };

  const getEnvLabel = (env: string): string => {
    const envMap: Record<string, string> = {
      dev: '开发环境',
      test: '测试环境',
      moni: '模拟环境',
    };
    return envMap[env] || env;
  };

  const getEnvType = (env: string): string => {
    const envMap: Record<string, string> = {
      dev: 'info',
      test: 'warning',
      moni: 'success',
    };
    return envMap[env] || 'info';
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
            message: item.message === 'NULL' ? '' : item.message,
            auto_deploy: item.auto_deploy,
            ci_job_name: item.ci_job_name === 'NULL' ? '' : item.ci_job_name,
            cd_job_name: item.cd_job_name === 'NULL' ? '' : item.cd_job_name,
            ci_build_id: item.ci_build_id || 0,
            cd_build_id: item.cd_build_id || 0,
            products: item.products === 'NULL' ? '' : item.products,
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
  const fetchCiLogsStream = async (jobName: string, buildId: number, retryCount = 0) => {
    // 说明：
    // - 这里只做“标准 SSE 消费者”：message/ping/end/error
    // - 断线重连/Last-Event-ID 由浏览器 EventSource 自动完成
    // - 服务端会在每条消息带 id，前端仅缓存 lastEventId 以便“手动重建连接”时带 start
    // - 不再做自定义心跳/超时/递归重试（这些容易误判“暂时没新日志”为异常）
    void retryCount;

    return new Promise<void>((resolve, reject) => {
      // 检查是否已经存在相同的活跃连接
      if (isConnectionActive('ci', jobName, buildId)) {
        console.log(`复用现有CI日志SSE连接: ${jobName}_${buildId}`);
        resolve();
        return;
      }

      // 先清理之前的CI连接
      cleanupConnection(generateConnectionId('ci', jobName, buildId));

      const connectionId = generateConnectionId('ci', jobName, buildId);
      const start = lastOffsetMap.value.get(connectionId);
      const url =
        `/api/v1/job/stream/log?job_name=${encodeURIComponent(jobName)}&build_id=${buildId}` +
        (typeof start === 'number' && !Number.isNaN(start) ? `&start=${start}` : '');

      console.log(`开始获取CI日志 (重试次数: ${retryCount}):`, { jobName, buildId, url });

      const eventSource = new EventSource(url, { withCredentials: false });
      eventSourceMap.value.set(connectionId, eventSource);
      streamingStatus.value.set(connectionId, false);

      let isResolved = false;

      const resolveOnce = (value?: any) => {
        if (!isResolved) {
          isResolved = true;
          resolve(value);
        }
      };

      const rejectOnce = (error: any) => {
        if (!isResolved) {
          isResolved = true;
          reject(error);
        }
      };

      eventSource.onmessage = (event: MessageEvent) => {
        try {
          if (event.lastEventId) {
            const offset = Number(event.lastEventId);
            if (!Number.isNaN(offset)) {
              lastOffsetMap.value.set(connectionId, offset);
            }
          }

          // 解析JSON数据（只关心 result: string[]）
          const data = JSON.parse(event.data);
          if (data.code === 1 && data.result && Array.isArray(data.result)) {
            // 空数组直接忽略（后端心跳通过 event: ping 推送）
            if (data.result.length === 0) return;

            // 首次内容直接渲染；后续走批量队列
            if (!ciLog.value) {
              ciLog.value = data.result.join('\n') + '\n';
              ciLogLoading.value = false;
              // 立即滚动到底部
              setTimeout(() => scrollToBottomIfActive('ci', ciLogContainer.value), 100);
            } else {
              addToUpdateQueue('ci', data.result);
            }
          } else if (data.code === 0) {
            const msg = data.message || data.msg || data.error || '获取 CI 日志失败';
            if (!ciLog.value) ciLog.value = `获取CI日志失败: ${msg}`;
            // 服务端返回业务错误：关闭连接
            cleanupConnection(connectionId);
            rejectOnce(new Error(msg));
          }
        } catch (error) {
          console.error('解析CI日志SSE数据失败:', error);
          if (!ciLog.value) {
            ciLog.value = '解析CI日志数据失败';
          }
          cleanupConnection(connectionId);
          rejectOnce(error);
        }
      };

      // 心跳：event: ping（无需处理）
      eventSource.addEventListener('ping', () => {
        // ignore
      });

      eventSource.onopen = () => {
        streamingStatus.value.set(connectionId, true);
        ciLogLoading.value = false;
        resolveOnce();
      };

      // 监听日志流结束事件（event: end）
      eventSource.addEventListener('end', () => {
        if (updateTimer.value) {
          clearTimeout(updateTimer.value);
          updateTimer.value = null;
        }
        batchUpdateLogs();
        cleanupConnection(connectionId);
      });

      // 监听错误事件（event: error）- 这是服务端主动推送的错误事件
      eventSource.addEventListener('error', (event: Event) => {
        // 注意：EventSource 原生 error 事件既可能是网络错误(Event)，也可能是服务端自定义 event:error(MessageEvent)
        if (event instanceof MessageEvent && event.data) {
          try {
            const errorData = JSON.parse(event.data);
            const msg = errorData.error || errorData.message || '未知错误';
            if (!ciLog.value) ciLog.value = `获取CI日志失败: ${msg}`;
            cleanupConnection(connectionId);
            rejectOnce(new Error(msg));
          } catch {
            if (!ciLog.value) ciLog.value = '获取CI日志失败: 解析错误事件失败';
            cleanupConnection(connectionId);
            rejectOnce(new Error('解析错误事件失败'));
          }
        } else {
          // 网络错误：不主动 close，交给浏览器自动重连
          streamingStatus.value.set(connectionId, false);
          ciLogLoading.value = false;
          resolveOnce();
        }
      });
    });
  };

  // 使用SSE获取CD日志
  const fetchCdLogsStream = async (jobName: string, buildId: number, retryCount = 0) => {
    void retryCount;

    return new Promise<void>((resolve, reject) => {
      // 检查是否已经存在相同的活跃连接
      if (isConnectionActive('cd', jobName, buildId)) {
        console.log(`复用现有CD日志SSE连接: ${jobName}_${buildId}`);
        resolve();
        return;
      }

      // 先清理之前的CD连接
      cleanupConnection(generateConnectionId('cd', jobName, buildId));

      const connectionId = generateConnectionId('cd', jobName, buildId);
      const start = lastOffsetMap.value.get(connectionId);
      const url =
        `/api/v1/job/stream/log?job_name=${encodeURIComponent(jobName)}&build_id=${buildId}` +
        (typeof start === 'number' && !Number.isNaN(start) ? `&start=${start}` : '');

      console.log(`开始获取CD日志 (重试次数: ${retryCount}):`, { jobName, buildId, url });

      const eventSource = new EventSource(url, { withCredentials: false });
      eventSourceMap.value.set(connectionId, eventSource);
      streamingStatus.value.set(connectionId, false);

      let isResolved = false;

      const resolveOnce = (value?: any) => {
        if (!isResolved) {
          isResolved = true;
          resolve(value);
        }
      };

      const rejectOnce = (error: any) => {
        if (!isResolved) {
          isResolved = true;
          reject(error);
        }
      };

      eventSource.onmessage = (event: MessageEvent) => {
        try {
          if (event.lastEventId) {
            const offset = Number(event.lastEventId);
            if (!Number.isNaN(offset)) {
              lastOffsetMap.value.set(connectionId, offset);
            }
          }

          // 解析JSON数据（只关心 result: string[]）
          const data = JSON.parse(event.data);

          if (data.code === 1 && data.result && Array.isArray(data.result)) {
            // 空数组直接忽略（后端心跳通过 event: ping 推送）
            if (data.result.length === 0) return;

            if (!cdLog.value) {
              cdLog.value = data.result.join('\n') + '\n';
              cdLogLoading.value = false;
              // 立即滚动到底部
              setTimeout(() => scrollToBottomIfActive('cd', cdLogContainer.value), 100);
            } else {
              addToUpdateQueue('cd', data.result);
            }
          } else if (data.code === 0) {
            const msg = data.message || data.msg || data.error || '获取 CD 日志失败';
            if (!cdLog.value) cdLog.value = `获取CD日志失败: ${msg}`;
            cleanupConnection(connectionId);
            rejectOnce(new Error(msg));
          } else {
            console.log('CD日志SSE收到未知数据格式:', data);
          }
        } catch (error) {
          console.error('解析CD日志SSE数据失败:', error);
          if (!cdLog.value) {
            cdLog.value = '解析CD日志数据失败';
          }
          cleanupConnection(connectionId);
          rejectOnce(error);
        }
      };

      // 心跳：event: ping（无需处理）
      eventSource.addEventListener('ping', () => {
        // ignore
      });

      eventSource.onopen = () => {
        streamingStatus.value.set(connectionId, true);
        cdLogLoading.value = false;
        resolveOnce();
      };

      // 监听日志流结束事件（event: end）
      eventSource.addEventListener('end', () => {
        if (updateTimer.value) {
          clearTimeout(updateTimer.value);
          updateTimer.value = null;
        }
        batchUpdateLogs();
        cleanupConnection(connectionId);
      });

      // 监听错误事件（event: error）
      eventSource.addEventListener('error', (event: Event) => {
        if (event instanceof MessageEvent && event.data) {
          try {
            const errorData = JSON.parse(event.data);
            const msg = errorData.error || errorData.message || '未知错误';
            if (!cdLog.value) cdLog.value = `获取CD日志失败: ${msg}`;
            cleanupConnection(connectionId);
            rejectOnce(new Error(msg));
          } catch {
            if (!cdLog.value) cdLog.value = '获取CD日志失败: 解析错误事件失败';
            cleanupConnection(connectionId);
            rejectOnce(new Error('解析错误事件失败'));
          }
        } else {
          // 网络错误：不主动 close，交给浏览器自动重连
          streamingStatus.value.set(connectionId, false);
          cdLogLoading.value = false;
          resolveOnce();
        }
      });
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

  // 开始定期检查连接状态
  const startConnectionCheck = () => {
    if (connectionCheckTimer.value) {
      clearInterval(connectionCheckTimer.value);
    }

    connectionCheckTimer.value = setInterval(() => {
      if (logDialogVisible.value && currentLog.value) {
        // 检查CI连接
        if (currentLog.value.ciJobName && currentLog.value.ciBuildId) {
          const ciConnectionId = generateConnectionId(
            'ci',
            currentLog.value.ciJobName,
            currentLog.value.ciBuildId
          );
          const ciConnection = eventSourceMap.value.get(ciConnectionId);

          // 只有在连接完全不存在或已关闭时才重新建立
          if (!ciConnection || ciConnection.readyState === EventSource.CLOSED) {
            console.log('定期检查发现CI连接已关闭，尝试重新建立');
            fetchCiLogsStream(currentLog.value.ciJobName, currentLog.value.ciBuildId);
          }
        }

        // 检查CD连接
        if (currentLog.value.cdJobName && currentLog.value.cdBuildId) {
          const cdConnectionId = generateConnectionId(
            'cd',
            currentLog.value.cdJobName,
            currentLog.value.cdBuildId
          );
          const cdConnection = eventSourceMap.value.get(cdConnectionId);

          // 只有在连接完全不存在或已关闭时才重新建立
          if (!cdConnection || cdConnection.readyState === EventSource.CLOSED) {
            console.log('定期检查发现CD连接已关闭，尝试重新建立');
            fetchCdLogsStream(currentLog.value.cdJobName, currentLog.value.cdBuildId);
          }
        }
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
    // 标准做法：关闭连接避免泄漏（浏览器会在重连时自动用 Last-Event-ID 续传）
    if (updateTimer.value) {
      clearTimeout(updateTimer.value);
      updateTimer.value = null;
    }
    batchUpdateLogs();
    cleanupAllConnections();
    ciLogLoading.value = false;
    cdLogLoading.value = false;
    stopConnectionCheck();
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
      cleanupConnection(connectionId);
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
    cleanupConnection(connectionId);
    return false;
  };

  // 日志对话框重新打开处理
  const handleLogDialogOpen = () => {
    console.log('日志对话框重新打开，检查并恢复连接状态');

    // 检查当前服务的连接状态
    if (currentLog.value) {
      // 检查CI连接
      if (currentLog.value.ciJobName && currentLog.value.ciBuildId) {
        const ciConnectionId = generateConnectionId(
          'ci',
          currentLog.value.ciJobName,
          currentLog.value.ciBuildId
        );
        const ciEventSource = eventSourceMap.value.get(ciConnectionId);
        const ciIsStreaming = streamingStatus.value.get(ciConnectionId);

        if (
          ciEventSource &&
          ciIsStreaming &&
          isConnectionReallyActive('ci', currentLog.value.ciJobName, currentLog.value.ciBuildId)
        ) {
          console.log('CI连接仍然活跃，恢复状态');
          // 连接仍然存在且活跃，恢复状态
          // 确保streamingStatus保持为true
          streamingStatus.value.set(ciConnectionId, true);
        } else {
          console.log('CI连接需要重新建立');
          // 连接不存在或已失效，需要重新建立
          cleanupConnection(ciConnectionId);
          if (activeLogTab.value === 'ci' && !ciLog.value) {
            fetchCiLogsStream(currentLog.value.ciJobName, currentLog.value.ciBuildId);
          }
        }
      }

      // 检查CD连接
      if (currentLog.value.cdJobName && currentLog.value.cdBuildId) {
        const cdConnectionId = generateConnectionId(
          'cd',
          currentLog.value.cdJobName,
          currentLog.value.cdBuildId
        );
        const cdEventSource = eventSourceMap.value.get(cdConnectionId);
        const cdIsStreaming = streamingStatus.value.get(cdConnectionId);

        if (
          cdEventSource &&
          cdIsStreaming &&
          isConnectionReallyActive('cd', currentLog.value.cdJobName, currentLog.value.cdBuildId)
        ) {
          console.log('CD连接仍然活跃，恢复状态');
          // 连接仍然存在且活跃，恢复状态
          // 确保streamingStatus保持为true
          streamingStatus.value.set(cdConnectionId, true);
        } else {
          console.log('CD连接需要重新建立');
          // 连接不存在或已失效，需要重新建立
          cleanupConnection(cdConnectionId);
          if (activeLogTab.value === 'cd' && !cdLog.value) {
            fetchCdLogsStream(currentLog.value.cdJobName, currentLog.value.cdBuildId);
          }
        }
      }
    }

    console.log('日志对话框重新打开完成');

    // 启动定期检查连接状态
    startConnectionCheck();

    // 恢复批量更新定时器（如果队列中有数据）
    if (updateQueue.value.length > 0 && !updateTimer.value) {
      console.log('恢复批量更新定时器，队列中有数据:', updateQueue.value.length);
      batchUpdateLogs();
    }
  };

  // 重试获取日志
  const retryFetchLogs = async () => {
    if (currentLog.value) {
      console.log('重试获取日志:', currentLog.value);
      await fetchLogs(currentLog.value);
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
      ciLog.value += ciUpdates.join('\n') + '\n';
      const afterLength = ciLog.value.length;
      console.log(`CI日志更新: ${beforeLength} -> ${afterLength} 字符`);
      // 使用精确滚动控制
      setTimeout(() => scrollToBottomIfActive('ci', ciLogContainer.value), 100);
    }

    if (cdUpdates.length > 0) {
      const beforeLength = cdLog.value.length;
      cdLog.value += cdUpdates.join('\n') + '\n';
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
    updateQueue.value.push({ type, data });
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
    return eventSource && isStreaming;
  };

  // 获取现有连接
  const getExistingConnection = (type: 'ci' | 'cd', jobName: string, buildId: number) => {
    const connectionId = generateConnectionId(type, jobName, buildId);
    return eventSourceMap.value.get(connectionId);
  };

  // 真正的清理函数（用于切换不同服务时）
  const cleanupLogsAndConnections = () => {
    console.log('清理所有日志和连接');
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

    // 检查CI日志状态
    if (row.ciJobName && row.ciBuildId) {
      const ciConnectionId = generateConnectionId('ci', row.ciJobName, row.ciBuildId);
      const isCiActive = streamingStatus.value.get(ciConnectionId);

      if (isCiActive && ciLog.value) {
        console.log('CI日志连接活跃且有内容，跳过重新获取');
        ciLogLoading.value = false;
        // 如果当前是CI标签页，直接返回true
        if (activeLogTab.value === 'ci') {
          return true;
        }
      }
    }

    // 检查CD日志状态
    if (row.cdJobName && row.cdBuildId) {
      const cdConnectionId = generateConnectionId('cd', row.cdJobName, row.cdBuildId);
      const isCdActive = streamingStatus.value.get(cdConnectionId);

      if (isCdActive && cdLog.value) {
        console.log('CD日志连接活跃且有内容，跳过重新获取');
        cdLogLoading.value = false;
        // 如果当前是CD标签页，直接返回true
        if (activeLogTab.value === 'cd') {
          return true;
        }
      }
    }

    // 如果当前标签页对应的日志已经准备好，返回true
    if (activeLogTab.value === 'ci' && ciLog.value && !ciLogLoading.value) {
      return true;
    }
    if (activeLogTab.value === 'cd' && cdLog.value && !cdLogLoading.value) {
      return true;
    }

    return false;
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
