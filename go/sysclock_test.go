// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import "github.com/teramoby/speedle-sdk/go/internal/clock"

func systemClock() clock.Clock { return clock.System{} }
