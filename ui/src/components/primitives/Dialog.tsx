import type { DialogHTMLAttributes, HTMLAttributes, ReactNode } from "react";
import { useEffect, useRef } from "react";
import { Button } from "./Button";
import { cn } from "@/lib/utils";

export type DialogProps = Omit<DialogHTMLAttributes<HTMLDialogElement>, "open"> & {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title?: string;
  description?: string;
  footer?: ReactNode;
};

export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  footer,
  children,
  className,
  ...props
}: DialogProps) {
  const ref = useRef<HTMLDialogElement | null>(null);

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) {
      return;
    }
    if (open && !dialog.open) {
      dialog.showModal();
    }
    if (!open && dialog.open) {
      dialog.close();
    }
  }, [open]);

  return (
    <dialog
      ref={ref}
      data-onlava-ui="Dialog"
      className={cn("w-full max-w-md rounded-md border border-border bg-popover p-0 text-popover-foreground shadow-lg", className)}
      onCancel={(event) => {
        event.preventDefault();
        onOpenChange(false);
      }}
      onClose={() => onOpenChange(false)}
      {...props}
    >
      <div className="grid gap-4 p-5">
        {title || description ? (
          <DialogHeader>
            {title ? <DialogTitle>{title}</DialogTitle> : null}
            {description ? <DialogDescription>{description}</DialogDescription> : null}
          </DialogHeader>
        ) : null}
        {children}
        {footer ? <DialogFooter>{footer}</DialogFooter> : null}
      </div>
    </dialog>
  );
}

export function DialogHeader({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="dialog-header" className={cn("grid gap-1.5", className)} {...props} />;
}

export function DialogTitle({ className, ...props }: HTMLAttributes<HTMLHeadingElement>) {
  return <h2 data-slot="dialog-title" className={cn("text-base font-semibold", className)} {...props} />;
}

export function DialogDescription({ className, ...props }: HTMLAttributes<HTMLParagraphElement>) {
  return <p data-slot="dialog-description" className={cn("text-sm text-muted-foreground", className)} {...props} />;
}

export function DialogFooter({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="dialog-footer" className={cn("flex justify-end gap-2", className)} {...props} />;
}

export function DialogCloseButton({ onClose }: { onClose: () => void }) {
  return (
    <Button tone="secondary" onClick={onClose}>
      Close
    </Button>
  );
}
