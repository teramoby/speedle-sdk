import assert from "node:assert/strict";
import { test } from "node:test";
import { Attr, compareCodePoints, formatDatetime, toWireAttributes } from "../src/attributes.js";
import { InvalidRequestError } from "../src/errors.js";

test("typed factories map one-to-one onto JsonAttribute, in the caller's order", () => {
  const wire = toWireAttributes([
    Attr.string("s", "eng"),
    Attr.string("sa", ["a", "b"]),
    Attr.numeric("n", 4.5),
    Attr.numeric("na", []),
    Attr.bool("b", false),
    Attr.bool("ba", [true]),
    Attr.datetime("d", new Date("2026-11-05T03:00:00Z")),
    Attr.datetime("da", [new Date("2026-11-05T03:00:00.250Z")]),
  ]);
  assert.deepEqual(wire, [
    { name: "s", type: "string", value: "eng" },
    { name: "sa", type: "string", value: ["a", "b"] },
    { name: "n", type: "numeric", value: 4.5 },
    { name: "na", type: "numeric", value: [] },
    { name: "b", type: "bool", value: false },
    { name: "ba", type: "bool", value: [true] },
    { name: "d", type: "datetime", value: "2026-11-05T03:00:00Z" },
    { name: "da", type: "datetime", value: ["2026-11-05T03:00:00.250Z"] },
  ]);
});

test("datetime is RFC 3339 UTC with fractions only when non-zero", () => {
  assert.equal(formatDatetime(new Date(Date.UTC(2026, 10, 5, 3))), "2026-11-05T03:00:00Z");
  assert.equal(formatDatetime(new Date("2026-11-05T05:00:00.001+02:00")), "2026-11-05T03:00:00.001Z");
});

test("factories copy and freeze arrays", () => {
  const values = ["a"];
  const attr = Attr.string("x", values);
  values.push("b");
  assert.deepEqual(attr.value, ["a"]);
  assert.ok(Object.isFrozen(attr) && Object.isFrozen(attr.value));
});

test("inferred form: types from native values, sorted by code point", () => {
  const wire = toWireAttributes({
    z: "2026-11-05T03:00:00Z",
    "\u{1F600}": 1,
    "\uFFFF": true,
    a: [1, 2],
    d: new Date("2026-11-05T03:00:00Z"),
    l: ["x", "y"],
  });
  assert.deepEqual(
    wire.map((a) => [a.name, a.type]),
    [
      ["a", "numeric"],
      ["d", "datetime"],
      ["l", "string"],
      ["z", "string"],
      ["\uFFFF", "bool"],
      ["\u{1F600}", "numeric"],
    ],
  );
  assert.equal(wire[3]?.value, "2026-11-05T03:00:00Z");
});

test("compareCodePoints differs from UTF-16 order for astral characters", () => {
  assert.equal(compareCodePoints("\u{1F600}", "\uFFFF"), 1);
  assert.equal(compareCodePoints("ab", "a"), 1);
  assert.equal(compareCodePoints("a", "a"), 0);
});

test("no attributes means an empty wire array", () => {
  assert.deepEqual(toWireAttributes(undefined), []);
  assert.deepEqual(toWireAttributes({}), []);
  assert.deepEqual(toWireAttributes([]), []);
});

const rejected: [string, () => unknown][] = [
  ["empty name", () => toWireAttributes([Attr.string("", "x")])],
  ["duplicate name", () => toWireAttributes([Attr.string("a", "x"), Attr.numeric("a", 1)])],
  ["null value", () => toWireAttributes([Attr.string("a", null as unknown as string)])],
  ["null item", () => toWireAttributes([Attr.string("a", ["x", null as unknown as string])])],
  ["NaN", () => toWireAttributes([Attr.numeric("a", Number.NaN)])],
  ["Infinity item", () => toWireAttributes([Attr.numeric("a", [1, Number.POSITIVE_INFINITY])])],
  ["invalid Date", () => toWireAttributes([Attr.datetime("a", new Date("nope"))])],
  ["wrong item type", () => toWireAttributes([Attr.bool("a", "true" as unknown as boolean)])],
  ["unknown type", () => toWireAttributes([{ name: "a", type: "", value: 1 } as never])],
  ["inferred null", () => toWireAttributes({ a: null } as never)],
  ["inferred empty list", () => toWireAttributes({ a: [] })],
  ["inferred mixed list", () => toWireAttributes({ a: [1, "x"] } as never)],
  ["inferred nested object", () => toWireAttributes({ a: { b: 1 } } as never)],
  ["inferred NaN", () => toWireAttributes({ a: Number.NaN })],
];
for (const [name, fn] of rejected) {
  test(`rejects ${name} with InvalidRequestError`, () => {
    assert.throws(fn, InvalidRequestError);
  });
}
