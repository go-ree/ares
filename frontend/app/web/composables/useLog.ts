import { ref, reactive, watch, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import { queryPublishLogs, queryTaskLogs } from '@/services/deploy'
import api from '@/config/api'
import type { LogItem, DeployingService, LogFilter } from '@/types/deploy'

export function useLog() {
  // 日志筛选条件
  const logFilter = reactive<LogFilter>({
    serviceName: '',
    environment: '',
    dateRange: []
  })

  // 日志列表数据
  const logList = ref<LogItem[]>([])
  const currentPage = ref(1)
  const pageSize = ref(10)
  const total = ref(0)
  const logLoading = ref(false)

  // 日志对话框相关
  const logDialogVisible = ref(false)
  const currentLog = ref<DeployingService>({} as DeployingService)
  const activeLogTab = ref('ci')
  const ciLog = ref('')
  const cdLog = ref('')
  const ciLogLoading = ref(false)
  const cdLogLoading = ref(false)

  // 日志容器引用
  const ciLogContainer = ref<HTMLElement>()
  const cdLogContainer = ref<HTMLElement>()

  // SSE连接状态
  const ciEventSource = ref<EventSource | null>(null)
  const cdEventSource = ref<EventSource | null>(null)
  const isStreamingCi = ref(false)
  const isStreamingCd = ref(false)

  // 工具函数
  const getStatusType = (status: string) => {
    const statusMap: Record<string, string> = {
      '初始化': 'info',
      '打包中': 'primary',
      '打包成功': 'success',
      '打包失败': 'danger',
      '部署中': 'primary',
      '部署成功': 'success',
      '部署失败': 'danger',
      '已取消': 'warning',
      '超时': 'warning',
      '未知状态': 'info'
    }
    return statusMap[status] || 'info'
  }

  const getDeployStatus = (status: string): string => {
    const statusMap: Record<string, string> = {
      'init': '初始化',
      'packaging': '打包中',
      'packaged': '打包成功',
      'package_failed': '打包失败',
      'deploying': '部署中',
      'deployed': '部署成功',
      'deploy_failed': '部署失败',
      'cancelled': '已取消',
      'timeout': '超时',
      'unknown': '未知状态'
    }
    return statusMap[status] || status
  }

  const getEnvLabel = (env: string): string => {
    const envMap: Record<string, string> = {
      'dev': '开发环境',
      'test': '测试环境',
      'moni': '模拟环境'
    }
    return envMap[env] || env
  }

  const getEnvType = (env: string): string => {
    const envMap: Record<string, string> = {
      'dev': 'info',
      'test': 'warning',
      'moni': 'success'
    }
    return envMap[env] || 'info'
  }

  const formatDateTime = (dateTime: string): string => {
    if (!dateTime) return ''
    const date = new Date(dateTime)
    return date.toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit'
    })
  }

  // 自动滚动到底部
  const scrollToBottom = (container: HTMLElement | undefined) => {
    if (container) {
      nextTick(() => {
        container.scrollTop = container.scrollHeight
      })
    }
  }

  const manualScrollToBottom = () => {
    if (activeLogTab.value === 'ci') {
      scrollToBottom(ciLogContainer.value)
    } else {
      scrollToBottom(cdLogContainer.value)
    }
  }

  // 查询日志
  const handleSearch = async () => {
    logLoading.value = true
    try {
      const params = {
        page_num: currentPage.value,
        page_size: pageSize.value,
        app_name: logFilter.serviceName || undefined,
        env: logFilter.environment || undefined,
        start_time: logFilter.dateRange && logFilter.dateRange[0] ? logFilter.dateRange[0].toISOString() : undefined,
        end_time: logFilter.dateRange && logFilter.dateRange[1] ? logFilter.dateRange[1].toISOString() : undefined
      }

      const response = await queryPublishLogs(params)

      if (response.data.code === 1) {
        const result = response.data.result
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
            products: item.products === 'NULL' ? '' : item.products
          }))
          total.value = result.total || 0
        } else {
          // 处理空结果的情况
          logList.value = []
          total.value = 0
        }
        
        // 显示查询结果提示
        if (logList.value.length > 0) {
          ElMessage.success(`查询成功，共找到 ${total.value} 条记录`)
        } else {
          ElMessage.info('查询完成，未找到相关记录')
        }
      } else {
        throw new Error(response.data.msg || '查询失败')
      }
    } catch (error) {
      console.error('查询日志失败:', error)
      const errorMessage = error instanceof Error ? error.message : '查询失败'
      ElMessage.error(errorMessage)
      
      // 清空数据
      logList.value = []
      total.value = 0
    } finally {
      logLoading.value = false
    }
  }

  // 重置日志筛选条件
  const handleResetLogFilter = () => {
    logFilter.serviceName = ''
    logFilter.environment = ''
    logFilter.dateRange = []
    currentPage.value = 1
    pageSize.value = 10
    // 重置后立即查询
    handleSearch()
  }

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
      auto_deploy: row.auto_deploy
    }
    
    currentLog.value = deployingService
    logDialogVisible.value = true
    activeLogTab.value = 'ci'
    fetchLogs(deployingService)
  }

  // 分页处理
  const handleSizeChange = (val: number) => {
    pageSize.value = val
    handleSearch()
  }

  const handleCurrentChange = (val: number) => {
    currentPage.value = val
    handleSearch()
  }

  // 获取日志
  const fetchLogs = async (row: DeployingService) => {
    // 先清理之前的SSE连接
    cleanupEventSources()

    if (activeLogTab.value === 'ci') {
      ciLogLoading.value = true
      ciLog.value = ''
      try {
        if (row.ciJobName && row.ciBuildId) {
          await fetchCiLogsStream(row.ciJobName, row.ciBuildId)
        } else {
          ciLog.value = ''
          isStreamingCi.value = false
          ciLogLoading.value = false
          return
        }
      } catch (error) {
        ciLog.value = ''
      } finally {
        ciLogLoading.value = false
      }
    } else if (activeLogTab.value === 'cd') {
      cdLogLoading.value = true
      cdLog.value = ''
      try {
        if (row.cdJobName && row.cdBuildId) {
          await fetchCdLogsStream(row.cdJobName, row.cdBuildId)
        } else {
          // 没有CD job信息，直接显示暂无日志
          cdLog.value = ''
          isStreamingCd.value = false
          cdLogLoading.value = false
          return
        }
      } catch (error) {
        cdLog.value = ''
      } finally {
        cdLogLoading.value = false
      }
    }
  }

  // 使用SSE获取CI日志
  const fetchCiLogsStream = async (jobName: string, buildId: number) => {
    return new Promise<void>((resolve, reject) => {
      const url = `/api/v1/job/stream/log?job_name=${encodeURIComponent(jobName)}&build_id=${buildId}`
      
      console.log('开始获取CI日志:', { jobName, buildId, url })
      
      ciEventSource.value = new EventSource(url)
      isStreamingCi.value = true
      
      let hasReceivedData = false
      
      ciEventSource.value.onmessage = (event: MessageEvent) => {
        try {
          console.log('CI日志SSE收到数据:', event.data)
          hasReceivedData = true
          
          // 解析JSON数据
          const data = JSON.parse(event.data)
          if (data.code === 1 && data.result && Array.isArray(data.result)) {
            // 将每一行日志添加到内容中
            ciLog.value += data.result.join('\n') + '\n'
            // 自动滚动到底部
            scrollToBottom(ciLogContainer.value)
          } else if (data.code === 0) {
            // 处理错误
            console.error('CI日志SSE错误:', data.msg || data.error)
            reject(new Error(data.msg || data.error || '获取 CI 日志失败'))
            cleanupEventSources()
          } else if (data.code === 1 && data.msg === 'end') {
            // 日志流结束
            console.log('CI日志SSE流正常结束')
            cleanupEventSources()
            resolve()
          }
        } catch (error) {
          console.error('解析CI日志SSE数据失败:', error)
          reject(error)
          cleanupEventSources()
        }
      }
      
      // 监听错误事件
      ciEventSource.value.addEventListener('error', (event: MessageEvent) => {
        console.error('CI日志SSE错误事件:', event)
        try {
          const data = JSON.parse(event.data)
          reject(new Error(data.msg || data.error || '获取 CI 日志失败'))
        } catch (error) {
          reject(new Error('获取 CI 日志失败'))
        }
        cleanupEventSources()
      })
      
      // 监听结束事件
      ciEventSource.value.addEventListener('end', () => {
        console.log('CI日志SSE流结束')
        cleanupEventSources()
        resolve()
      })
      
      ciEventSource.value.onerror = (error) => {
        console.error('CI日志SSE连接错误:', error)
        cleanupEventSources()
        reject(new Error('CI日志SSE连接失败'))
      }
      
      ciEventSource.value.onopen = () => {
        console.log('CI日志SSE连接已建立')
      }
      
      // 设置超时处理 - 增加超时时间
      const timeout = setTimeout(() => {
        console.log('CI日志SSE超时，hasReceivedData:', hasReceivedData)
        if (!hasReceivedData) {
          // 如果没有收到任何数据，可能是连接问题
          reject(new Error('CI日志获取超时，请重试'))
        } else {
          // 如果收到过数据，正常结束
          resolve()
        }
        cleanupEventSources()
      }, 60000) // 增加到60秒超时
      
      // 监听连接关闭
      ciEventSource.value.addEventListener('close', () => {
        console.log('CI日志SSE连接关闭')
        clearTimeout(timeout)
        isStreamingCi.value = false
        resolve()
      })
    })
  }

  // 使用SSE获取CD日志
  const fetchCdLogsStream = async (jobName: string, buildId: number) => {
    return new Promise<void>((resolve, reject) => {
      const url = `/api/v1/job/stream/log?job_name=${encodeURIComponent(jobName)}&build_id=${buildId}`
      
      console.log('开始获取CD日志:', { jobName, buildId, url })
      
      cdEventSource.value = new EventSource(url)
      isStreamingCd.value = true
      
      let hasReceivedData = false
      
      cdEventSource.value.onmessage = (event: MessageEvent) => {
        try {
          console.log('CD日志SSE收到数据:', event.data)
          hasReceivedData = true
          
          // 解析JSON数据
          const data = JSON.parse(event.data)
          if (data.code === 1 && data.result && Array.isArray(data.result)) {
            // 将每一行日志添加到内容中
            cdLog.value += data.result.join('\n') + '\n'
            // 自动滚动到底部
            scrollToBottom(cdLogContainer.value)
          } else if (data.code === 0) {
            // 处理错误
            console.error('CD日志SSE错误:', data.msg || data.error)
            reject(new Error(data.msg || data.error || '获取 CD 日志失败'))
            cleanupEventSources()
          } else if (data.code === 1 && data.msg === 'end') {
            // 日志流结束
            console.log('CD日志SSE流正常结束')
            cleanupEventSources()
            resolve()
          }
        } catch (error) {
          console.error('解析CD日志SSE数据失败:', error)
          reject(error)
          cleanupEventSources()
        }
      }
      
      // 监听错误事件
      cdEventSource.value.addEventListener('error', (event: MessageEvent) => {
        console.error('CD日志SSE错误事件:', event)
        try {
          const data = JSON.parse(event.data)
          reject(new Error(data.msg || data.error || '获取 CD 日志失败'))
        } catch (error) {
          reject(new Error('获取 CD 日志失败'))
        }
        cleanupEventSources()
      })
      
      // 监听结束事件
      cdEventSource.value.addEventListener('end', () => {
        console.log('CD日志SSE流结束')
        cleanupEventSources()
        resolve()
      })
      
      cdEventSource.value.onerror = (error) => {
        console.error('CD日志SSE连接错误:', error)
        cleanupEventSources()
        reject(new Error('CD日志SSE连接失败'))
      }
      
      cdEventSource.value.onopen = () => {
        console.log('CD日志SSE连接已建立')
      }
      
      // 设置超时处理 - 增加超时时间
      const timeout = setTimeout(() => {
        console.log('CD日志SSE超时，hasReceivedData:', hasReceivedData)
        if (!hasReceivedData) {
          // 如果没有收到任何数据，可能是连接问题
          reject(new Error('CD日志获取超时，请重试'))
        } else {
          // 如果收到过数据，正常结束
          resolve()
        }
        cleanupEventSources()
      }, 60000) // 增加到60秒超时
      
      // 监听连接关闭
      cdEventSource.value.addEventListener('close', () => {
        console.log('CD日志SSE连接关闭')
        clearTimeout(timeout)
        isStreamingCd.value = false
        resolve()
      })
    })
  }

  // 清理SSE连接
  const cleanupEventSources = () => {
    if (ciEventSource.value) {
      ciEventSource.value.close()
      ciEventSource.value = null
      isStreamingCi.value = false
    }
    if (cdEventSource.value) {
      cdEventSource.value.close()
      cdEventSource.value = null
      isStreamingCd.value = false
    }
  }

  // 监听日志标签页切换
  watch(activeLogTab, async (_newTab) => {
    if (logDialogVisible.value && currentLog.value) {
      await fetchLogs(currentLog.value)
    }
  })

  // 监听CI日志内容变化，自动滚动到底部
  watch(ciLog, () => {
    scrollToBottom(ciLogContainer.value)
  })

  // 监听CD日志内容变化，自动滚动到底部
  watch(cdLog, () => {
    scrollToBottom(cdLogContainer.value)
  })

  // 日志对话框关闭处理
  const handleLogDialogClose = () => {
    cleanupEventSources()
    ciLog.value = ''
    cdLog.value = ''
    isStreamingCi.value = false
    isStreamingCd.value = false
  }

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
    isStreamingCi,
    isStreamingCd,
    
    // 工具函数
    getStatusType,
    getDeployStatus,
    getEnvLabel,
    getEnvType,
    formatDateTime,
    scrollToBottom,
    manualScrollToBottom,
    
    // 事件处理函数
    handleSearch,
    handleResetLogFilter,
    viewLogDetail,
    handleSizeChange,
    handleCurrentChange,
    fetchLogs,
    handleLogDialogClose,
    
    // 清理函数
    cleanupEventSources
  }
} 