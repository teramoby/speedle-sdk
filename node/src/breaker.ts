import type { Clock } from "./clock.js";

export type BreakerState = "closed" | "open" | "half_open";

/** Outcome of a call, as the breaker counts it (design §6.6). */
export type BreakerOutcome = "success" | "failure" | "neutral";

/** One breaker per client; counts calls, not attempts. */
export class CircuitBreaker {
  private state: BreakerState = "closed";
  private failures = 0;
  private openedAt = 0;
  private trialInFlight = false;

  constructor(
    private readonly threshold: number,
    private readonly coolDownMs: number,
    private readonly clock: Clock,
    private readonly onChange: (from: BreakerState, to: BreakerState) => void,
  ) {}

  private move(to: BreakerState): void {
    const from = this.state;
    this.state = to;
    this.onChange(from, to);
  }

  /**
   * Admits or rejects a call. Returns a ticket that must be passed to
   * {@link record}, or undefined when the call is rejected.
   */
  acquire(): { trial: boolean } | undefined {
    if (this.state === "open") {
      if (this.clock.now() - this.openedAt < this.coolDownMs) return undefined;
      this.move("half_open");
    }
    if (this.state === "half_open") {
      if (this.trialInFlight) return undefined;
      this.trialInFlight = true;
      return { trial: true };
    }
    return { trial: false };
  }

  record(ticket: { trial: boolean }, outcome: BreakerOutcome): void {
    if (ticket.trial) {
      this.trialInFlight = false;
      if (this.state !== "half_open") return;
      if (outcome === "success") {
        this.failures = 0;
        this.move("closed");
      } else if (outcome === "failure") {
        this.open();
      }
      // neutral: stay half_open; the next call becomes the trial.
      return;
    }
    if (this.state !== "closed") return;
    if (outcome === "success") {
      this.failures = 0;
    } else if (outcome === "failure") {
      this.failures += 1;
      if (this.failures >= this.threshold) this.open();
    }
  }

  private open(): void {
    this.failures = 0;
    this.openedAt = this.clock.now();
    this.move("open");
  }
}
