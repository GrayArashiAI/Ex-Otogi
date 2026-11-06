import { SegmentedControl, SegmentedControlItem } from "@/components/ui/segmented-control";
import { cn } from "@/lib/utils";

/** Segmented picks one of a few options. */
export function Segmented<T extends string>({
  value,
  onChange,
  options,
  className,
}: {
  value: T;
  onChange: (value: T) => void;
  options: { value: T; label: string }[];
  className?: string;
}) {
  return (
    <SegmentedControl className={cn("flex w-full", className)}>
      {options.map((o) => (
        <SegmentedControlItem
          key={o.value}
          aria-pressed={o.value === value}
          data-pressed={o.value === value ? "" : undefined}
          onClick={() => onChange(o.value)}
          className="flex-1"
        >
          {o.label}
        </SegmentedControlItem>
      ))}
    </SegmentedControl>
  );
}
