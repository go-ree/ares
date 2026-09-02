import { ref, reactive, watch, nextTick } from 'vue';
import { ElMessage } from 'element-plus';
import { queryPublishLogs, queryTaskLogs } from '@/services/deploy';
import api from '@/config/api';
import type { LogItem, DeployingService, LogFilter } from '@/types/deploy';
import { normalizeLegacyNullableText } from '@/utils/legacy-nullable-text';

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
      lastOffsetMap.value.delete(connectionId);
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
    const maxRetries = 5; // 增加重试次数，提高连接稳定性

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
      streamingStatus.value.set(connectionId, true);

      let hasReceivedData = false;
      let isResolved = false;
      let messageCount = 0; // 添加消息计数器
      let is404Error = false; // 添加404错误标志
      let handledServerErrorEvent = false; // event: error（服务端推送）已处理标志，避免与 onerror 重复处理

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
          console.log('CI日志SSE收到数据:', event.data);
          hasReceivedData = true;
          if (event.lastEventId) {
            const offset = Number(event.lastEventId);
            if (!Number.isNaN(offset)) {
              lastOffsetMap.value.set(connectionId, offset);
            }
          }

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
            // 空数组直接忽略（后端心跳通过 event: ping 推送）
            if (data.result.length === 0) return;

            // 如果是首次收到数据，立即显示历史日志
            if (performanceMetrics.value.totalDataCount === 1) {
              console.log('首次收到CI日志数据，立即显示历史日志:', data.result.length, '行');
              ciLog.value = data.result.join('\n') + '\n';
              // 首次数据到达，立即关闭 loading，避免一直转圈
              if (ciLogLoading.value) {
                ciLogLoading.value = false;
              }
              // 立即滚动到底部
              setTimeout(() => scrollToBottomIfActive('ci', ciLogContainer.value), 100);
            } else {
              // 后续数据使用批量更新，提升性能
              addToUpdateQueue('ci', data.result);
            }
            performanceMetrics.value.updateCount++;
          } else if (data.code === 0) {
            // 处理错误，但不清理连接
            console.error('CI日志SSE错误:', data.message || data.error);
            if (!ciLog.value) {
              ciLog.value = '获取CI日志失败: ' + (data.message || data.error);
            }
            rejectOnce(new Error(data.message || data.error || '获取 CI 日志失败'));
          }
        } catch (error) {
          console.error('解析CI日志SSE数据失败:', error);
          if (!ciLog.value) {
            ciLog.value = '解析CI日志数据失败';
          }
          rejectOnce(error);
        }
      };

      // 心跳：event: ping（无需处理）
      eventSource.addEventListener('ping', () => {
        // ignore
      });

      eventSource.onerror = error => {
        // 如果已收到服务端 error 事件并处理，避免重复走网络错误逻辑
        if (handledServerErrorEvent) return;
        console.error('CI日志SSE连接错误:', error);
        console.log(
          'CI日志SSE连接错误详情 - hasReceivedData:',
          hasReceivedData,
          'retryCount:',
          retryCount,
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
              clearTimeout(timeout);
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
          clearTimeout(timeout);
          cleanupCiHeartbeat();
          return;
        }

        // 如果连接已经关闭且收到过数据，认为日志已经完成，不再重试
        if (eventSource.readyState === EventSource.CLOSED && hasReceivedData) {
          console.log('CI日志SSE连接已关闭且收到过数据，认为日志已完成');
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          clearTimeout(timeout);
          resolveOnce();
          return;
        }

        if (retryCount < maxRetries) {
          // 如果未超过重试次数，尝试重试（无论是否收到过数据）
          console.log(`CI日志SSE连接失败，${retryCount + 1}秒后重试...`);
          // 清理当前连接
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);

          setTimeout(
            () => {
              // 强制清理现有连接，确保重试能成功
              cleanupConnection(connectionId);
              fetchCiLogsStream(jobName, buildId, retryCount + 1)
                .then(resolveOnce)
                .catch(rejectOnce);
            },
            3000 * (retryCount + 1)
          ); // 增加重试延迟，给服务器更多时间恢复
        } else {
          // 超过重试次数，清理连接
          console.log('CI日志SSE连接失败，已超过重试次数');
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          clearTimeout(timeout);
          rejectOnce(new Error('CI日志SSE连接失败，已重试' + maxRetries + '次'));
        }
      };

      eventSource.onopen = () => {
        console.log('=== CI日志SSE连接已建立 ===');
        console.log('连接ID:', connectionId);
        console.log('URL:', url);
        performanceMetrics.value.connectionTime = Date.now();
        console.log('SSE连接建立时间:', new Date().toISOString());
        // 确保连接状态为活跃
        streamingStatus.value.set(connectionId, true);
        console.log('CI连接状态已设置为活跃');
        // 连接已建立就结束“获取中”的 loading，让界面至少显示“实时获取中/等待输出”
        // 注意：真正的日志追加仍依赖服务端正确 flush SSE event（以 \n\n 结束事件）
        if (ciLogLoading.value) {
          ciLogLoading.value = false;
        }
      };

      // 设置超时处理
      const timeout = setTimeout(() => {
        console.log(
          'CI日志SSE超时，hasReceivedData:',
          hasReceivedData,
          'retryCount:',
          retryCount,
          'readyState:',
          eventSource.readyState
        );

        // 如果连接已经关闭且收到过数据，认为日志已经完成，不再重试
        if (eventSource.readyState === EventSource.CLOSED && hasReceivedData) {
          console.log('CI日志SSE超时但连接已关闭且收到过数据，认为日志已完成');
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          resolveOnce();
          return;
        }

        if (retryCount < maxRetries) {
          // 超时且未超过重试次数，尝试重试（无论是否收到过数据）
          console.log(`CI日志SSE超时，${retryCount + 1}秒后重试...`);
          // 清理当前连接
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          cleanupCiHeartbeat();

          setTimeout(
            () => {
              // 强制清理现有连接，确保重试能成功
              cleanupConnection(connectionId);
              fetchCiLogsStream(jobName, buildId, retryCount + 1)
                .then(resolveOnce)
                .catch(rejectOnce);
            },
            3000 * (retryCount + 1)
          ); // 增加重试延迟，给服务器更多时间恢复
        } else {
          // 超过重试次数，清理连接
          console.log('CI日志SSE超时，已超过重试次数');
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          clearTimeout(timeout);
          cleanupCiHeartbeat();
          rejectOnce(new Error('CI日志获取超时，已重试' + maxRetries + '次'));
        }
      }, 60000); // 增加到60秒超时，给CI日志更多时间

      // 监听日志流结束事件（event: end）
      eventSource.addEventListener('end', (event: MessageEvent) => {
        console.log('收到SSE end事件，日志流结束');
        clearTimeout(timeout);
        cleanupCiHeartbeat();
        if (updateTimer.value) {
          clearTimeout(updateTimer.value);
          updateTimer.value = null;
        }
        batchUpdateLogs();
        eventSource.close();
        eventSourceMap.value.delete(connectionId);
        streamingStatus.value.set(connectionId, false);
        resolveOnce();
      });

      // 监听错误事件（event: error）- 这是服务端主动推送的错误事件
      eventSource.addEventListener('error', (event: MessageEvent) => {
        console.log('收到SSE error事件，处理错误');
        handledServerErrorEvent = true;
        try {
          const errorData = JSON.parse(event.data);
          console.error('CI日志SSE错误事件:', errorData);

          // 显示错误信息
          if (!ciLog.value) {
            ciLog.value = `获取CI日志失败: ${errorData.error || errorData.message || '未知错误'}`;
          }

          // 清理资源
          clearTimeout(timeout);
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
          if (errorData.error === '404') {
            console.log('CI日志SSE 404错误，任务可能不存在，不再重试');
            is404Error = true; // 设置404错误标志
            if (!ciLog.value) {
              ciLog.value = '未找到日志信息';
            }
            rejectOnce(new Error(`任务不存在: ${errorData.error}`));
          } else {
            console.log('CI日志SSE其他错误，尝试重试');
            if (retryCount < maxRetries) {
              setTimeout(
                () => {
                  cleanupConnection(connectionId);
                  fetchCiLogsStream(jobName, buildId, retryCount + 1)
                    .then(resolveOnce)
                    .catch(rejectOnce);
                },
                3000 * (retryCount + 1)
              );
            } else {
              rejectOnce(
                new Error(`CI日志获取失败: ${errorData.error || errorData.message || '未知错误'}`)
              );
            }
          }
        } catch (parseError) {
          console.error('解析CI日志SSE错误事件失败:', parseError);
          // 如果解析失败，按普通错误处理
          clearTimeout(timeout);
          cleanupCiHeartbeat();
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          rejectOnce(new Error('解析错误事件失败'));
        }
      });

      // 监听连接关闭事件
      eventSource.addEventListener('close', event => {
        console.log('CI日志SSE连接关闭事件触发');
        clearTimeout(timeout);
        cleanupCiHeartbeat();
        eventSourceMap.value.delete(connectionId);
        streamingStatus.value.set(connectionId, false);
        // 如果收到过数据，认为日志已完成
        if (hasReceivedData) {
          console.log('CI日志SSE连接关闭且收到过数据，认为日志已完成');
          resolveOnce();
        }
      });

      // 添加心跳检测机制，在日志流暂停期间保持连接
      let ciHeartbeatCount = 0;
      const ciHeartbeatInterval = setInterval(() => {
        ciHeartbeatCount++;
        console.log(`CI日志SSE心跳检测 #${ciHeartbeatCount} - 连接状态: ${eventSource.readyState}`);

        // 如果连接已关闭，清理心跳定时器
        if (eventSource.readyState === EventSource.CLOSED) {
          console.log('CI日志SSE连接已关闭，停止心跳检测');
          clearInterval(ciHeartbeatInterval);
          return;
        }

        // 如果长时间没有收到数据，可能是连接问题
        if (ciHeartbeatCount > 60) {
          // 60秒没有数据
          console.log('CI日志SSE心跳检测超时，连接可能有问题');
          clearInterval(ciHeartbeatInterval);
          // 不主动关闭连接，让错误处理逻辑处理
        }
      }, 1000); // 每秒检测一次

      // 在连接关闭时清理心跳定时器
      const cleanupCiHeartbeat = () => {
        clearInterval(ciHeartbeatInterval);
      };
    });
  };

  // 使用SSE获取CD日志
  const fetchCdLogsStream = async (jobName: string, buildId: number, retryCount = 0) => {
    const maxRetries = 5; // 增加重试次数，提高CD日志连接稳定性

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
      streamingStatus.value.set(connectionId, true);

      let hasReceivedData = false;
      let isResolved = false;
      let messageCount = 0; // 添加消息计数器
      let is404Error = false; // 添加404错误标志
      let handledServerErrorEvent = false; // event: error（服务端推送）已处理标志

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
          messageCount++;
          console.log(`CD日志SSE收到第${messageCount}条数据:`, event.data);
          hasReceivedData = true;
          if (event.lastEventId) {
            const offset = Number(event.lastEventId);
            if (!Number.isNaN(offset)) {
              lastOffsetMap.value.set(connectionId, offset);
            }
          }

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
          console.log('CD日志解析结果:', data);

          if (data.code === 1 && data.result && Array.isArray(data.result)) {
            // 空数组直接忽略（后端心跳通过 event: ping 推送）
            if (data.result.length === 0) return;

            // 如果是首次收到数据，立即显示历史日志
            if (performanceMetrics.value.cdDataCount === 1) {
              console.log('首次收到CD日志数据，立即显示历史日志:', data.result.length, '行');
              cdLog.value = data.result.join('\n') + '\n';
              // 首次数据到达，立即关闭 loading，避免一直转圈
              if (cdLogLoading.value) {
                cdLogLoading.value = false;
              }
              // 立即滚动到底部
              setTimeout(() => scrollToBottomIfActive('cd', cdLogContainer.value), 100);
            } else {
              // 后续数据使用批量更新，提升性能
              console.log(`CD日志后续数据，添加到更新队列: ${data.result.length} 行`);
              addToUpdateQueue('cd', data.result);
            }
            performanceMetrics.value.updateCount++;
          } else if (data.code === 0) {
            // 处理错误，但不清理连接
            console.error('CD日志SSE错误:', data.message || data.error);
            if (!cdLog.value) {
              cdLog.value = '获取CD日志失败: ' + (data.message || data.error);
            }
            rejectOnce(new Error(data.message || data.error || '获取 CD 日志失败'));
          } else {
            console.log('CD日志SSE收到未知数据格式:', data);
          }
        } catch (error) {
          console.error('解析CD日志SSE数据失败:', error);
          if (!cdLog.value) {
            cdLog.value = '解析CD日志数据失败';
          }
          rejectOnce(error);
        }
      };

      // 心跳：event: ping（无需处理）
      eventSource.addEventListener('ping', () => {
        // ignore
      });

      eventSource.onerror = error => {
        if (handledServerErrorEvent) return;
        console.error('CD日志SSE连接错误:', error);
        console.log(
          'CD日志SSE连接错误详情 - hasReceivedData:',
          hasReceivedData,
          'retryCount:',
          retryCount,
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
              clearTimeout(timeout);
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
          clearTimeout(timeout);
          cleanupHeartbeat();
          return;
        }

        // 如果连接已经关闭且收到过数据，认为日志已经完成，不再重试
        if (eventSource.readyState === EventSource.CLOSED && hasReceivedData) {
          console.log('CD日志SSE连接已关闭且收到过数据，认为日志已完成');
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          clearTimeout(timeout);
          cleanupHeartbeat();
          resolveOnce();
          return;
        }

        // 如果收到过数据但连接还在，可能是日志流暂停，不要立即重试
        if (hasReceivedData && eventSource.readyState !== EventSource.CLOSED) {
          console.log(
            'CD日志SSE连接错误但已收到数据且连接未关闭，可能是日志流暂停，等待更多数据...'
          );
          return;
        }

        if (retryCount < maxRetries) {
          // 如果未超过重试次数，尝试重试（无论是否收到过数据）
          console.log(`CD日志SSE连接失败，${retryCount + 1}秒后重试...`);
          // 清理当前连接
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);

          // 强制清理超时定时器
          clearTimeout(timeout);
          cleanupHeartbeat();

          setTimeout(
            () => {
              // 强制清理现有连接，确保重试能成功
              cleanupConnection(connectionId);
              fetchCdLogsStream(jobName, buildId, retryCount + 1)
                .then(resolveOnce)
                .catch(rejectOnce);
            },
            3000 * (retryCount + 1)
          ); // 增加重试延迟，给服务器更多时间恢复
        } else {
          // 超过重试次数，清理连接
          console.log('CD日志SSE连接失败，已超过重试次数');
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          clearTimeout(timeout);
          cleanupHeartbeat();
          rejectOnce(new Error('CD日志SSE连接失败，已重试' + maxRetries + '次'));
        }
      };

      eventSource.onopen = () => {
        console.log('=== CD日志SSE连接已建立 ===');
        console.log('连接ID:', connectionId);
        console.log('URL:', url);
        performanceMetrics.value.cdConnectionTime = Date.now();
        console.log('CD日志SSE连接建立时间:', new Date().toISOString());
        // 确保连接状态为活跃
        streamingStatus.value.set(connectionId, true);
        console.log('CD连接状态已设置为活跃');
        // 连接已建立就结束“获取中”的 loading，让界面至少显示“实时获取中/等待输出”
        if (cdLogLoading.value) {
          cdLogLoading.value = false;
        }
      };

      // 设置超时处理
      const timeout = setTimeout(() => {
        console.log(
          'CD日志SSE超时，hasReceivedData:',
          hasReceivedData,
          'retryCount:',
          retryCount,
          'readyState:',
          eventSource.readyState
        );

        // 如果连接已经关闭且收到过数据，认为日志已经完成，不再重试
        if (eventSource.readyState === EventSource.CLOSED && hasReceivedData) {
          console.log('CD日志SSE超时但连接已关闭且收到过数据，认为日志已完成');
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          clearTimeout(timeout);
          cleanupHeartbeat();
          resolveOnce();
          return;
        }

        if (retryCount < maxRetries) {
          // 超时且未超过重试次数，尝试重试（无论是否收到过数据）
          console.log(`CD日志SSE超时，${retryCount + 1}秒后重试...`);
          // 清理当前连接
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          cleanupHeartbeat();

          setTimeout(
            () => {
              // 强制清理现有连接，确保重试能成功
              cleanupConnection(connectionId);
              fetchCdLogsStream(jobName, buildId, retryCount + 1)
                .then(resolveOnce)
                .catch(rejectOnce);
            },
            3000 * (retryCount + 1)
          ); // 增加重试延迟，给服务器更多时间恢复
        } else {
          // 超过重试次数，清理连接
          console.log('CD日志SSE超时，已超过重试次数');
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          clearTimeout(timeout);
          cleanupHeartbeat();
          rejectOnce(new Error('CD日志获取超时，已重试' + maxRetries + '次'));
        }
      }, 120000); // 增加到120秒超时，给CD日志更多时间处理暂停期

      // 监听日志流结束事件（event: end）
      eventSource.addEventListener('end', (event: MessageEvent) => {
        console.log('收到SSE end事件，日志流结束');
        clearTimeout(timeout);
        cleanupHeartbeat();
        if (updateTimer.value) {
          clearTimeout(updateTimer.value);
          updateTimer.value = null;
        }
        batchUpdateLogs();
        eventSource.close();
        eventSourceMap.value.delete(connectionId);
        streamingStatus.value.set(connectionId, false);
        resolveOnce();
      });

      // 监听错误事件（event: error）
      eventSource.addEventListener('error', (event: MessageEvent) => {
        console.log('收到SSE error事件，处理错误');
        handledServerErrorEvent = true;
        try {
          const errorData = JSON.parse(event.data);
          console.error('CD日志SSE错误事件:', errorData);

          // 显示错误信息
          if (!cdLog.value) {
            cdLog.value = `获取CD日志失败: ${errorData.error || errorData.message || '未知错误'}`;
          }

          // 清理资源
          clearTimeout(timeout);
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
          if (errorData.error === '404') {
            console.log('CD日志SSE 404错误，任务可能不存在，不再重试');
            is404Error = true; // 设置404错误标志
            if (!cdLog.value) {
              cdLog.value = '未找到日志信息';
            }
            rejectOnce(new Error(`任务不存在: ${errorData.error}`));
          } else {
            console.log('CD日志SSE其他错误，尝试重试');
            if (retryCount < maxRetries) {
              setTimeout(
                () => {
                  cleanupConnection(connectionId);
                  fetchCdLogsStream(jobName, buildId, retryCount + 1)
                    .then(resolveOnce)
                    .catch(rejectOnce);
                },
                3000 * (retryCount + 1)
              );
            } else {
              rejectOnce(
                new Error(`CD日志获取失败: ${errorData.error || errorData.message || '未知错误'}`)
              );
            }
          }
        } catch (parseError) {
          console.error('解析CD日志SSE错误事件失败:', parseError);
          // 如果解析失败，按普通错误处理
          clearTimeout(timeout);
          cleanupHeartbeat();
          eventSource.close();
          eventSourceMap.value.delete(connectionId);
          streamingStatus.value.set(connectionId, false);
          rejectOnce(new Error('解析错误事件失败'));
        }
      });

      // 监听连接关闭事件
      eventSource.addEventListener('close', event => {
        console.log('CD日志SSE连接关闭事件触发');
        clearTimeout(timeout);
        cleanupHeartbeat();
        eventSourceMap.value.delete(connectionId);
        streamingStatus.value.set(connectionId, false);
        // 如果收到过数据，认为日志已完成
        if (hasReceivedData) {
          console.log('CD日志SSE连接关闭且收到过数据，认为日志已完成');
          resolveOnce();
        }
      });

      // 添加心跳检测机制，在日志流暂停期间保持连接
      let heartbeatCount = 0;
      const heartbeatInterval = setInterval(() => {
        heartbeatCount++;
        console.log(`CD日志SSE心跳检测 #${heartbeatCount} - 连接状态: ${eventSource.readyState}`);

        // 如果连接已关闭，清理心跳定时器
        if (eventSource.readyState === EventSource.CLOSED) {
          console.log('CD日志SSE连接已关闭，停止心跳检测');
          clearInterval(heartbeatInterval);
          return;
        }

        // 如果长时间没有收到数据，可能是连接问题
        if (heartbeatCount > 60) {
          // 60秒没有数据
          console.log('CD日志SSE心跳检测超时，连接可能有问题');
          clearInterval(heartbeatInterval);
          // 不主动关闭连接，让错误处理逻辑处理
        }
      }, 1000); // 每秒检测一次

      // 在连接关闭时清理心跳定时器
      const cleanupHeartbeat = () => {
        clearInterval(heartbeatInterval);
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
    console.log('日志对话框关闭，暂停SSE连接但不清理数据');
    // 不清空日志内容，保持数据
    // 不清空连接，只是暂停状态更新
    // 这样可以避免重新打开时的白屏问题

    // 暂停批量更新定时器，但保留队列中的数据
    if (updateTimer.value) {
      clearTimeout(updateTimer.value);
      updateTimer.value = null;
      console.log('暂停批量更新定时器，队列中还有数据:', updateQueue.value.length);
    }

    // 暂停loading状态
    ciLogLoading.value = false;
    cdLogLoading.value = false;

    // 停止定期检查，但不清理连接状态
    stopConnectionCheck();

    // 不清空streamingStatus，保持连接状态
    console.log('当前活跃连接状态:', Array.from(streamingStatus.value.entries()));
    console.log('当前EventSource连接:', Array.from(eventSourceMap.value.keys()));

    console.log('日志对话框关闭完成，保持连接和数据');
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
