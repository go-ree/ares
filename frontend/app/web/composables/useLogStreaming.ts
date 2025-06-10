import { ref, onUnmounted } from 'vue'

// 日志流式传输相关的组合式函数
export function useLogStreaming() {
  // 日志内容
  const ciLog = ref('')
  const cdLog = ref('')
  
  // 加载状态
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

  // 发布中服务列表数据
  interface DeployingService {
    id: number
    serviceName: string
    branch: string
    environment: string
    status: string
    progress: number
    startTime: string
    operator: string
    message?: string
    taskId: number
    ciJobName?: string
    cdJobName?: string
    ciBuildId?: number
    cdBuildId?: number
    products?: string
    auto_deploy?: number
    pipelineParam?: {
      env: string
      image: string
      branch: string
      git_url: string
      app_name: string
      [key: string]: any
    }
  }

  // 自动滚动到底部
  const scrollToBottom = (container: HTMLElement | undefined) => {
    if (container) {
      console.log('滚动到底部，容器高度:', container.scrollHeight, '当前滚动位置:', container.scrollTop)
      
      // 只滚动日志内容容器，不影响标签页
      const logContent = container.querySelector('.log-text') as HTMLElement
      if (logContent) {
        // 方法1: 直接设置scrollTop
        container.scrollTop = container.scrollHeight
        
        // 方法2: 使用scrollTo
        setTimeout(() => {
          container.scrollTo({
            top: container.scrollHeight,
            behavior: 'smooth'
          })
        }, 50)
        
        console.log('滚动完成，新位置:', container.scrollTop)
      } else {
        console.log('未找到日志内容元素')
      }
    } else {
      console.log('容器未找到，无法滚动')
    }
  }

  // 手动滚动到底部（用于测试）
  const manualScrollToBottom = () => {
    // 根据当前激活的标签页决定滚动哪个容器
    // 这里需要外部传入当前激活的标签页信息
    if (ciLogContainer.value) {
      scrollToBottom(ciLogContainer.value)
    } else if (cdLogContainer.value) {
      scrollToBottom(cdLogContainer.value)
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

  // 获取日志
  const fetchLogs = async (row: DeployingService, activeTab: string = 'ci') => {
    // 先清理之前的SSE连接和日志数据
    cleanupEventSources()
    clearLogs()

    console.log('开始获取日志:', { 
      serviceName: row.serviceName, 
      taskId: row.taskId,
      activeTab,
      ciJobName: row.ciJobName,
      cdJobName: row.cdJobName,
      ciBuildId: row.ciBuildId,
      cdBuildId: row.cdBuildId
    })

    // 根据当前激活的标签页决定获取哪种日志
    if (activeTab === 'ci') {
      await fetchCiLogs(row)
    } else if (activeTab === 'cd') {
      await fetchCdLogs(row)
    }
  }

  // 获取CI日志
  const fetchCiLogs = async (row: DeployingService) => {
    ciLogLoading.value = true
    ciLog.value = ''
    isStreamingCi.value = false
    
    try {
      if (row.ciJobName && row.ciBuildId) {
        console.log('获取CI日志:', { jobName: row.ciJobName, buildId: row.ciBuildId })
        await fetchCiLogsStream(row.ciJobName, row.ciBuildId)
      } else {
        console.log('CI日志参数缺失:', { ciJobName: row.ciJobName, ciBuildId: row.ciBuildId })
        ciLog.value = '暂无CI日志信息'
      }
    } catch (error) {
      console.error('获取CI日志失败:', error)
      ciLog.value = `获取CI日志失败: ${error instanceof Error ? error.message : '未知错误'}`
    } finally {
      ciLogLoading.value = false
      isStreamingCi.value = false
    }
  }

  // 获取CD日志
  const fetchCdLogs = async (row: DeployingService) => {
    cdLogLoading.value = true
    cdLog.value = ''
    isStreamingCd.value = false
    
    try {
      if (row.cdJobName && row.cdBuildId) {
        console.log('获取CD日志:', { jobName: row.cdJobName, buildId: row.cdBuildId })
        await fetchCdLogsStream(row.cdJobName, row.cdBuildId)
      } else {
        console.log('CD日志参数缺失:', { cdJobName: row.cdJobName, cdBuildId: row.cdBuildId })
        cdLog.value = '暂无CD日志信息'
      }
    } catch (error) {
      console.error('获取CD日志失败:', error)
      cdLog.value = `获取CD日志失败: ${error instanceof Error ? error.message : '未知错误'}`
    } finally {
      cdLogLoading.value = false
      isStreamingCd.value = false
    }
  }

  // 清理日志数据
  const clearLogs = () => {
    ciLog.value = ''
    cdLog.value = ''
    ciLogLoading.value = false
    cdLogLoading.value = false
    isStreamingCi.value = false
    isStreamingCd.value = false
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

  // 组件卸载时清理资源
  onUnmounted(() => {
    cleanupEventSources()
  })

  return {
    ciLog,
    cdLog,
    ciLogLoading,
    cdLogLoading,
    isStreamingCi,
    isStreamingCd,
    ciLogContainer,
    cdLogContainer,
    fetchLogs,
    fetchCiLogs,
    fetchCdLogs,
    fetchCiLogsStream,
    fetchCdLogsStream,
    cleanupEventSources,
    clearLogs,
    manualScrollToBottom,
    scrollToBottom
  }
} 