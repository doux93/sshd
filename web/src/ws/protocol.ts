// WebSocket protocol v1 client — mirrors internal/server/ws.go.
// Text frames carry JSON control messages; binary frames carry terminal
// bytes prefixed with a 1-byte channel id. One socket multiplexes all
// terminals and auto-reconnects with re-attach.

import { getToken } from "../api/client";

export interface ChannelHandlers {
  onData(data: Uint8Array): void;
  onClosed(): void;
  onReset(): void;
  /**
   * The PTY geometry the terminal must render at. It is not necessarily this
   * client's viewport: another attached client may have negotiated a smaller
   * one, and passive clients never influence it at all.
   */
  onSize(cols: number, rows: number): void;
}

interface ControlMsg {
  type: string;
  sessionId?: string;
  channelId?: number;
  cols?: number;
  rows?: number;
  sizeMode?: SizeMode;
  readOnly?: boolean;
  event?: string;
  message?: string;
  sample?: string | unknown;
  nodeId?: string;
  level?: string;
  cwd?: string;
}

/**
 * Whether this client's viewport constrains the remote PTY.
 * - "negotiate": the PTY is sized to the smallest negotiating viewport.
 * - "passive": never resizes the PTY; render what is there and scroll.
 *   Phones use this so watching a session does not reflow the desktop that
 *   opened it.
 */
export type SizeMode = "negotiate" | "passive";

interface Attachment {
  sessionId: string;
  cols: number;
  rows: number;
  sizeMode: SizeMode;
  readOnly: boolean;
  handlers: ChannelHandlers;
}

export type SessionEventListener = (sessionId: string, event: string) => void;
export type MetricsListener = (sessionId: string, sample: unknown) => void;
export type AlertListener = (
  nodeId: string,
  level: string,
  message: string,
) => void;
export type CwdListener = (sessionId: string, cwd: string) => void;

export type ConnectionStatus =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "disconnected";
export class WsClient {
  private status: ConnectionStatus = "disconnected";
  private statusListeners = new Set<(status: ConnectionStatus) => void>();
  onStatus(fn: (status: ConnectionStatus) => void): () => void {
    this.statusListeners.add(fn);
    fn(this.status);
    return () => this.statusListeners.delete(fn);
  }
  private updateStatus(status: ConnectionStatus) {
    this.status = status;
    for (const fn of this.statusListeners) fn(status);
  }
  private ws: WebSocket | null = null;
  private nextChannelId = 1;
  private attachments = new Map<number, Attachment>();
  private sessionListeners = new Set<SessionEventListener>();
  private metricsListeners = new Set<MetricsListener>();
  private alertListeners = new Set<AlertListener>();
  private cwdListeners = new Set<CwdListener>();
  private reconnectDelay = 500;
  private closedByUser = false;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  connect(): void {
    if (this.ws && this.ws.readyState !== WebSocket.CLOSED) return;
    this.closedByUser = false;
    this.updateStatus(
      this.status === "reconnecting" ? "reconnecting" : "connecting",
    );
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    const tok = getToken();
    // In remote mode the cookie rides the upgrade — no query param
    // needed. In local mode the cookie path doesn't exist so the
    // token MUST travel in the URL.
    const tail = tok ? `?token=${encodeURIComponent(tok)}` : "";
    const ws = new WebSocket(`${proto}://${window.location.host}/ws${tail}`);
    ws.binaryType = "arraybuffer";
    this.ws = ws;

    ws.onopen = () => {
      if (this.ws !== ws || this.closedByUser) return;
      this.reconnectDelay = 500;
      this.updateStatus("connected");
      // Re-attach every terminal after a reconnect; the server replays
      // each ring buffer so the screen restores itself.
      for (const [channelId, att] of this.attachments) {
        this.sendControl({
          type: "attach",
          sessionId: att.sessionId,
          channelId,
          cols: att.cols,
          rows: att.rows,
          sizeMode: att.sizeMode,
          readOnly: att.readOnly,
        });
      }
    };
    ws.onmessage = (ev) => {
      if (this.ws !== ws || this.closedByUser) return;
      if (typeof ev.data === "string") {
        try {
          this.handleControl(JSON.parse(ev.data) as ControlMsg);
        } catch (error) {
          console.warn("Invalid websocket frame", error);
        }
      } else {
        const frame = new Uint8Array(ev.data as ArrayBuffer);
        if (frame.length < 1) return;
        this.attachments.get(frame[0])?.handlers.onData(frame.subarray(1));
      }
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (!this.closedByUser) {
        this.updateStatus("reconnecting");
        this.reconnectTimer = setTimeout(() => {
          this.reconnectTimer = null;
          if (!this.closedByUser) this.connect();
        }, this.reconnectDelay);
        this.reconnectDelay = Math.min(this.reconnectDelay * 2, 10_000);
      }
    };
  }

  disconnect(): void {
    this.closedByUser = true;
    this.updateStatus("disconnected");
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.ws?.close();
    this.ws = null;
  }

  onSessionEvent(l: SessionEventListener): () => void {
    this.sessionListeners.add(l);
    return () => this.sessionListeners.delete(l);
  }

  onMetrics(l: MetricsListener): () => void {
    this.metricsListeners.add(l);
    return () => this.metricsListeners.delete(l);
  }

  onCwd(l: CwdListener): () => void {
    this.cwdListeners.add(l);
    return () => this.cwdListeners.delete(l);
  }

  onAlert(l: AlertListener): () => void {
    this.alertListeners.add(l);
    return () => this.alertListeners.delete(l);
  }

  private handleControl(msg: ControlMsg): void {
    switch (msg.type) {
      case "attached":
        this.attachments.get(msg.channelId ?? -1)?.handlers.onReset();
        break;
      case "channel-reset": {
        const channelId = msg.channelId ?? -1;
        const att = this.attachments.get(channelId);
        if (att)
          this.sendControl({
            type: "attach",
            channelId,
            sessionId: att.sessionId,
            cols: att.cols,
            rows: att.rows,
            sizeMode: att.sizeMode,
            readOnly: att.readOnly,
          });
        break;
      }
      case "channel-closed": {
        const att = this.attachments.get(msg.channelId ?? -1);
        if (att) {
          this.attachments.delete(msg.channelId!);
          att.handlers.onClosed();
        }
        break;
      }
      case "size":
        this.attachments
          .get(msg.channelId ?? -1)
          ?.handlers.onSize(msg.cols ?? 0, msg.rows ?? 0);
        break;
      case "error":
        // The server rejected an attach (typically "session not found"
        // after a reconnect where the session died). Drop the attachment
        // so subsequent output frames don't dangle, and notify the
        // terminal so it can show an honest error instead of "still alive".
        {
          const ch = msg.channelId ?? -1;
          const att = this.attachments.get(ch);
          if (att) {
            this.attachments.delete(ch);
            att.handlers.onClosed();
          }
          console.warn("sshx ws error:", msg.message);
        }
        break;
      case "cwd":
        // OSC 7 update from the remote shell. Push it into the store so
        // the file panel can react.
        for (const l of this.cwdListeners) {
          l(msg.sessionId ?? "", msg.cwd ?? "");
        }
        break;
      case "metrics": {
        if (msg.sample) {
          const sample =
            typeof msg.sample === "string"
              ? JSON.parse(msg.sample)
              : msg.sample;
          if (!sample || typeof sample !== "object") break;
          for (const l of this.metricsListeners) {
            l(msg.sessionId ?? "", sample);
          }
        }
        break;
      }
      case "alert": {
        for (const l of this.alertListeners) {
          l(msg.nodeId ?? "", msg.level ?? "", msg.message ?? "");
        }
        break;
      }
      case "session-event":
        for (const l of this.sessionListeners) {
          l(msg.sessionId ?? "", msg.event ?? "");
        }
        break;
    }
  }

  /** Attach to a session's terminal. Returns the channel id. */
  attach(
    sessionId: string,
    cols: number,
    rows: number,
    handlers: ChannelHandlers,
    sizeMode: SizeMode = "negotiate",
    readOnly = false,
  ): number {
    const channelId = this.allocChannelId();
    readOnly = readOnly || sizeMode === "passive";
    this.attachments.set(channelId, {
      sessionId,
      cols,
      rows,
      sizeMode,
      handlers,
      readOnly,
    });
    this.sendControl({
      type: "attach",
      sessionId,
      channelId,
      cols,
      rows,
      sizeMode,
      readOnly,
    });
    return channelId;
  }

  detach(channelId: number): void {
    if (this.attachments.delete(channelId)) {
      this.sendControl({ type: "detach", channelId });
    }
  }

  sendInput(channelId: number, data: Uint8Array): void {
    if (
      this.ws?.readyState !== WebSocket.OPEN ||
      !this.attachments.has(channelId) ||
      this.attachments.get(channelId)?.readOnly
    )
      return;
    const frame = new Uint8Array(1 + data.length);
    frame[0] = channelId;
    frame.set(data, 1);
    this.ws.send(frame);
  }

  resize(channelId: number, cols: number, rows: number): void {
    const att = this.attachments.get(channelId);
    if (!att) return;
    att.cols = cols;
    att.rows = rows;
    // Passive clients scale locally; the server ignores their resizes, so
    // do not spend a frame telling it.
    if (att.sizeMode === "passive") return;
    this.sendControl({ type: "resize", channelId, cols, rows });
  }

  private allocChannelId(): number {
    for (let i = 0; i < 255; i++) {
      const id = ((this.nextChannelId + i - 1) % 255) + 1;
      if (!this.attachments.has(id)) {
        this.nextChannelId = (id % 255) + 1;
        return id;
      }
    }
    throw new Error("no free terminal channels (max 255)");
  }

  private sendControl(msg: ControlMsg): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }
}

/** App-wide singleton; connected once at startup. */
export const wsClient = new WsClient();
