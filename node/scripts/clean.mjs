import { rmSync } from "node:fs";

const targets = process.argv.length > 2 ? process.argv.slice(2) : ["dist", "build"];
for (const target of targets) rmSync(target, { recursive: true, force: true });
