export function ResizeHandle({
  axis,
  value,
  onChange,
  min,
  max,
  label,
  reverse = false,
}: {
  axis: "x" | "y";
  value: number;
  onChange: (n: number) => void;
  min: number;
  max: number;
  label: string;
  reverse?: boolean;
}) {
  const clamp = (n: number) => Math.max(min, Math.min(max, n));
  return (
    <div
      role="separator"
      tabIndex={0}
      aria-label={label}
      aria-orientation={axis === "x" ? "vertical" : "horizontal"}
      aria-valuemin={min}
      aria-valuemax={max}
      aria-valuenow={value}
      className={
        axis === "x"
          ? "resize-handle w-1 shrink-0 cursor-col-resize"
          : "resize-handle h-1 shrink-0 cursor-row-resize"
      }
      onKeyDown={(e) => {
        const d =
          e.key === "ArrowRight" || e.key === "ArrowDown"
            ? 1
            : e.key === "ArrowLeft" || e.key === "ArrowUp"
              ? -1
              : 0;
        if (d) {
          e.preventDefault();
          onChange(clamp(value + d * 16 * (reverse ? -1 : 1)));
        }
      }}
      onPointerDown={(e) => {
        e.currentTarget.setPointerCapture(e.pointerId);
        e.currentTarget.dataset.start = String(
          axis === "x" ? e.clientX : e.clientY,
        );
        e.currentTarget.dataset.value = String(value);
      }}
      onPointerMove={(e) => {
        if (!e.currentTarget.hasPointerCapture(e.pointerId)) return;
        onChange(
          clamp(
            Number(e.currentTarget.dataset.value) +
              ((axis === "x" ? e.clientX : e.clientY) -
                Number(e.currentTarget.dataset.start)) *
                (reverse ? -1 : 1),
          ),
        );
      }}
      onPointerUp={(e) => {
        if (e.currentTarget.hasPointerCapture(e.pointerId))
          e.currentTarget.releasePointerCapture(e.pointerId);
      }}
    />
  );
}
