import { ref, reactive, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { batchDeploy, createDeploy } from '@/services/deploy'
import { useUserStore } from '@/stores/user'
import api from '@/config/api'
import type { 
  DeployingService, 
  ServiceInfo, 
  SelectedService, 
  DeployForm,
  Environment,
  DeployStatus 
} from '@/types/deploy'

export function useDeploy() {
  const userStore = useUserStore()

  // 发布表单数据
  const deployForm = reactive<DeployForm>({
    serviceName: '',
    environment: '',
    version: ''
  })

  // 全局分支后缀
  const globalBranchSuffix = ref('')

  // 可用服务列表
  const availableServices = ref<ServiceInfo[]>([])

  // 选中的服务列表
  const selectedServices = ref<SelectedService[]>([])

  // 发布中服务列表
  const deployingList = ref<DeployingService[]>([])
  const deployingLoading = ref(false)

  // 计算属性
  const hasDeployableServices = computed(() => {
    return selectedServices.value.some(service => 
      service.serviceName && service.branch && !isServiceProcessing(service.status)
    )
  })

  // 工具函数
  const isServiceProcessing = (status: string): boolean => {
    return ['发布中', '打包中', '部署中'].includes(status)
  }

  const getStatusType = (status: string) => {
    const displayStatus = getDeployStatus(status)
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
    return statusMap[displayStatus] || 'info'
  }

  const getProgressStatus = (status: string) => {
    const displayStatus = getDeployStatus(status)
    const statusMap: Record<string, string> = {
      '初始化': '',
      '打包中': '',
      '打包成功': 'success',
      '打包失败': 'exception',
      '部署中': '',
      '部署成功': 'success',
      '部署失败': 'exception',
      '已取消': 'warning',
      '超时': 'warning',
      '未知状态': ''
    }
    return statusMap[displayStatus] || ''
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

  const calculateProgress = (status: string): number => {
    const statusProgressMap: Record<string, number> = {
      'init': 10,
      'packaging': 30,
      'packaged': 60,
      'package_failed': 60,
      'deploying': 80,
      'deployed': 100,
      'deploy_failed': 100,
      'cancelled': 100,
      'timeout': 100,
      'unknown': 0
    }
    return statusProgressMap[status] || 0
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

  // 事件处理函数
  const handleEnvChange = () => {
    // 环境改变时的逻辑
    console.log('环境改变:', deployForm.environment)
    
    // 如果切换到模拟环境，更新所有服务的分支
    if (deployForm.environment === 'moni') {
      selectedServices.value.forEach(service => {
        service.branch = `release_${globalBranchSuffix.value}`
        service.branchSuffix = globalBranchSuffix.value
      })
    } else {
      // 如果切换到其他环境，更新所有服务的分支
      selectedServices.value.forEach(service => {
        service.branch = deployForm.environment
        service.branchSuffix = undefined
      })
    }
  }

  const handleGlobalBranchChange = () => {
    // 全局分支改变时的逻辑
    console.log('全局分支后缀改变:', globalBranchSuffix.value)
    selectedServices.value.forEach(service => {
      if (deployForm.environment === 'moni') {
        service.branch = `release_${globalBranchSuffix.value}`
        service.branchSuffix = globalBranchSuffix.value
        console.log(`更新服务 ${service.serviceName} 的分支为: ${service.branch}`)
      }
    })
  }

  const handleServiceSelect = (serviceName: string, index: number) => {
    const service = selectedServices.value[index]
    if (service) {
      service.serviceName = serviceName
      if (deployForm.environment === 'moni') {
        service.branch = `release_${globalBranchSuffix.value}`
      } else {
        service.branch = deployForm.environment
      }
    }
  }

  const handleBranchSuffixChange = (suffix: string, service: SelectedService) => {
    service.branchSuffix = suffix
    service.branch = `release_${suffix}`
  }

  const handleAddService = () => {
    const newService: SelectedService = {
      serviceName: '',
      branch: deployForm.environment === 'moni' ? `release_${globalBranchSuffix.value}` : deployForm.environment,
      status: '待发布'
    }
    selectedServices.value.push(newService)
  }

  const handleRemoveService = (index: number) => {
    selectedServices.value.splice(index, 1)
  }

  const handleDeploySingle = async (service: SelectedService, index: number) => {
    try {
      if (!userStore.userInfo) {
        throw new Error('用户未登录')
      }

      service.status = '发布中'
      service.lastUpdateTime = formatDateTime(new Date().toISOString())

      const response = await createDeploy({
        app_name: service.serviceName,
        env: deployForm.environment,
        branch: service.branch
      }, {
        username: userStore.userInfo.username,
        nameCn: userStore.userInfo.nameCn
      })

      if (response.data.code === 1) {
        const taskRecord = response.data.result
        service.taskId = taskRecord.task_record.task_id
        ElMessage.success(`${service.serviceName} 发布任务已提交`)
      } else {
        throw new Error(response.data.msg || '发布失败')
      }
    } catch (error) {
      service.status = '发布失败'
      console.error('单个发布失败:', error)
      const errorMessage = error instanceof Error ? error.message : '发布失败'
      ElMessage.error(errorMessage)
    }
  }

  const handleRedeploySingle = async (service: SelectedService, index: number) => {
    try {
      if (!userStore.userInfo) {
        throw new Error('用户未登录')
      }

      service.status = '发布中'
      service.lastUpdateTime = formatDateTime(new Date().toISOString())

      const response = await createDeploy({
        app_name: service.serviceName,
        env: deployForm.environment,
        branch: service.branch
      }, {
        username: userStore.userInfo.username,
        nameCn: userStore.userInfo.nameCn
      })

      if (response.data.code === 1) {
        const taskRecord = response.data.result
        service.taskId = taskRecord.task_record.task_id
        ElMessage.success(`${service.serviceName} 重发任务已提交`)
      } else {
        throw new Error(response.data.msg || '重发失败')
      }
    } catch (error) {
      service.status = '发布失败'
      console.error('单个重发失败:', error)
      const errorMessage = error instanceof Error ? error.message : '重发失败'
      ElMessage.error(errorMessage)
    }
  }

  const handleBatchDeploy = async () => {
    try {
      if (!userStore.userInfo) {
        throw new Error('用户未登录')
      }

      const deployableServices = selectedServices.value.filter(service => 
        service.serviceName && service.branch && !isServiceProcessing(service.status)
      )

      if (deployableServices.length === 0) {
        ElMessage.warning('没有可发布的服务')
        return
      }

      // 设置所有服务状态为发布中
      deployableServices.forEach(service => {
        service.status = '发布中'
      })

      // 构建批量发布请求
      const deployRequests = deployableServices.map(service => ({
        app_name: service.serviceName,
        env: deployForm.environment,
        branch: service.branch
      }))

      const response = await batchDeploy(deployRequests, {
        username: userStore.userInfo.username,
        nameCn: userStore.userInfo.nameCn
      })

      if (response.data.code === 1) {
        const result = response.data.result
        const successCount = result.success_count
        const failureCount = result.failure_count
        const totalCount = result.total_count
        
        ElMessage.success(`批量发布任务已提交，成功: ${successCount}，失败: ${failureCount}，总计: ${totalCount}`)
        
        // 更新任务ID
        if (result.task_records && Array.isArray(result.task_records)) {
          for (const taskRecord of result.task_records) {
            if (taskRecord.success && taskRecord.task_record) {
              const taskDetail = taskRecord.task_record
              const serviceIndex = selectedServices.value.findIndex(service => 
                service.serviceName === taskDetail.app_name && service.branch === taskDetail.branch
              )
              if (serviceIndex >= 0) {
                selectedServices.value[serviceIndex].taskId = taskDetail.task_id
              }
            }
          }
        }
      } else {
        throw new Error(response.data.msg || '批量发布失败')
      }
    } catch (error) {
      console.error('批量发布失败:', error)
      const errorMessage = error instanceof Error ? error.message : '批量发布失败'
      ElMessage.error(errorMessage)
      
      // 重置失败的服务状态
      selectedServices.value.forEach(service => {
        if (service.status === '发布中') {
          service.status = '发布失败'
        }
      })
    }
  }

  const handleBatchRedeploy = async () => {
    try {
      if (!userStore.userInfo) {
        throw new Error('用户未登录')
      }

      const deployableServices = selectedServices.value.filter(service => 
        service.serviceName && service.branch && !isServiceProcessing(service.status)
      )

      if (deployableServices.length === 0) {
        ElMessage.warning('没有可重发的服务')
        return
      }

      // 设置所有服务状态为发布中
      deployableServices.forEach(service => {
        service.status = '发布中'
      })

      // 构建批量重发请求
      const deployRequests = deployableServices.map(service => ({
        app_name: service.serviceName,
        env: deployForm.environment,
        branch: service.branch
      }))

      const response = await batchDeploy(deployRequests, {
        username: userStore.userInfo.username,
        nameCn: userStore.userInfo.nameCn
      })

      if (response.data.code === 1) {
        const result = response.data.result
        const successCount = result.success_count
        const failureCount = result.failure_count
        const totalCount = result.total_count
        
        ElMessage.success(`批量重发任务已提交，成功: ${successCount}，失败: ${failureCount}，总计: ${totalCount}`)
        
        // 更新任务ID
        if (result.task_records && Array.isArray(result.task_records)) {
          for (const taskRecord of result.task_records) {
            if (taskRecord.success && taskRecord.task_record) {
              const taskDetail = taskRecord.task_record
              const serviceIndex = selectedServices.value.findIndex(service => 
                service.serviceName === taskDetail.app_name && service.branch === taskDetail.branch
              )
              if (serviceIndex >= 0) {
                selectedServices.value[serviceIndex].taskId = taskDetail.task_id
              }
            }
          }
        }
      } else {
        throw new Error(response.data.msg || '批量重发失败')
      }
    } catch (error) {
      console.error('批量重发失败:', error)
      const errorMessage = error instanceof Error ? error.message : '批量重发失败'
      ElMessage.error(errorMessage)
      
      // 重置失败的服务状态
      selectedServices.value.forEach(service => {
        if (service.status === '发布中') {
          service.status = '发布失败'
        }
      })
    }
  }

  const handleCancelDeploy = async (service: DeployingService) => {
    try {
      await ElMessageBox.confirm(
        `确定要取消 ${service.serviceName} 的发布吗？`,
        '取消发布',
        {
          confirmButtonText: '确定',
          cancelButtonText: '取消',
          type: 'warning'
        }
      )
      
      // TODO: 调用取消发布 API
      service.status = '已取消'
      ElMessage.success('已取消发布')
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error('取消发布失败')
      }
    }
  }

  const refreshDeployingList = async () => {
    deployingLoading.value = true
    try {
      const response = await api.get('/api/v1/deploy/publish/status')
      
      if (response.data.code !== 1) {
        throw new Error(response.data.msg || '获取发布中服务列表失败')
      }
      
      // 将 API 返回的数据转换为组件需要的格式
      deployingList.value = (response.data.result || []).map((item: any) => ({
        id: item.task_id,
        serviceName: item.app_name,
        branch: item.branch,
        environment: item.env,
        status: getDeployStatus(item.status),
        progress: calculateProgress(item.status),
        startTime: formatDateTime(item.created_at),
        operator: item.publisher,
        message: item.message === 'NULL' ? '' : item.message,
        taskId: item.task_id,
        ciJobName: item.ci_job_name === 'NULL' ? '' : item.ci_job_name,
        cdJobName: item.cd_job_name === 'NULL' ? '' : item.cd_job_name,
        ciBuildId: item.ci_build_id || null,
        cdBuildId: item.cd_build_id || null,
        products: item.products === 'NULL' ? '' : item.products,
        pipelineParam: item.pipeline_param
      }))
    } catch (error) {
      console.error('获取发布中服务列表失败:', error)
      ElMessage.error('获取发布中服务列表失败')
    } finally {
      deployingLoading.value = false
    }
  }

  // 加载可用服务列表
  const loadAvailableServices = async () => {
    try {
      console.log('开始加载可用服务列表...')
      const response = await api.get('/api/v1/apps/query/appname')
      console.log('API响应:', response)
      
      if (response.data.code === 1) {
        // API返回的是应用名称字符串数组
        const appNames = response.data.result || []
        
        // 转换为ServiceInfo格式
        availableServices.value = appNames.map((appName: string) => ({
          name: appName,
          nameCn: appName, // 如果没有中文名称，使用英文名称
          description: ''
        }))
        
        console.log('加载到的服务列表:', availableServices.value)
      } else {
        console.error('API返回错误:', response.data)
        throw new Error(response.data.msg || '获取服务列表失败')
      }
    } catch (error) {
      console.error('获取服务列表失败:', error)
      ElMessage.error('获取服务列表失败')
    }
  }

  return {
    // 响应式数据
    deployForm,
    globalBranchSuffix,
    availableServices,
    selectedServices,
    deployingList,
    deployingLoading,
    
    // 计算属性
    hasDeployableServices,
    
    // 工具函数
    isServiceProcessing,
    getStatusType,
    getProgressStatus,
    getDeployStatus,
    getEnvLabel,
    getEnvType,
    calculateProgress,
    formatDateTime,
    
    // 事件处理函数
    handleEnvChange,
    handleGlobalBranchChange,
    handleServiceSelect,
    handleBranchSuffixChange,
    handleAddService,
    handleRemoveService,
    handleDeploySingle,
    handleRedeploySingle,
    handleBatchDeploy,
    handleBatchRedeploy,
    handleCancelDeploy,
    refreshDeployingList,
    loadAvailableServices
  }
} 