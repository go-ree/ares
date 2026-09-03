// 发布状态枚举
export enum DeployStatus {
  PENDING = 'PENDING', // 待发布
  DEPLOYING = 'DEPLOYING', // 发布中
  SUCCESS = 'SUCCESS', // 发布成功
  FAILED = 'FAILED', // 发布失败
  CANCELLED = 'CANCELLED', // 已取消
}

export type Environment = string;

// 发布信息接口
export interface DeployInfo {
  deploy_id: number;
  app_id: number;
  app_name: string;
  app_name_cn: string;
  environment: Environment;
  branch: string;
  version: string;
  publisher: string; // 发布人用户名
  publisher_cn: string; // 发布人中文名
  status: DeployStatus;
  start_time: string;
  end_time?: string;
  log_url?: string;
  comment?: string;
  created_at: string;
  updated_at: string;
}

// 单个发布请求参数接口
export interface DeployRequest {
  app_name: string;
  env: string;
  branch: string;
  publisher: string;
  // 是否走 rundeck（可选）
  is_rundeck?: boolean;
  // 额外发布参数（可选）
  extra_data?: Record<string, any>;
}

// 批量发布请求参数接口
export interface BatchDeployRequest {
  batch_publish: DeployRequest[];
}

// 任务记录接口
export interface TaskRecord {
  task_id: number;
  app_name: string;
  branch: string;
  env: string;
  publisher: string;
  ci_build_id: number;
  cd_build_id: number;
  status: string;
  message: string;
  ci_job_name: string;
  cd_job_name: string;
  auto_deploy: number;
  products: string;
  engine_version?: number;
  workflow_version_id?: number;
  steps?: TaskStepRecord[];
  created_at: string;
  updated_at: string;
  deleted_at: string | null;
}

export interface TaskStepRecord {
  step_record_id: number;
  task_id: number;
  workflow_version_id: number;
  step_key: string;
  name: string;
  uses: string;
  category?: string;
  position: number;
  timeout_seconds: number;
  on_failure: 'stop' | 'continue';
  status: string;
  attempt: number;
  message?: string;
  started_at?: string | null;
  finished_at?: string | null;
  created_at: string;
  updated_at: string;
}

// 任务记录结果接口
export interface TaskRecordResult {
  request_index: number;
  app_name: string;
  env: string;
  task_record: TaskRecord | null;
  error: string;
  success: boolean;
}

// 批量发布响应接口
export interface BatchDeployResponse {
  success_count: number;
  failure_count: number;
  total_count: number;
  task_records: TaskRecordResult[];
}

// 发布查询参数接口
export interface DeployQueryParams {
  page_num: number;
  page_size: number;
  app_id?: number;
  app_name?: string;
  environment?: Environment;
  publisher?: string;
  status?: DeployStatus;
  start_time?: string;
  end_time?: string;
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
  deploys: T[];
}

// 发布日志查询参数
export interface PublishLogQueryParams {
  app_name?: string;
  branch?: string;
  end_time?: string;
  env?: string;
  page_num?: number;
  page_size?: number;
  publisher?: string;
  sort?: {
    direction: string;
    field: string;
  };
  start_time?: string;
}

// 发布日志任务记录
export interface PublishLogTaskRecord {
  app_name: string;
  auto_deploy: number;
  branch: string;
  cd_build_id: number;
  cd_job_name: string;
  ci_build_id: number;
  ci_job_name: string;
  created_at: string;
  deleted_at: string | null;
  env: string;
  message: string;
  products: string;
  publisher: string;
  status: string;
  task_id: number;
  updated_at: string;
}

// 发布日志查询响应
export interface PublishLogQueryResponse {
  total: number;
  page_num: number;
  page_size: number;
  total_pages: number;
  task_record: PublishLogTaskRecord[];
}
