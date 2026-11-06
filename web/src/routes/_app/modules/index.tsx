import { createFileRoute, redirect } from "@tanstack/react-router";
import { modulesQuery } from "@/queries/modules";
import { byUse } from "../modules";

export const Route = createFileRoute("/_app/modules/")({
  // On a wide screen the list sits beside the first module; on a phone the
  // list is the page.
  beforeLoad: async ({ context }) => {
    if (!window.matchMedia("(min-width: 768px)").matches) return;
    const first = byUse(await context.queryClient.ensureQueryData(modulesQuery))[0];
    if (first) throw redirect({ to: "/modules/$module", params: { module: first.id } });
  },
});
