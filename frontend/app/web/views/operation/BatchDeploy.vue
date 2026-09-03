<template>
  <div class="batch-deploy">
    <el-card shadow="never">
      <template #header>
        <div class="card-header">
          <span>一键批量发布</span>
          <span class="hint">勾选应用，填写环境与分支后批量触发发布。</span>
        </div>
      </template>

      <div class="toolbar">
        <div class="left">
          <el-form :inline="true" @submit.prevent>
            <el-form-item label="环境">
              <el-select v-model="env" placeholder="请选择环境" style="width: 220px">
                <el-option
                  v-for="item in enabledEnvironments"
                  :key="item.code"
                  :label="labelForEnvironment(item.code)"
                  :value="item.code"
                />
              </el-select>
            </el-form-item>
            <el-form-item label="分支">
              <el-input
                v-model="branch"
                placeholder="如 master / feature_xxx / release_202601"
                style="width: 340px"
              />
            </el-form-item>
            <el-form-item label="Rundeck">
              <el-switch v-model="isRundeck" inline-prompt active-text="是" inactive-text="否" />
            </el-form-item>
          </el-form>
        </div>
        <div class="right">
          <el-input
            v-model="keyword"
            clearable
            placeholder="搜索应用名称/中文名"
            style="width: 260px"
            @keyup.enter="fetchApps()"
          />
          <el-button :loading="loading" @click="fetchApps()">查询</el-button>
          <el-button
            type="primary"
            :disabled="selectedKeys.length === 0 || !env || !branch || deploying"
            :loading="deploying"
            @click="submitBatchDeploy"
          >
            一键发布（已选 {{ selectedKeys.length }}）
          </el-button>
        </div>
      </div>

      <el-divider content-position="left">额外发布参数（extra_data）</el-divider>
      <div class="extra">
        <div class="extra-toolbar">
          <el-button type="primary" plain :disabled="deploying" @click="addExtraRow"
            >新增参数</el-button
          >
          <el-button :disabled="deploying || extraRows.length === 0" @click="clearExtraRows"
            >清空</el-button
          >
          <span class="extra-hint">会对本次批量发布的所有应用生效。</span>
        </div>
        <el-table :data="extraRows" border style="width: 100%" empty-text="暂无额外参数">
          <el-table-column label="key" min-width="220">
            <template #default="{ row }">
              <el-input v-model="row.key" placeholder="如 mini_type" :disabled="deploying" />
            </template>
          </el-table-column>
          <el-table-column label="value" min-width="260">
            <template #default="{ row }">
              <el-input v-model="row.value" placeholder="如 mp-weixin" :disabled="deploying" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="120" fixed="right">
            <template #default="{ $index }">
              <el-button type="danger" link :disabled="deploying" @click="removeExtraRow($index)"
                >删除</el-button
              >
            </template>
          </el-table-column>
        </el-table>
      </div>

      <el-table
        ref="tableRef"
        v-loading="loading"
        :data="apps"
        border
        style="width: 100%"
        row-key="app_id"
        @selection-change="handleSelectionChange"
      >
        <el-table-column type="selection" width="50" />
        <el-table-column prop="app_id" label="APPID" width="110" />
        <el-table-column prop="app_name" label="应用名称" min-width="220" show-overflow-tooltip />
        <el-table-column
          prop="app_name_cn"
          label="应用中文名"
          min-width="220"
          show-overflow-tooltip
        />
        <el-table-column prop="owner_cn" label="负责人" width="140" />
        <el-table-column prop="dev_language" label="语言" width="120" />
      </el-table>

      <div class="pager">
        <el-pagination
          v-model:current-page="pageNum"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handleSizeChange"
          @current-change="handlePageChange"
        />
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue';
import type { TableInstance } from 'element-plus';
import { ElMessage, ElMessageBox } from 'element-plus';
import { useUserStore } from '@/stores/user';
import { queryApps } from '@/services/application';
import { batchDeploy } from '@/services/deploy';
import type { AppInfo, AppQueryParams } from '@/models/application';
import type { AppEnv } from '@/models/application';
import { useEnvironments } from '@/composables/useEnvironments';

const userStore = useUserStore();

const { enabledEnvironments, loadEnvironments, labelForEnvironment } = useEnvironments();
const env = ref<AppEnv>('');
const branch = ref('');
const keyword = ref('');
const isRundeck = ref(false);

const extraRows = ref<Array<{ key: string; value: string }>>([]);

const pageNum = ref(1);
const pageSize = ref(20);
const total = ref(0);
const loading = ref(false);
const deploying = ref(false);

const apps = ref<AppInfo[]>([]);

// 使用 Map 做跨分页勾选缓存
const selectedMap = ref(new Map<number, AppInfo>());
const selectedKeys = computed(() => Array.from(selectedMap.value.keys()));

const tableRef = ref<TableInstance>();

const fetchApps = async () => {
  loading.value = true;
  try {
    const params: AppQueryParams = {
      page_num: pageNum.value,
      page_size: pageSize.value,
      ...(keyword.value ? { app_name: keyword.value } : {}),
    };
    const resp = await queryApps(params);
    if (resp.data.code !== 1) throw new Error(resp.data.message || '查询应用失败');
    apps.value = resp.data.result?.apps || [];
    total.value = resp.data.result?.total ?? 0;

    // 恢复本页勾选
    await nextTick();
    if (tableRef.value) {
      for (const row of apps.value) {
        if (selectedMap.value.has(row.app_id)) {
          tableRef.value.toggleRowSelection(row, true);
        }
      }
    }
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '查询应用失败');
  } finally {
    loading.value = false;
  }
};

const handleSelectionChange = (rows: AppInfo[]) => {
  // 先移除本页所有行
  for (const row of apps.value) {
    selectedMap.value.delete(row.app_id);
  }
  // 再写入当前选择
  for (const row of rows) {
    selectedMap.value.set(row.app_id, row);
  }
};

const handlePageChange = async (p: number) => {
  pageNum.value = p;
  await fetchApps();
};

const handleSizeChange = async (s: number) => {
  pageSize.value = s;
  pageNum.value = 1;
  await fetchApps();
};

const addExtraRow = () => {
  extraRows.value.push({ key: '', value: '' });
};

const removeExtraRow = (idx: number) => {
  extraRows.value.splice(idx, 1);
};

const clearExtraRows = () => {
  extraRows.value = [];
};

const extraData = computed(() => {
  const obj: Record<string, any> = {};
  for (const row of extraRows.value) {
    const k = (row.key || '').trim();
    if (!k) continue;
    obj[k] = row.value;
  }
  return obj;
});

const submitBatchDeploy = async () => {
  if (!userStore.userInfo) {
    ElMessage.error('用户未登录');
    return;
  }
  const selected = Array.from(selectedMap.value.values());
  const envValue = env.value;
  const branchValue = branch.value.trim();
  if (!envValue || !branchValue || selected.length === 0) return;

  await ElMessageBox.confirm(
    `确认对 ${selected.length} 个应用执行批量发布吗？\n环境：${envValue}\n分支：${branchValue}`,
    '确认发布',
    { type: 'warning', confirmButtonText: '发布', cancelButtonText: '取消' }
  );

  deploying.value = true;
  try {
    const deployRequests = selected.map(app => ({
      app_name: app.app_name,
      env: envValue,
      branch: branchValue,
      ...(isRundeck.value ? { is_rundeck: true } : {}),
      ...(Object.keys(extraData.value).length > 0 ? { extra_data: extraData.value } : {}),
    }));
    const resp = await batchDeploy(deployRequests, {
      username: userStore.userInfo.username,
      nameCn: userStore.userInfo.nameCn,
    });
    if (resp.data.code !== 1) throw new Error(resp.data.message || '批量发布失败');

    const r = resp.data.result;
    const failures = (r.task_records || []).filter(x => !x.success);
    const failureLines = failures
      .slice(0, 10)
      .map(x => `- ${x.app_name || x.task_record?.app_name || '未知应用'}：${x.error || '失败'}`)
      .join('\n');

    await ElMessageBox.alert(
      `已提交批量发布任务。\n成功：${r.success_count}，失败：${r.failure_count}，总计：${r.total_count}${
        failureLines ? `\n\n失败明细（最多10条）：\n${failureLines}` : ''
      }`,
      '发布结果',
      { type: r.failure_count > 0 ? 'warning' : 'success' }
    );
  } catch (e) {
    console.error(e);
    await ElMessageBox.alert(e instanceof Error ? e.message : '批量发布失败', '发布结果', {
      type: 'error',
    });
  } finally {
    deploying.value = false;
  }
};

onMounted(async () => {
  try {
    await Promise.all([fetchApps(), loadEnvironments()]);
    if (!env.value) env.value = enabledEnvironments.value[0]?.code || '';
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : '初始化发布页面失败');
  }
});
</script>

<style scoped>
.batch-deploy {
  padding: 20px;
}

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

.toolbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.right {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
}

.pager {
  margin-top: 12px;
  display: flex;
  justify-content: flex-end;
}

.extra {
  margin-top: 8px;
}

.extra-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
  flex-wrap: wrap;
}

.extra-hint {
  font-size: 12px;
  color: #909399;
}
</style>
