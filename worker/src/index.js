// Vote helper for e2emvc.meppie.eu (Cloudflare Worker).
//
// The site is static, so it cannot hold the GitHub App's client secret. This
// Worker does the three things that need a secret or a server:
//   /login, /callback  "Sign in with GitHub" (GitHub App user token)
//   /refresh           swaps an expired token for a new one
//   /vote              checks Turnstile and the GitHub account, signs the
//                      encrypted ballot and opens the vote issue as the user
//   /favourites        opens a favourites issue as the user
//   /slides            hands out a session's slides to people who voted on it
//                      (switched off until SLIDES_ENABLED is "1")
// The vote itself stays encrypted end to end: this Worker never sees the stars.
//
// Bot protection: GitHub sign-in, a Cloudflare Turnstile check on every vote,
// only "User" accounts at least MIN_ACCOUNT_DAYS old, and a signature the
// counting workflow requires, so issues made around the site are not counted.

const GH = "https://github.com";
const API = "https://api.github.com";

export default {
  async fetch(req, env) {
    const url = new URL(req.url);
    const origin = req.headers.get("Origin") || "";
    const cors = {
      "Access-Control-Allow-Origin": allowed(origin, env) ? origin : env.SITE_ORIGIN,
      "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
      "Access-Control-Expose-Headers": "Content-Disposition",
      "Access-Control-Allow-Headers": "Content-Type, Authorization",
      "Access-Control-Max-Age": "86400",
      "Vary": "Origin",
    };
    if (req.method === "OPTIONS") return new Response(null, { headers: cors });
    try {
      switch (url.pathname) {
        case "/login": return login(url, env);
        case "/callback": return await callback(url, env);
        case "/refresh": return await guard(req, origin, env, cors, refresh);
        case "/vote": return await guard(req, origin, env, cors, vote);
        case "/favourites": return await guard(req, origin, env, cors, favourites);
        case "/slides": return await slides(req, url, origin, env, cors);
        case "/": return new Response("E2E EUCDEX.Ai unofficial vote helper. Nothing to see here.", { headers: { "Content-Type": "text/plain" } });
        default: return new Response("Not found", { status: 404 });
      }
    } catch (e) {
      return json({ error: "Something went wrong. Please try again." }, 500, cors);
    }
  },
};

const allowed = (o, env) => o === env.SITE_ORIGIN || /^http:\/\/localhost(:\d+)?$/.test(o) && env.ALLOW_LOCALHOST === "1";

async function guard(req, origin, env, cors, fn) {
  if (req.method !== "POST" || !allowed(origin, env)) return json({ error: "Not allowed." }, 403, cors);
  let body = {};
  try { body = await req.json(); } catch {}
  return fn(req, body, env, cors);
}

function json(obj, status = 200, headers = {}) {
  return new Response(JSON.stringify(obj), { status, headers: { ...headers, "Content-Type": "application/json", "Cache-Control": "no-store" } });
}

function back(env, ret, params) {
  const target = new URL(ret && ret.startsWith(env.SITE_ORIGIN + "/") ? ret : env.SITE_ORIGIN + "/");
  target.hash = "auth=" + b64url(JSON.stringify(params));
  return Response.redirect(target.toString(), 302);
}

const b64url = (s) => btoa(unescape(encodeURIComponent(s))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");

function login(url, env) {
  const state = (url.searchParams.get("state") || "").slice(0, 64);
  const ret = url.searchParams.get("return") || "";
  // The return address travels in GitHub's state parameter, next to the site's own check value.
  const packed = b64url(JSON.stringify({ s: state, r: ret }));
  const gh = new URL(GH + "/login/oauth/authorize");
  gh.searchParams.set("client_id", env.GH_CLIENT_ID);
  gh.searchParams.set("redirect_uri", new URL("/callback", url).toString());
  gh.searchParams.set("state", packed);
  gh.searchParams.set("allow_signup", "false");
  return Response.redirect(gh.toString(), 302);
}

async function callback(url, env) {
  let st = {};
  try { st = JSON.parse(decodeURIComponent(escape(atob(url.searchParams.get("state").replace(/-/g, "+").replace(/_/g, "/"))))); } catch {}
  const code = url.searchParams.get("code");
  if (!code) return back(env, st.r, { error: "Sign-in was cancelled.", state: st.s });
  const tok = await token({ code }, env);
  if (!tok.access_token) return back(env, st.r, { error: "GitHub did not accept the sign-in.", state: st.s });
  const user = await ghUser(tok.access_token);
  return back(env, st.r, {
    state: st.s, login: user.login || "",
    t: tok.access_token, r: tok.refresh_token || "",
    e: tok.expires_in ? Date.now() + (tok.expires_in - 120) * 1000 : 0,
  });
}

async function token(extra, env) {
  const res = await fetch(GH + "/login/oauth/access_token", {
    method: "POST",
    headers: { "Accept": "application/json", "Content-Type": "application/json" },
    body: JSON.stringify({ client_id: env.GH_CLIENT_ID, client_secret: env.GH_CLIENT_SECRET, ...extra }),
  });
  return res.json();
}

async function refresh(req, body, env, cors) {
  if (!body.r) return json({ error: "Please sign in again." }, 401, cors);
  const tok = await token({ grant_type: "refresh_token", refresh_token: body.r }, env);
  if (!tok.access_token) return json({ error: "Please sign in again." }, 401, cors);
  return json({ t: tok.access_token, r: tok.refresh_token || "", e: tok.expires_in ? Date.now() + (tok.expires_in - 120) * 1000 : 0 }, 200, cors);
}

async function ghUser(t) {
  const res = await fetch(API + "/user", { headers: ghHeaders(t) });
  return res.ok ? res.json() : {};
}

const ghHeaders = (t) => ({ "Authorization": "Bearer " + t, "Accept": "application/vnd.github+json", "User-Agent": "e2e-unofficial-votes", "X-GitHub-Api-Version": "2022-11-28" });

async function person(req, env) {
  const t = (req.headers.get("Authorization") || "").replace(/^Bearer\s+/i, "");
  if (!t) return { error: "Please sign in with GitHub first.", status: 401 };
  const u = await ghUser(t);
  if (!u.login) return { error: "Your GitHub sign-in has expired. Please sign in again.", status: 401 };
  return { t, u };
}

async function turnstileOK(tsToken, req, env) {
  if (!tsToken) return false;
  const form = new FormData();
  form.append("secret", env.TURNSTILE_SECRET);
  form.append("response", tsToken);
  const ip = req.headers.get("CF-Connecting-IP");
  if (ip) form.append("remoteip", ip);
  const res = await fetch("https://challenges.cloudflare.com/turnstile/v0/siteverify", { method: "POST", body: form });
  const out = await res.json();
  return out.success === true;
}

async function hmacHex(key, msg) {
  const k = await crypto.subtle.importKey("raw", new TextEncoder().encode(key), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig = new Uint8Array(await crypto.subtle.sign("HMAC", k, new TextEncoder().encode(msg)));
  return [...sig].map(b => b.toString(16).padStart(2, "0")).join("");
}

async function openIssue(t, title, body, env) {
  const res = await fetch(`${API}/repos/${env.REPO}/issues`, {
    method: "POST", headers: { ...ghHeaders(t), "Content-Type": "application/json" },
    body: JSON.stringify({ title, body }),
  });
  return res.ok;
}

async function vote(req, body, env, cors) {
  const p = await person(req, env);
  if (p.error) return json({ error: p.error }, p.status, cors);
  const ballot = String(body.ballot || "");
  const key = String(body.key || "");
  if (!/^[a-z0-9-]{3,40}\/[a-z0-9-]{2,90}$/.test(key)) return json({ error: "This vote could not be read." }, 400, cors);
  if (!/^[A-Za-z0-9+/=]{300,400}$/.test(ballot)) return json({ error: "This vote could not be read." }, 400, cors);
  if (!(await turnstileOK(body.turnstile, req, env))) return json({ error: "The human check did not pass. Please try again." }, 403, cors);
  if (p.u.type !== "User") return json({ error: "Only personal GitHub accounts can vote." }, 403, cors);
  const days = (Date.now() - Date.parse(p.u.created_at)) / 864e5;
  if (!(days >= Number(env.MIN_ACCOUNT_DAYS || 7))) return json({ error: `GitHub accounts younger than ${env.MIN_ACCOUNT_DAYS || 7} days cannot vote yet.` }, 403, cors);
  const login = p.u.login.toLowerCase();
  const sig = await hmacHex(env.VOTE_HMAC, login + "\n" + ballot);
  const text = [
    "Sent from https://e2emvc.meppie.eu/ — this vote is encrypted; only averages are ever shown.",
    "", "### Vote", "", ballot, "", `<!-- sig:${sig} -->`,
  ].join("\n");
  if (!(await openIssue(p.t, "Session rating", text, env))) return json({ error: "GitHub did not accept the vote. Please try again later." }, 502, cors);
  // Remember THAT this account voted on this session (never what), to unlock its slides.
  if (env.VOTED) await env.VOTED.put(await votedKey(env, login, key), "1");
  return json({ ok: true }, 200, cors);
}

async function favourites(req, body, env, cors) {
  const p = await person(req, env);
  if (p.error) return json({ error: p.error }, p.status, cors);
  const ids = (Array.isArray(body.stars) ? body.stars : []).map(String).filter(x => /^[a-z0-9][a-z0-9-]{1,100}$/.test(x)).slice(0, 500);
  const text = ["Sent from https://e2emvc.meppie.eu/ (Sync).", "", "### Favourites", "", ids.join("\n") || "-"].join("\n");
  if (!(await openIssue(p.t, "Favourites", text, env))) return json({ error: "GitHub did not accept the list. Please try again later." }, 502, cors);
  return json({ ok: true, n: ids.length }, 200, cors);
}

const votedKey = async (env, login, key) => "v:" + (await hmacHex(env.VOTE_HMAC, "voted\n" + login.toLowerCase() + "\n" + key)).slice(0, 40);

// Slides live in a private GitHub repository (SLIDES_REPO) with a manifest.json:
// { "<event>/<session>": { "path": "2027-04-nice/9-some-talk.pdf", "name": "Some talk.pdf" } }
// SLIDES_TOKEN is a read-only token for that repository only.
async function slides(req, url, origin, env, cors) {
  if (req.method !== "GET" || !allowed(origin, env)) return json({ error: "Not allowed." }, 403, cors);
  if (env.SLIDES_ENABLED !== "1" || !env.SLIDES_TOKEN || !env.SLIDES_REPO) return json({ error: "Slides are not available yet." }, 404, cors);
  const key = url.searchParams.get("key") || "";
  const p = await person(req, env);
  if (p.error) return json({ error: p.error }, p.status, cors);
  if (!env.VOTED || !(await env.VOTED.get(await votedKey(env, p.u.login, key)))) return json({ error: "Vote on this session first; then the slides are yours." }, 403, cors);
  const gh = (path, accept) => fetch(`${API}/repos/${env.SLIDES_REPO}/contents/${path}`, { headers: { ...ghHeaders(env.SLIDES_TOKEN), "Accept": accept } });
  const man = await gh("manifest.json", "application/vnd.github.raw");
  if (!man.ok) return json({ error: "Slides are not available yet." }, 404, cors);
  const entry = (await man.json())[key];
  if (!entry || !entry.path) return json({ error: "No slides for this session." }, 404, cors);
  const file = await gh(entry.path.split("/").map(encodeURIComponent).join("/"), "application/vnd.github.raw");
  if (!file.ok) return json({ error: "The slides could not be fetched. Please try again later." }, 502, cors);
  const name = String(entry.name || entry.path.split("/").pop()).replace(/["\\\r\n]/g, "");
  return new Response(file.body, { headers: { ...cors, "Content-Type": file.headers.get("Content-Type") || "application/octet-stream", "Content-Disposition": `attachment; filename="${name}"`, "Cache-Control": "private, no-store" } });
}
