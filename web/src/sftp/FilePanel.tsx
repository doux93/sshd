// SFTP file panel. Follows the terminal's directory (OSC 7) when
// "跟随终端" is on — the FinalShell behaviour, done with real shell
// integration instead of guesswork.
import { useCallback, useEffect, useRef, useState } from "react";
import { api, type FileEntry } from "../api/client";
import { useAppStore } from "../store";
import { Icon } from "../components/Icon";

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[i]}`;
}

function parentOf(p: string): string {
  const trimmed = p.replace(/\/+$/, "");
  const idx = trimmed.lastIndexOf("/");
  if (idx <= 0) return "/";
  return trimmed.slice(0, idx);
}

export function FilePanel({ sessionId }: { sessionId: string }) {
  const terminalCwd = useAppStore(
    (s) => s.sessions.find((x) => x.id === sessionId)?.cwd ?? "",
  );
  const setError = useAppStore((s) => s.setError);

  const [path, setPath] = useState(".");
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [follow, setFollow] = useState(true);
  const [upload, setUpload] = useState<{ name: string; pct: number } | null>(
    null,
  );
  const fileInputRef = useRef<HTMLInputElement>(null);

  const load = useCallback(
    async (target: string) => {
      setLoading(true);
      try {
        const res = await api.listFiles(sessionId, target);
        setPath(res.path);
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
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setLoading(false);
      }
    },
    [sessionId, setError],
  );

  // Initial listing (remote home directory).
  useEffect(() => {
    load(".");
  }, [load]);

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

  const doUpload = async (files: FileList | null) => {
    if (!files?.length) return;
    for (const file of Array.from(files)) {
      setUpload({ name: file.name, pct: 0 });
      try {
        await api.uploadFile(
          sessionId,
          `${path.replace(/\/+$/, "")}/${file.name}`,
          file,
          (loaded, total) => {
            // Empty or unknown-size files: 0/0 -> NaN. Show 0% until done
            // then jump to 100%, otherwise the progress bar shows "NaN%".
            const pct = total > 0 ? (loaded / total) * 100 : 0;
            setUpload({ name: file.name, pct });
          },
        );
        if (file.size === 0) setUpload({ name: file.name, pct: 100 });
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      }
    }
    setUpload(null);
    load(path);
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
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && load(path)}
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

      {/* 上传进度 */}
      {upload && (
        <div className="border-b border-slate-700 px-2 py-1.5">
          <div className="mb-1 flex justify-between text-slate-300">
            <span className="truncate">上传 {upload.name}</span>
            <span>{upload.pct.toFixed(0)}%</span>
          </div>
          <div className="h-1 overflow-hidden rounded bg-slate-700">
            <div
              className="h-full bg-blue-500 transition-[width]"
              style={{ width: `${upload.pct}%` }}
            />
          </div>
        </div>
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
