// Command slides links slide decks to sessions, for "slides after voting".
//
// Step 1, propose: read a folder of decks (exported from the Signal group or
// collected by hand) and an event's agenda, and suggest a session per file:
//
//	go run ./cmd/slides -agenda site/events/2027-04-nice/agenda.json -in ~/slides-in -out match.json
//
// It scores each file on words shared with session titles and speaker names,
// using the file name, the text of a .pptx/.docx/.key-less zip deck, and the
// first page of a PDF when pdftotext is installed. Check match.json by hand:
// fix "session", or set it to "" to leave a file out (no permission, duplicate).
//
// Step 2, apply: copy the approved files into the private slides repository
// and write its manifest, plus the public list of sessions with slides:
//
//	go run ./cmd/slides -apply match.json -agenda ... -repo ~/e2e-slides -site site
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type agenda struct {
	Event *struct {
		ID string `json:"id"`
	} `json:"event"`
	Days []struct {
		Date     string `json:"date"`
		Sessions []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Kind     string `json:"kind"`
			Speakers []struct {
				Name string `json:"name"`
			} `json:"speakers"`
		} `json:"sessions"`
	} `json:"days"`
}

type match struct {
	File         string   `json:"file"`
	Session      string   `json:"session"` // "" = leave out
	Title        string   `json:"title"`
	Score        float64  `json:"score"`
	Sure         bool     `json:"sure"`
	Alternatives []string `json:"alternatives,omitempty"`
}

var wordRe = regexp.MustCompile(`[a-z0-9]{3,}`)
var stop = map[string]bool{"the": true, "and": true, "for": true, "with": true, "your": true, "from": true, "how": true, "what": true, "part": true, "intro": true, "session": true, "slides": true, "deck": true, "final": true, "pdf": true, "pptx": true, "e2e": true, "eucdex": true, "malta": true, "nice": true, "2026": true, "2027": true, "v1": true, "v2": true}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
		if !stop[w] {
			out[w] = true
		}
	}
	return out
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

// deckText pulls readable text from the start of a deck.
func deckText(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		if _, err := exec.LookPath("pdftotext"); err == nil {
			out, _ := exec.Command("pdftotext", "-l", "2", path, "-").Output()
			return string(out)
		}
	case ".pptx", ".docx":
		zr, err := zip.OpenReader(path)
		if err != nil {
			return ""
		}
		defer zr.Close()
		var buf bytes.Buffer
		for _, f := range zr.File {
			if f.Name == "docProps/core.xml" || f.Name == "ppt/slides/slide1.xml" || f.Name == "ppt/slides/slide2.xml" || f.Name == "word/document.xml" {
				rc, err := f.Open()
				if err == nil {
					b, _ := io.ReadAll(io.LimitReader(rc, 2<<20))
					rc.Close()
					buf.WriteString(tagRe.ReplaceAllString(string(b), " "))
					buf.WriteByte(' ')
				}
			}
		}
		return buf.String()
	}
	return ""
}

func main() {
	agendaPath := flag.String("agenda", "", "the event's agenda.json")
	in := flag.String("in", "", "folder with the decks")
	out := flag.String("out", "match.json", "where to write the proposal")
	apply := flag.String("apply", "", "approved match.json to apply")
	repo := flag.String("repo", "", "checkout of the private slides repository (for -apply)")
	site := flag.String("site", "site", "site folder (for -apply)")
	flag.Parse()

	var a agenda
	b, err := os.ReadFile(*agendaPath)
	if err != nil || json.Unmarshal(b, &a) != nil || a.Event == nil {
		fail(fmt.Errorf("cannot read the agenda %q (it needs an event block)", *agendaPath))
	}
	if *apply != "" {
		doApply(a, *apply, *repo, *site)
		return
	}

	type sess struct {
		id, title string
		w         map[string]bool
		names     map[string]bool
	}
	var all []sess
	for _, d := range a.Days {
		for _, s := range d.Sessions {
			if s.Kind == "break" {
				continue
			}
			names := map[string]bool{}
			for _, sp := range s.Speakers {
				for w := range words(sp.Name) {
					names[w] = true
				}
			}
			all = append(all, sess{s.ID, s.Title, words(s.Title), names})
		}
	}

	files, _ := os.ReadDir(*in)
	var res []match
	for _, f := range files {
		if f.IsDir() || strings.HasPrefix(f.Name(), ".") {
			continue
		}
		path := filepath.Join(*in, f.Name())
		fw := words(f.Name() + " " + deckText(path))
		type cand struct {
			id, title string
			score     float64
		}
		var cs []cand
		for _, s := range all {
			hit := 0.0
			for w := range s.w {
				if fw[w] {
					hit++
				}
			}
			score := hit / float64(len(s.w)+1)
			for w := range s.names {
				if fw[w] {
					score += 0.25
				}
			}
			cs = append(cs, cand{s.id, s.title, score})
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].score > cs[j].score })
		m := match{File: f.Name()}
		if len(cs) > 0 && cs[0].score > 0 {
			m.Session, m.Title, m.Score = cs[0].id, cs[0].title, round(cs[0].score)
			m.Sure = cs[0].score >= 0.5 && (len(cs) < 2 || cs[0].score-cs[1].score >= 0.2)
			for _, c := range cs[1:min(4, len(cs))] {
				if c.score > 0 {
					m.Alternatives = append(m.Alternatives, fmt.Sprintf("%s (%.2f)", c.id, c.score))
				}
			}
		}
		res = append(res, m)
	}
	b, _ = json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		fail(err)
	}
	for _, m := range res {
		mark := "check"
		if m.Sure {
			mark = "ok"
		}
		fmt.Printf("%-5s %-45.45s -> %s\n", mark, m.File, m.Session)
	}
	fmt.Printf("%d files; review %s, then run with -apply\n", len(res), *out)
}

func doApply(a agenda, matchPath, repo, site string) {
	if repo == "" {
		fail(fmt.Errorf("-repo is required with -apply"))
	}
	var ms []match
	b, err := os.ReadFile(matchPath)
	if err != nil || json.Unmarshal(b, &ms) != nil {
		fail(fmt.Errorf("cannot read %s", matchPath))
	}
	valid := map[string]string{}
	for _, d := range a.Days {
		for _, s := range d.Sessions {
			valid[s.ID] = s.Title
		}
	}
	src := filepath.Dir(matchPath)
	ev := a.Event.ID
	manPath := filepath.Join(repo, "manifest.json")
	man := map[string]map[string]string{}
	if b, err := os.ReadFile(manPath); err == nil {
		_ = json.Unmarshal(b, &man)
	}
	for _, m := range ms {
		if m.Session == "" {
			continue
		}
		if _, ok := valid[m.Session]; !ok {
			fail(fmt.Errorf("%s: unknown session %q", m.File, m.Session))
		}
		from := m.File
		if !filepath.IsAbs(from) {
			from = filepath.Join(src, m.File)
		}
		data, err := os.ReadFile(from)
		if err != nil {
			fail(fmt.Errorf("%s: %w (keep the decks next to match.json or use absolute paths)", m.File, err))
		}
		rel := filepath.Join(ev, m.Session+strings.ToLower(filepath.Ext(m.File)))
		_ = os.MkdirAll(filepath.Join(repo, ev), 0o755)
		if err := os.WriteFile(filepath.Join(repo, rel), data, 0o644); err != nil {
			fail(err)
		}
		man[ev+"/"+m.Session] = map[string]string{"path": filepath.ToSlash(rel), "name": valid[m.Session] + strings.ToLower(filepath.Ext(m.File))}
	}
	b, _ = json.MarshalIndent(man, "", "  ")
	if err := os.WriteFile(manPath, append(b, '\n'), 0o644); err != nil {
		fail(err)
	}
	// Public list: only which sessions of this event have slides.
	ids := []string{}
	for k := range man {
		if strings.HasPrefix(k, ev+"/") {
			ids = append(ids, strings.TrimPrefix(k, ev+"/"))
		}
	}
	sort.Strings(ids)
	_ = os.MkdirAll(filepath.Join(site, "slides"), 0o755)
	b, _ = json.MarshalIndent(ids, "", "  ")
	if err := os.WriteFile(filepath.Join(site, "slides", ev+".json"), append(b, '\n'), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("%d decks for %s in %s; public list in %s\n", len(ids), ev, repo, filepath.Join(site, "slides", ev+".json"))
}

func round(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func fail(err error) {
	fmt.Fprintln(os.Stderr, "slides:", err)
	os.Exit(1)
}
