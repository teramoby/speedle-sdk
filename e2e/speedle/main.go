// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

// Command speedle is the local self-hosted Speedle side of the SDK end-to-end tests.
//
// It loads e2e/policies.json into a self-hosted Speedle PMS with spctl, checks that the
// parsed form in the file is what self-hosted Speedle itself produces, and sends every
// vector in e2e/vectors.json to a self-hosted Speedle REST ADS, either checking or
// recording the expected decisions. It uses only the standard library and the
// spctl binary it is given. See e2e/README.md.
//
// Usage:
//
//	speedle load    -spctl BIN -pms URL [-service NAME] [-policies FILE]
//	speedle parsed  -store FILE [-service NAME] [-policies FILE] [-write]
//	speedle vectors -endpoint URL [-service NAME] [-vectors FILE] [-write] [-commit SHA]
package main

import (
	"flag"
	"fmt"
	"os"
)

const defaultService = "sdk-e2e"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "load":
		err = runLoad(os.Args[2:])
	case "parsed":
		err = runParsed(os.Args[2:])
	case "vectors":
		err = runVectors(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "speedle:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: speedle (load | parsed | vectors) [flags]; see e2e/README.md")
	os.Exit(2)
}

func newFlags(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ExitOnError)
}
