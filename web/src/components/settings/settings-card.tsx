import { useForm } from "@tanstack/react-form";
import { useInfiniteQuery, useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { formatDistanceToNowStrict } from "date-fns";
import { History, RotateCcw } from "lucide-react";
import { useState } from "react";
import { ApiError, api, data, type Schemas } from "@/api/client";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Sheet,
  SheetDescription,
  SheetHeader,
  SheetPanel,
  SheetPopup,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { toastManager } from "@/components/ui/toast";
import { revisionsQuery, settingsQuery } from "@/queries/modules";
import { changes, clean, type JSONSchema } from "./schema";
import { SchemaFields, type SettingsForm } from "./schema-form";

/** SettingsCard edits a module's settings: Save reviews what changes,
 * then saves it as a new revision, which the bot applies at once. */
export function SettingsCard({ module }: { module: string }) {
  const { data: current } = useSuspenseQuery(settingsQuery(module));
  // A new revision remounts the form with what was saved.
  return <SettingsEditor key={current.revision} module={module} current={current} />;
}

type Change = ReturnType<typeof changes>[number];

function SettingsEditor({ module, current }: { module: string; current: Schemas["SettingsOutputBody"] }) {
  const queryClient = useQueryClient();
  const root = current.schema as JSONSchema;
  const [review, setReview] = useState<Change[] | null>(null);
  const save = useMutation({
    mutationFn: (settings: unknown) =>
      data(
        api.PUT("/api/modules/{module}/settings", {
          params: { path: { module }, header: { "If-Match": `"${current.revision}"` } },
          body: settings as never,
        }),
      ),
    onSuccess: async (r) => {
      setReview(null);
      toastManager.add({
        type: "success",
        title: `Saved as revision ${r.revision}`,
        description: "The bot runs with it now.",
      });
      await queryClient.invalidateQueries({ queryKey: ["modules"] });
      await queryClient.invalidateQueries({ queryKey: ["events"] });
    },
    onError: (error) => {
      setReview(null);
      // Settings the server refused name their fields.
      if (error instanceof ApiError && error.problem?.errors) {
        for (const e of error.problem.errors) {
          if (e.location) {
            form.setFieldMeta(e.location as never, (meta) => ({
              ...meta,
              errorMap: { ...meta.errorMap, onServer: e.message },
            }));
          }
        }
      }
    },
  });
  const form = useForm({
    defaultValues: (current.settings ?? {}) as Record<string, unknown>,
    onSubmit: ({ value }) => setReview(changes(clean(current.settings ?? {}), clean(value))),
  });
  return (
    <section className="surface-raised rounded-2xl">
      <header className="flex items-center gap-3 px-4 pt-3.5 pb-2.5">
        <h2 className="font-semibold text-[15px]">Configuration</h2>
        <span className="text-muted-foreground text-xs">
          {current.revision ? `Revision ${current.revision}` : "Never saved"}
          {current.by && `, by ${current.by}`}
        </span>
        <HistorySheet module={module} current={current.revision} />
      </header>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void form.handleSubmit();
        }}
      >
        <div className="mx-3.5 rounded-xl bg-background px-3.5 shadow-[0_0_0_1px_--theme(--color-black/6%)]">
          <SchemaFields form={form as unknown as SettingsForm} root={root} schema={root} path="" />
        </div>
        <footer className="flex items-center justify-end gap-2 px-4 py-3">
          <form.Subscribe selector={(s) => s.isDirty}>
            {(dirty) => (
              <>
                {dirty && (
                  <Button variant="ghost" onClick={() => form.reset()}>
                    Discard
                  </Button>
                )}
                <Button type="submit" disabled={!dirty}>
                  Save…
                </Button>
              </>
            )}
          </form.Subscribe>
        </footer>
      </form>
      <Dialog open={review !== null} onOpenChange={(open) => !open && setReview(null)}>
        <DialogPopup className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>Apply these changes?</DialogTitle>
            <DialogDescription>
              They are saved as revision {current.revision + 1}, and the bot swaps in the module with them at once.
            </DialogDescription>
          </DialogHeader>
          <DialogPanel>
            {review?.length === 0 ? (
              <p className="text-muted-foreground text-sm">Nothing changes.</p>
            ) : (
              <ul className="flex flex-col gap-2">
                {review?.map((c) => (
                  <li key={c.path} className="surface-inset rounded-lg px-3 py-2 text-sm">
                    <div className="font-medium font-mono text-xs">{c.path}</div>
                    <div className="mt-1 grid grid-cols-[auto_1fr] gap-x-2 text-xs">
                      <span className="text-muted-foreground">was</span>
                      <Value v={c.from} />
                      <span className="text-muted-foreground">now</span>
                      <Value v={c.to} />
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </DialogPanel>
          <DialogFooter>
            <DialogClose render={<Button variant="ghost" />}>Keep editing</DialogClose>
            <Button
              loading={save.isPending}
              disabled={!review?.length}
              onClick={() => save.mutate(clean(form.state.values))}
            >
              Apply
            </Button>
          </DialogFooter>
        </DialogPopup>
      </Dialog>
    </section>
  );
}

function Value({ v }: { v: unknown }) {
  if (v === undefined) return <span className="text-muted-foreground italic">default</span>;
  const text = typeof v === "string" ? v : JSON.stringify(v);
  return <span className="line-clamp-3 whitespace-pre-wrap break-all font-mono">{text}</span>;
}

function HistorySheet({ module, current }: { module: string; current: number }) {
  const queryClient = useQueryClient();
  const history = useInfiniteQuery({ ...revisionsQuery(module), enabled: current > 0 });
  const restore = useMutation({
    mutationFn: (revision: number) =>
      data(
        api.POST("/api/modules/{module}/settings/revisions/{revision}/restore", {
          params: { path: { module, revision }, header: { "If-Match": `"${current}"` } },
        }),
      ),
    onSuccess: async (r) => {
      toastManager.add({ type: "success", title: `Restored as revision ${r.revision}` });
      await queryClient.invalidateQueries({ queryKey: ["modules"] });
    },
  });
  const revisions = history.data?.pages.flatMap((p) => p.revisions ?? []) ?? [];
  return (
    <Sheet>
      <SheetTrigger render={<Button variant="ghost" size="sm" className="ml-auto" disabled={current === 0} />}>
        <History /> History
      </SheetTrigger>
      <SheetPopup>
        <SheetHeader>
          <SheetTitle>Settings history</SheetTitle>
          <SheetDescription>
            Every saved revision. Restoring one saves its settings again as a new revision.
          </SheetDescription>
        </SheetHeader>
        <SheetPanel>
          <ol className="flex flex-col gap-2">
            {revisions.map((r) => (
              <li key={r.revision} className="surface-inset flex items-center gap-3 rounded-xl px-3 py-2.5">
                <span className="grid size-8 place-items-center rounded-lg bg-background font-semibold text-xs tabular shadow-[0_0_0_1px_--theme(--color-black/6%)]">
                  {r.revision}
                </span>
                <div className="min-w-0 flex-1 text-sm">
                  <div className="font-medium">
                    {r.by || "Unknown"}
                    {!r.enabled && <span className="ml-2 font-normal text-muted-foreground text-xs">off</span>}
                  </div>
                  <time className="text-muted-foreground text-xs" dateTime={r.at}>
                    {r.at && formatDistanceToNowStrict(new Date(r.at), { addSuffix: true })}
                  </time>
                </div>
                {r.revision === current ? (
                  <span className="text-muted-foreground text-xs">Current</span>
                ) : (
                  <Button
                    variant="outline"
                    size="sm"
                    loading={restore.isPending && restore.variables === r.revision}
                    onClick={() => restore.mutate(r.revision)}
                  >
                    <RotateCcw /> Restore
                  </Button>
                )}
              </li>
            ))}
          </ol>
          {history.hasNextPage && (
            <Button
              variant="ghost"
              className="mt-2 w-full"
              loading={history.isFetchingNextPage}
              onClick={() => void history.fetchNextPage()}
            >
              Older
            </Button>
          )}
        </SheetPanel>
      </SheetPopup>
    </Sheet>
  );
}
