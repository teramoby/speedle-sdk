import { InvalidRequestError } from "./errors.js";

export type AttributeType = "string" | "numeric" | "bool" | "datetime";

/** One Speedle `JsonAttribute`. Build it with the {@link Attr} factories. */
export type Attribute =
  | { readonly name: string; readonly type: "string"; readonly value: string | readonly string[] }
  | { readonly name: string; readonly type: "numeric"; readonly value: number | readonly number[] }
  | { readonly name: string; readonly type: "bool"; readonly value: boolean | readonly boolean[] }
  | { readonly name: string; readonly type: "datetime"; readonly value: Date | readonly Date[] };

/** Inferred form: a `Date` becomes `datetime`; a string is always `string`. */
export type InferredValue =
  | string
  | number
  | boolean
  | Date
  | readonly string[]
  | readonly number[]
  | readonly boolean[]
  | readonly Date[];

/** An array of {@link Attribute} is the typed form; a plain object is the inferred form. */
export type Attributes = readonly Attribute[] | Readonly<Record<string, InferredValue>>;

/** The wire form of one attribute. */
export interface WireAttribute {
  name: string;
  type: AttributeType;
  value: string | number | boolean | string[] | number[] | boolean[];
}

function freeze<T>(value: T | readonly T[]): T | readonly T[] {
  return Array.isArray(value) ? Object.freeze([...(value as readonly T[])]) : (value as T);
}

/**
 * Factories for typed attributes, one per Speedle type. Each takes a single
 * value or an array. Values are checked when the attribute is sent.
 */
export const Attr = Object.freeze({
  string(name: string, value: string | readonly string[]): Attribute {
    return Object.freeze({ name, type: "string" as const, value: freeze(value) });
  },
  numeric(name: string, value: number | readonly number[]): Attribute {
    return Object.freeze({ name, type: "numeric" as const, value: freeze(value) });
  },
  bool(name: string, value: boolean | readonly boolean[]): Attribute {
    return Object.freeze({ name, type: "bool" as const, value: freeze(value) });
  },
  datetime(name: string, value: Date | readonly Date[]): Attribute {
    return Object.freeze({ name, type: "datetime" as const, value: freeze(value) });
  },
});

function invalid(message: string): InvalidRequestError {
  return new InvalidRequestError(message);
}

/** RFC 3339 in UTC, with fractional seconds only when they are not zero. */
export function formatDatetime(date: Date): string {
  const iso = date.toISOString();
  return iso.endsWith(".000Z") ? `${iso.slice(0, -5)}Z` : iso;
}

function convertItem(name: string, type: AttributeType, item: unknown): string | number | boolean {
  if (item === null || item === undefined) {
    throw invalid(`attribute "${name}": null or undefined value`);
  }
  switch (type) {
    case "string":
      if (typeof item !== "string") throw invalid(`attribute "${name}": expected a string`);
      return item;
    case "numeric":
      if (typeof item !== "number") throw invalid(`attribute "${name}": expected a number`);
      if (!Number.isFinite(item)) throw invalid(`attribute "${name}": number is not finite`);
      return item;
    case "bool":
      if (typeof item !== "boolean") throw invalid(`attribute "${name}": expected a boolean`);
      return item;
    case "datetime":
      if (!(item instanceof Date)) throw invalid(`attribute "${name}": expected a Date`);
      if (Number.isNaN(item.getTime())) throw invalid(`attribute "${name}": invalid Date`);
      return formatDatetime(item);
  }
}

function convert(name: string, type: AttributeType, value: unknown): WireAttribute["value"] {
  if (Array.isArray(value)) {
    return value.map((item: unknown) => convertItem(name, type, item)) as WireAttribute["value"];
  }
  return convertItem(name, type, value);
}

const TYPES: ReadonlySet<string> = new Set(["string", "numeric", "bool", "datetime"]);

function inferType(value: unknown): AttributeType | undefined {
  if (typeof value === "string") return "string";
  if (typeof value === "number") return "numeric";
  if (typeof value === "boolean") return "bool";
  if (value instanceof Date) return "datetime";
  return undefined;
}

/** Compares strings by Unicode code point, not by UTF-16 code unit. */
export function compareCodePoints(a: string, b: string): number {
  const ia = a[Symbol.iterator]();
  const ib = b[Symbol.iterator]();
  for (;;) {
    const x = ia.next();
    const y = ib.next();
    if (x.done || y.done) return x.done && y.done ? 0 : x.done ? -1 : 1;
    const cx = x.value.codePointAt(0) as number;
    const cy = y.value.codePointAt(0) as number;
    if (cx !== cy) return cx < cy ? -1 : 1;
  }
}

function checkName(name: unknown, seen: Set<string>): string {
  if (typeof name !== "string" || name === "") throw invalid("attribute with an empty name");
  if (seen.has(name)) throw invalid(`attribute "${name}" given twice`);
  seen.add(name);
  return name;
}

/**
 * Converts the caller's attributes to the Speedle wire array (design §3.2.1).
 * Raises InvalidRequestError for every check listed there.
 */
export function toWireAttributes(attributes: Attributes | undefined | null): WireAttribute[] {
  if (attributes === undefined || attributes === null) return [];
  const seen = new Set<string>();
  if (Array.isArray(attributes)) {
    return (attributes as readonly unknown[]).map((raw) => {
      if (raw === null || typeof raw !== "object") throw invalid("attribute is not an object");
      const attr = raw as { name?: unknown; type?: unknown; value?: unknown };
      const name = checkName(attr.name, seen);
      if (typeof attr.type !== "string" || !TYPES.has(attr.type)) {
        throw invalid(`attribute "${name}": unknown type`);
      }
      const type = attr.type as AttributeType;
      return { name, type, value: convert(name, type, attr.value) };
    });
  }
  if (typeof attributes !== "object" || attributes instanceof Date) {
    throw invalid("attributes must be an array of typed attributes or a plain object");
  }
  const names = Object.keys(attributes).sort(compareCodePoints);
  return names.map((name) => {
    checkName(name, seen);
    const value: unknown = (attributes as Record<string, unknown>)[name];
    let type: AttributeType | undefined;
    if (Array.isArray(value)) {
      if (value.length === 0) throw invalid(`attribute "${name}": cannot infer the type of an empty array`);
      const types = new Set(value.map(inferType));
      if (types.size === 1) type = [...types][0];
    } else {
      type = inferType(value);
    }
    if (type === undefined) throw invalid(`attribute "${name}": cannot infer a Speedle type; use Attr`);
    return { name, type, value: convert(name, type, value) };
  });
}
