import { useEffect, useRef, useState } from "react";
import { useAppStore } from "./store";
import { api, getMode } from "./api/client";
import { wsClient, type ConnectionStatus } from "./ws/protocol";
import { TerminalWorkspace } from "./workspace/TerminalWorkspace";
import { CommandLibrary } from "./workspace/CommandLibrary";
import { ThemeToggle } from "./components/ThemeToggle";
import { TransferPanel } from "./sftp/TransferPanel";
import { uploadQueue, useTransfers, isPending } from "./sftp/transfers";
import { protectPendingUploads, confirmUploadLogout } from "./sftp/leaveGuard";
import { MonitorBar } from "./monitor/MonitorBar";
import { FilePanel } from "./sftp/FilePanel";
import { ConnectDialog } from "./ConnectDialog";
import { ConnectionTree } from "./tree/ConnectionTree";
import { NodeDialog } from "./NodeDialog";
import { HostKeyDialog } from "./HostKeyDialog";
import { useShortcuts } from "./useShortcuts";
import { AISettingsDialog } from "./ai/AISettingsDialog";
import { MobileLayout } from "./mobile/MobileLayout";
import { useIsMobile } from "./mobile/useIsMobile";
import { Icon } from "./components/Icon";
import { ResizeHandle } from "./components/ResizeHandle";
import { SessionShelf } from "./workspace/SessionShelf";
import { AlertsPanel } from "./workspace/AlertsPanel";
import {
  readPreference,
  writePreference,
  boundedNumber,
} from "./workspace/preferences";

export default function App() {
  const st = useAppStore(),
    mobile = useIsMobile();
  const [status, setStatus] = useState<ConnectionStatus>("connecting");
  const [side, setSide] = useState(() =>
    boundedNumber(readPreference("layout.side", 256), 256, 200, 360),
  );
  const [files, setFiles] = useState(() =>
    boundedNumber(readPreference("layout.files", 240), 240, 120, 500),
  );
  const [monitor, setMonitor] = useState(() =>
    boundedNumber(readPreference("layout.monitor", 200), 200, 160, 320),
  );
  const [collapsed, setCollapsed] = useState(
    () => readPreference<unknown>("layout.collapsed", false) === true,
  );
  const [showFiles, setShowFiles] = useState(
    () => readPreference<unknown>("layout.showFiles", false) === true,
  );
  const [showMonitor, setShowMonitor] = useState(
    () => readPreference("layout.showMonitor", true) === true,
  );
  const [panel, setPanel] = useState<
    "sessions" | "alerts" | "transfers" | null
  >(() =>
    new URL(location.href).searchParams.get("panel") === "alerts"
      ? "alerts"
      : null,
  );
  const [showAI, setShowAI] = useState(false);
  const [showCommands, setShowCommands] = useState(false);
  const transfers = useTransfers();
  const pendingTransfers = transfers.filter(isPending).length;
  useEffect(() => protectPendingUploads(uploadQueue, window), []);
  const [alertNode, setAlertNode] = useState(() =>
    new URL(location.href).searchParams.get("node"),
  );
  const deepLinkApplied = useRef(false);
  const refreshGeneration = useRef(0);
  useEffect(() => {
    writePreference("layout.side", side);
    writePreference("layout.files", files);
    writePreference("layout.monitor", monitor);
    writePreference("layout.collapsed", collapsed);
    writePreference("layout.showFiles", showFiles);
    writePreference("layout.showMonitor", showMonitor);
  }, [side, files, monitor, collapsed, showFiles, showMonitor]);
  useEffect(() => {
    const refresh = async () => {
      const generation = ++refreshGeneration.current;
      try {
        await Promise.all([
          st.refresh(),
          st.refreshNodes(),
          st.refreshAlerts(),
        ]);
        if (generation !== refreshGeneration.current) return;
        if (!deepLinkApplied.current) {
          deepLinkApplied.current = true;
          const node = new URL(location.href).searchParams.get("node");
          if (node) {
            const current = useAppStore.getState();
            current.focusNode(node);
            current.setActive(
              current.sessions.find(
                (s) => s.nodeId === node && s.state === "active",
              )?.id ?? null,
            );
          }
        }
      } catch (e) {
        st.setError(String(e));
      }
    };
    refresh();
    const unsub = wsClient.onSessionEvent((id, event) => {
      if (event === "closed") st.markClosed(id);
      void st.refresh().catch((e) => st.setError(String(e)));
    });
    const cwd = wsClient.onCwd(st.setSessionCwd);
    const alerts = wsClient.onAlert(() => {
      void st.refreshAlerts();
    });
    const connection = wsClient.onStatus((s) => {
      setStatus(s);
      if (s === "connected") refresh();
    });
    return () => {
      unsub();
      cwd();
      alerts();
      connection();
    };
  }, []);
  useShortcuts();
  const active = st.sessions.find((s) => s.id === st.activeId),
    focus = st.nodes.find((n) => n.id === st.focusedNodeId);
  const open = st.openIds
    .map((id) => st.sessions.find((s) => s.id === id))
    .filter((s): s is NonNullable<typeof s> => !!s);
  const unread = st.alerts.filter((a) => !a.acknowledgedAt).length;
  const background = st.sessions.filter(
    (s) => s.state === "active" && !st.openIds.includes(s.id),
  ).length;
  function viewNode(id: string) {
    st.focusNode(id);
    setPanel(null);
    const sess = st.sessions.find(
      (s) => s.nodeId === id && s.state === "active",
    );
    if (sess) st.setActive(sess.id);
    else st.setActive(null);
  }
  async function logout() {
    if (!confirmUploadLogout(uploadQueue, (message) => window.confirm(message)))
      return;
    try {
      await api.logout();
      wsClient.disconnect();
      location.assign("/login");
    } catch (e) {
      st.setError(`退出失败，请重试：${String(e)}`);
    }
  }
  if (mobile)
    return (
      <>
        <MobileLayout status={status} />
        <HostKeyDialog />
      </>
    );
  return (
    <div className="flex h-dvh overflow-hidden bg-[var(--surface-base)] text-slate-100">
      <aside
        className="flex shrink-0 flex-col border-r border-slate-800 bg-[var(--surface-panel)]"
        style={{ width: collapsed ? 52 : side }}
      >
        <div className="flex h-12 shrink-0 items-center gap-2 px-3">
          <button
            className="icon-button text-sky-300"
            onClick={() => setCollapsed(!collapsed)}
            aria-label={collapsed ? "展开主机栏" : "收起主机栏"}
          >
            <Icon name="panel" />
          </button>
          {!collapsed && (
            <>
              <span className="text-lg font-semibold tracking-tight">
                sshx
                <span className="ml-2 text-xs font-normal text-slate-500">
                  工作台
                </span>
              </span>
            </>
          )}
        </div>
        {!collapsed ? (
          <>
            <div className="grid grid-cols-2 gap-2 px-3 pb-4">
              <button
                className="button-primary text-xs"
                onClick={() => st.setEditingNode({ parentId: "" })}
              >
                <Icon name="plus" size={15} />
                新建主机
              </button>
              <button
                className="button-secondary text-xs"
                onClick={() => st.setQuickConnectOpen(true)}
              >
                快速连接
              </button>
            </div>
            <ConnectionTree />
            <div className="border-t border-slate-800 p-3 flex items-center gap-2">
              <button
                className="button-secondary flex-1 text-xs"
                onClick={() => setShowAI(true)}
              >
                <Icon name="sparkles" size={15} />
                AI 设置
              </button>
              {getMode() === "remote" && (
                <button
                  className="icon-button"
                  onClick={logout}
                  aria-label="退出登录"
                >
                  <Icon name="logout" />
                </button>
              )}
            </div>
          </>
        ) : (
          <button
            className="icon-button mx-auto"
            onClick={() => st.setQuickConnectOpen(true)}
            aria-label="快速连接"
          >
            <Icon name="plus" />
          </button>
        )}
      </aside>
      {!collapsed && (
        <ResizeHandle
          axis="x"
          value={side}
          min={200}
          max={360}
          onChange={setSide}
          label="主机栏宽度"
        />
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-11 shrink-0 items-center gap-2 border-b border-slate-800 px-3">
          <span
            className={`status-dot ${status === "connected" ? "bg-emerald-400" : "bg-amber-400 animate-pulse"}`}
          />
          <span className="min-w-0 flex-1 truncate text-xs text-slate-400">
            {status === "connected"
              ? "工作台已连接"
              : status === "reconnecting"
                ? "正在重新连接工作台…"
                : "工作台连接中…"}
          </span>
          <span className="ml-auto text-xs text-slate-500 hidden xl:inline">
            {getMode() === "remote" ? "远程工作台" : "本地工作台"}
          </span>
          <ThemeToggle />
          <button
            className={`toolbar-button ${showCommands ? "selected" : ""}`}
            aria-label="常用命令"
            title="常用命令"
            onClick={() => setShowCommands(true)}
          >
            <Icon name="star" size={15} />
            <span className="hidden lg:inline">常用命令</span>
          </button>
          <button
            className={`toolbar-button ${panel === "transfers" ? "selected" : ""}`}
            aria-label="上传"
            title="上传"
            onClick={() => setPanel(panel === "transfers" ? null : "transfers")}
          >
            <Icon name="upload" size={15} />
            <span className="hidden lg:inline">上传</span>
            {pendingTransfers > 0 && (
              <span className="count-badge">{pendingTransfers}</span>
            )}
          </button>
          <button
            className={`toolbar-button ${panel === "sessions" ? "selected" : ""}`}
            aria-label="会话库"
            title="会话库"
            onClick={() => setPanel(panel === "sessions" ? null : "sessions")}
          >
            <Icon name="terminal" size={16} />
            <span className="hidden lg:inline">会话库</span>
            {background > 0 && (
              <span className="count-badge">{background} 后台</span>
            )}
          </button>
          <button
            className={`toolbar-button ${panel === "alerts" ? "selected" : ""}`}
            aria-label="告警"
            title="告警"
            onClick={() => setPanel(panel === "alerts" ? null : "alerts")}
          >
            <Icon name="bell" size={16} />
            <span className="hidden lg:inline">告警</span>
            {unread > 0 && <span className="count-badge">{unread}</span>}
          </button>
        </header>
        <div className="session-bar">
          <div className="session-tabs" role="tablist" aria-label="打开的会话">
            {open.map((s, i) => (
              <div
                key={s.id}
                className={`flex shrink-0 items-center gap-1 rounded-t-lg border px-2 py-1 ${s.id === st.activeId ? "border-slate-600 border-b-transparent bg-[var(--terminal-bg)]" : "border-transparent text-slate-400"}`}
              >
                <button
                  role="tab"
                  aria-selected={s.id === st.activeId}
                  className="flex max-w-52 items-center gap-2 px-1 py-1 text-sm"
                  title={`${s.target} · Alt+${i + 1}`}
                  onClick={() => st.setActive(s.id)}
                >
                  <span
                    className={`status-dot ${s.state === "active" ? "bg-emerald-400" : "bg-slate-500"}`}
                  />
                  <span className="truncate">{s.label}</span>
                </button>
                <button
                  className="icon-button !h-7 !w-7"
                  onClick={() => st.closeView(s.id)}
                  aria-label={`关闭视图 ${s.label}，保留 SSH 会话`}
                  title="关闭视图，保留 SSH 会话"
                >
                  <Icon name="close" size={13} />
                </button>
              </div>
            ))}
            {open.length === 0 && (
              <span className="px-3 pb-3 text-xs text-slate-500">
                当前没有打开的视图
              </span>
            )}
          </div>
          <div className="session-tools">
            <button
              disabled={!active?.hasFiles}
              className={`toolbar-button ${showFiles ? "selected" : ""}`}
              onClick={() => setShowFiles(!showFiles)}
            >
              <Icon name="folder" size={15} />
              文件
            </button>
            <button
              disabled={!active?.monitored}
              className={`toolbar-button ${showMonitor ? "selected" : ""}`}
              onClick={() => setShowMonitor(!showMonitor)}
            >
              <Icon name="monitor" size={15} />
              监控
            </button>
          </div>
        </div>
        <main className="relative flex min-h-0 min-w-0 flex-1">
          <div className="flex min-w-0 flex-1 flex-col">
            {active ? (
              <>
                <TerminalWorkspace />
                {showFiles && active.hasFiles && (
                  <>
                    <ResizeHandle
                      axis="y"
                      value={files}
                      min={120}
                      max={500}
                      reverse
                      onChange={setFiles}
                      label="文件面板高度"
                    />
                    <div
                      className="shrink-0"
                      style={{ height: files, maxHeight: "55%" }}
                    >
                      <FilePanel
                        key={active.id}
                        sessionId={active.id}
                        onTransfers={() => setPanel("transfers")}
                        onClose={() => setShowFiles(false)}
                      />
                    </div>
                  </>
                )}
              </>
            ) : (
              <div className="flex h-full items-center justify-center overflow-auto p-8">
                <div className="w-full max-w-lg rounded-xl border border-slate-800 bg-slate-900/50 p-6">
                  <Icon name={focus ? "server" : "terminal"} size={30} />
                  <h1 className="mt-3 text-xl font-semibold">
                    {focus?.name ??
                      (st.recoveries.length
                        ? "继续远端工作"
                        : st.nodes.some((n) => n.kind === "connection")
                          ? "选择一台主机"
                          : "从一台主机开始")}
                  </h1>
                  <p className="mt-3 text-sm leading-7 text-slate-400">
                    {focus
                      ? `${focus.config.user ?? ""}@${focus.config.host ?? ""} · 当前没有打开的会话。连接后可使用终端、文件和监控。`
                      : "保存常用主机，在浏览器中连接与管理。关闭视图后，可从会话库继续工作。"}
                  </p>
                  {!focus && !st.nodes.some((n) => n.kind === "connection") && (
                    <ol
                      className="mt-4 space-y-2 text-xs text-slate-400"
                      aria-label="首次连接步骤"
                    >
                      <li>1. 添加地址、端口、用户和认证方式</li>
                      <li>2. 测试连接并核对主机指纹</li>
                      <li>3. 保存并连接，开始使用终端</li>
                    </ol>
                  )}
                  <div className="mt-6 flex flex-wrap gap-3">
                    {focus ? (
                      <button
                        className="button-primary"
                        disabled={st.connecting}
                        onClick={() => st.connectNode(focus.id)}
                      >
                        连接这台主机
                      </button>
                    ) : (
                      <button
                        className="button-primary"
                        onClick={() => st.setEditingNode({ parentId: "" })}
                      >
                        {st.nodes.some((n) => n.kind === "connection")
                          ? "新建主机"
                          : "新建第一台主机"}
                      </button>
                    )}
                    <button
                      className="button-secondary"
                      onClick={() => setPanel("sessions")}
                    >
                      {st.recoveries.length
                        ? `恢复已有终端（${st.recoveries.length}）`
                        : "打开会话库"}
                    </button>
                  </div>
                  {background > 0 && (
                    <p className="mt-4 text-xs text-emerald-300">
                      {background} 个会话正在后台运行
                    </p>
                  )}
                </div>
              </div>
            )}
          </div>
          {active?.monitored && showMonitor && (
            <>
              <ResizeHandle
                axis="x"
                value={monitor}
                min={160}
                max={320}
                reverse
                onChange={setMonitor}
                label="监控面板宽度"
              />
              <div className="shrink-0" style={{ width: monitor }}>
                <MonitorBar sessionId={active.id} />
              </div>
            </>
          )}
          {panel === "sessions" && (
            <SessionShelf onClose={() => setPanel(null)} />
          )}
          {panel === "alerts" && (
            <div className="workspace-panel absolute inset-y-0 right-0 z-20 w-80 max-w-full shadow-2xl">
              <AlertsPanel
                nodeId={alertNode}
                onClearFilter={() => setAlertNode(null)}
                onNode={viewNode}
                onClose={() => setPanel(null)}
              />
            </div>
          )}
          {panel === "transfers" && (
            <TransferPanel onClose={() => setPanel(null)} />
          )}
        </main>
      </div>
      {st.connecting && (
        <div
          className="fixed bottom-5 left-1/2 z-40 -translate-x-1/2 rounded-full border border-sky-700 bg-slate-900 px-5 py-3 text-sm shadow-xl"
          role="status"
        >
          正在连接主机…
        </div>
      )}
      {st.error && (
        <div
          role="alert"
          className="fixed bottom-4 right-4 z-[90] flex w-[calc(100%-2rem)] max-w-md items-start gap-3 rounded-xl border border-rose-800 bg-rose-950 p-4 text-sm shadow-xl"
        >
          <span className="whitespace-pre-wrap">{st.error}</span>
          <button aria-label="关闭错误提示" onClick={() => st.setError(null)}>
            <Icon name="close" size={16} />
          </button>
        </div>
      )}
      {showCommands && (
        <CommandLibrary onClose={() => setShowCommands(false)} />
      )}
      <ConnectDialog />
      <NodeDialog />
      <HostKeyDialog />
      <AISettingsDialog open={showAI} onClose={() => setShowAI(false)} />
    </div>
  );
}
