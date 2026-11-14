package app

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/application"
	applicationstore "github.com/1xxn/getafjob/internal/application/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/maintenance"
)

// maintenanceControl turns the maintenance mode on and off (#518). Turning it on also parks the running runs, so
// no browser keeps working while only admins can see what happens.
type maintenanceControl struct {
	sw   *maintenance.Switch
	runs *application.Runs
}

func newMaintenanceControl(pool *pgxpool.Pool, sw *maintenance.Switch, cfg config.Config) maintenanceControl {
	st := applicationstore.New(pool)
	return maintenanceControl{sw: sw, runs: application.NewRuns(newApplications(st, cfg), st)}
}

func (m maintenanceControl) State(ctx context.Context) (maintenance.State, error) {
	return m.sw.State(ctx)
}

func (m maintenanceControl) Set(ctx context.Context, on bool, message string) error {
	if err := m.sw.Set(ctx, on, message); err != nil || !on {
		return err
	}
	_, err := m.runs.ParkForMaintenance(ctx)
	return err
}

// paused reports whether background work must wait; serve and worker pass it to the gateway and the queue.
func paused(sw *maintenance.Switch) func(ctx context.Context) bool { return sw.On }

// maintenanceAPI answers API calls with 503 while the instance is in maintenance; the UI has its own page.
func maintenanceAPI(sw *maintenance.Switch, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && sw.On(r.Context()) {
			w.Header().Set("Retry-After", "300")
			http.Error(w, "maintenance", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const maintenanceUsage = "usage: getafjob admin maintenance on [-message …] | off | status"

// adminMaintenance switches the maintenance mode from the command line.
func adminMaintenance(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(env.Stderr, maintenanceUsage)
		return ExitUsage
	}
	cmd := args[0]
	fs := flag.NewFlagSet("maintenance", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	message := fs.String("message", "", "note for visitors on the maintenance page, e.g. when it ends")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || (cmd != "on" && cmd != "off" && cmd != "status") ||
		(cmd != "on" && *message != "") {
		_, _ = fmt.Fprintln(env.Stderr, maintenanceUsage)
		return ExitUsage
	}
	cfg, err := config.Load(env.Config)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "configuration error:\n%v\n", err)
		return ExitUsage
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-admin")
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	defer pool.Close()
	sw := maintenance.New(pool)
	if cmd != "status" {
		if err := newMaintenanceControl(pool, sw, cfg).Set(ctx, cmd == "on", *message); err != nil {
			_, _ = fmt.Fprintln(env.Stderr, "maintenance:", err)
			return ExitError
		}
	}
	st, err := sw.State(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "maintenance:", err)
		return ExitError
	}
	if !st.On {
		_, _ = fmt.Fprintln(env.Stdout, "maintenance off")
		return ExitOK
	}
	_, _ = fmt.Fprintf(env.Stdout, "maintenance on since %s\n", st.Since.Format("2006-01-02 15:04 MST"))
	return ExitOK
}
