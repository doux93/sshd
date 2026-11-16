import { useState } from "react";
import { api } from "../api/client";
import { useAppStore } from "../store";
import { RecoveryList } from "./RecoveryList";
import { Icon } from "../components/Icon";
export function SessionShelf({ onClose }: { onClose: () => void }) {
  const {
    sessions,
    openIds,
    setActive,
    refresh,
    reconnectClosedSession,
    setError,
  } = useAppStore();
  const [ending, setEnding] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const target = sessions.find((s) => s.id === ending);
  async function end() {
    if (!ending) return;
    setBusy(true);
    try {
      await api.closeSession(ending);
      await refresh();
      setEnding(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section
      className="workspace-panel absolute inset-y-0 right-0 z-20 w-80 max-w-full shadow-2xl"
      aria-label="会话库"
    >
      <header className="panel-heading">
        <div>
          <strong>会话库</strong>
          <p className="mt-1 text-xs text-slate-400">
            关闭标签仅收起视图，SSH 会话继续运行。
          </p>
        </div>
        <button
          className="icon-button"
          onClick={onClose}
          aria-label="收起会话库"
        >
          <Icon name="close" />
        </button>
      </header>
      <div className="min-h-0 flex-1 overflow-auto p-3 space-y-2">
        {sessions.length === 0 ? (
          <p className="empty-copy">还没有会话，先连接一台主机。</p>
        ) : (
          sessions.map((s) => (
            <article
              key={s.id}
              className="rounded-xl border border-slate-700 bg-slate-900 p-3"
            >
              <div className="flex items-center gap-2">
                <span
                  className={`status-dot ${s.state === "active" ? "bg-emerald-400" : "bg-slate-500"}`}
                />
                <strong className="truncate text-sm">{s.label}</strong>
                <span className="ml-auto text-xs text-slate-400">
                  {s.state === "closed"
                    ? s.recoveryId
                      ? "连接已断开"
                      : "已结束"
                    : openIds.includes(s.id)
                      ? "已打开"
                      : "后台运行"}
                </span>
              </div>
              <p className="my-2 truncate font-mono text-xs text-slate-400">
                {s.target}
              </p>
              <div className="flex gap-2">
                <button
                  className="button-secondary text-xs"
                  onClick={() => {
                    setActive(s.id);
                    onClose();
                  }}
                >
                  打开视图
                </button>
                {s.state === "closed" && s.nodeId && (
                  <button
                    className="button-secondary text-xs"
                    onClick={() => reconnectClosedSession(s.id)}
                  >
                    重新连接
                  </button>
                )}
                <button
                  className="ml-auto text-xs text-rose-300"
                  onClick={() => setEnding(s.id)}
                >
                  {s.recoveryId && s.state === "active"
                    ? "断开并保留"
                    : s.state === "active"
                      ? "结束会话"
                      : "移除记录"}
                </button>
              </div>
            </article>
          ))
        )}
        <RecoveryList onOpen={onClose} />
      </div>
      {target && (
        <div className="dialog-backdrop">
          <div
            role="alertdialog"
            aria-modal="true"
            aria-labelledby="end-title"
            className="dialog-card max-w-md"
          >
            <h2 id="end-title" className="text-lg font-semibold">
              {target.state === "active"
                ? target.recoveryId
                  ? "断开并保留远端终端？"
                  : "结束 SSH 会话？"
                : "移除已结束的会话？"}
            </h2>
            <p className="mt-3 text-sm leading-6 text-slate-300">
              {target.label} · {target.target}
            </p>
            <p className="mt-2 text-sm text-slate-400">
              {target.state === "active"
                ? target.recoveryId
                  ? "断开工作台到主机的连接，远端 tmux 任务继续运行。稍后可从会话库恢复；工作台监控在断开期间暂停。"
                  : "所有设备与此会话的连接将结束，依赖该终端的远端任务可能中断。只想收起窗口时，请关闭标签。"
                : "该会话的终端输出将从工作台移除。保存的主机配置仍然保留。"}
            </p>
            <div className="mt-5 flex justify-end gap-2">
              <button
                className="button-secondary"
                disabled={busy}
                onClick={() => setEnding(null)}
              >
                取消
              </button>
              <button className="button-danger" disabled={busy} onClick={end}>
                {busy
                  ? "处理中…"
                  : target.state === "active"
                    ? target.recoveryId
                      ? "断开并保留"
                      : "结束会话"
                    : "移除记录"}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
