import { CircleAlert, TriangleAlert } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

/** Notice is a problem said in place: red for a failure, amber for a caution. */
export function Notice({
  tone = "error",
  title,
  className,
  children,
  ...props
}: {
  tone?: "error" | "warning";
  title: string;
  children?: ReactNode;
} & Omit<ComponentProps<"div">, "title">) {
  const Icon = tone === "error" ? CircleAlert : TriangleAlert;
  return (
    <div
      className={cn(
        "flex gap-2.5 rounded-xl border px-3.5 py-3 text-sm",
        tone === "error" ? "border-destructive/24 bg-destructive/4" : "border-warning/32 bg-warning/6",
        className,
      )}
      {...props}
    >
      <Icon
        className={cn(
          "mt-0.5 size-4 flex-none",
          tone === "error" ? "text-destructive-foreground" : "text-warning-foreground",
        )}
      />
      <div className="min-w-0 break-words text-muted-foreground">
        <div className="font-medium text-foreground">{title}</div>
        {children}
      </div>
    </div>
  );
}
