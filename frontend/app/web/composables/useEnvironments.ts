import { computed, readonly, ref } from 'vue';
import { getEnvironments } from '@/services/environment';
import type { AppEnv, EnvironmentCatalogItem } from '@/models/application';

const environments = ref<EnvironmentCatalogItem[]>([]);
const loading = ref(false);
let loaded = false;
let pending: Promise<void> | null = null;

const normalize = (item: EnvironmentCatalogItem): EnvironmentCatalogItem => ({
  ...item,
  code: String(item.code || item.env || '').trim(),
  name: String(
    item.name || item.description_cn || item.description || item.code || item.env || ''
  ).trim(),
  env: String(item.code || item.env || '').trim(),
  description_cn: String(item.name || item.description_cn || item.description || '').trim(),
  enabled: item.enabled !== false,
  sort_order: Number.isFinite(item.sort_order) ? item.sort_order : 0,
});

export function useEnvironments() {
  const loadEnvironments = async (force = false) => {
    if (loaded && !force) return;
    if (pending) return pending;
    loading.value = true;
    pending = (async () => {
      const response = await getEnvironments();
      if (response.data.code !== 1) {
        throw new Error(response.data.error || response.data.message || '获取环境目录失败');
      }
      environments.value = (response.data.result || [])
        .map(normalize)
        .filter(item => item.code)
        .sort(
          (left, right) => left.sort_order - right.sort_order || left.code.localeCompare(right.code)
        );
      loaded = true;
    })().finally(() => {
      loading.value = false;
      pending = null;
    });
    return pending;
  };

  const enabledEnvironments = computed(() => environments.value.filter(item => item.enabled));
  const labelForEnvironment = (env: AppEnv) => {
    const item = environments.value.find(candidate => candidate.code === env);
    return item?.name && item.name !== env ? `${item.name}（${env}）` : env;
  };

  return {
    environments: readonly(environments),
    enabledEnvironments,
    environmentsLoading: readonly(loading),
    loadEnvironments,
    labelForEnvironment,
  };
}
