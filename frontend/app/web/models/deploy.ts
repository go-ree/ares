export interface TaskRecord {
  task_id: number;
  app_name: string;
  branch: string;
  env: string;
  publisher: string;
  /** @deprecated 仅供 engine_version < 2 的历史 Jenkins 日志兼容。 */
  ci_build_id?: number;
  /** @deprecated 仅供 engine_version < 2 的历史 Jenkins 日志兼容。 */
  cd_build_id?: number;
  status: string;
  message: string;
  /** @deprecated 仅供 engine_version < 2 的历史 Jenkins 日志兼容。 */
  ci_job_name?: string;
  /** @deprecated 仅供 engine_version < 2 的历史 Jenkins 日志兼容。 */
  cd_job_name?: string;
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
  capabilities?: TaskStepCapabilities;
  message?: string;
  started_at?: string | null;
  finished_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface TaskStepCapabilities {
  logs: boolean;
  cancel: boolean;
}

export interface ApiResponse<T> {
  code: number;
  message: string;
  result: T;
  error: string | null;
  help: string;
}

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

export interface PublishLogTaskRecord {
  app_name: string;
  auto_deploy: number;
  branch: string;
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

export interface PublishLogQueryResponse {
  total: number;
  page_num: number;
  page_size: number;
  total_pages: number;
  task_record: PublishLogTaskRecord[];
}
