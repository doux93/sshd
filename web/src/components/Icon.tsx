import type { CSSProperties } from "react";
const paths = {
  sun: "M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0",
  moon: "M20 15A9 9 0 0 1 9 3a9 9 0 1 0 11 12Z",
  split: "M3 4h18v16H3Zm9 0v16",
  copy: "M8 8h13v13H8ZM3 16V3h13",
  file: "M6 3h8l4 4v14H6Zm8 0v5h4M9 12h6m-6 4h6",
  link: "m10 13 4-4m-6 6-2 2a3 3 0 0 1-4-4l5-5a3 3 0 0 1 4 0m2 8a3 3 0 0 0 4 0l5-5a3 3 0 0 0-4-4l-2 2",
  terminal: "m5 7 4 5-4 5m7 0h7",
  folder: "M3 7V5h6l2 2h10v13H3Z",
  server: "M4 3h16v7H4Zm0 11h16v7H4ZM7 6h.01M7 17h.01M11 6h6m-6 11h6",
  search: "M21 21l-5-5M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0",
  plus: "M12 5v14M5 12h14",
  close: "m6 6 12 12M6 18 18 6",
  star: "m12 3 2.8 5.7 6.2.9-4.5 4.4 1.1 6.2-5.6-3-5.6 3 1.1-6.2L3 9.6l6.2-.9Z",
  clock: "M12 8v5l3 2M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0",
  chevron: "m9 5 7 7-7 7",
  edit: "m4 16 12-12 4 4L8 20H4Zm9-9 4 4",
  trash: "M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7m4-7v7",
  monitor: "M3 4h18v13H3Zm4 17h10m-5-4v4M6 12l3-4 4 6 3-5 2 3",
  bell: "M5 17h14l-2-3V9a5 5 0 0 0-10 0v5Zm5 3h4",
  panel: "M3 3h18v18H3Zm5 0v18",
  upload: "M12 16V3m-5 5 5-5 5 5M4 16v5h16v-5",
  refresh:
    "M20 7v5h-5M4 17v-5h5M5 7a8 8 0 0 1 14-2l1 2M4 17l1 2a8 8 0 0 0 14-2",
  logout: "M9 4H4v16h5m5-12 4 4-4 4m-5-4h12",
  check: "m4 12 5 5L20 6",
  sparkles: "m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5Z",
} as const;
export type IconName = keyof typeof paths;
export function Icon({
  name,
  size = 18,
  style,
}: {
  name: IconName;
  size?: number;
  style?: CSSProperties;
}) {
  return (
    <svg
      aria-hidden="true"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      style={style}
    >
      <path d={paths[name]} />
    </svg>
  );
}
