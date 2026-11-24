import { createFileRoute, Link, Outlet, useMatch } from "@tanstack/react-router";
import { Tile } from "@/components/tile";
import { settingsPages } from "@/lib/settings";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_app/settings")({
  head: () => ({ meta: [{ title: "Settings · otogi" }] }),
  component: Settings,
});

function Settings() {
  // On a phone the list is a page of its own, and this is its heading.
  const list = useMatch({ from: "/_app/settings/", shouldThrow: false });
  const Heading = list ? "h1" : "h2";
  return (
    <div className="grid flex-1 grid-cols-1 md:grid-cols-[17.5rem_minmax(0,1fr)]">
      <aside className={cn("px-3 py-5 md:block md:border-r", !list && "hidden")}>
        <Heading className="mx-2 mb-2 font-medium text-muted-foreground text-xs">Settings</Heading>
        <ul className="flex flex-col gap-0.5">
          {settingsPages.map((page) => (
            <li key={page.to}>
              <Link
                to={page.to}
                className="flex items-center gap-3 rounded-xl px-2 py-2 outline-none transition-colors hover:bg-accent/70 focus-visible:ring-2 focus-visible:ring-ring data-[status=active]:bg-accent"
              >
                <Tile tone={page.tone} icon={page.icon} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-semibold text-sm leading-tight">{page.title}</span>
                  <span className="block truncate text-muted-foreground text-xs">{page.detail}</span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      </aside>
      <Outlet />
    </div>
  );
}
