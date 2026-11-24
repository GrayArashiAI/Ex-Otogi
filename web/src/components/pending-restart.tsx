import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { RotateCw } from "lucide-react";
import { Fragment, useEffect, useRef, useState } from "react";
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
import { Spinner } from "@/components/ui/spinner";
import { toastManager } from "@/components/ui/toast";
import { pendingRestart, type Section } from "@/lib/restart";
import { titlesList } from "@/lib/secrets";
import { accountsPage, sectionPage } from "@/lib/settings";
import { took, useNow } from "@/lib/time";
import { restart, restartKey, sectionsQuery, untilBack } from "@/queries/system";

const list = new Intl.ListFormat("en");

const titles = (sections: Section[]) => sections.map((s) => sectionPage(`system:${s.id}`)?.title ?? s.id);

/** PendingRestart says which sections have a revision saved that the bot
 * has not started with, and restarts the bot: it asks first, then waits for
 * the bot to come back, for as long as that takes. Nothing waiting, it is
 * not there. */
export function PendingRestart() {
  const queryClient = useQueryClient();
  const { data: sections = [] } = useQuery(sectionsQuery);
  const pending = pendingRestart(sections);
  const [open, setOpen] = useState(false);
  const wait = useMutation({
    mutationKey: restartKey,
    mutationFn: untilBack,
    onSuccess: async () => {
      // Everything on screen is from before the restart.
      await queryClient.invalidateQueries();
      const still = pendingRestart(queryClient.getQueryData(sectionsQuery.queryKey) ?? []);
      setOpen(false);
      toastManager.add(
        still.length > 0
          ? {
              type: "warning",
              title: "The bot is back, with changes still waiting",
              description: `${titlesList(titles(still))} ${still.length === 1 ? "has" : "have"} a revision newer than the one it started with.`,
            }
          : { type: "success", title: "The bot is back", description: "It runs with the saved settings." },
      );
    },
  });
  const ask = useMutation({
    // The dialog shows why the bot could not be asked.
    meta: { quiet: true },
    mutationFn: restart,
    onSuccess: (startedAt) => wait.mutate(startedAt),
  });
  const waiting = wait.isPending;
  const now = useNow(waiting);
  // The Restart button the focus was on goes with the question.
  const progress = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    if (waiting) progress.current?.focus();
  }, [waiting]);

  return (
    <>
      {pending.length > 0 && (
        <Notice
          tone="warning"
          title="Changes are waiting for a restart"
          action={
            <Button
              variant="outline"
              onClick={() => {
                ask.reset();
                setOpen(true);
              }}
            >
              <RotateCw /> Restart…
            </Button>
          }
        >
          <p className="mt-0.5">
            {list.formatToParts(titles(pending)).map((part, i) => {
              const page = part.type === "element" && sectionPage(`system:${pending[i / 2]?.id}`);
              return page ? (
                <Link
                  key={page.to}
                  to={page.to}
                  className="hit-target relative rounded-sm font-medium text-foreground underline decoration-foreground/24 outline-none hover:decoration-foreground focus-visible:ring-2 focus-visible:ring-ring"
                >
                  {part.value}
                </Link>
              ) : (
                <Fragment key={part.value + String(i)}>{part.value}</Fragment>
              );
            })}{" "}
            {pending.length === 1 ? "has" : "have"} saved settings the bot has not started with: it runs with the ones
            it started with until it restarts.
            {pending.some((s) => s.id === accountsPage.section) && (
              <>
                {" "}
                A Telegram account added since has to be logged in first, with{" "}
                <span className="font-mono text-[13px]">otogi login &lt;account&gt;</span> on the server.
              </>
            )}
          </p>
        </Notice>
      )}
      <Dialog open={open} onOpenChange={(to) => !to && !ask.isPending && !waiting && setOpen(false)}>
        <DialogPopup
          className="sm:max-w-md"
          showCloseButton={!waiting}
          // Once nothing waits, the button the dialog opened from is gone.
          finalFocus={() => (pending.length > 0 ? true : document.getElementById("content"))}
        >
          <DialogHeader>
            <DialogTitle className="pe-8 leading-tight">
              {waiting ? "Restarting the bot" : "Restart the bot?"}
            </DialogTitle>
            <DialogDescription>
              The bot finishes what it is handling, stops, and its supervisor starts it again, with the saved settings.
              Chats get no answers meanwhile, and this panel loads nothing.
            </DialogDescription>
          </DialogHeader>
          <DialogPanel className="flex flex-col gap-2.5">
            {ask.error && (
              <Notice role="alert" title="The bot was not asked to restart">
                <p className="mt-0.5">{ask.error.message}</p>
              </Notice>
            )}
            {waiting && (
              <div className="surface-inset flex items-center gap-2.5 rounded-xl px-3.5 py-3 text-sm">
                <Spinner className="size-4 flex-none" role="presentation" aria-label={undefined} aria-hidden />
                <p ref={progress} tabIndex={-1} className="min-w-0 flex-1 font-medium outline-none">
                  Waiting for the bot to answer again
                </p>
                <span className="text-muted-foreground tabular">{took(now - wait.submittedAt)}</span>
              </div>
            )}
            {/* Here before the wait, so that what it says once the wait starts is announced. */}
            <p role="status" className="text-muted-foreground text-sm">
              {waiting
                ? "If the bot does not come back, nothing on the server started it again, and it has to be started there. This keeps waiting however long that takes, and carries on once the bot answers again."
                : "If nothing supervises the process, it stays down, and has to be started on the server."}
            </p>
          </DialogPanel>
          {!waiting && (
            <DialogFooter>
              <DialogClose render={<Button variant="ghost" />}>Cancel</DialogClose>
              <Button loading={ask.isPending} onClick={() => ask.mutate()}>
                {ask.error ? "Try again" : "Restart"}
              </Button>
            </DialogFooter>
          )}
        </DialogPopup>
      </Dialog>
    </>
  );
}
