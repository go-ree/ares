import { onMounted, onUnmounted, type Ref } from 'vue';
import { onBeforeRouteLeave } from 'vue-router';
import type {
  FrozenReleaseSubmission,
  ReleaseComposerPhase,
} from '@/composables/useReleaseComposer';

export const shouldProtectFrozenRelease = (
  phase: ReleaseComposerPhase,
  frozenSubmission: FrozenReleaseSubmission | null
): boolean => frozenSubmission !== null && (phase === 'submitting' || phase === 'uncertain');

export const useFrozenReleaseNavigationGuard = (
  phase: Ref<ReleaseComposerPhase>,
  frozenSubmission: Ref<FrozenReleaseSubmission | null>,
  onRouteBlocked?: () => void
) => {
  const isProtected = () => shouldProtectFrozenRelease(phase.value, frozenSubmission.value);

  const handleBeforeUnload = (event: BeforeUnloadEvent) => {
    if (!isProtected()) return;
    event.preventDefault();
    event.returnValue = '';
  };

  onBeforeRouteLeave(() => {
    if (!isProtected()) return true;
    onRouteBlocked?.();
    return false;
  });

  onMounted(() => window.addEventListener('beforeunload', handleBeforeUnload));
  onUnmounted(() => window.removeEventListener('beforeunload', handleBeforeUnload));

  return { isProtected };
};
