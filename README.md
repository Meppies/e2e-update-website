# E2E Malta agenda

A readable, phone-friendly copy of the agenda for E2E EUCDEX.Ai Malta (15-18 October 2026). The organiser's page at [e2emvc.com](https://e2emvc.com/) is hard to read on a phone and changes during the event, so this site copies it every 5 minutes.

Unofficial. The organiser's page is leading.

## How it works

- `cmd/scrape` (Go) fetches e2emvc.com, parses the four day sections into `site/agenda.json` and compares the result with the previous version. New or changed sessions get a change marker with a timestamp; removed sessions go into a `removed` list.
- `.github/workflows/update.yml` runs the scraper every 5 minutes until 19 October 2026. It commits `site/agenda.json` only when the agenda changed, then deploys `site/` to GitHub Pages.
- `site/index.html` is a single static page that reads `agenda.json`, refreshes it every 5 minutes, and shows what is on now and next.

## Safety net

The scraper refuses to publish when the parse looks broken (fewer than three days, under 20 sessions, a day that disappeared, or more than 30% of sessions gone). The run then fails, GitHub emails the repository owner, and the last good agenda stays online. Use `-force` to override after checking.

## Run locally

```bash
go run ./cmd/scrape -in testdata/e2emvc-2026-10-10.html -prev /dev/null -out site/agenda.json
cd site && python3 -m http.server 8000
```

Open http://localhost:8000. Add `#demo` to the URL to set the clock to Saturday 17 October, 12:20.

## Timing

GitHub runs scheduled workflows on a best-effort basis. In this account a weekly schedule has started 1 to 8 hours late, and the 5-minute schedule here did not fire at all in its first 40 minutes. So a Mac presses the button instead:

- `scripts/trigger-update.sh` runs `gh workflow run update.yml` and unloads itself after 19 October 2026.
- `scripts/eu.meppie.e2e-agenda-trigger.plist` is the launchd agent that calls it every 300 seconds.
- Installed copy: `~/.local/bin/e2e-agenda-trigger.sh` (launchd cannot read `~/Documents`), agent in `~/Library/LaunchAgents/`, log in `~/Library/Logs/e2e-agenda-trigger.log`.

Stop it early with `launchctl bootout gui/$(id -u)/eu.meppie.e2e-agenda-trigger`.
