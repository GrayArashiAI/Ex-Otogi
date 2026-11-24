import { getBy, useForm, useStore } from "@tanstack/react-form";
import { useInfiniteQuery, useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { useBlocker } from "@tanstack/react-router";
import { format, formatDistanceToNowStrict } from "date-fns";
import { History, RotateCcw } from "lucide-react";
import { type ReactNode, useEffect, useEffectEvent, useId, useMemo, useState } from "react";
import { ApiError, api, data, type Schemas } from "@/api/client";
import { Card, CardHeader } from "@/components/card";
import { Notice } from "@/components/notice";
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
import { cn } from "@/lib/utils";
import { revisionsQuery, settingsQuery } from "@/queries/modules";
import { type Change, changes, clean, type JSONSchema, type Problem, pathLabel, properties, validate } from "./schema";
import { controlId, SchemaFields, SettingsContext, type SettingsForm, useReveal } from "./schema-form";

type Current = Schemas["SettingsOutputBody"];
type Revision = Schemas["Revision"];

const count = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** SettingsCard edits a module's settings: Save reviews what changes,
 * then saves it as a new revision, which the bot applies at once. */
export function SettingsCard({ module }: { module: string }) {
  const queryClient = useQueryClient();
  const { data: current } = useSuspenseQuery(settingsQuery(module));
  const root = current.schema as JSONSchema;
  /** keep makes a revision the server just saved the current settings. */
  const keep = (saved: Revision): Current => {
    const next = { ...current, ...saved };
    queryClient.setQueryData(settingsQuery(module).queryKey, next);
    void queryClient.invalidateQueries({ queryKey: ["modules"] });
    void queryClient.invalidateQueries({ queryKey: ["events"] });
    // The secrets list says which modules' settings refer to each.
    void queryClient.invalidateQueries({ queryKey: ["secrets"] });
    return next;
  };
  if (properties(root).length > 0) {
    // Each module has its own form; a new revision of one does not remount it.
    return <SettingsEditor key={module} module={module} current={current} keep={keep} />;
  }
  return (
    <Card>
      <SettingsHeader current={current}>
        <HistorySheet current={current} unsaved={0} onRestored={keep} />
      </SettingsHeader>
      <p className="px-4 pb-4 text-muted-foreground text-sm">This module has no settings.</p>
    </Card>
  );
}

function SettingsHeader({ current, children }: { current: Current; children: ReactNode }) {
  return (
    <CardHeader
      title="Settings"
      note={`${current.revision ? `Revision ${current.revision}` : "Never saved"}${current.by ? `, by ${current.by}` : ""}`}
    >
      {children}
    </CardHeader>
  );
}

/** Refusal is a problem the server found, and the value it found it in:
 * it stands until that value changes. */
type Refusal = Problem & { value: string };

function SettingsEditor({
  module,
  current: fetched,
  keep,
}: {
  module: string;
  current: Current;
  keep: (saved: Revision) => Current;
}) {
  const queryClient = useQueryClient();
  const root = fetched.schema as JSONSchema;
  const id = useId();
  // base is the revision the edits started from. The current one runs ahead
  // of it when someone else saves meanwhile, and is base itself for the
  // moment after a save, until the query has it.
  const [base, setBase] = useState(fetched);
  const current = fetched.revision > base.revision ? fetched : base;
  const [review, setReview] = useState(false);
  // attempted: a save was stopped by problems, which now all show.
  const [attempted, setAttempted] = useState(false);
  const [refused, setRefused] = useState<Refusal[]>([]);
  const form = useForm({ defaultValues: (base.settings ?? {}) as Record<string, unknown> });
  const values = useStore(form.store, (s) => s.values);
  const fieldMeta = useStore(form.store, (s) => s.fieldMeta as Record<string, { isTouched: boolean } | undefined>);
  const { reveal, ...view } = useReveal(id);

  const cleaned = useMemo(() => clean(root, values), [root, values]);
  const diff = useMemo(() => changes(clean(root, current.settings ?? {}), cleaned), [root, current, cleaned]);
  const untouched = useMemo(
    () => changes(clean(root, base.settings ?? {}), cleaned).length === 0,
    [root, base, cleaned],
  );
  const invalid = useMemo(() => validate(root, values), [root, values]);
  const valueAt = (path: string) => JSON.stringify(path ? getBy(values, path) : values);
  const standing = refused.filter((p) => valueAt(p.path) === p.value);
  const problems = [...standing, ...invalid.filter((p) => attempted || fieldMeta[p.path]?.isTouched)];
  const summary = [...standing, ...(attempted ? invalid : [])];
  const dirty = diff.length > 0;
  const stale = current !== base;

  /** load starts the form over from a revision. */
  const load = (from: Current) => {
    form.reset((from.settings ?? {}) as Record<string, unknown>);
    setBase(from);
    setReview(false);
    setAttempted(false);
    setRefused([]);
  };
  // Settings saved elsewhere replace a form with nothing to lose.
  const follow = useEffectEvent(() => load(current));
  useEffect(() => {
    if (stale && (untouched || !dirty)) follow();
  }, [stale, untouched, dirty]);

  const save = useMutation({
    // The review dialog shows why a save failed.
    meta: { quiet: true },
    mutationFn: () =>
      data(
        api.PUT("/api/modules/{module}/settings", {
          params: { path: { module }, header: { "If-Match": `"${current.revision}"` } },
          body: cleaned as never,
        }),
      ),
    onSuccess: (saved) => {
      load(keep(saved));
      toastManager.add({
        type: "success",
        title: `Saved as revision ${saved.revision}`,
        description: "The bot runs with it now.",
      });
    },
    onError: async (error) => {
      if (error instanceof ApiError && error.status === 412) {
        // Someone saved first: their settings become what the changes are against.
        await queryClient.invalidateQueries({ queryKey: settingsQuery(module).queryKey });
        return;
      }
      // Settings the server refused name their fields; a refusal that names none is the whole form's.
      const errors = (error instanceof ApiError && error.problem?.errors) || [{ location: "", message: error.message }];
      setRefused(
        errors.map((e) => ({
          path: e.location ?? "",
          message: e.message ?? error.message,
          value: valueAt(e.location ?? ""),
        })),
      );
    },
  });
  const failure = save.error && !(save.error instanceof ApiError && save.error.status === 412) ? save.error : null;

  const submit = () => {
    if (!dirty || review) return;
    const first = invalid[0];
    setAttempted(Boolean(first));
    if (first) {
      reveal(first.path);
      return;
    }
    save.reset();
    setReview(true);
  };
  const onKey = useEffectEvent((e: KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      submit();
    }
  });
  useEffect(() => {
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const leaving = useBlocker({
    shouldBlockFn: ({ current: here, next }) => dirty && here.pathname !== next.pathname,
    enableBeforeUnload: () => dirty,
    withResolver: true,
  });

  return (
    <Card>
      <SettingsHeader current={current}>
        <HistorySheet current={current} unsaved={diff.length} onRestored={(saved) => load(keep(saved))} />
      </SettingsHeader>
      <form
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        {summary.length > 0 && (
          <Notice
            id={controlId(id, "")}
            tabIndex={-1}
            className="mx-3.5 mb-2.5 outline-none"
            title={
              standing.length > 0
                ? "The bot refused these settings"
                : `${count(summary.length, "field")} to fix before saving`
            }
          >
            <ul className="mt-1 flex flex-col items-start gap-0.5">
              {summary.map((p) => (
                <li key={`${p.path} ${p.message}`}>
                  {p.path ? (
                    <button
                      type="button"
                      className="rounded-sm text-left underline decoration-destructive/36 underline-offset-3 outline-none hover:decoration-destructive focus-visible:ring-2 focus-visible:ring-ring"
                      onClick={() => reveal(p.path)}
                    >
                      <span className="font-medium text-foreground">{pathLabel(p.path)}</span>: {p.message}
                    </button>
                  ) : (
                    p.message
                  )}
                </li>
              ))}
            </ul>
          </Notice>
        )}
        <div className="mx-3.5 rounded-xl bg-background px-3.5 shadow-[0_0_0_1px_--theme(--color-black/6%)]">
          <SettingsContext
            value={{ form: form as unknown as SettingsForm, root, id, saved: base.settings, problems, reveal, ...view }}
          >
            <SchemaFields schema={root} path="" />
          </SettingsContext>
        </div>
        <footer
          className={cn(
            "flex items-center justify-end gap-2 rounded-b-2xl px-4 py-3",
            // With changes, the bar stays in reach while the card is on screen.
            dirty &&
              "sticky bottom-0 z-10 bg-card/88 shadow-[0_-1px_0_--theme(--color-black/6%)] backdrop-blur-md max-sm:pb-[max(--spacing(3),env(safe-area-inset-bottom))]",
          )}
        >
          <span className="mr-auto text-muted-foreground text-sm tabular" aria-live="polite">
            {dirty && count(diff.length, "unsaved change")}
          </span>
          {dirty && (
            <Button variant="ghost" onClick={() => load(current)}>
              Discard
            </Button>
          )}
          <Button type="submit" variant={dirty ? "default" : "outline"} disabled={!dirty}>
            Save…
          </Button>
        </footer>
      </form>
      <Dialog open={review} onOpenChange={(open) => !open && setReview(false)}>
        <DialogPopup
          className="sm:max-w-xl"
          // Back in the form after a refusal, the focus goes to what was refused.
          finalFocus={() => {
            if (!standing[0]) return true;
            reveal(standing[0].path);
            return false;
          }}
        >
          <DialogHeader>
            <DialogTitle>Apply these changes?</DialogTitle>
            <DialogDescription>
              They are saved as revision {current.revision + 1}, and the bot swaps in the module with them at once.
            </DialogDescription>
          </DialogHeader>
          <DialogPanel className="flex flex-col gap-2.5">
            {failure && (
              <Notice
                title={
                  failure instanceof ApiError && failure.status === 422
                    ? "The bot refused these settings, and nothing changed"
                    : "These settings could not be saved"
                }
              >
                {standing.some((p) => p.path) ? (
                  <ul className="mt-1 flex flex-col gap-0.5">
                    {standing.map((p) => (
                      <li key={`${p.path} ${p.message}`}>
                        <span className="font-medium text-foreground">{pathLabel(p.path)}</span>: {p.message}
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="mt-0.5">{failure.message}</p>
                )}
              </Notice>
            )}
            {stale && (
              <Notice tone="warning" title={`${current.by || "Someone"} saved revision ${current.revision} meanwhile`}>
                <p className="mt-0.5">
                  Your changes are kept, and are now compared with that revision: Apply saves the settings as you have
                  them here, over it.
                </p>
              </Notice>
            )}
            <ChangeList changes={diff} />
          </DialogPanel>
          <DialogFooter>
            <DialogClose render={<Button variant="ghost" />}>Keep editing</DialogClose>
            <Button loading={save.isPending} disabled={!dirty} onClick={() => save.mutate()}>
              {failure ? "Try again" : "Apply"}
            </Button>
          </DialogFooter>
        </DialogPopup>
      </Dialog>
      <Dialog open={leaving.status === "blocked"} onOpenChange={(open) => !open && leaving.reset?.()}>
        <DialogPopup className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Leave without saving?</DialogTitle>
            <DialogDescription>
              {count(diff.length, "change")} to these settings {diff.length === 1 ? "is" : "are"} not saved, and leaving
              discards {diff.length === 1 ? "it" : "them"}.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose render={<Button variant="ghost" />}>Keep editing</DialogClose>
            <Button variant="destructive" onClick={() => leaving.proceed?.()}>
              Discard and leave
            </Button>
          </DialogFooter>
        </DialogPopup>
      </Dialog>
    </Card>
  );
}

/** ChangeList shows what a save or a restore changes, field by field. */
function ChangeList({ changes: list }: { changes: Change[] }) {
  if (list.length === 0) return <p className="text-muted-foreground text-sm">Nothing changes.</p>;
  return (
    <ul className="flex flex-col gap-2">
      {list.map((c) => {
        // A list item that is not there was added or removed; a field that is not set has its default.
        const unset = c.path.endsWith("]") ? "none" : "default";
        return (
          <li key={c.path} className="surface-inset rounded-lg px-3 py-2 text-sm">
            <div className="font-medium font-mono text-xs">{c.path}</div>
            <div className="mt-1 grid grid-cols-[auto_1fr] gap-x-2 text-xs">
              <span className="text-muted-foreground">was</span>
              <Value v={c.from} unset={unset} />
              <span className="text-muted-foreground">now</span>
              <Value v={c.to} unset={unset} />
            </div>
          </li>
        );
      })}
    </ul>
  );
}

function Value({ v, unset }: { v: unknown; unset: string }) {
  if (v === undefined) return <span className="text-muted-foreground italic">{unset}</span>;
  const text = typeof v === "string" ? v : JSON.stringify(v);
  return <span className="line-clamp-3 whitespace-pre-wrap break-all font-mono">{text}</span>;
}

function HistorySheet({
  current,
  unsaved,
  onRestored,
}: {
  current: Current;
  /** unsaved is how many changes the form has that a restore discards. */
  unsaved: number;
  onRestored: (saved: Revision) => void;
}) {
  const queryClient = useQueryClient();
  const module = current.module;
  const root = current.schema as JSONSchema;
  const history = useInfiniteQuery({ ...revisionsQuery(module), enabled: current.revision > 0 });
  const [picked, setPicked] = useState<Revision | null>(null);
  const restore = useMutation({
    mutationFn: (revision: number) =>
      data(
        api.POST("/api/modules/{module}/settings/revisions/{revision}/restore", {
          params: { path: { module, revision }, header: { "If-Match": `"${current.revision}"` } },
        }),
      ),
    onSuccess: (saved) => {
      setPicked(null);
      onRestored(saved);
      toastManager.add({
        type: "success",
        title: `Restored as revision ${saved.revision}`,
        description: "The bot runs with it now.",
      });
    },
    // A restore refused because someone saved first is tried again on what they saved.
    onError: () => queryClient.invalidateQueries({ queryKey: ["modules", module] }),
  });
  const revisions = history.data?.pages.flatMap((p) => p.revisions ?? []) ?? [];
  const diff = picked ? changes(clean(root, current.settings ?? {}), clean(root, picked.settings ?? {})) : [];
  return (
    <Sheet>
      <SheetTrigger render={<Button variant="ghost" size="sm" disabled={current.revision === 0} />}>
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
                    {!r.enabled && <span className="ml-2 font-normal text-muted-foreground text-xs">Off</span>}
                  </div>
                  {r.at && (
                    <time
                      className="text-muted-foreground text-xs"
                      dateTime={r.at}
                      title={format(new Date(r.at), "MMM d, yyyy HH:mm:ss")}
                    >
                      {formatDistanceToNowStrict(new Date(r.at), { addSuffix: true })}
                    </time>
                  )}
                </div>
                {r.revision === current.revision ? (
                  <span className="text-muted-foreground text-xs">Current</span>
                ) : (
                  <Button variant="outline" size="sm" onClick={() => setPicked(r)}>
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
        <Dialog open={picked !== null} onOpenChange={(open) => !open && setPicked(null)}>
          <DialogPopup className="sm:max-w-xl">
            <DialogHeader>
              <DialogTitle>Restore revision {picked?.revision}?</DialogTitle>
              <DialogDescription>
                Its settings are saved again as revision {current.revision + 1}, and the bot swaps in the module with
                them at once.
              </DialogDescription>
            </DialogHeader>
            <DialogPanel className="flex flex-col gap-2.5">
              {unsaved > 0 && (
                <Notice
                  tone="warning"
                  title={`Restoring discards the ${count(unsaved, "unsaved change")} in the form`}
                />
              )}
              <ChangeList changes={diff} />
            </DialogPanel>
            <DialogFooter>
              <DialogClose render={<Button variant="ghost" />}>Cancel</DialogClose>
              <Button
                loading={restore.isPending}
                disabled={diff.length === 0}
                onClick={() => picked && restore.mutate(picked.revision)}
              >
                Restore
              </Button>
            </DialogFooter>
          </DialogPopup>
        </Dialog>
      </SheetPopup>
    </Sheet>
  );
}
