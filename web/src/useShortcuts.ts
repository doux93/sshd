// Global keyboard shortcuts for session switching.
//
// Chord choice is constrained by the browser, not by taste: Ctrl+1..9,
// Ctrl+W/T and Ctrl+PageUp/Down are reserved for tab switching and closing,
// so a page cannot reliably intercept them. Alt+digit and Ctrl+Shift+bracket
// are free, and neither is commonly needed by a remote shell.
//
// Listeners run in the capture phase so they fire before xterm consumes the
// keystroke; preventDefault stops the bytes from reaching the shell.
import { useEffect } from "react";
import { useAppStore } from "./store";

export function useShortcuts(): void {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      // Never steal keys while a dialog field has focus — but xterm receives
      // input through a hidden textarea ("xterm-helper-textarea"), which is
      // the normal state while using the app. Excluding all textareas would
      // disable these shortcuts exactly when they are wanted.
      const el = document.activeElement;
      const isTerminalInput =
        el instanceof HTMLTextAreaElement &&
        el.classList.contains("xterm-helper-textarea");
      if (
        !isTerminalInput &&
        (el instanceof HTMLInputElement ||
          el instanceof HTMLTextAreaElement ||
          el instanceof HTMLSelectElement)
      ) {
        return;
      }

      const {
        sessions: allSessions,
        openIds,
        activeId,
        setActive,
      } = useAppStore.getState();
      const sessions = openIds
        .map((id) => allSessions.find((s) => s.id === id))
        .filter((s): s is NonNullable<typeof s> => !!s);
      if (sessions.length === 0) return;

      // Alt+1..9 — jump to the Nth session (9 = last, as in most tab UIs).
      if (e.altKey && !e.ctrlKey && !e.metaKey && /^Digit[1-9]$/.test(e.code)) {
        const n = Number(e.code.slice(5));
        const target =
          n === 9 ? sessions[sessions.length - 1] : sessions[n - 1];
        if (target) {
          e.preventDefault();
          setActive(target.id);
        }
        return;
      }

      // Ctrl+Shift+[ / ] — previous / next session, wrapping around.
      if (e.ctrlKey && e.shiftKey && !e.altKey) {
        const step =
          e.code === "BracketRight" ? 1 : e.code === "BracketLeft" ? -1 : 0;
        if (step === 0) return;
        e.preventDefault();
        const i = sessions.findIndex((s) => s.id === activeId);
        const next = (i + step + sessions.length) % sessions.length;
        setActive(sessions[next].id);
      }
    };

    window.addEventListener("keydown", handler, true);
    return () => window.removeEventListener("keydown", handler, true);
  }, []);
}
