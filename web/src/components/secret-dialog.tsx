import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff } from "lucide-react";
import { useId, useState } from "react";
import { ApiError, api, data, type Schemas } from "@/api/client";
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
import { Input } from "@/components/ui/input";
import { toastManager } from "@/components/ui/toast";
import { nameProblem, reference, restartNote, titlesList } from "@/lib/secrets";
import { llmPage, referenceTitle, sectionPage } from "@/lib/settings";
import { modulesQuery } from "@/queries/modules";
import { secretsQuery } from "@/queries/secrets";

type Secret = Schemas["Secret"];

/** referenceTitles names in a sentence the modules and sections that refer
 * to a secret, or that it restarted: Decisions and Language models. */
export function referenceTitles(ids: string[]): string {
  return titlesList(ids.map(referenceTitle));
}

/** SecretDialog sets a secret's value: a new secret's, or the one given.
 * The value leaves with the dialog and is never shown again. */
export function SecretDialog({
  secret,
  open,
  onOpenChange,
  onSaved,
  finalFocus,
}: {
  /** secret is the one to give a value; without it, the dialog adds a new one. */
  secret?: Secret;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved?: (name: string) => void;
  finalFocus?: React.RefObject<HTMLElement | null>;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogPopup className="sm:max-w-md" finalFocus={finalFocus}>
        {/* The form lives only while the dialog is open, and its value with it. */}
        {open && <SecretForm secret={secret} onSaved={onSaved} close={() => onOpenChange(false)} />}
      </DialogPopup>
    </Dialog>
  );
}

function SecretForm({
  secret,
  onSaved,
  close,
}: {
  secret?: Secret;
  onSaved?: (name: string) => void;
  close: () => void;
}) {
  const id = useId();
  const queryClient = useQueryClient();
  const { data: secrets = [] } = useQuery(secretsQuery);
  const { data: modules = [] } = useQuery(modulesQuery);
  const [typed, setTyped] = useState("");
  const [value, setValue] = useState("");
  const [shown, setShown] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const name = secret?.name ?? typed;
  // A new name can be one settings refer to already, from the environment or from nowhere.
  const known = secret ?? secrets.find((s) => s.name === name);
  const replaces = secret?.source === "panel";
  const references = known?.references ?? [];
  const restarts = references.filter((id) => modules.some((m) => m.id === id && m.running));
  const nameError = secret ? undefined : nameProblem(typed, secrets);
  const valueError = value ? undefined : "Required";

  const save = useMutation({
    // The dialog shows why a save failed, and the value does not outlive it.
    meta: { quiet: true },
    gcTime: 0,
    mutationFn: () => data(api.PUT("/api/secrets/{name}", { params: { path: { name } }, body: { value } })),
    onSuccess: async ({ restarted }) => {
      await queryClient.invalidateQueries({ queryKey: ["secrets"] });
      void queryClient.invalidateQueries({ queryKey: ["modules"] });
      void queryClient.invalidateQueries({ queryKey: ["events"] });
      toastManager.add({
        type: "success",
        title: `${replaces ? "Replaced" : secret ? "Set" : "Added"} ${name}`,
        description: restarted.length > 0 ? `${referenceTitles(restarted)} restarted with it.` : undefined,
      });
      onSaved?.(name);
      close();
    },
  });

  return (
    <form
      noValidate
      className="contents"
      onSubmit={(e) => {
        e.preventDefault();
        // Inside a settings form, this is not that form's submit.
        e.stopPropagation();
        setAttempted(true);
        if (nameError || valueError) {
          document.getElementById(nameError ? `${id}-name` : `${id}-value`)?.focus();
          return;
        }
        save.mutate();
      }}
    >
      <DialogHeader>
        <DialogTitle className="wrap-anywhere pe-8 leading-tight">
          {!secret ? (
            "Add a secret"
          ) : (
            <>
              {replaces ? "Replace" : "Set a value for"} <span className="font-mono text-lg">{name}</span>
            </>
          )}
        </DialogTitle>
        <DialogDescription>
          The value is stored on the server and never shown again: to change it, replace it.
          {known?.source === "environment" && " It wins over the environment variable of this name."}
        </DialogDescription>
      </DialogHeader>
      <DialogPanel className="flex flex-col gap-4">
        {save.error && (
          <Notice
            id={`${id}-failure`}
            role="alert"
            title={
              save.error instanceof ApiError && save.error.status === 422
                ? "The bot refused this value, and nothing changed"
                : "The secret could not be saved"
            }
          >
            <p className="mt-0.5">{save.error.message}</p>
          </Notice>
        )}
        {!secret && (
          <div className="flex flex-col gap-1.5">
            <label htmlFor={`${id}-name`} className="font-medium text-sm">
              Name
            </label>
            <Input
              id={`${id}-name`}
              className="font-mono text-[13px]"
              value={typed}
              placeholder="TODOU_TOKEN"
              autoComplete="off"
              spellCheck={false}
              aria-required
              aria-invalid={attempted && nameError ? true : undefined}
              aria-describedby={`${id}-name-note`}
              onChange={(e) => setTyped(e.target.value.trim())}
            />
            <p
              id={`${id}-name-note`}
              className={
                attempted && nameError ? "text-destructive-foreground text-xs" : "text-muted-foreground text-xs"
              }
            >
              {attempted && nameError ? (
                nameError
              ) : (
                <>
                  Settings refer to it as <span className="font-mono">{reference(typed || "NAME")}</span>.
                </>
              )}
            </p>
          </div>
        )}
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${id}-value`} className="font-medium text-sm">
            {replaces ? "New value" : "Value"}
          </label>
          <div className="relative">
            <Input
              id={`${id}-value`}
              type={shown ? "text" : "password"}
              className="font-mono text-[13px] [&_input]:pe-10"
              value={value}
              autoComplete="off"
              spellCheck={false}
              aria-required
              aria-invalid={(attempted && valueError) || save.error ? true : undefined}
              aria-describedby={
                [attempted && valueError && `${id}-value-error`, save.error && `${id}-failure`]
                  .filter(Boolean)
                  .join(" ") || undefined
              }
              onChange={(e) => setValue(e.target.value)}
            />
            <Button
              variant="ghost"
              size="icon-sm"
              className="absolute end-0.5 top-1/2 -translate-y-1/2 text-muted-foreground"
              aria-label="Show the value"
              aria-pressed={shown}
              onClick={() => setShown((s) => !s)}
            >
              {shown ? <EyeOff /> : <Eye />}
            </Button>
          </div>
          {attempted && valueError && (
            <p id={`${id}-value-error`} className="text-destructive-foreground text-xs">
              {valueError}
            </p>
          )}
        </div>
        <p className="text-muted-foreground text-sm">
          {restartNote(
            restarts.map(referenceTitle),
            references.some((id) => sectionPage(id) === llmPage),
          )}
        </p>
      </DialogPanel>
      <DialogFooter>
        <DialogClose render={<Button variant="ghost" />}>Cancel</DialogClose>
        <Button type="submit" loading={save.isPending}>
          {save.error ? "Try again" : replaces ? "Replace" : secret ? "Set value" : "Add secret"}
        </Button>
      </DialogFooter>
    </form>
  );
}
