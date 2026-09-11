import { createPinia, setActivePinia } from 'pinia';
import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, inject, provide, type PropType } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { TaskRecord, TaskStepRecord } from '@/models/deploy';
import type { DeployingService } from '@/types/deploy';
import { useAuthStore } from '@/stores/auth';
import { PERMISSIONS } from '@/types/auth';
import LogDetail from './LogDetail.vue';

const context = vi.hoisted(() => ({
  getTaskDetail: vi.fn(),
  getTaskAttempts: vi.fn(),
  retryTaskStep: vi.fn(),
}));

type TestTableRow = Record<string, unknown>;
const tableRowsKey = Symbol('tableRows');

const SlotStub = defineComponent({
  inheritAttrs: false,
  setup(_, { attrs, slots }) {
    return () => h('div', attrs, slots.default?.());
  },
});

const ButtonStub = defineComponent({
  inheritAttrs: false,
  emits: ['click'],
  setup(_, { attrs, emit, slots }) {
    return () =>
      h(
        'button',
        {
          ...attrs,
          onClick: (event: MouseEvent) => emit('click', event),
        },
        slots.default?.()
      );
  },
});

const TableStub = defineComponent({
  props: {
    data: {
      type: Array as PropType<TestTableRow[]>,
      default: () => [],
    },
  },
  setup(props, { slots }) {
    provide(tableRowsKey, () => props.data);
    return () => h('div', { class: 'table-stub' }, slots.default?.());
  },
});

const TableColumnStub = defineComponent({
  props: {
    prop: {
      type: String,
      default: '',
    },
  },
  setup(props, { slots }) {
    const rows = inject<() => TestTableRow[]>(tableRowsKey, () => []);
    return () =>
      h(
        'div',
        { class: 'table-column-stub' },
        slots.default
          ? rows().flatMap(row => slots.default?.({ row }) || [])
          : rows().map(row => String(row[props.prop] ?? ''))
      );
  },
});

vi.mock('@/services/deploy', async importOriginal => ({
  ...(await importOriginal<typeof import('@/services/deploy')>()),
  getTaskDetail: context.getTaskDetail,
  getTaskAttempts: context.getTaskAttempts,
  retryTaskStep: context.retryTaskStep,
}));

class FakeEventSource extends EventTarget {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];

  readonly url: string;
  readyState = FakeEventSource.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  constructor(url: string | URL) {
    super();
    this.url = String(url);
    FakeEventSource.instances.push(this);
  }

  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
}

const summary: DeployingService = {
  id: 7,
  taskId: 7,
  serviceName: 'api',
  branch: 'main',
  environment: 'prod',
  status: '执行中',
  progress: 50,
  startTime: '2026-09-07 10:00:00',
  operator: 'Reader',
};

const step = (stepKey: string, position: number, logs: boolean): TaskStepRecord => ({
  step_record_id: position + 1,
  task_id: 7,
  workflow_version_id: 3,
  step_key: stepKey,
  name: stepKey === 'build' ? '构建' : '部署',
  uses: logs ? 'jenkins.job@v1' : 'builtin.noop@v1',
  position,
  timeout_seconds: 60,
  on_failure: 'stop',
  status: 'running',
  attempt: 1,
  capabilities: { logs, cancel: false },
  created_at: '2026-09-07T00:00:00Z',
  updated_at: '2026-09-07T00:00:00Z',
});

const task = (overrides: Partial<TaskRecord> = {}): TaskRecord => ({
  task_id: 7,
  app_name: 'api',
  branch: 'main',
  env: 'prod',
  publisher: 'Reader',
  status: 'running',
  message: '',
  auto_deploy: 0,
  products: '',
  engine_version: 2,
  workflow_version_id: 3,
  steps: [],
  created_at: '2026-09-07T00:00:00Z',
  updated_at: '2026-09-07T00:00:00Z',
  deleted_at: null,
  ...overrides,
});

describe('LogDetail step capabilities', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
    fetchMock = vi.fn(
      (_input: RequestInfo | URL, init?: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => {
            reject(new DOMException('aborted', 'AbortError'));
          });
        })
    );
    vi.stubGlobal('fetch', fetchMock);
    const pinia = createPinia();
    setActivePinia(pinia);
    useAuthStore().$patch({
      status: 'authenticated',
      user: {
        id: '1',
        username: 'reader',
        display_name: 'Reader',
        auth_source: 'oidc',
        roles: ['viewer'],
        permissions: [PERMISSIONS.TASKS_READ, PERMISSIONS.LOGS_READ],
      },
      csrfToken: 'csrf',
      expiresAt: '2099-01-01T00:00:00Z',
    });
    context.getTaskDetail.mockReset();
    context.getTaskAttempts.mockReset();
    context.retryTaskStep.mockReset();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.innerHTML = '';
  });

  const mountDetail = () =>
    mount(LogDetail, {
      attachTo: document.body,
      props: { visible: true, logData: summary },
      global: {
        directives: { loading: () => undefined },
        stubs: {
          ElAlert: SlotStub,
          ElButton: ButtonStub,
          ElDescriptions: SlotStub,
          ElDescriptionsItem: SlotStub,
          ElDialog: SlotStub,
          ElEmpty: SlotStub,
          ElIcon: SlotStub,
          ElOption: SlotStub,
          ElSelect: SlotStub,
          ElTabPane: SlotStub,
          ElTable: TableStub,
          ElTableColumn: TableColumnStub,
          ElTabs: SlotStub,
          ElTag: SlotStub,
          ElTooltip: SlotStub,
          Loading: true,
          Teleport: true,
        },
      },
    });

  it.each([
    ['timed_out', '执行超时'],
    ['outcome_unknown', '执行结果待核查'],
  ])('keeps logs and shows external execution warning for %s', async (status, label) => {
    context.getTaskDetail.mockResolvedValue({
      data: { code: 1, result: task({ status, steps: [{ ...step('build', 0, true), status }] }) },
    });
    const wrapper = mountDetail();
    await flushPromises();
    expect(wrapper.text()).toContain(label);
    expect(wrapper.find('[title*="外部任务可能仍在运行"]').exists()).toBe(true);
    expect(wrapper.findAll('.step-log-button')).toHaveLength(1);
    wrapper.unmount();
  });

  it('loads attempt history and only offers retry to a release operator', async () => {
    const failedStep = {
      ...step('build', 0, true),
      status: 'failed',
      retry_eligible: true,
      max_attempts: 3,
    };
    context.getTaskDetail.mockResolvedValue({
      data: { code: 1, result: task({ status: 'failed', steps: [failedStep] }) },
    });
    context.getTaskAttempts.mockResolvedValue({
      data: { code: 1, result: [{ attempt: 1, status: 'failed', message: '第一次失败' }] },
    });
    context.retryTaskStep.mockResolvedValue({ data: { code: 1 } });
    const wrapper = mountDetail();
    await flushPromises();
    expect(wrapper.find('.step-retry-button').exists()).toBe(false);
    await wrapper.find('.attempt-history-button').trigger('click');
    await flushPromises();
    expect(context.getTaskAttempts).toHaveBeenCalledWith(7, 'build');
    expect(wrapper.text()).toContain('第一次失败');
    useAuthStore().user!.permissions.push(PERMISSIONS.RELEASES_CREATE);
    await flushPromises();
    await wrapper.find('.step-retry-button').trigger('click');
    await flushPromises();
    expect(context.retryTaskStep).toHaveBeenCalledExactlyOnceWith(7, 'build', 1);
    wrapper.unmount();
  });

  it('renders a log action only for v2 steps that declare capabilities.logs', async () => {
    context.getTaskDetail.mockResolvedValue({
      data: {
        code: 1,
        message: 'ok',
        result: task({
          steps: [step('deploy', 1, false), step('build', 0, true)],
          ci_job_name: 'must-not-fallback',
          ci_build_id: 42,
        }),
      },
    });
    const wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.text()).toContain('构建');
    expect(wrapper.text()).toContain('部署');
    expect(wrapper.findAll('.step-log-button')).toHaveLength(1);
    expect(wrapper.findAll('.legacy-log-button')).toHaveLength(0);

    await wrapper.find('.step-log-button').trigger('click');
    await flushPromises();
    expect(FakeEventSource.instances).toHaveLength(0);
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/tasks/7/steps/build/logs/stream?attempt=1',
      expect.objectContaining({ credentials: 'include', redirect: 'error', mode: 'same-origin' })
    );
    wrapper.unmount();
  });

  it('keeps v1 Jenkins logs behind the deprecated task-scoped adapter', async () => {
    context.getTaskDetail.mockResolvedValue({
      data: {
        code: 1,
        message: 'ok',
        result: task({
          engine_version: 1,
          ci_job_name: 'folder/api_ci',
          ci_build_id: 42,
          cd_job_name: '',
          cd_build_id: 0,
          steps: [],
        }),
      },
    });
    const wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.findAll('.step-log-button')).toHaveLength(0);
    expect(wrapper.findAll('.legacy-log-button')).toHaveLength(1);
    await wrapper.find('.legacy-log-button').trigger('click');
    await flushPromises();

    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.instances[0].url).toBe('/api/v1/job/stream/log?task_id=7&log_type=ci');
    expect(FakeEventSource.instances[0].url).not.toContain('folder');
    wrapper.unmount();
  });

  it('does not expose a stream action without logs:read', async () => {
    useAuthStore().$patch(state => {
      if (state.user) state.user.permissions = [PERMISSIONS.TASKS_READ];
    });
    context.getTaskDetail.mockResolvedValue({
      data: { code: 1, message: 'ok', result: task({ steps: [step('build', 0, true)] }) },
    });
    const wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.findAll('.step-log-button')).toHaveLength(0);
    expect(wrapper.text()).toContain('无权限');
    expect(FakeEventSource.instances).toHaveLength(0);
    wrapper.unmount();
  });

  it('does not expose the v1 compatibility action without logs:read', async () => {
    useAuthStore().$patch(state => {
      if (state.user) state.user.permissions = [PERMISSIONS.TASKS_READ];
    });
    context.getTaskDetail.mockResolvedValue({
      data: {
        code: 1,
        message: 'ok',
        result: task({
          engine_version: 1,
          ci_job_name: 'folder/api_ci',
          ci_build_id: 42,
        }),
      },
    });
    const wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.findAll('.legacy-log-button')).toHaveLength(0);
    expect(wrapper.text()).toContain('无日志读取权限');
    expect(FakeEventSource.instances).toHaveLength(0);
    wrapper.unmount();
  });
});
