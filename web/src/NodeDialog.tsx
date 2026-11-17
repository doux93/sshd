import { Modal } from "./components/Modal";
import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import {
  api,
  ApiError,
  connectionError,
  getMode,
  type NodeRequest,
  type AlertRule,
} from "./api/client";
import { useAppStore } from "./store";
import { Icon } from "./components/Icon";
function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="form-label">{label}</span>
      {children}
    </label>
  );
}
export function NodeDialog() {
  const { editingNode, setEditingNode, refreshNodes, nodes, setHostKeyPrompt } =
      useAppStore(),
    existing = editingNode?.node;
  const [name, setName] = useState(""),
    [host, setHost] = useState(""),
    [port, setPort] = useState("22"),
    [user, setUser] = useState("");
  const [authType, setAuthType] = useState<"password" | "key" | "agent">(
      "password",
    ),
    [password, setPassword] = useState(""),
    [keyPath, setKeyPath] = useState(""),
    [passphrase, setPassphrase] = useState("");
  const [persistentTerminal, setPersistentTerminal] = useState(false);
  const [jumpNodeId, setJumpNodeId] = useState(""),
    [proxy, setProxy] = useState(""),
    [monitor, setMonitor] = useState(false),
    [interval, setInterval] = useState("5");
  const [policy, setPolicy] = useState<"readonly" | "confirm" | "trusted">(
      "readonly",
    ),
    [rules, setRules] = useState<AlertRule[]>([]),
    [advanced, setAdvanced] = useState(false);
  const [busy, setBusy] = useState<"save" | "test" | null>(null),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  const form = useRef<HTMLFormElement>(null),
    generation = useRef(0);
  useEffect(() => {
    generation.current++;
    if (!editingNode) return;
    const c = existing?.config;
    setName(existing?.name ?? "");
    setHost(c?.host ?? "");
    setPort(String(c?.port || 22));
    setUser(c?.user ?? "");
    setAuthType(c?.authType ?? "password");
    setPassword("");
    setPassphrase("");
    setKeyPath(c?.keyPath ?? "");
    setJumpNodeId(c?.jumpNodeId ?? "");
    setProxy(c?.proxy ?? "");
    setPersistentTerminal(c?.persistentTerminal ?? false);
    setMonitor(c?.monitor?.enabled ?? false);
    setInterval(String(c?.monitor?.interval ?? 5));
    setPolicy(c?.aiPolicy ?? "readonly");
    setRules(c?.alerts ?? []);
    setAdvanced(false);
    setError("");
    setResult("");
    setBusy(null);
  }, [editingNode]);
  if (!editingNode) return null;
  const isFolder = existing?.kind === "folder";
  function close() {
    generation.current++;
    setHostKeyPrompt(null);
    setEditingNode(null);
  }
  function request(): NodeRequest {
    const req: NodeRequest = {
      parentId: editingNode!.parentId,
      kind: isFolder ? "folder" : "connection",
      name: name.trim() || `${user.trim()}@${host.trim()}`,
      sortOrder: existing?.sortOrder ?? 0,
      config: isFolder
        ? existing!.config
        : {
            ...existing?.config,
            host: host.trim(),
            port: Number(port),
            user: user.trim(),
            authType,
            keyPath: authType === "key" ? keyPath.trim() : "",
            jumpNodeId,
            proxy: proxy.trim() || undefined,
            aiPolicy: policy,
            persistentTerminal,
            monitor: { enabled: monitor, interval: Number(interval) },
            alerts: rules,
          },
    };
    if (authType === "password" && password !== "") req.password = password;
    if (authType === "key" && passphrase !== "") req.passphrase = passphrase;
    return req;
  }
  async function save(e: FormEvent) {
    e.preventDefault();
    const connectAfter =
      (e.nativeEvent as SubmitEvent | undefined)?.submitter?.getAttribute(
        "data-connect",
      ) === "true";
    setBusy("save");
    setError("");
    try {
      const req = request();
      const saved = existing
        ? await api.updateNode(existing.id, req)
        : await api.createNode(req);
      await refreshNodes();
      close();
      if (connectAfter && !isFolder)
        await useAppStore.getState().connectNode(saved.id);
    } catch (e) {
      setError(connectionError(e));
    } finally {
      setBusy(null);
    }
  }
  async function test() {
    if (!form.current?.reportValidity()) return;
    const current = ++generation.current,
      draft = request();
    setResult("");
    setError("");
    const attempt = async (accepts: Record<string, string>): Promise<void> => {
      if (current !== generation.current) return;
      setBusy("test");
      try {
        const r = await api.testConnection({
          ...draft,
          nodeId: existing?.id,
          accepts,
        });
        if (current !== generation.current) return;
        setHostKeyPrompt(null);
        setResult(
          `认证成功 · ${r.target} · ${r.elapsedMs} ms。测试连接已关闭，未创建会话或保存配置。`,
        );
      } catch (e) {
        if (current !== generation.current) return;
        if (e instanceof ApiError && e.status === 409 && e.hostKey) {
          setHostKeyPrompt({
            hostKey: e.hostKey,
            accepts,
            retry: attempt,
            actionLabel: "信任并测试",
          });
        } else {
          setHostKeyPrompt(null);
          setError(connectionError(e));
        }
      } finally {
        if (current === generation.current) setBusy(null);
      }
    };
    await attempt({});
  }
  const input = "form-field";
  return (
    <Modal
      labelledBy="node-title"
      className="max-w-xl"
      onClose={close}
      busy={busy === "save"}
    >
      <form
        ref={form}
        onSubmit={save}
        onChange={() => {
          generation.current++;
          setResult("");
          setHostKeyPrompt(null);
        }}
      >
        <div className="mb-5 flex items-start justify-between">
          <div>
            <h2 id="node-title" className="text-xl font-semibold">
              {isFolder ? "编辑文件夹" : existing ? "编辑主机" : "新建主机"}
            </h2>
            <p className="mt-1 text-xs text-slate-400">
              {isFolder
                ? "整理保存的主机。"
                : "先填写连接信息，高级选项可稍后调整。"}
            </p>
          </div>
          <button
            type="button"
            className="icon-button"
            onClick={close}
            disabled={busy === "save"}
            aria-label="关闭连接设置"
          >
            <Icon name="close" />
          </button>
        </div>
        <fieldset disabled={!!busy} className="space-y-4">
          <Field label={isFolder ? "文件夹名称" : "名称（选填）"}>
            <input
              className={input}
              value={name}
              required={isFolder}
              autoFocus
              onChange={(e) => setName(e.target.value)}
              placeholder="例如：生产数据库"
            />
          </Field>
          {!isFolder && (
            <>
              <div className="grid grid-cols-[1fr_6rem] gap-3">
                <Field label="主机地址">
                  <input
                    className={input}
                    required
                    value={host}
                    onChange={(e) => setHost(e.target.value)}
                    placeholder="IP 或域名"
                  />
                </Field>
                <Field label="端口">
                  <input
                    className={input}
                    required
                    type="number"
                    min={1}
                    max={65535}
                    value={port}
                    onChange={(e) => setPort(e.target.value)}
                  />
                </Field>
              </div>
              <Field label="登录用户">
                <input
                  className={input}
                  required
                  value={user}
                  onChange={(e) => setUser(e.target.value)}
                  placeholder="例如：ubuntu"
                  autoComplete="username"
                />
              </Field>
              <Field label="认证方式">
                <select
                  className={input}
                  value={authType}
                  onChange={(e) =>
                    setAuthType(e.target.value as typeof authType)
                  }
                >
                  <option value="password">密码</option>
                  <option value="key">私钥文件</option>
                  <option value="agent">SSH Agent</option>
                </select>
              </Field>
              {authType === "password" && (
                <Field
                  label={
                    existing?.config.hasPassword
                      ? "密码（已保存，留空保持不变）"
                      : "密码"
                  }
                >
                  <input
                    className={input}
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    autoComplete="new-password"
                  />
                </Field>
              )}
              {authType === "key" && (
                <>
                  <Field label="私钥路径">
                    <input
                      className={input}
                      required
                      value={keyPath}
                      onChange={(e) => setKeyPath(e.target.value)}
                      placeholder="例如：/home/user/.ssh/id_ed25519"
                    />
                  </Field>
                  <p className="text-xs leading-5 text-slate-400">
                    {getMode() === "remote"
                      ? "路径位于运行 sshx 的服务器，不是当前浏览器所在电脑。"
                      : "路径位于运行 sshx 的本机。"}
                  </p>
                  <Field
                    label={
                      existing?.config.hasPassphrase
                        ? "私钥口令（已保存，留空保持不变）"
                        : "私钥口令（可选）"
                    }
                  >
                    <input
                      className={input}
                      type="password"
                      value={passphrase}
                      onChange={(e) => setPassphrase(e.target.value)}
                    />
                  </Field>
                </>
              )}
              {authType === "agent" && (
                <p className="text-xs text-slate-400">
                  使用运行 sshx 的机器上可用的 SSH Agent。
                </p>
              )}
              <button
                type="button"
                className="flex w-full items-center justify-between border-t border-slate-700 pt-4 text-sm text-sky-300"
                aria-expanded={advanced}
                onClick={() => setAdvanced(!advanced)}
              >
                高级设置<span>{advanced ? "收起" : "展开"}</span>
              </button>
              {advanced && (
                <div className="space-y-4 rounded-xl border border-slate-800 bg-slate-950/40 p-4">
                  <label className="flex gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={persistentTerminal}
                      onChange={(e) => setPersistentTerminal(e.target.checked)}
                    />
                    终端保活与恢复（tmux）
                  </label>
                  <p className="text-xs leading-5 text-slate-400">
                    每台主机一个保活终端。远端需预装
                    tmux；工作台重启后从会话库恢复，远端主机重启后不能恢复。开启后不会注入
                    Shell 目录跟随脚本。
                  </p>
                  <Field label="跳板机">
                    <select
                      className={input}
                      value={jumpNodeId}
                      onChange={(e) => setJumpNodeId(e.target.value)}
                    >
                      <option value="">直接连接</option>
                      {nodes
                        .filter(
                          (n) =>
                            n.kind === "connection" && n.id !== existing?.id,
                        )
                        .map((n) => (
                          <option key={n.id} value={n.id}>
                            {n.name}
                          </option>
                        ))}
                    </select>
                  </Field>
                  <Field label="SOCKS5 代理（相对于服务运行机器）">
                    <input
                      className={input}
                      value={proxy}
                      onChange={(e) => setProxy(e.target.value)}
                      placeholder="例如：127.0.0.1:1080"
                    />
                  </Field>
                  <label className="flex gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={monitor}
                      onChange={(e) => setMonitor(e.target.checked)}
                    />
                    启用主机监控
                  </label>
                  {monitor && (
                    <>
                      <Field label="采集间隔（秒）">
                        <input
                          className={input}
                          type="number"
                          required
                          min={2}
                          max={3600}
                          value={interval}
                          onChange={(e) => setInterval(e.target.value)}
                        />
                      </Field>
                      <div className="flex justify-between text-xs text-slate-400">
                        <span>告警规则</span>
                        <button
                          type="button"
                          className="text-sky-300"
                          onClick={() =>
                            setRules([
                              ...rules,
                              {
                                metric: "cpuUsage",
                                op: ">",
                                threshold: 80,
                                duration: "1m",
                              },
                            ])
                          }
                        >
                          添加规则
                        </button>
                      </div>
                      {rules.map((r, i) => (
                        <div
                          key={i}
                          className="grid grid-cols-[1fr_4rem_4.5rem_5rem_1.5rem] gap-1"
                        >
                          <select
                            aria-label="告警指标"
                            className={input + " !px-1"}
                            value={r.metric}
                            onChange={(e) =>
                              setRules(
                                rules.map((x, j) =>
                                  j === i
                                    ? {
                                        ...x,
                                        metric: e.target
                                          .value as AlertRule["metric"],
                                      }
                                    : x,
                                ),
                              )
                            }
                          >
                            <option value="cpuUsage">CPU %</option>
                            <option value="memUsage">内存 %</option>
                            <option value="disk">磁盘 %</option>
                            <option value="load1">负载</option>
                          </select>
                          <select
                            aria-label="比较条件"
                            className={input + " !px-1"}
                            value={r.op}
                            onChange={(e) =>
                              setRules(
                                rules.map((x, j) =>
                                  j === i
                                    ? {
                                        ...x,
                                        op: e.target.value as AlertRule["op"],
                                      }
                                    : x,
                                ),
                              )
                            }
                          >
                            {[">", ">=", "<", "<="].map((op) => (
                              <option key={op}>{op}</option>
                            ))}
                          </select>
                          <input
                            aria-label="阈值"
                            className={input + " !px-1"}
                            type="number"
                            step="any"
                            required
                            min={0}
                            value={r.threshold}
                            onChange={(e) =>
                              setRules(
                                rules.map((x, j) =>
                                  j === i
                                    ? {
                                        ...x,
                                        threshold: Number(e.target.value),
                                      }
                                    : x,
                                ),
                              )
                            }
                          />
                          <select
                            aria-label="持续时间"
                            className={input + " !px-1"}
                            value={r.duration || "0s"}
                            onChange={(e) =>
                              setRules(
                                rules.map((x, j) =>
                                  j === i
                                    ? { ...x, duration: e.target.value }
                                    : x,
                                ),
                              )
                            }
                          >
                            {[
                              ...new Set([
                                "0s",
                                "30s",
                                "1m",
                                "5m",
                                r.duration || "0s",
                              ]),
                            ].map((t) => (
                              <option key={t} value={t}>
                                {t === "0s" ? "立即" : t}
                              </option>
                            ))}
                          </select>
                          <button
                            type="button"
                            aria-label="删除告警规则"
                            onClick={() =>
                              setRules(rules.filter((_, j) => j !== i))
                            }
                          >
                            <Icon name="close" size={14} />
                          </button>
                        </div>
                      ))}
                      <p className="text-[11px] text-slate-500">
                        修改监控和规则后，重新连接主机生效。
                      </p>
                    </>
                  )}
                  <Field label="AI 工具权限">
                    <select
                      className={input}
                      value={policy}
                      onChange={(e) =>
                        setPolicy(e.target.value as typeof policy)
                      }
                    >
                      <option value="readonly">固定只读工具（默认）</option>
                      <option value="confirm">写操作转人工终端</option>
                      <option value="trusted">允许任意命令（受信任）</option>
                    </select>
                  </Field>
                </div>
              )}
            </>
          )}
        </fieldset>
        {error && (
          <p
            role="alert"
            className="mt-4 whitespace-pre-wrap rounded-lg bg-rose-950/50 p-3 text-sm text-rose-300"
          >
            {error}
          </p>
        )}
        {result && (
          <p
            role="status"
            className="mt-4 rounded-lg bg-emerald-950/40 p-3 text-sm text-emerald-300"
          >
            {result}
          </p>
        )}
        <div className="mt-6 flex flex-wrap items-center gap-2">
          {!isFolder && (
            <button
              type="button"
              disabled={!!busy}
              className="button-secondary"
              onClick={test}
            >
              {busy === "test" ? "测试中…" : "测试连接"}
            </button>
          )}
          <button
            type="button"
            disabled={busy === "save"}
            className="ml-auto button-secondary"
            onClick={close}
          >
            取消
          </button>
          {!isFolder && (
            <button
              type="submit"
              data-connect="true"
              disabled={!!busy}
              className="button-primary"
            >
              保存并连接
            </button>
          )}
          <button type="submit" disabled={!!busy} className="button-secondary">
            {busy === "save" ? "保存中…" : isFolder ? "保存文件夹" : "保存主机"}
          </button>
        </div>
      </form>
    </Modal>
  );
}
