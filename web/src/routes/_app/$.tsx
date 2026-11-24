import { createFileRoute } from "@tanstack/react-router";
import { PageNotFound } from "@/components/route-states";

// Any address no other page has: said inside the shell, so the way back stays.
export const Route = createFileRoute("/_app/$")({
  head: () => ({ meta: [{ title: "No such page · otogi" }] }),
  component: PageNotFound,
});
