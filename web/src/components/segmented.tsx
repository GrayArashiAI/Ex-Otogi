import { SegmentedControl, SegmentedControlItem } from "@/components/ui/segmented-control";
import { cn } from "@/lib/utils";

/** Segmented picks one of a few options. */
export function Segmented<T extends string>({
  value,
  onChange,
  options,
  label,
  className,
}: {
  /** label names the choice for assistive tech. */
  label: string;
  value: T;
  onChange: (value: T) => void;
  options: { value: T; label: string }[];
  className?: string;
}) {
  return (
    <SegmentedControl role="group" aria-label={label} className={cn("flex w-full", className)}>
      {options.map((o) => (
        <SegmentedControlItem
          key={o.value}
          aria-pressed={o.value === value}
          data-pressed={o.value === value ? "" : undefined}
          onClick={() => onChange(o.value)}
          className="hit-target flex-1"
        >
          {o.label}
        </SegmentedControlItem>
      ))}
    </SegmentedControl>
  );
}
