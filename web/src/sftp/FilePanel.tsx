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
}: {
  sessionId: string;
  onTransfers?(): void;
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
  const [loading, setLoading] = useState(false);
  const [follow, setFollow] = useState(true);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const load = useCallback(
    async (target: string) => {
      const current = ++generation.current;
      setLoading(true);
      try {
        const res = await api.listFiles(sessionId, target);
        if (current !== generation.current) return;
        setPath(res.path);
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
        if (current === generation.current)
          setError(err instanceof Error ? err.message : String(err));
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

  const enter = (entry: FileEntry) => {
    if (entry.isDir) load(`${path.replace(/\/+$/, "")}/${entry.name}`);
    else
      window.open(
        api.downloadUrl(sessionId, `${path.replace(/\/+$/, "")}/${entry.name}`),
      );
  };

  const doUpload = (files: FileList | null) => {
    if (!files?.length) return;
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

  return (
    <div
      className="flex h-full flex-col bg-slate-900 text-xs"
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault();
        doUpload(e.dataTransfer.files);
      }}
    >
      {/* 工具栏 */}
      <div className="flex items-center gap-1 border-b border-slate-700 px-2 py-1.5">
        <button
          onClick={() => load(parentOf(path))}
          className="rounded px-1.5 py-0.5 text-slate-300 hover:bg-slate-700 hover:text-slate-50"
          title="上级目录"
        >
          <Icon
            name="chevron"
            size={16}
            style={{ transform: "rotate(-90deg)" }}
          />
        </button>
        <input
          className="min-w-0 flex-1 rounded border border-transparent bg-slate-800 px-2 py-1 font-mono text-slate-200 outline-none focus:border-blue-600"
          aria-label="远端目录"
          value={draftPath}
          onChange={(e) => setDraftPath(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && load(draftPath)}
          spellCheck={false}
        />
        <button
          onClick={() => load(path)}
          className="rounded px-1.5 py-0.5 text-slate-300 hover:bg-slate-700 hover:text-slate-50"
          title="刷新"
        >
          <Icon name="refresh" size={16} />
        </button>
        <button
          onClick={mkdir}
          className="rounded px-1.5 py-0.5 text-slate-300 hover:bg-slate-700 hover:text-slate-50"
          title="新建目录"
        >
          <Icon name="folder" size={16} />
        </button>
        <button
          onClick={() => fileInputRef.current?.click()}
          className="rounded px-1.5 py-0.5 text-slate-300 hover:bg-slate-700 hover:text-slate-50"
          aria-label="上传文件"
          title="上传文件(也可直接拖入)"
        >
          <Icon name="upload" size={16} />
        </button>
        <label
          className="flex cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 text-slate-300 hover:text-slate-50"
          title="终端 cd 时,文件面板自动跟随(需要连接时开启 shell 集成)"
        >
          <input
            type="checkbox"
            checked={follow}
            onChange={(e) => setFollow(e.target.checked)}
            className="accent-blue-600"
          />
          跟随终端
        </label>
        <input
          ref={fileInputRef}
          type="file"
          multiple
          hidden
          onChange={(e) => {
            doUpload(e.target.files);
            e.target.value = "";
          }}
        />
      </div>

      {pending > 0 && (
        <button
          className="border-b border-slate-700 px-3 py-2 text-left text-sky-300"
          onClick={onTransfers}
        >
          {pending} 个上传任务进行中 · 查看队列
        </button>
      )}

      {/* 文件列表 */}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {loading && entries.length === 0 ? (
          <p className="py-6 text-center text-slate-400">加载中…</p>
        ) : entries.length === 0 ? (
          <p className="py-6 text-center text-slate-400">
            空目录(可拖入文件上传)
          </p>
        ) : (
          <table className="w-full">
            <tbody>
              {entries.map((entry) => (
                <tr
                  key={entry.name}
                  onDoubleClick={() => enter(entry)}
                  className="group cursor-default hover:bg-slate-800"
                  title={entry.isDir ? "双击进入目录" : "双击下载"}
                >
                  <td className="w-5 py-1 pl-2 text-slate-400">
                    <Icon
                      name={
                        entry.isLink ? "link" : entry.isDir ? "folder" : "file"
                      }
                      size={14}
                    />
                  </td>
                  <td className="min-w-0 max-w-0 truncate py-1 pr-2 text-slate-200">
                    {entry.name}
                  </td>
                  <td className="whitespace-nowrap py-1 pr-2 text-right text-slate-400">
                    {entry.isDir ? "" : formatSize(entry.size)}
                  </td>
                  <td className="whitespace-nowrap py-1 pr-2 font-mono text-[10px] text-slate-400">
                    {entry.mode}
                  </td>
                  <td className="w-14 whitespace-nowrap py-1 pr-2 text-right">
                    <span className="inline-flex opacity-0 group-hover:opacity-100 group-focus-within:opacity-100">
                      <button
                        onClick={() => rename(entry)}
                        className="px-1 text-slate-400 hover:text-slate-100"
                        title="重命名"
                      >
                        <Icon name="edit" size={14} />
                      </button>
                      <button
                        onClick={() => remove(entry)}
                        className="px-1 text-slate-400 hover:text-red-400"
                        title="删除"
                      >
                        <Icon name="trash" size={14} />
                      </button>
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
