const { test } = require("node:test"),
  assert = require("node:assert/strict");
const React = require("react"),
  { create, act } = require("react-test-renderer");
const { fixture } = require("./ui-helper.cjs");
const text = (tree) => JSON.stringify(tree.toJSON());
const button = (tree, label) =>
  tree.root.findAllByType("button").find((n) => n.children.join("") === label);
const sessions = [
  { id: "a", label: "first", target: "u@one", state: "active", nodeId: "one" },
  { id: "b", label: "second", target: "u@two", state: "active", nodeId: "two" },
];

test("closing views keeps server sessions and stays closed after refresh/reload", async () => {
  const f = fixture(),
    { api } = f.load("src/api/client.ts"),
    { useAppStore } = f.load("src/store.ts");
  let ends = 0;
  api.closeSession = async () => {
    ends++;
  };
  api.listSessions = async () => sessions;
  await useAppStore.getState().refresh();
  useAppStore.getState().setActive("b");
  useAppStore.getState().closeView("b");
  assert.equal(useAppStore.getState().activeId, "a");
  useAppStore.getState().closeView("a");
  await useAppStore.getState().refresh();
  assert.equal(useAppStore.getState().activeId, null);
  assert.equal(useAppStore.getState().sessions.length, 2);
  assert.equal(ends, 0);
  const next = fixture(f.storage);
  next.load("src/api/client.ts").api.listSessions = async () => sessions;
  const store = next.load("src/store.ts").useAppStore;
  await store.getState().refresh();
  assert.equal(store.getState().openIds.length, 0);
  store.getState().setActive("a");
  assert.equal(store.getState().activeId, "a");
});

test("ending an SSH session requires a separate explicit confirmation", async () => {
  const f = fixture(),
    { api } = f.load("src/api/client.ts"),
    { useAppStore } = f.load("src/store.ts");
  let ends = 0;
  api.closeSession = async (id) => {
    assert.equal(id, "a");
    ends++;
  };
  api.listSessions = async () => [];
  useAppStore.setState({
    sessions: [sessions[0]],
    openIds: ["a"],
    activeId: "a",
  });
  const { SessionShelf } = f.load("src/workspace/SessionShelf.tsx");
  let tree;
  act(() => {
    tree = create(React.createElement(SessionShelf, { onClose() {} }));
  });
  act(() => button(tree, "结束会话").props.onClick());
  assert.equal(ends, 0);
  assert.equal(tree.root.findAllByProps({ role: "alertdialog" }).length, 1);
  await act(async () =>
    tree.root
      .findByProps({ role: "alertdialog" })
      .findAllByType("button")
      .at(-1)
      .props.onClick(),
  );
  assert.equal(ends, 1);
  act(() => tree.unmount());
});

test("host search and favorites operate on saved hosts", () => {
  const f = fixture(),
    { useAppStore } = f.load("src/store.ts");
  useAppStore.setState({
    nodes: [
      {
        id: "one",
        kind: "connection",
        parentId: "",
        name: "database-prod",
        config: { host: "10.0.0.1", user: "db" },
      },
      {
        id: "two",
        kind: "connection",
        parentId: "",
        name: "web-stage",
        config: { host: "10.0.0.2", user: "web" },
      },
    ],
  });
  const { ConnectionTree } = f.load("src/tree/ConnectionTree.tsx");
  let tree;
  act(() => {
    tree = create(React.createElement(ConnectionTree));
  });
  act(() =>
    tree.root
      .findByProps({ "aria-label": "搜索主机" })
      .props.onChange({ target: { value: "10.0.0.1" } }),
  );
  assert.ok(text(tree).includes("database-prod"));
  assert.ok(!text(tree).includes("web-stage"));
  act(() =>
    tree.root
      .findByProps({ "aria-label": "收藏 database-prod" })
      .props.onClick(),
  );
  act(() =>
    tree.root
      .findByProps({ "aria-label": "搜索主机" })
      .props.onChange({ target: { value: "" } }),
  );
  act(() => button(tree, "收藏").props.onClick());
  assert.ok(!text(tree).includes("web-stage"));
  assert.equal(useAppStore.getState().favorites[0], "one");
  act(() => tree.unmount());
});

test("connection test uses unsaved fields without creating or updating a host", async () => {
  const f = fixture(),
    { api } = f.load("src/api/client.ts"),
    { useAppStore } = f.load("src/store.ts");
  const drafts = [];
  let saves = 0;
  api.testConnection = async (req) => {
    drafts.push(req);
    return { target: "tester@new-host", elapsedMs: 12, ok: true };
  };
  api.createNode = api.updateNode = async () => {
    saves++;
  };
  useAppStore.setState({ editingNode: { parentId: "" } });
  const { NodeDialog } = f.load("src/NodeDialog.tsx");
  let tree;
  act(() => {
    tree = create(React.createElement(NodeDialog), {
      createNodeMock: (e) =>
        e.type === "form" ? { reportValidity: () => true } : null,
    });
  });
  act(() =>
    tree.root
      .findByProps({ placeholder: "IP 或域名" })
      .props.onChange({ target: { value: "new-host" } }),
  );
  act(() =>
    tree.root
      .findByProps({ placeholder: "例如：ubuntu" })
      .props.onChange({ target: { value: "tester" } }),
  );
  await act(async () => button(tree, "测试连接").props.onClick());
  assert.equal(drafts[0].config.host, "new-host");
  assert.equal(drafts[0].config.user, "tester");
  assert.equal(saves, 0);
  assert.ok(text(tree).includes("未创建会话或保存配置"));
  act(() => tree.unmount());
});

test("editing a host saves advanced settings and preserves existing alert rules", async () => {
  const f = fixture(),
    { api } = f.load("src/api/client.ts"),
    { useAppStore } = f.load("src/store.ts");
  let saved;
  api.updateNode = async (id, req) => {
    saved = req;
  };
  api.listNodes = async () => [];
  const rule = { metric: "cpuUsage", op: ">", threshold: 85, duration: "5m" };
  useAppStore.setState({
    editingNode: {
      parentId: "",
      node: {
        id: "one",
        name: "db",
        kind: "connection",
        sortOrder: 7,
        config: {
          host: "host",
          port: 22,
          user: "db",
          authType: "password",
          monitor: { enabled: true, interval: 8 },
          alerts: [rule],
          aiPolicy: "readonly",
          hasPassword: true,
        },
      },
    },
  });
  const { NodeDialog } = f.load("src/NodeDialog.tsx");
  let tree;
  act(() => {
    tree = create(React.createElement(NodeDialog));
  });
  act(() => tree.root.findByProps({ "aria-expanded": false }).props.onClick());
  act(() =>
    tree.root
      .findAllByType("select")
      .find((n) => n.props.value === "readonly")
      .props.onChange({ target: { value: "trusted" } }),
  );
  await act(async () =>
    tree.root.findByType("form").props.onSubmit({ preventDefault() {} }),
  );
  assert.equal(saved.config.aiPolicy, "trusted");
  assert.equal(saved.config.monitor.interval, 8);
  assert.equal(saved.sortOrder, 7);
  assert.equal(JSON.stringify(saved.config.alerts[0]), JSON.stringify(rule));
  assert.equal(saved.password, undefined);
  act(() => tree.unmount());
});

test("AI preview displays target, explanation and impact without executing", () => {
  const f = fixture(),
    { CtrlKBar } = f.load("src/ai/CtrlKBar.tsx");
  let tree;
  act(() => {
    tree = create(
      React.createElement(CtrlKBar, {
        open: true,
        loading: false,
        error: "",
        onSubmit() {
          throw Error("unexpected generation");
        },
        onClose() {},
        target: "old-host",
        suggestion: {
          command: "pwd",
          target: "user@production",
          explanation: "show directory",
          impact: "reads directory only",
        },
      }),
    );
  });
  assert.ok(text(tree).includes("user@production"));
  assert.ok(text(tree).includes("show directory"));
  assert.ok(text(tree).includes("reads directory only"));
  assert.equal(tree.root.findByType("textarea").props.readOnly, true);
  assert.equal(
    tree.root.findByProps({ type: "checkbox" }).props.checked,
    false,
  );
  act(() => tree.unmount());
});

test("alert history survives panel remount, links the host and records acknowledgment", async () => {
  const f = fixture(),
    { api } = f.load("src/api/client.ts"),
    { useAppStore } = f.load("src/store.ts");
  let seen,
    record = {
      id: 7,
      nodeId: "one",
      nodeLabel: "production",
      level: "warning",
      message: "CPU high",
      ts: Date.now(),
      acknowledgedAt: 0,
    };
  api.listAlerts = async () => [{ ...record }];
  api.acknowledgeAlert = async (id) => {
    assert.equal(id, 7);
    record = { ...record, acknowledgedAt: Date.now() };
  };
  const { AlertsPanel } = f.load("src/workspace/AlertsPanel.tsx");
  let tree;
  await act(async () => {
    tree = create(
      React.createElement(AlertsPanel, { onNode: (id) => (seen = id) }),
    );
  });
  assert.ok(text(tree).includes("CPU high"));
  act(() => button(tree, "查看主机与会话").props.onClick());
  assert.equal(seen, "one");
  await act(async () => button(tree, "标记已确认").props.onClick());
  assert.ok(useAppStore.getState().alerts[0].acknowledgedAt > 0);
  act(() => tree.unmount());
  await act(async () => {
    tree = create(React.createElement(AlertsPanel, { onNode() {} }));
  });
  assert.ok(text(tree).includes("CPU high"));
  assert.equal(button(tree, "标记已确认"), undefined);
  act(() => tree.unmount());
});

test("saved active view is restored with the workspace", async () => {
  const storage = new Map([
    ["sshx.views.v2", JSON.stringify({ ids: ["a", "b"], active: "a" })],
  ]);
  const f = fixture(storage);
  f.load("src/api/client.ts").api.listSessions = async () => sessions;
  const { useAppStore } = f.load("src/store.ts");
  await useAppStore.getState().refresh();
  assert.equal(useAppStore.getState().activeId, "a");
});

test("notification target filters history by host instead of treating it as a session id", async () => {
  const f = fixture(),
    { api } = f.load("src/api/client.ts");
  const requests = [];
  api.listAlerts = async (before, nodeId) => {
    requests.push(nodeId);
    return [
      {
        id: 4,
        nodeId: nodeId || "other",
        nodeLabel: nodeId ? "target-host" : "other-host",
        level: "warning",
        message: "host event",
        ts: Date.now(),
        acknowledgedAt: 0,
      },
    ];
  };
  const { AlertsPanel } = f.load("src/workspace/AlertsPanel.tsx");
  let tree;
  await act(async () => {
    tree = create(
      React.createElement(AlertsPanel, {
        nodeId: "target-node",
        onNode() {},
        onClearFilter() {},
      }),
    );
  });
  assert.ok(requests.includes("target-node"));
  assert.ok(text(tree).includes("target-host"));
  assert.ok(!text(tree).includes("other-host"));
  act(() => tree.unmount());
});

test("recovering a disconnected persistent terminal uses only the resume API", async () => {
  const f = fixture(),
    { api } = f.load("src/api/client.ts"),
    { useAppStore } = f.load("src/store.ts");
  let resumes = 0;
  api.connectNode = async () => {
    throw Error("must not create another terminal");
  };
  api.resumeRecovery = async (id) => {
    assert.equal(id, "recovery-a");
    resumes++;
    return { id: "resumed" };
  };
  api.listSessions = async () => [
    { ...sessions[0], id: "resumed", recoveryId: "recovery-a" },
  ];
  api.listRecovery = async () => [
    { id: "recovery-a", nodeId: "one", label: "first" },
  ];
  useAppStore.setState({
    sessions: [{ ...sessions[0], state: "closed", recoveryId: "recovery-a" }],
    recoveries: [{ id: "recovery-a", nodeId: "one" }],
  });
  await useAppStore.getState().reconnectClosedSession("a");
  assert.equal(resumes, 1);
  assert.equal(useAppStore.getState().activeId, "resumed");
});

test("recovery missing errors do not fall back to creating a replacement", async () => {
  const f = fixture(),
    { api, ApiError } = f.load("src/api/client.ts"),
    { useAppStore } = f.load("src/store.ts");
  api.resumeRecovery = async () => {
    throw new ApiError(410, "original terminal missing");
  };
  let creates = 0;
  api.connectNode = async () => {
    creates++;
  };
  useAppStore.setState({ recoveries: [{ id: "r", nodeId: "one" }] });
  await useAppStore.getState().recoverTerminal("r", "resume");
  assert.equal(creates, 0);
  assert.match(useAppStore.getState().error, /original terminal missing/);
  assert.equal(useAppStore.getState().connecting, false);
});

test("persistent disconnect is separate from explicitly confirmed remote termination", async () => {
  const f = fixture(),
    { api } = f.load("src/api/client.ts"),
    { useAppStore } = f.load("src/store.ts");
  let disconnects = 0,
    terminations = 0;
  const row = {
    id: "r",
    nodeId: "one",
    label: "first",
    target: "u@one",
    liveSessionId: "a",
  };
  api.closeSession = async () => {
    disconnects++;
  };
  api.terminateRecovery = async () => {
    terminations++;
  };
  api.listSessions = async () => [];
  api.listRecovery = async () => [{ ...row, liveSessionId: undefined }];
  useAppStore.setState({
    sessions: [{ ...sessions[0], recoveryId: "r" }],
    recoveries: [row],
    openIds: ["a"],
  });
  const { SessionShelf } = f.load("src/workspace/SessionShelf.tsx");
  let tree;
  act(() => {
    tree = create(React.createElement(SessionShelf, { onClose() {} }));
  });
  act(() => button(tree, "断开并保留").props.onClick());
  assert.equal(disconnects, 0);
  assert.equal(terminations, 0);
  await act(async () =>
    tree.root
      .findByProps({ role: "alertdialog" })
      .findAllByType("button")
      .at(-1)
      .props.onClick(),
  );
  assert.equal(disconnects, 1);
  assert.equal(terminations, 0);
  act(() => button(tree, "结束远端终端").props.onClick());
  assert.equal(terminations, 0);
  await act(async () => button(tree, "确认结束远端终端").props.onClick());
  assert.equal(terminations, 1);
  act(() => tree.unmount());
});
