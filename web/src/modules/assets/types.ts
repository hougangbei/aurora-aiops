export type AssetServerStatus = 'pending' | 'online' | 'offline' | 'error';
export type CredentialAuthType = 'password' | 'private_key';

export type AssetServer = {
  id: string;
  name: string;
  address: string;
  username: string;
  hostKeyFingerprint?: string;
  /** Whether the currently presented SSH host key has been explicitly trusted. */
  hostKeyConfirmed?: boolean;
  sshPort: number;
  status: AssetServerStatus;
  statusMessage?: string;
  osFamily?: string;
  osVersion?: string;
  architecture?: string;
  cpuCores: number;
  memoryBytes: number;
  diskBytes: number;
  lastSeenAt?: string;
  lastCollectedAt?: string;
  createdAt: string;
  updatedAt: string;
  credentialAuthType: CredentialAuthType;
  credentialConfigured: boolean;
};

export type AssetSnapshot = {
  id: string;
  serverId: string;
  osFamily: string;
  osVersion: string;
  kernelVersion: string;
  architecture: string;
  hostname: string;
  cpuCores: number;
  memoryBytes: number;
  diskBytes: number;
  load1: number;
  uptimeSeconds: number;
  collectedAt: string;
};

export type AssetSoftwareItem = {
  category: string;
  name: string;
  version: string;
  architecture: string;
  source: string;
  status: string;
};

export type CreateAssetServerInput = {
  name: string;
  address: string;
  username: string;
  sshPort: number;
  credentialAuthType: CredentialAuthType;
  password?: string;
  privateKey?: string;
  passphrase?: string;
  testConnection: boolean;
};

export type ConnectionResult = {
  fingerprint: string;
  trusted: boolean;
  changed: boolean;
};
