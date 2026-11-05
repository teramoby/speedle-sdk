// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	speedle "github.com/teramoby/speedle-sdk/go"
)

func Example() {
	client, err := speedle.NewClient(speedle.Config{
		Backend:     speedle.BackendSpeedleCloud,
		Endpoint:    "https://cloud.speedle.io",
		Auth:        speedle.BearerToken(os.Getenv("SPEEDLE_TOKEN")),
		ServiceName: "books",
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer client.Close()

	ok, err := client.IsAllowed(context.Background(), speedle.User("alice"), "get", "/books/123", speedle.Attributes{
		speedle.String("team", "eng"),
		speedle.Numeric("clearance", 4),
		speedle.Datetime("issuedAt", time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)),
	})
	var serr *speedle.Error
	switch {
	case errors.Is(err, speedle.ErrRateLimited) && errors.As(err, &serr):
		fmt.Println("rate limited, retry after", serr.RetryAfter)
	case err != nil:
		fmt.Println("no decision:", err)
	case ok:
		fmt.Println("allowed")
	}
}

func ExampleInfer() {
	attrs, err := speedle.Infer(map[string]any{"team": "eng", "clearance": 4, "tags": []string{"a", "b"}})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, a := range attrs {
		fmt.Println(a.Name(), a.Type(), a.IsArray())
	}
	// Output:
	// clearance numeric false
	// tags string true
	// team string false
}
