import { describe, expect, it } from 'vitest';
import { calculateTaskProgress } from './useDeploy';

describe('active release progress', () => {
  it('derives progress from settled workflow steps', () => {
    expect(calculateTaskProgress(2, { total: 5, settled: 3 })).toEqual({
      percentage: 60,
      indeterminate: false,
      settled: 3,
      total: 5,
    });
  });

  it('uses indeterminate progress for legacy tasks and empty step snapshots', () => {
    expect(calculateTaskProgress(1, { total: 5, settled: 3 }).indeterminate).toBe(true);
    expect(calculateTaskProgress(2, { total: 0, settled: 0 }).indeterminate).toBe(true);
  });

  it('bounds invalid settled counts to the snapshot total', () => {
    expect(calculateTaskProgress(2, { total: 2, settled: 9 })).toEqual({
      percentage: 100,
      indeterminate: false,
      settled: 2,
      total: 2,
    });
  });
});
