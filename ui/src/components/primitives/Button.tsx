import type { ButtonHTMLAttributes, ReactNode } from "react";
import { cn } from "@/lib/utils";

type ButtonTone = "primary" | "secondary" | "ghost" | "danger" | "link";
type ButtonSize = "sm" | "md" | "lg" | "icon";

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  tone?: ButtonTone;
  size?: ButtonSize;
  leadingIcon?: ReactNode;
  trailingIcon?: ReactNode;
};

const toneClasses: Record<ButtonTone, string> = {
  primary: "border-transparent bg-foreground text-background hover:bg-foreground/90",
  secondary: "border-border bg-background text-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
  ghost: "border-transparent bg-transparent text-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
  danger: "border-red-950/80 bg-red-950/20 text-red-300 hover:border-red-700 hover:text-red-200",
  link: "border-transparent bg-transparent px-0 text-primary underline-offset-4 hover:underline",
};

const sizeClasses: Record<ButtonSize, string> = {
  sm: "h-8 gap-1.5 px-3 text-xs",
  md: "h-9 gap-2 px-3 text-sm",
  lg: "h-10 gap-2 px-4 text-sm",
  icon: "h-9 w-9 p-0",
};

export function Button({
  className,
  tone = "primary",
  size = "md",
  leadingIcon,
  trailingIcon,
  children,
  type = "button",
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      data-onlava-ui="Button"
      data-tone={tone}
      data-size={size}
      className={cn(
        "inline-flex shrink-0 items-center justify-center rounded-md border font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
        toneClasses[tone],
        sizeClasses[size],
        className,
      )}
      {...props}
    >
      {leadingIcon}
      {children}
      {trailingIcon}
    </button>
  );
}
