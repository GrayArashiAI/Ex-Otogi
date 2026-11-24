import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

// Each tone has its own shape as well as its colour, drawn in a 12px box:
// the mark itself and a faint halo of the same shape around it.
const tones: Record<string, { color: string; halo?: ReactNode; mark: ReactNode }> = {
  ok: { color: "text-success", halo: <circle cx="6" cy="6" r="6" />, mark: <circle cx="6" cy="6" r="3" /> },
  error: {
    color: "text-destructive",
    halo: <path d="M6 0l6 6-6 6-6-6z" />,
    mark: <path d="M6 2l4 4-4 4-4-4z" />,
  },
  warn: {
    color: "text-warning",
    halo: <path d="M6 0l6.5 11.5h-13z" />,
    mark: <path d="M6 2.5l4 7h-8z" />,
  },
  running: {
    color: "animate-pulse text-info",
    halo: <circle cx="6" cy="6" r="6" />,
    mark: <circle cx="6" cy="6" r="2.75" fill="none" stroke="currentColor" strokeWidth="1.5" />,
  },
  off: { color: "text-muted-foreground", mark: <rect x="2.5" y="5.25" width="7" height="1.5" rx="0.75" /> },
};

export type DotTone = "ok" | "error" | "warn" | "running" | "off";

/** outcomeTone maps a record's outcome to a dot. */
export function outcomeTone(outcome?: string): DotTone {
  if (outcome === "running") return "running";
  if (outcome === "error") return "error";
  if (outcome === "slow" || outcome === "degraded" || outcome === "skipped") return "warn";
  return outcome ? "ok" : "off";
}

/** StatusDot marks a state by shape and colour. Give it a label where no
 * visible word beside it says the state. */
export function StatusDot({ tone, label, className }: { tone: DotTone; label?: string; className?: string }) {
  const t = tones[tone] as (typeof tones)[string];
  return (
    <svg
      viewBox="0 0 12 12"
      fill="currentColor"
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      className={cn("-m-[3px] inline-block size-3 flex-none overflow-visible", t.color, className)}
    >
      {t.halo && <g opacity="0.16">{t.halo}</g>}
      {t.mark}
    </svg>
  );
}
