import { createFileRoute } from "@tanstack/react-router";
import { SectionPage } from "@/components/settings/settings-page";
import { generalPage } from "@/lib/settings";
import { settingsQuery } from "@/queries/modules";

export const Route = createFileRoute("/_app/settings/general")({
  loader: ({ context }) => void context.queryClient.prefetchQuery(settingsQuery({ section: generalPage.section })),
  head: () => ({ meta: [{ title: "General · otogi" }] }),
  component: () => (
    <SectionPage
      page={generalPage}
      about="Who operates the bot, what starts a command, how long the event log is kept, and how much the process logs. A saved change applies at once, and nothing restarts."
    />
  ),
});
