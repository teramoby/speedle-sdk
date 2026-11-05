// Types for conformance/schema.json.
export type HeaderMatcher = string | { absent: true } | { pattern: string };

export interface Exchange {
  request: {
    method: "POST";
    path: string;
    headers?: Record<string, HeaderMatcher>;
    body: unknown;
    unorderedArrays?: string[];
  };
  response:
    | { status: number; headers?: Record<string, string>; body?: unknown; rawBody?: string }
    | { fault: "connection_reset" | "no_response" };
}

export interface VectorAttribute {
  name: string;
  type: "string" | "numeric" | "bool" | "datetime";
  value: unknown;
}

export interface VectorCall {
  method: "isAllowed" | "check";
  subject: string | { type?: string; name: string; idd?: string }[];
  action: string;
  resource: string;
  serviceName?: string;
  attributes?: VectorAttribute[];
  inferredAttributes?: Record<string, unknown>;
}

export interface VectorExpect {
  result?: boolean;
  decision?: { allowed: boolean; reason: number; errorMessage?: string | null; quotaDailyRemaining?: number | null };
  error?: {
    type: string;
    status?: number | null;
    code?: string | null;
    retryAfterSeconds?: number | null;
    quotaDailyRemaining?: number | null;
  };
  delaysMs?: number[];
}

export interface Vector {
  id: string;
  description: string;
  backend: "speedle-cloud" | "speedle-plus";
  config: {
    auth: { type: "none" } | { type: "bearer"; token: string };
    serviceName: string;
    retry?: Record<string, number>;
    failureMode?: "error" | "closed";
    circuitBreaker?: { failureThreshold?: number; coolDownMs?: number };
  };
  jitterFraction?: number;
  call?: VectorCall;
  expect?: VectorExpect;
  steps?: { advanceMs?: number; call: VectorCall; expect: VectorExpect }[];
  exchanges: Exchange[];
  events?: Record<string, unknown>[];
}
