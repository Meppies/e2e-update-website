// Command pages writes plain HTML pages next to the interactive site, so search
// engines (and people without JavaScript) can read every event, day, session
// and speaker:
//
//	/events/                 all events
//	/events/<id>/            one event: summary, look back, programme, speakers
//	/events/<id>/<day>/      one day: every session with abstract and speakers
//
// Each event page carries schema.org Event data. It also fills the crawlable
// summary on the home page (between the static markers in index.html) and
// rewrites sitemap.xml. Output only changes when the data changes.
//
// An event gets pages once it is archived, or from a week before its first day
// (the same moment the interactive site shows its agenda).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const siteURL = "https://e2emvc.meppie.eu"

type speaker struct {
	Name string `json:"name"`
	Role string `json:"role"`
	Bio  string `json:"bio"`
}

type session struct {
	ID          string    `json:"id"`
	Start       string    `json:"start"`
	End         string    `json:"end"`
	Room        string    `json:"room"`
	Kind        string    `json:"kind"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Speakers    []speaker `json:"speakers"`
}

type day struct {
	Date     string    `json:"date"`
	Title    string    `json:"title"`
	Sessions []session `json:"sessions"`
}

type eventMeta struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Place string `json:"place"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type agenda struct {
	UpdatedAt  string     `json:"updatedAt"`
	ArchivedAt string     `json:"archivedAt"`
	Days       []day      `json:"days"`
	Event      *eventMeta `json:"event"`
	Info       *struct {
		Brand string   `json:"brand"`
		About []string `json:"about"`
		Event *struct {
			Title   string   `json:"title"`
			Details []string `json:"details"`
		} `json:"event"`
		Upcoming []struct {
			Title string   `json:"title"`
			Lines []string `json:"lines"`
		} `json:"upcoming"`
	} `json:"info"`
}

type recap struct {
	Title  string      `json:"title"`
	Teaser string      `json:"teaser"`
	Stats  [][2]string `json:"stats"`
	Paras  []string    `json:"paras"`
}

// Page data for the templates.
type evPage struct {
	Brand, ID, Name, Place, Range, Venue, Address, URL, Updated string
	Past                                                        bool
	Days                                                        []dayPage
	Speakers                                                    []spkEntry
	Recap                                                       *recap
	RecapHTML                                                   []template.HTML
	Sessions                                                    int
	JSONLD                                                      template.JS
}

type dayPage struct {
	Key, Long, Title, URL, AppURL string
	Sessions                      []sessPage
}

type sessPage struct {
	ID, Time, Room, Title, Kind string
	Paras                       []string
	Speakers                    []speaker
}

type spkEntry struct {
	Name     string
	Sessions []sessRef
}

type sessRef struct{ Title, URL string }

type homeData struct {
	Brand  string
	About  []string
	Events []evPage
	Next   string
}

func main() {
	site := flag.String("site", "site", "site folder")
	flag.Parse()

	live := read[agenda](filepath.Join(*site, "agenda.json"))
	var index []eventMeta
	_ = readInto(filepath.Join(*site, "events", "index.json"), &index)
	recaps := map[string]recap{}
	_ = readInto(filepath.Join(*site, "recaps.json"), &recaps)

	brand := "E2E EUCDEX.Ai"
	if live.Info != nil && live.Info.Brand != "" {
		brand = live.Info.Brand
	}

	type src struct {
		a    agenda
		past bool
	}
	var events []src
	seen := map[string]bool{}
	for _, e := range index {
		a := read[agenda](filepath.Join(*site, "events", e.ID, "agenda.json"))
		if a.Event == nil {
			a.Event = &e
		}
		events = append(events, src{a, true})
		seen[e.ID] = true
	}
	today := time.Now().In(cet()).Format("2006-01-02")
	if ev := live.Event; ev != nil && !seen[ev.ID] && len(live.Days) > 0 && today >= addDays(ev.Start, -7) {
		events = append(events, src{live, false})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].a.Event.Start > events[j].a.Event.Start })

	var urls []sitemapURL
	var cards []evPage
	for _, e := range events {
		p := buildEvent(e.a, brand, e.past, recaps)
		dir := filepath.Join(*site, "events", p.ID)
		write(filepath.Join(dir, "index.html"), render(eventTpl, p))
		urls = append(urls, sitemapURL{p.URL, p.Updated})
		for _, d := range p.Days {
			write(filepath.Join(dir, d.Key, "index.html"), render(dayTpl, struct {
				E evPage
				D dayPage
			}{p, d}))
			urls = append(urls, sitemapURL{d.URL, p.Updated})
		}
		cards = append(cards, p)
	}
	home := homeData{Brand: brand, Events: cards}
	if live.Info != nil {
		home.About = live.Info.About
		if len(home.About) > 2 {
			home.About = home.About[:2]
		}
		for _, u := range live.Info.Upcoming {
			if home.Next == "" && len(u.Lines) > 0 && !pastTitle(u.Title, u.Lines, cards) {
				home.Next = cleanTitle(u.Title) + ", " + u.Lines[0]
			}
		}
	}
	write(filepath.Join(*site, "events", "index.html"), render(listTpl, home))
	// The home page changes with the live agenda; the list with the events. No "today": that would commit every day.
	homeMod := latest(append(urls, sitemapURL{"", strings.SplitN(live.UpdatedAt, "T", 2)[0]}))
	urls = append([]sitemapURL{{siteURL + "/", homeMod}, {siteURL + "/events/", latest(urls)}}, urls...)
	writeSitemap(filepath.Join(*site, "sitemap.xml"), urls)
	fillHome(filepath.Join(*site, "index.html"), render(homeTpl, home))
	fmt.Printf("pages: %d events, %d urls in the sitemap\n", len(cards), len(urls))
}

func pastTitle(title string, lines []string, cards []evPage) bool {
	text := title + " " + strings.Join(lines, " ")
	for _, c := range cards {
		if c.Past && regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(c.Place)+`\b`).MatchString(title) && strings.Contains(text, c.Range[len(c.Range)-4:]) {
			return true
		}
	}
	return false
}

var titleJunk = regexp.MustCompile(`(?i)^(\(?e2e\)?\s*)?eucdex\.ai\s*`)

func cleanTitle(t string) string {
	t = titleJunk.ReplaceAllString(strings.TrimSpace(t), "")
	return regexp.MustCompile(`\b[A-Z]{2,}\b`).ReplaceAllStringFunc(t, func(w string) string { return w[:1] + strings.ToLower(w[1:]) })
}

func buildEvent(a agenda, brand string, past bool, recaps map[string]recap) evPage {
	ev := a.Event
	p := evPage{Brand: brand, ID: ev.ID, Name: ev.Name, Place: ev.Place, Past: past,
		Range: dateRange(ev.Start, ev.End), URL: fmt.Sprintf("%s/events/%s/", siteURL, ev.ID)}
	p.Updated = strings.SplitN(firstNonEmpty(a.ArchivedAt, a.UpdatedAt, ev.End), "T", 2)[0]
	if r, ok := recaps[ev.ID]; ok && past {
		p.Recap = &r
		for _, para := range r.Paras {
			p.RecapHTML = append(p.RecapHTML, template.HTML(para)) // hand-written in recaps.json
		}
	}
	if a.Info != nil && a.Info.Event != nil {
		for _, d := range a.Info.Event.Details {
			if m := regexp.MustCompile(`(?i)take place at (?:the )?(.+?)\.\s*Address:\s*(.+?)\.?$`).FindStringSubmatch(d); m != nil {
				p.Venue, p.Address = strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
			}
		}
	}
	appBase := "/#"
	if past {
		appBase = "/#past/" + ev.ID + "/"
	}
	byName := map[string]*spkEntry{}
	for _, d := range a.Days {
		key := strings.ToLower(parse(d.Date).Format("Mon"))
		dp := dayPage{Key: key, Long: parse(d.Date).Format("Monday 2 January 2006"), Title: d.Title,
			URL: p.URL + key + "/", AppURL: appBase + key}
		for _, s := range d.Sessions {
			sp := sessPage{ID: s.ID, Title: s.Title, Kind: s.Kind, Room: s.Room, Speakers: s.Speakers}
			if s.Start != "" {
				sp.Time = s.Start + "–" + s.End
			}
			for _, para := range strings.Split(s.Description, "\n\n") {
				if strings.TrimSpace(para) != "" {
					sp.Paras = append(sp.Paras, strings.TrimSpace(para))
				}
			}
			dp.Sessions = append(dp.Sessions, sp)
			if s.Kind != "break" {
				p.Sessions++
				for _, x := range s.Speakers {
					if x.Name == "" {
						continue
					}
					e := byName[x.Name]
					if e == nil {
						e = &spkEntry{Name: x.Name}
						byName[x.Name] = e
					}
					e.Sessions = append(e.Sessions, sessRef{s.Title, dp.URL + "#" + s.ID})
				}
			}
		}
		p.Days = append(p.Days, dp)
	}
	for _, e := range byName {
		p.Speakers = append(p.Speakers, *e)
	}
	sort.Slice(p.Speakers, func(i, j int) bool { return surname(p.Speakers[i].Name) < surname(p.Speakers[j].Name) })
	p.JSONLD = template.JS(jsonLD(a, p, brand))
	return p
}

func surname(n string) string {
	f := strings.Fields(n)
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[len(f)-1] + " " + n)
}

// jsonLD describes the event and its sessions for search engines (schema.org).
func jsonLD(a agenda, p evPage, brand string) string {
	ev := a.Event
	loc := map[string]any{"@type": "Place", "name": firstNonEmpty(p.Venue, ev.Place),
		"address": map[string]any{"@type": "PostalAddress", "streetAddress": firstNonEmpty(p.Address, ev.Place), "addressLocality": ev.Place}}
	var subs []map[string]any
	performers := map[string]bool{}
	var people []map[string]any
	for _, d := range a.Days {
		for _, s := range d.Sessions {
			if s.Kind == "break" || s.Start == "" {
				continue
			}
			se := map[string]any{"@type": "Event", "name": s.Title,
				"startDate": iso(d.Date, s.Start), "endDate": iso(d.Date, s.End),
				"url":      fmt.Sprintf("%s%s/#%s", p.URL, strings.ToLower(parse(d.Date).Format("Mon")), s.ID),
				"location": loc, "eventAttendanceMode": "https://schema.org/OfflineEventAttendanceMode"}
			var perf []map[string]any
			for _, x := range s.Speakers {
				if x.Name != "" {
					perf = append(perf, map[string]any{"@type": "Person", "name": x.Name})
					if !performers[x.Name] {
						performers[x.Name] = true
						people = append(people, map[string]any{"@type": "Person", "name": x.Name})
					}
				}
			}
			if len(perf) > 0 {
				se["performer"] = perf
			}
			subs = append(subs, se)
		}
	}
	desc := fmt.Sprintf("%s in %s, %s: %d sessions by %d speakers on end-user computing, virtualisation and AI.", ev.Name, ev.Place, p.Range, p.Sessions, len(people))
	doc := map[string]any{
		"@context": "https://schema.org", "@type": "Event",
		"name": ev.Name, "description": desc,
		"startDate": ev.Start, "endDate": ev.End,
		"eventStatus":         "https://schema.org/EventScheduled",
		"eventAttendanceMode": "https://schema.org/OfflineEventAttendanceMode",
		"location":            loc,
		"organizer":           map[string]any{"@type": "Organization", "name": "Experts 2 Experts (" + brand + ")", "url": "https://e2emvc.com/"},
		"url":                 p.URL,
		"image":               siteURL + "/share.png",
		"performer":           people,
		"subEvent":            subs,
	}
	b, _ := json.Marshal(doc)
	return strings.ReplaceAll(string(b), "</", "<\\/")
}

func cet() *time.Location {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		return time.FixedZone("CET", 3600)
	}
	return loc
}

func iso(date, hm string) string {
	t, err := time.ParseInLocation("2006-01-02 15:04", date+" "+hm, cet())
	if err != nil {
		return date
	}
	return t.Format("2006-01-02T15:04:05-07:00")
}

func parse(d string) time.Time { t, _ := time.Parse("2006-01-02", d); return t }

func addDays(d string, n int) string { return parse(d).AddDate(0, 0, n).Format("2006-01-02") }

func dateRange(a, b string) string {
	ta, tb := parse(a), parse(b)
	if a == b {
		return ta.Format("2 January 2006")
	}
	if ta.Year() == tb.Year() && ta.Month() == tb.Month() {
		return fmt.Sprintf("%d–%s", ta.Day(), tb.Format("2 January 2006"))
	}
	return ta.Format("2 January") + " – " + tb.Format("2 January 2006")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func read[T any](path string) T {
	var v T
	if err := readInto(path, &v); err != nil {
		fail(err)
	}
	return v
}

func readInto(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func render(t *template.Template, data any) []byte {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		fail(err)
	}
	return buf.Bytes()
}

func write(path string, b []byte) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		fail(err)
	}
}

type sitemapURL struct{ Loc, Lastmod string }

func latest(u []sitemapURL) string {
	l := ""
	for _, x := range u {
		if x.Lastmod > l {
			l = x.Lastmod
		}
	}
	return l
}

func writeSitemap(path string, urls []sitemapURL) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, u := range urls {
		b.WriteString("  <url><loc>" + template.HTMLEscapeString(u.Loc) + "</loc>")
		if u.Lastmod != "" {
			b.WriteString("<lastmod>" + u.Lastmod + "</lastmod>")
		}
		b.WriteString("</url>\n")
	}
	b.WriteString("</urlset>\n")
	write(path, []byte(b.String()))
}

const (
	markBegin = "<!-- static:begin -->"
	markEnd   = "<!-- static:end -->"
)

// fillHome puts the crawlable summary into index.html; the app replaces it on load.
func fillHome(path string, block []byte) {
	b, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	s := string(b)
	i, j := strings.Index(s, markBegin), strings.Index(s, markEnd)
	if i < 0 || j < i {
		fail(fmt.Errorf("%s has no %s ... %s markers", path, markBegin, markEnd))
	}
	s = s[:i+len(markBegin)] + "\n" + strings.TrimSpace(string(block)) + "\n" + s[j:]
	write(path, []byte(s))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "pages:", err)
	os.Exit(1)
}
