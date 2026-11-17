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
    dialog.querySelector<HTMLElement>("[data-autofocus]")?.focus();
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
      onKeyDown={(event) => {
        if (event.key !== "Tab" || event.nativeEvent.isComposing) return;
        const dialog = event.currentTarget;
        if ((event.target as Element).closest("dialog") !== dialog) return;
        const controls = Array.from(
          dialog.querySelectorAll<HTMLElement>(
            "button, input, select, textarea, a[href], [tabindex]",
          ),
        ).filter(
          (element) =>
            element.tabIndex >= 0 &&
            !element.matches(":disabled") &&
            element.getClientRects().length > 0,
        );
        const first = controls[0],
          last = controls.at(-1);
        if (!first || !last) {
          event.preventDefault();
          return;
        }
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onClose();
      }}
    >
      {children}
    </dialog>
  );
}
