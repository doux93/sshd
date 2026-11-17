import { useLayoutEffect, useRef, type ReactNode } from "react";

// Native modal dialogs keep keyboard focus inside the topmost dialog and
// make the background inert, including when a host-key prompt opens on top
// of a connection form. Closing restores focus to the initiating control.
export function Modal({
  children,
  onClose,
  label,
  labelledBy,
  className = "max-w-lg",
  busy = false,
  kind = "dialog",
}: {
  children: ReactNode;
  onClose(): void;
  label?: string;
  labelledBy?: string;
  className?: string;
  busy?: boolean;
  kind?: "dialog" | "alertdialog";
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const dialog = ref.current!;
    dialog.showModal();
    return () => dialog.close();
  }, []);
  return (
    <dialog
      ref={ref}
      role={kind}
      aria-modal="true"
      aria-label={label}
      aria-labelledby={labelledBy}
      className={`modal-card ${className}`}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onClose();
      }}
    >
      {children}
    </dialog>
  );
}
