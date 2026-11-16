import { useState } from "react";
import { readPreference, writePreference } from "./preferences";
import { useAppStore } from "../store";
import { Icon } from "../components/Icon";
interface SavedCommand {
  id: string;
  name: string;
  command: string;
}
export function readCommands(): SavedCommand[] {
  const value = readPreference<unknown>("commands", []);
  return Array.isArray(value)
    ? value
        .filter(
          (x): x is SavedCommand =>
            x &&
            typeof x.id === "string" &&
            typeof x.name === "string" &&
            x.id.length > 0 &&
            x.id.length <= 200 &&
            x.name.length > 0 &&
            x.name.length <= 64 &&
            typeof x.command === "string" &&
            x.command.length > 0 &&
            x.command.length <= 4096 &&
            !/[\x00-\x1f\x7f-\x9f\u2028\u2029]/u.test(x.command),
        )
        .slice(0, 100)
    : [];
}
export function CommandLibrary({ onClose }: { onClose(): void }) {
  const st = useAppStore();
  const target = st.sessions.find((s) => s.id === st.activeId);
  const [commands, setCommands] = useState(readCommands);
  const [name, setName] = useState(""),
    [command, setCommand] = useState(""),
    [editing, setEditing] = useState<string | null>(null),
    [notice, setNotice] = useState("");
  function save(next: SavedCommand[]) {
    writePreference("commands", next);
    setCommands(next);
  }
  return (
    <div className="dialog-backdrop">
      <section
        role="dialog"
        aria-modal="true"
        aria-label="常用命令"
        className="dialog-card max-w-2xl"
      >
        <header className="flex items-center justify-between">
          <h2 className="text-lg font-semibold">常用命令</h2>
          <button
            className="icon-button"
            aria-label="关闭常用命令"
            onClick={onClose}
          >
            <Icon name="close" />
          </button>
        </header>
        <div className="my-4 rounded-lg border border-slate-700 p-3 text-xs">
          <span className="text-slate-400">当前目标</span>
          <strong className="ml-2 break-all font-mono text-sky-300">
            {target?.target ?? "尚未选择会话"}
          </strong>
          <p className="mt-2 text-slate-400">
            复制后在目标终端粘贴并检查，再按 Enter
            执行。命令保存在当前浏览器，请勿保存密码。
          </p>
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!name.trim() || !command.trim()) return;
            if (/[\x00-\x1f\x7f-\x9f\u2028\u2029]/u.test(command)) {
              setNotice("请保存单行命令，不要包含换行或控制字符。");
              return;
            }
            if (!editing && commands.length >= 100) {
              setNotice("最多保存 100 条命令，请先删除不需要的条目。");
              return;
            }
            const row = {
              id: editing ?? crypto.randomUUID(),
              name: name.trim(),
              command: command.trim(),
            };
            save(
              editing
                ? commands.map((c) => (c.id === editing ? row : c))
                : [...commands, row],
            );
            setEditing(null);
            setName("");
            setCommand("");
            setNotice("已保存");
          }}
          className="space-y-3"
        >
          <label className="block text-xs text-slate-400">
            命令名称
            <input
              className="form-field mt-1"
              required
              maxLength={64}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="例如：查看磁盘使用"
            />
          </label>
          <label className="block text-xs text-slate-400">
            命令内容
            <input
              className="form-field mt-1 font-mono"
              required
              maxLength={4096}
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="df -h"
              spellCheck={false}
            />
          </label>
          <div className="flex items-center gap-2">
            <button className="button-primary">
              {editing ? "保存修改" : "收藏命令"}
            </button>
            {editing && (
              <button
                type="button"
                className="button-secondary"
                onClick={() => {
                  setEditing(null);
                  setName("");
                  setCommand("");
                }}
              >
                取消编辑
              </button>
            )}
            <span role="status" className="text-xs text-slate-400">
              {notice}
            </span>
          </div>
        </form>
        <div className="mt-5 space-y-2 border-t border-slate-700 pt-4">
          {commands.length === 0 && (
            <p className="text-sm text-slate-400">
              把常用检查命令收藏在这里，下次无需重新输入。
            </p>
          )}
          {commands.map((c) => (
            <article
              key={c.id}
              className="rounded-lg border border-slate-700 p-3"
            >
              <div className="flex items-center gap-2">
                <strong className="min-w-0 flex-1 truncate text-sm">
                  {c.name}
                </strong>
                <button
                  className="toolbar-button"
                  onClick={async () => {
                    try {
                      await navigator.clipboard.writeText(c.command);
                      setNotice(`已复制：${c.name}`);
                    } catch {
                      setNotice("复制未获浏览器允许，请选中下方命令手动复制。");
                    }
                  }}
                >
                  复制
                </button>
                <button
                  className="icon-button"
                  aria-label={`编辑命令 ${c.name}`}
                  onClick={() => {
                    setEditing(c.id);
                    setName(c.name);
                    setCommand(c.command);
                    setNotice("");
                  }}
                >
                  <Icon name="edit" size={15} />
                </button>
                <button
                  className="icon-button"
                  aria-label={`删除命令 ${c.name}`}
                  onClick={() => {
                    if (window.confirm(`删除收藏「${c.name}」？`))
                      save(commands.filter((x) => x.id !== c.id));
                  }}
                >
                  <Icon name="trash" size={15} />
                </button>
              </div>
              <code className="mt-2 block select-text whitespace-pre-wrap break-all text-xs text-slate-300">
                {c.command}
              </code>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}
