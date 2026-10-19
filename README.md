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

The site is static, so voting goes through GitHub, which also handles the login. Votes are private:

1. Tapping a star on a session that has started encrypts `{event, session, stars}` in the browser with the public key in `index.html` (RSA-OAEP, SHA-256) and opens a pre-filled issue form (`.github/ISSUE_TEMPLATE/rating.yml`). The issue only shows that someone voted. The visitor presses Submit.
2. The workflow decrypts open `rating` issues with the private key (repository secret `VOTE_KEY`), checks the session and that it has started, and stores the vote under a keyed hash of the GitHub account (one vote per account per session, the latest counts). The register `data/votes.enc` is encrypted (AES-GCM, key derived from `VOTE_KEY`). Replies never mention the session or the stars.
3. Only the daily, manual and push runs rewrite `site/ratings.json`, and only for sessions with at least 3 votes: averages only, no counts per star.

Replacing `VOTE_KEY` makes the register unreadable, so it starts again from zero.

Favourites work the same way, but are not secret: **Sync → Save to GitHub** opens a `favourites` issue with the starred sessions; the workflow writes `site/favs/<login>.json`. On another device, **Sync → Load** with the GitHub name merges them in.

## Safety net

The scraper refuses to publish when the parse looks broken: no event heading, no about text and no sponsors, a day that disappeared, or more than 30% of sessions gone. When the organiser switches to the next event, the comparison with the old agenda is skipped. Use `-force` to override after checking.

## Run locally

```bash
go run ./cmd/scrape -in testdata/e2emvc-2026-10-10.html -prev /dev/null -out site/agenda.json
cd site && python3 -m http.server 8000
```

Open http://localhost:8000.
