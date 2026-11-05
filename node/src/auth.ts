import { inspect } from "node:util";
import { ConfigurationError } from "./errors.js";

/** Client authentication (design §6.2). Build it with the {@link Auth} factories. */
export interface Auth {
  readonly kind: "none" | "bearer" | "mutual-tls";
}

export interface MutualTlsOptions {
  /** Client certificate chain, PEM. */
  cert: string | Buffer;
  /** Private key, PEM. */
  key: string | Buffer;
}

const secrets = new WeakMap<Auth, { token?: string; cert?: string | Buffer; key?: string | Buffer }>();

function make(kind: Auth["kind"], secret: { token?: string; cert?: string | Buffer; key?: string | Buffer }): Auth {
  const label = kind === "none" ? "Auth.none()" : kind === "bearer" ? "Auth.bearer(<redacted>)" : "Auth.mutualTls(<redacted>)";
  const auth: Auth = Object.freeze({
    kind,
    toString: () => label,
    toJSON: () => ({ kind }),
    [inspect.custom]: () => label,
  });
  secrets.set(auth, secret);
  return auth;
}

function isPem(value: unknown): value is string | Buffer {
  return (typeof value === "string" || Buffer.isBuffer(value)) && value.length > 0;
}

// Whitespace or control characters, including DEL and Unicode whitespace.
// eslint-disable-next-line no-control-regex
const BAD_TOKEN_CHARS = /[\s\u0000-\u001f\u007f-\u009f]/u;

export const Auth = Object.freeze({
  /** No credential: Speedle+ on a trusted network or loopback. */
  none(): Auth {
    return make("none", {});
  },
  /** `Authorization: Bearer <token>`. Requires HTTPS. */
  bearer(token: string): Auth {
    if (typeof token !== "string" || token === "") throw new ConfigurationError("bearer token is empty");
    if (BAD_TOKEN_CHARS.test(token)) {
      throw new ConfigurationError("bearer token contains whitespace or control characters");
    }
    return make("bearer", { token });
  },
  /** Presents a client certificate on the TLS handshake (PEM). */
  mutualTls(options: MutualTlsOptions): Auth {
    if (options === null || typeof options !== "object" || !isPem(options.cert) || !isPem(options.key)) {
      throw new ConfigurationError("mutualTls needs a PEM cert and key");
    }
    return make("mutual-tls", { cert: options.cert, key: options.key });
  },
});

/** @internal */
export function authSecret(auth: Auth): { token?: string; cert?: string | Buffer; key?: string | Buffer } | undefined {
  return secrets.get(auth);
}
