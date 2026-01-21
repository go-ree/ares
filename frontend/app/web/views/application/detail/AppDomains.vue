<template>
  <div class="app-domains">
    <el-card v-loading="loading" shadow="never" class="domains-card">
      <template #header>
        <div class="card-header">
          <span>多域名（Ingress host/path）</span>
          <span class="hint">按环境切换，编辑后点击「保存」会以 PUT 覆盖写入。</span>
        </div>
      </template>

      <el-tabs v-model="activeEnv" @tab-change="handleEnvChange">
        <el-tab-pane v-for="env in envOptions" :key="env.value" :name="env.value">
          <template #label>
            <span>{{ env.label }}</span>
            <el-tag
              v-if="configsByEnv[env.value]?.config_id"
              size="small"
              type="success"
              class="ml-8"
            >
              可编辑
            </el-tag>
            <el-tag v-else size="small" type="info" class="ml-8">无配置</el-tag>
          </template>

          <div v-if="!configsByEnv[env.value]?.config_id" class="empty-env">
            <el-empty description="该环境暂无配置记录，无法管理多域名" :image-size="80" />
            <div class="empty-actions">
              <el-button
                type="primary"
                :loading="creatingEnv === env.value"
                :disabled="loading || saving || domainsLoading"
                @click="createEnvConfig(env.value)"
              >
                创建该环境配置
              </el-button>
            </div>
          </div>

          <div v-else class="content">
            <el-alert
              type="info"
              show-icon
              :closable="false"
              title="说明"
              description="服务端会对 host/path 做规范化并校验冲突（同 host + path 不可重复）。建议编辑后点击「保存」使用 PUT 幂等覆盖写入。"
              class="mb-12"
            />

            <div class="toolbar">
              <el-button
                v-if="!isEditingByEnv[env.value]"
                type="primary"
                :disabled="loading || saving || domainsLoading"
                @click="startEdit(env.value)"
              >
                编辑
              </el-button>
              <template v-else>
                <el-button
                  type="primary"
                  plain
                  :disabled="loading || saving || domainsLoading"
                  @click="addRow(env.value)"
                >
                  新增域名
                </el-button>
                <el-button
                  type="primary"
                  :loading="saving"
                  :disabled="loading || domainsLoading"
                  @click="save(env.value)"
                >
                  保存
                </el-button>
                <el-button
                  :disabled="loading || saving || domainsLoading"
                  @click="cancelEdit(env.value)"
                  >取消</el-button
                >
              </template>
              <el-button
                v-if="!isEditingByEnv[env.value]"
                :loading="domainsLoading"
                :disabled="loading || saving"
                @click="loadDomains(env.value, true)"
              >
                刷新
              </el-button>
            </div>

            <el-table
              v-loading="domainsLoading"
              :data="domainsByEnv[activeEnv]"
              border
              style="width: 100%"
              empty-text="暂无多域名配置"
            >
              <el-table-column label="host" min-width="260">
                <template #default="{ row }">
                  <el-input
                    v-if="isEditingByEnv[activeEnv]"
                    v-model="row.host"
                    placeholder="a.example.com"
                  />
                  <span v-else class="mono">{{ row.host || '-' }}</span>
                </template>
              </el-table-column>
              <el-table-column label="path" min-width="220">
                <template #default="{ row }">
                  <el-input v-if="isEditingByEnv[activeEnv]" v-model="row.path" placeholder="/" />
                  <span v-else class="mono">{{ row.path || '/' }}</span>
                </template>
              </el-table-column>
              <el-table-column
                v-if="isEditingByEnv[activeEnv]"
                label="操作"
                width="120"
                fixed="right"
              >
                <template #default="{ $index }">
                  <el-button type="danger" link @click="removeRow(activeEnv, $index)"
                    >删除</el-button
                  >
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
import { onMounted, reactive, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { ElMessage, ElMessageBox } from 'element-plus';
import type { AppConfig, AppEnv, DomainItem } from '@/models/application';
import {
  createAppConfig,
  getAppConfigDomains,
  getAppConfigs,
  upsertAppConfigDomains,
} from '@/services/application';

const route = useRoute();
const appId = ref<number>(Number(route.params.appId));

const envOptions: Array<{ label: string; value: AppEnv }> = [
  { label: '开发(dev)', value: 'dev' },
  { label: '测试(test)', value: 'test' },
  { label: '模拟(moni)', value: 'moni' },
];

const activeEnv = ref<AppEnv>('dev');
const loading = ref(false);
const saving = ref(false);
const domainsLoading = ref(false);
const creatingEnv = ref<AppEnv | null>(null);

const configsByEnv = reactive<Record<AppEnv, AppConfig | null>>({
  dev: null,
  test: null,
  moni: null,
});

const domainsByEnv = reactive<Record<AppEnv, DomainItem[]>>({
  dev: [],
  test: [],
  moni: [],
});

const originalDomainsByEnv = reactive<Record<AppEnv, DomainItem[]>>({
  dev: [],
  test: [],
  moni: [],
});

const isEditingByEnv = reactive<Record<AppEnv, boolean>>({
  dev: false,
  test: false,
  moni: false,
});

const reset = () => {
  envOptions.forEach(env => {
    configsByEnv[env.value] = null;
    domainsByEnv[env.value] = [];
    originalDomainsByEnv[env.value] = [];
    isEditingByEnv[env.value] = false;
  });
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
      }
    }
    await loadDomains(activeEnv.value, true);
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

const loadDomains = async (env: AppEnv, force = false) => {
  const cfg = configsByEnv[env];
  if (!cfg?.config_id) return;
  if (!force && domainsByEnv[env].length > 0) return;
  domainsLoading.value = true;
  try {
    const resp = await getAppConfigDomains(cfg.config_id);
    if (resp.data.code !== 1) throw new Error(resp.data.message || '获取多域名失败');
    domainsByEnv[env] = (resp.data.result || []).map(d => ({ host: d.host, path: d.path }));
    // 同步快照（用于取消编辑）
    originalDomainsByEnv[env] = domainsByEnv[env].map(d => ({ host: d.host, path: d.path }));
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '获取多域名失败');
  } finally {
    domainsLoading.value = false;
  }
};

const startEdit = (env: AppEnv) => {
  if (!configsByEnv[env]?.config_id) return;
  originalDomainsByEnv[env] = domainsByEnv[env].map(d => ({ host: d.host, path: d.path }));
  isEditingByEnv[env] = true;
};

const cancelEdit = (env: AppEnv) => {
  domainsByEnv[env] = originalDomainsByEnv[env].map(d => ({ host: d.host, path: d.path }));
  isEditingByEnv[env] = false;
};

const addRow = (env: AppEnv) => {
  if (!isEditingByEnv[env]) return;
  domainsByEnv[env].push({ host: '', path: '/' });
};

const removeRow = (env: AppEnv, index: number) => {
  if (!isEditingByEnv[env]) return;
  domainsByEnv[env].splice(index, 1);
};

const save = async (env: AppEnv) => {
  const cfg = configsByEnv[env];
  if (!cfg?.config_id) return;
  if (!isEditingByEnv[env]) return;
  saving.value = true;
  try {
    const payload = {
      domains: domainsByEnv[env].map(d => ({
        host: (d.host || '').trim(),
        path: d.path || '/',
      })),
    };
    const resp = await upsertAppConfigDomains(cfg.config_id, payload);
    if (resp.data.code !== 1) throw new Error(resp.data.message || '保存多域名失败');
    ElMessage.success('多域名保存成功');
    await ElMessageBox.alert('多域名保存成功', '保存结果', { type: 'success' });
    await loadDomains(env, true);
    isEditingByEnv[env] = false;
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '保存多域名失败');
    await ElMessageBox.alert(e instanceof Error ? e.message : '保存多域名失败', '保存结果', {
      type: 'error',
    });
  } finally {
    saving.value = false;
  }
};

const handleEnvChange = async (name: string | number) => {
  const env = name as AppEnv;
  // 切换 tab 时，自动退出当前环境编辑态，避免误改
  if (isEditingByEnv[activeEnv.value]) {
    cancelEdit(activeEnv.value);
  }
  activeEnv.value = env;
  await loadDomains(env, false);
};

watch(
  () => route.params.appId,
  async val => {
    appId.value = Number(val);
    await fetchConfigs();
  }
);

onMounted(async () => {
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

.mb-12 {
  margin-bottom: 12px;
}

.toolbar {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.empty-env {
  padding: 12px 0;
}

.empty-actions {
  margin-top: 12px;
  display: flex;
  gap: 8px;
  justify-content: center;
}

.domains-card {
  border-radius: 8px;
}

.mono {
  font-family:
    ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
    monospace;
  word-break: break-all;
}
</style>
