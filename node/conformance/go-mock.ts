// Adapter for the shared conformance mock in conformance/mock (a Go program).
// See conformance/README.md, "Mock server".
import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import type { Vector } from "./vector.js";

interface StartupInfo {
  endpoint: string;
  control: string;
  caPem: string;
}

const MOCK_DIR = fileURLToPath(new URL("../../../conformance/mock/", import.meta.url));

/** Builds the mock (or uses SPEEDLE_MOCK_BIN), starts it and talks to its control API. */
export class GoMock {
  private child: ChildProcess | undefined;
  private info: StartupInfo | undefined;

  private async start(): Promise<StartupInfo> {
    if (this.info) return this.info;
    let bin = process.env.SPEEDLE_MOCK_BIN;
    if (!bin) {
      bin = join(mkdtempSync(join(tmpdir(), "speedle-mock-")), "speedle-mock");
      execFileSync("go", ["build", "-o", bin, "."], { cwd: MOCK_DIR, stdio: ["ignore", "ignore", "inherit"] });
    }
    const child = spawn(bin, ["-exit-on-stdin-close"], { stdio: ["pipe", "pipe", "inherit"] });
    this.child = child;
    const lines = createInterface({ input: child.stdout as NodeJS.ReadableStream });
    const first = await new Promise<string>((resolve, reject) => {
      lines.once("line", resolve);
      child.once("exit", (code) => reject(new Error(`conformance mock exited with ${String(code)}`)));
    });
    lines.close();
    this.info = JSON.parse(first) as StartupInfo;
    return this.info;
  }

  async load(vector: Vector): Promise<{ baseUrl: string; caPem: string }> {
    const info = await this.start();
    const res = await fetch(`${info.control}/load`, { method: "POST", body: JSON.stringify(vector) });
    if (res.status !== 204) throw new Error(`mock /load: ${res.status} ${await res.text()}`);
    return { baseUrl: info.endpoint, caPem: info.caPem };
  }

  /** Mismatches, plus missing or extra requests, after the vector ran. */
  async verify(): Promise<string[]> {
    const info = await this.start();
    const res = await fetch(`${info.control}/verify`);
    const body = (await res.json()) as { ok: boolean; errors?: string[] };
    return body.ok ? [] : (body.errors ?? ["verify failed"]);
  }

  async close(): Promise<void> {
    const child = this.child;
    if (!child) return;
    const exited = new Promise<void>((resolve) => child.once("exit", () => resolve()));
    child.stdin?.end();
    await exited;
  }
}
