import { useQueryErrorResetBoundary } from "@tanstack/react-query";
import { type ErrorComponentProps, Link, useRouter } from "@tanstack/react-router";
import { CircleAlert, SearchX } from "lucide-react";
import { type ReactNode, useEffect } from "react";
import { Tile } from "@/components/tile";
import { Button } from "@/components/ui/button";
import type { Tone } from "@/lib/meta";

function Screen({
  tone,
  icon,
  title,
  detail,
  children,
}: {
  tone: Tone;
  icon: typeof CircleAlert;
  title: string;
  detail: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="mx-auto flex w-full max-w-md flex-1 flex-col items-center justify-center gap-3 px-5 py-16 text-center">
      <Tile tone={tone} icon={icon} size="lg" />
      <div>
        <h1 className="font-semibold text-lg tracking-tight">{title}</h1>
        <p className="wrap-anywhere mt-1 text-muted-foreground text-sm">{detail}</p>
      </div>
      <div className="mt-1 flex flex-wrap justify-center gap-2">{children}</div>
    </div>
  );
}

/** RouteError stands in for a page whose data failed to load; the header
 * and navigation around it stay. */
export function RouteError({ error }: ErrorComponentProps) {
  const router = useRouter();
  const queries = useQueryErrorResetBoundary();
  useEffect(() => queries.reset(), [queries]);
  return (
    <Screen
      tone="red"
      icon={CircleAlert}
      title="This page could not be loaded"
      detail={error instanceof Error ? error.message : String(error)}
    >
      <Button onClick={() => void router.invalidate()}>Retry</Button>
      <Button variant="outline" render={<Link to="/status" />}>
        Status
      </Button>
      <Button variant="outline" render={<Link to="/events" />}>
        Event log
      </Button>
    </Screen>
  );
}

/** NotFound names what is not there and links back to where it would be. */
export function NotFound({ title, detail, children }: { title: string; detail: ReactNode; children: ReactNode }) {
  return (
    <Screen tone="slate" icon={SearchX} title={title} detail={detail}>
      {children}
    </Screen>
  );
}

export function PageNotFound() {
  const router = useRouter();
  return (
    <NotFound
      title="No such page"
      detail={
        <>
          The panel has nothing at <span className="font-mono text-xs">{router.state.location.pathname}</span>.
        </>
      }
    >
      <Button variant="outline" render={<Link to="/status" />}>
        Status
      </Button>
      <Button variant="outline" render={<Link to="/modules" />}>
        Modules
      </Button>
      <Button variant="outline" render={<Link to="/chats" />}>
        Chats
      </Button>
      <Button variant="outline" render={<Link to="/events" />}>
        Event log
      </Button>
    </NotFound>
  );
}
