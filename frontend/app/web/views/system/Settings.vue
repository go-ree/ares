<template>
  <div class="settings-page">
    <el-card class="access-card" shadow="never">
      <template #header>
        <div class="card-header">
          <div>
            <h2>系统配置</h2>
            <p>在运行时管理发布环境、Jenkins 与 Kubernetes，无需重启服务。</p>
          </div>
        </div>
      </template>

      <el-alert
        title="管理员令牌仅保存在当前页面内存中，刷新或离开页面后会被清除。"
        type="info"
        :closable="false"
        show-icon
      />

      <div class="token-row">
        <el-input
          v-model="adminToken"
          type="password"
          show-password
          autocomplete="off"
          placeholder="输入 X-Ares-Admin-Token"
          clearable
          @keyup.enter="loadSettings"
        >
          <template #prepend>管理员令牌</template>
        </el-input>
        <el-button
          type="primary"
          :loading="loading"
          :disabled="savingJenkins || savingKubernetes || creatingEnvironment"
          @click="loadSettings"
        >
          加载配置
        </el-button>
      </div>
    </el-card>

    <el-empty v-if="!loaded" description="输入管理员令牌后加载系统配置" />

    <template v-else>
      <el-card class="integration-card" shadow="never">
        <template #header>
          <div class="integration-header">
            <div>
              <h3 class="section-title">发布环境</h3>
              <p class="section-description">
                环境代码是应用配置与发布记录的稳定标识，创建后不可修改。
              </p>
            </div>
            <el-tag type="info">共 {{ environmentCatalog.length }} 个</el-tag>
          </div>
        </template>

        <el-form class="environment-create-form" label-position="top">
          <el-form-item label="环境代码" required>
            <el-input
              v-model="newEnvironment.code"
              maxlength="63"
              placeholder="例如：preview"
              @keyup.enter="createEnvironment"
            />
          </el-form-item>
          <el-form-item label="环境名称" required>
            <el-input
              v-model="newEnvironment.name"
              maxlength="128"
              placeholder="例如：预览环境"
              @keyup.enter="createEnvironment"
            />
          </el-form-item>
          <el-form-item label="排序">
            <el-input-number
              v-model="newEnvironment.sort_order"
              controls-position="right"
              :step="10"
            />
          </el-form-item>
          <el-form-item label="初始状态">
            <el-switch
              v-model="newEnvironment.enabled"
              inline-prompt
              active-text="启用"
              inactive-text="停用"
            />
          </el-form-item>
          <el-form-item class="environment-create-action">
            <el-button
              type="primary"
              :loading="creatingEnvironment"
              :disabled="loading || savingJenkins || savingKubernetes"
              @click="createEnvironment"
            >
              新增环境
            </el-button>
          </el-form-item>
        </el-form>

        <el-table
          v-loading="loadingEnvironments"
          :data="environmentCatalog"
          row-key="code"
          class="environment-table"
          empty-text="尚未创建发布环境"
        >
          <el-table-column label="环境代码" min-width="150">
            <template #default="{ row }">
              <el-tag type="info">{{ row.code }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="环境名称" min-width="220">
            <template #default="{ row }">
              <el-input v-model="row.name" maxlength="128" />
            </template>
          </el-table-column>
          <el-table-column label="排序" width="150">
            <template #default="{ row }">
              <el-input-number
                v-model="row.sort_order"
                controls-position="right"
                :step="10"
                class="environment-sort-input"
              />
            </template>
          </el-table-column>
          <el-table-column label="状态" width="130">
            <template #default="{ row }">
              <el-switch
                :model-value="row.enabled"
                :loading="savingEnvironmentCodes.has(row.code)"
                inline-prompt
                active-text="启用"
                inactive-text="停用"
                @change="toggleEnvironment(row, $event)"
              />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="110" align="right">
            <template #default="{ row }">
              <el-button
                type="primary"
                link
                :loading="savingEnvironmentCodes.has(row.code)"
                @click="saveEnvironment(row)"
              >
                保存
              </el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <el-card class="integration-card" shadow="never">
        <template #header>
          <div class="integration-header">
            <div class="integration-title">
              <h3>Jenkins</h3>
              <el-tag :type="statusTagType(jenkinsStatus.enabled, jenkinsStatus.connected)">
                {{ statusText(jenkinsStatus.enabled, jenkinsStatus.connected) }}
              </el-tag>
              <el-tag v-if="jenkinsForm.enabled !== jenkinsStatus.enabled" type="warning">
                启停状态未保存
              </el-tag>
            </div>
            <el-switch
              v-model="jenkinsForm.enabled"
              inline-prompt
              active-text="启用"
              inactive-text="停用"
            />
          </div>
        </template>

        <el-alert
          v-if="jenkinsStatus.last_error"
          class="status-error"
          :title="jenkinsStatus.last_error"
          type="error"
          :closable="false"
          show-icon
        />

        <el-form label-position="top" class="integration-form">
          <div class="form-grid">
            <el-form-item label="服务地址" :required="jenkinsForm.enabled">
              <el-input
                v-model="jenkinsForm.address"
                placeholder="例如：https://jenkins.example.com"
              />
            </el-form-item>
            <el-form-item label="用户名" :required="jenkinsForm.enabled">
              <el-input v-model="jenkinsForm.username" placeholder="Jenkins 用户名" />
            </el-form-item>
          </div>

          <div class="form-grid">
            <el-form-item label="请求超时（秒）" required>
              <el-input-number
                v-model="jenkinsForm.timeout_seconds"
                :min="1"
                :max="120"
                controls-position="right"
              />
            </el-form-item>
            <el-form-item label="API Token">
              <el-input
                v-model="jenkinsForm.token"
                type="password"
                show-password
                autocomplete="new-password"
                :placeholder="
                  jenkinsStatus.token_configured
                    ? '已配置；留空将保留现有 Token'
                    : '尚未配置，请输入 Token'
                "
              />
              <div class="secret-hint">
                <el-tag size="small" :type="jenkinsStatus.token_configured ? 'success' : 'info'">
                  {{ jenkinsStatus.token_configured ? '已配置' : '未配置' }}
                </el-tag>
                <span>敏感值不会从服务端返回，留空不会覆盖。</span>
              </div>
            </el-form-item>
          </div>

          <div class="form-actions">
            <el-button
              type="primary"
              :loading="savingJenkins"
              :disabled="loading || savingKubernetes"
              @click="saveJenkins"
            >
              保存 Jenkins 配置
            </el-button>
          </div>
        </el-form>
      </el-card>

      <el-card class="integration-card" shadow="never">
        <template #header>
          <div class="integration-header">
            <div class="integration-title">
              <h3>Kubernetes</h3>
              <el-tag :type="statusTagType(kubernetesStatus.enabled, kubernetesStatus.connected)">
                {{ statusText(kubernetesStatus.enabled, kubernetesStatus.connected) }}
              </el-tag>
              <el-tag v-if="kubernetesForm.enabled !== kubernetesStatus.enabled" type="warning">
                启停状态未保存
              </el-tag>
            </div>
            <el-switch
              v-model="kubernetesForm.enabled"
              inline-prompt
              active-text="启用"
              inactive-text="停用"
            />
          </div>
        </template>

        <el-alert
          v-if="kubernetesStatus.last_error"
          class="status-error"
          :title="kubernetesStatus.last_error"
          type="error"
          :closable="false"
          show-icon
        />

        <el-form label-position="top" class="integration-form">
          <el-form-item label="请求超时（秒）" required>
            <el-input-number
              v-model="kubernetesForm.timeout_seconds"
              :min="1"
              :max="120"
              controls-position="right"
            />
          </el-form-item>

          <div class="cluster-heading">
            <div>
              <h4>集群配置</h4>
              <p>每个环境仅允许配置一个集群；Kubeconfig 留空将保留现有值。</p>
            </div>
            <el-button
              type="primary"
              plain
              :disabled="kubernetesForm.clusters.length >= environmentOptions.length"
              @click="addCluster"
            >
              新增集群
            </el-button>
          </div>

          <el-empty
            v-if="kubernetesForm.clusters.length === 0"
            :image-size="80"
            description="尚未配置 Kubernetes 集群"
          />

          <div
            v-for="(cluster, index) in kubernetesForm.clusters"
            :key="cluster.key"
            class="cluster-editor"
          >
            <div class="cluster-editor-header">
              <strong>集群 {{ index + 1 }}</strong>
              <el-button type="danger" link @click="removeCluster(index)">删除</el-button>
            </div>

            <div class="cluster-grid">
              <el-form-item label="环境" required>
                <el-select
                  v-model="cluster.environment"
                  class="full-width"
                  :disabled="cluster.kubeconfig_configured"
                >
                  <el-option
                    v-for="option in environmentOptions"
                    :key="option.value"
                    :label="option.label"
                    :value="option.value"
                    :disabled="isEnvironmentUsed(option.value, cluster.key)"
                  />
                </el-select>
                <div v-if="cluster.kubeconfig_configured" class="field-hint">
                  已保存集群的环境不可直接修改；如需迁移，请删除后重新添加。
                </div>
              </el-form-item>
              <el-form-item label="集群名称" required>
                <el-input v-model="cluster.name" placeholder="例如：开发集群" />
              </el-form-item>
            </div>

            <el-form-item label="说明">
              <el-input v-model="cluster.description" placeholder="集群用途或备注" />
            </el-form-item>

            <el-form-item label="Kubeconfig">
              <el-input
                v-model="cluster.kubeconfig"
                type="textarea"
                :rows="5"
                resize="vertical"
                autocomplete="off"
                spellcheck="false"
                :placeholder="
                  cluster.kubeconfig_configured
                    ? '已配置；留空将保留现有 Kubeconfig'
                    : '粘贴该集群的 Kubeconfig'
                "
              />
              <div class="secret-hint">
                <el-tag size="small" :type="cluster.kubeconfig_configured ? 'success' : 'info'">
                  {{ cluster.kubeconfig_configured ? '已配置' : '未配置' }}
                </el-tag>
                <span>敏感值只用于本次保存，保存后会立即从表单中清除。</span>
              </div>
            </el-form-item>
          </div>

          <div class="form-actions">
            <el-button
              type="primary"
              :loading="savingKubernetes"
              :disabled="loading || savingJenkins"
              @click="saveKubernetes"
            >
              保存 Kubernetes 配置
            </el-button>
          </div>
        </el-form>
      </el-card>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import {
  getIntegrationSettings,
  getSystemApiErrorMessage,
  updateJenkinsIntegration,
  updateKubernetesIntegration,
} from '@/services/system';
import {
  createSystemEnvironment,
  getEnvironmentApiErrorMessage,
  getSystemEnvironments,
  updateSystemEnvironment,
} from '@/services/environment';
import type { EnvironmentCatalogItem } from '@/models/application';
import type {
  JenkinsIntegrationSettings,
  KubernetesClusterSettings,
  KubernetesEnvironment,
  KubernetesIntegrationSettings,
  UpdateJenkinsIntegrationRequest,
  UpdateKubernetesClusterRequest,
} from '@/types/system';
import { useEnvironments } from '@/composables/useEnvironments';

interface JenkinsFormState {
  enabled: boolean;
  address: string;
  username: string;
  timeout_seconds: number;
  token: string;
}

interface KubernetesClusterFormState extends KubernetesClusterSettings {
  key: number;
  kubeconfig: string;
}

interface KubernetesFormState {
  enabled: boolean;
  timeout_seconds: number;
  clusters: KubernetesClusterFormState[];
}

interface EnvironmentEditor {
  id?: number;
  code: string;
  name: string;
  enabled: boolean;
  sort_order: number;
}

const { environments, loadEnvironments, labelForEnvironment } = useEnvironments();
const environmentOptions = computed<Array<{ label: string; value: KubernetesEnvironment }>>(() => {
  const codes = new Set(environments.value.filter(item => item.enabled).map(item => item.code));
  kubernetesForm.clusters.forEach(cluster => codes.add(cluster.environment));
  return Array.from(codes).map(value => ({ label: labelForEnvironment(value), value }));
});
const MAX_INTEGRATION_TIMEOUT_SECONDS = 120;
const MAX_JENKINS_TOKEN_BYTES = 64 * 1024;
const MAX_KUBECONFIG_BYTES = 1024 * 1024;

const adminToken = ref('');
const loading = ref(false);
const loaded = ref(false);
const savingJenkins = ref(false);
const savingKubernetes = ref(false);
const loadingEnvironments = ref(false);
const creatingEnvironment = ref(false);
const savingEnvironmentCodes = reactive(new Set<string>());
const environmentCatalog = ref<EnvironmentEditor[]>([]);
const newEnvironment = reactive({
  code: '',
  name: '',
  enabled: true,
  sort_order: 10,
});
let clusterKey = 0;

const jenkinsForm = reactive<JenkinsFormState>({
  enabled: false,
  address: '',
  username: '',
  timeout_seconds: 30,
  token: '',
});
const jenkinsStatus = reactive({
  enabled: false,
  token_configured: false,
  connected: false,
  last_error: '',
});
const originalJenkinsIdentity = reactive({
  address: '',
  username: '',
});

const kubernetesForm = reactive<KubernetesFormState>({
  enabled: false,
  timeout_seconds: 30,
  clusters: [],
});
const kubernetesStatus = reactive({
  enabled: false,
  connected: false,
  last_error: '',
});

const createClusterForm = (cluster: KubernetesClusterSettings): KubernetesClusterFormState => ({
  ...cluster,
  key: ++clusterKey,
  kubeconfig: '',
});

const applyJenkinsSettings = (settings: JenkinsIntegrationSettings) => {
  jenkinsForm.enabled = settings.enabled;
  jenkinsForm.address = settings.address;
  jenkinsForm.username = settings.username;
  jenkinsForm.timeout_seconds = settings.timeout_seconds;
  jenkinsForm.token = '';
  jenkinsStatus.enabled = settings.enabled;
  jenkinsStatus.token_configured = settings.token_configured;
  jenkinsStatus.connected = settings.connected;
  jenkinsStatus.last_error = settings.last_error;
  originalJenkinsIdentity.address = settings.address.trim();
  originalJenkinsIdentity.username = settings.username.trim();
};

const applyKubernetesSettings = (settings: KubernetesIntegrationSettings) => {
  kubernetesForm.enabled = settings.enabled;
  kubernetesForm.timeout_seconds = settings.timeout_seconds;
  kubernetesForm.clusters = [...settings.clusters]
    .sort((left, right) => {
      const leftOrder =
        environments.value.find(item => item.code === left.environment)?.sort_order ?? 0;
      const rightOrder =
        environments.value.find(item => item.code === right.environment)?.sort_order ?? 0;
      return leftOrder - rightOrder || left.environment.localeCompare(right.environment);
    })
    .map(createClusterForm);
  kubernetesStatus.enabled = settings.enabled;
  kubernetesStatus.connected = settings.connected;
  kubernetesStatus.last_error = settings.last_error;
};

const normalizeEnvironment = (item: EnvironmentCatalogItem): EnvironmentEditor => ({
  id: item.id,
  code: String(item.code || item.env || '').trim(),
  name: String(item.name || item.description_cn || item.description || '').trim(),
  enabled: item.enabled !== false,
  sort_order: Number.isFinite(item.sort_order) ? item.sort_order : 0,
});

const sortEnvironmentCatalog = () => {
  environmentCatalog.value.sort(
    (left, right) => left.sort_order - right.sort_order || left.code.localeCompare(right.code)
  );
};

const loadEnvironmentCatalog = async () => {
  loadingEnvironments.value = true;
  try {
    environmentCatalog.value = (await getSystemEnvironments(adminToken.value))
      .map(normalizeEnvironment)
      .filter(item => item.code);
    sortEnvironmentCatalog();
    if (!newEnvironment.code && !newEnvironment.name) {
      newEnvironment.sort_order = nextEnvironmentSortOrder();
    }
  } finally {
    loadingEnvironments.value = false;
  }
};

const loadSettings = async () => {
  if (loading.value || savingJenkins.value || savingKubernetes.value) return;
  if (!adminToken.value.trim()) {
    ElMessage.warning('请输入管理员令牌');
    return;
  }

  loading.value = true;
  try {
    const [settings] = await Promise.all([
      getIntegrationSettings(adminToken.value),
      loadEnvironmentCatalog(),
    ]);
    applyJenkinsSettings(settings.jenkins);
    applyKubernetesSettings(settings.kubernetes);
    loaded.value = true;
    ElMessage.success('系统配置已加载');
  } catch (error) {
    loaded.value = false;
    ElMessage.error(getEnvironmentApiErrorMessage(error));
  } finally {
    loading.value = false;
  }
};

const validateEnvironmentEditor = (environment: Pick<EnvironmentEditor, 'name' | 'sort_order'>) => {
  if (!environment.name.trim()) {
    ElMessage.warning('环境名称不能为空');
    return false;
  }
  if (!Number.isInteger(environment.sort_order)) {
    ElMessage.warning('环境排序必须是整数');
    return false;
  }
  return true;
};

const refreshPublicEnvironmentCatalog = async () => {
  try {
    await loadEnvironments(true);
  } catch (error) {
    ElMessage.warning(
      `环境已更新，但公共环境缓存刷新失败：${getEnvironmentApiErrorMessage(error)}`
    );
  }
};

const nextEnvironmentSortOrder = () => {
  const highest = environmentCatalog.value.reduce(
    (current, item) => Math.max(current, item.sort_order),
    0
  );
  return highest + 10;
};

const createEnvironment = async () => {
  if (creatingEnvironment.value) return;
  const code = newEnvironment.code.trim().toLowerCase();
  if (!/^[a-z][a-z0-9._-]{0,62}$/.test(code)) {
    ElMessage.warning('环境代码需以小写字母开头，仅可包含小写字母、数字、点、下划线和连字符');
    return;
  }
  if (!validateEnvironmentEditor(newEnvironment)) return;

  creatingEnvironment.value = true;
  try {
    const created = normalizeEnvironment(
      await createSystemEnvironment(adminToken.value, {
        code,
        name: newEnvironment.name.trim(),
        enabled: newEnvironment.enabled,
        sort_order: newEnvironment.sort_order,
      })
    );
    environmentCatalog.value.push(created);
    sortEnvironmentCatalog();
    newEnvironment.code = '';
    newEnvironment.name = '';
    newEnvironment.enabled = true;
    newEnvironment.sort_order = nextEnvironmentSortOrder();
    await refreshPublicEnvironmentCatalog();
    ElMessage.success(`环境 ${created.code} 已创建`);
  } catch (error) {
    ElMessage.error(getEnvironmentApiErrorMessage(error));
  } finally {
    creatingEnvironment.value = false;
  }
};

const saveEnvironment = async (environment: EnvironmentEditor) => {
  if (savingEnvironmentCodes.has(environment.code)) return;
  if (!validateEnvironmentEditor(environment)) return;

  savingEnvironmentCodes.add(environment.code);
  try {
    const updated = normalizeEnvironment(
      await updateSystemEnvironment(adminToken.value, environment.code, {
        name: environment.name.trim(),
        sort_order: environment.sort_order,
      })
    );
    Object.assign(environment, updated);
    sortEnvironmentCatalog();
    await refreshPublicEnvironmentCatalog();
    ElMessage.success(`环境 ${environment.code} 已保存`);
  } catch (error) {
    ElMessage.error(getEnvironmentApiErrorMessage(error));
  } finally {
    savingEnvironmentCodes.delete(environment.code);
  }
};

const toggleEnvironment = async (
  environment: EnvironmentEditor,
  rawEnabled: string | number | boolean
) => {
  const enabled = Boolean(rawEnabled);
  if (enabled === environment.enabled || savingEnvironmentCodes.has(environment.code)) return;

  if (!enabled) {
    try {
      await ElMessageBox.confirm(
        `停用环境“${environment.name}（${environment.code}）”后，它将无法用于新建应用配置和发起发布；已有配置与历史记录会保留。确定继续吗？`,
        '确认停用环境',
        {
          type: 'warning',
          confirmButtonText: '确认停用',
          cancelButtonText: '取消',
        }
      );
    } catch {
      return;
    }
  }

  savingEnvironmentCodes.add(environment.code);
  try {
    const updated = await updateSystemEnvironment(adminToken.value, environment.code, { enabled });
    environment.enabled = updated.enabled;
    await refreshPublicEnvironmentCatalog();
    ElMessage.success(`环境 ${environment.code} 已${enabled ? '启用' : '停用'}`);
  } catch (error) {
    ElMessage.error(getEnvironmentApiErrorMessage(error));
  } finally {
    savingEnvironmentCodes.delete(environment.code);
  }
};

const statusText = (enabled: boolean, connected: boolean) => {
  if (!enabled) return '未启用';
  return connected ? '连接正常' : '连接异常';
};

const statusTagType = (enabled: boolean, connected: boolean) => {
  if (!enabled) return 'info';
  return connected ? 'success' : 'danger';
};

const validateJenkins = () => {
  if (
    !Number.isInteger(jenkinsForm.timeout_seconds) ||
    jenkinsForm.timeout_seconds < 1 ||
    jenkinsForm.timeout_seconds > MAX_INTEGRATION_TIMEOUT_SECONDS
  ) {
    ElMessage.warning(`Jenkins 请求超时必须在 1-${MAX_INTEGRATION_TIMEOUT_SECONDS} 秒之间`);
    return false;
  }
  if (jenkinsForm.enabled && !jenkinsForm.address.trim()) {
    ElMessage.warning('启用 Jenkins 前请填写服务地址');
    return false;
  }
  if (jenkinsForm.address.trim()) {
    try {
      const address = new URL(jenkinsForm.address.trim());
      if (!['http:', 'https:'].includes(address.protocol) || address.username || address.password) {
        throw new Error('invalid Jenkins address');
      }
    } catch {
      ElMessage.warning('Jenkins 服务地址必须是有效的 HTTP/HTTPS 地址，且不能包含凭据');
      return false;
    }
  }
  if (jenkinsForm.enabled && !jenkinsForm.username.trim()) {
    ElMessage.warning('启用 Jenkins 前请填写用户名');
    return false;
  }
  if (jenkinsForm.enabled && !jenkinsStatus.token_configured && !jenkinsForm.token.trim()) {
    ElMessage.warning('启用 Jenkins 前请填写 API Token');
    return false;
  }
  const identityChanged =
    jenkinsForm.address.trim() !== originalJenkinsIdentity.address ||
    jenkinsForm.username.trim() !== originalJenkinsIdentity.username;
  if (jenkinsStatus.token_configured && identityChanged && !jenkinsForm.token.trim()) {
    ElMessage.warning('修改 Jenkins 地址或用户名时必须重新输入 API Token');
    return false;
  }
  if (new TextEncoder().encode(jenkinsForm.token).byteLength > MAX_JENKINS_TOKEN_BYTES) {
    ElMessage.warning('Jenkins API Token 不能超过 64 KiB');
    return false;
  }
  return true;
};

const saveJenkins = async () => {
  if (loading.value || savingJenkins.value || savingKubernetes.value) return;
  if (!validateJenkins()) return;

  const payload: UpdateJenkinsIntegrationRequest = {
    enabled: jenkinsForm.enabled,
    address: jenkinsForm.address.trim(),
    username: jenkinsForm.username.trim(),
    timeout_seconds: jenkinsForm.timeout_seconds,
  };
  if (jenkinsForm.token.trim()) payload.token = jenkinsForm.token;

  savingJenkins.value = true;
  try {
    const settings = await updateJenkinsIntegration(adminToken.value, payload);
    applyJenkinsSettings(settings);
    ElMessage.success('Jenkins 配置已保存');
  } catch (error) {
    ElMessage.error(getSystemApiErrorMessage(error));
  } finally {
    savingJenkins.value = false;
  }
};

const isEnvironmentUsed = (environment: KubernetesEnvironment, currentKey: number) =>
  kubernetesForm.clusters.some(
    cluster => cluster.key !== currentKey && cluster.environment === environment
  );

const addCluster = () => {
  const environment = environmentOptions.value.find(
    option => !kubernetesForm.clusters.some(cluster => cluster.environment === option.value)
  )?.value;
  if (!environment) return;

  kubernetesForm.clusters.push(
    createClusterForm({
      environment,
      name: '',
      description: '',
      kubeconfig_configured: false,
    })
  );
};

const removeCluster = async (index: number) => {
  const cluster = kubernetesForm.clusters[index];
  if (!cluster) return;

  try {
    await ElMessageBox.confirm(
      `确定从配置中移除 ${cluster.environment} 环境的“${cluster.name || '未命名集群'}”吗？保存 Kubernetes 配置后生效。`,
      '移除集群',
      {
        type: 'warning',
        confirmButtonText: '移除',
        cancelButtonText: '取消',
      }
    );
  } catch {
    return;
  }

  const [removed] = kubernetesForm.clusters.splice(index, 1);
  if (removed) removed.kubeconfig = '';
};

const validateKubernetes = () => {
  if (
    !Number.isInteger(kubernetesForm.timeout_seconds) ||
    kubernetesForm.timeout_seconds < 1 ||
    kubernetesForm.timeout_seconds > MAX_INTEGRATION_TIMEOUT_SECONDS
  ) {
    ElMessage.warning(`Kubernetes 请求超时必须在 1-${MAX_INTEGRATION_TIMEOUT_SECONDS} 秒之间`);
    return false;
  }
  if (kubernetesForm.enabled && kubernetesForm.clusters.length === 0) {
    ElMessage.warning('启用 Kubernetes 前请至少添加一个集群');
    return false;
  }

  const selectedEnvironments = new Set<KubernetesEnvironment>();
  for (const cluster of kubernetesForm.clusters) {
    if (selectedEnvironments.has(cluster.environment)) {
      ElMessage.warning('同一环境只能配置一个 Kubernetes 集群');
      return false;
    }
    selectedEnvironments.add(cluster.environment);

    if (!cluster.name.trim()) {
      ElMessage.warning(`请填写 ${cluster.environment} 环境的集群名称`);
      return false;
    }
    if (!cluster.kubeconfig_configured && !cluster.kubeconfig.trim()) {
      ElMessage.warning(`请填写 ${cluster.environment} 环境的 Kubeconfig`);
      return false;
    }
    if (new TextEncoder().encode(cluster.kubeconfig).byteLength > MAX_KUBECONFIG_BYTES) {
      ElMessage.warning(`${cluster.environment} 环境的 Kubeconfig 不能超过 1 MiB`);
      return false;
    }
  }

  return true;
};

const saveKubernetes = async () => {
  if (loading.value || savingJenkins.value || savingKubernetes.value) return;
  if (!validateKubernetes()) return;

  const clusters: UpdateKubernetesClusterRequest[] = kubernetesForm.clusters.map(cluster => {
    const payload: UpdateKubernetesClusterRequest = {
      environment: cluster.environment,
      name: cluster.name.trim(),
      description: cluster.description.trim(),
    };
    if (cluster.kubeconfig.trim()) payload.kubeconfig = cluster.kubeconfig;
    return payload;
  });

  savingKubernetes.value = true;
  try {
    const settings = await updateKubernetesIntegration(adminToken.value, {
      enabled: kubernetesForm.enabled,
      timeout_seconds: kubernetesForm.timeout_seconds,
      clusters,
    });
    applyKubernetesSettings(settings);
    ElMessage.success('Kubernetes 配置已保存');
  } catch (error) {
    ElMessage.error(getSystemApiErrorMessage(error));
  } finally {
    savingKubernetes.value = false;
  }
};

onBeforeUnmount(() => {
  adminToken.value = '';
  jenkinsForm.token = '';
  kubernetesForm.clusters.forEach(cluster => {
    cluster.kubeconfig = '';
  });
});

onMounted(() => {
  loadEnvironments().catch(error => ElMessage.error(getSystemApiErrorMessage(error)));
});
</script>

<style scoped>
.settings-page {
  display: flex;
  flex-direction: column;
  gap: 20px;
  max-width: 1180px;
  margin: 0 auto;
}

.access-card,
.integration-card {
  border: 1px solid #e4e7ed;
}

.card-header h2,
.integration-title h3,
.cluster-heading h4 {
  margin: 0;
  color: #303133;
}

.card-header p,
.cluster-heading p {
  margin: 6px 0 0;
  color: #909399;
  font-size: 14px;
}

.section-title {
  margin: 0;
  color: #303133;
}

.section-description {
  margin: 6px 0 0;
  color: #909399;
  font-size: 14px;
}

.token-row {
  display: grid;
  grid-template-columns: minmax(280px, 620px) auto;
  gap: 12px;
  margin-top: 18px;
  align-items: center;
}

.integration-header,
.integration-title,
.cluster-heading,
.cluster-editor-header,
.form-actions {
  display: flex;
  align-items: center;
}

.integration-header,
.cluster-heading,
.cluster-editor-header {
  justify-content: space-between;
  gap: 16px;
}

.integration-title {
  gap: 12px;
}

.status-error {
  margin-bottom: 18px;
}

.integration-form {
  max-width: 100%;
}

.environment-create-form {
  display: grid;
  grid-template-columns: minmax(160px, 1fr) minmax(180px, 1.3fr) 150px 110px auto;
  gap: 16px;
  align-items: end;
  padding-bottom: 18px;
  margin-bottom: 18px;
  border-bottom: 1px solid #ebeef5;
}

.environment-create-form :deep(.el-form-item) {
  margin-bottom: 0;
}

.environment-create-action :deep(.el-form-item__content) {
  justify-content: flex-end;
}

.environment-table {
  width: 100%;
}

.environment-sort-input {
  width: 120px;
}

.form-grid,
.cluster-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 20px;
}

.secret-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
  color: #909399;
  font-size: 12px;
  line-height: 1.5;
}

.field-hint {
  margin-top: 6px;
  color: #909399;
  font-size: 12px;
  line-height: 1.5;
}

.cluster-heading {
  margin: 8px 0 16px;
}

.cluster-editor {
  padding: 18px;
  margin-bottom: 16px;
  border: 1px solid #dcdfe6;
  border-radius: 6px;
  background: #fafafa;
}

.cluster-editor-header {
  margin-bottom: 12px;
  color: #606266;
}

.full-width {
  width: 100%;
}

.form-actions {
  justify-content: flex-end;
  padding-top: 4px;
}

@media (max-width: 760px) {
  .token-row,
  .form-grid,
  .cluster-grid,
  .environment-create-form {
    grid-template-columns: 1fr;
  }

  .token-row :deep(.el-button) {
    width: 100%;
  }

  .integration-header,
  .cluster-heading {
    align-items: flex-start;
  }

  .environment-create-action :deep(.el-button) {
    width: 100%;
  }
}
</style>
