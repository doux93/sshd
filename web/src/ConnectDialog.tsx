import { Modal } from "./components/Modal";
// Quick connect: ad-hoc session without saving a node. Goes through the
// same TOFU host-key flow as saved connections.
import { useState, type FormEvent } from "react";
import { api, ApiError, connectionError } from "./api/client";
import { useAppStore } from "./store";

export function ConnectDialog() {
  const {
    quickConnectOpen,
    setQuickConnectOpen,
    refresh,
    setActive,
    setHostKeyPrompt,
  } = useAppStore();
  const [host, setHost] = useState("");
  const [port, setPort] = useState("22");
  const [user, setUser] = useState("root");
  const [password, setPassword] = useState("");
  const [monitor, setMonitor] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  if (!quickConnectOpen) return null;

  const attempt = async (accepts: Record<string, string>): Promise<void> => {
    try {
      const info = await api.createSession({
        host: host.trim(),
        port: parseInt(port, 10) || 22,
        user: user.trim(),
        password,
        accepts,
        // Quick-connect has no saved node, so the server-side monitor
        // would otherwise never start for this session. Pass the user's
        // preference through so /api/metrics and the MonitorBar work.
        monitor: monitor ? { enabled: true, interval: 5 } : undefined,
      });
      await refresh();
      setActive(info.id);
      setHostKeyPrompt(null);
      setQuickConnectOpen(false);
      setPassword("");
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && err.hostKey) {
        setHostKeyPrompt({ hostKey: err.hostKey, accepts, retry: attempt });
        return;
      }
      setHostKeyPrompt(null);
      setError(connectionError(err));
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    await attempt({});
    setBusy(false);
  };

  const field = "form-field";
  const close = () => {
    setPassword("");
    setError("");
    setHostKeyPrompt(null);
    setQuickConnectOpen(false);
  };

  return (
    <Modal label="快速连接" className="max-w-md" onClose={close} busy={busy}>
      <form onSubmit={submit}>
        <h2 className="mb-4 text-lg font-semibold text-slate-50">快速连接</h2>
        <div className="space-y-3">
          <div className="flex gap-2">
            <input
              className={field}
              aria-label="主机地址"
              placeholder="主机地址"
              value={host}
              onChange={(e) => setHost(e.target.value)}
              autoFocus
              required
            />
            <input
              className={`${field} !w-24 shrink-0`}
              aria-label="端口"
              type="number"
              min={1}
              max={65535}
              required
              placeholder="端口"
              value={port}
              onChange={(e) => setPort(e.target.value)}
            />
          </div>
          <input
            className={field}
            aria-label="用户名"
            placeholder="用户名"
            value={user}
            onChange={(e) => setUser(e.target.value)}
            required
          />
          <input
            className={field}
            type="password"
            aria-label="密码"
            autoComplete="current-password"
            placeholder="密码"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input
              type="checkbox"
              checked={monitor}
              onChange={(e) => setMonitor(e.target.checked)}
              className="h-4 w-4 accent-blue-600"
            />
            启用免 agent 监控（CPU/内存/负载/磁盘）
          </label>
        </div>
        {error && <p className="mt-3 text-sm text-red-400">{error}</p>}
        <div className="mt-5 flex justify-end gap-2">
          <button
            type="button"
            onClick={close}
            disabled={busy}
            className="button-secondary"
          >
            取消
          </button>
          <button type="submit" disabled={busy} className="button-primary">
            {busy ? "连接中…" : "连接"}
          </button>
        </div>
      </form>
    </Modal>
  );
}
