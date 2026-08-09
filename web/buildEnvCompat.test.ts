import { describe, expect, it } from 'vitest';

import { resolveWebOutDir } from './buildEnvCompat';

describe('build environment compatibility', () => {
  it('prefers the Aurora output directory variable', () => {
    expect(
      resolveWebOutDir({
        AURORA_AIOPS_WEB_OUT_DIR: 'aurora-dist',
        KUBEJOJO_WEB_OUT_DIR: 'legacy-dist',
      }),
    ).toBe('aurora-dist');
  });

  it('falls back to the legacy output directory for one release', () => {
    expect(resolveWebOutDir({ KUBEJOJO_WEB_OUT_DIR: 'legacy-dist' })).toBe('legacy-dist');
  });
});
