// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Command examend runs experiments on agents and on programs built on
// models, and reports what changed. It takes one command and no flag, and
// is configured by its environment (specs/011-distribution.md):
//
//	examend serve    serve the API until a signal
//	examend migrate  apply the migrations the database lacks, then exit
//	examend version  print the version, the commit, the build date and the schema version
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"latere.ai/x/examen/internal/store/migrations"
	"latere.ai/x/examen/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, Hooks{})
	stop()
	os.Exit(code)
}

// usage is what a command line that names no known command prints.
const usage = "usage: examend serve | migrate | version (one command, no flag; configured by the environment)"

// The exit statuses of specs/011-distribution.md.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// run runs one command and answers its exit status. getenv is os.Getenv,
// out takes what the command prints and logs the log and the line that
// names a failure.
func run(ctx context.Context, args []string, getenv func(string) string, out, logs io.Writer, hooks Hooks) int {
	if len(args) != 1 {
		return fail(logs, exitUsage, usage)
	}
	var err error
	switch args[0] {
	case "version":
		err = printVersion(out)
	case "serve":
		err = serve(ctx, getenv, logs, hooks)
	case "migrate":
		err = migrate(ctx, getenv, out, logs)
	default:
		return fail(logs, exitUsage, usage)
	}
	if err != nil {
		return fail(logs, exitFailure, "examend: "+err.Error())
	}
	return exitOK
}

// fail writes one line and answers the status. An error of several parts,
// such as every variable that does not parse, stays one line.
func fail(logs io.Writer, code int, line string) int {
	line = strings.ReplaceAll(strings.TrimSpace(line), "\n", "; ")
	if _, err := fmt.Fprintln(logs, line); err != nil {
		// Nothing is left to tell; the status still says it.
		return code
	}
	return code
}

// printVersion prints the build identity and the newest schema version the
// binary carries.
func printVersion(out io.Writer) error {
	schema, err := migrations.Highest()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, version.Line(schema))
	return err
}
