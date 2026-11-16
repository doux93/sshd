import { useEffect, useRef, useState, type ReactNode } from "react";

export interface ContextMenuItem {
  label: string;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
  separator?: boolean;
}

interface ContextMenuProps {
  x: number;
  y: number;
  items: ContextMenuItem[];
  onClose: () => void;
}

export function ContextMenu({ x, y, items, onClose }: ContextMenuProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ x, y });

  // Reposition if the menu would overflow the viewport.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const pad = 8;
    let nx = x,
      ny = y;
    if (nx + rect.width + pad > window.innerWidth)
      nx = window.innerWidth - rect.width - pad;
    if (ny + rect.height + pad > window.innerHeight)
      ny = window.innerHeight - rect.height - pad;
    if (nx < pad) nx = pad;
    if (ny < pad) ny = pad;
    setPos({ x: nx, y: ny });
  }, [x, y]);

  // Close on outside click, scroll, or Escape.
  useEffect(() => {
    const down = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose();
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("mousedown", down);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", down);
      document.removeEventListener("keydown", key);
    };
  }, [onClose]);

  return (
    <div
      ref={ref}
      style={{ position: "fixed", left: pos.x, top: pos.y, zIndex: 70 }}
      className="min-w-44 rounded-md border border-slate-600 bg-slate-800 py-1 shadow-2xl"
      onContextMenu={(e) => e.preventDefault()}
    >
      {items.map((item, i) =>
        item.separator ? (
          <div key={i} className="my-1 h-px bg-slate-700" />
        ) : (
          <button
            key={i}
            disabled={item.disabled}
            onClick={() => {
              if (item.disabled) return;
              onClose();
              item.onClick();
            }}
            className={`flex w-full items-center px-3 py-1.5 text-left text-sm hover:bg-slate-700 disabled:opacity-40 disabled:hover:bg-transparent ${
              item.danger ? "text-red-400" : "text-slate-100"
            }`}
          >
            {item.label}
          </button>
        ),
      )}
    </div>
  );
}

// Convenient wrapper: parent holds whether the menu is open, and where.
interface ContextMenuPropsExternal {
  x: number;
  y: number;
  items: ContextMenuItem[];
  onClose: () => void;
}

export function ContextMenuHost({
  menu,
  children,
}: {
  menu: ContextMenuPropsExternal | null;
  children: ReactNode;
}) {
  return (
    <>
      {children}
      {menu && <ContextMenu {...menu} />}
    </>
  );
}
