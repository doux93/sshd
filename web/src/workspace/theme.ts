import { useSyncExternalStore } from "react";
import { readPreference, writePreference } from "./preferences";
export type Theme = "dark" | "light";
let theme: Theme =
  readPreference<unknown>("theme", "dark") === "light" ? "light" : "dark";
const listeners = new Set<() => void>();
export function setTheme(next: Theme) {
  theme = next;
  writePreference("theme", next);
  document.documentElement.dataset.theme = next;
  document
    .querySelector('meta[name="theme-color"]')
    ?.setAttribute("content", next === "light" ? "#f0f4f9" : "#1b2636");
  for (const listener of listeners) listener();
}
export function initTheme() {
  setTheme(theme);
}
export function useTheme() {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => {
        listeners.delete(fn);
      };
    },
    () => theme,
  );
}
export function terminalTheme(mode: Theme) {
  return mode === "light"
    ? {
        background: "#f8fafc",
        foreground: "#1e293b",
        cursor: "#0369a1",
        selectionBackground: "#bae6fd",
        black: "#334155",
        red: "#b91c1c",
        green: "#15803d",
        yellow: "#a16207",
        blue: "#1d4ed8",
        magenta: "#a21caf",
        cyan: "#0e7490",
        white: "#64748b",
        brightBlack: "#475569",
        brightRed: "#dc2626",
        brightGreen: "#15803d",
        brightYellow: "#a16207",
        brightBlue: "#2563eb",
        brightMagenta: "#a21caf",
        brightCyan: "#0e7490",
        brightWhite: "#0f172a",
      }
    : {
        background: "#17212f",
        foreground: "#e2e8f0",
        cursor: "#7dd3fc",
        selectionBackground: "#334b68",
      };
}
