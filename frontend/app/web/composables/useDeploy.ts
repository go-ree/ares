import { ref } from 'vue';
import { ElMessage } from 'element-plus';
import api from '@/config/api';
import type { DeployingService, ServiceInfo } from '@/types/deploy';
import { normalizeLegacyNullableText } from '@/utils/legacy-nullable-text';
import { useEnvironments } from '@/composables/useEnvironments';

interface ActiveTaskStepSummary {
  total: number;
  settled: number;
}

interface ActiveTaskRecord {
  task_id: number;
  app_name: string;
  branch: string;
  env: string;
  publisher: string;
  status: string;
  message?: string | null;
  products?: string | null;
  engine_version?: number;
  step_summary?: ActiveTaskStepSummary | null;
  created_at: string;
}

interface ActiveTaskEnvelope {
  code: number;
  message?: string;
  msg?: string;
  error?: string | null;
  result?: ActiveTaskRecord[] | null;
}

export interface TaskProgress {
  percentage: number;
  indeterminate: boolean;
  settled: number;
  total: number;
}

export const calculateTaskProgress = (
  engineVersion?: number,
  summary?: ActiveTaskStepSummary | null
): TaskProgress => {
  const total = Math.max(0, summary?.total || 0);
  const settled = Math.min(total, Math.max(0, summary?.settled || 0));
  if ((engineVersion || 1) < 2 || total === 0) {
    return { percentage: 0, indeterminate: true, settled, total };
  }
  return {
    percentage: Math.round((settled / total) * 100),
    indeterminate: false,
    settled,
    total,
  };
};

export function useDeploy() {
  const { labelForEnvironment } = useEnvironments();
  const availableServices = ref<ServiceInfo[]>([]);
  const deployingList = ref<DeployingService[]>([]);
  const deployingLoading = ref(false);

  const isServiceProcessing = (status: string): boolean =>
    ['发布中', '打包中', '部署中', '排队中', '执行中', 'queued', 'running'].includes(status);

  const getDeployStatus = (status: string): string => {
    const statusMap: Record<string, string> = {
      init: '初始化',
      packaging: '打包中',
      packaged: '打包成功',
      package_failed: '打包失败',
      deploying: '部署中',
      deployed: '部署成功',
      deploy_failed: '部署失败',
      cancelled: '已取消',
      timeout: '超时',
      unknown: '未知状态',
      queued: '排队中',
      running: '执行中',
      succeeded: '执行成功',
      failed: '执行失败',
      succeeded_with_warnings: '成功但有警告',
    };
    return statusMap[status] || status;
  };

  const getStatusType = (status: string) => {
    const statusMap: Record<string, string> = {
      初始化: 'info',
      打包中: 'primary',
      打包成功: 'success',
      打包失败: 'danger',
      部署中: 'primary',
      部署成功: 'success',
      部署失败: 'danger',
      已取消: 'warning',
      超时: 'warning',
      未知状态: 'info',
      排队中: 'info',
      执行中: 'primary',
      执行成功: 'success',
      执行失败: 'danger',
      成功但有警告: 'warning',
    };
    return statusMap[getDeployStatus(status)] || 'info';
  };

  const getProgressStatus = (status: string) => {
    const statusMap: Record<string, string> = {
      打包成功: 'success',
      打包失败: 'exception',
      部署成功: 'success',
      部署失败: 'exception',
      已取消: 'warning',
      超时: 'warning',
      执行成功: 'success',
      执行失败: 'exception',
      成功但有警告: 'warning',
    };
    return statusMap[getDeployStatus(status)] || '';
  };

  const getEnvLabel = (env: string): string => labelForEnvironment(env);

  const getEnvType = (env: string): string => {
    const tagTypes = ['info', 'warning', 'success', 'primary'] as const;
    const hash = Array.from(env).reduce((sum, char) => sum + char.charCodeAt(0), 0);
    return tagTypes[hash % tagTypes.length];
  };

  const formatDateTime = (dateTime: string): string => {
    if (!dateTime) return '';
    return new Date(dateTime).toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
  };

  const refreshDeployingList = async () => {
    deployingLoading.value = true;
    try {
      const response = await api.get<ActiveTaskEnvelope>('/api/v1/deploy/publish/status');
      if (response.data.code !== 1) {
        throw new Error(
          response.data.error ||
            response.data.message ||
            response.data.msg ||
            '获取发布中服务列表失败'
        );
      }
      deployingList.value = (response.data.result || []).map(item => {
        const progress = calculateTaskProgress(item.engine_version, item.step_summary);
        return {
          id: item.task_id,
          serviceName: item.app_name,
          branch: item.branch,
          environment: item.env,
          status: getDeployStatus(item.status),
          progress: progress.percentage,
          progressIndeterminate: progress.indeterminate,
          settledSteps: progress.settled,
          totalSteps: progress.total,
          startTime: formatDateTime(item.created_at),
          operator: item.publisher,
          message: normalizeLegacyNullableText(item.message),
          taskId: item.task_id,
          products: normalizeLegacyNullableText(item.products),
        };
      });
    } catch (error) {
      console.error('获取发布中服务列表失败:', error);
      ElMessage.error(error instanceof Error ? error.message : '获取发布中服务列表失败');
    } finally {
      deployingLoading.value = false;
    }
  };

  const loadAvailableServices = async () => {
    try {
      const response = await api.get<{
        code: number;
        message?: string;
        msg?: string;
        error?: string | null;
        result?: string[] | null;
      }>('/api/v1/apps/query/appname');
      if (response.data.code !== 1) {
        throw new Error(
          response.data.error || response.data.message || response.data.msg || '获取服务列表失败'
        );
      }
      availableServices.value = (response.data.result || []).map(appName => ({
        name: appName,
        nameCn: appName,
        description: '',
      }));
    } catch (error) {
      console.error('获取服务列表失败:', error);
      ElMessage.error(error instanceof Error ? error.message : '获取服务列表失败');
    }
  };

  return {
    availableServices,
    deployingList,
    deployingLoading,
    isServiceProcessing,
    getStatusType,
    getProgressStatus,
    getDeployStatus,
    getEnvLabel,
    getEnvType,
    formatDateTime,
    refreshDeployingList,
    loadAvailableServices,
  };
}
