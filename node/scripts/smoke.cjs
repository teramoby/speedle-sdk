// Loads the built package through its CommonJS export.
const assert = require("node:assert/strict");
const sdk = require("@speedle/sdk");

assert.equal(typeof sdk.SpeedleClient, "function");
assert.equal(typeof sdk.Attr.string, "function");
assert.equal(new sdk.TimeoutError("x").name, "TimeoutError");
assert.ok(new sdk.DeadlineExceededError("x") instanceof sdk.NetworkError);
assert.equal(sdk.VERSION, require("../package.json").version);
console.log("cjs ok");
