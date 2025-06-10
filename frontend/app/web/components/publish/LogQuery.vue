<template>
  <div class="log-content">
    <div class="log-filter">
      <el-form :inline="true" :model="logFilter">
        <el-form-item label="服务名称">
          <el-select 
            v-model="logFilter.serviceName" 
            filterable 
            placeholder="请选择服务名称"
            clearable
            style="width: 200px"
          >
            <el-option
              v-for="service in availableServices"
              :key="service.name"
              :label="service.name"
              :value="service.name"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="环境">
          <el-select v-model="logFilter.environment" placeholder="请选择环境" clearable>
            <el-option label="全部" value="" />
            <el-option label="开发环境" value="dev" />
            <el-option label="测试环境" value="test" />
            <el-option label="模拟环境" value="moni" />
          </el-select>
        </el-form-item>
        <el-form-item label="时间范围">
          <el-date-picker
            v-model="logFilter.dateRange"
            type="daterange"
            range-separator="至"
            start-placeholder="开始日期"
            end-placeholder="结束日期"
            clearable
          />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="handleSearch">查询</el-button>
          <el-button @click="handleResetLogFilter">重置</el-button>
        </el-form-item>
      </el-form>
    </div>
    <div class="log-table">
      <el-table :data="logList" style="width: 100%" v-loading="logLoading">
        <el-table-column prop="serviceName" label="服务名称" min-width="120" max-width="180" show-overflow-tooltip />
        <el-table-column prop="branch" label="发布分支" min-width="100" max-width="140" show-overflow-tooltip />
        <el-table-column prop="environment" label="环境" width="80">
          <template #default="{ row }">
            <el-tag :type="getEnvType(row.environment)">
              {{ getEnvLabel(row.environment) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="status" label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="getStatusType(row.status)">
              {{ row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="deployTime" label="发布时间" width="140" />
        <el-table-column prop="operator" label="操作人" width="80" />
        <el-table-column prop="auto_deploy" label="自动部署" width="80">
          <template #default="{ row }">
            <el-tag :type="row.auto_deploy ? 'success' : 'info'" size="small">
              {{ row.auto_deploy ? '是' : '否' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-button type="primary" link @click="viewLogDetail(row)">
              查看日志
            </el-button>
          </template>
        </el-table-column>
      </el-table>
      
      <!-- 空数据提示 -->
      <div v-if="logList.length === 0 && !logLoading" class="empty-data">
        <el-empty description="暂无日志数据" />
      </div>
      
      <div class="pagination">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[10, 20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @size-change="handleSizeChange"
          @current-change="handleCurrentChange"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { queryPublishLogs } from '@/services/deploy'
import type { PublishLogQueryParams, PublishLogTaskRecord } from '@/models/deploy'
import { useDeployStatus } from '@/composables/useDeployStatus'
import { useServiceManagement } from '@/composables/useServiceManagement'

// 使用组合式函数
const { getEnvType, getEnvLabel, getStatusType, getDeployStatus, formatDateTime } = useDeployStatus()
const { availableServices, fetchAvailableServices } = useServiceManagement()

// 定义日志数据接口
interface LogItem {
  task_id: number
  serviceName: string
  branch: string
  environment: string
  status: string
  deployTime: string
  operator: string
  message: string
  auto_deploy: number
  ci_job_name: string
  cd_job_name: string
  ci_build_id: number
  cd_build_id: number
  products: string
}

// 日志筛选条件
const logFilter = reactive({
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

// 定义事件
const emit = defineEmits<{
  (e: 'view-log-detail', logItem: LogItem): void
}>()

// 处理日志查询
const handleSearch = async (silent = false) => {
  // 基本参数验证
  if (currentPage.value < 1) {
    currentPage.value = 1
  }
  if (pageSize.value < 1 || pageSize.value > 100) {
    pageSize.value = 10
  }
  
  logLoading.value = true
  try {
    // 构建查询参数
    const params: PublishLogQueryParams = {
      page_num: currentPage.value,
      page_size: pageSize.value,
      app_name: logFilter.serviceName || undefined,
      env: logFilter.environment || undefined,
      publisher: undefined
    }

    // 处理时间范围
    if (logFilter.dateRange && logFilter.dateRange.length === 2) {
      params.start_time = logFilter.dateRange[0]
      params.end_time = logFilter.dateRange[1]
    }

    // 调用日志查询API
    const response = await queryPublishLogs(params)
    
    if (response.data.code === 1) {
      const result = response.data.result
      // 转换数据格式，添加空值检查
      if (result && result.task_record && Array.isArray(result.task_record)) {
        logList.value = result.task_record.map((item: PublishLogTaskRecord) => ({
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
      
      // 只在非静默模式下显示查询结果提示
      if (!silent) {
        if (logList.value.length > 0) {
          ElMessage.success(`查询成功，共找到 ${total.value} 条记录`)
        } else {
          ElMessage.info('查询完成，未找到相关记录')
        }
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
  emit('view-log-detail', row)
}

// 分页处理
const handleSizeChange = (val: number) => {
  pageSize.value = val
  handleSearch(true) // 静默模式，不显示提示
}

const handleCurrentChange = (val: number) => {
  currentPage.value = val
  handleSearch(true) // 静默模式，不显示提示
}

// 暴露方法给父组件调用
defineExpose({
  handleSearch
})

// 组件挂载时只获取可用服务列表，不自动查询日志
onMounted(() => {
  fetchAvailableServices()
  // 移除自动查询，只在切换到日志标签页时由父组件触发
})
</script>

<style scoped>
.log-content {
  padding: 20px 0;
}

.log-filter {
  margin-bottom: 20px;
  padding: 20px;
  background: #f8f9fa;
  border-radius: 4px;
  border: 1px solid #e9ecef;
}

.log-filter .el-form {
  margin-bottom: 0;
}

.log-filter .el-form-item {
  margin-bottom: 0;
  margin-right: 20px;
}

.log-filter .el-form-item:last-child {
  margin-right: 0;
}

.log-table {
  margin-top: 20px;
}

.log-table :deep(.el-table) {
  box-shadow: none !important;
  border: 1px solid #ebeef5 !important;
  border-radius: 4px !important;
  background-color: #fff !important;
  color: #213547 !important;
}

.log-table :deep(.el-table__header-wrapper) {
  background-color: #fafafa !important;
}

.log-table :deep(.el-table__body-wrapper) {
  background-color: #fff !important;
}

.log-table :deep(.el-table__header) {
  background-color: #fafafa !important;
}

.log-table :deep(.el-table__body) {
  background-color: #fff !important;
}

.log-table :deep(.el-table__row) {
  background-color: #fff !important;
}

.log-table :deep(.el-table__row:hover) {
  background-color: #f5f7fa !important;
}

.log-table :deep(.el-table__cell) {
  background-color: transparent !important;
  border-bottom: 1px solid #ebeef5 !important;
  color: #213547 !important;
}

.log-table :deep(.el-table__header .el-table__cell) {
  background-color: #fafafa !important;
  border-bottom: 1px solid #ebeef5 !important;
  color: #213547 !important;
}

.log-table :deep(.el-table__empty-block) {
  background-color: #fff !important;
}

.log-table :deep(.el-table__empty-text) {
  color: #909399 !important;
}

.pagination {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
}

.empty-data {
  padding: 40px 0;
  text-align: center;
}
</style> 