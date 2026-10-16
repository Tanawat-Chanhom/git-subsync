// Command git-subsync is a Git extension; git runs it as `git subsync`.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Tanawat-Chanhom/git-subsync/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := cli.Execute(ctx)
	stop()
	if err != nil {
		if msg := err.Error(); msg != "" {
			fmt.Fprintf(os.Stderr, "git-subsync: %s\n", msg)
		}
		os.Exit(cli.ExitCode(err))
	}
}
