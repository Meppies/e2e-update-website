package main

import (
	"net/url"
	"regexp"
	"strings"
)

// Info holds the non-agenda parts of e2emvc.com: about text, event details,
// sponsors, the master class panel and upcoming events.
type Info struct {
	About    []string   `json:"about,omitempty"`
	Event    *Event     `json:"event,omitempty"`
	Diamond  []Sponsor  `json:"diamondSponsors,omitempty"`
	Featured []Sponsor  `json:"featuredSponsors,omitempty"`
	Panel    []Speaker  `json:"panel,omitempty"`
	Upcoming []Upcoming `json:"upcoming,omitempty"`
	Offer    *Offer     `json:"offer,omitempty"`
}

type Event struct {
	Title   string   `json:"title"`
	Details []string `json:"details,omitempty"`
	Terms   []string `json:"terms,omitempty"`
}

type Sponsor struct {
	Name  string `json:"name"`
	URL   string `json:"url,omitempty"`
	Logo  string `json:"logo,omitempty"`
	Text  string `json:"text,omitempty"`
	Links []Link `json:"links,omitempty"`
}

type Upcoming struct {
	Title string   `json:"title"`
	Lines []string `json:"lines,omitempty"`
	Image string   `json:"image,omitempty"`
}

type Offer struct {
	Title string   `json:"title"`
	Lines []string `json:"lines,omitempty"`
}

var knownSponsors = map[string]string{
	"amazon":      "AWS End User Computing",
	"aws":         "AWS End User Computing",
	"controlup":   "ControlUp",
	"parallels":   "Parallels",
	"auxilium-it": "Auxilium IT",
	"appventix":   "AppVentiX",
	"eucscore":    "EUC Score",
	"liquidware":  "Liquidware",
	"ferroque":    "Ferroque Systems",
	"igel":        "IGEL",
	"citrix":      "Citrix",
	"microsoft":   "Microsoft",
	"nerdio":      "Nerdio",
}

// sponsorName derives a readable name from a sponsor's website.
func sponsorName(href string) string {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil || u.Host == "" {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(strings.ToLower(u.Host), "www."), ".")
	if len(parts) >= 2 {
		key := parts[len(parts)-2]
		if n, ok := knownSponsors[key]; ok {
			return n
		}
		if n, ok := knownSponsors[parts[0]]; ok {
			return n
		}
		return strings.ToUpper(key[:1]) + key[1:]
	}
	return u.Host
}

func isNoise(href string) bool {
	h := strings.ToLower(href)
	for _, bad := range []string{"godaddy.com", "e2emvc.com", "paypal.com", "twitter.com", "x.com/", "linkedin.com", "privacy-policy"} {
		if strings.Contains(h, bad) {
			return true
		}
	}
	return false
}

var (
	footerRe = regexp.MustCompile(`(?i)^(copyright|unterstützt von|powered by)`)
	termsRe  = regexp.MustCompile(`(?i)^t\s*&\s*c$|^terms`)
	// "… Website: https://example.com" at the end of a sponsor blurb.
	trailingSiteRe = regexp.MustCompile(`(?i)\s*website\s*:\s*\S+\s*$`)
)

func cardTexts(c Card) []string {
	var out []string
	for _, p := range c.Paras() {
		t := p.Text()
		if t != "" && !footerRe.MatchString(t) {
			out = append(out, t)
		}
	}
	return out
}

func buildInfo(secs []Section) *Info {
	info := &Info{}
	for _, sec := range secs {
		h := strings.ToLower(sec.Heading)
		switch {
		case strings.Contains(h, "what is"):
			for _, b := range sec.Loose {
				if b.Tag == "p" {
					info.About = append(info.About, b.Text())
				}
			}
			for _, c := range sec.Cards {
				info.About = append(info.About, cardTexts(c)...)
			}

		case strings.Contains(h, "schedule"):
			for _, c := range sec.Cards {
				t := firstHeading(c, nil)
				if t == "" {
					continue
				}
				info.Upcoming = append(info.Upcoming, Upcoming{Title: t, Lines: cardTexts(c), Image: c.Logo})
			}

		case strings.Contains(h, "diamond"):
			for _, c := range sec.Cards {
				for _, l := range c.Links {
					if isNoise(l.Href) {
						continue
					}
					info.Diamond = append(info.Diamond, Sponsor{Name: sponsorName(l.Href), URL: strings.TrimSpace(l.Href), Logo: c.Logo})
					break
				}
				// The registration offer for the next event sits in the same block.
				if lines := cardTexts(c); len(lines) > 0 {
					if info.Offer == nil {
						info.Offer = &Offer{}
					}
					info.Offer.Lines = append(info.Offer.Lines, lines...)
				}
			}
			for _, b := range sec.Loose {
				if info.Offer == nil {
					info.Offer = &Offer{}
				}
				t := b.Text()
				if b.Tag == "h" && strings.Contains(strings.ToLower(t), "register") {
					info.Offer.Title = t
				} else if b.Tag == "p" && !footerRe.MatchString(t) {
					info.Offer.Lines = append(info.Offer.Lines, t)
				}
			}
			if info.Offer != nil && info.Offer.Title == "" && len(info.Offer.Lines) == 0 {
				info.Offer = nil
			}

		case strings.Contains(h, "featured sponsors"):
			for _, c := range sec.Cards {
				var site Link
				var other []Link
				for _, l := range c.Links {
					if strings.Contains(l.Href, "godaddy.com") || strings.Contains(l.Href, "e2emvc.com") {
						continue
					}
					if site.Href == "" && !isNoise(l.Href) {
						site = l
						continue
					}
					other = append(other, l)
				}
				var text []string
				for _, t := range cardTexts(c) {
					lt := strings.ToLower(t)
					if strings.HasPrefix(lt, "website") || strings.HasPrefix(lt, "twitter") || strings.HasPrefix(lt, "linkedin") {
						continue
					}
					t = trailingSiteRe.ReplaceAllString(t, "")
					text = append(text, strings.Trim(t, "“”\" "))
				}
				if site.Href == "" && len(text) == 0 {
					continue
				}
				info.Featured = append(info.Featured, Sponsor{
					Name: sponsorName(site.Href), URL: site.Href, Logo: c.Logo,
					Text: joinParas(text), Links: other,
				})
			}

		case strings.Contains(h, "panel of experts"):
			seen := map[string]bool{}
			for _, c := range sec.Cards {
				head := firstHeading(c, nil)
				name := nameCase(nameJunk.ReplaceAllString(head, ""))
				bio := joinParas(cardTexts(c))
				if name == "" || bio == "" || seen[strings.ToLower(name)] {
					continue
				}
				first := strings.Fields(name)[0]
				lead := strings.Fields(bio + " x")[0]
				if !(strings.HasPrefix(first, lead) || strings.HasPrefix(lead, first)) {
					continue
				}
				seen[strings.ToLower(name)] = true
				sp := Speaker{Name: name, Bio: bio, Photo: c.Image}
				if i := strings.Index(head, " - "); i > 0 {
					sp.Role = clean(head[i+3:])
				}
				info.Panel = append(info.Panel, sp)
			}

		case dayOf(sec.Heading) == 0 && regexp.MustCompile(`(?i)october\s+\d+\s*-\s*\d+`).MatchString(sec.Heading):
			ev := &Event{Title: sec.Heading}
			var all []string
			for _, b := range sec.Loose {
				if b.Tag == "p" {
					all = append(all, b.Text())
				}
			}
			for _, c := range sec.Cards {
				all = append(all, cardTexts(c)...)
			}
			inTerms := false
			for _, t := range all {
				if termsRe.MatchString(t) {
					inTerms = true
					continue
				}
				if inTerms {
					ev.Terms = append(ev.Terms, t)
				} else {
					ev.Details = append(ev.Details, t)
				}
			}
			info.Event = ev
		}
	}
	return info
}
