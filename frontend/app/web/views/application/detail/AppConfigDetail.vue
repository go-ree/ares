<template>
  <div class="app-config-detail">
    <el-card v-loading="loading" shadow="never" class="config-card">
      <template #header>
        <div class="card-header">
          <span>环境配置</span>
          <span class="hint">
            PATCH 仅更新提交字段；多域名建议在「多域名」子菜单里管理（基于 config_id）。
          </span>
        </div>
        <div class="sub-header">
          <span class="app-name">当前应用：{{ displayName }}</span>
        </div>
      </template>

      <el-tabs v-model="activeEnv" class="env-tabs" @tab-change="handleEnvChange">
        <el-tab-pane v-for="env in envOptions" :key="env.value" :name="env.value">
          <template #label>
            <span>{{ env.label }}</span>
            <el-tag v-if="!env.enabled" size="small" type="warning" class="ml-8">
              {{ env.known ? '已停用' : '历史环境' }}
            </el-tag>
            <el-tag v-if="configsByEnv[env.value]" size="small" type="success" class="ml-8">
              已配置
            </el-tag>
            <el-tag v-else size="small" type="info" class="ml-8">未配置</el-tag>
          </template>

          <div v-if="!configsByEnv[env.value]" class="empty-env">
            <el-alert
              type="warning"
              show-icon
              :closable="false"
              title="该环境暂无应用配置。"
              :description="
                env.enabled
                  ? '可在此按需创建配置。'
                  : '环境已停用或仅存在于历史记录中，不能新建配置。'
              "
            />
            <div class="empty-actions">
              <el-button
                type="primary"
                :loading="creatingEnv === env.value"
                :disabled="loading || saving || !env.enabled"
                @click="createEnvConfig(env.value)"
              >
                创建该环境配置
              </el-button>
            </div>
          </div>

          <div v-else class="env-content">
            <div class="env-actions">
              <el-button
                v-if="!isEditingByEnv[env.value]"
                type="primary"
                :disabled="loading || saving"
                @click="startEdit(env.value)"
              >
                编辑
              </el-button>
              <template v-else>
                <el-button
                  type="primary"
                  :loading="saving"
                  :disabled="loading"
                  @click="saveEnvConfig(env.value)"
                >
                  保存
                </el-button>
                <el-button :disabled="loading || saving" @click="cancelEdit(env.value)"
                  >取消</el-button
                >
              </template>
              <el-button
                v-if="!isEditingByEnv[env.value]"
                :disabled="loading || saving"
                @click="refresh"
                >刷新</el-button
              >
            </div>

            <el-form
              :model="formsByEnv[env.value]"
              label-width="140px"
              class="config-form"
              @submit.prevent
            >
              <el-divider content-position="left">基础</el-divider>
              <el-row :gutter="16">
                <el-col :span="8">
                  <el-form-item label="应用实例数量">
                    <el-input-number
                      v-model="formsByEnv[env.value].pod_count"
                      :min="0"
                      :step="1"
                      :disabled="!isEditingByEnv[env.value]"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="8">
                  <el-form-item label="应用端口号">
                    <el-input-number
                      v-model="formsByEnv[env.value].container_port"
                      :min="1"
                      :max="65535"
                      :step="1"
                      placeholder="如 8080"
                      :disabled="!isEditingByEnv[env.value]"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="8">
                  <el-form-item label="代码包类型">
                    <el-input
                      :model-value="formsByEnv[env.value].code_package_type || ''"
                      placeholder="-"
                      disabled
                    />
                  </el-form-item>
                </el-col>
              </el-row>

              <el-divider content-position="left">资源</el-divider>
              <el-row :gutter="16">
                <el-col :span="8">
                  <el-form-item label="内存限制">
                    <el-input-number
                      v-model="formsByEnv[env.value].limits_memory"
                      :min="0"
                      :step="1"
                      :disabled="!isEditingByEnv[env.value]"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="8">
                  <el-form-item label="GPU数量">
                    <el-input-number
                      v-model="formsByEnv[env.value].gpu_count"
                      :min="0"
                      :step="1"
                      :disabled="!isEditingByEnv[env.value]"
                    />
                  </el-form-item>
                </el-col>
              </el-row>

              <el-divider content-position="left">探针</el-divider>
              <el-row :gutter="16">
                <el-col :span="8">
                  <el-form-item label="健康监测探针类型">
                    <el-select
                      v-model="formsByEnv[env.value].probe_type"
                      placeholder="选择探针类型"
                      clearable
                      :disabled="!isEditingByEnv[env.value]"
                      @change="handleProbeTypeChange(env.value)"
                    >
                      <el-option label="HTTP" value="HTTP" />
                      <el-option label="TCP" value="TCP" />
                    </el-select>
                  </el-form-item>
                </el-col>
                <el-col v-if="formsByEnv[env.value].probe_type === 'HTTP'" :span="8">
                  <el-form-item label="健康监测探针路径">
                    <el-input
                      v-model="formsByEnv[env.value].probe_check_path"
                      placeholder="如 /inside/checkup"
                      :disabled="!isEditingByEnv[env.value]"
                    />
                  </el-form-item>
                </el-col>
                <el-col v-if="formsByEnv[env.value].probe_type === 'HTTP'" :span="8">
                  <el-form-item label="端口">
                    <el-input-number
                      v-model="formsByEnv[env.value].probe_check_http_port"
                      :min="1"
                      :max="65535"
                      :step="1"
                      placeholder="如 8080"
                      :disabled="!isEditingByEnv[env.value]"
                    />
                  </el-form-item>
                </el-col>
                <el-col v-else-if="formsByEnv[env.value].probe_type === 'TCP'" :span="8">
                  <el-form-item label="端口">
                    <el-input-number
                      v-model="formsByEnv[env.value].probe_check_tcp_port"
                      :min="1"
                      :max="65535"
                      :step="1"
                      placeholder="如 8080"
                      :disabled="!isEditingByEnv[env.value]"
                    />
                  </el-form-item>
                </el-col>
              </el-row>

              <el-divider content-position="left">PreStop</el-divider>
              <el-row :gutter="16">
                <el-col :span="8">
                  <el-form-item label="类型">
                    <el-select
                      v-model="formsByEnv[env.value].pre_stop_type"
                      placeholder="选择 TCP / HTTP / COMMAND"
                      clearable
                      :disabled="!isEditingByEnv[env.value]"
                      @change="handlePreStopTypeChange(env.value)"
                    >
                      <el-option label="HTTP" value="HTTP" />
                      <el-option label="COMMAND" value="command" />
                    </el-select>
                  </el-form-item>
                </el-col>
                <el-col v-if="formsByEnv[env.value].pre_stop_type === 'HTTP'" :span="8">
                  <el-form-item label="URL">
                    <el-input
                      v-model="formsByEnv[env.value].pre_stop_check_path"
                      placeholder="如 /inside/prestop"
                      :disabled="!isEditingByEnv[env.value]"
                    />
                  </el-form-item>
                </el-col>
                <el-col v-if="formsByEnv[env.value].pre_stop_type === 'HTTP'" :span="8">
                  <el-form-item label="端口">
                    <el-input-number
                      v-model="formsByEnv[env.value].probe_stop_check_http_port"
                      :min="1"
                      :max="65535"
                      :step="1"
                      placeholder="如 8080"
                      :disabled="!isEditingByEnv[env.value]"
                    />
                  </el-form-item>
                </el-col>
                <el-col v-if="formsByEnv[env.value].pre_stop_type === 'command'" :span="16">
                  <el-form-item label="命令">
                    <el-input
                      v-model="formsByEnv[env.value].pre_stop_command"
                      type="textarea"
                      :autosize="{ minRows: 2, maxRows: 6 }"
                      placeholder="如 sleep 5"
                      :disabled="!isEditingByEnv[env.value]"
                    />
                  </el-form-item>
                </el-col>
              </el-row>
            </el-form>

            <el-divider content-position="left">发布流程</el-divider>
            <div class="workflow-access">
              <el-input
                v-model="workflowAdminToken"
                type="password"
                show-password
                autocomplete="off"
                placeholder="输入系统设置管理员令牌后管理发布流程"
                @input="workflowAccessReady = false"
                @keyup.enter="loadProtectedWorkflow(env.value)"
              >
                <template #prepend>管理员令牌</template>
              </el-input>
              <el-button
                type="primary"
                plain
                :loading="workflowLoadingByEnv[env.value]"
                @click="loadProtectedWorkflow(env.value)"
              >
                加载流程
              </el-button>
            </div>
            <el-alert
              v-if="!workflowAccessReady"
              title="发布流程属于受保护的系统配置，请先验证管理员令牌。"
              type="info"
              :closable="false"
              show-icon
            />
            <div v-else class="workflow-editor" v-loading="workflowLoadingByEnv[env.value]">
              <div class="workflow-toolbar">
                <div>
                  <strong>{{ workflowDraftsByEnv[env.value]?.name || '未配置发布流程' }}</strong>
                  <el-tag v-if="workflowMetaByEnv[env.value]?.version" class="ml-8" size="small">
                    v{{ workflowMetaByEnv[env.value]?.version }}
                  </el-tag>
                </div>
                <div>
                  <el-button
                    type="primary"
                    plain
                    :disabled="workflowSaving || pipelineStepTypes.length === 0"
                    @click="addWorkflowStep(env.value)"
                    >新增步骤</el-button
                  >
                  <el-button
                    type="primary"
                    :loading="workflowSaving"
                    @click="saveWorkflow(env.value)"
                    >保存流程</el-button
                  >
                </div>
              </div>
              <el-input
                v-model="workflowDraftsByEnv[env.value].name"
                placeholder="流程名称"
                class="workflow-name"
              />
              <el-empty
                v-if="workflowDraftsByEnv[env.value].steps.length === 0"
                :image-size="70"
                description="暂无步骤，可添加 Noop 或 Jenkins 等执行步骤"
              />
              <div
                v-for="(step, index) in workflowDraftsByEnv[env.value].steps"
                :key="`${step.key}-${index}`"
                class="workflow-step"
              >
                <div class="workflow-step-header">
                  <strong>步骤 {{ index + 1 }}</strong>
                  <div>
                    <el-button
                      link
                      :disabled="index === 0"
                      @click="moveWorkflowStep(env.value, index, -1)"
                      >上移</el-button
                    >
                    <el-button
                      link
                      :disabled="index === workflowDraftsByEnv[env.value].steps.length - 1"
                      @click="moveWorkflowStep(env.value, index, 1)"
                      >下移</el-button
                    >
                    <el-button type="danger" link @click="removeWorkflowStep(env.value, index)"
                      >删除</el-button
                    >
                  </div>
                </div>
                <el-row :gutter="12">
                  <el-col :span="6"
                    ><el-input v-model="step.key" placeholder="唯一 key，如 build"
                  /></el-col>
                  <el-col :span="6"><el-input v-model="step.name" placeholder="步骤名称" /></el-col>
                  <el-col :span="8">
                    <el-select v-model="step.uses" placeholder="选择执行器" style="width: 100%">
                      <el-option
                        v-for="stepType in pipelineStepTypes"
                        :key="stepType.uses"
                        :label="`${stepType.name}（${stepType.uses}）${stepType.available === false ? '— 当前不可运行' : ''}`"
                        :value="stepType.uses"
                      />
                    </el-select>
                  </el-col>
                  <el-col :span="4">
                    <el-select v-model="step.on_failure" style="width: 100%">
                      <el-option label="失败即停" value="stop" />
                      <el-option label="继续执行" value="continue" />
                    </el-select>
                  </el-col>
                </el-row>
                <el-input
                  v-model="step.withText"
                  type="textarea"
                  :rows="3"
                  class="step-config"
                  placeholder='步骤配置 JSON，例如 {"message":"done"}'
                />
              </div>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import axios from 'axios';
import { ElMessage, ElMessageBox } from 'element-plus';
import type {
  AppConfig,
  AppConfigWorkflow,
  AppEnv,
  AppInfo,
  PipelineStepType,
  UpdateAppConfigRequest,
  WorkflowFailurePolicy,
  WorkflowSpec,
} from '@/models/application';
import {
  createAppConfig,
  getAppConfigs,
  getAppDetail,
  patchAppConfigByEnv,
  getAppConfigWorkflow,
  getApplicationApiErrorMessage,
  getPipelineStepTypes,
  putAppConfigWorkflow,
} from '@/services/application';
import { normalizeLegacyNullableText } from '@/utils/legacy-nullable-text';
import { useEnvironments } from '@/composables/useEnvironments';

const route = useRoute();
const appId = ref<number>(Number(route.params.appId));
const appDetail = ref<AppInfo | null>(null);
const displayName = ref<string>(
  typeof route.query.name === 'string' && route.query.name
    ? route.query.name
    : `应用 ${appId.value}`
);

const { environments, loadEnvironments, labelForEnvironment } = useEnvironments();
const discoveredEnvironments = ref<AppEnv[]>([]);
const envOptions = computed(() => {
  const codes = new Set<AppEnv>(environments.value.map(item => item.code));
  discoveredEnvironments.value.forEach(code => codes.add(code));
  return Array.from(codes).map(value => {
    const catalog = environments.value.find(item => item.code === value);
    return {
      label: labelForEnvironment(value),
      value,
      known: Boolean(catalog),
      enabled: catalog?.enabled === true,
    };
  });
});

const activeEnv = ref<AppEnv>('');
const loading = ref(false);
const saving = ref(false);
const creatingEnv = ref<AppEnv | null>(null);

const configsByEnv = reactive<Record<AppEnv, AppConfig | null>>({});
const formsByEnv = reactive<Record<AppEnv, UpdateAppConfigRequest>>({});
const originalFormsByEnv = reactive<Record<AppEnv, UpdateAppConfigRequest>>({});
const isEditingByEnv = reactive<Record<AppEnv, boolean>>({});

interface WorkflowStepForm {
  key: string;
  name: string;
  uses: string;
  category?: string;
  timeout_seconds?: number;
  on_failure: WorkflowFailurePolicy;
  withText: string;
}

interface WorkflowDraft {
  name: string;
  steps: WorkflowStepForm[];
}
const pipelineStepTypes = ref<PipelineStepType[]>([]);
const workflowDraftsByEnv = reactive<Record<AppEnv, WorkflowDraft>>({});
const workflowMetaByEnv = reactive<Record<AppEnv, AppConfigWorkflow | null>>({});
const workflowLoadingByEnv = reactive<Record<AppEnv, boolean>>({});
const workflowSaving = ref(false);
const workflowAdminToken = ref('');
const workflowAccessReady = ref(false);

const ensureEnvState = (env: AppEnv) => {
  if (!(env in configsByEnv)) configsByEnv[env] = null;
  if (!(env in formsByEnv)) formsByEnv[env] = {};
  if (!(env in originalFormsByEnv)) originalFormsByEnv[env] = {};
  if (!(env in isEditingByEnv)) isEditingByEnv[env] = false;
  if (!(env in workflowDraftsByEnv)) workflowDraftsByEnv[env] = { name: '', steps: [] };
  if (!(env in workflowMetaByEnv)) workflowMetaByEnv[env] = null;
  if (!(env in workflowLoadingByEnv)) workflowLoadingByEnv[env] = false;
};

watch(envOptions, options => options.forEach(option => ensureEnvState(option.value)), {
  immediate: true,
});

const reset = () => {
  envOptions.value.forEach(env => {
    ensureEnvState(env.value);
    configsByEnv[env.value] = null;
    formsByEnv[env.value] = {};
    originalFormsByEnv[env.value] = {};
    isEditingByEnv[env.value] = false;
  });
};

const handleProbeTypeChange = (env: AppEnv) => {
  if (!isEditingByEnv[env]) return;
  const probeType = formsByEnv[env].probe_type;
  if (probeType === 'HTTP') {
    formsByEnv[env].probe_check_tcp_port = undefined;
    if (!formsByEnv[env].probe_check_http_port) {
      formsByEnv[env].probe_check_http_port = formsByEnv[env].container_port;
    }
    // 兼容清理
    formsByEnv[env].probe_check_port = undefined;
  } else if (probeType === 'TCP') {
    formsByEnv[env].probe_check_path = '';
    formsByEnv[env].probe_check_http_port = undefined;
    if (!formsByEnv[env].probe_check_tcp_port) {
      formsByEnv[env].probe_check_tcp_port = formsByEnv[env].container_port;
    }
    // 兼容清理
    formsByEnv[env].probe_check_port = undefined;
  } else {
    formsByEnv[env].probe_type = '';
    formsByEnv[env].probe_check_path = '';
    formsByEnv[env].probe_check_tcp_port = undefined;
    formsByEnv[env].probe_check_http_port = undefined;
    formsByEnv[env].probe_check_port = undefined;
  }
};

const handlePreStopTypeChange = (env: AppEnv) => {
  if (!isEditingByEnv[env]) return;
  const t = formsByEnv[env].pre_stop_type;
  if (t === 'HTTP') {
    // HTTP：路径 + 端口（probe_stop_check_http_port）
    formsByEnv[env].pre_stop_command = '';
    if (!formsByEnv[env].probe_stop_check_http_port) {
      formsByEnv[env].probe_stop_check_http_port =
        formsByEnv[env].probe_check_http_port || formsByEnv[env].container_port;
    }
  } else if (t === 'command') {
    // COMMAND：只命令
    formsByEnv[env].pre_stop_check_path = '';
    formsByEnv[env].probe_stop_check_http_port = undefined;
    formsByEnv[env].pre_stop_check_port = undefined;
  } else {
    // 清空
    formsByEnv[env].pre_stop_type = '';
    formsByEnv[env].pre_stop_check_path = '';
    formsByEnv[env].pre_stop_check_port = undefined;
    formsByEnv[env].pre_stop_command = '';
    formsByEnv[env].probe_stop_check_http_port = undefined;
  }
};

const fetchConfigs = async () => {
  if (!Number.isFinite(appId.value) || appId.value <= 0) return;
  loading.value = true;
  try {
    const resp = await getAppConfigs(appId.value);
    if (resp.data.code !== 1) throw new Error(resp.data.message || '获取应用配置失败');

    reset();
    discoveredEnvironments.value = (resp.data.result || []).map(cfg => cfg.env);
    envOptions.value.forEach(option => ensureEnvState(option.value));
    for (const cfg of resp.data.result || []) {
      ensureEnvState(cfg.env);
      configsByEnv[cfg.env] = cfg;
      const form: UpdateAppConfigRequest = {
        code_package_type: cfg.code_package_type || undefined,
        code_package_path: normalizeLegacyNullableText(cfg.code_package_path),
        code_package_name: normalizeLegacyNullableText(cfg.code_package_name),
        base_image: normalizeLegacyNullableText(cfg.base_image),
        pod_count: cfg.pod_count ?? undefined,
        limits_memory: cfg.limits_memory ?? undefined,
        gpu_count: cfg.gpu_count ?? undefined,
        container_port: (cfg as any).container_port ?? undefined,
        probe_type: cfg.probe_type || undefined,
        probe_check_path: cfg.probe_check_path || undefined,
        // TCP 探针端口：优先取 probe_check_tcp_port，其次兼容 probe_check_port
        probe_check_tcp_port:
          (cfg as any).probe_check_tcp_port ?? cfg.probe_check_port ?? undefined,
        probe_check_http_port: (cfg as any).probe_check_http_port ?? undefined,
        probe_stop_check_http_port: (cfg as any).probe_stop_check_http_port ?? undefined,
        // 兼容字段保留（不主动回写）
        probe_check_port: undefined,
        // PreStop：前端不再提供 TCP 选项；如果后端返回 TCP，则按“未启用”处理
        pre_stop_type: cfg.pre_stop_type === 'TCP' ? undefined : cfg.pre_stop_type || undefined,
        pre_stop_check_path: cfg.pre_stop_check_path || undefined,
        pre_stop_check_port: cfg.pre_stop_check_port ?? undefined,
        pre_stop_command: normalizeLegacyNullableText(cfg.pre_stop_command),
      };
      formsByEnv[cfg.env] = { ...form };
      originalFormsByEnv[cfg.env] = { ...form };
      isEditingByEnv[cfg.env] = false;
    }
    if (!activeEnv.value || !envOptions.value.some(option => option.value === activeEnv.value)) {
      activeEnv.value = envOptions.value[0]?.value || '';
    }
    if (
      workflowAccessReady.value &&
      workflowAdminToken.value.trim() &&
      activeEnv.value &&
      configsByEnv[activeEnv.value]
    ) {
      await loadWorkflow(activeEnv.value);
    }
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '获取应用配置失败');
  } finally {
    loading.value = false;
  }
};

const createEnvConfig = async (env: AppEnv) => {
  if (!Number.isFinite(appId.value) || appId.value <= 0) return;
  creatingEnv.value = env;
  try {
    await ElMessageBox.confirm(`确认创建 ${env} 环境配置吗？`, '创建环境配置', {
      confirmButtonText: '创建',
      cancelButtonText: '取消',
      type: 'warning',
    });

    const resp = await createAppConfig(appId.value, { env });
    if (resp.data.code !== 1) throw new Error(resp.data.message || '创建失败');
    await ElMessageBox.alert(`已创建 ${env} 环境配置`, '创建结果', { type: 'success' });
    activeEnv.value = env;
    await fetchConfigs();
  } catch (e) {
    if (e === 'cancel') return;
    console.error(e);
    await ElMessageBox.alert(e instanceof Error ? e.message : '创建失败', '创建结果', {
      type: 'error',
    });
  } finally {
    creatingEnv.value = null;
  }
};

const fetchAppName = async () => {
  if (!Number.isFinite(appId.value) || appId.value <= 0) return;
  try {
    const resp = await getAppDetail(appId.value);
    if (resp.data.code === 1) {
      appDetail.value = resp.data.result;
      displayName.value = resp.data.result?.app_name || displayName.value;
    }
  } catch (e) {
    // 忽略：名称展示失败不影响配置编辑
    console.warn('获取应用名称失败:', e);
  }
};

const startEdit = (env: AppEnv) => {
  if (!configsByEnv[env]) return;
  originalFormsByEnv[env] = { ...formsByEnv[env] };
  isEditingByEnv[env] = true;
};

const cancelEdit = (env: AppEnv) => {
  formsByEnv[env] = { ...originalFormsByEnv[env] };
  isEditingByEnv[env] = false;
};

const getChangedKeys = (env: AppEnv) => {
  const cur = formsByEnv[env] as Record<string, any>;
  const orig = originalFormsByEnv[env] as Record<string, any>;
  const keys = new Set<string>([...Object.keys(cur), ...Object.keys(orig)]);
  const changed: string[] = [];
  keys.forEach(k => {
    if (cur[k] !== orig[k]) changed.push(k);
  });
  return changed;
};

const saveEnvConfig = async (env: AppEnv) => {
  if (!Number.isFinite(appId.value) || appId.value <= 0) return;
  if (!isEditingByEnv[env]) return;
  const changedKeys = getChangedKeys(env);
  // 没有任何变动时，不发请求，避免后端报“未更新任何记录”
  if (changedKeys.length === 0) {
    ElMessage.info('未做任何修改，无需保存');
    isEditingByEnv[env] = false;
    return;
  }
  saving.value = true;
  try {
    const payload: UpdateAppConfigRequest = {};
    const currentForm = formsByEnv[env] as Record<string, unknown>;
    for (const key of changedKeys) {
      const value = currentForm[key];
      if (value !== undefined) {
        (payload as Record<string, unknown>)[key] = value;
      }
    }
    // 兼容：避免把历史字段 probe_check_port 误提交；统一走 probe_check_tcp_port
    if (payload.probe_type === 'TCP') {
      delete payload.probe_check_http_port;
    } else if (payload.probe_type === 'HTTP') {
      delete payload.probe_check_tcp_port;
    }

    // PreStop
    // - HTTP：类型+路径+端口（probe_stop_check_http_port）
    // - command：类型+命令（pre_stop_command）
    if (Object.prototype.hasOwnProperty.call(payload, 'pre_stop_type')) {
      if (payload.pre_stop_type === 'HTTP') {
        // 兼容字段不再使用
        delete payload.pre_stop_check_port;
      } else if (payload.pre_stop_type === 'command') {
        delete payload.probe_stop_check_http_port;
        delete payload.pre_stop_check_port;
      } else {
        delete payload.pre_stop_check_port;
        delete payload.probe_stop_check_http_port;
      }
    }

    delete payload.probe_check_port;
    const resp = await patchAppConfigByEnv(appId.value, env, payload);
    if (resp.data.code !== 1) {
      // 后端语义：未更新任何记录（当作无变动提示，不视为错误）
      if ((resp.data.error || '').includes('未更新任何记录')) {
        ElMessage.info(resp.data.error || resp.data.message || '未更新任何记录');
        isEditingByEnv[env] = false;
        return;
      }
      throw new Error(resp.data.error || resp.data.message || '保存失败');
    }
    ElMessage.success('保存成功');
    await ElMessageBox.alert('环境配置保存成功', '保存结果', { type: 'success' });
    await fetchConfigs();
    isEditingByEnv[env] = false;
  } catch (e) {
    console.error(e);
    let message = '保存失败';
    if (axios.isAxiosError(e) && e.response?.data) {
      const data: any = e.response.data;
      message = data.error || data.message || message;
    } else if (e instanceof Error) {
      message = e.message || message;
    }
    ElMessage.error(message);
    await ElMessageBox.alert(message, '保存结果', { type: 'error' });
  } finally {
    saving.value = false;
  }
};

const refresh = async () => {
  await fetchConfigs();
};

const emptyWorkflow = (): WorkflowDraft => ({ name: '', steps: [] });

const loadStepTypes = async () => {
  const response = await getPipelineStepTypes();
  if (response.data.code !== 1) throw new Error(response.data.message || '获取步骤类型失败');
  pipelineStepTypes.value = response.data.result || [];
};

const specToDraft = (spec?: WorkflowSpec | null): WorkflowDraft => ({
  name: spec?.name || '',
  steps: (spec?.steps || []).map(step => ({
    key: step.key,
    name: step.name,
    uses: step.uses,
    category: step.category,
    timeout_seconds: step.timeout_seconds,
    on_failure: step.on_failure || 'stop',
    withText: JSON.stringify(step.with || {}, null, 2),
  })),
});

const loadWorkflow = async (env: AppEnv) => {
  const configId = configsByEnv[env]?.config_id;
  if (!configId || !workflowAdminToken.value.trim() || workflowLoadingByEnv[env]) return;
  workflowLoadingByEnv[env] = true;
  try {
    const response = await getAppConfigWorkflow(configId, workflowAdminToken.value);
    const result = response.data.result;
    const wrapped = result && 'spec' in result ? result : null;
    const spec = wrapped?.spec || (result as WorkflowSpec | null);
    workflowMetaByEnv[env] = wrapped;
    workflowDraftsByEnv[env] = specToDraft(spec);
    workflowAccessReady.value = true;
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 404) {
      workflowMetaByEnv[env] = null;
      workflowDraftsByEnv[env] = emptyWorkflow();
      workflowAccessReady.value = true;
    } else {
      workflowAccessReady.value = false;
      ElMessage.error(getApplicationApiErrorMessage(error, '获取发布流程失败'));
    }
  } finally {
    workflowLoadingByEnv[env] = false;
  }
};

const loadProtectedWorkflow = async (env: AppEnv) => {
  if (!workflowAdminToken.value.trim()) {
    ElMessage.warning('请输入管理员令牌');
    return;
  }
  await loadWorkflow(env);
};

const addWorkflowStep = (env: AppEnv) => {
  ensureEnvState(env);
  const type =
    pipelineStepTypes.value.find(item => item.available !== false) || pipelineStepTypes.value[0];
  const index = workflowDraftsByEnv[env].steps.length + 1;
  workflowDraftsByEnv[env].steps.push({
    key: `step-${index}`,
    name: type?.name || `步骤 ${index}`,
    uses: type?.uses || '',
    on_failure: 'stop',
    withText: '{}',
  });
};

const removeWorkflowStep = (env: AppEnv, index: number) =>
  workflowDraftsByEnv[env].steps.splice(index, 1);
const moveWorkflowStep = (env: AppEnv, index: number, offset: number) => {
  const steps = workflowDraftsByEnv[env].steps;
  const target = index + offset;
  if (target < 0 || target >= steps.length) return;
  const [step] = steps.splice(index, 1);
  if (step) steps.splice(target, 0, step);
};

const saveWorkflow = async (env: AppEnv) => {
  const configId = configsByEnv[env]?.config_id;
  if (!configId) return;
  if (!workflowAccessReady.value || !workflowAdminToken.value.trim()) {
    return ElMessage.warning('请先使用管理员令牌加载发布流程');
  }
  const draft = workflowDraftsByEnv[env];
  if (!draft.name.trim()) return ElMessage.warning('请填写流程名称');
  if (draft.steps.length === 0) return ElMessage.warning('发布流程至少需要一个步骤');
  try {
    const keys = new Set<string>();
    const steps = draft.steps.map((step, index) => {
      const key = step.key.trim();
      if (!key || keys.has(key)) throw new Error(`步骤 ${index + 1} 的 key 为空或重复`);
      keys.add(key);
      if (!step.name.trim() || !step.uses)
        throw new Error(`请完善步骤 ${index + 1} 的名称和执行器`);
      let config: Record<string, unknown>;
      try {
        const parsed: unknown = JSON.parse(step.withText || '{}');
        if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error();
        config = parsed as Record<string, unknown>;
      } catch {
        throw new Error(`步骤 ${index + 1} 的配置必须是 JSON 对象`);
      }
      return {
        key,
        name: step.name.trim(),
        uses: step.uses,
        ...(step.category ? { category: step.category } : {}),
        with: config,
        ...(step.timeout_seconds ? { timeout_seconds: step.timeout_seconds } : {}),
        on_failure: step.on_failure,
      };
    });
    workflowSaving.value = true;
    const response = await putAppConfigWorkflow(
      configId,
      workflowMetaByEnv[env]?.revision || 0,
      {
        schema_version: 1,
        name: draft.name.trim(),
        steps,
      },
      workflowAdminToken.value
    );
    if (response.data.code !== 1)
      throw new Error(response.data.error || response.data.message || '保存流程失败');
    workflowMetaByEnv[env] = response.data.result;
    workflowDraftsByEnv[env] = specToDraft(response.data.result.spec);
    ElMessage.success('发布流程已保存为新版本');
  } catch (error) {
    const message = getApplicationApiErrorMessage(error, '保存流程失败');
    if (axios.isAxiosError(error) && error.response?.status === 409) {
      ElMessage.error(`配置已被其他管理员更新，请重新加载后再保存：${message}`);
    } else {
      ElMessage.error(message);
    }
  } finally {
    workflowSaving.value = false;
  }
};

const handleEnvChange = async (name: string | number) => {
  const nextEnv = name as AppEnv;
  // 每个环境拥有独立表单状态，切换时保留尚未保存的编辑内容。
  activeEnv.value = nextEnv;
  if (workflowAccessReady.value && configsByEnv[nextEnv]) await loadWorkflow(nextEnv);
};

watch(
  () => route.params.appId,
  async val => {
    appId.value = Number(val);
    displayName.value =
      typeof route.query.name === 'string' && route.query.name
        ? route.query.name
        : `应用 ${appId.value}`;
    appDetail.value = null;
    await fetchAppName();
    await fetchConfigs();
  }
);

onMounted(async () => {
  try {
    await Promise.all([fetchAppName(), loadEnvironments(), loadStepTypes()]);
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : '初始化页面失败');
  }
  await fetchConfigs();
});

onBeforeUnmount(() => {
  workflowAdminToken.value = '';
});
</script>

<style scoped>
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.hint {
  font-size: 12px;
  color: #909399;
}

.ml-8 {
  margin-left: 8px;
}

.env-content {
  padding-top: 8px;
}

.config-form {
  max-width: 1100px;
}

.actions {
  margin-top: 12px;
  display: flex;
  gap: 8px;
}

.empty-env {
  padding: 12px 0;
}

.empty-actions {
  margin-top: 12px;
  display: flex;
  gap: 8px;
}

.config-card {
  border-radius: 8px;
}

.sub-header {
  margin-top: 6px;
}

.app-name {
  font-size: 12px;
  color: #606266;
}

.workflow-toolbar,
.workflow-step-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.workflow-name {
  margin: 12px 0;
}
.workflow-access {
  display: grid;
  grid-template-columns: minmax(280px, 620px) auto;
  gap: 12px;
  margin-bottom: 12px;
}
.workflow-step {
  padding: 14px;
  margin-bottom: 12px;
  border: 1px solid #dcdfe6;
  border-radius: 6px;
  background: #fafafa;
}
.workflow-step-header {
  margin-bottom: 10px;
}
.step-config {
  margin-top: 10px;
}
</style>
