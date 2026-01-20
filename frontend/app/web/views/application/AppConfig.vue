<template>
  <div class="app-config-page">
    <el-card class="mb-16">
      <template #header>
        <div class="card-header">
          <span>应用配置</span>
        </div>
      </template>

      <div class="toolbar">
        <el-form
          :inline="true"
          label-width="100px"
          class="search-form"
          @keyup.enter.prevent="queryApplications()"
        >
          <el-form-item label="APPID">
            <el-input
              v-model="appIdKeyword"
              clearable
              placeholder="如 10001"
              class="filter-control"
            />
          </el-form-item>
          <el-form-item label="应用名称">
            <el-select
              v-model="selectedAppName"
              filterable
              clearable
              :loading="appsLoading"
              class="filter-control"
              placeholder="请选择应用名称"
            >
              <el-option v-for="name in appNameOptions" :key="name" :label="name" :value="name" />
            </el-select>
          </el-form-item>
          <el-form-item label="负责人">
            <el-input
              v-model="appQuery.owner"
              placeholder="owner"
              clearable
              class="filter-control"
            />
          </el-form-item>
          <el-form-item label="负责人中文">
            <el-input
              v-model="appQuery.owner_cn"
              placeholder="owner_cn"
              clearable
              class="filter-control"
            />
          </el-form-item>
          <el-form-item label="语言">
            <el-select
              v-model="appQuery.dev_language"
              placeholder="dev_language"
              clearable
              class="filter-control"
            >
              <el-option
                v-for="opt in devLanguageOptions"
                :key="opt.value"
                :label="opt.label"
                :value="opt.value"
              />
            </el-select>
          </el-form-item>
          <el-form-item class="actions-item">
            <el-button type="primary" :loading="appsLoading" @click="queryApplications()">
              查询应用
            </el-button>
            <el-button :disabled="appsLoading" @click="resetAppQuery()">重置</el-button>
            <el-button :disabled="!selectedAppId" :loading="configsLoading" @click="fetchConfigs">
              获取配置
            </el-button>
          </el-form-item>
        </el-form>

        <div class="app-meta" />
      </div>

      <el-divider content-position="left">应用基本信息</el-divider>

      <el-empty
        v-if="queriedApps.length === 0"
        description="请使用上方条件查询应用"
        :image-size="60"
      />

      <div v-else class="app-result">
        <el-table
          :data="queriedApps"
          border
          style="width: 100%"
          highlight-current-row
          row-key="app_id"
          @row-click="handleSelectApp"
        >
          <el-table-column label="APPID" prop="app_id" width="120" />
          <el-table-column label="应用名称" prop="app_name" min-width="200" />
          <el-table-column label="负责人" prop="owner" width="140" />
          <el-table-column label="负责人中文" prop="owner_cn" width="160" />
          <el-table-column label="语言" prop="dev_language" width="120" />
        </el-table>

        <el-descriptions v-if="selectedApp" :column="3" border class="mt-12">
          <el-descriptions-item label="APPID">{{ selectedApp.app_id }}</el-descriptions-item>
          <el-descriptions-item label="应用名称">{{ selectedApp.app_name }}</el-descriptions-item>
          <el-descriptions-item label="应用中文名">{{
            selectedApp.app_name_cn
          }}</el-descriptions-item>
          <el-descriptions-item label="负责人">{{ selectedApp.owner }}</el-descriptions-item>
          <el-descriptions-item label="负责人中文">{{ selectedApp.owner_cn }}</el-descriptions-item>
          <el-descriptions-item label="语言">{{ selectedApp.dev_language }}</el-descriptions-item>
          <el-descriptions-item label="Git URL" :span="3">{{
            selectedApp.git_url
          }}</el-descriptions-item>
          <el-descriptions-item label="描述" :span="3">{{
            selectedApp.description_cn
          }}</el-descriptions-item>
        </el-descriptions>
      </div>
    </el-card>

    <el-card v-loading="configsLoading" class="config-card">
      <template #header>
        <div class="card-header">
          <span>环境配置</span>
          <span class="hint">
            PATCH 仅更新提交字段；多域名建议使用下方「多域名」管理（基于 config_id）。
          </span>
        </div>
      </template>

      <el-empty
        v-if="!selectedAppId"
        description="请先查询并选择一个应用，再点击「获取配置」"
        :image-size="80"
      />

      <el-tabs v-else v-model="activeEnv" class="env-tabs" @tab-change="handleEnvChange">
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
          </div>

          <div v-else class="env-content">
            <el-form
              :model="formsByEnv[env.value]"
              label-width="140px"
              class="config-form"
              @submit.prevent
            >
              <el-divider content-position="left">基础</el-divider>
              <el-row :gutter="16">
                <el-col :span="12">
                  <el-form-item label="应用实例数量">
                    <el-input-number v-model="formsByEnv[env.value].pod_count" :min="0" :step="1" />
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
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="8">
                  <el-form-item label="GPU数量">
                    <el-input-number v-model="formsByEnv[env.value].gpu_count" :min="0" :step="1" />
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
                      @change="handleProbeTypeChange(env.value)"
                    >
                      <el-option label="HTTP" value="HTTP" />
                      <el-option label="TCP" value="TCP" />
                    </el-select>
                  </el-form-item>
                </el-col>
                <el-col :span="16">
                  <el-form-item
                    v-if="formsByEnv[env.value].probe_type === 'HTTP'"
                    label="健康监测探针路径"
                  >
                    <el-input
                      v-model="formsByEnv[env.value].probe_check_path"
                      placeholder="如 /ttpai/inside/checkup"
                    />
                  </el-form-item>
                  <el-form-item v-else-if="formsByEnv[env.value].probe_type === 'TCP'" label="端口">
                    <el-input-number
                      v-model="formsByEnv[env.value].probe_check_port"
                      :min="1"
                      :max="65535"
                      :step="1"
                      placeholder="如 8080"
                    />
                  </el-form-item>
                </el-col>
              </el-row>

              <el-divider content-position="left">PreStop</el-divider>
              <el-row :gutter="16">
                <el-col :span="8">
                  <el-form-item label="开启">
                    <el-switch
                      v-model="preStopEnabledByEnv[env.value]"
                      inline-prompt
                      active-text="开"
                      inactive-text="关"
                      @change="handlePreStopToggle(env.value)"
                    />
                  </el-form-item>
                </el-col>
                <el-col v-if="preStopEnabledByEnv[env.value]" :span="8">
                  <el-form-item label="URL">
                    <el-input
                      v-model="formsByEnv[env.value].pre_stop_check_path"
                      placeholder="如 /ttpai/inside/prestop"
                    />
                  </el-form-item>
                </el-col>
                <el-col v-if="preStopEnabledByEnv[env.value]" :span="8">
                  <el-form-item label="端口">
                    <el-input-number
                      v-model="formsByEnv[env.value].pre_stop_check_port"
                      :min="1"
                      :max="65535"
                      :step="1"
                      placeholder="如 8080"
                    />
                  </el-form-item>
                </el-col>
              </el-row>

              <div class="actions">
                <el-button
                  type="primary"
                  :loading="savingConfig"
                  :disabled="!selectedAppId"
                  @click="saveEnvConfig(env.value)"
                >
                  保存环境配置
                </el-button>
              </div>
            </el-form>

            <el-divider content-position="left">多域名（Ingress host/path）</el-divider>
            <el-alert
              type="info"
              show-icon
              :closable="false"
              title="说明"
              description="服务端会对 host/path 做规范化并校验冲突（同 host + path 不可重复）。建议编辑后点击「保存多域名」使用 PUT 覆盖写入。"
              class="mb-12"
            />

            <div class="domain-toolbar">
              <el-button
                type="primary"
                plain
                :disabled="!configsByEnv[env.value]?.config_id"
                @click="addDomainRow(env.value)"
              >
                新增域名
              </el-button>
              <el-button
                type="primary"
                :loading="savingDomains"
                :disabled="!configsByEnv[env.value]?.config_id"
                @click="saveDomains(env.value)"
              >
                保存多域名
              </el-button>
              <el-button
                :loading="domainsLoadingByEnv[env.value]"
                :disabled="!configsByEnv[env.value]?.config_id"
                @click="loadDomains(env.value, true)"
              >
                刷新
              </el-button>
            </div>

            <el-table
              v-loading="domainsLoadingByEnv[env.value]"
              :data="domainsByEnv[env.value]"
              border
              style="width: 100%"
              empty-text="暂无多域名配置"
            >
              <el-table-column label="host" min-width="240">
                <template #default="{ row }">
                  <el-input v-model="row.host" placeholder="a.example.com" />
                </template>
              </el-table-column>
              <el-table-column label="path" min-width="200">
                <template #default="{ row }">
                  <el-input v-model="row.path" placeholder="/" />
                </template>
              </el-table-column>
              <el-table-column label="操作" width="120" fixed="right">
                <template #default="{ $index }">
                  <el-button type="danger" link @click="removeDomainRow(env.value, $index)">
                    删除
                  </el-button>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue';
import { ElMessage } from 'element-plus';
import {
  DevLanguage,
  type AppConfig,
  type AppEnv,
  type AppInfo,
  type DomainItem,
  type UpdateAppConfigRequest,
} from '@/models/application';
import {
  getAllAppNames,
  getAppConfigs,
  queryApps,
  getAppConfigDomains,
  patchAppConfigByEnv,
  upsertAppConfigDomains,
} from '@/services/application';

const envOptions: Array<{ label: string; value: AppEnv }> = [
  { label: '开发(dev)', value: 'dev' },
  { label: '测试(test)', value: 'test' },
  { label: '模拟(moni)', value: 'moni' },
];

// 应用选择
const selectedAppName = ref<string | null>(null);
const appNameOptions = ref<string[]>([]);
const allAppNameOptions = ref<string[]>([]);
const queriedApps = ref<AppInfo[]>([]);
const selectedApp = ref<AppInfo | null>(null);
const appsLoading = ref(false);
const appIdKeyword = ref('');

// 应用查询条件（复用应用信息查询接口参数）
const appQuery = reactive<{
  app_name?: string;
  owner?: string;
  owner_cn?: string;
  dev_language?: DevLanguage;
}>({
  app_name: undefined,
  owner: undefined,
  owner_cn: undefined,
  dev_language: undefined,
});

const devLanguageOptions = [
  { label: 'JAVA', value: DevLanguage.JAVA },
  { label: 'PYTHON', value: DevLanguage.PYTHON },
  { label: 'GOLANG', value: DevLanguage.GO },
  { label: 'NODE.JS', value: DevLanguage.NODE },
];

const selectedAppId = computed(() => selectedApp.value?.app_id ?? null);

// 配置数据
const activeEnv = ref<AppEnv>('dev');
const configsLoading = ref(false);
const savingConfig = ref(false);
const savingDomains = ref(false);

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

// PreStop 开关（按 env）
const preStopEnabledByEnv = reactive<Record<AppEnv, boolean>>({
  dev: false,
  test: false,
  moni: false,
});

const handleProbeTypeChange = (env: AppEnv) => {
  const probeType = formsByEnv[env].probe_type;
  if (probeType === 'HTTP') {
    formsByEnv[env].probe_check_port = undefined;
  } else if (probeType === 'TCP') {
    formsByEnv[env].probe_check_path = undefined;
  } else {
    // 清空
    formsByEnv[env].probe_check_path = undefined;
    formsByEnv[env].probe_check_port = undefined;
  }
};

const handlePreStopToggle = (env: AppEnv) => {
  if (preStopEnabledByEnv[env]) {
    // 当前需求：开启后使用 HTTP URL + 端口
    formsByEnv[env].pre_stop_type = 'HTTP';
  } else {
    formsByEnv[env].pre_stop_type = undefined;
    formsByEnv[env].pre_stop_check_path = undefined;
    formsByEnv[env].pre_stop_check_port = undefined;
    formsByEnv[env].pre_stop_command = undefined;
  }
};

// 多域名
const domainsByEnv = reactive<Record<AppEnv, DomainItem[]>>({
  dev: [],
  test: [],
  moni: [],
});
const domainsLoadingByEnv = reactive<Record<AppEnv, boolean>>({
  dev: false,
  test: false,
  moni: false,
});

const queryApplications = async () => {
  appsLoading.value = true;
  try {
    const appId = Number(appIdKeyword.value);
    const appIdParam = Number.isFinite(appId) && appId > 0 ? appId : undefined;
    const response = await queryApps({
      page_num: 1,
      page_size: 50,
      app_name: selectedAppName.value || undefined,
      owner: appQuery.owner || undefined,
      owner_cn: appQuery.owner_cn || undefined,
      dev_language: appQuery.dev_language,
      // 兼容：如果后端 apps/query 支持 app_id，就直接传；不支持则忽略（不影响其他条件）
      ...(appIdParam ? ({ app_id: appIdParam } as any) : {}),
    });
    if (response.data.code === 1) {
      const apps = response.data.result?.apps || [];
      queriedApps.value = Array.isArray(apps) ? apps : [];

      // 选中逻辑：只有 1 条时自动选中；多条时保留旧选中（如果还在结果里），否则清空
      if (queriedApps.value.length === 1) {
        selectedApp.value = queriedApps.value[0];
      } else if (selectedApp.value) {
        const stillExists = queriedApps.value.some(a => a.app_id === selectedApp.value?.app_id);
        if (!stillExists) selectedApp.value = null;
      } else {
        selectedApp.value = null;
      }

      // 查询应用后清空已加载的配置，避免看见上一个应用的配置
      resetConfigs();
    } else {
      ElMessage.error(response.data.message || '查询应用失败');
    }
  } catch (e) {
    console.error(e);
    ElMessage.error('查询应用失败');
  } finally {
    appsLoading.value = false;
  }
};

const loadAllAppNames = async () => {
  appsLoading.value = true;
  try {
    const resp = await getAllAppNames();
    if (resp.data.code !== 1) {
      throw new Error(resp.data.message || '获取应用名称失败');
    }
    const names = resp.data.result || [];
    allAppNameOptions.value = Array.isArray(names) ? names : [];
    appNameOptions.value = [...allAppNameOptions.value];
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '获取应用名称失败');
  } finally {
    appsLoading.value = false;
  }
};

const handleSelectApp = (row: AppInfo) => {
  selectedApp.value = row;
  // 选择应用后清空已加载的配置，避免看见上一个应用的配置
  resetConfigs();
};

const resetAppQuery = async () => {
  appIdKeyword.value = '';
  appQuery.app_name = undefined;
  appQuery.owner = undefined;
  appQuery.owner_cn = undefined;
  appQuery.dev_language = undefined;
  selectedAppName.value = null;
  selectedApp.value = null;
  queriedApps.value = [];
  appNameOptions.value = [...allAppNameOptions.value];
};

const resetConfigs = () => {
  envOptions.forEach(env => {
    configsByEnv[env.value] = null;
    formsByEnv[env.value] = {};
    domainsByEnv[env.value] = [];
  });
};

const fetchConfigs = async () => {
  if (!selectedAppId.value) return;
  configsLoading.value = true;
  try {
    const resp = await getAppConfigs(selectedAppId.value);
    if (resp.data.code !== 1) {
      throw new Error(resp.data.message || '获取应用配置失败');
    }

    resetConfigs();
    for (const cfg of resp.data.result || []) {
      if (cfg.env === 'dev' || cfg.env === 'test' || cfg.env === 'moni') {
        configsByEnv[cfg.env] = cfg;
        // 表单初始化：只取可 PATCH 的字段
        formsByEnv[cfg.env] = {
          code_package_type: cfg.code_package_type || undefined,
          code_package_path: cfg.code_package_path || undefined,
          code_package_name: cfg.code_package_name || undefined,
          base_image: cfg.base_image || undefined,
          pod_count: cfg.pod_count ?? undefined,
          limits_memory: cfg.limits_memory ?? undefined,
          gpu_count: cfg.gpu_count ?? undefined,
          probe_type: cfg.probe_type || undefined,
          probe_check_path: cfg.probe_check_path || undefined,
          probe_check_port: (cfg as any).probe_check_port ?? undefined,
          pre_stop_type: cfg.pre_stop_type || undefined,
          pre_stop_check_path: cfg.pre_stop_check_path || undefined,
          pre_stop_check_port: (cfg as any).pre_stop_check_port ?? undefined,
          pre_stop_command: cfg.pre_stop_command || undefined,
        };
        // 初始化开关状态
        preStopEnabledByEnv[cfg.env] = Boolean(
          formsByEnv[cfg.env].pre_stop_check_path ||
            formsByEnv[cfg.env].pre_stop_check_port ||
            formsByEnv[cfg.env].pre_stop_type
        );
      }
    }

    // 默认加载当前 env 的多域名
    await loadDomains(activeEnv.value, true);
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '获取应用配置失败');
  } finally {
    configsLoading.value = false;
  }
};

const loadDomains = async (env: AppEnv, force = false) => {
  const cfg = configsByEnv[env];
  if (!cfg?.config_id) return;
  if (!force && domainsByEnv[env].length > 0) return;
  domainsLoadingByEnv[env] = true;
  try {
    const resp = await getAppConfigDomains(cfg.config_id);
    if (resp.data.code !== 1) {
      throw new Error(resp.data.message || '获取多域名失败');
    }
    domainsByEnv[env] = (resp.data.result || []).map(d => ({ host: d.host, path: d.path }));
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '获取多域名失败');
  } finally {
    domainsLoadingByEnv[env] = false;
  }
};

const addDomainRow = (env: AppEnv) => {
  domainsByEnv[env].push({ host: '', path: '/' });
};

const removeDomainRow = (env: AppEnv, index: number) => {
  domainsByEnv[env].splice(index, 1);
};

const saveEnvConfig = async (env: AppEnv) => {
  if (!selectedAppId.value) return;
  savingConfig.value = true;
  try {
    const payload: UpdateAppConfigRequest = { ...formsByEnv[env] };
    const resp = await patchAppConfigByEnv(selectedAppId.value, env, payload);
    if (resp.data.code !== 1) {
      throw new Error(resp.data.message || '保存失败');
    }
    ElMessage.success('保存成功');
    await fetchConfigs();
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '保存失败');
  } finally {
    savingConfig.value = false;
  }
};

const saveDomains = async (env: AppEnv) => {
  const cfg = configsByEnv[env];
  if (!cfg?.config_id) return;
  savingDomains.value = true;
  try {
    const payload = {
      domains: domainsByEnv[env].map(d => ({
        host: (d.host || '').trim(),
        path: d.path || '/',
      })),
    };
    const resp = await upsertAppConfigDomains(cfg.config_id, payload);
    if (resp.data.code !== 1) {
      throw new Error(resp.data.message || '保存多域名失败');
    }
    ElMessage.success('多域名保存成功');
    await loadDomains(env, true);
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '保存多域名失败');
  } finally {
    savingDomains.value = false;
  }
};

const handleEnvChange = async (name: string | number) => {
  const env = name as AppEnv;
  await loadDomains(env, false);
};

onMounted(async () => {
  await loadAllAppNames();
});
</script>

<style scoped>
.app-config-page {
  padding: 20px;
}

.mb-16 {
  margin-bottom: 16px;
}

.ml-8 {
  margin-left: 8px;
}

.mb-12 {
  margin-bottom: 12px;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.hint {
  font-size: 12px;
  color: #909399;
}

.toolbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.search-form :deep(.el-form-item) {
  margin-bottom: 10px;
}

.search-form :deep(.el-form-item) {
  margin-right: 12px;
}

.search-form {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
}

.actions-item {
  margin-left: auto;
}

.actions-item :deep(.el-form-item__content) {
  display: flex;
  gap: 8px;
}

.filter-control {
  width: 220px;
}

.filter-control :deep(.el-input__wrapper),
.filter-control :deep(.el-select__wrapper) {
  width: 100%;
}

.mt-12 {
  margin-top: 12px;
}

.app-meta {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
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

.domain-toolbar {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.empty-env {
  padding: 12px 0;
}
</style>
