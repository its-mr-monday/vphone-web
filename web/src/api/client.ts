// Typed client for the vphone-web REST/WebSocket API. Interfaces mirror the Go
// response structs in internal/vm and internal/api.

export type VMStatus =
  | "CREATING"
  | "RESTORING"
  | "INSTALLING_CFW"
  | "STOPPED"
  | "BOOTING"
  | "RUNNING"
  | "STOPPING"
  | "DELETING"
  | "ERROR";

export type Variant = "regular" | "dev" | "jb" | "exp";

export interface PortBlock {
  base: number;
  vnc: number;
  ssh: number;
  ssh2: number;
  rpc: number;
  frida: number;
}

export interface VM {
  id: string;
  name: string;
  status: VMStatus;
  variant: Variant;
  ios_version: string;
  ipsw_id?: string;
  network_mode: string;
  network_interface?: string;
  cpu: number;
  memory: number;
  disk_size: number;
  port_block_base: number;
  vm_dir: string;
  pid?: number;
  error_message?: string;
  created_at: string;
  updated_at: string;
  ports: PortBlock;
  screen_width: number;
  screen_height: number;
  vnc_password: string;
}

export type NetworkMode = "nat" | "bridged" | "hostOnly" | "none";

/** Live guest metadata read from a running VM's control socket. */
export interface GuestInfo {
  connected: boolean;
  ip?: string;
  ios?: string;
  name?: string;
}

/** Guest frida-server state. */
export interface FridaStatus {
  installed: boolean;
  running: boolean;
  version?: string;
  port: number;
}

/** One entry from frida-ps (a running application on the guest). */
export interface FridaProcess {
  pid: number;
  name: string;
  identifier?: string;
}

export interface CreateVMRequest {
  name: string;
  variant: Variant;
  ios_version?: string;
  ipsw_id?: string;
  network_mode?: NetworkMode;
  network_interface?: string;
  cpu?: number;
  memory?: number;
  disk_size?: number;
}

export interface HostInterface {
  name: string;
  type: string;
  wired: boolean;
  addrs: string[];
}

export type JobStatus =
  | "PENDING"
  | "RUNNING"
  | "COMPLETED"
  | "FAILED"
  | "CANCELLED";

export interface Job {
  id: string;
  vm_id?: string;
  ipsw_id?: string;
  type: string;
  label: string;
  status: JobStatus;
  exit_code?: number;
  error?: string;
  output?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}

export type IPSWStatus = "REGISTERED" | "DOWNLOADING" | "READY" | "ERROR";

export interface IPSW {
  id: string;
  version: string;
  build: string;
  device: string;
  source_url?: string;
  file_path: string;
  status: IPSWStatus;
  size: number;
  sha256?: string;
  error?: string;
  created_at: string;
  updated_at: string;
  managed: boolean;
}

export interface Snapshot {
  id: string;
  vm_id: string;
  name: string;
  size: number;
  created_at: string;
}

export interface DiskUsage {
  path: string;
  total_bytes: number;
  free_bytes: number;
  used_bytes: number;
}

export interface SystemStatus {
  hostname: string;
  os: string;
  arch: string;
  num_cpu: number;
  go_max_procs: number;
  total_vms: number;
  running_vms: number;
  vms_by_status: Record<string, number>;
  max_concurrent_vms: number;
  active_jobs: number;
  alloc_mb: number;
  vphone_cli_dir: string;
  vm_disk: DiskUsage;
  ipsw_disk: DiskUsage;
}

export interface SystemConfig {
  vm_root: string;
  ipsw_dir: string;
  vphone_cli_dir: string;
  port_base: number;
  port_block_size: number;
  max_concurrent_vms: number;
  max_concurrent_jobs: number;
}

/** Error thrown by request() carrying the HTTP status and server message. */
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });

  if (!res.ok) {
    let message = res.statusText;
    try {
      const body = (await res.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // non-JSON error body; keep statusText
    }
    throw new ApiError(res.status, message);
  }

  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const api = {
  // VMs
  listVMs: () => request<VM[]>("/vms"),
  getVM: (id: string) => request<VM>(`/vms/${id}`),
  createVM: (body: CreateVMRequest) =>
    request<VM>("/vms", { method: "POST", body: JSON.stringify(body) }),
  importVM: (body: { name: string; vm_dir: string; variant: Variant; ios_version?: string }) =>
    request<VM>("/vms/import", { method: "POST", body: JSON.stringify(body) }),
  updateVM: (
    id: string,
    body: {
      name?: string;
      cpu?: number;
      memory?: number;
      network_mode?: NetworkMode;
      network_interface?: string;
    },
  ) => request<VM>(`/vms/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  deleteVM: (id: string) => request<{ job_id?: string }>(`/vms/${id}`, { method: "DELETE" }),
  exportVMURL: (id: string) => `/api/v1/vms/${id}/export`,
  bootVM: (id: string) => request<VM>(`/vms/${id}/boot`, { method: "POST" }),
  stopVM: (id: string) => request<VM>(`/vms/${id}/stop`, { method: "POST" }),
  restartVM: (id: string) => request<VM>(`/vms/${id}/restart`, { method: "POST" }),

  // Control
  touch: (id: string, body: { type?: "tap" | "swipe"; x: number; y: number; x2?: number; y2?: number; ms?: number }) =>
    request<{ ok: boolean }>(`/vms/${id}/touch`, { method: "POST", body: JSON.stringify(body) }),
  key: (id: string, key: string) =>
    request<{ ok: boolean }>(`/vms/${id}/key`, { method: "POST", body: JSON.stringify({ key }) }),
  screenshotURL: (id: string) => `/api/v1/vms/${id}/screenshot`,
  guestInfo: (id: string) => request<GuestInfo>(`/vms/${id}/info`),

  // Frida (dynamic instrumentation)
  fridaStatus: (id: string) => request<FridaStatus>(`/vms/${id}/frida`),
  fridaInstall: (id: string) => request<{ job_id: string }>(`/vms/${id}/frida/install`, { method: "POST" }),
  fridaStart: (id: string) => request<FridaStatus>(`/vms/${id}/frida/start`, { method: "POST" }),
  fridaStop: (id: string) => request<FridaStatus>(`/vms/${id}/frida/stop`, { method: "POST" }),
  fridaProcesses: (id: string) => request<FridaProcess[]>(`/vms/${id}/frida/processes`),
  fridaSetPort: (id: string, port: number) =>
    request<VM>(`/vms/${id}/frida/port`, { method: "POST", body: JSON.stringify({ port }) }),

  // Jobs
  listJobs: (vmID?: string, limit = 100) =>
    request<Job[]>(`/jobs?${vmID ? `vm=${vmID}&` : ""}limit=${limit}`),
  getJob: (id: string) => request<Job>(`/jobs/${id}`),
  cancelJob: (id: string) => request<{ ok: boolean }>(`/jobs/${id}/cancel`, { method: "POST" }),

  // IPSWs
  listIPSWs: () => request<IPSW[]>("/ipsws"),
  registerIPSW: (body: { file_path: string; version?: string; build?: string; device?: string }) =>
    request<IPSW>("/ipsws", { method: "POST", body: JSON.stringify(body) }),
  downloadIPSW: (url: string) =>
    request<{ ipsw: IPSW; job_id: string }>("/ipsws/download", {
      method: "POST",
      body: JSON.stringify({ url }),
    }),
  deleteIPSW: (id: string) => request<void>(`/ipsws/${id}`, { method: "DELETE" }),

  // Snapshots
  listSnapshots: (vmID: string) => request<Snapshot[]>(`/vms/${vmID}/snapshots`),
  createSnapshot: (vmID: string, name: string) =>
    request<{ job_id: string }>(`/vms/${vmID}/snapshots`, {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  restoreSnapshot: (vmID: string, name: string) =>
    request<{ job_id: string }>(`/vms/${vmID}/snapshots/${encodeURIComponent(name)}/restore`, {
      method: "POST",
    }),
  deleteSnapshot: (vmID: string, name: string) =>
    request<void>(`/vms/${vmID}/snapshots/${encodeURIComponent(name)}`, { method: "DELETE" }),

  // System
  systemStatus: () => request<SystemStatus>("/system/status"),
  systemConfig: () => request<SystemConfig>("/system/config"),
  systemInterfaces: () => request<HostInterface[]>("/system/interfaces"),

  // Auth
  me: () => request<AuthStatus>("/auth/me"),
  authProviders: () => request<AuthStatus>("/auth/providers"),
  login: (username: string, password: string) =>
    request<AuthStatus>("/auth/login", { method: "POST", body: JSON.stringify({ username, password }) }),
  logout: () => request<{ ok: boolean }>("/auth/logout", { method: "POST" }),

  // Cluster nodes (admin)
  listNodes: () => request<ClusterNode[]>("/nodes"),
  registerNode: async (body: RegisterNodeRequest): Promise<RegisterNodeResult> => {
    const res = await fetch("/api/v1/nodes", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (res.status === 409) {
      const trust = (await res.json()) as CertTrustPrompt;
      if (trust.needs_trust) return { trust };
    }
    if (!res.ok) {
      let message = res.statusText;
      try {
        const b = (await res.json()) as { error?: string };
        if (b.error) message = b.error;
      } catch {
        /* non-JSON */
      }
      throw new ApiError(res.status, message);
    }
    return { node: (await res.json()) as ClusterNode };
  },
  deleteNode: (id: string) => request<void>(`/nodes/${id}`, { method: "DELETE" }),

  // Users (admin)
  listUsers: () => request<AuthUser[]>("/users"),
  createUser: (body: { username: string; password: string; role: UserRole }) =>
    request<AuthUser>("/users", { method: "POST", body: JSON.stringify(body) }),
  updateUser: (id: string, body: { role?: UserRole; password?: string; disabled?: boolean }) =>
    request<{ ok: boolean }>(`/users/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  deleteUser: (id: string) => request<void>(`/users/${id}`, { method: "DELETE" }),
};

export type UserRole = "vphone-admin" | "vphone-user";

export interface AuthUser {
  id: string;
  username: string;
  email?: string;
  role: UserRole;
  provider: string;
  disabled: boolean;
  must_change?: boolean;
  created_at: string;
  updated_at: string;
}

export interface SSOProvider {
  name: string;
  label: string;
  login_url: string;
}

export interface AuthStatus {
  enabled: boolean;
  user?: AuthUser | null;
  providers?: string[];
  sso?: SSOProvider[];
}

export type NodeStatus = "ONLINE" | "OFFLINE" | "UNKNOWN";

export interface ClusterNode {
  id: string;
  name: string;
  address: string;
  tls: boolean;
  status: NodeStatus;
  hostname?: string;
  chip?: string;
  cpu?: number;
  memory_mb?: number;
  running_vms: number;
  version?: string;
  error?: string;
  cert_fingerprint?: string;
  last_seen?: string;
  created_at: string;
}

/** Request body for registering a worker node. */
export interface RegisterNodeRequest {
  name: string;
  address: string;
  system_password: string;
  tls?: boolean;
  trust_fingerprint?: string;
}

/** Returned when an HTTPS agent's certificate must be accepted (TOFU). */
export interface CertTrustPrompt {
  needs_trust: true;
  fingerprint: string;
  subject?: string;
  issuer?: string;
  expires?: string;
  message: string;
}

/** registerNode returns the node on success, or a trust prompt (HTTP 409). */
export type RegisterNodeResult =
  | { node: ClusterNode }
  | { trust: CertTrustPrompt };

function wsURL(path: string): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}${path}`;
}

/** Absolute ws:// or wss:// URL for a VM's VNC proxy endpoint. */
export function vncWebSocketURL(id: string): string {
  return wsURL(`/api/v1/vms/${id}/vnc`);
}

/** Absolute ws:// URL for a VM's SSH terminal proxy endpoint. */
export function terminalWebSocketURL(id: string): string {
  return wsURL(`/api/v1/vms/${id}/terminal`);
}

/** Absolute ws:// URL for a job's live log stream. */
export function jobLogsWebSocketURL(id: string): string {
  return wsURL(`/api/v1/jobs/${id}/logs`);
}

/** Human-readable byte size. */
export function formatBytes(bytes: number): string {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}
