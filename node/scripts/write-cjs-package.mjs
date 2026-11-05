import { writeFileSync } from "node:fs";

// Marks dist/cjs as CommonJS inside a "type": "module" package.
writeFileSync("dist/cjs/package.json", `${JSON.stringify({ type: "commonjs" }, null, 2)}\n`);
