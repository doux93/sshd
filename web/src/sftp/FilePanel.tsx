// SFTP file panel. Follows the terminal's directory (OSC 7) when
// "跟随终端" is on — the FinalShell behaviour, done with real shell
// integration instead of guesswork.
import { useCallback, useEffect, useRef, useState } from "react";
import { api, type FileEntry } from "../api/client";
import { useAppStore } from "../store";
import { uploadQueue, useTransfers, isPending } from "./transfers";
import { Icon } from "../components/Icon";

import { formatSize } from "./formatSize";

function parentOf(p: string): string {
  const trimmed = p.replace(/\/+$/, "");
  const idx = trimmed.lastIndexOf("/");
  if (idx <= 0) return "/";
  return trimmed.slice(0, idx);
}

export function FilePanel({
  sessionId,
  onTransfers,
  onClose,
}: {
  sessionId: string;
  onTransfers?(): void;
  onClose?(): void;
}) {
  const jobs = useTransfers();
  const target = useAppStore(
    (s) => s.sessions.find((x) => x.id === sessionId)?.target ?? sessionId,
  );
  const completed = jobs
    .filter((j) => j.sessionId === sessionId && j.status === "completed")
    .map((j) => j.id)
    .join(",");
  const pending = jobs.filter(
    (j) => j.sessionId === sessionId && isPending(j),
  ).length;
  const generation = useRef(0);
  const terminalCwd = useAppStore(
    (s) => s.sessions.find((x) => x.id === sessionId)?.cwd ?? "",
  );
  const setError = useAppStore((s) => s.setError);

  const [path, setPath] = useState(".");
  const [draftPath, setDraftPath] = useState(".");
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [ready, setReady] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [filter, setFilter] = useState("");
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0);
  const currentPath = useRef(".");
  const [follow, setFollow] = useState(true);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const load = useCallback(
    async (target: string) => {
      const current = ++generation.current;
      setLoading(true);
      setLoadError("");
      try {
        const res = await api.listFiles(sessionId, target);
        if (current !== generation.current) return;
        currentPath.current = res.path;
        setPath(res.path);
        setReady(true);
        setFilter("");
        setDraftPath(res.path);
        // Directories first, then case-insensitive by name.
        setEntries(
          res.entries.sort((a, b) =>
            a.isDir !== b.isDir
              ? a.isDir
                ? -1
                : 1
              : a.name.localeCompare(b.name, undefined, {
                  sensitivity: "base",
                }),
          ),
        );
      } catch (err) {
        if (current === generation.current) {
          setLoadError(
            `无法打开 ${target}：${err instanceof Error ? err.message : String(err)}`,
          );
          setDraftPath(currentPath.current);
        }
      } finally {
        if (current === generation.current) setLoading(false);
      }
    },
    [sessionId, setError],
  );

  // Initial listing (remote home directory).
  useEffect(() => {
    load(".");
    return () => {
      generation.current++;
    };
  }, [load]);
  useEffect(() => {
    if (completed) void load(path);
  }, [completed]);

  // Follow the terminal's cwd when enabled.
  useEffect(() => {
    if (follow && terminalCwd && terminalCwd !== path) load(terminalCwd);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [terminalCwd, follow]);

  const doUpload = (files: FileList | null) => {
    if (!files?.length) return;
    if (loading || !ready) {
      setLoadError("请等待目录加载成功后再上传。");
      return;
    }
    const list = Array.from(files);
    const existing = list.filter((f) => entries.some((e) => e.name === f.name));
    if (
      existing.length &&
      !window.confirm(
        `覆盖目标目录中 ${existing.length} 个同名文件？上传失败会保留原文件。`,
      )
    )
      return;
    try {
      uploadQueue.enqueue(sessionId, target, path, list);
      onTransfers?.();
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    }
  };

  const remove = async (entry: FileEntry) => {
    const target = `${path.replace(/\/+$/, "")}/${entry.name}`;
    if (
      !window.confirm(
        `删除 ${entry.isLink ? "符号链接（仅删除链接）" : entry.isDir ? "目录(含全部内容)" : "文件"}「${entry.name}」?`,
      )
    )
      return;
    try {
      await api.deleteFile(sessionId, target, entry.isDir);
      load(path);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const rename = async (entry: FileEntry) => {
    const next = window.prompt("重命名为", entry.name);
    if (!next || next === entry.name) return;
    const base = path.replace(/\/+$/, "");
    try {
      await api.renameFile(
        sessionId,
        `${base}/${entry.name}`,
        `${base}/${next}`,
      );
      load(path);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const mkdir = async () => {
    const name = window.prompt("新建目录名称");
    if (!name?.trim()) return;
    try {
      await api.mkdir(sessionId, `${path.replace(/\/+$/, "")}/${name.trim()}`);
      load(path);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const visible = entries.filter((entry) =>
    entry.name.toLocaleLowerCase().includes(filter.toLocaleLowerCase()),
  );
  return (
    <section
      aria-label="远端文件"
      aria-busy={loading}
      className={`file-panel ${dragging ? "file-drop-active" : ""}`}
      onDragEnter={(event) => {
        event.preventDefault();
        if (event.dataTransfer.types.includes("Files")) {
          dragDepth.current++;
          setDragging(true);
        }
      }}
      onDragLeave={() => {
        if (--dragDepth.current <= 0) {
          dragDepth.current = 0;
          setDragging(false);
        }
      }}
      onDragOver={(event) => event.preventDefault()}
      onDrop={(event) => {
        event.preventDefault();
        dragDepth.current = 0;
        setDragging(false);
        doUpload(event.dataTransfer.files);
      }}
    >
      <div className="file-toolbar">
        <button
          className="icon-button"
          aria-label="上级目录"
          title="上级目录"
          disabled={loading || !ready || path === "/"}
          onClick={() => load(parentOf(path))}
        >
          <Icon
            name="chevron"
            size={16}
            style={{ transform: "rotate(-90deg)" }}
          />
        </button>
        <form
          className="min-w-0 flex-1"
          onSubmit={(event) => {
            event.preventDefault();
            if (draftPath.trim()) void load(draftPath.trim());
          }}
        >
          <input
            className="file-path"
            aria-label="远端目录"
            value={draftPath}
            onChange={(event) => setDraftPath(event.target.value)}
            spellCheck={false}
            title={path}
          />
        </form>
        <button
          className="icon-button"
          aria-label="刷新目录"
          title="刷新目录"
          disabled={loading}
          onClick={() => load(path)}
        >
          <Icon name="refresh" size={16} />
        </button>
        <button
          className="icon-button"
          aria-label="新建目录"
          title="新建目录"
          disabled={loading || !ready}
          onClick={mkdir}
        >
          <Icon name="folder" size={16} />
        </button>
        <button
          className="toolbar-button"
          aria-label="上传文件"
          title="上传文件，也可拖入此区域"
          disabled={loading || !ready}
          onClick={() => fileInputRef.current?.click()}
        >
          <Icon name="upload" size={16} />
          <span className="file-action-label">上传</span>
        </button>
        {onClose && (
          <button
            className="icon-button"
            aria-label="收起文件面板"
            title="收起文件面板"
            onClick={onClose}
          >
            <Icon name="close" size={16} />
          </button>
        )}
        <input
          ref={fileInputRef}
          type="file"
          multiple
          hidden
          onChange={(event) => {
            doUpload(event.target.files);
            event.target.value = "";
          }}
        />
      </div>
      <div className="file-filterbar">
        <label className="file-filter">
          <Icon name="search" size={14} />
          <input
            aria-label="筛选文件"
            placeholder="筛选当前目录"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
        </label>
        <span className="text-xs text-slate-400">
          {loading
            ? "加载中…"
            : filter
              ? `${visible.length} / ${entries.length} 项`
              : `${entries.length} 项`}
        </span>
        <label
          className="ml-auto flex shrink-0 cursor-pointer items-center gap-1.5 text-xs text-slate-400"
          title="终端切换目录时自动跟随，需要开启 Shell 集成"
        >
          <input
            type="checkbox"
            checked={follow}
            onChange={(event) => setFollow(event.target.checked)}
          />
          跟随终端
        </label>
      </div>
      {loadError && (
        <div role="alert" className="file-error">
          <span>{loadError}</span>
          <button
            className="toolbar-button"
            onClick={() => load(path)}
            disabled={loading}
          >
            重试
          </button>
        </div>
      )}
      {pending > 0 && (
        <button
          className="border-b border-slate-700 px-3 py-2 text-left text-xs text-sky-300"
          onClick={onTransfers}
        >
          {pending} 个上传任务进行中 · 查看队列
        </button>
      )}
      <div className="min-h-0 flex-1 overflow-auto">
        {!visible.length ? (
          <p className="empty-copy !py-5 !text-xs">
            {loading
              ? "正在读取远端目录…"
              : loadError && !ready
                ? "目录尚未加载，请重试。"
                : filter
                  ? "没有匹配的文件"
                  : "目录为空，可拖入文件上传。"}
          </p>
        ) : (
          <table className="file-table">
            <thead>
              <tr>
                <th>名称</th>
                <th className="text-right">大小</th>
                <th className="file-mode">权限</th>
                <th>
                  <span className="sr-only">操作</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {visible.map((entry) => (
                <tr key={entry.name} className="group">
                  <td>
                    {entry.isDir ? (
                      <button
                        className="file-entry"
                        title={`打开目录 ${entry.name}`}
                        disabled={loading}
                        onClick={() =>
                          load(`${path.replace(/\/+$/, "")}/${entry.name}`)
                        }
                      >
                        <Icon
                          name={entry.isLink ? "link" : "folder"}
                          size={16}
                        />
                        <span className="truncate">{entry.name}</span>
                      </button>
                    ) : (
                      <a
                        className="file-entry"
                        title={`下载 ${entry.name}`}
                        download={entry.name}
                        href={api.downloadUrl(
                          sessionId,
                          `${path.replace(/\/+$/, "")}/${entry.name}`,
                        )}
                      >
                        <Icon name={entry.isLink ? "link" : "file"} size={16} />
                        <span className="truncate">{entry.name}</span>
                      </a>
                    )}
                  </td>
                  <td className="whitespace-nowrap text-right text-slate-400">
                    {entry.isDir ? "—" : formatSize(entry.size)}
                  </td>
                  <td className="file-mode font-mono text-slate-400">
                    {entry.mode}
                  </td>
                  <td>
                    <span className="file-row-actions">
                      <button
                        className="icon-button"
                        aria-label={`重命名 ${entry.name}`}
                        title="重命名"
                        disabled={loading}
                        onClick={() => rename(entry)}
                      >
                        <Icon name="edit" size={15} />
                      </button>
                      <button
                        className="icon-button hover:!text-rose-300"
                        aria-label={`删除 ${entry.name}`}
                        title="删除"
                        disabled={loading}
                        onClick={() => remove(entry)}
                      >
                        <Icon name="trash" size={15} />
                      </button>
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {dragging && (
        <div className="file-drop-hint" aria-hidden="true">
          <Icon name="upload" size={24} />
          <span>松开上传到当前目录</span>
        </div>
      )}
    </section>
  );
}
