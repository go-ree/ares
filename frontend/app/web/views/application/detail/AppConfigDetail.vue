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
              title="该环境暂无配置记录（接口未返回 config）。"
              description="如需初始化该环境配置，请联系后端补齐配置记录后再编辑。"
            />
            <div class="empty-actions">
              <el-button
                type="primary"
                :loading="creatingEnv === env.value"
                :disabled="loading || saving"
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
                      placeholder="如 /ttpai/inside/checkup"
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
                      placeholder="如 /ttpai/inside/prestop"
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
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { ElMessage, ElMessageBox } from 'element-plus';
import type { AppConfig, AppEnv, AppInfo, UpdateAppConfigRequest } from '@/models/application';
import {
  createAppConfig,
  getAppConfigs,
  getAppDetail,
  patchAppConfigByEnv,
} from '@/services/application';

const route = useRoute();
const appId = ref<number>(Number(route.params.appId));
const appDetail = ref<AppInfo | null>(null);
const displayName = ref<string>(
  typeof route.query.name === 'string' && route.query.name
    ? route.query.name
    : `应用 ${appId.value}`
);

const envOptions: Array<{ label: string; value: AppEnv }> = [
  { label: '开发(dev)', value: 'dev' },
  { label: '测试(test)', value: 'test' },
  { label: '模拟(moni)', value: 'moni' },
];

const activeEnv = ref<AppEnv>('dev');
const loading = ref(false);
const saving = ref(false);
const creatingEnv = ref<AppEnv | null>(null);

const configsByEnv = reactive<Record<AppEnv, AppConfig | null>>({
  dev: null,
  test: null,
  moni: null,
});

const formsByEnv = reactive<Record<AppEnv, UpdateAppConfigRequest>>({
  dev: {},
  test: {},
  moni: {},
});

const originalFormsByEnv = reactive<Record<AppEnv, UpdateAppConfigRequest>>({
  dev: {},
  test: {},
  moni: {},
});

const isEditingByEnv = reactive<Record<AppEnv, boolean>>({
  dev: false,
  test: false,
  moni: false,
});

const reset = () => {
  envOptions.forEach(env => {
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
    formsByEnv[env].probe_check_path = undefined;
    formsByEnv[env].probe_check_http_port = undefined;
    if (!formsByEnv[env].probe_check_tcp_port) {
      formsByEnv[env].probe_check_tcp_port = formsByEnv[env].container_port;
    }
    // 兼容清理
    formsByEnv[env].probe_check_port = undefined;
  } else {
    formsByEnv[env].probe_check_path = undefined;
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
    formsByEnv[env].pre_stop_command = undefined;
    if (!formsByEnv[env].probe_stop_check_http_port) {
      formsByEnv[env].probe_stop_check_http_port =
        formsByEnv[env].probe_check_http_port || formsByEnv[env].container_port;
    }
  } else if (t === 'command') {
    // COMMAND：只命令
    formsByEnv[env].pre_stop_check_path = undefined;
    formsByEnv[env].probe_stop_check_http_port = undefined;
    formsByEnv[env].pre_stop_check_port = undefined;
  } else {
    // 清空
    formsByEnv[env].pre_stop_type = undefined;
    formsByEnv[env].pre_stop_check_path = undefined;
    formsByEnv[env].pre_stop_check_port = undefined;
    formsByEnv[env].pre_stop_command = undefined;
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
    for (const cfg of resp.data.result || []) {
      if (cfg.env === 'dev' || cfg.env === 'test' || cfg.env === 'moni') {
        configsByEnv[cfg.env] = cfg;
        const form: UpdateAppConfigRequest = {
          code_package_type: cfg.code_package_type || undefined,
          code_package_path: cfg.code_package_path || undefined,
          code_package_name: cfg.code_package_name || undefined,
          base_image: cfg.base_image || undefined,
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
          pre_stop_command: cfg.pre_stop_command || undefined,
        };
        formsByEnv[cfg.env] = { ...form };
        originalFormsByEnv[cfg.env] = { ...form };
        isEditingByEnv[cfg.env] = false;
      }
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

const saveEnvConfig = async (env: AppEnv) => {
  if (!Number.isFinite(appId.value) || appId.value <= 0) return;
  if (!isEditingByEnv[env]) return;
  saving.value = true;
  try {
    const payload: UpdateAppConfigRequest = { ...formsByEnv[env] };
    // 兼容：避免把历史字段 probe_check_port 误提交；统一走 probe_check_tcp_port
    if (payload.probe_type === 'TCP') {
      payload.probe_check_path = undefined;
      payload.probe_check_http_port = undefined;
    } else if (payload.probe_type === 'HTTP') {
      payload.probe_check_tcp_port = undefined;
      payload.probe_check_http_port =
        payload.probe_check_http_port ?? payload.container_port ?? undefined;
    }

    // PreStop
    // - HTTP：类型+路径+端口（probe_stop_check_http_port）
    // - command：类型+命令（pre_stop_command）
    if (payload.pre_stop_type === 'HTTP') {
      payload.pre_stop_command = undefined;
      payload.probe_stop_check_http_port =
        payload.probe_stop_check_http_port ??
        payload.probe_check_http_port ??
        payload.container_port ??
        undefined;
      // 兼容字段不再使用
      payload.pre_stop_check_port = undefined;
    } else if (payload.pre_stop_type === 'command') {
      payload.pre_stop_check_path = undefined;
      payload.probe_stop_check_http_port = undefined;
      payload.pre_stop_check_port = undefined;
    } else {
      payload.pre_stop_type = undefined;
      payload.pre_stop_check_path = undefined;
      payload.pre_stop_check_port = undefined;
      payload.pre_stop_command = undefined;
      payload.probe_stop_check_http_port = undefined;
    }

    delete (payload as any).probe_check_port;
    const resp = await patchAppConfigByEnv(appId.value, env, payload);
    if (resp.data.code !== 1) throw new Error(resp.data.message || '保存失败');
    ElMessage.success('保存成功');
    await ElMessageBox.alert('环境配置保存成功', '保存结果', { type: 'success' });
    await fetchConfigs();
    isEditingByEnv[env] = false;
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '保存失败');
    await ElMessageBox.alert(e instanceof Error ? e.message : '保存失败', '保存结果', {
      type: 'error',
    });
  } finally {
    saving.value = false;
  }
};

const refresh = async () => {
  await fetchConfigs();
};

const handleEnvChange = async (name: string | number) => {
  const nextEnv = name as AppEnv;
  // 切换 tab 时，为避免误改，自动退出当前环境的编辑态（保留数据可再点编辑）
  if (isEditingByEnv[activeEnv.value]) {
    cancelEdit(activeEnv.value);
  }
  activeEnv.value = nextEnv;
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
  await fetchAppName();
  await fetchConfigs();
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
</style>
