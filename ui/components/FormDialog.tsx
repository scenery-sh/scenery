import type { ISODateString } from "@astryxdesign/core/Calendar";
import { DateInput } from "@astryxdesign/core/DateInput";
import { Dialog, DialogHeader } from "@astryxdesign/core/Dialog";
import { NumberInput } from "@astryxdesign/core/NumberInput";
import { Selector } from "@astryxdesign/core/Selector";
import { TextArea } from "@astryxdesign/core/TextArea";
import { TextInput } from "@astryxdesign/core/TextInput";
import {
  borderVars,
  colorVars,
  radiusVars,
  spacingVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import type { FormEvent, ReactNode } from "react";
import type { Problem } from "./request-state.js";

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

export type TextFieldType = "text" | "password" | "email" | "number" | "date";

interface FieldControlProps {
  label: string;
  hint?: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  required?: boolean;
  disabled?: boolean;
}

export function TextField({
  label,
  hint,
  value,
  onChange,
  placeholder,
  required,
  disabled,
  type = "text",
  min,
  max,
}: FieldControlProps & { type?: TextFieldType; min?: number; max?: number }) {
  if (type === "number") {
    const parsed = Number(value);
    return (
      <NumberInput
        description={hint}
        hasClear
        isDisabled={disabled}
        isRequired={required}
        label={label}
        max={max}
        min={min}
        onChange={(next: number | null) =>
          onChange(next === null ? "" : String(next))
        }
        placeholder={placeholder}
        value={value !== "" && Number.isFinite(parsed) ? parsed : null}
      />
    );
  }
  if (type === "date") {
    return (
      <DateInput
        description={hint}
        isDisabled={disabled}
        isRequired={required}
        label={label}
        onChange={(next: ISODateString | undefined) => onChange(next ?? "")}
        value={value === "" ? undefined : (value as ISODateString)}
      />
    );
  }
  return (
    <TextInput
      description={hint}
      isDisabled={disabled}
      isRequired={required}
      label={label}
      onChange={onChange}
      placeholder={placeholder}
      type={type}
      value={value}
    />
  );
}

export function SelectField({
  label,
  hint,
  value,
  onChange,
  options,
  placeholder,
  required,
  disabled,
}: FieldControlProps & {
  options: readonly { value: string; label: string }[];
}) {
  return (
    <Selector
      description={hint}
      hasClear
      isDisabled={disabled}
      isRequired={required}
      label={label}
      onChange={(next: string | null) => onChange(next ?? "")}
      options={options.map((option) => ({ ...option }))}
      placeholder={placeholder}
      value={value || null}
    />
  );
}

export function TextAreaField({
  label,
  hint,
  value,
  onChange,
  placeholder,
  required,
  disabled,
  rows,
  maxLength,
}: FieldControlProps & { rows?: number; maxLength?: number }) {
  return (
    <TextArea
      description={hint}
      isDisabled={disabled}
      isRequired={required}
      label={label}
      maxLength={maxLength}
      onChange={onChange}
      placeholder={placeholder}
      rows={rows}
      value={value}
    />
  );
}

export function FormProblem({ problem }: { problem?: Problem }) {
  if (!problem) return null;
  return (
    <div role="alert" {...stylex.props(styles.problem)}>
      {problem.message}
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
  problem: {
    padding: spacingVars["--spacing-2"],
    borderColor: colorVars["--color-error"],
    borderStyle: "solid",
    borderWidth: borderVars["--border-width"],
    borderRadius: radiusVars["--radius-element"],
    color: colorVars["--color-error"],
    fontSize: 12,
  },
});
