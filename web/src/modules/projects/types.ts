import type { DeploymentTask } from '../deployment/types';

export type ProjectInstallation = {
  id?: string;
  serverId: string;
  serverName?: string;
  version: string;
  status: string;
  finishedAt?: string;
};

export type Project = {
  id: string;
  name: string;
  description: string;
  versions: string[];
  recommendedVersion?: string;
  supportedOsFamilies: string[];
  supportedArchitectures: string[];
  installedServerCount?: number;
  installations?: ProjectInstallation[];
};

export type InstallProjectInput = {
  serverId: string;
  version: string;
  configuration?: {
    bootstrapAdminUser: string;
    bootstrapAdminPassword: string;
  };
};

export type InstallProjectResult = DeploymentTask;
