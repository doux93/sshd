// Global UI state: connection tree, live sessions, dialogs, TOFU flow.
import { create } from "zustand";
import {
  api,
  ApiError,
  connectionError,
  type HostKeyInfo,
  type NodeInfo,
  type SessionInfo,
  type RecoveryInfo,
  type AlertRecord,
} from "./api/client";

import {
  readPreference,
  writePreference,
  stringList,
  reconcileViews,
  closeView,
} from "./workspace/preferences";
const storedViews = readPreference<unknown>("views.v2", null);
const initialViews =
  storedViews && typeof storedViews === "object" && "ids" in storedViews
    ? stringList(storedViews.ids)
    : [];
const initialActive =
  storedViews &&
  typeof storedViews === "object" &&
  "active" in storedViews &&
  typeof storedViews.active === "string"
    ? storedViews.active
    : null;
let viewsInitialized = storedViews !== null;
let sessionRefresh = 0;
let alertRefresh = 0;
function saveViews(ids: string[], active: string | null) {
  writePreference("views.v2", { ids, active });
}
const savedRecent = readPreference<unknown>("recent", {});
const recent: Record<string, number> =
  savedRecent && typeof savedRecent === "object" && !Array.isArray(savedRecent)
    ? Object.fromEntries(
        Object.entries(savedRecent).filter(
          ([, v]) => typeof v === "number" && Number.isFinite(v),
        ),
      )
    : {};

/** Pending host-key confirmation. Retry re-runs the connect with accepts. */
export interface HostKeyPrompt {
  actionLabel?: string;
  hostKey: HostKeyInfo;
  accepts: Record<string, string>; // fingerprints confirmed so far (multi-hop)
  retry: (accepts: Record<string, string>) => Promise<void>;
}

interface AppState {
  recoveries: RecoveryInfo[];
  recoverTerminal(id: string, action: "resume" | "terminate"): Promise<void>;
  sessions: SessionInfo[];
  openIds: string[];
  favorites: string[];
  recent: Record<string, number>;
  focusedNodeId: string | null;
  alerts: AlertRecord[];
  closeView(id: string): void;
  toggleFavorite(id: string): void;
  focusNode(id: string | null): void;
  refreshAlerts(): Promise<void>;
  acknowledgeAlert(id: number): Promise<void>;

  nodes: NodeInfo[];
  activeId: string | null;
  quickConnectOpen: boolean;
  editingNode: { node?: NodeInfo; parentId: string } | null; // undefined node = create
  hostKeyPrompt: HostKeyPrompt | null;
  error: string | null;
  connecting: boolean;
  connectingNodeId: string | null;

  setActive(id: string | null): void;
  setQuickConnectOpen(open: boolean): void;
  setEditingNode(v: { node?: NodeInfo; parentId: string } | null): void;
  setHostKeyPrompt(p: HostKeyPrompt | null): void;
  setError(msg: string | null): void;
  setSessionCwd(id: string, cwd: string): void;

  refresh(): Promise<void>;
  refreshNodes(): Promise<void>;
  markClosed(id: string): void;
  connectNode(nodeId: string): Promise<void>;
  reconnectClosedSession(id: string): Promise<void>;
}

export const useAppStore = create<AppState>((set, get) => ({
  sessions: [],
  recoveries: [],
  openIds: initialViews,
  favorites: stringList(readPreference("favorites", [])),
  recent,
  focusedNodeId: new URL(window.location.href).searchParams.get("node"),
  alerts: [],
  focusNode: (id) => set({ focusedNodeId: id }),
  toggleFavorite: (id) => {
    const favorites = get().favorites.includes(id)
      ? get().favorites.filter((x) => x !== id)
      : [...get().favorites, id];
    writePreference("favorites", favorites);
    set({ favorites });
  },
  closeView: (id) => {
    const next = closeView(get().openIds, get().activeId, id);
    saveViews(next.openIds, next.activeId);
    set(next);
  },
  refreshAlerts: async () => {
    try {
      const generation = ++alertRefresh;
      const alerts = await api.listAlerts();
      if (generation !== alertRefresh) return;
      const current = new Map(
        get().alerts.map((a) => [a.id, a.acknowledgedAt]),
      );
      set({
        alerts: alerts.map((a) => ({
          ...a,
          acknowledgedAt: Math.max(a.acknowledgedAt, current.get(a.id) ?? 0),
        })),
      });
    } catch (e) {
      set({ error: e instanceof Error ? e.message : String(e) });
    }
  },
  acknowledgeAlert: async (id) => {
    await api.acknowledgeAlert(id);
    set({
      alerts: get().alerts.map((a) =>
        a.id === id
          ? { ...a, acknowledgedAt: a.acknowledgedAt || Date.now() }
          : a,
      ),
    });
  },

  nodes: [],
  activeId: initialActive,
  quickConnectOpen: false,
  editingNode: null,
  hostKeyPrompt: null,
  error: null,
  connecting: false,
  connectingNodeId: null,

  setActive: (id) => {
    const openIds =
      id && !get().openIds.includes(id)
        ? [...get().openIds, id]
        : get().openIds;
    saveViews(openIds, id);
    set({ activeId: id, openIds });
  },
  setQuickConnectOpen: (open) => set({ quickConnectOpen: open }),
  setEditingNode: (v) => set({ editingNode: v }),
  setHostKeyPrompt: (p) => set({ hostKeyPrompt: p }),
  setError: (msg) => set({ error: msg }),

  // Update one session's cwd in place. Called by the cwd listener wired up
  // in App.tsx; FilePanel re-reads the new value reactively.
  setSessionCwd: (id, cwd) =>
    set((st) => ({
      sessions: st.sessions.map((s) => (s.id === id ? { ...s, cwd } : s)),
    })),

  refresh: async () => {
    const generation = ++sessionRefresh;
    const [sessions, recoveries] = await Promise.all([
      api.listSessions(),
      api.listRecovery(),
    ]);
    if (generation !== sessionRefresh) return;
    const { activeId, openIds } = get();
    const ids = viewsInitialized
      ? openIds
      : sessions.slice(0, 1).map((s) => s.id);
    const selection = viewsInitialized ? activeId : (ids[0] ?? null);
    viewsInitialized = true;
    const next = reconcileViews(
      ids,
      selection,
      sessions.map((s) => s.id),
    );
    saveViews(next.openIds, next.activeId);
    set({ sessions, recoveries, ...next });
  },

  refreshNodes: async () => {
    set({ nodes: await api.listNodes() });
  },

  markClosed: (id) =>
    set((st) => ({
      sessions: st.sessions.map((s) =>
        s.id === id ? { ...s, state: "closed" } : s,
      ),
    })),

  // Connect to a saved node, driving the TOFU dialog when the server
  // answers 409. `accepts` accumulates across hops of a jump chain.
  connectNode: async (nodeId) => {
    // Prevent double-clicks from stacking concurrent connects.
    if (get().connecting) return;
    set({ connecting: true, connectingNodeId: nodeId, error: null });
    const attempt = async (accepts: Record<string, string>): Promise<void> => {
      try {
        const info = await api.connectNode(nodeId, { accepts });
        await get().refresh();
        get().setActive(info.id);
        const recent = { ...get().recent, [nodeId]: Date.now() };
        writePreference("recent", recent);
        set({
          recent,
          hostKeyPrompt: null,
          connecting: false,
          connectingNodeId: null,
        });
      } catch (err) {
        if (err instanceof ApiError && err.status === 409 && err.hostKey) {
          set({
            hostKeyPrompt: { hostKey: err.hostKey, accepts, retry: attempt },
            connecting: false,
            connectingNodeId: null,
          });
          return;
        }
        set({
          error: connectionError(err),
          connecting: false,
          connectingNodeId: null,
        });
        await get()
          .refresh()
          .catch(() => {});
      }
    };
    await attempt({});
  },

  recoverTerminal: async (id, action) => {
    if (get().connecting) return;
    const rec = get().recoveries.find((r) => r.id === id);
    set({
      connecting: true,
      connectingNodeId: rec?.nodeId ?? null,
      error: null,
    });
    const attempt = async (accepts: Record<string, string>): Promise<void> => {
      set({ connecting: true });
      try {
        const result =
          action === "resume"
            ? await api.resumeRecovery(id, accepts)
            : await api.terminateRecovery(id, accepts);
        await get().refresh();
        if (result) get().setActive(result.id);
        set({ hostKeyPrompt: null, connecting: false, connectingNodeId: null });
      } catch (error) {
        if (
          error instanceof ApiError &&
          error.status === 409 &&
          error.hostKey
        ) {
          set({
            hostKeyPrompt: {
              hostKey: error.hostKey,
              accepts,
              retry: attempt,
              actionLabel:
                action === "resume" ? "信任并恢复" : "信任并结束远端终端",
            },
            connecting: false,
            connectingNodeId: null,
          });
        } else
          set({
            error: connectionError(error),
            hostKeyPrompt: null,
            connecting: false,
            connectingNodeId: null,
          });
      }
    };
    await attempt({});
  },

  // Reconnect a previously-closed session that came from a saved node.
  // Quick-connect sessions (nodeId empty) just disappear — there's nothing
  // to reconnect to.
  reconnectClosedSession: async (id) => {
    const s = get().sessions.find((x) => x.id === id);
    if (!s || !s.nodeId) return;
    if (s.recoveryId) await get().recoverTerminal(s.recoveryId, "resume");
    else await get().connectNode(s.nodeId);
  },
}));
