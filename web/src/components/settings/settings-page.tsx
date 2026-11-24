import { Link } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { type ReactNode, Suspense } from "react";
import { Tile } from "@/components/tile";
import { Skeleton } from "@/components/ui/skeleton";
import type { generalPage, llmPage, settingsPages } from "@/lib/settings";
import { SettingsCard } from "./settings-card";

/** SettingsPage is a page under Settings: its heading, as the Settings list
 * names it, what the page is about, and an action beside them. */
export function SettingsPage({
  page,
  about,
  action,
  children,
}: {
  page: (typeof settingsPages)[number];
  about: ReactNode;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="min-w-0 px-5 py-6 sm:px-10 sm:py-7">
      <Link
        to="/settings"
        activeOptions={{ exact: true }}
        className="hit-target relative mb-4 inline-flex items-center gap-1 rounded-md text-muted-foreground text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring md:hidden"
      >
        <ArrowLeft className="size-4" /> Settings
      </Link>
      <div className="flex flex-wrap items-start gap-x-4 gap-y-3">
        <Tile tone={page.tone} icon={page.icon} size="lg" />
        <div className="min-w-0 flex-1 basis-64">
          <h1 className="font-bold text-2xl leading-tight tracking-[-0.035em] sm:text-[1.875rem]">{page.title}</h1>
          <p className="max-w-[70ch] text-muted-foreground text-sm">{about}</p>
        </div>
        {action}
      </div>
      {children}
    </div>
  );
}

/** SectionPage edits one section of the settings outside modules, with the
 * settings card modules have. */
export function SectionPage({ page, about }: { page: typeof generalPage | typeof llmPage; about: ReactNode }) {
  return (
    <SettingsPage page={page} about={about}>
      <div className="mt-6 max-w-4xl">
        <Suspense fallback={<Skeleton className="h-96 w-full rounded-2xl" />}>
          <SettingsCard of={{ section: page.section }} effect={page.effect} />
        </Suspense>
      </div>
    </SettingsPage>
  );
}
