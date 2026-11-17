import { Modal } from "../components/Modal";
import { useEffect, useRef, useState } from "react";
import type { CommandSuggestion } from "../api/client";
import { Icon } from "../components/Icon";
export function CtrlKBar({
  open,
  loading,
  error,
  onSubmit,
  onClose,
  suggestion,
  target,
}: {
  open: boolean;
  loading: boolean;
  error: string;
  onSubmit: (prompt: string, includeContext: boolean) => void;
  onClose: () => void;
  suggestion: CommandSuggestion | null;
  target: string;
}) {
  const [prompt, setPrompt] = useState(""),
    [copy, setCopy] = useState(""),
    [includeContext, setIncludeContext] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (open) {
      setPrompt("");
      setCopy("");
      requestAnimationFrame(() => input.current?.focus());
    }
  }, [open]);
  useEffect(() => setCopy(""), [suggestion]);
  if (!open) return null;
  return (
    <Modal label="AI 命令助手" className="max-w-2xl" onClose={onClose}>
      <div className="mb-3 flex items-center justify-between">
        <span className="flex items-center gap-2 text-sm font-semibold">
          <Icon name="sparkles" size={16} />
          命令助手
        </span>
        <button
          className="icon-button"
          onClick={onClose}
          aria-label="关闭 AI 助手"
        >
          <Icon name="close" size={16} />
        </button>
      </div>
      <p className="mb-3 break-all text-xs text-slate-400">
        目标主机{" "}
        <span className="font-mono text-sky-300">
          {suggestion?.target || target}
        </span>
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (prompt.trim() && !loading)
            onSubmit(prompt.trim(), includeContext);
        }}
      >
        <div className="flex gap-2">
          <input
            ref={input}
            className="form-field min-w-0 flex-1"
            aria-label="命令需求"
            placeholder="例如：找出当前目录最大的 10 个文件"
            value={prompt}
            disabled={loading}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") onClose();
            }}
          />
          <button
            className="button-primary shrink-0"
            disabled={loading || !prompt.trim()}
          >
            {loading ? "生成中…" : "生成"}
          </button>
        </div>
        <label className="mt-3 flex items-start gap-2 text-xs leading-5 text-slate-400">
          <input
            type="checkbox"
            className="mt-1"
            checked={includeContext}
            disabled={loading}
            onChange={(e) => setIncludeContext(e.target.checked)}
          />
          附带最近终端输出（最多 16 KiB，将发送给已配置的模型服务）
        </label>
      </form>
      {error && (
        <p
          role="alert"
          className="mt-3 rounded-lg bg-rose-950/50 p-3 text-sm text-rose-300"
        >
          {error}
        </p>
      )}
      {suggestion && (
        <div className="mt-4 space-y-3 border-t border-slate-700 pt-4">
          <textarea
            aria-label="生成的命令"
            className="form-field resize-y font-mono"
            readOnly
            rows={3}
            value={suggestion.command}
          />
          <div>
            <h3 className="text-xs text-slate-500">命令说明</h3>
            <p className="mt-1 whitespace-pre-wrap text-sm leading-6">
              {suggestion.explanation}
            </p>
          </div>
          <div className="rounded-lg border border-amber-800/50 bg-amber-950/20 p-3">
            <h3 className="text-xs text-amber-300">影响与前提</h3>
            <p className="mt-1 whitespace-pre-wrap text-sm leading-6 text-slate-300">
              {suggestion.impact}
            </p>
          </div>
          <p className="text-xs leading-5 text-slate-500">
            以上为模型生成的说明，命令尚未执行。请核对主机、路径和参数后自行粘贴。
          </p>
          <div className="flex justify-end">
            <button
              className="button-primary"
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(suggestion.command);
                  setCopy("已复制");
                } catch {
                  setCopy("请选中命令手动复制");
                }
              }}
            >
              {copy || "复制命令"}
            </button>
          </div>
        </div>
      )}
    </Modal>
  );
}
