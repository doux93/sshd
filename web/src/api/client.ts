// REST client. Runtime auth mode is fetched once before mounting the app.

const TOKEN_KEY = "sshx.token";

let mode: "local" | "remote" | null = null;
export function getMode(): "local" | "remote" | null {
  return mode;
}
function isRemote(): boolean {
  return mode === "remote";
}

/** Fetch runtime configuration before opening sockets or making protected calls.
 * Works for static HTML, service-worker navigation, and the Vite dev proxy. */
export async function initAuth(): Promise<void> {
  const res = await fetch("/api/auth/status", {
    cache: "no-store",
    credentials: "same-origin",
  });
  if (!res.ok) throw new Error("无法读取服务状态，请检查连接后重试");
  const status = await res.json();
  if (status.mode !== "local" && status.mode !== "remote")
    throw new Error("服务返回了无效的运行模式");
  mode = status.mode;
  if (mode === "remote") sessionStorage.removeItem(TOKEN_KEY);
  initToken();
  if (
    mode === "remote" &&
    window.location.pathname !== "/login" &&
    (!status.authenticated || status.locked)
  ) {
    window.location.replace(
      `/login?next=${encodeURIComponent(window.location.pathname + window.location.search)}`,
    );
    throw new Error("正在转到登录页面…");
  }
}

export function initToken(): void {
  if (isRemote()) {
    // Remote mode uses an HttpOnly session cookie.
    return;
  }
  const url = new URL(window.location.href);
  const token = url.searchParams.get("token");
  if (token) {
    sessionStorage.setItem(TOKEN_KEY, token);
    url.searchParams.delete("token");
    window.history.replaceState({}, "", url.toString());
  }
}

export function getToken(): string {
  if (isRemote()) {
    return "";
  }
  return sessionStorage.getItem(TOKEN_KEY) ?? "";
}

export interface SessionInfo {
  id: string;
  label: string;
  kind: string;
  target: string;
  state: "active" | "closed";
  /** Live client attachments; 0 = running detached on the server. */
  attached: number;
  /** Last directory reported by the remote shell via OSC 7. */
  cwd: string;
  /** Whether this connection type supports SFTP. */
  hasFiles: boolean;
  createdAt: string;
  /** Node this session came from; empty for quick-connect sessions. */
  nodeId?: string;
  /**
   * Whether a metrics collector is actually running. False means monitoring
   * was never enabled for this connection — distinct from "enabled but no
   * sample has arrived yet", so the UI can say which.
   */
  monitored: boolean;
}

export interface FileEntry {
  name: string;
  size: number;
  mode: string;
  modTime: number;
  isDir: boolean;
  isLink: boolean;
}

export interface MonitorConfig {
  enabled: boolean;
  interval?: number; // seconds, 0 = default 5
}

export interface AlertRule {
  metric: "cpuUsage" | "memUsage" | "load1" | "disk";
  op: ">" | ">=" | "<" | "<=";
  threshold: number;
  duration?: string; // e.g. "5m"
}

export interface ConnConfig {
  host?: string;
  port?: number;
  user?: string;
  authType?: "password" | "key" | "agent";
  keyPath?: string;
  jumpNodeId?: string;
  hasPassword?: boolean;
  hasPassphrase?: boolean;
  aiPolicy?: "readonly" | "confirm" | "trusted";
  proxy?: string;
  monitor?: MonitorConfig;
  alerts?: AlertRule[];
}

export interface MonitorSample {
  ts: string;
  cpuUsage: number;
  memTotalKB: number;
  memUsedKB: number;
  memUsage: number;
  load1: number;
  load5: number;
  load15: number;
  disks: { mount: string; totalKB: number; usedKB: number; usagePct: number }[];
  net: { name: string; rxBytes: number; txBytes: number }[];
}

export interface NodeInfo {
  id: string;
  parentId: string;
  kind: "folder" | "connection";
  name: string;
  sortOrder: number;
  config: ConnConfig;
  createdAt: string;
  updatedAt: string;
}

export interface NodeRequest {
  parentId: string;
  kind: "folder" | "connection";
  name: string;
  sortOrder?: number;
  config: ConnConfig;
  password?: string | null; // undefined = unchanged, "" = clear
  passphrase?: string | null;
}

export interface HostKeyInfo {
  host: string;
  keyType: string;
  fingerprint: string;
  changed: boolean;
}

export interface CreateSessionRequest {
  host: string;
  port: number;
  user: string;
  password: string;
  label?: string;
  cols?: number;
  rows?: number;
  accepts?: Record<string, string>;
  /** Opt-in monitoring for quick-connect. No effect when undefined. */
  monitor?: { enabled: boolean; interval?: number };
}

/** API error carrying status and the parsed body (e.g. hostKey on 409). */
export class ApiError extends Error {
  status: number;
  hostKey?: HostKeyInfo;
  hint?: string;
  constructor(status: number, message: string, hostKey?: HostKeyInfo) {
    super(message);
    this.status = status;
    this.hostKey = hostKey;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  // In remote mode, omit the X-Auth-Token header entirely — cookies
  // carry the session. In local mode the token is the source of truth.
  const tok = getToken();
  if (tok) headers["X-Auth-Token"] = tok;
  Object.assign(headers, init?.headers as Record<string, string> | undefined);

  const res = await fetch(path, { ...init, headers });
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    let hostKey: HostKeyInfo | undefined;
    let hint: string | undefined;
    try {
      const body = await res.json();
      if (body.error) message = body.error;
      if (body.hostKey) hostKey = body.hostKey;
      if (typeof body.hint === "string") hint = body.hint;
    } catch {
      /* non-JSON error body */
    }
    // 401 + login_required → send the user to /login. We only do this
    // in remote mode (local mode never emits login_required) and we
    // skip the auth endpoints themselves so a failed login doesn't
    // immediately re-redirect.
    if (
      res.status === 401 &&
      isRemote() &&
      !path.startsWith("/api/auth/") &&
      message === "login_required"
    ) {
      // Preserve the path the user was trying to reach so /login can
      // bounce them back after a successful authentication.
      const next = encodeURIComponent(
        window.location.pathname + window.location.search,
      );
      window.location.assign(`/login?next=${next}`);
    }
    const error = new ApiError(res.status, message, hostKey);
    error.hint = hint;
    throw error;
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

export const api = {
  // sessions
  listSessions: () => request<SessionInfo[]>("/api/sessions"),
  createSession: (req: CreateSessionRequest) =>
    request<SessionInfo>("/api/sessions", {
      method: "POST",
      body: JSON.stringify(req),
    }),
  closeSession: (id: string) =>
    request<void>(`/api/sessions/${id}`, { method: "DELETE" }),

  // connection tree
  listNodes: () => request<NodeInfo[]>("/api/nodes"),
  createNode: (req: NodeRequest) =>
    request<NodeInfo>("/api/nodes", {
      method: "POST",
      body: JSON.stringify(req),
    }),
  updateNode: (id: string, req: NodeRequest) =>
    request<NodeInfo>(`/api/nodes/${id}`, {
      method: "PUT",
      body: JSON.stringify(req),
    }),
  deleteNode: (id: string) =>
    request<void>(`/api/nodes/${id}`, { method: "DELETE" }),
  connectNode: (
    id: string,
    opts: { cols?: number; rows?: number; accepts?: Record<string, string> },
  ) =>
    request<SessionInfo>(`/api/nodes/${id}/connect`, {
      method: "POST",
      body: JSON.stringify(opts),
    }),

  testConnection: (
    req: NodeRequest & { nodeId?: string; accepts?: Record<string, string> },
  ) =>
    request<{ ok: boolean; target: string; elapsedMs: number }>(
      "/api/connections/test",
      { method: "POST", body: JSON.stringify(req) },
    ),
  listAlerts: (before = 0, nodeId = "") =>
    request<AlertRecord[]>(
      `/api/alerts?before=${before}&nodeId=${encodeURIComponent(nodeId)}`,
    ),
  acknowledgeAlert: (id: number) =>
    request<void>(`/api/alerts/${id}/ack`, { method: "POST" }),

  // Monitor
  getMetrics: (nodeId: string, limit?: number) =>
    request<{ samples: MonitorSample[]; count: number }>(
      `/api/metrics/${nodeId}${limit ? `?limit=${limit}` : ""}`,
    ),

  importSshConfig: () =>
    request<{ imported: number; warnings: string[] }>("/api/import/sshconfig", {
      method: "POST",
      body: "{}",
    }),

  /** Auth lifecycle (remote mode only). */
  authStatus: () =>
    request<{ mode: "local" | "remote"; configured: boolean }>(
      "/api/auth/status",
    ),
  logout: () => request<void>("/api/auth/logout", { method: "POST" }),

  // Web Push (remote mode only).
  pushPublicKey: () =>
    request<{ publicKey: string; subject: string }>("/api/push/public-key"),
  pushSubscribe: (sub: PushSubscribeBody) =>
    request<{ id: string }>("/api/push/subscribe", {
      method: "POST",
      body: JSON.stringify(sub),
    }),
  pushList: () => request<PushDevice[]>("/api/push/subscriptions"),
  pushRevoke: (id: string) =>
    request<void>(`/api/push/subscriptions/${id}`, { method: "DELETE" }),

  // AI (Ctrl+K command generation + provider config).
  aiConfig: () => request<AIConfig>("/api/ai/config"),
  setAIConfig: (req: AISetConfig) =>
    request<{ ok: boolean }>("/api/ai/config", {
      method: "POST",
      body: JSON.stringify(req),
    }),
  ctrlk: (
    prompt: string,
    nodeId: string,
    sessionId: string,
    includeContext: boolean,
  ) =>
    request<CommandSuggestion>("/api/ai/ctrlk", {
      method: "POST",
      body: JSON.stringify({ prompt, nodeId, sessionId, includeContext }),
    }),

  // SFTP
  listFiles: (sessionId: string, path: string) =>
    request<{ path: string; entries: FileEntry[] }>(
      `/api/files/list?session=${sessionId}&path=${encodeURIComponent(path)}`,
    ),
  mkdir: (sessionId: string, path: string) =>
    request<void>(`/api/files/mkdir?session=${sessionId}`, {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  deleteFile: (sessionId: string, path: string, recursive: boolean) =>
    request<void>(`/api/files/delete?session=${sessionId}`, {
      method: "POST",
      body: JSON.stringify({ path, recursive }),
    }),
  renameFile: (sessionId: string, path: string, newPath: string) =>
    request<void>(`/api/files/rename?session=${sessionId}`, {
      method: "POST",
      body: JSON.stringify({ path, newPath }),
    }),

  /** Browser-native download; the token rides in the query string in
   *  local mode, the cookie in remote mode (no query param needed). */
  downloadUrl: (sessionId: string, path: string) => {
    const tok = getToken();
    const tail = tok ? `&token=${encodeURIComponent(tok)}` : "";
    return (
      `/api/files/download?session=${sessionId}&path=${encodeURIComponent(path)}` +
      tail
    );
  },

  /**
   * Streams a File to the remote path with progress. Uses XHR because
   * fetch() cannot report upload progress.
   */
  uploadFile: (
    sessionId: string,
    remotePath: string,
    file: File,
    onProgress?: (loaded: number, total: number) => void,
    signal?: AbortSignal,
  ) =>
    new Promise<void>((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      const abort = () => xhr.abort();
      const finish = (error?: Error) => {
        signal?.removeEventListener("abort", abort);
        if (error) reject(error);
        else resolve();
      };
      if (signal?.aborted) {
        reject(new DOMException("上传已取消", "AbortError"));
        return;
      }
      xhr.open(
        "POST",
        `/api/files/upload?session=${encodeURIComponent(sessionId)}&path=${encodeURIComponent(remotePath)}`,
      );
      xhr.setRequestHeader("X-Auth-Token", getToken());
      xhr.timeout = 30 * 60 * 1000;
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress?.(e.loaded, e.total);
      };
      xhr.onload = () =>
        finish(
          xhr.status >= 200 && xhr.status < 300
            ? undefined
            : new ApiError(xhr.status, parseXhrError(xhr)),
        );
      xhr.onerror = () =>
        finish(
          new Error("上传失败：网络连接中断，请检查工作台与主机连接后重试。"),
        );
      xhr.ontimeout = () => finish(new Error("上传超时，请检查网络后重试。"));
      xhr.onabort = () => finish(new DOMException("上传已取消", "AbortError"));
      signal?.addEventListener("abort", abort, { once: true });
      xhr.send(file);
    }),
};

function parseXhrError(xhr: XMLHttpRequest): string {
  try {
    const body = JSON.parse(xhr.responseText);
    if (body.error) return body.error;
  } catch {
    /* non-JSON body */
  }
  return `${xhr.status} ${xhr.statusText}`;
}

// Web Push payload types ----------------------------------------------------

export interface PushSubscribeBody {
  endpoint: string;
  p256dh: string;
  auth: string;
  userAgent?: string;
}

export interface PushDevice {
  id: string;
  endpoint: string;
  userAgent: string;
  createdAt: number;
  lastUsedAt: number;
  revoked: boolean;
}

// AI / Ctrl+K ---------------------------------------------------------------

export interface AIConfig {
  provider: string; // "" | "openai" | "anthropic"
  model: string;
  baseURL: string;
  hasKey: boolean;
}

export interface AISetConfig {
  provider: string;
  model: string;
  baseURL: string;
  apiKey?: string; // omit/empty to keep the stored key unchanged
}

export interface AlertRecord {
  id: number;
  nodeId: string;
  nodeLabel: string;
  level: string;
  message: string;
  ts: number;
  acknowledgedAt: number;
}
export interface CommandSuggestion {
  command: string;
  explanation: string;
  impact: string;
  target: string;
  sessionId: string;
}

export function connectionError(error: unknown): string {
  return error instanceof ApiError && error.hint
    ? `${error.hint}\n详细信息：${error.message}`
    : error instanceof Error
      ? error.message
      : String(error);
}
