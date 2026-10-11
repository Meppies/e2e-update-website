// Command scrape reads the agenda from e2emvc.com and writes it as JSON for
// the static site. It keeps change markers from the previous run and refuses
// to publish a result that looks broken.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

type Removed struct {
	At    string `json:"at"`
	Date  string `json:"date"`
	Title string `json:"title"`
	Start string `json:"start,omitempty"`
	Room  string `json:"room,omitempty"`
}

type Agenda struct {
	Source    string    `json:"source"`
	UpdatedAt string    `json:"updatedAt"`
	Days      []Day     `json:"days"`
	Removed   []Removed `json:"removed,omitempty"`
	Info      *Info     `json:"info,omitempty"`
}

const year, month = 2026, 10

func main() {
	url := flag.String("url", "https://e2emvc.com/", "page to scrape")
	in := flag.String("in", "", "read HTML from this file instead of the URL")
	prevPath := flag.String("prev", "site/agenda.json", "previous agenda (for change markers)")
	out := flag.String("out", "site/agenda.json", "where to write the agenda")
	force := flag.Bool("force", false, "write even if the sanity check fails")
	flag.Parse()

	if err := run(*url, *in, *prevPath, *out, *force); err != nil {
		fmt.Fprintln(os.Stderr, "scrape:", err)
		os.Exit(1)
	}
}

func run(url, in, prevPath, out string, force bool) error {
	raw, err := load(url, in)
	if err != nil {
		return err
	}
	secs, err := parseSections(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	next := Agenda{Source: url, UpdatedAt: now}

	next.Info = buildInfo(secs)
	bios := collectBios(secs)
	byDate := map[string]int{}
	for _, sec := range secs {
		d := dayOf(sec.Heading)
		if d == 0 {
			continue
		}
		date := fmt.Sprintf("%d-%02d-%02d", year, month, d)
		sessions := parseDay(sec)
		fillBios(sessions, bios)
		if i, ok := byDate[date]; ok {
			next.Days[i].Sessions = append(next.Days[i].Sessions, sessions...)
			sortSessions(next.Days[i].Sessions)
			continue
		}
		byDate[date] = len(next.Days)
		next.Days = append(next.Days, Day{
			Date:     date,
			Title:    dayTitle(sec.Heading),
			Updating: strings.Contains(strings.ToLower(sec.Heading), "updating"),
			Sessions: sessions,
		})
	}
	sortDays(next.Days)
	crossFill(next.Days)
	n := 0
	for i := range next.Days {
		if next.Days[i].Title == "Main conference" {
			n++
			next.Days[i].Title = fmt.Sprintf("Main conference, day %d", n)
		}
	}
	for i := range next.Days {
		assignIDs(&next.Days[i])
	}

	var prev *Agenda
	if b, err := os.ReadFile(prevPath); err == nil {
		var p Agenda
		if json.Unmarshal(b, &p) == nil && len(p.Days) > 0 {
			prev = &p
		}
	}
	if err := sane(prev, &next); err != nil && !force {
		return fmt.Errorf("not publishing: %w", err)
	}
	markChanges(prev, &next, now)

	if prev != nil && sameContent(prev, &next) {
		fmt.Println("unchanged")
		setOutput("changed", "false")
		return nil
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
		return err
	}
	total := 0
	for _, d := range next.Days {
		total += len(d.Sessions)
		fmt.Printf("%s  %-36s %3d sessions\n", d.Date, d.Title, len(d.Sessions))
	}
	fmt.Printf("changed: %d days, %d sessions\n", len(next.Days), total)
	setOutput("changed", "true")
	return nil
}

func load(url, in string) ([]byte, error) {
	if in != "" {
		return os.ReadFile(in)
	}
	client := &http.Client{Timeout: 45 * time.Second}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; e2e-agenda-mirror/1.0; +https://github.com/Meppies)")
		req.Header.Set("Cache-Control", "no-cache")
		resp, err := client.Do(req)
		if err == nil {
			b, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr == nil && resp.StatusCode == 200 && len(b) > 20000 {
				return b, nil
			}
			err = fmt.Errorf("HTTP %d, %d bytes", resp.StatusCode, len(b))
		}
		lastErr = err
		time.Sleep(time.Duration(attempt*5) * time.Second)
	}
	return nil, lastErr
}

func setOutput(k, v string) {
	if p := os.Getenv("GITHUB_OUTPUT"); p != "" {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%s=%s\n", k, v)
			f.Close()
		}
	}
}

func sortDays(ds []Day) {
	for i := 1; i < len(ds); i++ {
		for j := i; j > 0 && ds[j].Date < ds[j-1].Date; j-- {
			ds[j], ds[j-1] = ds[j-1], ds[j]
		}
	}
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	return s
}

func assignIDs(d *Day) {
	rooms := map[string]bool{}
	seen := map[string]int{}
	for i := range d.Sessions {
		s := &d.Sessions[i]
		base := d.Date[8:] + "-" + slug(s.Title)
		seen[base]++
		s.ID = base
		if n := seen[base]; n > 1 {
			s.ID = fmt.Sprintf("%s-%d", base, n)
		}
		if s.Room != "" {
			rooms[s.Room] = true
		}
	}
	d.Rooms = nil
	for r := range rooms {
		d.Rooms = append(d.Rooms, r)
	}
	for i := 1; i < len(d.Rooms); i++ {
		for j := i; j > 0 && d.Rooms[j] < d.Rooms[j-1]; j-- {
			d.Rooms[j], d.Rooms[j-1] = d.Rooms[j-1], d.Rooms[j]
		}
	}
}

// sane refuses results that are clearly the product of a layout change
// rather than an agenda change: a day that vanished or a big drop in sessions.
func sane(prev, next *Agenda) error {
	total := 0
	for _, d := range next.Days {
		total += len(d.Sessions)
	}
	if len(next.Days) < 3 || total < 20 {
		return fmt.Errorf("only %d days and %d sessions parsed", len(next.Days), total)
	}
	if prev == nil {
		return nil
	}
	prevTotal := 0
	prevDays := map[string]int{}
	for _, d := range prev.Days {
		prevTotal += len(d.Sessions)
		prevDays[d.Date] = len(d.Sessions)
	}
	if total*10 < prevTotal*7 {
		return fmt.Errorf("sessions dropped from %d to %d", prevTotal, total)
	}
	for _, d := range next.Days {
		delete(prevDays, d.Date)
	}
	for date, n := range prevDays {
		if n > 0 {
			return errors.New("day " + date + " disappeared")
		}
	}
	return nil
}

func fingerprint(s Session) map[string]string {
	var names []string
	for _, sp := range s.Speakers {
		names = append(names, sp.Name)
	}
	return map[string]string{
		"time":        s.Start + "-" + s.End,
		"room":        s.Room,
		"speakers":    strings.Join(names, ", "),
		"description": s.Description,
		"title":       s.Title,
	}
}

// markChanges compares with the previous run and carries old markers forward.
func markChanges(prev, next *Agenda, now string) {
	if prev == nil {
		return
	}
	old := map[string]Session{}
	oldDay := map[string]string{}
	for _, d := range prev.Days {
		for _, s := range d.Sessions {
			old[s.ID] = s
			oldDay[s.ID] = d.Date
		}
	}
	next.Removed = append(next.Removed, prev.Removed...)
	seen := map[string]bool{}
	for di := range next.Days {
		for si := range next.Days[di].Sessions {
			s := &next.Days[di].Sessions[si]
			seen[s.ID] = true
			o, ok := old[s.ID]
			if !ok {
				s.Changed = &Change{Kind: "new", At: now}
				continue
			}
			var what []string
			a, b := fingerprint(o), fingerprint(*s)
			for _, k := range []string{"time", "room", "speakers", "title", "description"} {
				if a[k] != b[k] {
					what = append(what, k)
				}
			}
			if len(what) > 0 {
				s.Changed = &Change{Kind: "updated", At: now, What: what}
			} else {
				s.Changed = o.Changed
			}
		}
	}
	for id, o := range old {
		if !seen[id] {
			next.Removed = append([]Removed{{At: now, Date: oldDay[id], Title: o.Title, Start: o.Start, Room: o.Room}}, next.Removed...)
		}
	}
	if len(next.Removed) > 60 {
		next.Removed = next.Removed[:60]
	}
}

func sameContent(a, b *Agenda) bool {
	x, y := *a, *b
	x.UpdatedAt, y.UpdatedAt = "", ""
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

// collectBios builds name -> speaker (bio and photo) from every card that
// looks like a speaker card: a name heading followed by a paragraph that
// starts with that name.
func collectBios(secs []Section) map[string]Speaker {
	out := map[string]Speaker{}
	for _, sec := range secs {
		for _, c := range sec.Cards {
			h := nameJunk.ReplaceAllString(firstHeading(c, nil), "")
			if !looksLikeName(nameCase(h)) {
				continue
			}
			var parts []string
			for _, p := range c.Paras() {
				parts = append(parts, p.Text())
			}
			bio := joinParas(parts)
			first := strings.Fields(nameCase(h))[0]
			lead := strings.Fields(bio + " x")[0]
			if len(bio) < 60 || !(strings.HasPrefix(first, lead) || strings.HasPrefix(lead, first)) {
				continue
			}
			key := strings.ToLower(nameCase(h))
			if _, ok := out[key]; !ok {
				out[key] = Speaker{Bio: bio, Photo: c.Image}
			}
		}
	}
	return out
}

func lookupSpeaker(name string, bios map[string]Speaker) (Speaker, bool) {
	key := strings.ToLower(name)
	if b, ok := bios[key]; ok {
		return b, true
	}
	// Fall back to a unique surname match ("Sam Legrand" -> "Samuel Legrand").
	f := strings.Fields(key)
	if len(f) < 2 {
		return Speaker{}, false
	}
	var match Speaker
	n := 0
	for k, b := range bios {
		kf := strings.Fields(k)
		if kf[len(kf)-1] == f[len(f)-1] && strings.HasPrefix(kf[0], f[0][:1]) {
			match = b
			n++
		}
	}
	return match, n == 1
}

func fillBios(ss []Session, bios map[string]Speaker) {
	for i := range ss {
		for j := range ss[i].Speakers {
			sp := &ss[i].Speakers[j]
			if sp.Bio != "" && sp.Photo != "" {
				continue
			}
			if b, ok := lookupSpeaker(sp.Name, bios); ok {
				if sp.Bio == "" {
					sp.Bio = b.Bio
				}
				if sp.Photo == "" {
					sp.Photo = b.Photo
				}
			}
		}
	}
}

var parenRe = regexp.MustCompile(`\([^)]*\)|\bpart\s*\d\b`)

func titleKey(t string) string {
	return strings.TrimSpace(slugRe.ReplaceAllString(parenRe.ReplaceAllString(strings.ToLower(t), " "), " "))
}

func sameTalk(a, b string) bool {
	ka, kb := titleKey(a), titleKey(b)
	if len(ka) < 15 || len(kb) < 15 {
		return ka == kb && ka != ""
	}
	return strings.HasPrefix(ka, kb) || strings.HasPrefix(kb, ka) || strings.HasSuffix(ka, kb) || strings.HasSuffix(kb, ka)
}

// crossFill gives a "part 2" session the speakers and description of its
// intro on another day when the source left them out, and fills empty bios
// from the longest bio found for the same name anywhere in the agenda.
func crossFill(days []Day) {
	type ref struct{ d, s int }
	var all []ref
	for di := range days {
		for si := range days[di].Sessions {
			all = append(all, ref{di, si})
		}
	}
	for _, r := range all {
		s := &days[r.d].Sessions[r.s]
		if s.Kind != "talk" || (len(s.Speakers) > 0 && s.Description != "") {
			continue
		}
		for _, o := range all {
			if o.d == r.d {
				continue
			}
			src := days[o.d].Sessions[o.s]
			if !sameTalk(s.Title, src.Title) {
				continue
			}
			if len(s.Speakers) == 0 && len(src.Speakers) > 0 {
				s.Speakers = append([]Speaker(nil), src.Speakers...)
				if s.Photo == "" {
					s.Photo = src.Photo
				}
			}
			if s.Description == "" {
				s.Description = src.Description
			}
			break
		}
	}
	best := map[string]string{}
	photo := map[string]string{}
	for _, r := range all {
		for _, sp := range days[r.d].Sessions[r.s].Speakers {
			k := strings.ToLower(sp.Name)
			if len(sp.Bio) > len(best[k]) {
				best[k] = sp.Bio
			}
			if photo[k] == "" {
				photo[k] = sp.Photo
			}
		}
	}
	for _, r := range all {
		for j := range days[r.d].Sessions[r.s].Speakers {
			sp := &days[r.d].Sessions[r.s].Speakers[j]
			k := strings.ToLower(sp.Name)
			if sp.Bio == "" {
				sp.Bio = best[k]
			}
			if sp.Photo == "" {
				sp.Photo = photo[k]
			}
		}
	}
}
