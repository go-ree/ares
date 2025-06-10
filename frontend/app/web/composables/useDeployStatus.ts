// 部署状态相关的组合式函数
export function useDeployStatus() {
  // 获取环境标签类型
  const getEnvType = (env: string) => {
    const envMap: Record<string, string> = {
      'dev': 'primary',    // 改为 primary，更柔和的蓝色
      'test': 'warning',   // 保持 warning，醒目的黄色
      'moni': 'info'       // 改为 info，更柔和的灰色
    }
    return envMap[env] || 'info'
  }

  // 获取环境显示名称
  const getEnvLabel = (env: string) => {
    const envMap: Record<string, string> = {
      'dev': '开发环境',
      'test': '测试环境',
      'moni': '模拟环境'
    }
    return envMap[env] || env
  }

  // 获取状态标签类型
  const getStatusType = (status: string) => {
    // 先获取显示文本，再根据显示文本获取标签类型
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

  // 获取进度条状态
  const getProgressStatus = (status: string) => {
    // 先获取显示文本，再根据显示文本获取进度条状态
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

  // 根据状态计算进度
  const calculateProgress = (status: string): number => {
    const progressMap: Record<string, number> = {
      'init': 0,
      'packaging': 25,
      'packaged': 50,
      'package_failed': 50,
      'deploying': 75,
      'deployed': 100,
      'deploy_failed': 100
    }
    return progressMap[status] || 0
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

  // 判断服务是否正在处理中
  const isServiceProcessing = (status: string): boolean => {
    return status === '发布中' ||
           status === '初始化' ||
           status === '打包中' ||
           status === '打包成功' ||
           status === '部署中'
  }

  return {
    getEnvType,
    getEnvLabel,
    getStatusType,
    getProgressStatus,
    getDeployStatus,
    calculateProgress,
    formatDateTime,
    isServiceProcessing
  }
} 