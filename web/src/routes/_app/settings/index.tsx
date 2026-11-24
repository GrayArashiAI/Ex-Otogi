import { createFileRoute, redirect } from "@tanstack/react-router";
import { settingsPages } from "@/lib/settings";

export const Route = createFileRoute("/_app/settings/")({
  // On a wide screen the list sits beside the first page; on a phone the
  // list is the page.
  beforeLoad: () => {
    if (window.matchMedia("(min-width: 768px)").matches) throw redirect({ to: settingsPages[0].to });
  },
});
