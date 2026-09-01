<template>
  <div class="deploying-list">
    <div class="section-title">
      <el-icon><Loading /></el-icon>
      <span>正在发布的服务</span>
      <el-button type="primary" link :loading="deployingLoading" @click="refreshDeployingList">
        <el-icon><Refresh /></el-icon>
        刷新
      </el-button>
    </div>
    <div class="table-container">
      <el-table
        :data="deployingList"
        style="width: 100%"
        border
        v-loading="deployingLoading"
        :max-height="400"
        stripe
        empty-text="暂无正在发布的服务"
      >
        <el-table-column prop="serviceName" label="服务名称" min-width="150" />
        <el-table-column prop="branch" label="发布分支" min-width="150" />
        <el-table-column prop="environment" label="环境" width="100">
          <template #default="{ row }">
            <el-tag :type="getEnvType(row.environment)">
              {{ getEnvLabel(row.environment) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="status" label="状态" width="120">
          <template #default="{ row }">
            <el-tag :type="getStatusType(row.status)">
              {{ row.status }}
              <el-icon v-if="row.status === '发布中'" class="is-loading"><Loading /></el-icon>
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="progress" label="进度" width="200">
          <template #default="{ row }">
            <el-progress
              :percentage="row.progress"
              :status="getProgressStatus(row.status)"
              :stroke-width="15"
            />
          </template>
        </el-table-column>
        <el-table-column prop="startTime" label="开始时间" width="160" />
        <el-table-column prop="operator" label="操作人" width="100" />
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button-group>
              <el-button
                type="primary"
                link
                :disabled="row.status !== '发布中'"
                @click="handleCancelDeploy(row)"
              >
                取消发布
              </el-button>
              <el-button type="primary" link @click="handleViewLog(row)"> 查询日志 </el-button>
            </el-button-group>
          </template>
        </el-table-column>
      </el-table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, watch } from 'vue';
import { Loading, Refresh } from '@element-plus/icons-vue';
import { useDeploy } from '@/composables/useDeploy';

// 定义props
interface Props {
  isActive?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  isActive: false,
});

const {
  // 响应式数据
  deployingList,
  deployingLoading,

  // 工具函数
  getStatusType,
  getProgressStatus,
  getEnvLabel,
  getEnvType,

  // 事件处理函数
  handleCancelDeploy,
  refreshDeployingList,
} = useDeploy();

// 定义事件
const emit = defineEmits<{
  viewLog: [service: any];
}>();

// 定时刷新
let refreshTimer: ReturnType<typeof setInterval> | null = null;

const startAutoRefresh = () => {
  refreshTimer = setInterval(() => {
    refreshDeployingList();
  }, 10000); // 每10秒刷新一次
};

const stopAutoRefresh = () => {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
};

// 暂停自动刷新
const pauseAutoRefresh = () => {
  stopAutoRefresh();
};

// 恢复自动刷新
const resumeAutoRefresh = () => {
  if (props.isActive) {
    startAutoRefresh();
  }
};

// 查看日志
const handleViewLog = (service: any) => {
  // 暂停自动刷新，避免影响日志查询
  pauseAutoRefresh();
  emit('viewLog', service);
};

// 监听标签页激活状态
watch(
  () => props.isActive,
  isActive => {
    if (isActive) {
      console.log('DeployingList: 工具页激活，加载正在发布的服务列表');
      refreshDeployingList();
      startAutoRefresh();
    } else {
      console.log('DeployingList: 工具页非激活，停止自动刷新');
      stopAutoRefresh();
    }
  },
  { immediate: false }
);

// 暴露方法给父组件
defineExpose({
  pauseAutoRefresh,
  resumeAutoRefresh,
});

// 组件挂载时，如果已经是激活状态则加载数据
onMounted(() => {
  if (props.isActive) {
    console.log('DeployingList: 组件挂载且工具页激活，加载正在发布的服务列表');
    refreshDeployingList();
    startAutoRefresh();
  }
});

// 组件卸载时停止自动刷新
onUnmounted(() => {
  stopAutoRefresh();
});
</script>

<style scoped>
.deploying-list {
  margin-top: 30px;
  padding: 0 20px 20px 20px;
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

.table-container {
  background: #fff;
  border-radius: 8px;
  overflow: hidden;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
  max-height: 400px !important;
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
