export type ReleaseInputValue = string;
export type ReleaseInputs = Record<string, ReleaseInputValue>;

export interface ReleaseStepSummary {
  key: string;
  name: string;
  uses: string;
  position?: number;
  available?: boolean;
  capabilities?: {
    logs: boolean;
    cancel: boolean;
  };
  error_code?: string;
}

export interface ReleaseTarget {
  config_id?: number;
  app_id: number;
  app_name: string;
  app_name_cn: string;
  env: string;
  workflow_version_id?: string;
  available: boolean;
  unavailable_code?: string;
  steps: ReleaseStepSummary[];
}

export interface ReleaseTargetQuery {
  env: string;
  q?: string;
  page_num: number;
  page_size: number;
}

export interface ReleaseTargetPage {
  total: number;
  page_num: number;
  page_size: number;
  total_pages: number;
  targets: ReleaseTarget[];
}

export interface ReleaseRequest {
  ref: string;
  inputs: ReleaseInputs;
  expected_workflow_version_id?: string;
}

export interface BatchReleaseItem extends ReleaseRequest {
  config_id: number;
}

export interface BatchReleaseRequest {
  items: BatchReleaseItem[];
}

export interface ReleasePreflightItem {
  request_index?: number;
  config_id: number;
  ready: boolean;
  workflow_version_id?: string;
  app_id?: number;
  app_name?: string;
  app_name_cn?: string;
  env?: string;
  steps: ReleaseStepSummary[];
  error_code?: string;
  error_message?: string;
}

export interface BatchReleasePreflightResult {
  total_count: number;
  ready_count: number;
  failure_count: number;
  items: ReleasePreflightItem[];
}

export interface ReleaseReceiptItem {
  request_index: number;
  config_id: number;
  success: boolean;
  task_id?: number;
  workflow_version_id?: string;
  error_code?: string;
}

export interface ReleaseReceipt {
  record_id: string;
  total_count: number;
  success_count: number;
  failure_count: number;
  items: ReleaseReceiptItem[];
}

export interface ReleaseApiEnvelope<T> {
  code: number;
  message?: string;
  msg?: string;
  result: T;
  error?: string | null;
  help?: string;
}
