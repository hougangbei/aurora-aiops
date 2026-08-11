import { http } from '../../services/http';
import type {
  AssetServer,
  AssetSnapshot,
  AssetSoftwareItem,
  ConnectionResult,
  CreateAssetServerInput,
} from './types';

type Envelope<T> = { code: string; message?: string; data: T };

function unwrap<T>(response: { data: Envelope<T> }): T {
  return response.data.data;
}

const serverPath = (id: string) => `/assets/servers/${encodeURIComponent(id)}`;

export async function listAssetServers(): Promise<AssetServer[]> {
  return unwrap(await http.get('/assets/servers'));
}

export async function createAssetServer(input: CreateAssetServerInput): Promise<AssetServer> {
  return unwrap(await http.post('/assets/servers', input));
}

export async function getAssetServer(id: string): Promise<AssetServer> {
  return unwrap(await http.get(serverPath(id)));
}

export async function testAssetConnection(id: string): Promise<ConnectionResult> {
  return unwrap(await http.post(`${serverPath(id)}/test-connection`));
}

export async function confirmAssetHostKey(id: string, fingerprint: string): Promise<void> {
  await http.post(`${serverPath(id)}/confirm-host-key`, { fingerprint });
}

export async function collectAssetServer(id: string): Promise<AssetSnapshot> {
  return unwrap(await http.post(`${serverPath(id)}/collect`));
}

export async function getLatestAssetSnapshot(id: string): Promise<AssetSnapshot> {
  return unwrap(await http.get(`${serverPath(id)}/snapshots/latest`));
}

export async function listAssetSoftware(id: string): Promise<AssetSoftwareItem[]> {
  return unwrap(await http.get(`${serverPath(id)}/software`));
}
