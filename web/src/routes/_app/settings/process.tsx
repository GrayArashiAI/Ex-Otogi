import { createFileRoute } from "@tanstack/react-router";
import { SectionPage } from "@/components/settings/settings-page";
import { processPage } from "@/lib/settings";
import { settingsQuery } from "@/queries/modules";

export const Route = createFileRoute("/_app/settings/process")({
  loader: ({ context }) => void context.queryClient.prefetchQuery(settingsQuery({ section: processPage.section })),
  head: () => ({ meta: [{ title: "Process · otogi" }] }),
  component: () => (
    <SectionPage
      page={processPage}
      about="How much the bot handles at once, how long it lets a delivery and a stop take, and how its log is written. The process reads these when it starts: a saved change applies on restart."
    />
  ),
});
