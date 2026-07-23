import type { ButtonHTMLAttributes, ReactNode } from "react";

type Variant = "primary" | "ghost" | "danger" | "success";

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  icon?: ReactNode;
}

const VARIANTS: Record<Variant, string> = {
  primary:
    "bg-accent/10 text-accent border-accent/40 hover:bg-accent/20 hover:border-accent",
  success:
    "bg-success/10 text-success border-success/40 hover:bg-success/20 hover:border-success",
  danger:
    "bg-error/10 text-error border-error/40 hover:bg-error/20 hover:border-error",
  ghost:
    "bg-transparent text-fg-muted border-border hover:text-fg hover:border-border-bright",
};

/** Hand-rolled button — terminal-flavored, monospace, sharp corners. */
export function Button({ variant = "ghost", icon, children, className = "", ...rest }: Props) {
  return (
    <button
      className={`inline-flex items-center gap-2 rounded-sm border px-3 py-1.5 font-mono text-xs uppercase tracking-wide transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${VARIANTS[variant]} ${className}`}
      {...rest}
    >
      {icon}
      {children}
    </button>
  );
}
