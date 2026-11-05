/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * The server a client talks to. It is never auto-detected. Both backends receive the same request
 * body; the backend selects the authentication rules and how error bodies are read.
 */
public enum Backend {
  /** Speedle Cloud ADS ({@code https://cloud.speedle.io}). Requires {@link Auth#bearer}. */
  SPEEDLE_CLOUD,
  /** A self-hosted Speedle+ ADS (REST, default port 6734). */
  SPEEDLE_PLUS
}
