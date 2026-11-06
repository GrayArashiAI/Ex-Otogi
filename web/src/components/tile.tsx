import type { ComponentType, SVGProps } from "react";
import type { Tone } from "@/lib/meta";
import { cn } from "@/lib/utils";

const tones: Record<Tone, string> = {
  emerald: "from-emerald-50 to-emerald-100/80 border-emerald-200/90 text-emerald-600",
  violet: "from-violet-50 to-violet-100/80 border-violet-200/90 text-violet-600",
  rose: "from-rose-50 to-rose-100/80 border-rose-200/90 text-rose-600",
  zinc: "from-zinc-50 to-zinc-100/90 border-zinc-300/80 text-zinc-900",
  sky: "from-sky-50 to-sky-100/80 border-sky-200/90 text-sky-600",
  orange: "from-orange-50 to-orange-100/80 border-orange-200/90 text-orange-600",
  amber: "from-amber-50 to-amber-100/80 border-amber-200/90 text-amber-600",
  indigo: "from-indigo-50 to-indigo-100/80 border-indigo-200/90 text-indigo-600",
  slate: "from-slate-50 to-slate-100/90 border-slate-200 text-slate-600",
  teal: "from-teal-50 to-teal-100/80 border-teal-200/90 text-teal-600",
};

const sizes = {
  sm: "size-7 rounded-lg [&_svg]:size-3.5",
  md: "size-9 rounded-[10px] [&_svg]:size-4.5",
  lg: "size-12 rounded-xl [&_svg]:size-6",
};

/** Tile is a module's or platform's pastel icon tile, lit from above. */
export function Tile({
  tone,
  icon: Icon,
  size = "md",
  className,
}: {
  tone: Tone;
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  size?: keyof typeof sizes;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "grid flex-none place-items-center border bg-linear-to-b shadow-[0_1px_2px_--theme(--color-black/5%)] inset-shadow-[0_1px_--theme(--color-white/90%)]",
        tones[tone],
        sizes[size],
        className,
      )}
    >
      <Icon />
    </span>
  );
}
