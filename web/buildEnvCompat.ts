type BuildEnvironment = Record<string, string | undefined>;

export function resolveWebOutDir(environment: BuildEnvironment) {
  return environment.AURORA_AIOPS_WEB_OUT_DIR || environment.KUBEJOJO_WEB_OUT_DIR || 'dist';
}
