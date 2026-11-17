import { Modal } from "../components/Modal";
import { useState } from "react";
import { api, connectionError, type RecoveryInfo } from "../api/client";
import { useAppStore } from "../store";

export function RecoveryList({ onOpen }: { onOpen(): void }) {
  const st = useAppStore();
  const [confirmation, setConfirmation] = useState<{
    row: RecoveryInfo;
    action: "terminate" | "forget";
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setLocalError] = useState("");
  async function confirm() {
    if (!confirmation) return;
    const { row, action } = confirmation;
    setBusy(true);
    setLocalError("");
    try {
      if (action === "forget") {
        await api.forgetRecovery(row.id);
        await st.refresh();
      } else await st.recoverTerminal(row.id, "terminate");
      setConfirmation(null);
    } catch (error) {
      setLocalError(connectionError(error));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="远端保活终端" className="space-y-2">
      <h3 className="text-sm font-semibold">远端保活终端</h3>
      <p className="my-2 text-xs leading-5 text-slate-400">
        工作台重启后可重新接入。远端需安装 tmux，主机重启或进程退出后不能恢复。
      </p>
      {!st.recoveries.length && (
        <p className="text-xs text-slate-500">
          在主机高级设置中开启“终端保活与恢复”。
        </p>
      )}
      {st.recoveries.map((row) => (
        <article
          key={row.id}
          className="my-2 rounded-lg border border-slate-700 p-3"
          aria-label={`保活终端 ${row.label}`}
        >
          <strong className="block truncate text-sm">{row.label}</strong>
          <p className="my-1 break-all font-mono text-xs text-slate-400">
            {row.target}
          </p>
          <p className="text-xs text-sky-300">
            {row.liveSessionId ? "已接入工作台" : "待接入 · 远端状态尚未确认"}
          </p>
          <div className="mt-3 flex flex-wrap gap-2">
            <button
              className="button-secondary text-xs"
              disabled={st.connecting || busy}
              onClick={async () => {
                if (row.liveSessionId) {
                  st.setActive(row.liveSessionId);
                  onOpen();
                } else await st.recoverTerminal(row.id, "resume");
              }}
            >
              {row.liveSessionId ? "打开保活终端" : "恢复终端"}
            </button>
            <button
              className="text-xs text-rose-300"
              disabled={st.connecting || busy}
              onClick={() => setConfirmation({ row, action: "terminate" })}
            >
              结束远端终端
            </button>
            {!row.liveSessionId && (
              <button
                className="text-xs text-slate-400"
                disabled={st.connecting || busy}
                onClick={() => setConfirmation({ row, action: "forget" })}
              >
                忘记恢复记录
              </button>
            )}
          </div>
          <details className="mt-3 text-xs text-slate-500">
            <summary className="cursor-pointer">手动接入命令</summary>
            <code className="mt-2 block select-text break-all">
              tmux -L sshx-{row.id} attach-session -t '=terminal'
            </code>
          </details>
        </article>
      ))}
      {confirmation && (
        <Modal
          onClose={() => {
            setLocalError("");
            setConfirmation(null);
          }}
          busy={busy}
          kind="alertdialog"
          label={
            confirmation.action === "terminate"
              ? "结束远端终端"
              : "忘记恢复记录"
          }
          className="max-w-md"
        >
          <h2 className="text-lg font-semibold">
            {confirmation.action === "terminate"
              ? "结束远端终端？"
              : "忘记恢复记录？"}
          </h2>
          <p className="my-3 break-all text-sm">
            {confirmation.row.label} · {confirmation.row.target}
          </p>
          <p className="text-sm leading-6 text-slate-400">
            {confirmation.action === "terminate"
              ? "这会结束该 tmux 会话的全部窗口，正在运行的任务可能中断。只想暂时离开，请使用“断开并保留”。"
              : "仅删除工作台中的恢复入口，不停止远端任务。需要时请先保存上面的手动接入命令；下次连接将创建新终端。"}
          </p>
          {error && (
            <p role="alert" className="mt-3 text-sm text-rose-300">
              {error}
            </p>
          )}
          <div className="mt-5 flex justify-end gap-2">
            <button
              className="button-secondary"
              disabled={busy}
              onClick={() => setConfirmation(null)}
            >
              取消
            </button>
            <button className="button-danger" disabled={busy} onClick={confirm}>
              {busy
                ? "处理中…"
                : confirmation.action === "terminate"
                  ? "确认结束远端终端"
                  : "确认忘记恢复记录"}
            </button>
          </div>
        </Modal>
      )}
    </section>
  );
}
