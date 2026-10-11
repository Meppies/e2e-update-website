package main

import (
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Run is a stretch of text inside a paragraph, bold or not.
type Run struct {
	Text string
	Bold bool
}

// Block is a heading ("h") or a paragraph ("p") inside a content card.
type Block struct {
	Tag  string
	Runs []Run
}

func (b Block) Text() string {
	var sb strings.Builder
	for _, r := range b.Runs {
		sb.WriteString(r.Text)
	}
	return clean(sb.String())
}

// Card is one GoDaddy "ContentCard".
type Card struct {
	Blocks []Block
	Image  string // first real image in the card, usually the speaker photo
	Logo   string // the same image, resized without cropping (for logos)
	Links  []Link
}

// Link is an anchor found inside a card.
type Link struct {
	Href string `json:"href"`
	Text string `json:"text,omitempty"`
}

func (c *Card) addLink(n *html.Node) {
	href := strings.TrimSpace(attr(n, "href"))
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") {
		return
	}
	if strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
		href = "https://e2emvc.com" + href
	}
	for _, l := range c.Links {
		if l.Href == href {
			return
		}
	}
	c.Links = append(c.Links, Link{Href: href, Text: textOf(n)})
}

func collectLinks(c *Card, n *html.Node) {
	if n.Type == html.ElementNode && n.Data == "a" {
		c.addLink(n)
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		collectLinks(c, ch)
	}
}

// logoURL is like photoURL but keeps the aspect ratio.
func logoURL(raw string) string {
	p := photoURL(raw)
	if p == "" {
		return ""
	}
	return strings.TrimSuffix(p, "/:/rs=w:240,h:240,cg:true,m") + "/:/rs=w:480,cg:true"
}

// photoURL turns a GoDaddy lazy-image reference into a small square image URL.
func photoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "data:") || strings.Contains(raw, "transparent_placeholder") {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	if !strings.HasPrefix(raw, "https://") {
		return ""
	}
	if i := strings.Index(raw, "/:/"); i > 0 {
		raw = raw[:i]
	}
	return raw + "/:/rs=w:240,h:240,cg:true,m"
}

func (c Card) Headings() []string {
	var out []string
	for _, b := range c.Blocks {
		if b.Tag == "h" {
			out = append(out, b.Text())
		}
	}
	return out
}

func (c Card) Paras() []Block {
	var out []Block
	for _, b := range c.Blocks {
		if b.Tag == "p" {
			out = append(out, b)
		}
	}
	return out
}

// Section is everything under one "SectionHeading".
type Section struct {
	Heading string
	Loose   []Block // headings and paragraphs outside any card
	Cards   []Card
}

var wsRe = regexp.MustCompile(`\s+`)

func clean(s string) string {
	s = strings.ReplaceAll(s, " ", " ")
	s = strings.ReplaceAll(s, "​", "")
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.Data == "br" {
			sb.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return clean(sb.String())
}

func runsOf(n *html.Node) []Run {
	var runs []Run
	add := func(t string, bold bool) {
		if t == "" {
			return
		}
		if len(runs) > 0 && runs[len(runs)-1].Bold == bold {
			runs[len(runs)-1].Text += t
			return
		}
		runs = append(runs, Run{Text: t, Bold: bold})
	}
	var f func(*html.Node, bool)
	f = func(n *html.Node, bold bool) {
		switch n.Type {
		case html.TextNode:
			add(n.Data, bold)
			return
		case html.ElementNode:
			switch n.Data {
			case "br":
				add(" ", bold)
				return
			case "strong", "b":
				bold = true
			case "script", "style":
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c, bold)
		}
	}
	f(n, false)
	for i := range runs {
		runs[i].Text = strings.ReplaceAll(runs[i].Text, " ", " ")
		runs[i].Text = wsRe.ReplaceAllString(runs[i].Text, " ")
	}
	return runs
}

// parseSections walks the page in document order and groups content cards
// under the section heading that precedes them.
func parseSections(r io.Reader) ([]Section, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var secs []Section
	card := -1
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript":
				return
			}
			ux := attr(n, "data-ux")
			if ux == "SectionHeading" {
				secs = append(secs, Section{Heading: textOf(n)})
				card = -1
				return
			}
			if ux == "ContentCard" && len(secs) > 0 {
				s := &secs[len(secs)-1]
				s.Cards = append(s.Cards, Card{})
				prev := card
				card = len(s.Cards) - 1
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				card = prev
				return
			}
			if card >= 0 && len(secs) > 0 && n.Data == "img" {
				c := &secs[len(secs)-1].Cards[card]
				if c.Image == "" {
					src := attr(n, "data-srclazy")
					if src == "" {
						src = attr(n, "src")
					}
					c.Image = photoURL(src)
					c.Logo = logoURL(src)
				}
				return
			}
			if card >= 0 && len(secs) > 0 && n.Data == "a" {
				secs[len(secs)-1].Cards[card].addLink(n)
			}
			if len(secs) > 0 {
				tag := ""
				switch n.Data {
				case "h1", "h2", "h3", "h4", "h5", "h6":
					tag = "h"
				case "p", "li":
					tag = "p"
				}
				if tag != "" {
					b := Block{Tag: tag, Runs: runsOf(n)}
					s := &secs[len(secs)-1]
					if b.Text() != "" {
						if card >= 0 {
							s.Cards[card].Blocks = append(s.Cards[card].Blocks, b)
						} else {
							s.Loose = append(s.Loose, b)
						}
					}
					if card >= 0 {
						collectLinks(&s.Cards[card], n)
					}
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return secs, nil
}

// isAllCaps reports whether every letter in s is upper case.
func isAllCaps(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if !unicode.IsUpper(r) {
				return false
			}
		}
	}
	return letters > 1
}

// nameCase turns "ALEX COOPER" into "Alex Cooper" and leaves mixed case alone.
func nameCase(s string) string {
	s = clean(s)
	if !isAllCaps(s) {
		return s
	}
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		rs := []rune(w)
		rs[0] = unicode.ToUpper(rs[0])
		for j := 1; j < len(rs); j++ {
			if rs[j-1] == '\'' || rs[j-1] == '-' {
				rs[j] = unicode.ToUpper(rs[j])
			}
		}
		words[i] = string(rs)
	}
	return strings.Join(words, " ")
}

// sentenceCase turns "WELCOME SESSION" into "Welcome session".
func sentenceCase(s string) string {
	s = clean(s)
	if !isAllCaps(s) {
		return s
	}
	rs := []rune(strings.ToLower(s))
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

// joinParas glues paragraphs back together. The source often breaks one
// sentence over several paragraphs; those are joined with a space.
func joinParas(paras []string) string {
	var out []string
	for _, p := range paras {
		p = clean(p)
		if p == "" {
			continue
		}
		if n := len(out); n > 0 {
			last, _ := utf8.DecodeLastRuneInString(out[n-1])
			if !strings.ContainsRune(".!?:)\"'”’", last) && last < 0x1F000 {
				out[n-1] += " " + p
				continue
			}
		}
		out = append(out, p)
	}
	return strings.Join(out, "\n\n")
}
