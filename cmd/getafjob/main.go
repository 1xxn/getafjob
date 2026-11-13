// Command getafjob is the only binary; the role (serve, worker, runner, migrate, admin) is the first
// argument.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // time zones without the image's zoneinfo (GETAFJOB_TIMEZONE)

	"github.com/1xxn/getafjob/internal/app"
	"github.com/1xxn/getafjob/internal/platform/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app.Main(ctx, os.Args[1:], app.Env{Stdout: os.Stdout, Stderr: os.Stderr, Config: config.OS()})
	stop()
	os.Exit(code)
}
