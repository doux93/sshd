import { useSyncExternalStore } from "react";
import { readPreference, writePreference } from "../workspace/preferences";

export type CommandShortcut = "ctrl-shift-k" | "ctrl-k";
const readShortcut = (): CommandShortcut =>
  readPreference<unknown>("ai.shortcut", "ctrl-shift-k") === "ctrl-k"
    ? "ctrl-k"
    : "ctrl-shift-k";
let shortcut = readShortcut();
const listeners = new Set<() => void>();
export const shortcutLabel = (value: CommandShortcut) =>
  value === "ctrl-k" ? "Ctrl+K" : "Ctrl+Shift+K";
export function setCommandShortcut(next: CommandShortcut) {
  shortcut = next;
  writePreference("ai.shortcut", next);
  for (const listener of listeners) listener();
}
export function useCommandShortcut() {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    () => shortcut,
  );
}
export function matchesCommandShortcut(event: KeyboardEvent) {
  return (
    event.type === "keydown" &&
    !event.isComposing &&
    event.ctrlKey &&
    !event.altKey &&
    !event.metaKey &&
    event.code === "KeyK" &&
    event.shiftKey === (shortcut === "ctrl-shift-k")
  );
}
