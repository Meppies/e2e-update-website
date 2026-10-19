// Command votes counts session ratings and saves favourites sent in as GitHub
// issues. The site opens a pre-filled issue form; GitHub handles the login, so
// every vote belongs to one GitHub account. Each account has one vote per
// session and the latest one counts.
//
// Input is the JSON of `gh issue list --json number,author,body,title`.
// Output: data/votes.json (who voted what, accounts hashed), site/ratings.json
// (averages for the site), site/favs/<login>.json, and a list of replies for
// the workflow to post before it closes each issue.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Author struct {
		Login string `json:"login"`
		IsBot bool   `json:"is_bot"`
	} `json:"author"`
}

type action struct {
	Number  int    `json:"number"`
	Comment string `json:"comment"`
	Reason  string `json:"reason"` // "completed" or "not planned"
}

type session struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Start string `json:"start"`
	Kind  string `json:"kind"`
}

type agenda struct {
	Event *struct {
		ID string `json:"id"`
	} `json:"event"`
	Days []struct {
		Date     string    `json:"date"`
		Sessions []session `json:"sessions"`
	} `json:"days"`
}

type known struct {
	session
	Date string
}

type stat struct {
	N    int     `json:"n"`
	Avg  float64 `json:"avg"`
	Dist [5]int  `json:"dist"`
}

func main() {
	ratingsIn := flag.String("ratings", "", "open rating issues (gh issue list JSON)")
	favsIn := flag.String("favourites", "", "open favourites issues (gh issue list JSON)")
	site := flag.String("site", "site", "site folder")
	data := flag.String("data", "data", "folder for the vote register")
	actionsOut := flag.String("actions", "actions.json", "where to write the replies")
	flag.Parse()

	sessions := loadSessions(*site)
	votesPath := filepath.Join(*data, "votes.json")
	votes := map[string]map[string]int{}
	if b, err := os.ReadFile(votesPath); err == nil {
		_ = json.Unmarshal(b, &votes)
	}

	var acts []action
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		loc = time.FixedZone("CET", 3600)
	}
	now := time.Now().In(loc)

	for _, is := range readIssues(*ratingsIn) {
		if is.Author.IsBot || is.Author.Login == "" {
			acts = append(acts, action{is.Number, "Votes come from people, not bots. Closing this one.", "not planned"})
			continue
		}
		f := fields(is.Body)
		ev, sid := strings.TrimSpace(f["event"]), strings.TrimSpace(f["session"])
		stars, _ := strconv.Atoi(strings.TrimSpace(strings.TrimRight(f["stars"], "★* ")))
		s, ok := sessions[ev+"/"+sid]
		switch {
		case !ok:
			acts = append(acts, action{is.Number, "Sorry, I could not find that session. Please use the stars on https://e2emvc.meppie.eu/ to rate, they fill in this form for you.", "not planned"})
		case s.Kind == "break":
			acts = append(acts, action{is.Number, "Breaks cannot be rated (not even the coffee). Closing this one.", "not planned"})
		case stars < 1 || stars > 5:
			acts = append(acts, action{is.Number, "A rating is 1 to 5 stars. Please try again from the site.", "not planned"})
		case !started(s, now, loc):
			acts = append(acts, action{is.Number, fmt.Sprintf("“%s” has not started yet. You can rate it once you have seen it.", s.Title), "not planned"})
		default:
			key := ev + "/" + sid
			if votes[key] == nil {
				votes[key] = map[string]int{}
			}
			_, again := votes[key][voter(is.Author.Login)]
			votes[key][voter(is.Author.Login)] = stars
			msg := fmt.Sprintf("Thanks @%s! Your %s for “%s” is counted.", is.Author.Login, starText(stars), s.Title)
			if again {
				msg = fmt.Sprintf("Thanks @%s! Your vote for “%s” is now %s; it replaces your earlier one.", is.Author.Login, s.Title, starText(stars))
			}
			acts = append(acts, action{is.Number, msg + " The average on the site updates in a minute or two.", "completed"})
		}
	}

	idRe := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,100}$`)
	for _, is := range readIssues(*favsIn) {
		login := strings.ToLower(is.Author.Login)
		if is.Author.IsBot || login == "" {
			acts = append(acts, action{is.Number, "Closing: favourites belong to people.", "not planned"})
			continue
		}
		var ids []string
		seen := map[string]bool{}
		for _, l := range strings.Split(fields(is.Body)["favourites"], "\n") {
			l = strings.TrimSpace(strings.Trim(l, "-*` "))
			if idRe.MatchString(l) && !seen[l] && len(ids) < 500 {
				seen[l] = true
				ids = append(ids, l)
			}
		}
		out := map[string]any{"login": login, "updatedAt": time.Now().UTC().Format(time.RFC3339), "stars": ids}
		if ids == nil {
			out["stars"] = []string{}
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		p := filepath.Join(*site, "favs", login+".json")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
			fail(err)
		}
		acts = append(acts, action{is.Number, fmt.Sprintf("Saved %d favourite sessions for @%s. On another phone or computer: open https://e2emvc.meppie.eu/, tap “Sync” and enter `%s`.", len(ids), is.Author.Login, is.Author.Login), "completed"})
	}

	// The register: who (hashed) gave what. Stable output, so git only sees real changes.
	_ = os.MkdirAll(*data, 0o755)
	b, _ := json.MarshalIndent(votes, "", "  ")
	if err := os.WriteFile(votesPath, append(b, '\n'), 0o644); err != nil {
		fail(err)
	}

	// Averages per event and session for the site.
	agg := map[string]map[string]*stat{}
	for key, byVoter := range votes {
		ev, sid, ok := strings.Cut(key, "/")
		if !ok || len(byVoter) == 0 {
			continue
		}
		st := &stat{}
		sum := 0
		for _, v := range byVoter {
			if v >= 1 && v <= 5 {
				st.N++
				st.Dist[v-1]++
				sum += v
			}
		}
		if st.N == 0 {
			continue
		}
		st.Avg = math.Round(float64(sum)/float64(st.N)*10) / 10
		if agg[ev] == nil {
			agg[ev] = map[string]*stat{}
		}
		agg[ev][sid] = st
	}
	b, _ = json.MarshalIndent(map[string]any{"events": agg}, "", "  ")
	if err := os.WriteFile(filepath.Join(*site, "ratings.json"), append(b, '\n'), 0o644); err != nil {
		fail(err)
	}

	sort.Slice(acts, func(i, j int) bool { return acts[i].Number < acts[j].Number })
	if acts == nil {
		acts = []action{}
	}
	b, _ = json.MarshalIndent(acts, "", "  ")
	if err := os.WriteFile(*actionsOut, b, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("%d issues handled, %d sessions with votes\n", len(acts), len(votes))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "votes:", err)
	os.Exit(1)
}

func readIssues(path string) []issue {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	var list []issue
	if err := json.Unmarshal(b, &list); err != nil {
		fail(err)
	}
	return list
}

// fields reads a GitHub issue form body: "### Label\n\nvalue" blocks. Keys are
// the first word of the label in lower case ("Stars (1-5)" -> "stars").
func fields(body string) map[string]string {
	out := map[string]string{}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	for _, part := range strings.Split("\n"+body, "\n### ")[1:] {
		label, value, _ := strings.Cut(part, "\n")
		key := strings.ToLower(strings.Fields(label + " x")[0])
		value = strings.TrimSpace(value)
		if value == "_No response_" {
			value = ""
		}
		out[key] = value
	}
	return out
}

func voter(login string) string {
	h := sha256.Sum256([]byte("e2e-vote:" + strings.ToLower(login)))
	return hex.EncodeToString(h[:8])
}

func starText(n int) string {
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

// started: a session can be rated from its start time (event local time,
// which is Central European time for every E2E venue so far).
func started(s known, now time.Time, loc *time.Location) bool {
	at := s.Date + " 00:00"
	if s.Start != "" {
		at = s.Date + " " + s.Start
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", at, loc)
	return err == nil && !now.Before(t)
}

// loadSessions reads the live agenda and every archived event.
func loadSessions(site string) map[string]known {
	out := map[string]known{}
	files, _ := filepath.Glob(filepath.Join(site, "events", "*", "agenda.json"))
	files = append(files, filepath.Join(site, "agenda.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var a agenda
		if json.Unmarshal(b, &a) != nil || a.Event == nil || a.Event.ID == "" {
			continue
		}
		for _, d := range a.Days {
			for _, s := range d.Sessions {
				out[a.Event.ID+"/"+s.ID] = known{s, d.Date}
			}
		}
	}
	return out
}
