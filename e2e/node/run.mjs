// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

// Speedle SDK end-to-end runner for Node.js. Runs every vector in
// e2e/vectors.json with SpeedleClient.check and fails on any mismatch. The
// contract is in e2e/README.md.

import { readFileSync } from "node:fs";
import { Attr, Auth, SpeedleClient } from "../../node/dist/esm/index.js";

function env(name, required) {
  const value = process.env[name];
  if (!value) {
    if (required) usage(`${name} is required`);
    return undefined;
  }
  return value;
}

function usage(message) {
  console.error(`node e2e: ${message}`);
  process.exit(2);
}

const endpoint = env("SPEEDLE_E2E_ENDPOINT", true);
const serviceName = env("SPEEDLE_E2E_SERVICE", true);
const backend = env("SPEEDLE_E2E_BACKEND", false) ?? "speedle-cloud";
const token = env("SPEEDLE_E2E_TOKEN", false);
if (backend !== "speedle-cloud" && backend !== "speedle-plus") {
  usage("SPEEDLE_E2E_BACKEND must be speedle-cloud or speedle-plus");
}
if (backend === "speedle-cloud" && !token) usage("SPEEDLE_E2E_TOKEN is required for speedle-cloud");
if (process.argv.length !== 3) usage("usage: run.mjs VECTORS_JSON");

const factories = {
  string: (name, value) => Attr.string(name, value),
  numeric: (name, value) => Attr.numeric(name, value),
  bool: (name, value) => Attr.bool(name, value),
  datetime: (name, value) =>
    Attr.datetime(name, Array.isArray(value) ? value.map((s) => new Date(s)) : new Date(value)),
};

function format(allowed, reason, errorMessage) {
  const s = `{allowed: ${allowed}, reason: ${reason}`;
  return errorMessage === undefined ? `${s}}` : `${s}, errorMessage: ${JSON.stringify(errorMessage)}}`;
}

const { vectors } = JSON.parse(readFileSync(process.argv[2], "utf8"));
let client;
try {
  client = new SpeedleClient({
    backend,
    endpoint,
    auth: token ? Auth.bearer(token) : Auth.none(),
    serviceName,
    userAgentSuffix: "speedle-sdk-e2e",
  });
} catch (e) {
  usage(`${e.name}: ${e.message}`);
}
let failed = 0;
try {
  for (const vector of vectors) {
    const want = vector.expect
      ? format(vector.expect.allowed, vector.expect.reason, vector.expect.errorMessage)
      : "no expectation";
    const { subject, action, resource, attributes = [] } = vector.request;
    let got;
    try {
      const d = await client.check(
        subject.principals.map(({ type, name, idd }) => ({ type, name, idd })),
        action,
        resource,
        attributes.map(({ name, type, value }) => factories[type](name, value)),
      );
      got = format(d.allowed, d.reason, d.errorMessage);
    } catch (e) {
      got = `${e.name}: ${e.message}`;
    }
    if (want === got) {
      console.log(`PASS ${vector.name}`);
    } else {
      failed++;
      console.log(`FAIL ${vector.name}: want ${want}, got ${got}`);
    }
  }
} finally {
  await client.close();
}
console.log(`node: ${vectors.length - failed} passed, ${failed} failed against ${endpoint}`);
process.exit(failed === 0 && vectors.length > 0 ? 0 : 1);
