import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useState } from "react";
import { api, data } from "@/api/client";
import { Card, CardHeader, CardInset, rows } from "@/components/card";
import { ChatPicker } from "@/components/chat-picker";
import { LoadError } from "@/components/load-error";
import { Tile } from "@/components/tile";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { toastManager } from "@/components/ui/toast";
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
  const { data: allowlist, isPending, error, refetch } = useQuery(allowlistQuery(module, list));
  const [picking, setPicking] = useState(false);
  const queryClient = useQueryClient();
  const settled = async () => {
    await queryClient.invalidateQueries({ queryKey: ["modules"] });
    await queryClient.invalidateQueries({ queryKey: ["chats"] });
  };
  const add = useMutation({
    mutationFn: ({ chat }: { chat: string; name: string }) =>
      data(
        api.PUT("/api/modules/{module}/allowlists/{list}/chats/{chat}", { params: { path: { module, list, chat } } }),
      ),
    onSuccess: (_, { name }) => toastManager.add({ type: "success", title: `Added ${name} to ${meta.title} again` }),
    onSettled: settled,
  });
  const remove = useMutation({
    mutationFn: ({ chat }: { chat: string; name: string }) =>
      data(
        api.DELETE("/api/modules/{module}/allowlists/{list}/chats/{chat}", {
          params: { path: { module, list, chat } },
        }),
      ),
    onSuccess: (_, removed) =>
      toastManager.add({
        type: "success",
        title: `Removed ${removed.name} from ${meta.title}`,
        actionProps: { children: "Undo", onClick: () => add.mutate(removed) },
      }),
    onSettled: settled,
  });
  const chats = allowlist?.chats ?? [];
  return (
    <Card id={`list-${list}`} className="scroll-mt-6">
      <CardHeader
        icon={<Tile tone={meta.tone} icon={meta.icon} size="sm" />}
        title={meta.heading}
        description={description}
      >
        <Button size="sm" onClick={() => setPicking(true)}>
          <Plus /> Add chats
        </Button>
      </CardHeader>
      <CardInset>
        {isPending && <Skeleton className="my-3 h-9 w-full" />}
        {error && !allowlist && <LoadError what="these chats" error={error} retry={() => void refetch()} />}
        {allowlist && chats.length === 0 && (
          <p className="py-6 text-center text-muted-foreground text-sm">{meta.empty}</p>
        )}
        <ul className={rows}>
          {chats.map((c) => {
            const p = platformMeta(c.chat.split(":")[0] ?? "");
            const name = chatName(c.directory, c.chat);
            return (
              <li key={c.chat} className="flex items-center gap-3 py-2.5">
                <Tile tone={p.tone} icon={p.icon} size="sm" />
                <Link
                  to="/chats/$chat"
                  params={{ chat: c.chat }}
                  className="hit-target relative min-w-0 flex-1 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <span className="block truncate font-medium text-sm hover:underline">{name}</span>
                  <span className="block truncate font-mono text-muted-foreground text-xs">{c.chat}</span>
                </Link>
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-muted-foreground"
                  loading={remove.isPending && remove.variables.chat === c.chat}
                  onClick={() => remove.mutate({ chat: c.chat, name })}
                  aria-label={`Remove ${name}`}
                >
                  Remove
                </Button>
              </li>
            );
          })}
        </ul>
      </CardInset>
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
    </Card>
  );
}
