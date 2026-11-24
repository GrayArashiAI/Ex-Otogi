import { createFileRoute } from "@tanstack/react-router";
import { SectionPage } from "@/components/settings/settings-page";
import { llmPage } from "@/lib/settings";
import { settingsQuery } from "@/queries/modules";

export const Route = createFileRoute("/_app/settings/llm")({
  loader: ({ context }) => void context.queryClient.prefetchQuery(settingsQuery({ section: llmPage.section })),
  head: () => ({ meta: [{ title: "Language models · otogi" }] }),
  component: () => (
    <SectionPage
      page={llmPage}
      about="The providers modules reach language models through, each under the name modules use for it. Saving rebuilds the providers and restarts the running modules that use one. If one of those modules cannot be rebuilt, nothing is saved."
    />
  ),
});
