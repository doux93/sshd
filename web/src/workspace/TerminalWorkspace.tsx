import { useEffect, useRef, useState } from "react";
import { useAppStore } from "../store";
import { TerminalView, type TerminalActions } from "../terminal/TerminalView";
import { readPreference, writePreference } from "./preferences";
import { Icon } from "../components/Icon";
import { useCommandShortcut, shortcutLabel } from "../ai/shortcut";

export function TerminalWorkspace() {
  const shortcut = useCommandShortcut();
  const { sessions, openIds, activeId, setActive } = useAppStore();
  const actions = useRef(new Map<string, TerminalActions>());
  const active = sessions.find((s) => s.id === activeId);
  const [split, setSplit] = useState(false);
  const [stacked, setStacked] = useState(
    () => readPreference<unknown>("layout.stacked", false) === true,
  );
  const [pair, setPair] = useState<string[]>([]);
  const [focused, setFocused] = useState(0);
  const open = openIds
    .map((id) => sessions.find((s) => s.id === id))
    .filter((s) => !!s);
  const validPair = pair.filter((id) => openIds.includes(id));
  useEffect(() => {
    if (!split) return;
    if (openIds.length < 2) {
      setSplit(false);
      return;
    }
    if (activeId && !validPair.includes(activeId)) {
      const other =
        validPair.find((_, i) => i !== focused) ??
        openIds.find((id) => id !== activeId)!;
      setPair(focused === 0 ? [activeId, other] : [other, activeId]);
    } else if (validPair.length < 2) {
      const first = validPair[0] ?? activeId ?? openIds[0];
      setPair([first, openIds.find((id) => id !== first)!]);
    }
  }, [activeId, openIds, split, focused, pair]);
  const displayed =
    split && validPair.length === 2 ? validPair : activeId ? [activeId] : [];
  function toggleSplit() {
    if (split) {
      setSplit(false);
      return;
    }
    if (!activeId || open.length < 2) return;
    setPair([activeId, open.find((s) => s!.id !== activeId)!.id]);
    setFocused(0);
    setSplit(true);
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="terminal-toolbar">
        <div
          className="terminal-context"
          title={`${active?.target ?? ""}${active?.cwd ? " · " + active.cwd : ""}`}
        >
          <span className="truncate font-mono text-xs text-slate-300">
            {active?.target}
          </span>
          <span className="truncate text-[11px] text-slate-400">
            {active?.state === "closed"
              ? "连接已断开"
              : active?.recoveryId
                ? "tmux 保活"
                : active?.cwd || "SSH 终端"}
          </span>
        </div>
        {active?.state === "closed" && active.nodeId && (
          <button
            className="toolbar-button text-amber-300"
            onClick={() =>
              useAppStore.getState().reconnectClosedSession(active.id)
            }
          >
            重新连接
          </button>
        )}
        <button
          className="toolbar-button"
          aria-label="搜索终端"
          title="搜索终端输出（Ctrl+Shift+F）"
          onClick={() => activeId && actions.current.get(activeId)?.search()}
        >
          <Icon name="search" size={15} />
          <span className="terminal-action-label">搜索</span>
        </button>
        <button
          className="toolbar-button"
          aria-label="命令助手"
          title={`命令助手（${shortcutLabel(shortcut)}）`}
          disabled={!active?.nodeId || active.state !== "active"}
          onClick={() => activeId && actions.current.get(activeId)?.assistant()}
        >
          <Icon name="sparkles" size={15} />
          <span className="terminal-action-label">命令助手</span>
        </button>
        <button
          className={`toolbar-button ${split ? "selected" : ""}`}
          disabled={!activeId || open.length < 2}
          title={
            open.length < 2
              ? "打开两个会话视图后可分屏"
              : "同时查看两个会话；输入仅发送到当前聚焦的终端"
          }
          onClick={toggleSplit}
        >
          <Icon name="split" size={14} />
          {split ? "退出分屏" : "分屏"}
        </button>
        {split && (
          <button
            className="toolbar-button"
            onClick={() => {
              setStacked(!stacked);
              writePreference("layout.stacked", !stacked);
            }}
          >
            {stacked ? "左右排列" : "上下排列"}
          </button>
        )}
      </div>
      <div
        className={
          displayed.length === 2
            ? `split-terminals ${stacked ? "stacked" : ""}`
            : "flex min-h-0 flex-1"
        }
      >
        {displayed.map((id, index) => {
          const session = sessions.find((s) => s.id === id);
          if (!session) return null;
          return (
            <section
              key={id}
              aria-label={`终端窗格 ${session.label}`}
              data-focused={activeId === id}
              className="terminal-pane flex-1"
              onPointerDownCapture={() => {
                setFocused(index);
                setActive(id);
              }}
              onFocusCapture={() => {
                setFocused(index);
                setActive(id);
              }}
            >
              {displayed.length === 2 && (
                <div className="flex h-8 shrink-0 items-center gap-2 px-2 text-xs">
                  <span className="truncate">{session.label}</span>
                  <span className="truncate font-mono text-slate-500">
                    {session.target}
                  </span>
                </div>
              )}
              <div className="min-h-0 flex-1 p-2">
                <TerminalView
                  ref={(value) => {
                    if (value) actions.current.set(id, value);
                    else actions.current.delete(id);
                  }}
                  sessionId={id}
                  nodeId={session.nodeId}
                  readOnly={session.state === "closed"}
                  focusOnMount={activeId === id}
                />
              </div>
            </section>
          );
        })}
      </div>
    </div>
  );
}
