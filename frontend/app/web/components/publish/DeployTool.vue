<template>
  <div class="release-composer">
    <el-alert
      title="发布以应用环境配置（AppConfig）为目标；提交前会校验工作流与执行器可用性。"
      type="info"
      :closable="false"
      show-icon
      class="composer-alert"
    />

    <el-form label-position="top" class="composer-form" @submit.prevent>
      <div class="form-grid">
        <el-form-item label="发布环境" required>
          <el-select
            v-model="environment"
            :disabled="locked"
            placeholder="请选择环境"
            style="width: 100%"
            @change="handleEnvironmentChange"
          >
            <el-option
              v-for="item in enabledEnvironments"
              :key="item.code"
              :label="labelForEnvironment(item.code)"
              :value="item.code"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="Git 分支或 ref" required>
          <el-input
            v-model="releaseRef"
            :disabled="locked"
            placeholder="例如 main、release/2026-09 或完整 tag"
            @input="invalidateDraft"
          />
        </el-form-item>
        <el-form-item label="筛选目标">
          <el-input
            v-model="keyword"
            :disabled="locked || !environment"
            clearable
            placeholder="应用名称或中文名"
            @keyup.enter="handleSearchTargets"
            @clear="handleSearchTargets"
          >
            <template #append>
              <el-button :disabled="locked || !environment" @click="handleSearchTargets">
                <el-icon><Search /></el-icon>
              </el-button>
            </template>
          </el-input>
        </el-form-item>
      </div>
    </el-form>

    <section class="composer-section">
      <div class="section-heading">
        <div>
          <h3>发布参数</h3>
          <p>参数以 key/value 字符串传给工作流；空 key 会被忽略，重复 key 不允许提交。</p>
        </div>
        <el-button :disabled="locked" @click="addInputRow">
          <el-icon><Plus /></el-icon>
          新增参数
        </el-button>
      </div>
      <el-table :data="inputRows" border empty-text="本次发布没有自定义参数">
        <el-table-column label="Key" min-width="220">
          <template #default="{ row }">
            <el-input
              v-model="row.key"
              :disabled="locked"
              placeholder="例如 image_registry"
              @input="invalidateDraft"
            />
          </template>
        </el-table-column>
        <el-table-column label="Value" min-width="280">
          <template #default="{ row }">
            <el-input
              v-model="row.value"
              :disabled="locked"
              placeholder="参数值"
              @input="invalidateDraft"
            />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ $index }">
            <el-button type="danger" link :disabled="locked" @click="removeInputRow($index)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </section>

    <section class="composer-section">
      <div class="section-heading">
        <div>
          <h3>发布目标</h3>
          <p>
            已选择 {{ selectedCount }} /
            {{ MAX_RELEASE_TARGETS }} 个目标；切换环境会清空选择，避免跨环境误发。
          </p>
        </div>
        <el-button
          :loading="targetsLoading"
          :disabled="locked || !environment"
          @click="handleReloadTargets"
        >
          <el-icon><Refresh /></el-icon>
          刷新
        </el-button>
      </div>
      <el-table
        ref="targetTableRef"
        v-loading="targetsLoading"
        :data="targets"
        :row-key="targetRowKey"
        border
        empty-text="当前环境暂无发布目标"
        @selection-change="handleSelectionChange"
      >
        <el-table-column type="selection" width="48" :selectable="isTargetSelectable" />
        <el-table-column prop="config_id" label="Config ID" width="105" />
        <el-table-column prop="app_name" label="应用" min-width="180">
          <template #default="{ row }">
            <div>{{ row.app_name }}</div>
            <small class="muted">{{ row.app_name_cn || '-' }}</small>
          </template>
        </el-table-column>
        <el-table-column prop="env" label="环境" width="130">
          <template #default="{ row }">{{ labelForEnvironment(row.env) }}</template>
        </el-table-column>
        <el-table-column label="工作流" min-width="210">
          <template #default="{ row }">
            <template v-if="row.workflow_version_id">
              <div>版本 #{{ row.workflow_version_id }}</div>
              <small class="muted">{{ stepSummary(row.steps) }}</small>
            </template>
            <span v-else class="muted">未配置</span>
          </template>
        </el-table-column>
        <el-table-column label="可发布状态" min-width="180">
          <template #default="{ row }">
            <el-tag v-if="row.available" type="success">可发布</el-tag>
            <el-tooltip v-else :content="releaseReasonLabel(row.unavailable_code)" placement="top">
              <el-tag type="danger">{{ releaseReasonLabel(row.unavailable_code) }}</el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
      </el-table>
      <div class="pagination">
        <el-pagination
          v-model:current-page="pageNum"
          v-model:page-size="pageSize"
          :disabled="locked"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handlePageSizeChange"
          @current-change="handlePageChange"
        />
      </div>
    </section>

    <section v-if="preflightResult" class="composer-section result-section">
      <div class="section-heading">
        <div>
          <h3>预检结果</h3>
          <p>
            总计 {{ preflightResult.total_count }}，可发布 {{ preflightResult.ready_count }}，失败
            {{ preflightResult.failure_count }}。创建时服务端仍会在同一事务内重新校验。
          </p>
        </div>
        <el-button v-if="phase === 'ready'" @click="cancelStagedSubmission">返回修改</el-button>
      </div>
      <el-table :data="preflightResult.items" border>
        <el-table-column prop="request_index" label="#" width="60" />
        <el-table-column prop="config_id" label="Config ID" width="105" />
        <el-table-column label="应用" min-width="160">
          <template #default="{ row }">{{ targetName(row.config_id) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="row.ready ? 'success' : 'danger'">
              {{ row.ready ? '通过' : '未通过' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="工作流" min-width="210">
          <template #default="{ row }">
            <span v-if="row.workflow_version_id">#{{ row.workflow_version_id }}</span>
            <span v-else>-</span>
            <small v-if="row.steps?.length" class="inline-detail">{{
              stepSummary(row.steps)
            }}</small>
          </template>
        </el-table-column>
        <el-table-column label="说明" min-width="220">
          <template #default="{ row }">
            {{ row.error_message || (row.ready ? '校验通过' : releaseReasonLabel(row.error_code)) }}
          </template>
        </el-table-column>
      </el-table>
    </section>

    <el-alert
      v-if="phase === 'uncertain'"
      title="提交结果不明确"
      :description="uncertainDescription"
      type="warning"
      :closable="false"
      show-icon
      class="composer-alert"
    />

    <section v-if="receipt" class="composer-section result-section">
      <div class="section-heading">
        <div>
          <h3>创建结果 · 凭据 #{{ receipt.record_id }}</h3>
          <p>
            总计 {{ receipt.total_count }}，成功 {{ receipt.success_count }}，失败
            {{ receipt.failure_count }}。以下为完整逐项结果。
          </p>
        </div>
        <el-button
          v-if="receipt.failure_count > 0"
          type="warning"
          plain
          @click="startNewReleaseFromFailures"
        >
          将失败项作为新发布
        </el-button>
      </div>
      <el-table :data="receipt.items" border>
        <el-table-column prop="request_index" label="#" width="60" />
        <el-table-column prop="config_id" label="Config ID" width="105" />
        <el-table-column label="应用" min-width="160">
          <template #default="{ row }">{{ targetName(row.config_id) }}</template>
        </el-table-column>
        <el-table-column label="结果" width="100">
          <template #default="{ row }">
            <el-tag :type="row.success ? 'success' : 'danger'">
              {{ row.success ? '已创建' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="任务" min-width="120">
          <template #default="{ row }">
            <el-button
              v-if="row.task_id && canViewDetails"
              type="primary"
              link
              @click="openTask(row.task_id, row.config_id)"
            >
              #{{ row.task_id }}
            </el-button>
            <span v-else>{{ row.task_id ? `#${row.task_id}` : '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="workflow_version_id" label="工作流版本" min-width="120" />
        <el-table-column label="说明" min-width="220">
          <template #default="{ row }">
            {{ row.success ? '任务已持久化' : releaseReasonLabel(row.error_code) }}
          </template>
        </el-table-column>
      </el-table>
    </section>

    <div class="submit-actions">
      <el-button
        v-if="phase !== 'uncertain' && !retrying"
        type="primary"
        size="large"
        :loading="phase === 'preflighting' || phase === 'submitting'"
        :disabled="!canPreflight || phase === 'ready'"
        @click="preflightAndSubmit"
      >
        预检并创建发布
      </el-button>
      <el-button
        v-else
        type="warning"
        size="large"
        :loading="retrying"
        :disabled="retryDelaySeconds > 0"
        @click="retrySubmission"
      >
        {{
          retryDelaySeconds > 0 ? `${retryDelaySeconds} 秒后可用同一请求重试` : '使用同一请求重试'
        }}
      </el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import axios from 'axios';
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import type { TableInstance } from 'element-plus';
import { ElMessage, ElMessageBox } from 'element-plus';
import { Plus, Refresh, Search } from '@element-plus/icons-vue';
import { useEnvironments } from '@/composables/useEnvironments';
import {
  FrozenReleaseActorError,
  MAX_RELEASE_TARGETS,
  releaseReasonLabel,
  useReleaseComposer,
} from '@/composables/useReleaseComposer';
import { useFrozenReleaseNavigationGuard } from '@/composables/useFrozenReleaseNavigationGuard';
import { getReleaseApiErrorMessage, getReleaseRetryAfterSeconds } from '@/services/releases';
import type { ReleaseStepSummary, ReleaseTarget } from '@/models/release';
import { useAuthStore } from '@/stores/auth';

interface Props {
  isActive?: boolean;
  canViewDetails?: boolean;
}

interface ReleaseTaskLink {
  taskId: number;
  appName: string;
  ref: string;
  environment: string;
}

const props = withDefaults(defineProps<Props>(), {
  isActive: false,
  canViewDetails: false,
});

const emit = defineEmits<{
  viewTask: [task: ReleaseTaskLink];
}>();

const authStore = useAuthStore();
const { enabledEnvironments, loadEnvironments, labelForEnvironment } = useEnvironments();
const {
  environment,
  keyword,
  releaseRef,
  inputRows,
  pageNum,
  pageSize,
  targets,
  total,
  selectedTargets,
  selectedCount,
  targetsLoading,
  phase,
  preflightResult,
  receipt,
  frozenSubmission,
  locked,
  canPreflight,
  invalidateDraft,
  setEnvironment,
  loadTargets,
  searchTargets,
  changePage,
  changePageSize,
  replaceCurrentPageSelection,
  addInputRow,
  removeInputRow,
  runPreflight,
  cancelStagedSubmission,
  submitFrozen,
  retryFrozenSubmission,
  selectFailedReceiptItems,
} = useReleaseComposer({ getActorID: () => authStore.user?.id || null });

useFrozenReleaseNavigationGuard(phase, frozenSubmission, () => {
  ElMessage.warning('发布结果尚未确定，请先使用已冻结的请求完成重试');
});

const targetTableRef = ref<TableInstance>();
const restoringSelection = ref(false);
const retrying = ref(false);
const retryDelaySeconds = ref(0);
const defaultUncertainDescription =
  '任务可能已经创建。为避免重复发布，只能使用已冻结的请求体、同一个幂等键和原登录账号重试。';
const uncertainDescription = ref(defaultUncertainDescription);
let initialized = false;
let retryDelayTimer: ReturnType<typeof setInterval> | null = null;

const targetRowKey = (row: ReleaseTarget) =>
  row.config_id ? `config:${row.config_id}` : `app:${row.app_id}`;
const isTargetSelectable = (row: ReleaseTarget) =>
  row.available &&
  Boolean(row.config_id) &&
  !locked.value &&
  (Boolean(row.config_id && selectedTargets.value.has(row.config_id)) ||
    selectedCount.value < MAX_RELEASE_TARGETS);

const stepSummary = (steps: ReleaseStepSummary[] = []) => {
  if (steps.length === 0) return '无步骤';
  return steps.map(step => step.name || step.key || step.uses).join(' → ');
};

const targetName = (configId: number) => {
  const target = selectedTargets.value.get(configId);
  if (!target) return `Config #${configId}`;
  return target.app_name_cn ? `${target.app_name}（${target.app_name_cn}）` : target.app_name;
};

const restorePageSelection = async () => {
  const table = targetTableRef.value;
  if (!table) return;
  const refreshed = new Map(selectedTargets.value);
  for (const target of targets.value) {
    if (!target.config_id || !refreshed.has(target.config_id)) continue;
    if (target.available) refreshed.set(target.config_id, target);
    else refreshed.delete(target.config_id);
  }
  selectedTargets.value = refreshed;
  restoringSelection.value = true;
  table.clearSelection();
  for (const target of targets.value) {
    if (target.config_id && refreshed.has(target.config_id) && target.available) {
      table.toggleRowSelection(target, true);
    }
  }
  await nextTick();
  restoringSelection.value = false;
};

watch(targets, () => nextTick(restorePageSelection));

const handleSelectionChange = (rows: ReleaseTarget[]) => {
  if (restoringSelection.value) return;
  replaceCurrentPageSelection(rows);
  void nextTick(restorePageSelection);
};

const handleEnvironmentChange = async (value: string) => {
  try {
    await setEnvironment(value);
  } catch (error) {
    ElMessage.error(getReleaseApiErrorMessage(error, '获取发布目标失败'));
  }
};

const runTargetRequest = async (request: () => Promise<void>) => {
  try {
    await request();
  } catch (error) {
    ElMessage.error(getReleaseApiErrorMessage(error, '获取发布目标失败'));
  }
};

const handleSearchTargets = () => runTargetRequest(searchTargets);
const handleReloadTargets = () => runTargetRequest(loadTargets);
const handlePageChange = (value: number) => runTargetRequest(() => changePage(value));
const handlePageSizeChange = (value: number) => runTargetRequest(() => changePageSize(value));

const setRetryDelay = (error: unknown) => {
  if (retryDelayTimer) clearInterval(retryDelayTimer);
  retryDelayTimer = null;
  retryDelaySeconds.value = getReleaseRetryAfterSeconds(error);
  if (retryDelaySeconds.value === 0) return;
  retryDelayTimer = setInterval(() => {
    retryDelaySeconds.value = Math.max(0, retryDelaySeconds.value - 1);
    if (retryDelaySeconds.value === 0 && retryDelayTimer) {
      clearInterval(retryDelayTimer);
      retryDelayTimer = null;
    }
  }, 1000);
};

const setUncertainDescription = (error: unknown) => {
  if (error instanceof FrozenReleaseActorError) {
    uncertainDescription.value = error.message;
    return;
  }
  if (axios.isAxiosError(error) && error.response?.status === 401) {
    uncertainDescription.value =
      '会话已失效，但冻结请求仍保留。请在新标签页使用原账号重新登录，再回到本页重试；系统会先核对登录主体。';
    return;
  }
  if (axios.isAxiosError(error) && error.response?.status === 403) {
    uncertainDescription.value =
      '当前权限不足，但冻结请求仍保留。请等待原账号恢复发布权限后再重试，不能切换账号。';
    return;
  }
  if (axios.isAxiosError(error) && error.response?.status === 429) {
    uncertainDescription.value =
      '请求受到流量控制，冻结请求仍保留。请等待 Retry-After 倒计时结束后使用原账号重试。';
    return;
  }
  uncertainDescription.value = defaultUncertainDescription;
};

const preflightAndSubmit = async () => {
  try {
    const result = await runPreflight();
    if (result.ready_count === 0) {
      ElMessage.warning('预检未发现可发布目标，已禁止创建；请返回修改');
      return;
    }
    try {
      await ElMessageBox.confirm(
        `预检完成：将仅为 ${result.ready_count} 个通过目标创建任务，${result.failure_count} 个未通过目标不会提交。确认继续吗？`,
        '确认创建发布任务',
        {
          type: result.failure_count > 0 ? 'warning' : 'success',
          confirmButtonText: '确认创建',
          cancelButtonText: '返回修改',
          closeOnClickModal: false,
        }
      );
    } catch (error) {
      if (error === 'cancel' || error === 'close') {
        cancelStagedSubmission();
        return;
      }
      throw error;
    }
    const resultReceipt = await submitFrozen();
    const summary = `发布任务创建完成：成功 ${resultReceipt.success_count}，失败 ${resultReceipt.failure_count}`;
    if (resultReceipt.failure_count > 0) ElMessage.warning(summary);
    else ElMessage.success(summary);
  } catch (error) {
    if (phase.value === 'uncertain') {
      setUncertainDescription(error);
      setRetryDelay(error);
      ElMessage.warning('提交结果不明确，请使用同一请求重试');
      return;
    }
    ElMessage.error(getReleaseApiErrorMessage(error, '创建发布任务失败'));
  }
};

const retrySubmission = async () => {
  if (retryDelaySeconds.value > 0) return;
  retrying.value = true;
  try {
    const sessionConfirmed = await authStore.refreshSession();
    if (!sessionConfirmed || authStore.initializationError) {
      uncertainDescription.value =
        '尚未确认当前登录主体，冻结请求仍保留。请在新标签页使用原账号登录后再重试。';
      ElMessage.warning('尚未确认原登录账号，已阻止发布请求');
      return;
    }
    const result = await retryFrozenSubmission();
    const summary = `发布任务创建完成：成功 ${result.success_count}，失败 ${result.failure_count}`;
    if (result.failure_count > 0) ElMessage.warning(summary);
    else ElMessage.success(summary);
  } catch (error) {
    if (phase.value === 'uncertain') {
      setUncertainDescription(error);
      setRetryDelay(error);
      ElMessage.warning(
        error instanceof FrozenReleaseActorError
          ? error.message
          : '结果仍不明确，可继续使用同一请求重试'
      );
      return;
    }
    ElMessage.error(getReleaseApiErrorMessage(error, '重试发布失败'));
  } finally {
    retrying.value = false;
  }
};

const openTask = (taskId: number, configId: number) => {
  const target = selectedTargets.value.get(configId);
  emit('viewTask', {
    taskId,
    appName: target?.app_name || `Config #${configId}`,
    ref: releaseRef.value.trim(),
    environment: target?.env || environment.value,
  });
};

const startNewReleaseFromFailures = async () => {
  selectFailedReceiptItems();
  await nextTick(restorePageSelection);
  ElMessage.info('已仅保留失败目标；重新预检后会使用新的幂等键');
};

const initialize = async () => {
  if (initialized) return;
  initialized = true;
  try {
    await loadEnvironments();
    const firstEnvironment = enabledEnvironments.value[0]?.code || '';
    if (firstEnvironment) await setEnvironment(firstEnvironment);
  } catch (error) {
    initialized = false;
    ElMessage.error(getReleaseApiErrorMessage(error, '初始化发布页面失败'));
  }
};

watch(
  () => props.isActive,
  active => {
    if (active) void initialize();
  },
  { immediate: true }
);

onMounted(() => {
  if (props.isActive) void initialize();
});

onUnmounted(() => {
  if (retryDelayTimer) clearInterval(retryDelayTimer);
});
</script>

<style scoped>
.release-composer {
  padding: 20px;
}

.composer-alert {
  margin-bottom: 18px;
}

.composer-form {
  padding: 18px 18px 0;
  background: #f8f9fa;
  border-radius: 8px;
}

.form-grid {
  display: grid;
  grid-template-columns: minmax(180px, 0.8fr) minmax(260px, 1.2fr) minmax(240px, 1fr);
  gap: 16px;
}

.composer-section {
  margin-top: 22px;
}

.result-section {
  padding-top: 4px;
}

.section-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 12px;
}

.section-heading h3 {
  margin: 0 0 4px;
  color: #303133;
  font-size: 16px;
}

.section-heading p {
  margin: 0;
  color: #909399;
  font-size: 13px;
}

.muted,
.inline-detail {
  color: #909399;
}

.inline-detail {
  margin-left: 10px;
}

.pagination {
  display: flex;
  justify-content: flex-end;
  margin-top: 14px;
}

.submit-actions {
  display: flex;
  justify-content: center;
  margin-top: 24px;
}

@media (max-width: 960px) {
  .form-grid {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
