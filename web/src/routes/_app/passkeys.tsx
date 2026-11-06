import { useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { formatDistanceToNowStrict } from "date-fns";
import { KeyRound, Plus } from "lucide-react";
import { useState } from "react";
import { api, data } from "@/api/client";
import { Tile } from "@/components/tile";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { toastManager } from "@/components/ui/toast";
import { createPasskey, passkeyName } from "@/lib/passkey";
import { passkeysQuery } from "@/queries/session";

export const Route = createFileRoute("/_app/passkeys")({
  loader: ({ context }) => context.queryClient.ensureQueryData(passkeysQuery),
  component: Passkeys,
});

function Passkeys() {
  const { data: keys } = useSuspenseQuery(passkeysQuery);
  const [name, setName] = useState(passkeyName);
  const queryClient = useQueryClient();
  const add = useMutation({
    mutationFn: async () => {
      const options = await data(api.POST("/api/auth/passkeys/begin"));
      const credential = await createPasskey(options);
      await data(api.POST("/api/auth/passkeys/finish", { body: { name: name.trim(), credential } }));
    },
    onSuccess: async () => {
      toastManager.add({ type: "success", title: `Registered ${name.trim()}` });
      await queryClient.invalidateQueries(passkeysQuery);
    },
  });
  const remove = useMutation({
    mutationFn: (id: string) => data(api.DELETE("/api/auth/passkeys/{id}", { params: { path: { id } } })),
    onSettled: () => queryClient.invalidateQueries(passkeysQuery),
  });
  return (
    <main className="mx-auto w-full max-w-2xl px-5 py-8">
      <h1 className="font-bold text-[1.875rem] tracking-[-0.035em]">Passkeys</h1>
      <p className="text-muted-foreground text-sm">
        Whoever holds one of these signs in to this panel. Changes record its name.
      </p>
      <section className="surface-raised mt-6 rounded-2xl p-3.5">
        <ul className="surface-inset divide-y divide-border/80 rounded-xl px-3">
          {keys.map((k) => (
            <li key={k.id} className="flex items-center gap-3 py-2.5">
              <Tile tone="slate" icon={KeyRound} size="sm" />
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium text-sm">
                  {k.name}
                  {k.current && <span className="ml-2 font-normal text-muted-foreground text-xs">this session</span>}
                </div>
                <div className="text-muted-foreground text-xs">
                  Added {formatDistanceToNowStrict(new Date(k.created_at), { addSuffix: true })}
                  {k.used_at && `, last used ${formatDistanceToNowStrict(new Date(k.used_at), { addSuffix: true })}`}
                </div>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="text-muted-foreground"
                disabled={keys.length === 1}
                loading={remove.isPending && remove.variables === k.id}
                onClick={() => remove.mutate(k.id)}
              >
                Remove
              </Button>
            </li>
          ))}
        </ul>
        <form
          className="mt-3.5 flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate();
          }}
        >
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Name of the new passkey"
            maxLength={100}
          />
          <Button type="submit" loading={add.isPending} disabled={!name.trim()}>
            <Plus /> Add passkey
          </Button>
        </form>
      </section>
    </main>
  );
}
