import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link, Outlet, useMatch } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { Ago } from "@/components/ago";
import { SearchInput } from "@/components/search-input";
import { Segmented } from "@/components/segmented";
import { Tile } from "@/components/tile";
import { chatName, preview } from "@/lib/chats";
import { platformMeta } from "@/lib/meta";
import { cn } from "@/lib/utils";
import { chatsQuery } from "@/queries/chats";

export const Route = createFileRoute("/_app/chats")({
  loader: ({ context }) => context.queryClient.ensureQueryData(chatsQuery),
  head: () => ({ meta: [{ title: "Chats · otogi" }] }),
  component: Chats,
});

type Filter = "all" | "telegram" | "qq" | "off";

const day = 24 * 60 * 60 * 1000;

function Chats() {
  const { data: chats } = useSuspenseQuery(chatsQuery);
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const shown = useMemo(() => {
    const q = search.trim().toLowerCase();
    return chats.filter(
      (c) =>
        (filter === "all" || (filter === "off" ? !c.lists?.length : c.platform === filter)) &&
        (!q || `${chatName(c)} ${c.title ?? ""} ${c.id} ${preview(c)}`.toLowerCase().includes(q)),
    );
  }, [chats, filter, search]);
  const open = useMatch({ from: "/_app/chats/$chat", shouldThrow: false });
  const current = open?.params.chat;
  return (
    <div className="grid min-h-0 flex-1 grid-cols-1 grid-rows-[minmax(0,1fr)] md:grid-cols-[21rem_minmax(0,1fr)]">
      <aside className={cn("min-h-0 flex-col md:flex md:border-r", open ? "hidden" : "flex")}>
        {!open && <h1 className="sr-only">Chats</h1>}
        <div className="flex flex-col gap-2.5 px-3 pt-4 pb-2">
          <SearchInput
            aria-label="Search chats or messages"
            placeholder="Search chats or messages"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <Segmented
            label="Which chats"
            value={filter}
            onChange={setFilter}
            options={[
              { value: "all", label: "All" },
              { value: "telegram", label: "Telegram" },
              { value: "qq", label: "QQ" },
              { value: "off", label: "Modules off" },
            ]}
          />
        </div>
        <ul className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
          {shown.map((c) => {
            const p = platformMeta(c.platform);
            const fresh = Date.now() - new Date(c.first_seen).getTime() < day;
            return (
              <li key={c.id}>
                <Link
                  to="/chats/$chat"
                  params={{ chat: c.id }}
                  className={cn(
                    "flex gap-3 rounded-xl px-2 py-2.5 outline-none transition-colors hover:bg-accent/70 focus-visible:ring-2 focus-visible:ring-ring",
                    c.id === current && "bg-accent",
                  )}
                >
                  <Tile tone={p.tone} icon={p.icon} />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-baseline gap-2">
                      <span className="truncate font-semibold text-sm">{chatName(c)}</span>
                      {fresh && <span className="font-semibold text-info-foreground text-xs tracking-wide">NEW</span>}
                      <Ago at={c.last_seen} className="ml-auto flex-none text-muted-foreground text-xs" />
                    </span>
                    <span className="block truncate text-[12.5px] text-muted-foreground">{preview(c) || " "}</span>
                  </span>
                </Link>
              </li>
            );
          })}
          {shown.length === 0 && (
            <li className="px-3 py-10 text-center text-muted-foreground text-sm">
              {chats.length === 0
                ? "The bot has not seen any chats yet. They appear here as messages arrive."
                : "No chats match."}
            </li>
          )}
        </ul>
      </aside>
      <Outlet />
    </div>
  );
}
