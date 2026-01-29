// 应用状态枚举
export enum AppStatus {
  PENDING = 'PENDING', // 待审核
  APPROVED = 'APPROVED', // 已通过
  REJECTED = 'REJECTED', // 已拒绝
  DEPLOYED = 'DEPLOYED', // 已部署
}

// 开发语言枚举
export enum DevLanguage {
  JAVA = 'JAVA',
  PYTHON = 'PYTHON',
  GO = 'GOLANG',
  NODE = 'NODE.JS',
  // PHP = 'PHP',
  // OTHER = 'OTHER'
}

// API 响应接口
export interface ApiResponse<T> {
  code: number;
  message: string;
  result: T;
  error: string | null;
  help: string;
}

// 分页响应接口
export interface PageResponse<T> {
  total: number;
  page_num: number;
  page_size: number;
  total_pages: number;
  apps: T[];
}

// 应用信息接口
export interface AppInfo {
  app_id: number;
  app_name: string;
  app_name_cn: string;
  owner: string;
  owner_cn: string;
  // 后端可能返回枚举或字符串（如 "GOLANG"/"golang"）
  dev_language: DevLanguage | string;
  description_cn: string;
  git_url: string;
  rundeck_app_name?: string | null;
  created_at: string;
  updated_at: string;
  deleted_at: string | null;
  status?: AppStatus;
  version?: string;
  last_deploy_time?: string;
  last_deploy_user?: string;
  last_deploy_status?: string;
  last_deploy_comment?: string;
}

// 应用查询参数接口
export interface AppQueryParams {
  page_num: number;
  page_size: number;
  app_name?: string;
  app_name_cn?: string;
  owner?: string;
  owner_cn?: string;
  dev_language?: DevLanguage | string;
  git_url?: string;
  status?: AppStatus;
}

// PATCH /apps/{app_id}：只传要改的字段
export interface PatchAppRequest {
  app_name_cn?: string;
  owner?: string;
  owner_cn?: string;
  dev_language?: string;
  description_cn?: string;
  git_url?: string;
  rundeck_app_name?: string;
}

// =========================
// AppConfig（应用环境配置）
// =========================

export type AppEnv = 'dev' | 'test' | 'moni';

// 应用环境配置（与后端 AppConfigs 对齐）
export interface AppConfig {
  config_id: number;
  app_id: number;
  env: AppEnv;

  code_package_type?: string;
  code_package_path?: string;
  code_package_name?: string;
  base_image?: string;

  pod_count?: number;
  limits_memory?: number;
  gpu_count?: number;
  // 应用端口号
  container_port?: number;

  probe_type?: string;
  probe_check_path?: string;
  // TCP 探针端口（后端字段）
  probe_check_tcp_port?: number;
  // HTTP 探针端口（后端字段）
  probe_check_http_port?: number;
  // PreStop HTTP 探针端口（后端字段）
  probe_stop_check_http_port?: number;
  // 兼容字段：历史版本可能仍返回/使用 probe_check_port
  probe_check_port?: number;

  pre_stop_type?: string;
  pre_stop_check_path?: string;
  pre_stop_check_port?: number;
  pre_stop_command?: string;

  created_at?: string;
  updated_at?: string;
  deleted_at?: string | null;
}

// 更新应用环境配置（PATCH：仅更新传入字段）
export interface UpdateAppConfigRequest {
  code_package_type?: string;
  code_package_path?: string;
  code_package_name?: string;
  base_image?: string;
  pod_count?: number;
  limits_memory?: number;
  gpu_count?: number;
  // 应用端口号
  container_port?: number;
  probe_type?: string;
  probe_check_path?: string;
  // TCP 探针端口（后端字段）
  probe_check_tcp_port?: number;
  // HTTP 探针端口（后端字段）
  probe_check_http_port?: number;
  // PreStop HTTP 探针端口（后端字段）
  probe_stop_check_http_port?: number;
  // 兼容字段：历史版本可能仍使用 probe_check_port
  probe_check_port?: number;
  pre_stop_type?: string;
  pre_stop_check_path?: string;
  pre_stop_check_port?: number;
  pre_stop_command?: string;
}

// 创建应用环境配置（POST：未传字段按默认值落库）
export interface CreateAppConfigRequest extends UpdateAppConfigRequest {
  env: AppEnv;
}

// 多域名（Ingress host/path）
export interface AppConfigDomain {
  id: number;
  config_id: number;
  host: string;
  path: string;
  created_at?: string;
  updated_at?: string;
  deleted_at?: string | null;
}

export interface DomainItem {
  host: string;
  path?: string;
}

export interface UpsertDomainsRequest {
  domains: DomainItem[];
}

export interface PatchDomainRequest {
  host?: string;
  path?: string;
}
