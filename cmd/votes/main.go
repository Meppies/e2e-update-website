// Command votes counts session ratings and saves favourites sent in as GitHub
// issues. The site opens a pre-filled issue form; GitHub handles the login, so
// every vote belongs to one GitHub account. Each account has one vote per
// session and the latest one counts.
//
// Votes are private. The site encrypts event, session and stars with the
// public key in index.html, so the issue only shows that someone voted. This
// program decrypts them with the private key (secret VOTE_KEY), keeps the
// register encrypted in data/votes.enc, and publishes only averages of at
// least MinVotes votes in site/ratings.json, and only with -publish (the daily
// run), so a single vote cannot be read from the change it makes.
//
// Input is the JSON of `gh issue list --json number,author,body,title`.
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MinVotes is the smallest number of votes for which an average is shown.
const MinVotes = 3

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
	N   int     `json:"-"` // never published: only the average is shown
	Avg float64 `json:"avg"`
}

type ballot struct {
	Event   string `json:"e"`
	Session string `json:"s"`
	Stars   int    `json:"n"`
}

type keys struct {
	priv  *rsa.PrivateKey
	store []byte // AES key for the register
	salt  []byte // HMAC key for voter ids
}

func loadKeys() (*keys, error) {
	raw := os.Getenv("VOTE_KEY")
	if raw == "" {
		return nil, errors.New("VOTE_KEY is not set")
	}
	blk, _ := pem.Decode([]byte(raw))
	if blk == nil {
		return nil, errors.New("VOTE_KEY is not a PEM key")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("VOTE_KEY is not an RSA key")
	}
	der := x509.MarshalPKCS1PrivateKey(priv)
	a := sha256.Sum256(append([]byte("e2e-votes-store:"), der...))
	b := sha256.Sum256(append([]byte("e2e-votes-voter:"), der...))
	return &keys{priv, a[:], b[:]}, nil
}

func (k *keys) open(b64 string) (ballot, error) {
	var bl ballot
	ct, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64), ""))
	if err != nil {
		return bl, err
	}
	pt, err := rsa.DecryptOAEP(sha256.New(), nil, k.priv, ct, nil)
	if err != nil {
		return bl, err
	}
	err = json.Unmarshal(pt, &bl)
	return bl, err
}

func (k *keys) voter(login string) string {
	m := hmac.New(sha256.New, k.salt)
	m.Write([]byte(strings.ToLower(login)))
	return hex.EncodeToString(m.Sum(nil)[:12])
}

func (k *keys) readStore(path string) (map[string]map[string]int, error) {
	votes := map[string]map[string]int{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return votes, nil
	} else if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(k.store)
	gcm, _ := cipher.NewGCM(blk)
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("register too short")
	}
	pt, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return nil, fmt.Errorf("register cannot be read with this VOTE_KEY: %w", err)
	}
	return votes, json.Unmarshal(pt, &votes)
}

func (k *keys) writeStore(path string, votes map[string]map[string]int) error {
	pt, _ := json.Marshal(votes)
	blk, _ := aes.NewCipher(k.store)
	gcm, _ := cipher.NewGCM(blk)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	out := base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, pt, nil))
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, []byte(out+"\n"), 0o644)
}

func main() {
	ratingsIn := flag.String("ratings", "", "open rating issues (gh issue list JSON)")
	favsIn := flag.String("favourites", "", "open favourites issues (gh issue list JSON)")
	site := flag.String("site", "site", "site folder")
	data := flag.String("data", "data", "folder for the vote register")
	actionsOut := flag.String("actions", "actions.json", "where to write the replies")
	publish := flag.Bool("publish", false, "rewrite site/ratings.json from the register")
	flag.Parse()

	sessions := loadSessions(*site)
	votesPath := filepath.Join(*data, "votes.enc")
	k, keyErr := loadKeys()
	votes := map[string]map[string]int{}
	if keyErr == nil {
		var err error
		if votes, err = k.readStore(votesPath); err != nil {
			fail(err)
		}
	}
	before := map[string]map[string]int{}
	for key, m := range votes {
		before[key] = map[string]int{}
		for v, n := range m {
			before[key][v] = n
		}
	}

	var acts []action
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		loc = time.FixedZone("CET", 3600)
	}
	now := time.Now().In(loc)

	ratingIssues := readIssues(*ratingsIn)
	if keyErr != nil && len(ratingIssues) > 0 {
		// Leave the issues open; they are counted once the key is there.
		fmt.Fprintln(os.Stderr, "votes: cannot count ratings:", keyErr)
		ratingIssues = nil
	}
	for _, is := range ratingIssues {
		if is.Author.IsBot || is.Author.Login == "" {
			acts = append(acts, action{is.Number, "Votes come from people, not bots. Closing this one.", "not planned"})
			continue
		}
		bl, err := k.open(fields(is.Body)["vote"])
		if err != nil {
			acts = append(acts, action{is.Number, "Sorry, this vote could not be read. Please vote with the stars on https://e2emvc.meppie.eu/, they fill in this form for you.", "not planned"})
			continue
		}
		ev, sid, stars := bl.Event, bl.Session, bl.Stars
		s, ok := sessions[ev+"/"+sid]
		// Replies never repeat the session or the stars: the issue is public.
		switch {
		case !ok:
			acts = append(acts, action{is.Number, "Sorry, I could not find that session. Please use the stars on https://e2emvc.meppie.eu/ to rate, they fill in this form for you.", "not planned"})
		case s.Kind == "break" || stars < 1 || stars > 5:
			acts = append(acts, action{is.Number, "Sorry, this vote could not be counted. Please vote with the stars on the site.", "not planned"})
		case !started(s, now, loc):
			acts = append(acts, action{is.Number, "This session has not started yet. You can rate it once you have seen it.", "not planned"})
		default:
			key := ev + "/" + sid
			if votes[key] == nil {
				votes[key] = map[string]int{}
			}
			votes[key][k.voter(is.Author.Login)] = stars
			acts = append(acts, action{is.Number, fmt.Sprintf("Thanks @%s, your vote is counted. It stays private: only the averages are shown, once a session has %d votes, and they are updated once a day.", is.Author.Login, MinVotes), "completed"})
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

	// The register is encrypted with a fresh nonce, so write it only on a real change.
	if keyErr == nil && !reflect.DeepEqual(before, votes) {
		if err := k.writeStore(votesPath, votes); err != nil {
			fail(err)
		}
	}

	// Averages per event and session, only from MinVotes votes, only on the daily run.
	if *publish && keyErr == nil {
		agg := map[string]map[string]*stat{}
		for key, byVoter := range votes {
			ev, sid, ok := strings.Cut(key, "/")
			if !ok {
				continue
			}
			st := &stat{}
			sum := 0
			for _, v := range byVoter {
				if v >= 1 && v <= 5 {
					st.N++
					sum += v
				}
			}
			if st.N < MinVotes {
				continue
			}
			st.Avg = math.Round(float64(sum)/float64(st.N)*10) / 10
			if agg[ev] == nil {
				agg[ev] = map[string]*stat{}
			}
			agg[ev][sid] = st
		}
		b, _ := json.MarshalIndent(map[string]any{"minVotes": MinVotes, "events": agg}, "", "  ")
		if err := os.WriteFile(filepath.Join(*site, "ratings.json"), append(b, '\n'), 0o644); err != nil {
			fail(err)
		}
	}

	sort.Slice(acts, func(i, j int) bool { return acts[i].Number < acts[j].Number })
	if acts == nil {
		acts = []action{}
	}
	b, _ := json.MarshalIndent(acts, "", "  ")
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
