<template>
  <div class="app-info">
    <el-card v-loading="loading" shadow="never" class="info-card">
      <template #header>
        <div class="card-header">
          <span>应用基本信息</span>
          <div class="actions">
            <el-button v-if="!isEditing" :disabled="loading || saving" @click="reload"
              >刷新</el-button
            >
            <el-button
              v-if="!isEditing"
              type="primary"
              :disabled="loading || !appDetail"
              @click="startEdit"
            >
              编辑
            </el-button>
            <el-button
              v-else
              type="primary"
              :loading="saving"
              :disabled="loading || !isDirty"
              @click="save"
            >
              保存
            </el-button>
            <el-button v-if="isEditing" :disabled="loading || saving" @click="cancelEdit"
              >取消</el-button
            >
          </div>
        </div>
      </template>

      <!-- 展示态：更像“详情页”，不使用输入框 -->
      <div v-if="!isEditing" class="panel">
        <el-descriptions :column="2" border>
          <el-descriptions-item label="APPID">{{ appId }}</el-descriptions-item>
          <el-descriptions-item label="开发语言">{{
            String(appDetail?.dev_language || '-')
          }}</el-descriptions-item>

          <el-descriptions-item label="应用名称" :span="2">
            <span class="mono">{{ appDetail?.app_name || '-' }}</span>
          </el-descriptions-item>

          <el-descriptions-item label="应用中文名">
            {{ appDetail?.app_name_cn || '-' }}
          </el-descriptions-item>
          <el-descriptions-item label="Rundeck AppName">
            <span class="mono">{{ appDetail?.rundeck_app_name || '-' }}</span>
          </el-descriptions-item>

          <el-descriptions-item label="负责人">
            <span class="mono">{{ appDetail?.owner || '-' }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="负责人中文">
            {{ appDetail?.owner_cn || '-' }}
          </el-descriptions-item>

          <el-descriptions-item label="Git URL" :span="2">
            <span class="mono">{{ appDetail?.git_url || '-' }}</span>
          </el-descriptions-item>

          <el-descriptions-item label="描述(可空)" :span="2">
            <div class="desc">{{ appDetail?.description_cn || '-' }}</div>
          </el-descriptions-item>
        </el-descriptions>
      </div>

      <!-- 编辑态：保持同样的 Descriptions 布局，仅把值替换成输入控件 -->
      <el-form v-else ref="formRef" :model="form" :rules="rules" class="panel" @submit.prevent>
        <el-descriptions :column="2" border>
          <el-descriptions-item label="APPID">{{ appId }}</el-descriptions-item>
          <el-descriptions-item label="开发语言">{{
            String(appDetail?.dev_language || '-')
          }}</el-descriptions-item>

          <el-descriptions-item label="应用名称" :span="2">
            <span class="mono">{{ appDetail?.app_name || '-' }}</span>
          </el-descriptions-item>

          <el-descriptions-item label="应用中文名">
            <el-form-item prop="app_name_cn" class="inline-item">
              <el-input v-model="form.app_name_cn" placeholder="中文名" />
            </el-form-item>
          </el-descriptions-item>
          <el-descriptions-item label="Rundeck AppName">
            <el-form-item prop="rundeck_app_name" class="inline-item">
              <el-input v-model="form.rundeck_app_name" placeholder="demo-app" />
            </el-form-item>
          </el-descriptions-item>

          <el-descriptions-item label="负责人">
            <el-form-item prop="owner" class="inline-item">
              <el-input v-model="form.owner" placeholder="san.zhang" />
            </el-form-item>
          </el-descriptions-item>
          <el-descriptions-item label="负责人中文">
            <el-form-item prop="owner_cn" class="inline-item">
              <el-input v-model="form.owner_cn" placeholder="张三" />
            </el-form-item>
          </el-descriptions-item>

          <el-descriptions-item label="Git URL" :span="2">
            <el-form-item prop="git_url" class="inline-item span-2">
              <el-input v-model="form.git_url" placeholder="git@gitlab.xxx:group/repo.git" />
            </el-form-item>
          </el-descriptions-item>

          <el-descriptions-item label="描述(可空)" :span="2">
            <el-form-item prop="description_cn" class="inline-item span-2">
              <el-input
                v-model="form.description_cn"
                type="textarea"
                :autosize="{ minRows: 3, maxRows: 8 }"
              />
            </el-form-item>
          </el-descriptions-item>
        </el-descriptions>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { ElMessage, type FormInstance, type FormRules } from 'element-plus';
import type { AppInfo, PatchAppRequest } from '@/models/application';
import { getAppDetail, patchApp } from '@/services/application';
import { normalizeLegacyNullableText } from '@/utils/legacy-nullable-text';

const route = useRoute();
const appId = computed(() => Number(route.params.appId));

const loading = ref(false);
const saving = ref(false);
const isEditing = ref(false);

const appDetail = ref<AppInfo | null>(null);
const original = ref<PatchAppRequest>({});

const form = reactive<PatchAppRequest>({
  app_name_cn: '',
  owner: '',
  owner_cn: '',
  dev_language: '',
  description_cn: '',
  git_url: '',
  rundeck_app_name: '',
});

const rules: FormRules = {
  app_name_cn: [{ required: true, message: '请输入应用中文名', trigger: 'blur' }],
  owner: [{ required: true, message: '请输入负责人(owner)', trigger: 'blur' }],
  owner_cn: [{ required: true, message: '请输入负责人中文', trigger: 'blur' }],
  git_url: [{ required: true, message: '请输入 Git URL', trigger: 'blur' }],
};

const formRef = ref<FormInstance>();

const hydrate = (detail: AppInfo) => {
  const appNameCN = normalizeLegacyNullableText(detail.app_name_cn);
  const descriptionCN = normalizeLegacyNullableText(detail.description_cn);
  const rundeckAppName = normalizeLegacyNullableText(detail.rundeck_app_name);
  appDetail.value = {
    ...detail,
    app_name_cn: appNameCN,
    description_cn: descriptionCN,
    rundeck_app_name: rundeckAppName || null,
  };
  form.app_name_cn = appNameCN;
  form.owner = detail.owner || '';
  form.owner_cn = detail.owner_cn || '';
  form.description_cn = descriptionCN;
  form.git_url = detail.git_url || '';
  form.rundeck_app_name = rundeckAppName;

  original.value = {
    app_name_cn: form.app_name_cn,
    owner: form.owner,
    owner_cn: form.owner_cn,
    description_cn: form.description_cn,
    git_url: form.git_url,
    rundeck_app_name: form.rundeck_app_name,
  };
};

const fetchDetail = async () => {
  if (!Number.isFinite(appId.value) || appId.value <= 0) return;
  loading.value = true;
  try {
    const resp = await getAppDetail(appId.value);
    if (resp.data.code !== 1) throw new Error(resp.data.message || '获取应用详情失败');
    hydrate(resp.data.result);
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '获取应用详情失败');
  } finally {
    loading.value = false;
  }
};

const isDirty = computed(() => {
  const o = original.value;
  return (
    (form.app_name_cn ?? '') !== (o.app_name_cn ?? '') ||
    (form.owner ?? '') !== (o.owner ?? '') ||
    (form.owner_cn ?? '') !== (o.owner_cn ?? '') ||
    (form.description_cn ?? '') !== (o.description_cn ?? '') ||
    (form.git_url ?? '') !== (o.git_url ?? '') ||
    (form.rundeck_app_name ?? '') !== (o.rundeck_app_name ?? '')
  );
});

const buildPatch = (): PatchAppRequest => {
  const o = original.value;
  const patch: PatchAppRequest = {};

  const setIfChanged = (k: keyof PatchAppRequest, v: any, ov: any) => {
    const nv = (v ?? '') as any;
    const pv = (ov ?? '') as any;
    if (nv !== pv) patch[k] = nv;
  };

  setIfChanged('app_name_cn', form.app_name_cn, o.app_name_cn);
  setIfChanged('owner', form.owner, o.owner);
  setIfChanged('owner_cn', form.owner_cn, o.owner_cn);

  // 描述可空：清空时传空字符串，表示明确覆盖
  setIfChanged('description_cn', form.description_cn, o.description_cn);
  setIfChanged('git_url', form.git_url, o.git_url);
  setIfChanged('rundeck_app_name', form.rundeck_app_name, o.rundeck_app_name);

  // 清理 undefined
  Object.keys(patch).forEach(key => {
    const k = key as keyof PatchAppRequest;
    if (patch[k] === undefined) delete patch[k];
  });

  return patch;
};

const startEdit = () => {
  if (!appDetail.value) return;
  isEditing.value = true;
};

const cancelEdit = () => {
  if (appDetail.value) hydrate(appDetail.value);
  isEditing.value = false;
  formRef.value?.clearValidate();
};

const save = async () => {
  if (!Number.isFinite(appId.value) || appId.value <= 0) return;
  if (!isEditing.value) return;
  const ok = await formRef.value?.validate().catch(() => false);
  if (!ok) return;

  const payload = buildPatch();
  if (Object.keys(payload).length === 0) {
    ElMessage.info('没有变更可保存');
    return;
  }

  saving.value = true;
  try {
    const resp = await patchApp(appId.value, payload);
    if (resp.data.code !== 1) throw new Error(resp.data.message || '保存失败');
    ElMessage.success('保存成功');
    // 以服务端为准刷新一次
    await fetchDetail();
    isEditing.value = false;
  } catch (e) {
    console.error(e);
    ElMessage.error(e instanceof Error ? e.message : '保存失败');
  } finally {
    saving.value = false;
  }
};

const reload = async () => {
  await fetchDetail();
};

watch(
  () => route.params.appId,
  async () => {
    appDetail.value = null;
    original.value = {};
    isEditing.value = false;
    await fetchDetail();
  }
);

onMounted(async () => {
  await fetchDetail();
});
</script>

<style scoped>
.info-card {
  border-radius: 8px;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.actions {
  display: flex;
  gap: 8px;
}

.panel {
  width: 100%;
  max-width: 980px;
  margin: 0 auto;
}

/* 调整 Descriptions 默认“偏粗”的视觉 */
.panel :deep(.el-descriptions__label) {
  font-weight: 400;
  color: #606266;
}

.panel :deep(.el-descriptions__content) {
  font-weight: 400;
}

.mono {
  font-family:
    ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
    monospace;
  word-break: break-all;
  font-weight: 400;
}

.desc {
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.6;
  color: #606266;
}

/* 让表单控件在 Descriptions 单元格里不撑出额外上下间距 */
.inline-item {
  margin: 0;
}

.inline-item :deep(.el-form-item__content) {
  margin-left: 0 !important;
}

.inline-item :deep(.el-form-item__error) {
  position: static;
  margin-top: 6px;
}

.span-2 :deep(.el-form-item__content) {
  width: 100%;
}
</style>
