<template>
  <div class="log-content">
    <div class="section-title">
      <el-icon><Document /></el-icon>
      <span>历史发布记录</span>
      <el-button
        type="primary"
        link
        :loading="logLoading"
        @click="handleSearch"
      >
        <el-icon><Refresh /></el-icon>
        刷新
      </el-button>
    </div>
    
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
          <el-select v-model="logFilter.environment" placeholder="请选择环境" clearable style="width: 120px">
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
    
    <div class="table-container">
      <el-table 
        :data="logList" 
        style="width: 100%" 
        v-loading="logLoading" 
        :max-height="500" 
        border
        stripe
        empty-text="暂无日志数据"
      >
        <el-table-column prop="serviceName" label="服务名称" min-width="120" />
        <el-table-column prop="branch" label="发布分支" min-width="80" />
        <el-table-column prop="environment" label="环境" width="100">
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
        <el-table-column prop="deployTime" label="发布时间" width="180" show-overflow-tooltip />
        <el-table-column prop="operator" label="操作人" width="100" />
        <el-table-column prop="auto_deploy" label="自动部署" width="80">
          <template #default="{ row }">
            <el-tag :type="row.auto_deploy ? 'success' : 'info'" size="small">
              {{ row.auto_deploy ? '是' : '否' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleViewLogDetail(row)">
              查看日志
            </el-button>
          </template>
        </el-table-column>
      </el-table>
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
      <div class="jump-to-page">
        <span>跳转到第</span>
        <el-input
          v-model="jumpPageInput"
          type="text"
          placeholder="页码"
          style="width: 60px; margin: 0 5px;"
          @keyup.enter="handleJumpToPage"
          @blur="handleJumpToPage"
        />
        <span>页</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, watch, ref } from 'vue'
import { Document, Refresh } from '@element-plus/icons-vue'
import { useLog } from '@/composables/useLog'
import { useDeploy } from '@/composables/useDeploy'
import { ElMessage } from 'element-plus'

// 定义props
interface Props {
  isActive?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  isActive: false
})

// 跳转页面相关
const jumpPageInput = ref('')

const {
  // 响应式数据
  logFilter,
  logList,
  currentPage,
  pageSize,
  total,
  logLoading,
  
  // 工具函数
  getStatusType,
  getEnvLabel,
  getEnvType,
  
  // 事件处理函数
  handleSearch,
  handleResetLogFilter,
  viewLogDetail,
  handleSizeChange,
  handleCurrentChange
} = useLog()

const {
  // 响应式数据
  availableServices,
  
  // 函数
  loadAvailableServices
} = useDeploy()

// 定义事件
const emit = defineEmits<{
  viewLogDetail: [logItem: any]
}>()

// 重写viewLogDetail方法，触发事件
const handleViewLogDetail = (row: any) => {
  console.log('LogQuery: 点击查看日志按钮', row)
  // 只触发事件，让父组件处理对话框显示
  emit('viewLogDetail', row)
  console.log('LogQuery: 已触发viewLogDetail事件')
}

// 处理跳转到指定页面
const handleJumpToPage = () => {
  // 如果输入框为空，不执行跳转
  if (!jumpPageInput.value || jumpPageInput.value.trim() === '') {
    return
  }
  
  const page = parseInt(jumpPageInput.value.trim())
  const maxPage = Math.ceil(total.value / pageSize.value)
  
  if (isNaN(page) || page < 1 || page > maxPage) {
    ElMessage.warning(`请输入1-${maxPage}之间的页码`)
    jumpPageInput.value = '' // 清空无效输入
    return
  }
  
  // 设置当前页并触发数据请求
  currentPage.value = page
  jumpPageInput.value = '' // 清空输入框
  
  // 确保触发数据请求
  handleSearch()
}

// 监听标签页激活状态
watch(() => props.isActive, async (isActive) => {
  if (isActive) {
    console.log('LogQuery: 日志页激活，加载服务列表和历史记录')
    // 先加载服务列表
    await loadAvailableServices()
    // 然后查询日志
    handleSearch()
  }
}, { immediate: true })

// 组件挂载时，如果已经是激活状态则加载数据
onMounted(async () => {
  if (props.isActive) {
    console.log('LogQuery: 组件挂载且日志页激活，加载服务列表和历史记录')
    // 先加载服务列表
    await loadAvailableServices()
    // 然后查询日志
    handleSearch()
  }
})
</script>

<style scoped>
.log-content {
  padding: 20px;
}

.section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 16px;
  font-size: 16px;
  font-weight: 500;
  color: #303133;
}

.log-filter {
  margin-bottom: 20px;
  padding: 20px;
  background: #f8f9fa;
  border-radius: 8px;
}

.table-container {
  background: #fff;
  border-radius: 8px;
  overflow: hidden;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
  margin-bottom: 16px;
  max-height: 600px !important;
}

.pagination {
  padding: 20px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 15px;
}

.jump-to-page {
  display: flex;
  align-items: center;
  font-size: 14px;
  color: #606266;
  gap: 5px;
}

:deep(.el-table) {
  border-radius: 8px;
}

:deep(.el-table__header) {
  background-color: #f5f7fa;
}

:deep(.el-table__row:hover) {
  background-color: #f5f7fa;
}

:deep(.el-table__body-wrapper) {
  max-height: 500px !important;
}
</style> 