# Runbook: maintenance

Two ways to take the instance out of use. Both show visitors a maintenance page instead of an error.

## Maintenance mode (the app runs, only admins get in)

Use it to look at a problem, check data or settings, or before a risky change while the app keeps running.

- Start: admin system page, "Start maintenance" (optional note such as "back at 18:00"), or on the server
  `docker compose … exec serve /usr/local/bin/getafjob admin maintenance on -message "back at 18:00"`.
- End: "End maintenance" in the banner or on the system page, or `admin maintenance off`. `admin maintenance status`
  shows the state.
- The switch is stored in the database: it survives restarts and deploys.

While it is on:

| Who or what | What happens |
|---|---|
| Visitors and users | Every page answers 503 with the maintenance page (EN/DE, `noindex`, the admin's note). Sign-in and the mail link stay open. |
| Admins | Sign in and use the app as usual; a banner says maintenance is on. |
| API (`/api/`) | 503. Health checks (`/healthz`, `/readyz`) stay up. |
| Runners | Get no new run. Running and waiting runs are parked with the reason "maintenance"; a run that is submitting finishes its attempt. |
| Worker | Cancels every job when it is due: no crawl, matching, planning, inbox check or mail. Periodic jobs come again at their next time; nothing cancelled runs later on its own. |

After it ends, queued applications are leased again and periodic jobs run on their schedule. Parked runs wait for
their users.

## Containers stopped (deploy, `gj stop`, crash)

When `serve` does not answer, Caddy serves the static maintenance page with 503 (`deploy/maintenance/index.html`, both
languages, no script). Caddy has to keep running for that: stop single services (`gj stop serve worker runner`),
not the whole stack.

Before starting again after a longer stop, look at the job queue (admin queue page): jobs queued in the meantime run
as soon as the worker is back. If they should not, turn the maintenance mode on first, start the containers, then end
it.

The page is generated from `views.StaticMaintenance` by `go tool task generate` with the colors of `ui.css`;
`deploy/test-fallback.sh` checks it in CI.
