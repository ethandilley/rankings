# 01 — Authentication & Ownership Enforcement

**Priority: do this first.** Everything else in this repo assumes it's fine that anyone can edit anyone's rankings. It isn't, and it's the one thing the project owner explicitly called out as a requirement.

## Goal

Exactly 12 known people, each with their own login, each able to edit **only their own** ranking list. Nobody signs up — the commissioner (the person running this) creates all 12 accounts up front. No password-reset-via-email flow is required, but a simple recovery path should exist (see "Lost access" below).

## Why not [big auth provider]?

You could reach for Auth0/Clerk/Supabase Auth/NextAuth, but this is a single Go binary serving 12 people who all know each other. Pulling in a third-party auth SaaS adds an external dependency, a vendor account, and a network call on every request for a problem that a `users` table and a signed session cookie solve completely. Recommend rolling minimal auth in-house. If the team later wants SSO via Google (many leagues already use Google for other league admin), that's a reasonable v2 — see "Optional: Google OAuth" at the end.

## Recommended approach: passcode-based accounts + signed session cookies

### 1. Data model

```sql
-- +goose Up
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,       -- e.g. "ethan", stable slug, lowercase
    display_name  TEXT NOT NULL,               -- e.g. "Ethan Dilley"
    password_hash TEXT NOT NULL,               -- bcrypt or argon2id
    is_admin      BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token       TEXT PRIMARY KEY,              -- random 32-byte token, base64url
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_sessions_user_id ON sessions (user_id);

-- +goose Down
DROP TABLE sessions;
DROP TABLE users;
```

Why a `sessions` table instead of a stateless JWT: with 12 users, the ability to instantly revoke a session (someone leaves the league, a laptop gets lost) matters more than avoiding one indexed lookup per request. Keep it simple — this is not a scale problem.

### 2. Reconcile identity with existing data — do this before writing any code

`rankings.owner` and `players.owner` are currently free-text strings, populated inconsistently (see `00-overview.md` point 8: sometimes fantasy team nicknames like `"Whats up my Nabers"`, sometimes real names like `"Ethan Dilley"` from the ESPN sync). Before building `users`, the commissioner needs to:

1. Pick **one canonical `username`** per person (suggest: first name, lowercase, e.g. `ethan`, `rohan`, disambiguated with a last-initial if two share a first name).
2. Write a **one-time backfill migration** that maps every distinct existing value of `rankings.owner` to the correct new `users.id`, then adds a `user_id` foreign key column to `rankings` (see `02-data-model-and-players-integration.md`, which also touches this table — coordinate so you're not writing two competing migrations against the same column).
3. Manually reconcile the fantasy-team-nickname rows and the real-name rows created by the ESPN sync script — they very likely refer to the same 12 people and need to collapse to one `user_id` each. This is a manual data-fixing step, not something to automate blindly, because nickname → person mapping isn't guaranteed stable (check `scripts/load_players.py`'s `team_to_owner` dict for the current mapping as of the season this repo was exported).

### 3. Login flow

- Add page: static `login.html` (or a login view baked into `index.html` if you'd rather keep one file — the project owner's call, either is fine).
- `POST /auth/login` — body `{ "username": "...", "password": "..." }`.
  - Look up `users` by `username`.
  - Compare password with `bcrypt.CompareHashAndPassword` (use `golang.org/x/crypto/bcrypt`; cost factor 12 is fine at this scale).
  - On success: generate a 32-byte random token (`crypto/rand`, not `math/rand`), insert into `sessions` with `expires_at = now() + 30 days`, set it as an `HttpOnly`, `Secure`, `SameSite=Lax` cookie named e.g. `session`.
  - On failure: generic `401 invalid username or password` — don't reveal whether the username existed.
- `POST /auth/logout` — delete the session row matching the cookie, clear the cookie.
- `GET /auth/me` — returns the logged-in user's `username`/`display_name`/`is_admin`, or `401` if no valid session. The frontend calls this on load to know who's logged in and to show/hide "edit" controls.

### 4. Provisioning accounts (no self-signup)

Add a small admin-only endpoint or a one-off CLI command (`cmd/adminuser/main.go`) that the commissioner runs once per person:

```
go run ./cmd/adminuser -username=ethan -display-name="Ethan Dilley" -password=<temp password>
```

Have it print the generated password if one wasn't supplied (random 12-char), so the commissioner can hand out temp credentials via text/Discord/whatever the league already uses. Add a `PATCH /auth/password` endpoint (auth required, changes own password) so people can change the temp password on first login. This avoids building email delivery entirely, which is overkill for 12 people you can just text.

### 5. Middleware & ownership enforcement — the actual point of this doc

Add a middleware that runs before every mutating route:

```go
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        user, err := userFromSessionCookie(r)
        if err != nil {
            http.Error(w, "not authenticated", http.StatusUnauthorized)
            return
        }
        ctx := context.WithValue(r.Context(), userCtxKey, user)
        next(w, r.WithContext(ctx))
    }
}
```

Then, critically, **stop trusting the `{owner}` path parameter for write operations.** Right now `PATCH /rankings/{owner}/move` will happily let a request claim any owner string. Change every mutating handler to derive the owner from the authenticated session, not the URL:

```go
func (h *RankingsService) moveRanking(w http.ResponseWriter, r *http.Request) {
    authedUser := userFromContext(r.Context())
    pathOwner := r.PathValue("owner")
    if pathOwner != authedUser.Username {
        http.Error(w, "you can only edit your own rankings", http.StatusForbidden)
        return
    }
    // ...existing logic, unchanged
}
```

Keeping `{owner}` in the URL (rather than dropping it and always inferring "self") is intentional: it keeps the API self-documenting and makes a 403 explicit and debuggable, rather than silently redirecting a mismatched request to the wrong person's data.

Apply `requireAuth` to: `POST /rankings`, `PATCH /rankings/{owner}/move`, `POST /rankings/{owner}/players`, `DELETE /rankings/{owner}/players/{player}`, and any new mutating routes from the other docs (tiers, trade proposals, etc.).

**Leave `GET /rankings` public to any logged-in user** — the whole point of a shared league board is that everyone can see everyone's rankings; only *editing* is restricted. So `GET /rankings` should require *some* valid session (any of the 12), but not ownership of a specific owner.

### 6. Frontend changes

- On load, call `GET /auth/me`. If `401`, redirect to `login.html`.
- Store nothing sensitive in `localStorage` — the session cookie handles auth automatically on every `fetch` as long as requests use `credentials: 'include'` (needed because the API may be on a different origin than the static file — check the CORS doc, `07-security-hardening.md`, for the corresponding server-side change, since `Access-Control-Allow-Origin: *` is incompatible with credentialed requests and must become an explicit allow-list).
- Only render the up/down/remove/add controls on the owner board that matches the logged-in user. Everyone else's board should render read-only (still visible — that's the point of a shared league tool — just no edit affordances). **Do this in the UI as a courtesy, but never rely on it for security** — the backend ownership check in step 5 is what actually protects the data; the frontend hiding buttons is just good UX.

### Edge cases to handle

- Session expired mid-edit → API returns `401` → frontend should redirect to login rather than showing a generic error toast.
- Two tabs open, one for each of two different people sharing a browser profile (this will happen — people share laptops) → make sure logging in as B fully replaces A's session cookie, and that `GET /auth/me` is the source of truth the UI checks, not anything cached client-side.
- Admin flag (`is_admin`): reserve this for the commissioner. Use it later to gate the "seed all 12 teams" button and the players-sync trigger (see `06-espn-sync-service.md`) so a random owner can't wipe/reseed everyone's board.

### Optional: Google OAuth instead of passwords

If the league already lives on Google (shared Sheets, Google Groups, etc.), you could skip passwords entirely and let people log in with their Google account via OAuth 2.0, matching on email address to a pre-provisioned `users` row (still no self-signup — an unrecognized email gets a friendly "ask the commissioner to add you" page). This removes password-reset concerns entirely. It's more setup (Google Cloud OAuth client, consent screen) for a marginal UX gain with only 12 users, so treat it as a nice-to-have, not a requirement.
