package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type Speaker struct {
	Name  string `json:"name"`
	Role  string `json:"role,omitempty"`
	Bio   string `json:"bio,omitempty"`
	Photo string `json:"photo,omitempty"`
}

type Change struct {
	Kind string   `json:"kind"` // "new" or "updated"
	At   string   `json:"at"`
	What []string `json:"what,omitempty"`
}

type Session struct {
	ID          string    `json:"id"`
	Start       string    `json:"start,omitempty"`
	End         string    `json:"end,omitempty"`
	Room        string    `json:"room,omitempty"`
	Kind        string    `json:"kind"` // talk, break, panel
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Speakers    []Speaker `json:"speakers,omitempty"`
	Photo       string    `json:"photo,omitempty"` // group photo when one picture covers several speakers
	Changed     *Change   `json:"changed,omitempty"`
}

type Day struct {
	Date     string    `json:"date"`
	Title    string    `json:"title"`
	Updating bool      `json:"updating,omitempty"`
	Rooms    []string  `json:"rooms,omitempty"`
	Sessions []Session `json:"sessions"`
}

var (
	timeRe    = regexp.MustCompile(`(\d{1,2})[:.](\d{2})\s*[-–—]\s*(\d{1,2})[:.](\d{2})`)
	roomRe    = regexp.MustCompile(`(?i)(\d{1,2})[:.](\d{2})\s*[-–—]\s*(\d{1,2})[:.](\d{2})\s*,?\s*room\s*(\d+)\s*,?`)
	breakRe   = regexp.MustCompile(`(?i)\b(lunch|coffee|break|dinner|drinks)\b`)
	dayRe     = regexp.MustCompile(`(?i)october\s+(\d{1,2})\s*(?:,|$)`)
	quotedRe  = regexp.MustCompile(`^(.+?)\s*["“”]\s*(.+?)\s*["“”]\s*$`)
	nameJunk  = regexp.MustCompile(`\s+-\s+.*$`)
	stopNames = map[string]bool{"linkedin": true, "twitter": true, "x": true, "blog": true, "github": true, "socials": true, "website": true, "links": true}
)

func hhmm(h, m string) string {
	n, _ := strconv.Atoi(h)
	return fmt.Sprintf("%02d:%s", n, m)
}

// findTime returns the first start/end pair in a card, ignoring break headings.
func findTime(c Card) (string, string, bool) {
	for _, b := range c.Blocks {
		t := b.Text()
		if breakRe.MatchString(t) {
			continue
		}
		if m := timeRe.FindStringSubmatch(t); m != nil {
			return hhmm(m[1], m[2]), hhmm(m[3], m[4]), true
		}
	}
	return "", "", false
}

func isTimeOnly(s string) bool {
	return strings.TrimSpace(timeRe.ReplaceAllString(s, "")) == ""
}

func firstHeading(c Card, skip func(string) bool) string {
	for _, h := range c.Headings() {
		if skip != nil && skip(h) {
			continue
		}
		return h
	}
	return ""
}

func parasText(c Card) []string {
	var out []string
	for _, p := range c.Paras() {
		t := p.Text()
		if isTimeOnly(t) || breakRe.MatchString(t) && timeRe.MatchString(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func splitNames(s string) []Speaker {
	s = nameJunk.ReplaceAllString(clean(s), "")
	var out []Speaker
	for _, p := range regexp.MustCompile(`\s*(?:&|\band\b|,)\s*`).Split(s, -1) {
		if p = nameCase(p); p != "" {
			out = append(out, Speaker{Name: p})
		}
	}
	return out
}

// dayOf maps a section heading to an October date, or 0 if it is not a day.
func dayOf(heading string) int {
	h := strings.ToLower(heading)
	if strings.Contains(h, "panel of experts") {
		return 0
	}
	m := dayRe.FindStringSubmatch(heading)
	if m == nil {
		return 0
	}
	d, _ := strconv.Atoi(m[1])
	if d < 14 || d > 19 {
		return 0
	}
	return d
}

func dayTitle(heading string) string {
	if m := regexp.MustCompile(`["“”]([^"“”]+)["“”]\s*master class`).FindStringSubmatch(strings.ToLower(heading)); m != nil {
		q := regexp.MustCompile(`["“”]([^"“”]+)["“”]`).FindStringSubmatch(heading)
		return clean(q[1]) + " master class"
	}
	if i := strings.LastIndex(heading, " - "); i > 0 && !timeRe.MatchString(heading[i:]) {
		return clean(heading[i+3:])
	}
	return "Main conference"
}

// parseDay picks a parser based on the shape of the section.
func parseDay(sec Section) []Session {
	var cards []Card
	for _, c := range sec.Cards {
		if len(c.Blocks) > 0 {
			cards = append(cards, c)
		}
	}
	hasRoom, hasTime := false, false
	for _, c := range cards {
		for _, b := range c.Blocks {
			t := b.Text()
			if roomRe.MatchString(t) {
				hasRoom = true
			}
			if timeRe.MatchString(t) {
				hasTime = true
			}
		}
	}
	switch {
	case hasRoom:
		return parseRooms(cards)
	case hasTime:
		return parseGroups(cards)
	default:
		return parsePanel(cards)
	}
}

// parseGroups handles single-track days. Each time slot is a run of cards
// that starts with a card holding a time: slot, talk, speaker.
func parseGroups(cards []Card) []Session {
	var groups [][]Card
	var sessions []Session
	seenBreak := map[string]bool{}
	for _, c := range cards {
		for _, h := range c.Headings() {
			if breakRe.MatchString(h) {
				if m := timeRe.FindStringSubmatch(h); m != nil && !seenBreak[m[0]] {
					seenBreak[m[0]] = true
					sessions = append(sessions, Session{Kind: "break", Start: hhmm(m[1], m[2]), End: hhmm(m[3], m[4]),
						Title: sentenceCase(strings.TrimSpace(timeRe.ReplaceAllString(h, "")))})
				}
			}
		}
		if _, _, ok := findTime(c); ok {
			groups = append(groups, []Card{c})
		} else if len(groups) > 0 {
			groups[len(groups)-1] = append(groups[len(groups)-1], c)
		}
	}
	notTitle := func(h string) bool { return isTimeOnly(h) || breakRe.MatchString(h) && timeRe.MatchString(h) }
	for _, g := range groups {
		s := Session{Kind: "talk"}
		s.Start, s.End, _ = findTime(g[0])
		var desc []string
		var spk *Card
		switch {
		case len(g) >= 2 && firstHeading(g[1], notTitle) == "":
			// Master class style: slot, `Speaker " Title "`, description.
			for _, c := range g {
				desc = append(desc, parasText(c)...)
			}
		case len(g) >= 3:
			s.Title = firstHeading(g[1], notTitle)
			desc = append(parasText(g[0]), parasText(g[1])...)
			for _, c := range g[2 : len(g)-1] {
				desc = append(desc, parasText(c)...)
			}
			spk = &g[len(g)-1]
		case len(g) == 2:
			s.Title = firstHeading(g[0], notTitle)
			desc = parasText(g[0])
			spk = &g[1]
		default:
			s.Title = firstHeading(g[0], notTitle)
			desc = parasText(g[0])
		}
		if s.Title == "" && len(desc) > 0 {
			first := desc[0]
			desc = desc[1:]
			if m := quotedRe.FindStringSubmatch(first); m != nil {
				s.Speakers = splitNames(m[1])
				s.Title = clean(m[2])
			} else if i := strings.Index(first, " . "); i > 0 {
				s.Title = clean(first[:i])
				desc = append([]string{first[i+3:]}, desc...)
			} else {
				s.Title = first
			}
		}
		if spk != nil {
			s.Speakers = splitNames(firstHeading(*spk, notTitle))
			if len(s.Speakers) > 0 {
				assignBios(s.Speakers, spk.Paras())
				if len(s.Speakers) == 1 {
					s.Speakers[0].Photo = spk.Image
				} else {
					s.Photo = spk.Logo // full picture, not cropped: it holds one person per half
				}
			}
		}
		s.Title = sentenceCase(s.Title)
		s.Description = joinParas(desc)
		sessions = append(sessions, s)
	}
	sortSessions(sessions)
	return sessions
}

func looksLikeName(s string) bool {
	s = strings.Trim(clean(s), ",:")
	if s == "" || len(s) > 40 || stopNames[strings.ToLower(s)] {
		return false
	}
	words := strings.Fields(s)
	if len(words) > 4 {
		return false
	}
	for _, w := range words {
		r := []rune(w)
		if !unicode.IsUpper(r[0]) {
			return false
		}
		for _, c := range r {
			if unicode.IsDigit(c) {
				return false
			}
		}
	}
	return true
}

var bioLeadRe = regexp.MustCompile(`^([A-ZÀ-Ý][\p{L}'-]+)\s+(is|has|was)\b`)

// parseRooms handles multi-room days where each card is one session and the
// first bold run reads "10:00 - 10:30, ROOM 1,".
func parseRooms(cards []Card) []Session {
	var sessions []Session
	for _, c := range cards {
		s := Session{Kind: "talk", Title: sentenceCase(firstHeading(c, nil))}
		found := false
		var desc []string
		var cur *strings.Builder
		descB := &strings.Builder{}
		flush := func() {
			if cur == nil && descB.Len() > 0 {
				desc = append(desc, descB.String())
				descB.Reset()
			}
		}
		for _, p := range c.Paras() {
			runs := append([]Run(nil), p.Runs...)
			if !found {
				for i, r := range runs {
					if m := roomRe.FindStringSubmatchIndex(r.Text); m != nil {
						t := r.Text
						s.Start = hhmm(t[m[2]:m[3]], t[m[4]:m[5]])
						s.End = hhmm(t[m[6]:m[7]], t[m[8]:m[9]])
						s.Room = "Room " + t[m[10]:m[11]]
						runs[i].Text = t[:m[0]] + t[m[1]:]
						found = true
						break
					}
				}
			}
			atStart := true
			for i := 0; i < len(runs); i++ {
				r := runs[i]
				if strings.TrimSpace(r.Text) == "" {
					if cur != nil {
						cur.WriteString(r.Text)
					} else {
						descB.WriteString(r.Text)
					}
					continue
				}
				if r.Bold && looksLikeName(r.Text) {
					j, name := i, ""
					for j < len(runs) && (runs[j].Bold || strings.TrimSpace(runs[j].Text) == "") {
						name += runs[j].Text
						j++
					}
					rest := ""
					if j < len(runs) {
						rest = strings.TrimSpace(runs[j].Text)
					}
					name = clean(name)
					if looksLikeName(name) && (atStart || strings.HasSuffix(name, ",") || strings.HasPrefix(rest, ",")) {
						flush()
						if cur == nil && descB.Len() > 0 {
							desc = append(desc, descB.String())
							descB.Reset()
						}
						s.Speakers = append(s.Speakers, Speaker{Name: nameCase(strings.Trim(name, ", "))})
						cur = &strings.Builder{}
						if j < len(runs) {
							runs[j].Text = strings.TrimLeft(strings.TrimSpace(runs[j].Text), ", ")
						}
						i = j - 1
						atStart = false
						continue
					}
				}
				atStart = false
				if cur != nil {
					cur.WriteString(r.Text)
				} else {
					descB.WriteString(r.Text)
				}
			}
			if cur != nil {
				sp := &s.Speakers[len(s.Speakers)-1]
				sp.Bio = joinParas([]string{sp.Bio, cur.String()})
				cur.Reset()
			} else {
				flush()
			}
		}
		if !found {
			continue
		}
		if len(s.Speakers) == 1 {
			s.Speakers[0].Photo = c.Image
		} else if len(s.Speakers) > 1 {
			s.Photo = c.Logo // full picture, not cropped: it holds one person per half
		} else {
			s.Photo = c.Image // a portrait without a name; matched to a speaker later if possible
		}
		for i := range s.Speakers {
			sp := &s.Speakers[i]
			if m := bioLeadRe.FindStringSubmatch(sp.Bio); m != nil && !strings.HasPrefix(strings.Fields(sp.Name)[0], m[1]) {
				sp.Name += " " + m[1]
				sp.Bio = strings.TrimSpace(sp.Bio[len(m[1]):])
			}
		}
		// Drop description paragraphs that repeat a speaker bio.
		var kept []string
		for _, d := range desc {
			d = clean(d)
			probe := d
			if len(probe) > 60 {
				probe = probe[:60]
			}
			dup := false
			for _, sp := range s.Speakers {
				if strings.Contains(clean(sp.Bio), probe) {
					dup = true
				}
			}
			if !dup {
				kept = append(kept, d)
			}
		}
		s.Description = joinParas(kept)
		sessions = append(sessions, s)
	}
	sortSessions(sessions)
	return sessions
}

// parsePanel handles a day without times: one panel with its panellists.
func parsePanel(cards []Card) []Session {
	s := Session{Kind: "panel"}
	count := map[string]int{}
	var desc []string
	for _, c := range cards {
		ps := c.Paras()
		long := false
		for _, p := range ps {
			if len(p.Text()) > 80 {
				long = true
			}
		}
		if long {
			name := firstHeading(c, nil)
			sp := Speaker{Name: nameCase(nameJunk.ReplaceAllString(name, ""))}
			if i := strings.Index(name, " - "); i > 0 {
				sp.Role = clean(strings.SplitN(name[i+3:], " and ", 2)[0])
			}
			var parts []string
			for _, p := range ps {
				parts = append(parts, p.Text())
			}
			sp.Bio = joinParas(parts)
			sp.Photo = c.Image
			s.Speakers = append(s.Speakers, sp)
			continue
		}
		for _, h := range c.Headings() {
			if strings.EqualFold(h, "time") {
				continue
			}
			count[h]++
		}
		for _, p := range ps {
			desc = append(desc, p.Text())
		}
	}
	best := 0
	for h, n := range count {
		if n > best || n == best && h < s.Title {
			best, s.Title = n, h
		}
	}
	if s.Title == "" {
		s.Title = "Panel"
	}
	s.Title = sentenceCase(s.Title)
	s.Description = joinParas(desc)
	return []Session{s}
}

func sortSessions(ss []Session) {
	sort.SliceStable(ss, func(i, j int) bool {
		if ss[i].Start != ss[j].Start {
			return ss[i].Start < ss[j].Start
		}
		return ss[i].Room < ss[j].Room
	})
}

// assignBios splits a speaker card over the speakers named in its heading.
// A paragraph that opens with a speaker's name in bold, or whose first word is
// a speaker's first name, starts that speaker's part; following paragraphs stay
// with them until the next speaker starts. Separator lines such as "|" are dropped.
func assignBios(sp []Speaker, paras []Block) {
	if len(sp) == 0 {
		return
	}
	firstName := func(n string) string {
		f := strings.Fields(strings.ToLower(n))
		if len(f) == 0 {
			return ""
		}
		return strings.Trim(f[0], ",.:;|")
	}
	parts := make([][]string, len(sp))
	cur := 0
	for _, p := range paras {
		t := p.Text()
		if strings.Trim(t, "|·-–— ") == "" || isTimeOnly(t) || (breakRe.MatchString(t) && timeRe.MatchString(t)) {
			continue
		}
		matched := -1
		lead := ""
		for _, r := range p.Runs {
			if strings.TrimSpace(r.Text) == "" {
				continue
			}
			if r.Bold {
				lead = clean(r.Text)
			}
			break
		}
		if lead != "" {
			ln := strings.ToLower(strings.Trim(lead, ", "))
			for i, s := range sp {
				n := strings.ToLower(s.Name)
				if n == ln || firstName(n) == ln || (len(ln) > 2 && strings.HasPrefix(n, ln)) {
					matched = i
					break
				}
			}
			if matched >= 0 {
				t = strings.TrimLeft(strings.TrimSpace(strings.TrimPrefix(t, lead)), ", ")
			}
		}
		if matched < 0 {
			w := firstName(t)
			for i, s := range sp {
				if len(parts[i]) == 0 && firstName(s.Name) == w {
					matched = i
					break
				}
			}
		}
		if matched >= 0 {
			cur = matched
		}
		if t != "" {
			parts[cur] = append(parts[cur], t)
		}
	}
	for i := range sp {
		sp[i].Bio = joinParas(parts[i])
	}
}
