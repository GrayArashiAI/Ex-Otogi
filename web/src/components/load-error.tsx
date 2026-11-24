import { CircleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/** LoadError stands where something failed to load: what it was, what the
 * server said, and a way to try again. */
export function LoadError({
  what,
  error,
  retry,
  className,
}: {
  what: string;
  error: Error;
  retry: () => void;
  className?: string;
}) {
  return (
    <div role="alert" className={cn("flex items-start gap-2.5 py-3 text-left", className)}>
      <CircleAlert className="mt-0.5 size-4 flex-none text-destructive-foreground" />
      <div className="min-w-0 flex-1">
        <div className="font-medium text-sm">Could not load {what}</div>
        <p className="wrap-anywhere text-muted-foreground text-xs">{error.message}</p>
      </div>
      <Button variant="outline" size="sm" onClick={retry}>
        Retry
      </Button>
    </div>
  );
}
