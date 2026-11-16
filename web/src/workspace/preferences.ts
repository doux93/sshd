// Preferences are per browser and origin. Private browsing/storage denial is safe.
export function readPreference<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(`sshx.${key}`);
    return raw === null ? fallback : JSON.parse(raw);
  } catch {
    return fallback;
  }
}
export function writePreference(key: string, value: unknown): void {
  try {
    localStorage.setItem(`sshx.${key}`, JSON.stringify(value));
  } catch {
    /* optional preference */
  }
}
export function stringList(value: unknown): string[] {
  return Array.isArray(value)
    ? [
        ...new Set(value.filter((x): x is string => typeof x === "string")),
      ].slice(0, 1000)
    : [];
}
export function boundedNumber(
  value: unknown,
  fallback: number,
  min: number,
  max: number,
): number {
  return typeof value === "number" && Number.isFinite(value)
    ? Math.min(max, Math.max(min, value))
    : fallback;
}
export function reconcileViews(
  ids: string[],
  active: string | null,
  available: string[],
) {
  const openIds = ids.filter((id) => available.includes(id));
  return {
    openIds,
    activeId:
      active === null
        ? null
        : openIds.includes(active)
          ? active
          : (openIds.at(-1) ?? null),
  };
}
export function closeView(ids: string[], active: string | null, id: string) {
  const index = ids.indexOf(id),
    openIds = ids.filter((x) => x !== id);
  return {
    openIds,
    activeId:
      active === id
        ? (openIds[Math.min(index, openIds.length - 1)] ?? null)
        : active,
  };
}
