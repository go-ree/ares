// 日志数据接口
export interface LogItem {
  task_id: number;
  serviceName: string;
  branch: string;
  environment: string;
  status: string;
  deployTime: string;
  operator: string;
  message: string;
  auto_deploy: number;
  ci_job_name: string;
  cd_job_name: string;
  ci_build_id: number;
  cd_build_id: number;
  products: string;
}

// 发布中服务列表数据
export interface DeployingService {
  id: number;
  serviceName: string;
  branch: string;
  environment: string;
  status: string;
  progress: number;
  startTime: string;
  operator: string;
  message?: string;
  taskId: number;
  ciJobName?: string;
  cdJobName?: string;
  ciBuildId?: number;
  cdBuildId?: number;
  products?: string;
  auto_deploy?: number;
}

// 服务信息接口
export interface ServiceInfo {
  name: string;
  nameCn: string;
  description?: string;
}

// 选中的服务接口
export interface SelectedService {
  serviceName: string;
  branch: string;
  branchSuffix?: string;
  status: string;
  lastUpdateTime?: string;
  taskId?: number;
}

// 发布表单数据
export interface DeployForm {
  serviceName: string;
  environment: string;
  version: string;
}

// 日志筛选条件
export interface LogFilter {
  serviceName: string;
  environment: string;
  dateRange: any[];
}

// 环境类型
export type Environment = string;

// 发布状态类型
export type DeployStatus =
  | 'queued'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'succeeded_with_warnings'
  | 'init'
  | 'packaging'
  | 'packaged'
  | 'package_failed'
  | 'deploying'
  | 'deployed'
  | 'deploy_failed'
  | 'cancelled'
  | 'timeout'
  | 'unknown';
