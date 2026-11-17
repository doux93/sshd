import { Modal } from "./components/Modal";
// TOFU host-key confirmation. A changed key gets the loud red treatment —
// this is the dialog FinalShell never showed and OpenSSH always did.
import { useState } from "react";
import { useAppStore } from "./store";

export function HostKeyDialog() {
  const { hostKeyPrompt, setHostKeyPrompt } = useAppStore();
  const [busy, setBusy] = useState(false);

  if (!hostKeyPrompt) return null;
  const { hostKey, accepts, retry } = hostKeyPrompt;

  const confirm = async () => {
    setBusy(true);
    try {
      await retry({ ...accepts, [hostKey.host]: hostKey.fingerprint });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      labelledBy="host-key-title"
      onClose={() => setHostKeyPrompt(null)}
      busy={busy}
      className={`max-w-md ${
        hostKey.changed ? "modal-danger" : "border-slate-600 bg-slate-900"
      }`}
    >
      <h2
        id="host-key-title"
        className={`mb-3 text-lg font-semibold ${
          hostKey.changed ? "text-red-300" : "text-slate-50"
        }`}
      >
        {hostKey.changed ? "⚠ 主机密钥已改变!" : "未知主机密钥"}
      </h2>
      <p className="text-sm leading-relaxed text-slate-200">
        {hostKey.changed ? (
          <>
            <span className="font-semibold text-red-300">{hostKey.host}</span>{" "}
            返回的密钥与之前记录的不一致。这可能意味着中间人攻击,也可能是服务器
            重装了系统。请务必核实后再继续。
          </>
        ) : (
          <>
            首次连接{" "}
            <span className="font-semibold text-slate-50">{hostKey.host}</span>
            ,无法验证其身份。请核对下面的密钥指纹:
          </>
        )}
      </p>
      <div className="mt-3 rounded-md border border-slate-700 bg-[var(--surface-base)] p-3 font-mono text-xs text-slate-200">
        <div>{hostKey.keyType}</div>
        <div className="mt-1 break-all">{hostKey.fingerprint}</div>
      </div>
      <div className="mt-5 flex justify-end gap-2">
        <button
          disabled={busy}
          autoFocus
          data-autofocus
          onClick={() => setHostKeyPrompt(null)}
          className="rounded-md px-4 py-2 text-sm text-slate-300 hover:text-slate-50"
        >
          取消连接
        </button>
        <button
          onClick={confirm}
          disabled={busy}
          className={`rounded-md px-4 py-2 text-sm font-medium text-white disabled:opacity-50 ${
            hostKey.changed
              ? "bg-red-600 hover:bg-red-500"
              : "bg-blue-600 hover:bg-blue-500"
          }`}
        >
          {hostKey.changed
            ? "我已核实,信任新密钥"
            : (hostKeyPrompt.actionLabel ?? "信任并连接")}
        </button>
      </div>
    </Modal>
  );
}
