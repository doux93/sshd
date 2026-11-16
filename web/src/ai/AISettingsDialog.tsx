import { useEffect, useState } from "react";
import { api, type AIConfig } from "../api/client";

// Modal for the LLM provider config used by Ctrl+K. The API key is
// stored in the vault server-side; the UI never receives it back, only
// a hasKey flag. Leaving the key field blank keeps the stored key.
export function AISettingsDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const [provider, setProvider] = useState("openai");
  const [model, setModel] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [hasKey, setHasKey] = useState(false);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    if (!open) return;
    setErr("");
    api
      .aiConfig()
      .then((c: AIConfig) => {
        setProvider(c.provider || "openai");
        setModel(c.model);
        setBaseURL(c.baseURL);
        setHasKey(c.hasKey);
      })
      .catch(() => {});
  }, [open]);

  if (!open) return null;

  const save = async () => {
    setSaving(true);
    setErr("");
    try {
      await api.setAIConfig({
        provider,
        model,
        baseURL,
        apiKey: apiKey || undefined,
      });
      setApiKey("");
      setHasKey(true);
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onClose}
    >
      <div
        className="w-[min(28rem,92%)] rounded-lg border border-slate-600 bg-slate-800 p-5 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="mb-3 text-base font-semibold text-slate-100">
          AI 设置 (Ctrl+K)
        </h2>
        <p className="mb-4 text-xs text-slate-400">
          配置命令生成用的 LLM。API key 存在凭据库,不会回传前端。
        </p>
        <label className="mb-1 block text-xs text-slate-400">Provider</label>
        <select
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
          className="mb-3 w-full rounded border border-slate-600 bg-slate-900 px-2 py-1.5 text-sm text-slate-100"
        >
          <option value="openai">
            OpenAI 兼容 (OpenAI / OpenRouter / DeepSeek)
          </option>
          <option value="anthropic">Anthropic (Claude)</option>
        </select>

        <label className="mb-1 block text-xs text-slate-400">Model</label>
        <input
          value={model}
          onChange={(e) => setModel(e.target.value)}
          placeholder="gpt-4o-mini / claude-3-5-haiku-latest"
          className="mb-3 w-full rounded border border-slate-600 bg-slate-900 px-2 py-1.5 text-sm text-slate-100"
        />

        <label className="mb-1 block text-xs text-slate-400">
          Base URL (可选,留空用默认)
        </label>
        <input
          value={baseURL}
          onChange={(e) => setBaseURL(e.target.value)}
          placeholder="https://api.openai.com/v1"
          className="mb-3 w-full rounded border border-slate-600 bg-slate-900 px-2 py-1.5 text-sm text-slate-100"
        />

        <label className="mb-1 block text-xs text-slate-400">
          API Key{hasKey ? " (已设置,留空保持不变)" : ""}
        </label>
        <input
          type="password"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          placeholder={hasKey ? "••••••••" : "sk-…"}
          className="mb-4 w-full rounded border border-slate-600 bg-slate-900 px-2 py-1.5 text-sm text-slate-100"
        />

        {err && <p className="mb-3 text-xs text-red-400">{err}</p>}

        <div className="flex justify-end gap-2">
          <button
            onClick={onClose}
            className="rounded border border-slate-600 px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-700"
          >
            关闭
          </button>
          <button
            onClick={save}
            disabled={saving}
            className="rounded bg-emerald-600 px-3 py-1.5 text-sm text-white hover:bg-emerald-500 disabled:opacity-40"
          >
            {saving ? "保存中…" : "保存"}
          </button>
        </div>
      </div>
    </div>
  );
}
