import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useState } from "react";
import { api, data } from "@/api/client";
import { ChatPicker } from "@/components/chat-picker";
import { Tile } from "@/components/tile";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { chatName } from "@/lib/chats";
import { type ListMeta, platformMeta } from "@/lib/meta";
import { allowlistQuery } from "@/queries/modules";

/** AllowlistCard shows the chats on one allowlist, to add and remove. */
export function AllowlistCard({
  module,
  list,
  meta,
  description,
}: {
  module: string;
  list: string;
  meta: ListMeta;
  description?: string;
}) {
  const { data: allowlist, isPending } = useQuery(allowlistQuery(module, list));
  const [picking, setPicking] = useState(false);
  const queryClient = useQueryClient();
  const remove = useMutation({
    mutationFn: (chat: string) =>
      data(
        api.DELETE("/api/modules/{module}/allowlists/{list}/chats/{chat}", {
          params: { path: { module, list, chat } },
        }),
      ),
    onSettled: async () => {
      await queryClient.invalidateQueries({ queryKey: ["modules"] });
      await queryClient.invalidateQueries({ queryKey: ["chats"] });
    },
  });
  const chats = allowlist?.chats ?? [];
  return (
    <section id={`list-${list}`} className="surface-raised scroll-mt-20 rounded-2xl">
      <header className="flex items-center gap-3 px-4 pt-3.5 pb-2.5">
        <Tile tone={meta.tone} icon={meta.icon} size="sm" />
        <div className="min-w-0 flex-1">
          <h2 className="font-semibold text-[15px] leading-tight">{meta.heading}</h2>
          {description && <p className="truncate text-muted-foreground text-xs">{description}</p>}
        </div>
        <Button size="sm" onClick={() => setPicking(true)}>
          <Plus /> Add chats
        </Button>
      </header>
      <div className="surface-inset mx-3.5 mb-3.5 rounded-xl px-3">
        {isPending && <Skeleton className="my-3 h-9 w-full" />}
        {!isPending && chats.length === 0 && (
          <p className="py-6 text-center text-muted-foreground text-sm">{meta.empty}</p>
        )}
        <ul className="divide-y divide-border/80">
          {chats.map((c) => {
            const p = platformMeta(c.chat.split(":")[0] ?? "");
            return (
              <li key={c.chat} className="flex items-center gap-3 py-2.5">
                <Tile tone={p.tone} icon={p.icon} size="sm" />
                <Link
                  to="/chats/$chat"
                  params={{ chat: c.chat }}
                  className="min-w-0 flex-1 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <span className="block truncate font-medium text-sm hover:underline">
                    {chatName(c.directory, c.chat)}
                  </span>
                  <span className="block truncate font-mono text-[11.5px] text-muted-foreground">{c.chat}</span>
                </Link>
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-muted-foreground"
                  loading={remove.isPending && remove.variables === c.chat}
                  onClick={() => remove.mutate(c.chat)}
                >
                  Remove
                </Button>
              </li>
            );
          })}
        </ul>
      </div>
      <p className="px-4 pb-3.5 text-muted-foreground text-xs">
        Changes apply at once and are recorded in the event log.
      </p>
      <ChatPicker
        module={module}
        list={list}
        title={meta.title}
        on={chats.map((c) => c.chat)}
        open={picking}
        onOpenChange={setPicking}
      />
    </section>
  );
}
