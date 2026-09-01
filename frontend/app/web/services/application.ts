import api from '@/config/api';
import type {
  ApiResponse,
  AppConfig,
  AppConfigDomain,
  AppInfo,
  AppQueryParams,
  AppEnv,
  PageResponse,
  PatchDomainRequest,
  PatchAppRequest,
  CreateAppConfigRequest,
  UpdateAppConfigRequest,
  UpsertDomainsRequest,
  DomainItem,
} from '../models/application';

const BASE_URL = '/api/v1';

// 查询应用列表
export const queryApps = async (params: AppQueryParams) => {
  return api.post<ApiResponse<PageResponse<AppInfo>>>(`${BASE_URL}/apps/query`, params);
};

// 获取全量应用名称（字符串数组）
export const getAllAppNames = async () => {
  // 全量接口可能较慢，单独放宽超时
  return api.get<ApiResponse<string[]>>(`${BASE_URL}/apps/query/appname`, { timeout: 30000 });
};

// 获取应用列表
export const getAppList = (params: AppQueryParams) => {
  return api.get<ApiResponse<PageResponse<AppInfo>>>(`${BASE_URL}/list`, { params });
};

// 获取应用详情
export const getAppDetail = (id: number) => {
  return api.get<ApiResponse<AppInfo>>(`${BASE_URL}/apps/${id}`);
};

// 创建应用
export const createApp = (data: Partial<AppInfo>) => {
  return api.post<ApiResponse<AppInfo>>(`${BASE_URL}/create`, data);
};

// 更新应用
export const updateApp = (id: number, data: Partial<AppInfo>) => {
  return api.put<ApiResponse<AppInfo>>(`${BASE_URL}/apps/${id}`, data);
};

// 更新应用（部分更新，仅提交变动字段）
export const patchApp = (id: number, data: PatchAppRequest) => {
  return api.patch<ApiResponse<AppInfo | null>>(`${BASE_URL}/apps/${id}`, data);
};

// 审核应用
export const reviewApp = (id: number, approved: boolean, comment?: string) => {
  return api.post<ApiResponse<void>>(`${BASE_URL}/${id}/review`, { approved, comment });
};

// =========================
// AppConfig（应用环境配置）
// =========================

// 1.1 获取应用所有环境配置
export const getAppConfigs = (appId: number) => {
  return api.get<ApiResponse<AppConfig[]>>(`${BASE_URL}/apps/${appId}/configs`);
};

// 1.0 创建应用某环境配置（未传字段按默认值落库）
export const createAppConfig = (appId: number, data: CreateAppConfigRequest) => {
  return api.post<ApiResponse<AppConfig | null>>(`${BASE_URL}/apps/${appId}/configs`, data);
};

// 1.2 获取应用指定环境配置
export const getAppConfigByEnv = (appId: number, env: AppEnv) => {
  return api.get<ApiResponse<AppConfig>>(`${BASE_URL}/apps/${appId}/configs/${env}`);
};

// 1.3 更新应用指定环境配置（部分更新）
export const patchAppConfigByEnv = (appId: number, env: AppEnv, data: UpdateAppConfigRequest) => {
  return api.patch<ApiResponse<null>>(`${BASE_URL}/apps/${appId}/configs/${env}`, data);
};

// 2.1 按 config_id 获取配置
export const getAppConfigById = (configId: number) => {
  return api.get<ApiResponse<AppConfig>>(`${BASE_URL}/app-configs/${configId}`);
};

// 2.2 按 config_id 更新配置（部分更新）
export const patchAppConfigById = (configId: number, data: UpdateAppConfigRequest) => {
  return api.patch<ApiResponse<null>>(`${BASE_URL}/app-configs/${configId}`, data);
};

// 3.1 查询多域名列表
export const getAppConfigDomains = (configId: number) => {
  return api.get<ApiResponse<AppConfigDomain[]>>(`${BASE_URL}/app-configs/${configId}/domains`);
};

// 3.2 全量覆盖写入多域名（幂等）
export const upsertAppConfigDomains = (configId: number, data: UpsertDomainsRequest) => {
  return api.put<ApiResponse<null>>(`${BASE_URL}/app-configs/${configId}/domains`, data);
};

// 3.3 新增单条域名
export const createAppConfigDomain = (configId: number, data: DomainItem) => {
  return api.post<ApiResponse<AppConfigDomain>>(
    `${BASE_URL}/app-configs/${configId}/domains`,
    data
  );
};

// 3.4 删除单条域名（按 domain_id）
export const deleteAppConfigDomain = (configId: number, domainId: number) => {
  return api.delete<ApiResponse<null>>(`${BASE_URL}/app-configs/${configId}/domains/${domainId}`);
};

// 3.5 修改单条域名（按 domain_id，部分更新）
export const patchAppConfigDomain = (
  configId: number,
  domainId: number,
  data: PatchDomainRequest
) => {
  return api.patch<ApiResponse<AppConfigDomain>>(
    `${BASE_URL}/app-configs/${configId}/domains/${domainId}`,
    data
  );
};
