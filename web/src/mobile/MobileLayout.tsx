import { useEffect, useState } from "react";
import { useAppStore } from "../store";
import { api, getMode, type MonitorSample } from "../api/client";
import { wsClient, type ConnectionStatus } from "../ws/protocol";
import { TerminalView } from "../terminal/TerminalView";
import { AlertsPanel } from "../workspace/AlertsPanel";
import { ThemeToggle } from "../components/ThemeToggle";
import { Icon } from "../components/Icon";
export function MobileLayout({ status }: { status: ConnectionStatus }) {
  const st = useAppStore();
  const [tab, setTab] = useState<"sessions" | "monitor" | "alerts">(() =>
    new URL(location.href).searchParams.get("panel") === "alerts"
      ? "alerts"
      : "sessions",
  );
  const [watch, setWatch] = useState<string | null>(null),
    [selectedNode, setSelectedNode] = useState<string | null>(() =>
      new URL(location.href).searchParams.get("node"),
    );
  const [samples, setSamples] = useState<Record<string, MonitorSample>>({});
  useEffect(
    () =>
      wsClient.onMetrics((id, s) =>
        setSamples((p) => ({ ...p, [id]: s as MonitorSample })),
      ),
    [],
  );
  useEffect(() => {
    let alive = true;
    for (const s of st.sessions) {
      if (s.monitored && s.nodeId)
        api
          .getMetrics(s.nodeId, 1)
          .then((r) => {
            if (alive && r.samples.length)
              setSamples((p) => ({ ...p, [s.id]: r.samples.at(-1)! }));
          })
          .catch(() => {});
    }
    return () => {
      alive = false;
    };
  }, [st.sessions]);
  async function logout() {
    try {
      await api.logout();
      wsClient.disconnect();
      location.assign("/login");
    } catch (e) {
      st.setError(String(e));
    }
  }
  const focused = st.nodes.find((n) => n.id === selectedNode),
    related = st.sessions.filter((s) => s.nodeId === selectedNode),
    watching = st.sessions.find((s) => s.id === watch);
  return (
    <div className="flex h-dvh flex-col overflow-hidden bg-[var(--surface-base)] text-slate-100">
      <header className="flex shrink-0 items-center gap-2 border-b border-slate-800 px-4 py-3">
        <Icon name="terminal" />
        <strong>sshx</strong>
        <span
          className={`status-dot ml-2 ${status === "connected" ? "bg-emerald-400" : "bg-amber-400"}`}
        />
        <span className="text-xs text-slate-400">
          {status === "connected" ? "只读工作台" : "正在重新连接…"}
        </span>
        <div className="ml-auto">
          <ThemeToggle />
        </div>
        {getMode() === "remote" && (
          <button
            className="icon-button"
            onClick={logout}
            aria-label="退出登录"
          >
            <Icon name="logout" size={17} />
          </button>
        )}
      </header>
      <main className="min-h-0 flex-1 overflow-y-auto">
        {tab === "alerts" ? (
          <AlertsPanel
            nodeId={selectedNode}
            onClearFilter={() => setSelectedNode(null)}
            onNode={(id) => {
              setSelectedNode(id);
              setTab("sessions");
            }}
          />
        ) : (
          <>
            {selectedNode && (
              <section className="m-3 rounded-xl border border-sky-800 bg-sky-950/20 p-4">
                <div className="flex justify-between">
                  <strong className="text-sm">
                    {focused?.name ??
                      st.alerts.find((a) => a.nodeId === selectedNode)
                        ?.nodeLabel ??
                      "关联主机"}
                  </strong>
                  <button
                    onClick={() => setSelectedNode(null)}
                    aria-label="清除主机筛选"
                  >
                    <Icon name="close" size={15} />
                  </button>
                </div>
                <p className="mt-2 text-xs text-slate-400">
                  {focused
                    ? `${focused.config.user ?? ""}@${focused.config.host ?? ""}`
                    : "保存的主机可能已删除。"}
                </p>
                <p className="mt-2 text-xs text-slate-400">
                  {related.some((s) => s.state === "active")
                    ? "选择下方会话查看输出。"
                    : "没有正在运行的会话，请在桌面端连接后查看。"}
                </p>
              </section>
            )}
            {tab === "sessions" ? (
              <div className="p-3 space-y-3">
                {(selectedNode ? related : st.sessions).length === 0 ? (
                  <p className="empty-copy">
                    在桌面端连接主机后，可以在这里查看输出。关闭浏览器不会结束
                    SSH 会话。
                  </p>
                ) : (
                  (selectedNode ? related : st.sessions).map((s) => (
                    <button
                      key={s.id}
                      className="w-full rounded-xl border border-slate-800 bg-slate-900/70 p-4 text-left"
                      onClick={() => setWatch(s.id)}
                    >
                      <div className="flex gap-2 items-center">
                        <span
                          className={`status-dot ${s.state === "active" ? "bg-emerald-400" : "bg-slate-500"}`}
                        />
                        <strong className="truncate text-sm">{s.label}</strong>
                        <span className="ml-auto shrink-0 text-xs text-slate-500">
                          {s.state === "active" ? "查看输出" : "已结束"}
                        </span>
                      </div>
                      <p className="mt-2 truncate font-mono text-xs text-slate-500">
                        {s.target}
                      </p>
                    </button>
                  ))
                )}
              </div>
            ) : (
              <div className="p-3 space-y-3">
                {st.sessions
                  .filter(
                    (s) =>
                      s.monitored &&
                      (!selectedNode || s.nodeId === selectedNode),
                  )
                  .map((s) => {
                    const m = samples[s.id],
                      stale = m && Date.now() - Date.parse(m.ts) > 30000;
                    return (
                      <button
                        key={s.id}
                        className="w-full rounded-xl border border-slate-800 bg-slate-900 p-4 text-left"
                        onClick={() => setWatch(s.id)}
                      >
                        <div className="flex justify-between text-sm">
                          <strong>{s.label}</strong>
                          <span className="text-xs text-sky-300">查看会话</span>
                        </div>
                        {m ? (
                          <>
                            <div className="mt-4 grid grid-cols-3 gap-3">
                              {[
                                ["CPU", m.cpuUsage.toFixed(1) + "%"],
                                ["内存", m.memUsage.toFixed(1) + "%"],
                                ["负载", m.load1.toFixed(2)],
                              ].map(([label, value]) => (
                                <div key={label}>
                                  <span className="block text-xs text-slate-500">
                                    {label}
                                  </span>
                                  <span className="mt-1 block text-base font-mono">
                                    {value}
                                  </span>
                                </div>
                              ))}
                            </div>
                            <p
                              className={`mt-3 text-[11px] ${stale ? "text-amber-300" : "text-slate-500"}`}
                            >
                              {stale ? "数据可能已过期 · " : ""}
                              {new Date(m.ts).toLocaleTimeString()}
                            </p>
                          </>
                        ) : (
                          <p className="mt-3 text-sm text-slate-500">
                            等待监控数据…
                          </p>
                        )}
                      </button>
                    );
                  })}
                {!st.sessions.some((s) => s.monitored) && (
                  <p className="empty-copy">
                    在桌面端编辑主机、启用监控并连接后，这里会显示数据。
                  </p>
                )}
              </div>
            )}
          </>
        )}
      </main>
      <nav className="grid shrink-0 grid-cols-3 border-t border-slate-800 pb-[env(safe-area-inset-bottom)]">
        {(
          [
            ["sessions", "会话", "terminal"],
            ["monitor", "监控", "monitor"],
            ["alerts", "告警", "bell"],
          ] as const
        ).map(([value, label, icon]) => (
          <button
            key={value}
            className={`flex flex-col items-center gap-1 py-3 text-xs ${tab === value ? "text-sky-300" : "text-slate-500"}`}
            aria-current={tab === value ? "page" : undefined}
            onClick={() => setTab(value)}
          >
            <Icon name={icon} size={18} />
            {label}
            {value === "alerts" && st.alerts.some((a) => !a.acknowledgedAt) && (
              <span className="sr-only">有未确认告警</span>
            )}
          </button>
        ))}
      </nav>
      {watch && (
        <div className="fixed inset-0 z-40 flex flex-col bg-[var(--terminal-bg)]">
          <header className="flex shrink-0 items-center gap-2 border-b border-slate-800 bg-slate-900 px-3 py-3">
            <button
              className="button-secondary text-xs"
              onClick={() => setWatch(null)}
            >
              返回
            </button>
            <span className="min-w-0 flex-1 truncate text-xs">
              {watching?.label ?? "会话输出"}
            </span>
            <span className="rounded bg-sky-950 px-2 py-1 text-[11px] text-sky-300">
              只读 · 不发送输入
            </span>
            <ThemeToggle />
          </header>
          <div className="min-h-0 flex-1">
            <TerminalView
              key={watch}
              sessionId={watch}
              sizeMode="passive"
              readOnly
            />
          </div>
        </div>
      )}
      {st.error && (
        <div
          role="alert"
          className="fixed bottom-20 left-3 right-3 z-50 rounded-lg bg-rose-950 p-4 text-sm text-rose-200"
          onClick={() => st.setError(null)}
        >
          {st.error} · 点此关闭
        </div>
      )}
    </div>
  );
}
