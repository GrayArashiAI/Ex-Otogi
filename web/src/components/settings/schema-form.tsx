import type { AnyFieldApi, ReactFormExtendedApi } from "@tanstack/react-form";
import { Check, ChevronRight, Plus, Trash2, X } from "lucide-react";
import { type ReactNode, useState } from "react";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsiblePanel, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Select, SelectItem, SelectPopup, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { blank, check, type JSONSchema, kindOf, label, properties, resolve } from "./schema";

// biome-ignore lint/suspicious/noExplicitAny: settings are any module's, shaped by the schema at runtime
export type SettingsForm = ReactFormExtendedApi<any, any, any, any, any, any, any, any, any, any, any, any>;

type Props = { form: SettingsForm; root: JSONSchema };

/** SchemaFields renders an object's fields, in the order of the type. */
export function SchemaFields({ form, root, schema, path }: Props & { schema: JSONSchema; path: string }) {
  const s = resolve(root, schema);
  const required = new Set(s.required ?? []);
  return (
    <div className="flex flex-col divide-y divide-border/70">
      {properties(s).map(([key, prop]) => (
        <SchemaField
          key={key}
          form={form}
          root={root}
          schema={prop}
          name={path ? `${path}.${key}` : key}
          title={label(key)}
          required={required.has(key)}
        />
      ))}
      {properties(s).length === 0 && <p className="py-3 text-muted-foreground text-sm">This module has no settings.</p>}
    </div>
  );
}

function SchemaField({
  form,
  root,
  schema,
  name,
  title,
  required,
}: Props & { schema: JSONSchema; name: string; title: string; required: boolean }) {
  const s = resolve(root, schema);
  const kind = kindOf(s);
  const doc = schema.description ?? s.description;
  if (kind === "object") {
    return (
      <form.Field name={name}>
        {(field: AnyFieldApi) => {
          const set = required || field.state.value !== undefined;
          return (
            <Row
              title={
                <span className="flex items-center justify-between gap-3">
                  {title}
                  {!required && (
                    <Switch
                      checked={set}
                      onCheckedChange={(on) => field.handleChange(on ? {} : undefined)}
                      aria-label={`Use ${title.toLowerCase()}`}
                    />
                  )}
                </span>
              }
              doc={doc}
              block
            >
              {set && (
                <div className="w-full border-l pl-4">
                  <SchemaFields form={form} root={root} schema={s} path={name} />
                </div>
              )}
            </Row>
          );
        }}
      </form.Field>
    );
  }
  if (kind === "array" && s.items && ["object", "map"].includes(kindOf(resolve(root, s.items)))) {
    return <ObjectList form={form} root={root} schema={s} name={name} title={title} doc={doc} />;
  }
  if (kind === "map") {
    return <ObjectMap form={form} root={root} schema={s} name={name} title={title} doc={doc} />;
  }
  return (
    <form.Field name={name} validators={{ onChange: ({ value }: { value: unknown }) => check(s, value, required) }}>
      {(field: AnyFieldApi) => (
        <Row
          title={title}
          doc={doc}
          error={field.state.meta.errors.filter(Boolean).join("; ")}
          block={kind === "array" || s.format === "markdown"}
          inline={kind === "boolean"}
        >
          <Control field={field} schema={s} />
        </Row>
      )}
    </form.Field>
  );
}

function Row({
  title,
  doc,
  error,
  block,
  inline,
  children,
}: {
  title: ReactNode;
  doc?: string;
  error?: string;
  block?: boolean;
  inline?: boolean;
  children: ReactNode;
}) {
  return (
    <div
      className={cn(
        "grid grid-cols-1 gap-x-8 gap-y-2 py-3.5",
        block ? "grid-cols-1" : "sm:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]",
        inline && "grid-cols-[minmax(0,1fr)_auto] sm:grid-cols-[minmax(0,1fr)_auto]",
      )}
    >
      <div className="min-w-0">
        <div className="font-medium text-sm">{title}</div>
        {doc && <p className="mt-0.5 text-muted-foreground text-xs leading-relaxed">{doc}</p>}
      </div>
      <div className={cn("min-w-0", inline && "pt-0.5")}>
        {children}
        {error && <p className="mt-1.5 text-destructive-foreground text-xs">{error}</p>}
      </div>
    </div>
  );
}

function Control({ field, schema }: { field: AnyFieldApi; schema: JSONSchema }) {
  const kind = kindOf(schema);
  const value = field.state.value;
  if (kind === "boolean") {
    return <Switch checked={Boolean(value)} onCheckedChange={(v) => field.handleChange(v)} />;
  }
  if (kind === "integer" || kind === "number") {
    return (
      <Input
        type="number"
        inputMode={kind === "integer" ? "numeric" : "decimal"}
        className="tabular"
        value={value ?? ""}
        min={schema.minimum}
        max={schema.maximum}
        step={kind === "integer" ? 1 : "any"}
        placeholder="Default"
        onChange={(e) => field.handleChange(e.target.value === "" ? undefined : Number(e.target.value))}
        onBlur={field.handleBlur}
      />
    );
  }
  if (kind === "array") {
    const choices = (schema.items as { enum?: unknown[] } | undefined)?.enum;
    const words = (value as string[] | undefined) ?? [];
    if (choices) {
      return <Choices options={choices.map(String)} value={words} onChange={(v) => field.handleChange(v)} />;
    }
    return <Tags value={words} onChange={(v) => field.handleChange(v)} />;
  }
  if (schema.enum) {
    const items = [
      { value: "", label: "Default" },
      ...schema.enum.map((v) => ({ value: String(v), label: String(v) })),
    ];
    return (
      <Select items={items} value={value ?? ""} onValueChange={(v) => field.handleChange(v || undefined)}>
        <SelectTrigger>
          <SelectValue />
        </SelectTrigger>
        <SelectPopup>
          {items.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectPopup>
      </Select>
    );
  }
  if (schema.format === "markdown") {
    return (
      <div>
        <Textarea
          className="min-h-72 font-[450] text-[13.5px] leading-relaxed"
          value={value ?? ""}
          onChange={(e) => field.handleChange(e.target.value)}
          onBlur={field.handleBlur}
          placeholder="Who the agent is, how it talks, what it knows. Markdown."
        />
        <p className="mt-1 text-right text-muted-foreground text-xs tabular">{(value ?? "").length} characters</p>
      </div>
    );
  }
  const mono = schema.format === "secret" || schema.format === "duration";
  return (
    <Input
      className={cn(mono && "font-mono text-[13px]")}
      value={value ?? ""}
      placeholder={
        schema.format === "secret"
          ? // biome-ignore lint/suspicious/noTemplateCurlyInString: the placeholder shows a ${NAME} reference
            "${ENVIRONMENT_VARIABLE}"
          : schema.format === "duration"
            ? "Default, or like 5m"
            : ""
      }
      spellCheck={!mono}
      onChange={(e) => field.handleChange(e.target.value)}
      onBlur={field.handleBlur}
    />
  );
}

/** Choices picks any of a few fixed words, each a chip that toggles. */
function Choices({
  options,
  value,
  onChange,
}: {
  options: string[];
  value: string[];
  onChange: (v: string[]) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((o) => {
        const on = value.includes(o);
        return (
          <button
            key={o}
            type="button"
            aria-pressed={on}
            onClick={() =>
              onChange(on ? value.filter((w) => w !== o) : options.filter((x) => x === o || value.includes(x)))
            }
            className={cn(
              "inline-flex h-7 items-center gap-1.5 rounded-md border px-2.5 font-mono text-xs outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
              on
                ? "border-foreground/80 bg-foreground text-background"
                : "bg-background text-muted-foreground shadow-xs/5 hover:text-foreground",
            )}
          >
            {on && <Check className="size-3" />}
            {o}
          </button>
        );
      })}
    </div>
  );
}

/** Tags edits a list of words: Enter or comma adds one. */
function Tags({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const [draft, setDraft] = useState("");
  const add = () => {
    const word = draft.trim().replace(/,$/, "");
    if (word && !value.includes(word)) onChange([...value, word]);
    setDraft("");
  };
  return (
    <div className="flex min-h-9 w-full flex-wrap items-center gap-1.5 rounded-lg border border-input bg-background px-1.5 py-1 shadow-xs/5 focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/24 sm:min-h-8">
      {value.map((word) => (
        <span
          key={word}
          className="inline-flex items-center gap-1 rounded-md border bg-linear-to-b from-white to-neutral-50 py-0.5 ps-2 pe-1 font-mono text-xs shadow-[0_1px_--theme(--color-black/4%)]"
        >
          {word}
          <button
            type="button"
            className="rounded text-muted-foreground hover:text-foreground"
            aria-label={`Remove ${word}`}
            onClick={() => onChange(value.filter((w) => w !== word))}
          >
            <X className="size-3" />
          </button>
        </span>
      ))}
      <input
        className="min-w-24 flex-1 bg-transparent px-1 text-sm outline-none placeholder:text-muted-foreground/72"
        value={draft}
        placeholder={value.length ? "" : "Type, then Enter"}
        onChange={(e) => (e.target.value.endsWith(",") ? setDraft(e.target.value) : setDraft(e.target.value))}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            add();
          } else if (e.key === "Backspace" && !draft && value.length) {
            onChange(value.slice(0, -1));
          }
        }}
        onBlur={add}
      />
    </div>
  );
}

/** ObjectList edits a list of objects, such as chat agent profiles. */
function ObjectList({
  form,
  root,
  schema,
  name,
  title,
  doc,
}: Props & { schema: JSONSchema; name: string; title: string; doc?: string }) {
  const item = resolve(root, schema.items ?? {});
  return (
    <form.Field name={name} mode="array">
      {(field: AnyFieldApi) => {
        const items = (field.state.value as Record<string, unknown>[] | undefined) ?? [];
        return (
          <Row title={title} doc={doc} block>
            <div className="flex flex-col gap-2">
              {items.map((value, i) => (
                <Collapsible
                  // biome-ignore lint/suspicious/noArrayIndexKey: items have no identity until named
                  key={i}
                  defaultOpen={!value?.name}
                  className="rounded-xl border"
                >
                  <div className="flex items-center gap-2 px-2 py-1.5">
                    <CollapsibleTrigger className="group flex flex-1 items-center gap-1.5 rounded-md px-1.5 py-1 text-left font-medium text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring">
                      <ChevronRight className="size-4 text-muted-foreground transition-transform group-data-panel-open:rotate-90" />
                      {String(value?.name || value?.label || `New ${title.toLowerCase().replace(/s$/, "")}`)}
                    </CollapsibleTrigger>
                    <Button variant="ghost" size="icon-sm" aria-label="Remove" onClick={() => field.removeValue(i)}>
                      <Trash2 />
                    </Button>
                  </div>
                  <CollapsiblePanel>
                    <div className="border-t px-3.5">
                      <SchemaFields form={form} root={root} schema={item} path={`${name}[${i}]`} />
                    </div>
                  </CollapsiblePanel>
                </Collapsible>
              ))}
              <Button
                variant="outline"
                size="sm"
                className="self-start"
                onClick={() => field.pushValue(blank(root, item))}
              >
                <Plus /> Add
              </Button>
            </div>
          </Row>
        );
      }}
    </form.Field>
  );
}

/** ObjectMap edits named objects, such as todou's agents. */
function ObjectMap({
  form,
  root,
  schema,
  name,
  title,
  doc,
}: Props & { schema: JSONSchema; name: string; title: string; doc?: string }) {
  const [key, setKey] = useState("");
  const item = typeof schema.additionalProperties === "object" ? resolve(root, schema.additionalProperties) : {};
  return (
    <form.Field name={name}>
      {(field: AnyFieldApi) => {
        const entries = Object.keys((field.state.value as Record<string, unknown> | undefined) ?? {});
        return (
          <Row title={title} doc={doc} block>
            <div className="flex flex-col gap-2">
              {entries.map((k) => (
                <div key={k} className="rounded-xl border">
                  <div className="flex items-center gap-2 px-3.5 pt-2.5">
                    <span className="font-medium font-mono text-sm">{k}</span>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="ml-auto"
                      aria-label={`Remove ${k}`}
                      onClick={() => {
                        const { [k]: _, ...rest } = field.state.value as Record<string, unknown>;
                        field.handleChange(rest);
                      }}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                  <div className="mt-1.5 border-t px-3.5">
                    <SchemaFields form={form} root={root} schema={item} path={`${name}.${k}`} />
                  </div>
                </div>
              ))}
              <div className="flex gap-2">
                <Input
                  className="max-w-48 font-mono text-[13px]"
                  placeholder="name"
                  value={key}
                  onChange={(e) => setKey(e.target.value.trim())}
                />
                <Button
                  variant="outline"
                  size="sm"
                  disabled={!key || entries.includes(key)}
                  onClick={() => {
                    field.handleChange({ ...(field.state.value ?? {}), [key]: blank(root, item) });
                    setKey("");
                  }}
                >
                  <Plus /> Add
                </Button>
              </div>
            </div>
          </Row>
        );
      }}
    </form.Field>
  );
}
