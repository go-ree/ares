import { ref, reactive, computed } from 'vue';
import { ElMessage } from 'element-plus';
import { batchDeploy, createDeploy } from '@/services/deploy';
import api from '@/config/api';
import type { DeployingService, ServiceInfo, SelectedService, DeployForm } from '@/types/deploy';
import { normalizeLegacyNullableText } from '@/utils/legacy-nullable-text';
import { useEnvironments } from '@/composables/useEnvironments';
import type { BatchDeployResponse } from '@/models/deploy';

export function useDeploy() {
  const { labelForEnvironment } = useEnvironments();

  // 发布表单数据
  const deployForm = reactive<DeployForm>({
    serviceName: '',
    environment: '',
    version: '',
  });

  // 全局分支后缀
  const globalBranchSuffix = ref('');

  // 可用服务列表
  const availableServices = ref<ServiceInfo[]>([]);

  // 选中的服务列表
  const selectedServices = ref<SelectedService[]>([]);

  // 发布中服务列表
  const deployingList = ref<DeployingService[]>([]);
  const deployingLoading = ref(false);

  // 计算属性
  const hasDeployableServices = computed(() => {
    return selectedServices.value.some(
      service => service.serviceName && service.branch && !isServiceProcessing(service.status)
    );
  });

  // 工具函数
  const isServiceProcessing = (status: string): boolean => {
    return ['发布中', '打包中', '部署中', '排队中', '执行中', 'queued', 'running'].includes(status);
  };

  const getStatusType = (status: string) => {
    const displayStatus = getDeployStatus(status);
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
    return statusMap[displayStatus] || 'info';
  };

  const getProgressStatus = (status: string) => {
    const displayStatus = getDeployStatus(status);
    const statusMap: Record<string, string> = {
      初始化: '',
      打包中: '',
      打包成功: 'success',
      打包失败: 'exception',
      部署中: '',
      部署成功: 'success',
      部署失败: 'exception',
      已取消: 'warning',
      超时: 'warning',
      未知状态: '',
      排队中: '',
      执行中: '',
      执行成功: 'success',
      执行失败: 'exception',
      成功但有警告: 'warning',
    };
    return statusMap[displayStatus] || '';
  };

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

  const getEnvLabel = (env: string): string => {
    return labelForEnvironment(env);
  };

  const getEnvType = (env: string): string => {
    const tagTypes = ['info', 'warning', 'success', 'primary'] as const;
    const hash = Array.from(env).reduce((sum, char) => sum + char.charCodeAt(0), 0);
    return tagTypes[hash % tagTypes.length];
  };

  const calculateProgress = (status: string): number => {
    const statusProgressMap: Record<string, number> = {
      init: 10,
      packaging: 30,
      packaged: 60,
      package_failed: 60,
      deploying: 80,
      deployed: 100,
      deploy_failed: 100,
      cancelled: 100,
      timeout: 100,
      unknown: 0,
      queued: 10,
      running: 50,
      succeeded: 100,
      failed: 100,
      succeeded_with_warnings: 100,
    };
    return statusProgressMap[status] || 0;
  };

  const formatDateTime = (dateTime: string): string => {
    if (!dateTime) return '';
    const date = new Date(dateTime);
    return date.toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
  };

  // 事件处理函数
  const handleEnvChange = () => {
    console.log('环境改变:', deployForm.environment);
    // 环境只代表目标配置，不得隐式改写 Git 分支。
  };

  const handleGlobalBranchChange = () => {
    // 全局分支改变时的逻辑
    console.log('全局分支后缀改变:', globalBranchSuffix.value);
    selectedServices.value.forEach(service => {
      service.branch = globalBranchSuffix.value;
    });
  };

  const handleServiceSelect = (serviceName: string, index: number) => {
    const service = selectedServices.value[index];
    if (service) {
      service.serviceName = serviceName;
    }
  };

  const handleAddService = () => {
    const newService: SelectedService = {
      serviceName: '',
      branch: globalBranchSuffix.value,
      status: '待发布',
    };
    selectedServices.value.push(newService);
  };

  const handleRemoveService = (index: number) => {
    selectedServices.value.splice(index, 1);
  };

  const handleDeploySingle = async (service: SelectedService, _index: number) => {
    try {
      service.status = '发布中';
      service.lastUpdateTime = formatDateTime(new Date().toISOString());

      const response = await createDeploy({
        app_name: service.serviceName,
        env: deployForm.environment,
        branch: service.branch,
      });

      if (response.data.code === 1) {
        const taskRecord = response.data.result;
        const taskId = taskRecord?.task_record?.task_id;
        if (!taskId) {
          throw new Error(
            taskRecord?.error || response.data.message || (response.data as any).msg || '发布失败'
          );
        }
        service.taskId = taskId;
        ElMessage.success(`${service.serviceName} 发布任务已提交`);
      } else {
        throw new Error(response.data.message || (response.data as any).msg || '发布失败');
      }
    } catch (error) {
      service.status = '发布失败';
      console.error('单个发布失败:', error);
      const errorMessage = error instanceof Error ? error.message : '发布失败';
      ElMessage.error(errorMessage);
    }
  };

  const handleBatchDeploy = async () => {
    try {
      const deployableServices = selectedServices.value.filter(
        service => service.serviceName && service.branch && !isServiceProcessing(service.status)
      );

      if (deployableServices.length === 0) {
        ElMessage.warning('没有可发布的服务');
        return;
      }

      // 设置所有服务状态为发布中
      deployableServices.forEach(service => {
        service.status = '发布中';
      });

      // 构建批量发布请求
      const deployRequests = deployableServices.map(service => ({
        app_name: service.serviceName,
        env: deployForm.environment,
        branch: service.branch,
      }));

      const response = await batchDeploy(deployRequests);

      const envelope = response.data as unknown as {
        code: number;
        message?: string;
        result?: BatchDeployResponse | null;
        error?: BatchDeployResponse | string | null;
      };
      const result =
        envelope.result ||
        (typeof envelope.error === 'object' && envelope.error !== null ? envelope.error : null);
      if (result) {
        const successCount = result.success_count;
        const failureCount = result.failure_count;
        const totalCount = result.total_count;

        const summary = `批量发布任务已提交，成功: ${successCount}，失败: ${failureCount}，总计: ${totalCount}`;
        if (failureCount > 0) ElMessage.warning(summary);
        else ElMessage.success(summary);

        result.task_records?.forEach((taskResult, index) => {
          const service = deployableServices[taskResult.request_index ?? index];
          if (!service) return;
          if (taskResult.success && taskResult.task_record) {
            service.taskId = taskResult.task_record.task_id;
            service.status = getDeployStatus(taskResult.task_record.status);
            service.lastUpdateTime = formatDateTime(taskResult.task_record.updated_at);
          } else {
            service.status = '发布失败';
          }
        });
      } else {
        throw new Error(
          (typeof envelope.error === 'string' && envelope.error) ||
            envelope.message ||
            '批量发布失败'
        );
      }
    } catch (error) {
      console.error('批量发布失败:', error);
      const errorMessage = error instanceof Error ? error.message : '批量发布失败';
      ElMessage.error(errorMessage);

      // 重置失败的服务状态
      selectedServices.value.forEach(service => {
        if (service.status === '发布中') {
          service.status = '发布失败';
        }
      });
    }
  };

  const refreshDeployingList = async () => {
    deployingLoading.value = true;
    try {
      const response = await api.get('/api/v1/deploy/publish/status');

      if (response.data.code !== 1) {
        throw new Error(response.data.msg || '获取发布中服务列表失败');
      }

      // 将 API 返回的数据转换为组件需要的格式
      deployingList.value = (response.data.result || []).map((item: any) => ({
        id: item.task_id,
        serviceName: item.app_name,
        branch: item.branch,
        environment: item.env,
        status: getDeployStatus(item.status),
        progress: calculateProgress(item.status),
        startTime: formatDateTime(item.created_at),
        operator: item.publisher,
        message: normalizeLegacyNullableText(item.message),
        taskId: item.task_id,
        ciJobName: normalizeLegacyNullableText(item.ci_job_name),
        cdJobName: normalizeLegacyNullableText(item.cd_job_name),
        ciBuildId: item.ci_build_id || null,
        cdBuildId: item.cd_build_id || null,
        products: normalizeLegacyNullableText(item.products),
      }));
    } catch (error) {
      console.error('获取发布中服务列表失败:', error);
      ElMessage.error('获取发布中服务列表失败');
    } finally {
      deployingLoading.value = false;
    }
  };

  // 加载可用服务列表
  const loadAvailableServices = async () => {
    try {
      console.log('开始加载可用服务列表...');
      const response = await api.get('/api/v1/apps/query/appname');
      console.log('API响应:', response);

      if (response.data.code === 1) {
        // API返回的是应用名称字符串数组
        const appNames = response.data.result || [];

        // 转换为ServiceInfo格式
        availableServices.value = appNames.map((appName: string) => ({
          name: appName,
          nameCn: appName, // 如果没有中文名称，使用英文名称
          description: '',
        }));

        console.log('加载到的服务列表:', availableServices.value);
      } else {
        console.error('API返回错误:', response.data);
        throw new Error(response.data.msg || '获取服务列表失败');
      }
    } catch (error) {
      console.error('获取服务列表失败:', error);
      ElMessage.error('获取服务列表失败');
    }
  };

  return {
    // 响应式数据
    deployForm,
    globalBranchSuffix,
    availableServices,
    selectedServices,
    deployingList,
    deployingLoading,

    // 计算属性
    hasDeployableServices,

    // 工具函数
    isServiceProcessing,
    getStatusType,
    getProgressStatus,
    getDeployStatus,
    getEnvLabel,
    getEnvType,
    calculateProgress,
    formatDateTime,

    // 事件处理函数
    handleEnvChange,
    handleGlobalBranchChange,
    handleServiceSelect,
    handleAddService,
    handleRemoveService,
    handleDeploySingle,
    handleBatchDeploy,
    refreshDeployingList,
    loadAvailableServices,
  };
}
