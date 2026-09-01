<template>
  <div class="deploy-tool">
    <!-- 环境选择区域 -->
    <div class="env-selector">
      <el-radio-group v-model="deployForm.environment" size="large" @change="handleEnvChange">
        <el-radio-button :value="'dev'">开发环境</el-radio-button>
        <el-radio-button :value="'test'">测试环境</el-radio-button>
        <el-radio-button :value="'moni'">模拟环境</el-radio-button>
      </el-radio-group>
      <div v-if="deployForm.environment === 'moni'" class="global-branch-input">
        <span class="branch-label">统一发布分支：</span>
        <div class="branch-input-wrapper">
          <el-input
            v-model="globalBranchSuffix"
            placeholder="请输入分支后缀"
            style="width: 240px"
            @input="handleGlobalBranchChange"
          >
            <template #prefix>
              <span class="branch-prefix">release_</span>
            </template>
          </el-input>
        </div>
        <!-- 调试信息 -->
        <div v-if="selectedServices.length > 0" class="debug-info">
          <small style="color: #909399; margin-left: 10px;">
            当前服务数量: {{ selectedServices.length }}，全局分支: release_{{ globalBranchSuffix }}
          </small>
        </div>
        <!-- 服务列表调试信息 -->
        <div class="debug-info">
          <small style="color: #909399; margin-left: 10px;">
            可用服务数量: {{ availableServices.length }}
          </small>
        </div>
      </div>
    </div>

    <!-- 服务选择表格 -->
    <div v-if="deployForm.environment" class="service-table">
      <div class="table-container">
        <el-table :data="selectedServices" style="width: 100%" border :max-height="400" stripe>
          <el-table-column label="服务名称" min-width="200">
            <template #default="{ row, $index }">
              <el-select
                v-model="row.serviceName"
                filterable
                placeholder="请选择服务"
                style="width: 100%"
                @change="(val: string) => handleServiceSelect(val, $index)"
              >
                <el-option
                  v-for="service in availableServices"
                  :key="service.name"
                  :label="service.name"
                  :value="service.name"
                />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column prop="branch" label="发布分支" min-width="200">
            <template #default="{ row }">
              <template v-if="deployForm.environment === 'moni'">
                <div class="branch-input-wrapper">
                  <el-input
                    v-model="row.branchSuffix"
                    placeholder="请输入分支后缀"
                    style="width: 240px"
                    @input="(val: string) => handleBranchSuffixChange(val, row)"
                  >
                    <template #prefix>
                      <span class="branch-prefix">release_</span>
                    </template>
                  </el-input>
                </div>
              </template>
              <template v-else>
                <span class="branch-text">{{ deployForm.environment }}</span>
              </template>
            </template>
          </el-table-column>
          <el-table-column prop="status" label="发布状态" width="120">
            <template #default="{ row }">
              <el-tag :type="getStatusType(row.status)">
                {{ row.status }}
                <el-icon v-if="row.status === '发布中'" class="is-loading"><Loading /></el-icon>
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="lastUpdateTime" label="最后更新" width="160">
            <template #default="{ row }">
              <span v-if="row.lastUpdateTime">{{ row.lastUpdateTime }}</span>
              <span v-else class="text-muted">-</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="280" fixed="right">
            <template #default="{ row, $index }">
              <el-button-group>
                <el-button
                  type="primary"
                  :disabled="!row.serviceName || !row.branch || isServiceProcessing(row.status)"
                  @click="handleDeploySingle(row, $index)"
                >
                  编译并发布
                </el-button>
                <el-button
                  type="warning"
                  :disabled="!row.serviceName || !row.branch || isServiceProcessing(row.status)"
                  @click="handleRedeploySingle(row, $index)"
                >
                  仅重发
                </el-button>
                <el-button
                  type="danger"
                  :disabled="isServiceProcessing(row.status)"
                  @click="handleRemoveService($index)"
                >
                  删除
                </el-button>
              </el-button-group>
            </template>
          </el-table-column>
        </el-table>
      </div>

      <!-- 添加服务按钮 -->
      <div class="table-actions">
        <el-button 
          type="primary" 
          plain 
          class="add-service-btn"
          @click="handleAddService"
        >
          <el-icon><Plus /></el-icon>
          添加服务
        </el-button>
      </div>

      <!-- 批量操作按钮 -->
      <div class="batch-actions">
        <el-button 
          type="primary" 
          size="large" 
          :disabled="!hasDeployableServices"
          @click="handleBatchDeploy"
        >
          <el-icon><Upload /></el-icon>
          一键编译并发布
        </el-button>
        <el-button 
          type="warning" 
          size="large"
          :disabled="!hasDeployableServices"
          @click="handleBatchRedeploy"
        >
          <el-icon><RefreshRight /></el-icon>
          一键重发
        </el-button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { Loading, Plus, Upload, RefreshRight } from '@element-plus/icons-vue'
import { useDeploy } from '@/composables/useDeploy'

// 定义props
interface Props {
  isActive?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  isActive: false
})

const {
  // 响应式数据
  deployForm,
  globalBranchSuffix,
  availableServices,
  selectedServices,
  
  // 计算属性
  hasDeployableServices,
  
  // 工具函数
  isServiceProcessing,
  getStatusType,
  
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
  loadAvailableServices
} = useDeploy()

// 监听标签页激活状态
watch(() => props.isActive, (isActive) => {
  if (isActive && availableServices.value.length === 0) {
    console.log('DeployTool: 工具页激活，加载服务列表')
    loadAvailableServices()
  }
}, { immediate: true })

// 组件挂载时，如果已经是激活状态则加载服务列表
onMounted(() => {
  if (props.isActive && availableServices.value.length === 0) {
    console.log('DeployTool: 组件挂载且工具页激活，加载服务列表')
    loadAvailableServices()
  }
})
</script>

<style scoped>
.deploy-tool {
  padding: 20px;
}

.env-selector {
  margin-bottom: 20px;
  padding: 20px;
  background: #f8f9fa;
  border-radius: 8px;
  display: flex;
  align-items: center;
  gap: 20px;
}

.global-branch-input {
  display: flex;
  align-items: center;
  gap: 10px;
}

.branch-label {
  font-weight: 500;
  color: #606266;
}

.branch-input-wrapper {
  display: flex;
  align-items: center;
}

.branch-prefix {
  color: #909399;
  font-size: 14px;
}

.branch-text {
  color: #409EFF;
  font-weight: 500;
}

.service-table {
  margin-top: 20px;
}

.table-container {
  background: #fff;
  border-radius: 8px;
  overflow: hidden;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
  margin-bottom: 16px;
  max-height: 400px !important;
}

.table-actions {
  margin-top: 16px;
  display: flex;
  justify-content: flex-start;
}

.add-service-btn {
  width: 100% !important;
  height: 40px !important;
  border: 2px dashed #dcdfe6 !important;
  border-radius: 8px !important;
  color: #c0c4cc !important;
  font-size: 14px !important;
  font-weight: 500 !important;
  background: transparent !important;
  transition: all 0.3s ease !important;
}

.add-service-btn:hover {
  border-color: #909399 !important;
  color: #909399 !important;
  background: rgba(220, 223, 230, 0.1) !important;
  transform: translateY(-1px) !important;
  box-shadow: 0 2px 8px rgba(220, 223, 230, 0.2) !important;
}

.add-service-btn:active {
  transform: translateY(0) !important;
  box-shadow: 0 1px 4px rgba(220, 223, 230, 0.2) !important;
}

.add-service-btn .el-icon {
  margin-right: 8px !important;
  font-size: 16px !important;
}

.batch-actions {
  margin-top: 20px;
  display: flex;
  gap: 16px;
  justify-content: center;
}

.text-muted {
  color: #909399;
}

.is-loading {
  animation: rotating 2s linear infinite;
}

@keyframes rotating {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
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

:deep(.el-table__fixed-right) {
  box-shadow: -2px 0 8px rgba(0, 0, 0, 0.1);
}

/* 确保表格高度限制正确 */
:deep(.el-table) {
  max-height: 400px !important;
  overflow-y: auto !important;
}

:deep(.el-table__body-wrapper) {
  max-height: 400px !important;
  overflow-y: auto !important;
}
</style> 