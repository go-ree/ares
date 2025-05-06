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

// 应用信息接口
export interface AppInfo {
  appId: number
  appName: string
  appNameCn: string
  owner: string
  ownerCn: string
  devLanguage: DevLanguage
  descriptionCn: string
  gitUrl: string
  createdAt: string
  updatedAt: string
  deletedAt?: string
  status?: AppStatus
  version?: string
  lastDeployTime?: string
  lastDeployUser?: string
  lastDeployStatus?: string
  lastDeployComment?: string
}

// 应用查询参数接口
export interface AppQueryParams {
  pageNum: number
  pageSize: number
  appName?: string
  appNameCn?: string
  owner?: string
  ownerCn?: string
  devLanguage?: DevLanguage
  gitUrl?: string
  status?: AppStatus
}

// 分页响应接口
export interface PageResponse<T> {
  total: number
  list: T[]
  pageNum: number
  pageSize: number
} 