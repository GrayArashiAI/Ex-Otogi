// The JSON Schema of module settings, as the server generates it from each
// module's Config type: object properties (ordered by x-order), arrays,
// maps (additionalProperties), and $defs references.

export type JSONSchema = {
  type?: string | string[];
  properties?: Record<string, JSONSchema>;
  required?: string[];
  items?: JSONSchema;
  additionalProperties?: boolean | JSONSchema;
  $ref?: string;
  $defs?: Record<string, JSONSchema>;
  description?: string;
  enum?: unknown[];
  format?: string;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  minProperties?: number;
  pattern?: string;
  patternDescription?: string;
  "x-order"?: string[];
};

export type Kind = "string" | "integer" | "number" | "boolean" | "array" | "map" | "object";

/** resolve follows a $ref into the root's $defs. */
export function resolve(root: JSONSchema, s: JSONSchema): JSONSchema {
  if (!s.$ref) return s;
  const name = s.$ref.replace("#/$defs/", "");
  return root.$defs?.[name] ?? {};
}

export function kindOf(s: JSONSchema): Kind {
  const type = Array.isArray(s.type) ? s.type.find((t) => t !== "null") : s.type;
  if (type === "object") return s.properties ? "object" : "map";
  return (type as Kind | undefined) ?? "string";
}

export function properties(s: JSONSchema): [string, JSONSchema][] {
  const props = s.properties ?? {};
  const order = s["x-order"] ?? Object.keys(props);
  return order.filter((k) => k in props).map((k) => [k, props[k] as JSONSchema]);
}

/** label turns max_tool_rounds into "Max tool rounds". */
export function label(key: string): string {
  const words = key.replaceAll("_", " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/** check validates one value against its schema, as the server will. */
export function check(s: JSONSchema, value: unknown, required: boolean): string | undefined {
  const empty = value === undefined || value === null || value === "";
  if (empty) return required ? "Required" : undefined;
  if (typeof value === "string") {
    if (s.minLength !== undefined && value.length < s.minLength) return "Required";
    if (s.maxLength !== undefined && value.length > s.maxLength) return `At most ${s.maxLength} characters`;
    if (s.pattern && !new RegExp(s.pattern).test(value))
      return `Should be ${s.patternDescription ?? `like ${s.pattern}`}`;
    if (s.enum && !s.enum.includes(value)) return `One of ${s.enum.join(", ")}`;
  }
  if (typeof value === "number") {
    if (Number.isNaN(value)) return "A number";
    if (kindOf(s) === "integer" && !Number.isInteger(value)) return "A whole number";
    if (s.minimum !== undefined && value < s.minimum) return `At least ${s.minimum}`;
    if (s.maximum !== undefined && value > s.maximum) return `At most ${s.maximum}`;
  }
  if (Array.isArray(value) && s.minItems !== undefined && value.length < s.minItems) {
    return `At least ${s.minItems}`;
  }
  return undefined;
}

/** blank is a new item's value: an object with nothing set. */
export function blank(root: JSONSchema, s: JSONSchema): unknown {
  const r = resolve(root, s);
  switch (kindOf(r)) {
    case "object":
    case "map":
      return {};
    case "array":
      return [];
    case "boolean":
      return false;
    default:
      return "";
  }
}

/** clean drops unset optional values, which the module defaults. */
export function clean(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(clean);
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value)) {
      if (v === "" || v === undefined || v === null || (typeof v === "number" && Number.isNaN(v))) continue;
      out[k] = clean(v);
    }
    return out;
  }
  return value;
}

/** changes lists what differs between two settings, by path. */
export function changes(before: unknown, after: unknown, path = ""): { path: string; from: unknown; to: unknown }[] {
  const isObj = (v: unknown) => v !== null && typeof v === "object" && !Array.isArray(v);
  if (isObj(before) && isObj(after)) {
    const a = before as Record<string, unknown>;
    const b = after as Record<string, unknown>;
    return [...new Set([...Object.keys(a), ...Object.keys(b)])].flatMap((k) =>
      changes(a[k], b[k], path ? `${path}.${k}` : k),
    );
  }
  if (Array.isArray(before) && Array.isArray(after) && before.some(isObj)) {
    return Array.from({ length: Math.max(before.length, after.length) }, (_, i) =>
      changes(before[i], after[i], `${path}[${i}]`),
    ).flat();
  }
  return JSON.stringify(before) === JSON.stringify(after) ? [] : [{ path, from: before, to: after }];
}
