import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useMemo, useState } from "react";
import { Segmented } from "@/components/segmented";
import { Tile } from "@/components/tile";
import { Input } from "@/components/ui/input";
import { chatName, preview } from "@/lib/chats";
import { platformMeta } from "@/lib/meta";
import { ago } from "@/lib/time";
import { chatsQuery } from "@/queries/chats";

export const Route = createFileRoute("/_app/chats")({
  loader: ({ context }) => context.queryClient.ensureQueryData(chatsQuery),
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
  return (
    <div className="grid min-h-0 flex-1 md:grid-cols-[21rem_minmax(0,1fr)]">
      <aside className="flex min-h-0 flex-col border-b md:h-[calc(100svh-3.5rem)] md:border-r md:border-b-0">
        <div className="flex flex-col gap-2.5 px-3 pt-4 pb-2">
          <div className="relative">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 z-10 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="ps-7"
              placeholder="Search chats or messages"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <Segmented
            value={filter}
            onChange={setFilter}
            options={[
              { value: "all", label: "All" },
              { value: "telegram", label: "Telegram" },
              { value: "qq", label: "QQ" },
              { value: "off", label: "Not enabled" },
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
                  className="flex gap-3 rounded-xl px-2 py-2.5 outline-none transition-colors hover:bg-accent/70 focus-visible:ring-2 focus-visible:ring-ring data-[status=active]:bg-accent"
                >
                  <Tile tone={p.tone} icon={p.icon} />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-baseline gap-2">
                      <span className="truncate font-semibold text-sm">{chatName(c)}</span>
                      {fresh && <span className="font-semibold text-[10.5px] text-sky-600 tracking-wide">NEW</span>}
                      <time className="ml-auto flex-none text-muted-foreground text-xs" dateTime={c.last_seen}>
                        {ago(c.last_seen)}
                      </time>
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
