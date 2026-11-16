import { useEffect, useState } from "react";
import { api, getMode, type AlertRecord } from "../api/client";
import { useAppStore } from "../store";
import { Icon } from "../components/Icon";
import { NotificationStatus } from "./NotificationStatus";
import { enableNotifications } from "./notifications";
export function AlertsPanel({
  onNode,
  onClose,
  nodeId,
  onClearFilter,
}: {
  onNode: (id: string) => void;
  onClose?: () => void;
  nodeId?: string | null;
  onClearFilter?: () => void;
}) {
  const { alerts, acknowledgeAlert, refreshAlerts, nodes } = useAppStore();
  const [older, setOlder] = useState<AlertRecord[]>([]),
    [loading, setLoading] = useState(false),
    [more, setMore] = useState(true),
    [error, setError] = useState("");
  const [onlyUnread, setOnlyUnread] = useState(false);
  const [filtered, setFiltered] = useState<AlertRecord[]>([]);
  const [push, setPush] = useState(""),
    [subscribing, setSubscribing] = useState(false);
  useEffect(() => {
    void refreshAlerts();
  }, [refreshAlerts]);
  useEffect(() => {
    setOlder([]);
    setMore(true);
  }, [nodeId]);
  useEffect(() => {
    if (!nodeId) return;
    let current = true;
    setError("");
    api
      .listAlerts(0, nodeId)
      .then((items) => {
        if (current) setFiltered(items);
      })
      .catch((e) => {
        if (current) setError(String(e));
      });
    return () => {
      current = false;
    };
  }, [nodeId, alerts]);
  const source = nodeId ? filtered.filter((a) => a.nodeId === nodeId) : alerts;
  const all = [
    ...source,
    ...older.filter(
      (a) =>
        (!nodeId || a.nodeId === nodeId) && !source.some((x) => x.id === a.id),
    ),
  ].sort((a, b) => b.id - a.id);
  const visible = onlyUnread ? all.filter((a) => !a.acknowledgedAt) : all;
  async function loadMore() {
    setLoading(true);
    setError("");
    try {
      const next = await api.listAlerts(all.at(-1)?.id ?? 0, nodeId ?? "");
      setOlder((prev) => [...prev, ...next]);
      setMore(next.length === 100);
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }
  async function ack(id: number) {
    try {
      await acknowledgeAlert(id);
      setFiltered((prev) =>
        prev.map((a) =>
          a.id === id ? { ...a, acknowledgedAt: Date.now() } : a,
        ),
      );
      setOlder((prev) =>
        prev.map((a) =>
          a.id === id ? { ...a, acknowledgedAt: Date.now() } : a,
        ),
      );
    } catch (e) {
      setError(String(e));
    }
  }
  return (
    <section className="flex h-full min-h-0 flex-col" aria-label="告警历史">
      <header className="panel-heading">
        <div>
          <strong>告警历史</strong>
          <p className="mt-1 text-xs text-slate-400">
            最近 1,000 条 · 确认仅标记已查看
          </p>
        </div>
        {onClose && (
          <button
            className="icon-button"
            onClick={onClose}
            aria-label="收起告警"
          >
            <Icon name="close" />
          </button>
        )}
      </header>
      {nodeId && (
        <div className="flex items-center justify-between gap-2 border-b border-slate-800 px-4 py-2 text-xs">
          <span className="truncate text-sky-300">
            主机：
            {nodes.find((n) => n.id === nodeId)?.name ||
              filtered[0]?.nodeLabel ||
              nodeId}
          </span>
          <button className="shrink-0 text-slate-400" onClick={onClearFilter}>
            全部告警
          </button>
        </div>
      )}
      {getMode() === "remote" && (
        <div className="border-b border-slate-800 px-4 py-2">
          <NotificationStatus />
          <button
            className="text-xs text-sky-300"
            disabled={subscribing}
            onClick={async () => {
              setSubscribing(true);
              try {
                setPush(await enableNotifications());
              } catch (e) {
                setPush(e instanceof Error ? e.message : String(e));
              } finally {
                setSubscribing(false);
              }
            }}
          >
            {subscribing ? "正在启用…" : "启用当前设备通知"}
          </button>
          {push && (
            <p role="status" className="mt-1 text-xs text-slate-400">
              {push}
            </p>
          )}
        </div>
      )}
      <div className="flex items-center justify-between border-b border-slate-800 px-4 py-2 text-xs">
        <label className="flex gap-2 items-center">
          <input
            type="checkbox"
            checked={onlyUnread}
            onChange={(e) => setOnlyUnread(e.target.checked)}
          />
          仅看未确认
        </label>
        <button
          className="icon-button"
          onClick={() => refreshAlerts()}
          aria-label="刷新告警"
        >
          <Icon name="refresh" size={15} />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3 space-y-3">
        {visible.length === 0 && (
          <p className="empty-copy">
            暂无{onlyUnread ? "未确认" : ""}告警。主机需开启监控并配置告警规则。
          </p>
        )}
        {visible.map((a) => (
          <article
            key={a.id}
            className={`rounded-xl border p-3 ${a.acknowledgedAt ? "border-slate-800 bg-slate-900/50" : "border-amber-700/50 bg-amber-950/10"}`}
          >
            <div className="flex items-center gap-2 text-xs">
              <span
                className={`status-dot ${a.level === "critical" ? "bg-rose-400" : "bg-amber-400"}`}
              />
              <span className="truncate font-medium">
                {nodes.find((n) => n.id === a.nodeId)?.name ||
                  a.nodeLabel ||
                  "未命名主机"}
              </span>
              <span className="ml-auto shrink-0 text-slate-500">
                {a.acknowledgedAt ? "已确认" : "待查看"}
              </span>
            </div>
            <p className="mt-2 break-words text-sm leading-6">{a.message}</p>
            <time className="mt-2 block text-xs text-slate-500">
              {new Date(a.ts).toLocaleString()}
            </time>
            <div className="mt-3 flex gap-3 text-xs">
              <button className="text-sky-300" onClick={() => onNode(a.nodeId)}>
                查看主机与会话
              </button>
              {!a.acknowledgedAt && (
                <button
                  className="ml-auto text-slate-300"
                  onClick={() => ack(a.id)}
                >
                  标记已确认
                </button>
              )}
            </div>
          </article>
        ))}
        {error && (
          <p role="alert" className="text-sm text-rose-300">
            {error}
          </p>
        )}
        {more && all.length >= 100 && (
          <button
            className="button-secondary w-full"
            disabled={loading}
            onClick={loadMore}
          >
            {loading ? "加载中…" : "更早的告警"}
          </button>
        )}
      </div>
    </section>
  );
}
