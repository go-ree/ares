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
  progressIndeterminate?: boolean;
  settledSteps?: number;
  totalSteps?: number;
  startTime: string;
  operator: string;
  message?: string;
  taskId: number;
  products?: string;
  auto_deploy?: number;
}

// 服务信息接口
export interface ServiceInfo {
  name: string;
  nameCn: string;
  description?: string;
}

// 日志筛选条件
export interface LogFilter {
  serviceName: string;
  environment: string;
  dateRange: Date[];
}
