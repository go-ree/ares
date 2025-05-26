// 应用状态枚举
export enum AppStatus {
  PENDING = 'PENDING',    // 待审核
  APPROVED = 'APPROVED',  // 已通过
  REJECTED = 'REJECTED',  // 已拒绝
  DEPLOYED = 'DEPLOYED'   // 已部署
}

// 开发语言枚举
export enum DevLanguage {
  JAVA = 'JAVA',
  PYTHON = 'PYTHON',
  GO = 'GO',
  NODE = 'NODE',
  PHP = 'PHP',
  OTHER = 'OTHER'
}

// API 响应接口
export interface ApiResponse<T> {
  code: number
  msg: string
  result: T
  error: string | null
  help: string
}

// 分页响应接口
export interface PageResponse<T> {
  total: number
  page_num: number
  page_size: number
  total_pages: number
  apps: T[]
}

// 应用信息接口
export interface AppInfo {
  app_id: number
  app_name: string
  app_name_cn: string
  owner: string
  owner_cn: string
  dev_language: DevLanguage
  description_cn: string
  git_url: string
  created_at: string
  updated_at: string
  deleted_at: string | null
  status?: AppStatus
  version?: string
  last_deploy_time?: string
  last_deploy_user?: string
  last_deploy_status?: string
  last_deploy_comment?: string
}

// 应用查询参数接口
export interface AppQueryParams {
  page_num: number
  page_size: number
  app_name?: string
  app_name_cn?: string
  owner?: string
  owner_cn?: string
  dev_language?: DevLanguage
  git_url?: string
  status?: AppStatus
} 