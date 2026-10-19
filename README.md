# E2E EUCDEX.Ai unofficial

The name follows the organiser’s own, taken from the "What is … CONFERENCE?" heading on e2emvc.com.

A readable, phone-friendly copy of the E2E EUCDEX.Ai conference agenda, with an archive of past events and session ratings. Live at https://e2emvc.meppie.eu/.

Unofficial. The organiser's page at [e2emvc.com](https://e2emvc.com/) is leading.

## What the site shows when

- **More than a week before an event:** only the general information (about, upcoming events, sponsors).
- **From a week before the first day until the last day:** the agenda, with what is on now and next.
- **From the day after the last day:** the agenda is frozen in `site/events/<id>/agenda.json` and listed under **Past**. The front page goes back to the general information.

## How it works

- `cmd/scrape` (Go) fetches e2emvc.com and writes `site/agenda.json`: the days, the general information and an `event` block (id, name, first and last day). New or changed sessions get a change marker; removed ones go into a `removed` list. With `-events site/events` it first archives the previous event once it has ended.
- `cmd/votes` (Go) handles ratings and favourites sent in as GitHub issues (see below) and writes the encrypted register `data/votes.enc` and `site/ratings.json`.
- `.github/workflows/update.yml` runs once a day, on every push, on manual runs, and when someone opens a rating or favourites issue. It commits what changed and deploys `site/` to GitHub Pages. If the repository has had no commit for 50 days it writes `.github/keepalive`, because GitHub switches off schedules in quiet repositories after 60 days.
- `site/index.html` is a single static page that reads the JSON files.

## Ratings and favourites

Visitors vote on the site itself. The flow, and how it keeps bots out:

1. **Sign in with GitHub, once per device.** The GitHub App *E2E Unofficial Votes* (owned by Meppies, installed only on this repository, permission: issues) signs people in. The Cloudflare Worker in `worker/` (`https://e2e-votes.meppie.workers.dev`) holds the app's client secret and swaps the login code for a user token, which the site keeps in the browser.
2. **Voting.** The site encrypts `{event, session, stars}` with the public key in `index.html` (RSA-OAEP, SHA-256) and sends it to the Worker together with a Cloudflare Turnstile token (human check, usually invisible).
3. **Checks in the Worker.** Turnstile must pass, the GitHub account must be a personal account (type `User`) and at least `MIN_ACCOUNT_DAYS` (7) days old. The Worker then signs the ballot (HMAC with `VOTE_HMAC`, bound to the GitHub login) and opens an issue titled "Session rating" as the user.
4. **Counting.** The workflow reads open issues; `cmd/votes` counts only ballots with a valid signature, decrypts them with `VOTE_KEY`, checks the session has started, and stores one vote per account per session (the latest counts) in the encrypted register `data/votes.enc`. Replies never mention the session or the stars.
5. **Publishing.** Only the daily, manual and push runs rewrite `site/ratings.json`, and only for sessions with at least 3 votes: averages only.

Favourites: **Sync → Save** sends the starred sessions through the Worker as a "Favourites" issue; the workflow writes `site/favs/<login>.json` (public). **Get my saved stars** loads them on another signed-in device.

Secrets: repository secrets `VOTE_KEY` and `VOTE_HMAC`; Worker secrets `GH_CLIENT_SECRET`, `TURNSTILE_SECRET` and `VOTE_HMAC` (same value as the repository's). Replacing `VOTE_KEY` makes the register unreadable, so votes start again from zero. Deploy the Worker with `cd worker && npx wrangler deploy`.

## Safety net

The scraper refuses to publish when the parse looks broken: no event heading, no about text and no sponsors, a day that disappeared, or more than 30% of sessions gone. When the organiser switches to the next event, the comparison with the old agenda is skipped. Use `-force` to override after checking.

## Run locally

```bash
go run ./cmd/scrape -in testdata/e2emvc-2026-10-10.html -prev /dev/null -out site/agenda.json
cd site && python3 -m http.server 8000
```

Open http://localhost:8000.
