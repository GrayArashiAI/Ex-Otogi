import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { Card, CardHeader, CardInset, rows } from "@/components/card";
import { LoadError } from "@/components/load-error";
import { SettingsPage } from "@/components/settings/settings-page";
import { StatusDot } from "@/components/status-dot";
import { Skeleton } from "@/components/ui/skeleton";
import { startupPage } from "@/lib/settings";
import { startupQuery } from "@/queries/system";

export const Route = createFileRoute("/_app/settings/startup")({
  loader: ({ context }) => void context.queryClient.prefetchQuery(startupQuery),
  head: () => ({ meta: [{ title: "Startup file · otogi" }] }),
  component: StartupPage,
});

function StartupPage() {
  const { data: startup, error, refetch } = useQuery(startupQuery);
  const management = startup?.management;
  return (
    <SettingsPage
      page={startupPage}
      about="What the bot reads from its config file when it starts, as the running bot read it. These stay in the file because they decide whether this panel can be reached: a mistake in them could not be undone from here. To change one, edit the file on the server and restart the bot."
    >
      <Card className="mt-6 max-w-4xl">
        <CardHeader
          title="Config file"
          description={startup && <span className="wrap-anywhere font-mono">{startup.config_file}</span>}
        />
        <CardInset>
          {error && !startup && <LoadError what="the startup settings" error={error} retry={() => void refetch()} />}
          {!startup && !error && (
            <div className="flex flex-col gap-2 py-3">
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
            </div>
          )}
          {startup && management && (
            <dl className={rows}>
              <Value name="Data directory" at="data_dir" value={startup.data_dir} />
              <Value name="Management address" at="management.addr" value={management.addr} />
              <Value name="Bearer token" at="management.token" set={management.token_set} />
              <Value name="Public URL" at="management.public_url" value={management.public_url} />
              <Value name="Sign-in provider" at="management.oidc.issuer" value={management.oidc.issuer} />
              <Value name="Client ID" at="management.oidc.client_id" value={management.oidc.client_id} />
              <Value name="Client secret" at="management.oidc.client_secret" set={management.oidc.client_secret_set} />
              <Value name="Who may sign in" at="management.oidc.allow" value={management.oidc.allow.join(", ")} />
            </dl>
          )}
        </CardInset>
      </Card>
    </SettingsPage>
  );
}

/** Value is one of the file's settings: what it is, the key the file has it
 * under, and its value, or for a secret only whether it is set. */
function Value({ name, at, value, set }: { name: string; at: string; value?: string; set?: boolean }) {
  // A secret says only whether it is set.
  const text = set === undefined ? value : set && "Set";
  const shown: ReactNode = !text ? (
    <span className="text-muted-foreground">Not set</span>
  ) : set ? (
    <span className="flex items-center gap-2">
      <StatusDot tone="ok" /> {text}
    </span>
  ) : (
    <span className="wrap-anywhere font-mono text-[13px]">{text}</span>
  );
  return (
    <div className="grid grid-cols-1 gap-x-8 gap-y-1 py-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)] sm:items-center">
      <dt>
        <span className="block font-medium text-sm">{name}</span>
        <span className="block font-mono text-muted-foreground text-xs">{at}</span>
      </dt>
      <dd className="min-w-0 text-sm">{shown}</dd>
    </div>
  );
}
