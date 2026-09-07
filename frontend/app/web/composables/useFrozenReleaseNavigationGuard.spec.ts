import { defineComponent, ref } from 'vue';
import { mount } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { NavigationGuard } from 'vue-router';
import type {
  FrozenReleaseSubmission,
  ReleaseComposerPhase,
} from '@/composables/useReleaseComposer';
import { useFrozenReleaseNavigationGuard } from './useFrozenReleaseNavigationGuard';

const routeGuard = vi.hoisted(() => ({ callback: undefined as NavigationGuard | undefined }));

vi.mock('vue-router', () => ({
  onBeforeRouteLeave: vi.fn((callback: NavigationGuard) => {
    routeGuard.callback = callback;
  }),
}));

const frozen: FrozenReleaseSubmission = {
  key: '08db8664-f05b-47a1-bc5e-3f4299827457',
  actorUserId: '42',
  body: { items: [{ config_id: 7, ref: 'main', inputs: {} }] },
};

describe('frozen release navigation guard', () => {
  beforeEach(() => {
    routeGuard.callback = undefined;
  });

  it('blocks route leave and beforeunload while a frozen submission can have committed', () => {
    const phase = ref<ReleaseComposerPhase>('uncertain');
    const submission = ref<FrozenReleaseSubmission | null>(frozen);
    const onRouteBlocked = vi.fn();
    const wrapper = mount(
      defineComponent({
        setup() {
          useFrozenReleaseNavigationGuard(phase, submission, onRouteBlocked);
          return () => null;
        },
      })
    );

    expect(routeGuard.callback?.({} as never, {} as never, () => undefined)).toBe(false);
    expect(onRouteBlocked).toHaveBeenCalledTimes(1);

    const unload = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(unload);
    expect(unload.defaultPrevented).toBe(true);

    wrapper.unmount();
    const afterUnmount = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(afterUnmount);
    expect(afterUnmount.defaultPrevented).toBe(false);
  });

  it('allows navigation before submission and after a definitive result', () => {
    const phase = ref<ReleaseComposerPhase>('ready');
    const submission = ref<FrozenReleaseSubmission | null>(null);
    const wrapper = mount(
      defineComponent({
        setup() {
          useFrozenReleaseNavigationGuard(phase, submission);
          return () => null;
        },
      })
    );

    expect(routeGuard.callback?.({} as never, {} as never, () => undefined)).toBe(true);
    const unload = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(unload);
    expect(unload.defaultPrevented).toBe(false);

    wrapper.unmount();
  });
});
