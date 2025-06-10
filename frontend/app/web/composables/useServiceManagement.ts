import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { getTaskDetail } from '@/services/deploy'
import type { TaskRecord } from '@/models/deploy'

// 服务管理相关的组合式函数
export function useServiceManagement() {
  // 服务信息接口
  interface ServiceInfo {
    name: string
    branches: string[]
  }

  // 选中的服务信息
  interface SelectedService {
    serviceName: string
    branch: string
    branchSuffix: string  // 新增字段，用于存储模拟环境的分支后缀
    status: string
    taskId?: number       // 任务ID
    lastUpdateTime?: string // 最后更新时间
  }

  // 可用服务列表
  const availableServices = ref<ServiceInfo[]>([])
  // 选中的服务列表
  const selectedServices = ref<SelectedService[]>([])

  // 定时刷新选中服务的任务状态
  let taskStatusTimer: number | null = null

  // 获取可用服务列表
  const fetchAvailableServices = async () => {
    try {
      // 使用正确的API获取应用名称列表
      const response = await fetch('/api/v1/apps/query/appname', {
        method: 'GET',
        headers: {
          'Content-Type': 'application/json'
        }
      })
      
      if (!response.ok) {
        throw new Error('获取服务列表失败')
      }
      
      const data = await response.json()
      if (data.code !== 1) {
        throw new Error(data.msg || '获取服务列表失败')
      }
      
      // 将 API 返回的数据转换为组件需要的格式
      availableServices.value = (data.result || []).map((appname: string) => ({
        name: appname,
        branches: [] // 分支列表不再需要
      }))
    } catch (error) {
      console.error('获取服务列表失败:', error)
      ElMessage.error(error instanceof Error ? error.message : '获取服务列表失败')
    }
  }

  // 查询任务状态
  const queryTaskStatus = async (taskId: number): Promise<TaskRecord | null> => {
    try {
      const response = await getTaskDetail(taskId)
      if (response.data.code === 1) {
        return response.data.result
      } else {
        console.error('查询任务状态失败:', response.data.msg)
        return null
      }
    } catch (error) {
      console.error('查询任务状态失败:', error)
      return null
    }
  }

  // 更新选中服务的状态
  const updateSelectedServiceStatus = async (serviceIndex: number, taskId: number) => {
    try {
      const taskDetail = await queryTaskStatus(taskId)
      if (taskDetail) {
        const service = selectedServices.value[serviceIndex]
        const oldStatus = service.status
        service.taskId = taskId
        service.lastUpdateTime = formatDateTime(taskDetail.updated_at)
        
        // 根据任务状态更新显示状态
        switch (taskDetail.status) {
          case 'init':
            service.status = '初始化'
            break
          case 'packaging':
            service.status = '打包中'
            break
          case 'packaged':
            service.status = '打包成功'
            break
          case 'package_failed':
            service.status = '打包失败'
            break
          case 'deploying':
            service.status = '部署中'
            break
          case 'deployed':
            service.status = '部署成功'
            break
          case 'deploy_failed':
            service.status = '部署失败'
            break
          default:
            service.status = getDeployStatus(taskDetail.status)
        }
        
        console.log(`服务 ${service.serviceName} 状态更新: ${oldStatus} -> ${service.status} (任务状态: ${taskDetail.status})`)
      }
    } catch (error) {
      console.error('更新服务状态失败:', error)
    }
  }

  // 刷新所有选中服务的任务状态
  const refreshSelectedServicesStatus = async () => {
    console.log('开始刷新选中服务状态，服务数量:', selectedServices.value.length)
    for (let i = 0; i < selectedServices.value.length; i++) {
      const service = selectedServices.value[i]
      console.log(`检查服务 ${i}: ${service.serviceName}`, {
        taskId: service.taskId,
        status: service.status,
        branch: service.branch
      })
      
      // 检查是否有任务ID且状态不是最终状态
      if (service.taskId && 
          service.status !== '部署成功' && 
          service.status !== '部署失败' && 
          service.status !== '未发布' &&
          (service.status === '发布中' || 
           service.status === '初始化' || 
           service.status === '打包中' || 
           service.status === '打包成功' || 
           service.status === '部署中')) {
        console.log(`更新服务 ${service.serviceName} 状态，任务ID: ${service.taskId}，当前状态: ${service.status}`)
        await updateSelectedServiceStatus(i, service.taskId)
      } else {
        console.log(`跳过服务 ${service.serviceName}:`, {
          hasTaskId: !!service.taskId,
          status: service.status,
          isFinalStatus: service.status === '部署成功' || service.status === '部署失败' || service.status === '未发布',
          isUpdatingStatus: service.status === '发布中' || service.status === '初始化' || service.status === '打包中' || service.status === '打包成功' || service.status === '部署中'
        })
      }
    }
  }

  // 获取部署状态显示文本
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

  // 格式化日期时间
  const formatDateTime = (dateStr: string): string => {
    if (!dateStr) return ''
    const date = new Date(dateStr)
    return date.toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false
    })
  }

  // 启动定时刷新
  const startStatusRefresh = () => {
    // 每5秒刷新一次选中服务的任务状态
    taskStatusTimer = window.setInterval(refreshSelectedServicesStatus, 5000)
  }

  // 停止定时刷新
  const stopStatusRefresh = () => {
    if (taskStatusTimer) {
      clearInterval(taskStatusTimer)
      taskStatusTimer = null
    }
  }

  // 组件卸载时清理定时器
  onUnmounted(() => {
    stopStatusRefresh()
  })

  return {
    availableServices,
    selectedServices,
    fetchAvailableServices,
    updateSelectedServiceStatus,
    refreshSelectedServicesStatus,
    startStatusRefresh,
    stopStatusRefresh
  }
} 