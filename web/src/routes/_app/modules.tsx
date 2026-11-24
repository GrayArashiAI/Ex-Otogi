import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link, Outlet, useMatch } from "@tanstack/react-router";
import type { Schemas } from "@/api/client";
import { Tile } from "@/components/tile";
import { listMeta, moduleMeta, moduleState } from "@/lib/meta";
import { cn } from "@/lib/utils";
import { modulesQuery } from "@/queries/modules";

export const Route = createFileRoute("/_app/modules")({
  loader: ({ context }) => context.queryClient.ensureQueryData(modulesQuery),
  head: () => ({ meta: [{ title: "Modules · otogi" }] }),
  component: Modules,
});

/** byUse puts the modules that answer in chosen chats first. */
export function byUse(modules: Schemas["Module"][]) {
  return [...modules].sort(
    (a, b) =>
      Number(b.allowlists.length > 0) - Number(a.allowlists.length > 0) ||
      Number(b.id === "chat") - Number(a.id === "chat") ||
      Number(b.enabled) - Number(a.enabled) ||
      moduleMeta(a.id).title.localeCompare(moduleMeta(b.id).title),
  );
}

function Modules() {
  const { data: modules } = useSuspenseQuery(modulesQuery);
  const open = useMatch({ from: "/_app/modules/$module", shouldThrow: false });
  // On a phone the list is a page of its own, and this is its heading.
  const Heading = open ? "h2" : "h1";
  return (
    <div className="grid flex-1 grid-cols-1 md:grid-cols-[17.5rem_minmax(0,1fr)]">
      <aside className={cn("px-3 py-5 md:block md:border-r", open && "hidden")}>
        <Heading className="mx-2 mb-2 font-medium text-muted-foreground text-xs">Modules</Heading>
        <ul className="flex flex-col gap-0.5">
          {byUse(modules).map((m) => {
            const meta = moduleMeta(m.id);
            const chats = m.allowlists.reduce((n, l) => n + l.chats, 0);
            const state = moduleState(m);
            return (
              <li key={m.id}>
                <Link
                  to="/modules/$module"
                  params={{ module: m.id }}
                  activeOptions={{ includeHash: true }}
                  className={cn(
                    "group flex items-center gap-3 rounded-xl px-2 py-2 outline-none transition-colors hover:bg-accent/70 focus-visible:ring-2 focus-visible:ring-ring",
                    open?.params.module === m.id && "bg-accent",
                  )}
                >
                  <Tile tone={meta.tone} icon={meta.icon} className={cn(!m.running && "opacity-55 saturate-50")} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-semibold text-sm leading-tight">{meta.title}</span>
                    <span className="block truncate font-mono text-muted-foreground text-xs">{m.id}</span>
                  </span>
                  <span
                    className={cn(
                      "text-xs tabular",
                      state.tone === "error" ? "font-medium text-destructive-foreground" : "text-muted-foreground",
                    )}
                  >
                    {!m.running ? state.label : m.allowlists.length > 0 ? `${chats} chat${chats === 1 ? "" : "s"}` : ""}
                  </span>
                </Link>
                {m.id === "chat" && m.allowlists.length > 0 && (
                  <ul className="mt-0.5 mb-1 ml-[1.375rem] flex flex-col gap-0.5 border-l pl-3">
                    {m.allowlists.map((l, i) => {
                      const lm = listMeta(m.id, l, i);
                      if (!lm.agent) return null;
                      return (
                        <li key={l.name}>
                          <Link
                            to="/modules/$module"
                            params={{ module: m.id }}
                            hash={`list-${l.name}`}
                            activeOptions={{ includeHash: true }}
                            className="flex items-center gap-2.5 rounded-lg px-2 py-1.5 text-sm outline-none transition-colors hover:bg-accent/70 focus-visible:ring-2 focus-visible:ring-ring data-[status=active]:bg-accent/70 pointer-coarse:min-h-11"
                          >
                            <Tile tone={lm.tone} icon={lm.icon} size="sm" />
                            <span className="flex-1 truncate font-medium">{l.title}</span>
                            <span className="text-muted-foreground text-xs tabular">{l.chats}</span>
                          </Link>
                        </li>
                      );
                    })}
                  </ul>
                )}
              </li>
            );
          })}
        </ul>
      </aside>
      <Outlet />
    </div>
  );
}
