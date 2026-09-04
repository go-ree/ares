<template>
  <div class="app-detail-page">
    <el-card class="header-card" shadow="never">
      <div class="header-row">
        <div class="title">
          <div class="name">
            <span class="app-name">{{ displayName }}</span>
            <span class="app-id">APPID: {{ appId }}</span>
          </div>
          <div class="meta">
            <el-tag v-if="appDetail?.dev_language" type="info">
              {{ appDetail.dev_language }}
            </el-tag>
            <el-tag v-if="appDetail?.status" :type="statusTagType">
              {{ statusText }}
            </el-tag>
            <span v-if="appDetail?.owner_cn" class="owner">负责人：{{ appDetail.owner_cn }}</span>
          </div>
          <div v-if="appDetail?.description_cn" class="desc">
            {{ appDetail.description_cn }}
          </div>
        </div>

        <div class="actions">
          <el-button @click="goBack">返回列表</el-button>
          <el-button v-if="safeGitURL" type="primary" plain @click="openGit"> 打开 Git </el-button>
        </div>
      </div>
    </el-card>

    <div class="content">
      <el-card class="nav-card" shadow="never">
        <el-menu :default-active="activeKey" class="nav" :router="true">
          <el-menu-item :index="`/application/${appId}/info`">
            <el-icon><InfoFilled /></el-icon>
            <span>应用详情</span>
          </el-menu-item>
          <el-menu-item
            v-if="authStore.can(PERMISSIONS.APP_CONFIGS_READ)"
            :index="`/application/${appId}/config`"
          >
            <el-icon><Setting /></el-icon>
            <span>环境配置</span>
          </el-menu-item>
          <el-menu-item
            v-if="authStore.can(PERMISSIONS.DOMAINS_READ)"
            :index="`/application/${appId}/domains`"
          >
            <el-icon><Share /></el-icon>
            <span>多域名</span>
          </el-menu-item>
          <el-menu-item
            v-if="authStore.can(PERMISSIONS.KUBERNETES_READ)"
            :index="`/application/${appId}/pods`"
          >
            <el-icon><Monitor /></el-icon>
            <span>Pod 实例</span>
          </el-menu-item>
        </el-menu>
      </el-card>

      <div class="panel">
        <router-view />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { ElMessage } from 'element-plus';
import { InfoFilled, Monitor, Setting, Share } from '@element-plus/icons-vue';
import { getAppDetail } from '@/services/application';
import { AppStatus, type AppInfo } from '@/models/application';
import { useAuthStore } from '@/stores/auth';
import { PERMISSIONS } from '@/types/auth';
import { repositoryWebURL } from '@/utils/repository-url';

const props = defineProps<{
  appId: string | number;
}>();

const router = useRouter();
const route = useRoute();
const authStore = useAuthStore();

const appId = computed(() => Number(props.appId));
const appDetail = ref<AppInfo | null>(null);
const safeGitURL = computed(() => repositoryWebURL(appDetail.value?.git_url));

const activeKey = computed(() => route.path);

const displayName = computed(() => {
  const fromQuery = typeof route.query.name === 'string' ? route.query.name : '';
  return appDetail.value?.app_name || fromQuery || `应用 ${appId.value}`;
});

const statusTagType = computed(() => {
  switch (appDetail.value?.status) {
    case AppStatus.DEPLOYED:
      return 'success';
    case AppStatus.REJECTED:
      return 'danger';
    case AppStatus.PENDING:
      return 'warning';
    case AppStatus.APPROVED:
      return 'info';
    default:
      return 'info';
  }
});

const statusText = computed(() => {
  switch (appDetail.value?.status) {
    case AppStatus.DEPLOYED:
      return '已部署';
    case AppStatus.REJECTED:
      return '已拒绝';
    case AppStatus.PENDING:
      return '待审核';
    case AppStatus.APPROVED:
      return '已通过';
    default:
      return '未知';
  }
});

const fetchDetail = async () => {
  if (!Number.isFinite(appId.value) || appId.value <= 0) return;
  try {
    const resp = await getAppDetail(appId.value);
    if (resp.data.code === 1) {
      appDetail.value = resp.data.result;
    } else {
      ElMessage.error(resp.data.message || '获取应用详情失败');
    }
  } catch (e) {
    console.error(e);
    ElMessage.error('获取应用详情失败');
  }
};

const goBack = () => {
  router.push('/application/list');
};

const openGit = () => {
  if (safeGitURL.value) window.open(safeGitURL.value, '_blank', 'noopener,noreferrer');
};

watch(appId, async () => {
  appDetail.value = null;
  await fetchDetail();
});

onMounted(async () => {
  await fetchDetail();
});
</script>

<style scoped>
.app-detail-page {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.header-card {
  border-radius: 8px;
}

.header-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.title {
  min-width: 0;
}

.name {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
}

.app-name {
  font-size: 18px;
  font-weight: 700;
  color: #303133;
}

.app-id {
  font-size: 12px;
  color: #909399;
}

.meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
  flex-wrap: wrap;
}

.owner {
  font-size: 12px;
  color: #606266;
}

.desc {
  margin-top: 8px;
  font-size: 13px;
  color: #606266;
  line-height: 1.5;
}

.actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

.content {
  display: grid;
  grid-template-columns: 220px 1fr;
  gap: 12px;
  align-items: start;
}

.nav-card {
  border-radius: 8px;
}

.nav {
  border-right: none;
}

.panel {
  min-width: 0;
}

@media (max-width: 1000px) {
  .content {
    grid-template-columns: 1fr;
  }
}
</style>
