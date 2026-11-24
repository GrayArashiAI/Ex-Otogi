import { format } from "date-fns";
import { ago } from "@/lib/time";

/** Ago shows how long ago a time was; hovering gives the time itself. */
export function Ago({ at, className }: { at: string; className?: string }) {
  return (
    <time dateTime={at} title={format(new Date(at), "MMM d, yyyy HH:mm:ss")} className={className}>
      {ago(at)}
    </time>
  );
}
