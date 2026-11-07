import { Auth } from "../src/auth.js";
import { createClientForTesting, type SpeedleClient } from "../src/client.js";
import type { ClientOptions } from "../src/options.js";
import { testCerts, TOKEN, VirtualClock } from "./helpers.js";

export interface Built {
  client: SpeedleClient;
  clock: VirtualClock;
  events: Record<string, unknown>[];
}

/** A client with the virtual clock, jitter fraction 1 and recording hooks. */
export function build(endpoint: string, overrides: Partial<ClientOptions> = {}, jitter = 1): Built {
  const clock = new VirtualClock();
  const events: Record<string, unknown>[] = [];
  const cloud = overrides.backend !== "speedle" && endpoint.startsWith("https:");
  const client = createClientForTesting(
    {
      backend: cloud ? "speedle-cloud" : "speedle",
      endpoint,
      auth: cloud ? Auth.bearer(TOKEN) : Auth.none(),
      serviceName: "books",
      caCert: endpoint.startsWith("https:") ? testCerts().ca : undefined,
      hooks: {
        onAttempt: (e) => events.push({ type: "attempt", ...e }),
        onRetry: (e) => events.push({ type: "retry", ...e }),
        onGiveUp: (e) => events.push({ type: "giveUp", attempts: e.attempts, reason: e.reason, errorType: e.error.name }),
        onBreakerStateChange: (e) => events.push({ type: "breaker", ...e }),
      },
      ...overrides,
    },
    clock,
    () => jitter,
  );
  return { client, clock, events };
}
