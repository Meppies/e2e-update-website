package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EventMeta says which conference the agenda belongs to. The site uses the
// dates to decide what to show: the agenda from a week before the start until
// the last day, and only the general information before and after that.
type EventMeta struct {
	ID    string `json:"id"`    // "2026-10-malta"
	Name  string `json:"name"`  // "E2E EUCDEX.Ai Malta 2026"
	Place string `json:"place"` // "Malta"
	Start string `json:"start"` // first day, master class included
	End   string `json:"end"`   // last day
}

const monthNames = `january|february|march|april|may|june|july|august|september|october|november|december`

var (
	months  = strings.Split(monthNames, "|")
	rangeA  = regexp.MustCompile(`(?i)\b(` + monthNames + `)\s+(\d{1,2})\s*[-–]\s*(\d{1,2}),?\s*(\d{4})`)
	rangeB  = regexp.MustCompile(`(?i)\b(\d{1,2})\s*[-–]\s*(\d{1,2})\s+(` + monthNames + `),?\s*(\d{4})`)
	dayA    = regexp.MustCompile(`(?i)\b(` + monthNames + `)\s+(\d{1,2})(?:st|nd|rd|th)?\b(\s*[-–]\s*\d)?(?:,?\s*(\d{4}))?`)
	dayB    = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)?\s+(` + monthNames + `)\b(?:,?\s*(\d{4}))?`)
	placeRe = regexp.MustCompile(`(?i)\(e2e\)|\be2e\b|eucdex\.ai|eucdex|conference|agenda`)
)

func monthNum(s string) int {
	s = strings.ToLower(s)
	for i, m := range months {
		if m == s {
			return i + 1
		}
	}
	return 0
}

func ymd(y, m, d int) (time.Time, bool) {
	if m < 1 || d < 1 || d > 31 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t, t.Day() == d
}

// eventRange reads "MALTA, October 16 - 18, 2026" or "9 - 11 April 2027".
func eventRange(s string) (start, end time.Time, ok bool) {
	if m := rangeA.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[4])
		a, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		start, ok1 := ymd(y, monthNum(m[1]), a)
		end, ok2 := ymd(y, monthNum(m[1]), b)
		return start, end, ok1 && ok2 && !end.Before(start)
	}
	if m := rangeB.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[4])
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		start, ok1 := ymd(y, monthNum(m[3]), a)
		end, ok2 := ymd(y, monthNum(m[3]), b)
		return start, end, ok1 && ok2 && !end.Before(start)
	}
	return time.Time{}, time.Time{}, false
}

// dateOf reads a single date from a day heading ("October 16, FRIDAY",
// "SUNDAY October 18, 2026", "Friday 9 April"). Date ranges are not days.
// Without a year in the heading the event year is used.
func dateOf(heading string, year int) (time.Time, bool) {
	if strings.Contains(strings.ToLower(heading), "panel of experts") {
		return time.Time{}, false
	}
	if m := dayA.FindStringSubmatch(heading); m != nil && m[3] == "" {
		d, _ := strconv.Atoi(m[2])
		y := year
		if m[4] != "" {
			y, _ = strconv.Atoi(m[4])
		}
		return ymd(y, monthNum(m[1]), d)
	}
	if loc := dayB.FindStringSubmatchIndex(heading); loc != nil {
		before := strings.TrimRight(heading[:loc[0]], " ")
		if strings.HasSuffix(before, "-") || strings.HasSuffix(before, "–") {
			return time.Time{}, false
		}
		m := dayB.FindStringSubmatch(heading)
		d, _ := strconv.Atoi(m[1])
		y := year
		if m[3] != "" {
			y, _ = strconv.Atoi(m[3])
		}
		return ymd(y, monthNum(m[2]), d)
	}
	return time.Time{}, false
}

// eventHeading finds the section that announces the conference with its dates.
func eventHeading(secs []Section) (string, time.Time, time.Time, bool) {
	for _, sec := range secs {
		if strings.Contains(strings.ToLower(sec.Heading), "schedule") {
			continue
		}
		if s, e, ok := eventRange(sec.Heading); ok {
			return sec.Heading, s, e, true
		}
	}
	return "", time.Time{}, time.Time{}, false
}

// placeOf turns "(E2E) EUCDEX.Ai MALTA, October 16 - 18, 2026" into "Malta".
func placeOf(title string) string {
	head := title
	if i := strings.Index(head, ","); i > 0 {
		head = head[:i]
	}
	head = rangeA.ReplaceAllString(head, "")
	head = clean(placeRe.ReplaceAllString(head, " "))
	if head == "" {
		return ""
	}
	return nameCase(strings.ToUpper(head))
}

// eventMeta describes the event from its heading and the days found.
func eventMeta(info *Info, days []Day) *EventMeta {
	var start, end, title string
	if info != nil && info.Event != nil {
		title = info.Event.Title
		if s, e, ok := eventRange(title); ok {
			start, end = s.Format("2006-01-02"), e.Format("2006-01-02")
		}
	}
	for _, d := range days {
		if len(d.Sessions) == 0 {
			continue
		}
		if start == "" || d.Date < start {
			start = d.Date
		}
		if end == "" || d.Date > end {
			end = d.Date
		}
	}
	if start == "" {
		return nil
	}
	brand := "E2E EUCDEX.Ai"
	if info != nil && info.Brand != "" {
		brand = info.Brand
	}
	place := placeOf(title)
	if place == "" {
		place = "Event"
	}
	return &EventMeta{
		ID:    start[:7] + "-" + slug(place),
		Name:  fmt.Sprintf("%s %s %s", brand, place, start[:4]),
		Place: place,
		Start: start,
		End:   end,
	}
}

func amsterdamToday() string {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		loc = time.FixedZone("CET", 3600)
	}
	return time.Now().In(loc).Format("2006-01-02")
}

// archive freezes the previous agenda in events/<id>/agenda.json once the day
// after its last day has begun, and lists it in events/index.json. An archive
// is written once and never changed afterwards.
func archive(prevPath, dir, today string) (bool, error) {
	if dir == "" {
		return false, nil
	}
	b, err := os.ReadFile(prevPath)
	if err != nil {
		return false, nil
	}
	var a Agenda
	if err := json.Unmarshal(b, &a); err != nil || len(a.Days) == 0 {
		return false, nil
	}
	if a.Event == nil {
		a.Event = eventMeta(a.Info, a.Days)
	}
	ev := a.Event
	if ev == nil || today <= ev.End {
		return false, nil
	}
	path := filepath.Join(dir, ev.ID, "agenda.json")
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	a.Removed = nil
	a.ArchivedAt = time.Now().UTC().Format(time.RFC3339)
	for di := range a.Days {
		for si := range a.Days[di].Sessions {
			a.Days[di].Sessions[si].Changed = nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	out, _ := json.MarshalIndent(a, "", "  ")
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return false, err
	}

	type entry struct {
		EventMeta
		Sessions int `json:"sessions"`
	}
	var list []entry
	idx := filepath.Join(dir, "index.json")
	if b, err := os.ReadFile(idx); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	n := 0
	for _, d := range a.Days {
		for _, s := range d.Sessions {
			if s.Kind != "break" {
				n++
			}
		}
	}
	kept := list[:0]
	for _, e := range list {
		if e.ID != ev.ID {
			kept = append(kept, e)
		}
	}
	list = append(kept, entry{*ev, n})
	sort.Slice(list, func(i, j int) bool { return list[i].Start > list[j].Start })
	out, _ = json.MarshalIndent(list, "", "  ")
	if err := os.WriteFile(idx, append(out, '\n'), 0o644); err != nil {
		return false, err
	}
	fmt.Printf("archived %s (%s to %s, %d sessions)\n", ev.ID, ev.Start, ev.End, n)
	return true, nil
}
