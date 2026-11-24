import { createFileRoute } from "@tanstack/react-router";
import { SectionPage } from "@/components/settings/settings-page";
import { accountsPage } from "@/lib/settings";
import { settingsQuery } from "@/queries/modules";

export const Route = createFileRoute("/_app/settings/accounts")({
  loader: ({ context }) => void context.queryClient.prefetchQuery(settingsQuery({ section: accountsPage.section })),
  head: () => ({ meta: [{ title: "Accounts · otogi" }] }),
  component: () => (
    <SectionPage
      page={accountsPage}
      about={
        <>
          The platform accounts the bot runs as. A saved change applies on restart. A wrong credential is found only
          when the bot starts, and then keeps it from starting: the way back is{" "}
          <span className="font-mono text-[13px]">otogi settings revert system:accounts</span>, run on the server. For a
          new Telegram account, save it here, run{" "}
          <span className="font-mono text-[13px]">otogi login &lt;account&gt;</span> on the server, then restart.
        </>
      }
    />
  ),
});
