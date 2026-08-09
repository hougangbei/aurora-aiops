const AURORA_STORAGE_KEY = 'aurora-aiops-app';
const LEGACY_STORAGE_KEY = 'kubejojo-app';

export function migrateLegacyAppStorage() {
  if (typeof window === 'undefined') return;
  if (localStorage.getItem(AURORA_STORAGE_KEY) !== null) return;

  const legacyState = localStorage.getItem(LEGACY_STORAGE_KEY);
  if (legacyState !== null) {
    localStorage.setItem(AURORA_STORAGE_KEY, legacyState);
  }
}

export { AURORA_STORAGE_KEY };
