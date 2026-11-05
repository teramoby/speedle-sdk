// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

// Command mock is the shared conformance mock server for the Speedle SDKs.
//
// It serves the SDK under test over HTTPS on 127.0.0.1 with a throwaway CA,
// replays the exchanges of one loaded vector (responses and faults), checks
// every request against the vector, and reports the result on a plain-HTTP
// control port. Both ports are ephemeral. On start-up it prints one JSON line
// on stdout:
//
//	{"endpoint":"https://127.0.0.1:41234","control":"http://127.0.0.1:41235","caFile":"/tmp/speedle-mock-ca-123.pem","caPem":"-----BEGIN CERTIFICATE-----\n..."}
//
// See conformance/README.md for the control API.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// startupInfo is the line printed on stdout once both ports listen.
type startupInfo struct {
	Endpoint string `json:"endpoint"`
	Control  string `json:"control"`
	CAFile   string `json:"caFile"`
	CAPEM    string `json:"caPem"`
}

// instance is a running mock: the HTTPS server for the SDK and the control
// server for the runner.
type instance struct {
	info    startupInfo
	mock    *mock
	sdk     *http.Server
	control *http.Server
	done    chan struct{}
	once    sync.Once
}

func start(host, caFile string, verbose bool) (*instance, error) {
	pki, err := newTestPKI()
	if err != nil {
		return nil, err
	}
	if caFile == "" {
		f, err := os.CreateTemp("", "speedle-mock-ca-*.pem")
		if err != nil {
			return nil, err
		}
		caFile = f.Name()
		_ = f.Close()
	}
	if err := os.WriteFile(caFile, pki.caPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write CA file: %w", err)
	}

	sdkLn, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, err
	}
	ctlLn, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		_ = sdkLn.Close()
		return nil, err
	}

	errLog := log.New(io.Discard, "", 0)
	if verbose {
		errLog = log.New(os.Stderr, "mock: ", log.LstdFlags)
	}
	m := newMock()
	inst := &instance{mock: m, done: make(chan struct{})}
	inst.sdk = &http.Server{
		Handler: m.sdkHandler(),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{pki.serverCert},
			MinVersion:   tls.VersionTLS12,
		},
		// HTTP/1.1 only: faults are injected by hijacking the connection.
		TLSNextProto:      map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          errLog,
	}
	inst.control = &http.Server{
		Handler:           m.controlHandler(pki.caPEM, inst.stop),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          errLog,
	}
	go func() { _ = inst.sdk.ServeTLS(sdkLn, "", "") }()
	go func() { _ = inst.control.Serve(ctlLn) }()

	inst.info = startupInfo{
		Endpoint: "https://" + sdkLn.Addr().String(),
		Control:  "http://" + ctlLn.Addr().String(),
		CAFile:   caFile,
		CAPEM:    string(pki.caPEM),
	}
	return inst, nil
}

func (i *instance) stop() {
	i.once.Do(func() {
		i.mock.load(&vector{Exchanges: []exchange{}}) // release held requests
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = i.sdk.Shutdown(ctx)
		_ = i.control.Shutdown(ctx)
		close(i.done)
	})
}

func main() {
	host := flag.String("host", "127.0.0.1", "loopback address to listen on")
	caFile := flag.String("ca-file", "", "where to write the test CA (PEM); default a new temporary file")
	exitOnStdin := flag.Bool("exit-on-stdin-close", false, "exit when stdin reaches EOF, so the mock ends with its parent")
	verbose := flag.Bool("v", false, "log server errors to stderr")
	flag.Parse()

	inst, err := start(*host, *caFile, *verbose)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mock:", err)
		os.Exit(1)
	}
	line, _ := json.Marshal(inst.info)
	fmt.Println(string(line))

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigs; inst.stop() }()
	if *exitOnStdin {
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); inst.stop() }()
	}
	<-inst.done
	if *caFile == "" {
		_ = os.Remove(inst.info.CAFile)
	}
}
