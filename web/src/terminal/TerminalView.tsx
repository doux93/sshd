// xterm.js wrapper bound to one session via the shared WebSocket.
// Mount = attach (server replays the ring buffer), unmount = detach
// (the server-side session keeps running).
import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { WebglAddon } from "@xterm/addon-webgl";
import "@xterm/xterm/css/xterm.css";
import { wsClient, type SizeMode } from "../ws/protocol";
import { useAppStore } from "../store";
import {
  fontSize,
  adjustFontSize,
  resetFontSize,
  onFontSize,
} from "./fontSize";
import { api, type CommandSuggestion } from "../api/client";
import { useTheme, terminalTheme } from "../workspace/theme";
import { CtrlKBar } from "../ai/CtrlKBar";

const encoder = new TextEncoder();

export function TerminalView({
  sessionId,
  sizeMode,
  nodeId,
  readOnly: requestedReadOnly = false,
  focusOnMount = true,
}: {
  sessionId: string;
  nodeId?: string;
  readOnly?: boolean;
  focusOnMount?: boolean;
  /**
   * "passive" makes this view render at the PTY's geometry instead of its
   * own, so it never reflows a terminal another client is using. The mobile
   * layout (M9) passes it explicitly; do not infer it from a media query —
   * touchscreen desktops report a coarse pointer and would be misdetected.
   */
  sizeMode?: SizeMode;
}) {
  const theme = useTheme();
  const termRef = useRef<Terminal | null>(null);
  useEffect(() => {
    if (termRef.current) termRef.current.options.theme = terminalTheme(theme);
  }, [theme]);
  useEffect(() => {
    if (focusOnMount && !requestedReadOnly) termRef.current?.focus();
  }, [focusOnMount, requestedReadOnly]);
  const containerRef = useRef<HTMLDivElement>(null);
  const mode = sizeMode ?? "negotiate";
  const readOnly = requestedReadOnly || mode === "passive";
  const target = useAppStore(
    (st) => st.sessions.find((s) => s.id === sessionId)?.target ?? "",
  );

  const [ctrlkOpen, setCtrlkOpen] = useState(false);
  const [ctrlkLoading, setCtrlkLoading] = useState(false);
  const [ctrlkError, setCtrlkError] = useState("");
  const [suggestion, setSuggestion] = useState<CommandSuggestion | null>(null);
  const requestGeneration = useRef(0);

  const closeCtrlK = () => {
    requestGeneration.current++;
    setCtrlkLoading(false);
    setCtrlkOpen(false);
  };
  useEffect(
    () => () => {
      requestGeneration.current++;
    },
    [sessionId],
  );
  const runCtrlK = async (prompt: string, includeContext: boolean) => {
    if (readOnly) return;
    if (!nodeId) {
      setCtrlkError("当前会话没有关联节点，无法生成命令");
      return;
    }
    const generation = ++requestGeneration.current;
    setCtrlkLoading(true);
    setCtrlkError("");
    setSuggestion(null);
    try {
      const result = await api.ctrlk(prompt, nodeId, sessionId, includeContext);
      const { command } = result;
      if (generation !== requestGeneration.current) return;
      if (!command || /[\x00-\x1f\x7f-\x9f\u2028\u2029]/u.test(command))
        throw new Error("生成结果包含换行或控制字符，请重试");
      setSuggestion(result);
    } catch (e) {
      if (generation === requestGeneration.current)
        setCtrlkError(e instanceof Error ? e.message : String(e));
    } finally {
      if (generation === requestGeneration.current) setCtrlkLoading(false);
    }
  };

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const term = new Terminal({
      cursorBlink: !readOnly,
      disableStdin: readOnly,
      allowProposedApi: true,
      fontSize: fontSize(),
      fontFamily: '"Cascadia Mono", Consolas, "Courier New", monospace',
      scrollback: 10_000,
      theme: terminalTheme(theme),
    });
    termRef.current = term;
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.loadAddon(new Unicode11Addon());
    term.unicode.activeVersion = "11";
    term.open(container);
    // Passive/mobile viewers favor reliable text rendering over GPU throughput.
    // WebGL can render blank at high DPR even after replay bytes have arrived.
    if (mode !== "passive") {
      let webgl: WebglAddon | undefined;
      try {
        webgl = new WebglAddon();
        const addon = webgl;
        addon.onContextLoss(() => addon.dispose());
        term.loadAddon(addon);
      } catch {
        webgl?.dispose(); // Retain xterm's DOM renderer when WebGL is unavailable.
      }
    }
    // A passive client renders at whatever geometry the PTY already has, so
    // it must not fit to its own container — it waits for the server's size.
    if (mode === "negotiate") fit.fit();

    const channelId = wsClient.attach(
      sessionId,
      term.cols,
      term.rows,
      {
        onData: (data) => term.write(data),
        onReset: () => term.write("\x1bc"),
        onClosed: () => {
          // Only mark the tab "closed" — don't write "[会话已结束]" into
          // the terminal. The closed tab shows a grey dot + tab text;
          // writing red text into the terminal leaves it visible after a
          // later reattach (or a new terminal mounted for this tab).
          useAppStore.getState().markClosed(sessionId);
        },
        onSize: (cols, rows) => {
          // Server-authoritative: another client may hold the PTY smaller
          // than this viewport. Rendering at our own size would misplace
          // every wrapped line.
          if (
            cols > 0 &&
            rows > 0 &&
            (cols !== term.cols || rows !== term.rows)
          ) {
            term.resize(cols, rows);
          }
        },
      },
      mode,
      readOnly,
    );

    const onData = term.onData(
      (s) => !readOnly && wsClient.sendInput(channelId, encoder.encode(s)),
    );
    const onBinary = term.onBinary((s) => {
      const bytes = new Uint8Array(s.length);
      for (let i = 0; i < s.length; i++) bytes[i] = s.charCodeAt(i) & 0xff;
      if (!readOnly) wsClient.sendInput(channelId, bytes);
    });

    // Passive clients keep the PTY geometry and let the container scroll,
    // so there is nothing to observe.
    const ro =
      mode === "negotiate"
        ? new ResizeObserver(() => {
            fit.fit();
            wsClient.resize(channelId, term.cols, term.rows);
          })
        : null;
    ro?.observe(container);

    // Font size is a device preference shared by every open terminal, so a
    // change here reflows this one too even if it was made in another tab.
    const refit = () => {
      if (mode !== "negotiate") return;
      fit.fit();
      wsClient.resize(channelId, term.cols, term.rows);
    };
    const unsubFont = onFontSize((size) => {
      term.options.fontSize = size;
      refit();
    });
    // Ctrl+= / Ctrl+- / Ctrl+0, intercepted before the bytes reach the shell.
    // preventDefault also stops the browser's own zoom on the same chord.
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== "keydown" || !e.ctrlKey || e.altKey || e.metaKey)
        return true;
      switch (e.code) {
        case "Equal":
          e.preventDefault();
          adjustFontSize(1);
          return false;
        case "Minus":
          e.preventDefault();
          adjustFontSize(-1);
          return false;
        case "Digit0":
          e.preventDefault();
          resetFontSize();
          return false;
        case "KeyK":
          if (readOnly) return false;
          // Open the command-generation bar instead of sending Ctrl-K
          // (0x0b) to the shell.
          e.preventDefault();
          setSuggestion(null);
          setCtrlkError("");
          setCtrlkOpen(true);
          return false;
        default:
          return true;
      }
    });

    if (!readOnly && focusOnMount) term.focus();

    return () => {
      ro?.disconnect();
      unsubFont();
      onData.dispose();
      onBinary.dispose();
      wsClient.detach(channelId);
      termRef.current = null;
      term.dispose();
    };
  }, [sessionId, mode, readOnly]);

  return (
    <div className="relative h-full w-full">
      <CtrlKBar
        open={ctrlkOpen}
        loading={ctrlkLoading}
        error={ctrlkError}
        onSubmit={runCtrlK}
        onClose={closeCtrlK}
        suggestion={suggestion}
        target={target}
      />
      <div
        ref={containerRef}
        className={
          mode === "passive" ? "h-full w-full overflow-auto" : "h-full w-full"
        }
      />
    </div>
  );
}
