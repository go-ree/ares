<template>
  <div class="service-deploy">
    <div class="page-header">
      <h2>服务发布管理</h2>
    </div>
    
    <el-card>
      <el-tabs v-model="activeTab" type="border-card">
        <el-tab-pane label="工具" name="tool">
          <DeployTool :is-active="activeTab === 'tool'" />
          <DeployingList 
            ref="deployingListRef"
            :is-active="activeTab === 'tool'"
            @view-log="handleViewLog" 
          />
        </el-tab-pane>
        
        <el-tab-pane label="日志" name="log">
          <LogQuery 
            :is-active="activeTab === 'log'"
            @view-log-detail="handleViewLogDetail" 
          />
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <LogDetail 
      v-model:visible="logDetailVisible"
      :log-data="currentLogData"
      @close="handleLogDialogClose"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import DeployTool from './DeployTool.vue'
import DeployingList from './DeployingList.vue'
import LogQuery from './LogQuery.vue'
import LogDetail from './LogDetail.vue'
import type { DeployingService } from '@/types/deploy'

// 当前激活的标签页
const activeTab = ref('tool')

// 日志详情相关
const logDetailVisible = ref(false)
const currentLogData = ref<DeployingService | undefined>()

// DeployingList组件引用
const deployingListRef = ref()

// 查看日志（从发布中服务列表）
const handleViewLog = (service: DeployingService) => {
  currentLogData.value = service
  logDetailVisible.value = true
}

// 查看日志详情（从日志查询列表）
const handleViewLogDetail = (logItem: any) => {
  console.log('ServiceDeploy: 收到查看日志详情事件', logItem)
  
  // 将LogItem转换为DeployingService格式
  const deployingService: DeployingService = {
    id: logItem.task_id,
    serviceName: logItem.serviceName,
    branch: logItem.branch,
    environment: logItem.environment,
    status: logItem.status,
    progress: 100, // 已完成的任务进度为100
    startTime: logItem.deployTime,
    operator: logItem.operator,
    message: logItem.message,
    taskId: logItem.task_id,
    ciJobName: logItem.ci_job_name,
    cdJobName: logItem.cd_job_name,
    ciBuildId: logItem.ci_build_id,
    cdBuildId: logItem.cd_build_id,
    products: logItem.products,
    auto_deploy: logItem.auto_deploy
  }
  
  console.log('ServiceDeploy: 转换后的服务数据', deployingService)
  
  currentLogData.value = deployingService
  logDetailVisible.value = true
  
  console.log('ServiceDeploy: 对话框状态', { logDetailVisible: logDetailVisible.value, currentLogData: currentLogData.value })
}

// 处理日志对话框关闭
const handleLogDialogClose = () => {
  // 恢复DeployingList的自动刷新
  if (deployingListRef.value && deployingListRef.value.resumeAutoRefresh) {
    deployingListRef.value.resumeAutoRefresh()
  }
}

// 监听主标签页切换
watch(activeTab, (newTab) => {
  console.log('切换到标签页:', newTab)
  
  // 通知子组件标签页切换
  if (newTab === 'tool') {
    // 工具页激活时，通知DeployTool和DeployingList加载数据
    console.log('工具页激活，通知相关组件加载数据')
  } else if (newTab === 'log') {
    // 日志页激活时，通知LogQuery加载数据
    console.log('日志页激活，通知LogQuery加载数据')
  }
})

// 组件挂载时确保标签页正确初始化
onMounted(() => {
  console.log('ServiceDeploy组件挂载，当前标签页:', activeTab.value)
})
</script>

<style scoped>
.service-deploy {
  padding: 20px;
}

.page-header {
  margin-bottom: 20px;
}

.page-header h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  color: #303133;
}
</style> 