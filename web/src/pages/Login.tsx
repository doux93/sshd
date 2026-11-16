// Login + Setup page (remote mode).
//
// Two paths converge here:
//
//   1. First run: the server has no auth_config row. The user POSTs
//      POST /api/auth/setup with their chosen master password, then
//      POST /api/auth/login with the same password to derive the
//      vault wrap key. After that the SPA loads.
//
//   2. Returning user: just POST /api/auth/login.
//
// Login completes independently of notification permissions; opt in from Alerts.

import { useEffect, useState } from "react";
import { ThemeToggle } from "../components/ThemeToggle";

interface AuthStatus {
  mode: "local" | "remote";
  configured: boolean;
}

function readNext(): string {
  const u = new URL(window.location.href);
  try {
    const next = new URL(
      u.searchParams.get("next") ?? "/",
      window.location.origin,
    );
    return next.origin === window.location.origin
      ? next.pathname + next.search
      : "/";
  } catch {
    return "/";
  }
}

export function LoginPage() {
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch("/api/auth/status", { credentials: "same-origin" })
      .then((r) =>
        r.ok ? r.json() : Promise.reject(new Error(`status ${r.status}`)),
      )
      .then((s: AuthStatus) => setStatus(s))
      .catch((e) => setError(String(e)));
  }, []);

  async function submitLogin() {
    setBusy(true);
    setError(null);
    try {
      const res = await fetch("/api/auth/login", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password }),
      });
      if (res.status === 204) {
        window.location.assign(readNext());
        return;
      }
      const body = await res.json().catch(() => ({}));
      setError(humanizeError(res.status, body.error));
    } catch (e) {
      setError(e instanceof Error ? e.message : "网络请求失败，请重试");
    } finally {
      setBusy(false);
    }
  }

  async function submitSetup() {
    setBusy(true);
    setError(null);
    try {
      if (password !== confirm) {
        setError(
          "\u4e24\u6b21\u8f93\u5165\u7684\u5bc6\u7801\u4e0d\u4e00\u81f4",
        );
        return;
      }
      const setupRes = await fetch("/api/auth/setup", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password }),
      });
      if (!setupRes.ok) {
        const body = await setupRes.json().catch(() => ({}));
        setError(humanizeError(setupRes.status, body.error));
        return;
      }
      // Setup seals the verifier; the wrap key still needs the login
      // step to derive. Run that immediately so the SPA can read the
      // vault from the very first request.
      await submitLogin();
    } catch (e) {
      setError(e instanceof Error ? e.message : "网络请求失败，请重试");
    } finally {
      setBusy(false);
    }
  }

  const setupMode = status?.configured === false;
  const submit = setupMode ? submitSetup : submitLogin;

  return (
    <div className="flex h-screen items-center justify-center bg-slate-900 text-slate-50">
      <div className="absolute right-4 top-4">
        <ThemeToggle />
      </div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
        className="w-full max-w-sm rounded-lg border border-slate-700 bg-slate-800 p-6 shadow-xl"
      >
        <h1 className="mb-1 text-xl font-semibold tracking-wide">sshx</h1>
        <p className="mb-6 text-sm text-slate-400">
          {setupMode
            ? "\u9996\u6b21\u542f\u52a8\uff1a\u8bbe\u7f6e\u4e3b\u5bc6\u7801"
            : "\u8bf7\u8f93\u5165\u4e3b\u5bc6\u7801\u767b\u5f55"}
        </p>

        <label
          htmlFor="login-password"
          className="mb-2 block text-xs uppercase tracking-wide text-slate-400"
        >
          {"\u4e3b\u5bc6\u7801"}
        </label>
        <input
          id="login-password"
          type="password"
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          disabled={busy}
          className="mb-4 w-full rounded-md border border-slate-600 bg-slate-900 px-3 py-2 text-base text-slate-50 outline-none focus:border-blue-500"
        />

        {setupMode && (
          <>
            <label className="mb-2 block text-xs uppercase tracking-wide text-slate-400">
              {"\u518d\u6b21\u8f93\u5165"}
            </label>
            <input
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              disabled={busy}
              className="mb-4 w-full rounded-md border border-slate-600 bg-slate-900 px-3 py-2 text-base text-slate-50 outline-none focus:border-blue-500"
            />
            <p className="mb-4 text-xs text-amber-300">
              {
                "\u8b66\u544a\uff1a\u5fd8\u8bb0\u4e3b\u5bc6\u7801\u5c06\u5bfc\u81f4\u6240\u6709\u5df2\u4fdd\u5b58\u7684\u8fde\u63a5\u5bc6\u7801\u4e0e SSH \u79c1\u94a5\u4e0d\u53ef\u6062\u590d\u3002\u8bf7\u59a5\u5584\u5907\u4efd\u3002"
              }
            </p>
          </>
        )}

        {error && (
          <div className="mb-4 rounded-md border border-red-800 bg-red-950 px-3 py-2 text-sm text-red-200">
            {error}
          </div>
        )}

        <button
          type="submit"
          disabled={busy || password.length === 0}
          className="w-full rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {busy
            ? "\u5904\u7406\u4e2d\u2026"
            : setupMode
              ? "\u8bbe\u7f6e\u5bc6\u7801\u5e76\u767b\u5f55"
              : "\u767b\u5f55"}
        </button>
      </form>
    </div>
  );
}

function humanizeError(status: number, raw?: string): string {
  if (raw === "invalid_password") return "\u5bc6\u7801\u9519\u8bef";
  if (raw === "setup_required")
    return "\u670d\u52a1\u5668\u5c1a\u672a\u5b8c\u6210\u9996\u6b21\u8bbe\u7f6e";
  if (raw === "login_required") return "\u9700\u8981\u767b\u5f55";
  return raw ?? `${status} \u8bf7\u6c42\u5931\u8d25`;
}
