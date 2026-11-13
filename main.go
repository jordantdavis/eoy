// Command eoy is a command line tool with kubectl-style commands.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/jordantdavis/eoy/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, cli.NewRootCmd(cli.Resources()), os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
