import { cn } from "@/lib/utils";

const tones = {
  ok: "bg-emerald-500 shadow-[0_0_0_3px_--theme(--color-emerald-500/16%)]",
  error: "bg-red-500 shadow-[0_0_0_3px_--theme(--color-red-500/16%)]",
  warn: "bg-amber-500 shadow-[0_0_0_3px_--theme(--color-amber-500/16%)]",
  off: "bg-neutral-300",
};

export type DotTone = keyof typeof tones;

/** outcomeTone maps a record's outcome to a dot. */
export function outcomeTone(outcome?: string): DotTone {
  if (outcome === "error") return "error";
  if (outcome === "slow" || outcome === "degraded" || outcome === "skipped") return "warn";
  return outcome ? "ok" : "off";
}

export function StatusDot({ tone, className }: { tone: DotTone; className?: string }) {
  return <span aria-hidden className={cn("inline-block size-1.5 flex-none rounded-full", tones[tone], className)} />;
}
