import { useState } from "react";
import { api, type NodeInfo } from "../api/client";
import { useAppStore } from "../store";
import { Icon } from "../components/Icon";
export function ConnectionTree() {
  const st = useAppStore(),
    [query, setQuery] = useState(""),
    [mode, setMode] = useState<"all" | "favorites" | "recent">("all"),
    [expanded, setExpanded] = useState<Set<string>>(new Set());
  const hosts = st.nodes.filter((n) => n.kind === "connection"),
    q = query.trim().toLocaleLowerCase();
  const selected = hosts.filter(
    (n) =>
      (!q ||
        `${n.name} ${n.config.host} ${n.config.user}`
          .toLocaleLowerCase()
          .includes(q)) &&
      (mode !== "favorites" || st.favorites.includes(n.id)) &&
      (mode !== "recent" || !!st.recent[n.id]),
  );
  if (mode === "recent")
    selected.sort((a, b) => (st.recent[b.id] ?? 0) - (st.recent[a.id] ?? 0));
  async function remove(n: NodeInfo) {
    if (
      !confirm(
        `删除${n.kind === "folder" ? "文件夹及其中保存的主机" : "保存的主机"}「${n.name}」？已建立的 SSH 会话仍然保留。`,
      )
    )
      return;
    try {
      await api.deleteNode(n.id);
      await st.refreshNodes();
    } catch (e) {
      st.setError(String(e));
    }
  }
  async function folder(parentId = "") {
    const name = prompt("文件夹名称");
    if (!name?.trim()) return;
    try {
      await api.createNode({
        parentId,
        kind: "folder",
        name: name.trim(),
        config: {},
      });
      await st.refreshNodes();
      setExpanded((p) => new Set(p).add(parentId));
    } catch (e) {
      st.setError(String(e));
    }
  }
  async function importConfig() {
    try {
      const res = await api.importSshConfig();
      await st.refreshNodes();
      if (res.warnings.length) st.setError(res.warnings.join("\n"));
      else if (!res.imported)
        st.setError("服务运行机器的 ~/.ssh/config 中没有可导入的主机");
    } catch (e) {
      st.setError(String(e));
    }
  }
  function row(n: NodeInfo, depth = 0) {
    const isFolder = n.kind === "folder",
      live = st.sessions.some((s) => s.nodeId === n.id && s.state === "active");
    return (
      <div
        key={n.id}
        className={`group flex items-center rounded-lg py-1 pr-1 ${st.focusedNodeId === n.id ? "bg-sky-950/60" : "hover:bg-slate-800/60"}`}
        style={{ paddingLeft: 8 + depth * 12 }}
      >
        <button
          className="flex min-w-0 flex-1 items-center gap-2 py-2 text-left"
          onClick={() => {
            if (isFolder)
              setExpanded((p) => {
                const x = new Set(p);
                x.has(n.id) ? x.delete(n.id) : x.add(n.id);
                return x;
              });
            else st.focusNode(n.id);
          }}
          onDoubleClick={() => {
            if (!isFolder) void st.connectNode(n.id);
          }}
          title={
            isFolder
              ? n.name
              : `${n.config.user ?? ""}@${n.config.host ?? ""} · 双击连接`
          }
        >
          <span className={live ? "text-emerald-400" : "text-slate-500"}>
            <Icon name={isFolder ? "folder" : "server"} size={16} />
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm">{n.name}</span>
            {!isFolder && (
              <span className="block truncate text-[11px] text-slate-500">
                {n.config.host}
              </span>
            )}
          </span>
          {isFolder && (
            <Icon
              name="chevron"
              size={12}
              style={{
                transform: expanded.has(n.id) ? "rotate(90deg)" : undefined,
              }}
            />
          )}
        </button>
        {!isFolder && (
          <button
            className={`icon-button !w-6 ${st.favorites.includes(n.id) ? "!text-amber-300" : ""}`}
            aria-label={
              st.favorites.includes(n.id)
                ? `取消收藏 ${n.name}`
                : `收藏 ${n.name}`
            }
            onClick={() => st.toggleFavorite(n.id)}
          >
            <Icon name="star" size={14} />
          </button>
        )}
        <details className="relative">
          <summary
            className="icon-button list-none text-lg"
            aria-label={`${n.name} 操作`}
          >
            ⋯
          </summary>
          <div
            className="absolute right-0 top-8 z-30 w-36 rounded-lg border border-slate-600 bg-slate-900 p-1 shadow-xl"
            onClick={(e) => {
              e.currentTarget.parentElement?.removeAttribute("open");
            }}
          >
            {!isFolder && (
              <button
                className="tree-menu-item"
                onClick={() => st.connectNode(n.id)}
              >
                连接主机
              </button>
            )}
            <button
              className="tree-menu-item"
              onClick={() =>
                st.setEditingNode({ node: n, parentId: n.parentId })
              }
            >
              编辑{isFolder ? "文件夹" : "连接"}
            </button>
            {isFolder && (
              <>
                <button
                  className="tree-menu-item"
                  onClick={() => st.setEditingNode({ parentId: n.id })}
                >
                  新建主机
                </button>
                <button className="tree-menu-item" onClick={() => folder(n.id)}>
                  新建子文件夹
                </button>
              </>
            )}
            <button
              className="tree-menu-item !text-rose-300"
              onClick={() => remove(n)}
            >
              删除
            </button>
          </div>
        </details>
      </div>
    );
  }
  function tree(
    parent = "",
    seen = new Set<string>(),
    depth = 0,
  ): React.ReactNode {
    return st.nodes
      .filter((n) => n.parentId === parent && !seen.has(n.id))
      .map((n) => (
        <div key={n.id}>
          {row(n, depth)}
          {n.kind === "folder" &&
            expanded.has(n.id) &&
            tree(n.id, new Set([...seen, n.id]), depth + 1)}
        </div>
      ));
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="px-3">
        <label className="flex items-center gap-2 rounded-lg border border-slate-700 bg-slate-950/50 px-3 py-2 text-slate-500">
          <Icon name="search" size={15} />
          <input
            className="min-w-0 flex-1 bg-transparent text-sm text-slate-200 outline-none"
            placeholder="搜索名称、地址或用户"
            aria-label="搜索主机"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          {q && (
            <button onClick={() => setQuery("")} aria-label="清空搜索">
              <Icon name="close" size={12} />
            </button>
          )}
        </label>
        <div className="mt-3 mb-2 flex gap-1">
          {(
            [
              ["all", "全部"],
              ["favorites", "收藏"],
              ["recent", "最近"],
            ] as const
          ).map(([value, label]) => (
            <button
              key={value}
              className={`toolbar-button flex-1 ${mode === value ? "selected" : ""}`}
              onClick={() => setMode(value)}
            >
              {label}
            </button>
          ))}
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-2">
        {q || mode !== "all" ? (
          selected.length ? (
            selected.map((n) => row(n))
          ) : (
            <p className="empty-copy">
              {q
                ? "没有匹配的主机"
                : mode === "favorites"
                  ? "点主机旁的星标，收藏常用主机。"
                  : "成功连接后会显示在这里。"}
            </p>
          )
        ) : st.nodes.length ? (
          tree()
        ) : (
          <div className="empty-copy">
            还没有保存的主机。
            <button
              className="mt-4 button-secondary w-full text-xs"
              onClick={importConfig}
            >
              从 SSH 配置导入
            </button>
            <p className="mt-2 text-[11px]">
              读取运行 sshx 的机器上的 ~/.ssh/config
            </p>
          </div>
        )}
      </div>
      <div className="flex justify-between border-t border-slate-800 px-3 py-2">
        <span className="text-xs text-slate-500 self-center">
          {hosts.length} 台主机
        </span>
        <div className="flex">
          <button
            className="icon-button"
            aria-label="导入服务运行机器的 SSH 配置"
            title="从服务运行机器的 ~/.ssh/config 导入"
            onClick={importConfig}
          >
            <Icon name="upload" size={16} />
          </button>
          <button
            className="icon-button"
            aria-label="新建文件夹"
            onClick={() => folder()}
          >
            <Icon name="folder" size={16} />
          </button>
        </div>
      </div>
    </div>
  );
}
