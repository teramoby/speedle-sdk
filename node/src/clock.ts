import { performance } from "node:perf_hooks";

/**
 * Time source of the retry loop and the breaker. Internal: the conformance
 * runner and the unit tests replace it with a virtual clock (design §11).
 */
export interface Clock {
  /** Monotonic milliseconds. */
  now(): number;
  /** Sleeps `ms`; rejects with `signal.reason` when the signal aborts. */
  sleep(ms: number, signal?: AbortSignal): Promise<void>;
}

/** Returns a value in [0, 1). Internal, replaced by the conformance runner. */
export type RandomSource = () => number;

export const systemClock: Clock = {
  now: () => performance.now(),
  sleep: (ms, signal) =>
    new Promise<void>((resolve, reject) => {
      if (signal?.aborted) {
        reject(signal.reason);
        return;
      }
      const onAbort = (): void => {
        clearTimeout(timer);
        reject(signal?.reason);
      };
      const timer = setTimeout(() => {
        signal?.removeEventListener("abort", onAbort);
        resolve();
      }, ms);
      signal?.addEventListener("abort", onAbort, { once: true });
    }),
};

export const systemRandom: RandomSource = Math.random;
