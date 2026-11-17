import { useEffect, useState, type FormEvent } from "react";
import { api } from "../api/client";
import { Modal } from "../components/Modal";
import { Icon } from "../components/Icon";
import {
  useCommandShortcut,
  setCommandShortcut,
  type CommandShortcut,
} from "./shortcut";

export function AISettingsDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose(): void;
}) {
  return open ? <AISettingsForm onClose={onClose} /> : null;
}
function AISettingsForm({ onClose }: { onClose(): void }) {
  const shortcut = useCommandShortcut();
  const [provider, setProvider] = useState("openai");
  const [model, setModel] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [hasKey, setHasKey] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setLoadError("");
    api
      .aiConfig()
      .then((config) => {
        if (cancelled) return;
        setProvider(config.provider || "openai");
        setModel(config.model);
        setBaseURL(config.baseURL);
        setHasKey(config.hasKey);
      })
      .catch((e) => {
        if (!cancelled)
          setLoadError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [attempt]);
  async function save(event: FormEvent) {
    event.preventDefault();
    if (loading || loadError || saving) return;
    setSaving(true);
    setError("");
    setNotice("");
    try {
      await api.setAIConfig({
        provider,
        model: model.trim(),
        baseURL: baseURL.trim(),
        apiKey: apiKey || undefined,
      });
      setHasKey(hasKey || !!apiKey);
      setApiKey("");
      setNotice("已保存，可在终端工具栏打开命令助手。");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }
  return (
    <Modal label="AI 设置" className="max-w-lg" onClose={onClose} busy={saving}>
      <header className="modal-heading">
        <div>
          <h2>AI 设置</h2>
          <p>生成命令建议，核对后自行粘贴执行。</p>
        </div>
        <button
          type="button"
          className="icon-button"
          aria-label="关闭 AI 设置"
          onClick={onClose}
          disabled={saving}
        >
          <Icon name="close" />
        </button>
      </header>
      <label className="form-label" htmlFor="ai-shortcut">
        命令助手快捷键
      </label>
      <select
        id="ai-shortcut"
        className="form-field"
        value={shortcut}
        onChange={(e) => setCommandShortcut(e.target.value as CommandShortcut)}
      >
        <option value="ctrl-shift-k">
          Ctrl+Shift+K（保留 Shell 的 Ctrl+K）
        </option>
        <option value="ctrl-k">Ctrl+K（兼容旧习惯，占用 Shell 快捷键）</option>
      </select>
      <p className="mt-2 mb-5 text-xs leading-5 text-slate-400">
        立即生效，保存在当前浏览器；切换不会重连终端。
      </p>
      <form
        onSubmit={save}
        onChange={() => {
          setNotice("");
          setError("");
        }}
      >
        {loading && (
          <p role="status" className="mb-3 text-sm text-slate-400">
            正在读取模型配置…
          </p>
        )}
        {loadError && (
          <div role="alert" className="mb-4 text-sm text-rose-300">
            配置读取失败：{loadError}
            <button
              type="button"
              className="button-secondary ml-2"
              onClick={() => setAttempt(attempt + 1)}
            >
              重新加载
            </button>
          </div>
        )}
        <fieldset
          disabled={loading || !!loadError || saving}
          className="space-y-4 border-t border-slate-700 pt-4"
        >
          <label className="block">
            <span className="form-label">模型服务</span>
            <select
              className="form-field"
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
            >
              <option value="openai">
                OpenAI 兼容（OpenAI / OpenRouter / DeepSeek）
              </option>
              <option value="anthropic">Anthropic（Claude）</option>
            </select>
          </label>
          <label className="block">
            <span className="form-label">模型 ID</span>
            <input
              className="form-field"
              value={model}
              required
              onChange={(e) => setModel(e.target.value)}
              placeholder="与模型服务商提供的 ID 一致"
            />
          </label>
          <label className="block">
            <span className="form-label">服务地址（选填）</span>
            <input
              className="form-field"
              value={baseURL}
              type="url"
              onChange={(e) => setBaseURL(e.target.value)}
              placeholder="留空使用默认地址"
            />
          </label>
          <label className="block">
            <span className="form-label">
              API Key{hasKey ? "（已设置，留空保持不变）" : ""}
            </span>
            <input
              className="form-field"
              type="password"
              value={apiKey}
              autoComplete="new-password"
              onChange={(e) => setApiKey(e.target.value)}
              placeholder={hasKey ? "已保存在凭据库" : "输入 API Key"}
            />
          </label>
          <p className="text-xs leading-5 text-slate-400">
            密钥保存在工作台凭据库，不会回传前端。终端内容仅在生成时勾选后发送给模型。
          </p>
        </fieldset>
        {error && (
          <p role="alert" className="mt-4 text-sm text-rose-300">
            {error}
          </p>
        )}
        {notice && (
          <p role="status" className="mt-4 text-sm text-emerald-300">
            {notice}
          </p>
        )}
        <footer className="modal-actions">
          <button
            type="button"
            className="button-secondary"
            onClick={onClose}
            disabled={saving}
          >
            关闭
          </button>
          <button
            className="button-primary"
            disabled={loading || !!loadError || saving}
          >
            {saving ? "保存中…" : "保存"}
          </button>
        </footer>
      </form>
    </Modal>
  );
}
