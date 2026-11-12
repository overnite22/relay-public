import { X } from "lucide-react";
import { useEffect } from "react";

export function Modal({
  title,
  icon,
  onClose,
  children,
  footer,
  width = 560,
  height,
}: {
  title: string;
  icon?: React.ReactNode;
  onClose: () => void;
  children: React.ReactNode;
  footer?: React.ReactNode;
  width?: number;
  height?: number;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" style={{ width, height }} role="dialog" aria-label={title}>
        <div className="modal-title">
          {icon}
          <span>{title}</span>
          <button className="icon-btn" onClick={onClose} title="Fermer">
            <X size={16} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-footer">{footer}</div>}
      </div>
    </div>
  );
}

export function Confirm({
  title,
  message,
  danger,
  confirmLabel = "Confirmer",
  onConfirm,
  onClose,
}: {
  title: string;
  message: React.ReactNode;
  danger?: boolean;
  confirmLabel?: string;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Modal
      title={title}
      onClose={onClose}
      width={440}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Annuler
          </button>
          <button
            className={`btn ${danger ? "danger" : "primary"}`}
            autoFocus
            onClick={() => {
              onConfirm();
              onClose();
            }}
          >
            {confirmLabel}
          </button>
        </>
      }
    >
      <div className="confirm-msg">{message}</div>
    </Modal>
  );
}

export function Prompt({
  title,
  label,
  initial = "",
  onSubmit,
  onClose,
}: {
  title: string;
  label: string;
  initial?: string;
  onSubmit: (v: string) => void;
  onClose: () => void;
}) {
  return (
    <Modal title={title} onClose={onClose} width={420}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const v = new FormData(e.currentTarget).get("v")?.toString().trim() ?? "";
          if (v) onSubmit(v);
        }}
      >
        <label className="field">
          <span>{label}</span>
          <input name="v" defaultValue={initial} autoFocus required />
        </label>
        <div className="modal-footer inline">
          <button type="button" className="btn" onClick={onClose}>
            Annuler
          </button>
          <button type="submit" className="btn primary">
            OK
          </button>
        </div>
      </form>
    </Modal>
  );
}
