<template>
  <el-dialog
    v-model="logDialogVisible"
    :title="`${currentLog.serviceName || '未知服务'} - 发布日志详情`"
    width="90%"
    destroy-on-close
    class="log-dialog"
    @close="handleLogDialogClose"
  >
    <div class="log-dialog-content">
      <el-descriptions :column="3" border class="log-info">
        <el-descriptions-item label="服务名称">{{ currentLog.serviceName }}</el-descriptions-item>
        <el-descriptions-item label="发布分支">{{ currentLog.branch }}</el-descriptions-item>
        <el-descriptions-item label="环境">{{
          getEnvLabel(currentLog.environment)
        }}</el-descriptions-item>
        <el-descriptions-item label="发布状态">
          <el-tag :type="getStatusType(currentLog.status)">{{ currentLog.status }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="开始时间">{{ currentLog.startTime }}</el-descriptions-item>
        <el-descriptions-item label="操作人">{{ currentLog.operator }}</el-descriptions-item>
        <el-descriptions-item label="自动部署">
          <el-tag :type="currentLog.auto_deploy ? 'success' : 'info'" size="small">
            {{ currentLog.auto_deploy ? '是' : '否' }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item v-if="currentLog.products" label="镜像地址">
          <el-tooltip :content="currentLog.products" placement="top">
            <span class="truncate-text">{{ currentLog.products }}</span>
          </el-tooltip>
        </el-descriptions-item>
        <el-descriptions-item v-if="currentLog.message" label="错误信息">
          <el-tooltip :content="currentLog.message" placement="top">
            <span class="error-message">{{ currentLog.message }}</span>
          </el-tooltip>
        </el-descriptions-item>
      </el-descriptions>

      <el-alert
        v-if="taskSteps.some(step => ['timed_out', 'outcome_unknown'].includes(step.status))"
        title="流程已停止推进，但外部任务可能仍在运行。请先核查外部执行状态，避免重复发布。"
        type="warning"
        :closable="false"
        show-icon
      />

      <div v-if="isStreaming" class="connection-status">
        <el-tag type="success" size="small">
          <el-icon><Loading /></el-icon>
          正在读取：{{ activeLogTarget?.label }}
        </el-tag>
      </div>

      <el-tabs v-model="activePanel" class="log-tabs">
        <el-tab-pane label="流程步骤" name="steps">
          <div class="steps-toolbar">
            <span class="steps-hint">展示任务创建时保存的不可变步骤快照与执行结果。</span>
            <el-button
              type="primary"
              link
              :loading="taskDetailsLoading"
              @click="loadTaskDetails(currentLog.taskId)"
            >
              刷新状态
            </el-button>
          </div>

          <el-alert
            v-if="taskDetailsError"
            :title="taskDetailsError"
            type="error"
            :closable="false"
            show-icon
          />
          <el-table
            v-else
            v-loading="taskDetailsLoading"
            :data="taskSteps"
            border
            stripe
            empty-text="该任务没有通用步骤快照（可能是旧版任务）"
          >
            <el-table-column label="#" width="60">
              <template #default="{ row }">{{ row.position + 1 }}</template>
            </el-table-column>
            <el-table-column prop="name" label="步骤" min-width="150" />
            <el-table-column prop="uses" label="执行器" min-width="180" show-overflow-tooltip />
            <el-table-column label="状态" width="120">
              <template #default="{ row }">
                <el-tag :type="stepStatusType(row.status)">{{
                  stepStatusLabel(row.status)
                }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="失败策略" width="100">
              <template #default="{ row }">{{
                row.on_failure === 'continue' ? '继续' : '停止'
              }}</template>
            </el-table-column>
            <el-table-column label="执行时间" min-width="175">
              <template #default="{ row }">{{ stepTimeText(row) }}</template>
            </el-table-column>
            <el-table-column label="消息" min-width="220">
              <template #default="{ row }">
                <div v-if="row.message" class="step-message">{{ row.message }}</div>
                <span v-else class="steps-hint">-</span>
              </template>
            </el-table-column>
            <el-table-column label="日志" width="120" fixed="right">
              <template #default="{ row }">
                <el-button
                  v-if="row.capabilities?.logs === true && canReadTaskLogs"
                  class="step-log-button"
                  type="primary"
                  link
                  @click="viewStepLog(row.step_key)"
                >
                  查看日志
                </el-button>
                <span v-else-if="row.capabilities?.logs === true" class="steps-hint">无权限</span>
                <span v-else class="steps-hint">不支持</span>
              </template>
            </el-table-column>
          </el-table>

          <div v-if="isLegacyTask && logTargets.length > 0" class="legacy-log-actions">
            <el-alert
              title="这是旧版任务，仅通过兼容接口只读历史 Jenkins 日志。"
              type="info"
              :closable="false"
              show-icon
            />
            <template v-if="canReadTaskLogs">
              <el-button
                v-for="target in logTargets"
                :key="targetKey(target)"
                class="legacy-log-button"
                type="primary"
                plain
                @click="viewLogTarget(target)"
              >
                {{ target.label }}
              </el-button>
            </template>
            <span v-else class="steps-hint">无日志读取权限</span>
          </div>
        </el-tab-pane>

        <el-tab-pane v-if="logTargets.length > 0 && canReadTaskLogs" label="步骤日志" name="log">
          <div class="log-toolbar">
            <el-select v-model="selectedTargetKey" aria-label="日志步骤" style="width: 260px">
              <el-option
                v-for="target in logTargets"
                :key="targetKey(target)"
                :label="target.label"
                :value="targetKey(target)"
              />
            </el-select>
            <el-tag v-if="isStreaming" type="success">实时</el-tag>
          </div>

          <el-alert
            v-if="activeLogError"
            :title="activeLogError"
            type="error"
            :closable="false"
            show-icon
            class="log-error"
          >
            <el-button type="primary" size="small" @click="retryActiveLogStream">重试</el-button>
          </el-alert>

          <div ref="logContainer" v-loading="activeLogLoading" class="log-detail-content">
            <div v-if="activeLogLoading && !activeLog" class="loading-indicator">
              <el-icon class="is-loading"><Loading /></el-icon>
              <span>正在获取日志...</span>
            </div>
            <pre v-else-if="activeLog" class="log-text">{{ getDisplayLog(activeLog) }}</pre>
            <el-empty v-else description="暂无日志输出" :image-size="60" />
          </div>
          <div v-if="activeLog" class="log-controls">
            <el-button size="small" type="primary" @click="scrollToBottom">滚动到底部</el-button>
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { Loading } from '@element-plus/icons-vue';
import { taskLogTargetKey, taskLogTargets, useLog, type TaskLogTarget } from '@/composables/useLog';
import type { TaskRecord, TaskStepRecord } from '@/models/deploy';
import type { DeployingService } from '@/types/deploy';
import { getTaskDetail } from '@/services/deploy';
import { useAuthStore } from '@/stores/auth';
import { PERMISSIONS } from '@/types/auth';

interface Props {
  visible: boolean;
  logData?: DeployingService;
}

const props = withDefaults(defineProps<Props>(), {
  visible: false,
  logData: undefined,
});

const emit = defineEmits<{
  'update:visible': [value: boolean];
  close: [];
}>();

const authStore = useAuthStore();
const {
  logDialogVisible,
  currentLog,
  activeLogTarget,
  activeLog,
  activeLogError,
  activeLogLoading,
  isStreaming,
  logContainer,
  canReadTaskLogs,
  getStatusType,
  getEnvLabel,
  scrollToBottom,
  getDisplayLog,
  setCurrentLog,
  openLogTarget,
  pauseLogStream,
  resumeLogStream,
  retryActiveLogStream,
  handleLogDialogClose,
  cleanupLogsAndConnections,
} = useLog();

const activePanel = ref<'steps' | 'log'>('steps');
const selectedTargetKey = ref('');
const taskRecord = ref<TaskRecord | null>(null);
const taskSteps = ref<TaskStepRecord[]>([]);
const logTargets = ref<TaskLogTarget[]>([]);
const taskDetailsLoading = ref(false);
const taskDetailsError = ref('');
let taskDetailsRequestVersion = 0;

const targetKey = taskLogTargetKey;
const isLegacyTask = computed(
  () => taskRecord.value !== null && (taskRecord.value.engine_version ?? 1) < 2
);

const selectedTarget = computed(() =>
  logTargets.value.find(target => targetKey(target) === selectedTargetKey.value)
);

const loadTaskDetails = async (taskId: number) => {
  if (!taskId || !authStore.can(PERMISSIONS.TASKS_READ)) {
    taskDetailsError.value = '没有查看任务详情的权限';
    return;
  }
  const requestVersion = ++taskDetailsRequestVersion;
  taskDetailsLoading.value = true;
  taskDetailsError.value = '';
  try {
    const response = await getTaskDetail(taskId);
    if (requestVersion !== taskDetailsRequestVersion || currentLog.value.taskId !== taskId) return;
    if (response.data.code !== 1 || !response.data.result) {
      throw new Error(response.data.error || response.data.message || '获取任务详情失败');
    }
    taskRecord.value = response.data.result;
    taskSteps.value = [...(taskRecord.value.steps || [])].sort(
      (left, right) => left.position - right.position
    );
    logTargets.value = taskLogTargets(taskRecord.value);

    if (!logTargets.value.some(target => targetKey(target) === selectedTargetKey.value)) {
      selectedTargetKey.value = logTargets.value[0] ? targetKey(logTargets.value[0]) : '';
      pauseLogStream();
      if (activePanel.value === 'log' && !selectedTargetKey.value) activePanel.value = 'steps';
    }
  } catch (error) {
    if (requestVersion !== taskDetailsRequestVersion || currentLog.value.taskId !== taskId) return;
    taskRecord.value = null;
    taskSteps.value = [];
    logTargets.value = [];
    selectedTargetKey.value = '';
    pauseLogStream();
    taskDetailsError.value = error instanceof Error ? error.message : '获取任务详情失败';
  } finally {
    if (requestVersion === taskDetailsRequestVersion) taskDetailsLoading.value = false;
  }
};

const viewLogTarget = (target: TaskLogTarget) => {
  selectedTargetKey.value = targetKey(target);
  activePanel.value = 'log';
  openLogTarget(target);
};

const viewStepLog = (stepKey: string) => {
  const target = logTargets.value.find(
    candidate => candidate.kind === 'step' && candidate.stepKey === stepKey
  );
  if (target) viewLogTarget(target);
};

const stepStatusLabel = (status: string) =>
  ({
    pending: '等待中',
    running: '执行中',
    succeeded: '成功',
    failed: '失败',
    timed_out: '执行超时',
    outcome_unknown: '执行结果待核查',
    skipped: '已跳过',
    cancelled: '已取消',
  })[status] || status;

const stepStatusType = (status: string) => {
  if (status === 'succeeded') return 'success';
  if (status === 'failed') return 'danger';
  if (status === 'running') return 'primary';
  if (['cancelled', 'skipped', 'timed_out', 'outcome_unknown'].includes(status)) return 'warning';
  return 'info';
};

const formatDateTime = (value?: string | null) =>
  value ? new Date(value).toLocaleString('zh-CN') : '';

const stepTimeText = (step: TaskStepRecord) => {
  const started = formatDateTime(step.started_at);
  const finished = formatDateTime(step.finished_at);
  if (started && finished) return `${started} → ${finished}`;
  return started || finished || '-';
};

watch(
  () => props.visible,
  visible => {
    if (!visible) {
      taskDetailsRequestVersion += 1;
      taskDetailsLoading.value = false;
      logDialogVisible.value = false;
      return;
    }
    logDialogVisible.value = true;
    if (!props.logData) return;

    const taskChanged = currentLog.value.taskId !== props.logData.taskId;
    setCurrentLog(props.logData);
    if (taskChanged) {
      taskRecord.value = null;
      taskSteps.value = [];
      logTargets.value = [];
      selectedTargetKey.value = '';
    }
    activePanel.value = 'steps';
    void loadTaskDetails(props.logData.taskId);
  },
  { immediate: true }
);

watch(logDialogVisible, visible => {
  emit('update:visible', visible);
  if (!visible) {
    handleLogDialogClose();
    emit('close');
  }
});

watch(activePanel, panel => {
  if (panel !== 'log') {
    pauseLogStream();
    return;
  }
  if (selectedTarget.value) openLogTarget(selectedTarget.value);
});

watch(selectedTargetKey, () => {
  if (activePanel.value === 'log' && selectedTarget.value) openLogTarget(selectedTarget.value);
});

watch(canReadTaskLogs, allowed => {
  if (!allowed) {
    pauseLogStream();
    if (activePanel.value === 'log') activePanel.value = 'steps';
  } else if (logDialogVisible.value && activePanel.value === 'log') {
    resumeLogStream();
  }
});

onUnmounted(() => {
  taskDetailsRequestVersion += 1;
  cleanupLogsAndConnections();
});
</script>

<style scoped>
.log-dialog-content {
  max-height: 80vh;
  overflow-y: auto;
}

.log-info,
.connection-status,
.steps-toolbar,
.log-toolbar,
.log-error {
  margin-bottom: 16px;
}

.steps-toolbar,
.log-toolbar,
.log-controls {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.steps-hint {
  color: #909399;
  font-size: 12px;
}

.step-message,
.error-message {
  white-space: pre-wrap;
  word-break: break-word;
}

.legacy-log-actions {
  margin-top: 16px;
}

.legacy-log-button {
  margin-top: 12px;
  margin-right: 8px;
}

.log-detail-content {
  height: 500px;
  overflow: auto;
  padding: 16px;
  border: 1px solid #e4e7ed;
  border-radius: 4px;
  background: #1e1e1e;
}

.log-text {
  margin: 0;
  color: #d4d4d4;
  font-family: Monaco, Menlo, Consolas, monospace;
  font-size: 13px;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.loading-indicator {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: #c0c4cc;
}

.log-controls {
  justify-content: flex-end;
  margin-top: 10px;
}

.truncate-text {
  display: inline-block;
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
</style>
