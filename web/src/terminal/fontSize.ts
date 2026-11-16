// Terminal font size, shared by every open terminal and persisted across
// restarts. Ctrl+= / Ctrl+- / Ctrl+0 drive it, as in every other terminal.
//
// Kept out of the zustand store on purpose: this is a device preference, not
// session state, and it must survive a reload without a round-trip.

const KEY = "sshx.terminal.fontSize";
const MIN = 8;
const MAX = 32;
export const DEFAULT_FONT_SIZE = 15;

function load(): number {
  const raw = Number(localStorage.getItem(KEY));
  return Number.isFinite(raw) && raw >= MIN && raw <= MAX ? raw : DEFAULT_FONT_SIZE;
}

let current = load();
const listeners = new Set<(size: number) => void>();

export function fontSize(): number {
  return current;
}

export function setFontSize(size: number): void {
  const next = Math.min(MAX, Math.max(MIN, Math.round(size)));
  if (next === current) return;
  current = next;
  try {
    localStorage.setItem(KEY, String(next));
  } catch {
    // Private mode / storage disabled: keep it for this session only.
  }
  for (const l of listeners) l(next);
}

export function adjustFontSize(delta: number): void {
  setFontSize(current + delta);
}

export function resetFontSize(): void {
  setFontSize(DEFAULT_FONT_SIZE);
}

/** Subscribe to changes; returns an unsubscribe function. */
export function onFontSize(l: (size: number) => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}
