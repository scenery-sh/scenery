import { Dialog, DialogHeader } from "@astryxdesign/core/Dialog";
import {
  borderVars,
  colorVars,
  radiusVars,
  spacingVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import type {
  FormEvent,
  InputHTMLAttributes,
  ReactNode,
  SelectHTMLAttributes,
  TextareaHTMLAttributes,
} from "react";
import { useId } from "react";

export function FormDialog({
  title,
  subtitle,
  onOpenChange,
  onSubmit,
  footer,
  children,
  width = 560,
}: {
  title: string;
  subtitle?: string;
  onOpenChange: (open: boolean) => void;
  onSubmit?: (event: FormEvent<HTMLFormElement>) => void;
  footer: ReactNode;
  children: ReactNode;
  width?: number;
}) {
  const body = (
    <>
      <div {...stylex.props(styles.body)}>{children}</div>
      <footer {...stylex.props(styles.footer)}>{footer}</footer>
    </>
  );
  return (
    <Dialog
      isOpen
      onOpenChange={onOpenChange}
      padding={0}
      purpose="form"
      width={`min(${width}px, calc(100vw - 16px))`}
      xstyle={styles.dialog}
    >
      <div {...stylex.props(styles.content)}>
        <div {...stylex.props(styles.header)}>
          <DialogHeader
            onOpenChange={onOpenChange}
            subtitle={subtitle}
            title={title}
          />
        </div>
        {onSubmit ? (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              onSubmit(event);
            }}
            {...stylex.props(styles.form)}
          >
            {body}
          </form>
        ) : (
          body
        )}
      </div>
    </Dialog>
  );
}

export function Field({
  label,
  hint,
  children,
}: {
  label: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <label {...stylex.props(styles.field)}>
      <span {...stylex.props(styles.fieldLabel)}>{label}</span>
      {children}
      {hint ? <small {...stylex.props(styles.fieldHint)}>{hint}</small> : null}
    </label>
  );
}

type ControlAttributes<T> = Omit<T, "className" | "style">;

export function TextField({
  label,
  hint,
  ...input
}: {
  label: ReactNode;
  hint?: ReactNode;
} & ControlAttributes<InputHTMLAttributes<HTMLInputElement>>) {
  const id = useId();
  return (
    <FieldFor id={id} label={label} hint={hint}>
      <input id={id} {...stylex.props(styles.control)} {...input} />
    </FieldFor>
  );
}

export function SelectField({
  label,
  hint,
  options,
  children,
  ...select
}: {
  label: ReactNode;
  hint?: ReactNode;
  options?: readonly { value: string; label: string }[];
  children?: ReactNode;
} & ControlAttributes<SelectHTMLAttributes<HTMLSelectElement>>) {
  const id = useId();
  return (
    <FieldFor id={id} label={label} hint={hint}>
      <select id={id} {...stylex.props(styles.control)} {...select}>
        {options
          ? options.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))
          : children}
      </select>
    </FieldFor>
  );
}

export function TextAreaField({
  label,
  hint,
  ...textarea
}: {
  label: ReactNode;
  hint?: ReactNode;
} & ControlAttributes<TextareaHTMLAttributes<HTMLTextAreaElement>>) {
  const id = useId();
  return (
    <FieldFor id={id} label={label} hint={hint}>
      <textarea
        id={id}
        {...stylex.props(styles.control, styles.textarea)}
        {...textarea}
      />
    </FieldFor>
  );
}

function FieldFor({
  id,
  label,
  hint,
  children,
}: {
  id: string;
  label: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div {...stylex.props(styles.field)}>
      <label htmlFor={id} {...stylex.props(styles.fieldLabel)}>
        {label}
      </label>
      {children}
      {hint ? <small {...stylex.props(styles.fieldHint)}>{hint}</small> : null}
    </div>
  );
}

const styles = stylex.create({
  dialog: { maxHeight: "calc(100dvh - 16px)" },
  content: {
    maxHeight: "calc(100dvh - 16px)",
    display: "flex",
    flexDirection: "column",
  },
  form: { minHeight: 0, display: "flex", flexDirection: "column" },
  header: { flexShrink: 0, padding: spacingVars["--spacing-4"] },
  body: {
    minHeight: 0,
    overflowY: "auto",
    display: "flex",
    flexDirection: "column",
    gap: spacingVars["--spacing-3"],
    padding: `0 ${spacingVars["--spacing-4"]} ${spacingVars["--spacing-4"]}`,
    scrollbarColor: `${colorVars["--color-text-secondary"]} transparent`,
    scrollbarWidth: "thin",
  },
  footer: {
    flexShrink: 0,
    display: "flex",
    justifyContent: "flex-end",
    gap: spacingVars["--spacing-2"],
    padding: spacingVars["--spacing-3"],
    borderTopColor: colorVars["--color-border"],
    borderTopStyle: "solid",
    borderTopWidth: borderVars["--border-width"],
  },
  field: {
    display: "flex",
    flexDirection: "column",
    gap: spacingVars["--spacing-1"],
  },
  fieldLabel: { fontSize: 12, color: colorVars["--color-text-secondary"] },
  fieldHint: { color: colorVars["--color-text-secondary"] },
  control: {
    boxSizing: "border-box",
    width: "100%",
    minHeight: 36,
    paddingBlock: spacingVars["--spacing-2"],
    paddingInline: spacingVars["--spacing-3"],
    borderColor: colorVars["--color-border"],
    borderStyle: "solid",
    borderWidth: borderVars["--border-width"],
    borderRadius: radiusVars["--radius-element"],
    backgroundColor: colorVars["--color-background-body"],
    color: colorVars["--color-text-primary"],
    font: "inherit",
  },
  textarea: { minHeight: 96, resize: "vertical" },
});
