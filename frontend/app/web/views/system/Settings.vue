<template>
  <div class="settings-page">
    <el-card class="access-card" shadow="never">
      <template #header>
        <div class="card-header">
          <div>
            <h2>系统集成配置</h2>
            <p>在运行时配置 Jenkins 与 Kubernetes，无需重启服务。</p>
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
          :disabled="savingJenkins || savingKubernetes"
          @click="loadSettings"
        >
          加载配置
        </el-button>
      </div>
    </el-card>

    <el-empty v-if="!loaded" description="输入管理员令牌后加载系统集成配置" />

    <template v-else>
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
import { onBeforeUnmount, reactive, ref } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import {
  getIntegrationSettings,
  getSystemApiErrorMessage,
  updateJenkinsIntegration,
  updateKubernetesIntegration,
} from '@/services/system';
import type {
  JenkinsIntegrationSettings,
  KubernetesClusterSettings,
  KubernetesEnvironment,
  KubernetesIntegrationSettings,
  UpdateJenkinsIntegrationRequest,
  UpdateKubernetesClusterRequest,
} from '@/types/system';

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

const environmentOptions: { label: string; value: KubernetesEnvironment }[] = [
  { label: '开发环境（dev）', value: 'dev' },
  { label: '测试环境（test）', value: 'test' },
  { label: '预发布环境（moni）', value: 'moni' },
];
const environmentOrder: Record<KubernetesEnvironment, number> = {
  dev: 0,
  test: 1,
  moni: 2,
};
const MAX_INTEGRATION_TIMEOUT_SECONDS = 120;
const MAX_JENKINS_TOKEN_BYTES = 64 * 1024;
const MAX_KUBECONFIG_BYTES = 1024 * 1024;

const adminToken = ref('');
const loading = ref(false);
const loaded = ref(false);
const savingJenkins = ref(false);
const savingKubernetes = ref(false);
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
    .sort((left, right) => environmentOrder[left.environment] - environmentOrder[right.environment])
    .map(createClusterForm);
  kubernetesStatus.enabled = settings.enabled;
  kubernetesStatus.connected = settings.connected;
  kubernetesStatus.last_error = settings.last_error;
};

const loadSettings = async () => {
  if (loading.value || savingJenkins.value || savingKubernetes.value) return;
  if (!adminToken.value.trim()) {
    ElMessage.warning('请输入管理员令牌');
    return;
  }

  loading.value = true;
  try {
    const settings = await getIntegrationSettings(adminToken.value);
    applyJenkinsSettings(settings.jenkins);
    applyKubernetesSettings(settings.kubernetes);
    loaded.value = true;
    ElMessage.success('系统集成配置已加载');
  } catch (error) {
    loaded.value = false;
    ElMessage.error(getSystemApiErrorMessage(error));
  } finally {
    loading.value = false;
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
  const environment = environmentOptions.find(
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

  const environments = new Set<KubernetesEnvironment>();
  for (const cluster of kubernetesForm.clusters) {
    if (environments.has(cluster.environment)) {
      ElMessage.warning('同一环境只能配置一个 Kubernetes 集群');
      return false;
    }
    environments.add(cluster.environment);

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
  .cluster-grid {
    grid-template-columns: 1fr;
  }

  .token-row :deep(.el-button) {
    width: 100%;
  }

  .integration-header,
  .cluster-heading {
    align-items: flex-start;
  }
}
</style>
