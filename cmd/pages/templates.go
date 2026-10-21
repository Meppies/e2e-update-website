package main

import (
	"fmt"
	"html/template"
)

var funcs = template.FuncMap{
	"headFor": func(p evPage) head {
		return head{Title: p.Name + " agenda: sessions and speakers (unofficial)",
			Desc: p.Name + ", " + p.Range + " in " + p.Place + ": the full programme with " + itoa(p.Sessions) + " sessions and " + itoa(len(p.Speakers)) + " speakers, readable on your phone.",
			URL:  p.URL, Brand: p.Brand, LD: p.JSONLD}
	},
	"headForDay": func(x struct {
		E evPage
		D dayPage
	}) head {
		return head{Title: x.D.Long + ": " + x.E.Name + " agenda (unofficial)",
			Desc: "All sessions on " + x.D.Long + " at " + x.E.Name + " (" + x.D.Title + "), with abstracts and speakers.",
			URL:  x.D.URL, Brand: x.E.Brand}
	},
	"headForList": func(x homeData) head {
		return head{Title: "Events: " + x.Brand + " agendas (unofficial)",
			Desc: "Agendas, sessions and speakers of the " + x.Brand + " end-user computing conferences, readable on your phone.",
			URL:  siteURL + "/events/", Brand: x.Brand}
	},
}

func itoa(i int) string { return fmt.Sprint(i) }

const layout = `{{define "head"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>{{.Title}}</title>
<meta name="description" content="{{.Desc}}">
<link rel="canonical" href="{{.URL}}">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Desc}}">
<meta property="og:url" content="{{.URL}}">
<meta property="og:type" content="website">
<meta property="og:site_name" content="{{.Brand}} unofficial">
<meta property="og:image" content="https://e2emvc.meppie.eu/share.png">
<meta property="og:image:width" content="1200">
<meta property="og:image:height" content="630">
<meta name="twitter:card" content="summary_large_image">
<meta name="theme-color" content="#161616">
<link rel="stylesheet" href="/pages.css">
{{if .LD}}<script type="application/ld+json">{{.LD}}</script>{{end}}
</head>
<body>
<header class="top"><div class="wrap">
  <a class="home" href="/"><img src="/e2e-logo.png" alt="" width="400" height="235"><span><b>{{.Brand}}</b> unofficial</span></a>
  <nav><a href="/">Home</a><a href="/events/">Events</a><a href="https://e2emvc.com/" rel="noopener">Official site ↗</a></nav>
</div></header>
<main class="wrap">
{{end}}
{{define "foot"}}</main>
<footer><div class="wrap">
  <p>Unofficial copy of the agenda on <a href="https://e2emvc.com/" rel="noopener">e2emvc.com</a>, which is leading. Built by <a href="https://meppie.eu/" rel="noopener">Jeroen Meppelink</a>. No cookies, no tracking.</p>
</div></footer>
</body>
</html>
{{end}}`

type head struct {
	Title, Desc, URL, Brand string
	LD                      template.JS
}

var eventTpl = template.Must(template.New("event").Funcs(funcs).Parse(layout + `{{template "head" (headFor .)}}
<p class="crumbs"><a href="/events/">Events</a> › {{.Name}}</p>
<h1>{{.Name}} <span class="sub">agenda, sessions and speakers</span></h1>
<p class="lead">{{.Range}} · {{if .Venue}}{{.Venue}}, {{end}}{{.Place}} · {{.Sessions}} sessions · {{len .Speakers}} speakers</p>
<p><a class="btn" href="{{if .Past}}/#past/{{.ID}}{{else}}/#info{{end}}">Open the interactive agenda →</a></p>
{{with .Recap}}<section class="recap"><h2>{{.Title}}</h2>
<ul class="stats">{{range .Stats}}<li><b>{{index . 0}}</b> {{index . 1}}</li>{{end}}</ul>
{{end}}{{if .Recap}}{{range .RecapHTML}}<p>{{.}}</p>{{end}}</section>{{end}}
<section><h2>Programme</h2>
{{range $d := .Days}}<h3><a href="{{$d.URL}}">{{$d.Long}}</a> <span class="sub">{{$d.Title}}</span></h3>
<ul class="prog">{{range $d.Sessions}}{{if ne .Kind "break"}}<li><span class="t">{{.Time}}</span> <a href="{{$d.URL}}#{{.ID}}">{{.Title}}</a>{{if .Speakers}} <span class="sub">— {{range $i, $s := .Speakers}}{{if $i}}, {{end}}{{$s.Name}}{{end}}</span>{{end}}</li>{{end}}{{end}}</ul>
{{end}}</section>
{{if .Speakers}}<section><h2>Speakers</h2>
<ul class="speakers">{{range .Speakers}}<li><b>{{.Name}}</b>: {{range $i, $r := .Sessions}}{{if $i}}; {{end}}<a href="{{$r.URL}}">{{$r.Title}}</a>{{end}}</li>{{end}}</ul></section>{{end}}
{{template "foot"}}`))

var dayTpl = template.Must(template.New("day").Funcs(funcs).Parse(layout + `{{template "head" (headForDay .)}}
<p class="crumbs"><a href="/events/">Events</a> › <a href="{{.E.URL}}">{{.E.Name}}</a> › {{.D.Long}}</p>
<h1>{{.D.Long}} <span class="sub">{{.E.Name}} · {{.D.Title}}</span></h1>
<p><a class="btn" href="{{.D.AppURL}}">Open this day in the interactive agenda →</a></p>
<nav class="days">{{range .E.Days}}<a href="{{.URL}}"{{if eq .Key $.D.Key}} aria-current="page"{{end}}>{{.Long}}</a>{{end}}</nav>
{{range .D.Sessions}}{{if eq .Kind "break"}}<p class="break">{{.Time}} · {{.Title}}</p>{{else}}
<article class="session" id="{{.ID}}">
  <p class="meta">{{if .Time}}<span class="t">{{.Time}}</span>{{end}}{{if .Room}} · {{.Room}}{{end}}{{if eq .Kind "panel"}} · Panel{{end}}</p>
  <h2>{{.Title}}</h2>
  {{if .Speakers}}<p class="who">{{range $i, $s := .Speakers}}{{if $i}}, {{end}}{{if $s.Name}}<b>{{$s.Name}}</b>{{else}}Speaker not named{{end}}{{if $s.Role}} ({{$s.Role}}){{end}}{{end}}</p>{{end}}
  {{range .Paras}}<p>{{.}}</p>{{end}}
  {{range .Speakers}}{{if .Bio}}<details><summary>About {{.Name}}</summary><p class="bio">{{.Bio}}</p></details>{{end}}{{end}}
</article>{{end}}{{end}}
{{template "foot"}}`))

var listTpl = template.Must(template.New("list").Funcs(funcs).Parse(layout + `{{template "head" (headForList .)}}
<h1>Events <span class="sub">{{.Brand}} agendas, sessions and speakers</span></h1>
{{if .Next}}<p class="lead">Next: {{.Next}}. The agenda appears on the <a href="/">home page</a> a week before the start.</p>{{end}}
<ul class="events">{{range .Events}}<li><a href="{{.URL}}"><b>{{.Name}}</b></a><br><span class="sub">{{.Range}} · {{.Place}} · {{.Sessions}} sessions · {{len .Speakers}} speakers</span>{{with .Recap}}<br>{{.Teaser}}{{end}}</li>{{end}}</ul>
{{template "foot"}}`))

// homeTpl is the crawlable block inside index.html (the app replaces it on load).
var homeTpl = template.Must(template.New("home").Parse(`<div class="info">
<section><h2>About {{.Brand}}</h2><div class="prose">{{range .About}}<p>{{.}}</p>{{end}}</div></section>
{{if .Next}}<section><h2>Next event</h2><p>{{.Next}}. The agenda appears here a week before the start.</p></section>{{end}}
{{if .Events}}<section><h2>Events</h2><ul>{{range .Events}}<li><a href="/events/{{.ID}}/">{{.Name}}</a>: {{.Range}}, {{.Sessions}} sessions, {{len .Speakers}} speakers</li>{{end}}</ul><p><a href="/events/">All events</a></p></section>{{end}}
</div>`))
