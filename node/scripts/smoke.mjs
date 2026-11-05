// Loads the built package through its ESM export.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { Attr, SpeedleClient, TimeoutError, VERSION } from "@speedle/sdk";

assert.equal(typeof SpeedleClient, "function");
assert.equal(typeof Attr.datetime, "function");
assert.equal(new TimeoutError("x").name, "TimeoutError");
assert.equal(VERSION, JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")).version);
console.log("esm ok");
