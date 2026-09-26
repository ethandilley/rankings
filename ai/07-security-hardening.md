# 07 — Security Hardening

None of this is exotic — it's the standard checklist for "a handful of real people are about to put their session cookies and league opinions into this thing." Most items here are small, and several are called out already in other docs; this doc collects them plus a few not mentioned elsewhere.

## Immediate, do regardless of what else ships

1. **Rotate the leaked ESPN session cookie** (`ESPN_S2`/`ESPN_SWID` values currently sitting in `.envrc` in the repo). Covered in detail in `06-espn-sync-service.md` — repeating here because it's the single most urgent item in this whole doc set. Log out/in on fantasy.espn.com to invalidate the old cookie, add `.envrc` to `.gitignore`, commit an `.envrc.example` instead.
2. **Check git history, not just the current tree.** If `.envrc` (or any other secret) was committed at any point in this repo's history, it's still recoverable from `git log`/`git show` even after being removed from the latest commit, unless history is rewritten (`git filter-repo` or BFG Repo-Cleaner) or the repo is recreated fresh. Given this is a small personal-project repo, recreating it fresh (new repo, no history) after rotating the secret is the simplest safe option — cheaper than history surgery for a repo this size.

## CORS

`withCORS` in `cmd/server/main.go` currently sets `Access-Control-Allow-Origin: *` unconditionally. This is fine for a no-auth prototype but becomes actively dangerous once `01-authentication.md` introduces session cookies, because:

- Wildcard origin **cannot** be combined with `Access-Control-Allow-Credentials: true` per the CORS spec (browsers will reject it) — so a cookie-based session won't even work correctly alongside `*` once the frontend starts sending `credentials: 'include'`.
- Even if it did work, a wildcard origin means literally any website could make credentialed requests to your API from a visitor's browser.

Fix: allow-list the actual origin(s) the frontend is served from (e.g. `http://localhost:8080` for local dev, whatever domain it's deployed to in production — read from an env var, don't hardcode):

```go
func withCORS(allowedOrigin string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            origin := r.Header.Get("Origin")
            if origin == allowedOrigin {
                w.Header().Set("Access-Control-Allow-Origin", origin)
                w.Header().Set("Access-Control-Allow-Credentials", "true")
            }
            w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
            w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
            if r.Method == http.MethodOptions {
                w.WriteHeader(http.StatusNoContent)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

If the frontend and API end up served from the same origin in production (simplest deploy: have the Go server also serve `index.html` as a static file, rather than running it as a separate `file://` page), CORS mostly stops mattering for same-origin requests and this becomes a smaller concern — worth considering as the simpler fix rather than maintaining an allow-list at all. Recommend this: have the Go binary `http.FileServer` the static HTML alongside the API, one origin, one deploy artifact, no CORS configuration needed at all.

## Input validation

Current handlers do reasonable validation of presence (`owner is required`, `player_name is required`) but not much else:

- `rank` values: currently only checked for `>= 1` in some handlers. Also cap at a sane maximum (e.g. `<= 500`) to stop a typo or malicious request from creating a rank of `999999999` and causing a huge shift-loop in `addPlayerToRankings`/`movePlayer`.
- String length limits: `owner`, `player_name` (pre-doc-02) have no length cap — add reasonable ones (`owner <= 64 chars`, matches `username` format from doc 01 once that exists) to stop pathologically large payloads.
- Once doc 02 lands, validate `player_id` actually exists in `players` before insert (the foreign key constraint will catch this at the DB level and return an error — make sure the Go handler translates that into a friendly `400`/`404` rather than leaking a raw Postgres constraint-violation message to the client).

## Rate limiting

At 12 known, authenticated users this is a low-priority item — you're not defending against internet-scale abuse. Still cheap to add a basic per-IP or per-session limiter (e.g. `golang.org/x/time/rate`, one bucket per session token) on mutating endpoints just to make sure a buggy frontend retry-loop (or a script one of your more chaotic-neutral league members writes for fun) can't hammer the DB. Not worth more engineering effort than a simple in-memory token bucket keyed by session — no need for a distributed rate limiter with a single server instance.

## Transport security

If this ends up hosted anywhere reachable over the public internet (rather than just a LAN/VPN the league uses), it needs HTTPS before real session cookies go over the wire — the `Secure` cookie flag from doc 01 requires it anyway (browsers won't send a `Secure` cookie over plain HTTP). Whatever hosting platform is chosen almost certainly offers free TLS termination (Caddy, a platform's built-in HTTPS, Let's Encrypt via certbot) — don't hand-roll certificate management.

## Don't over-invest

Explicitly **not** recommending for a 12-person friend-group app: a Web Application Firewall, DDoS protection service, SOC2-style audit logging, dependency vulnerability scanning pipeline, or penetration testing. These are real practices for real production systems with real attackers and real liability; they're overkill here. Rotate the leaked secret, fix CORS, validate inputs, use HTTPS if public-facing, and this project is appropriately secured for what it actually is.
