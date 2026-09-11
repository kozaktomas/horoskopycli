# Architecture

`horoskopycli` prints one horoskopy.cz horoscope and exits. It is a single Go binary with one
outbound dependency, the horoskopy.cz JSON API, and no dependencies outside the standard
library beyond `golang.org/x/text`.

The dream book (`snar`) is the one feature that needs state: horoskopy.cz gates it on a Seznam
login, so a session cookie is kept between runs. Everything else stays stateless.

## Components

```
main.go                        CLI: commands, argument parsing, usage text, exit codes
├── internal/horoskopy
│   ├── catalog.go             signs, periods, and parsing user input into them
│   ├── document.go            sign + period -> horoskopy.cz article ID and slug
│   ├── client.go              HTTP client, JSON decoding, article -> Horoscope
│   ├── dreams.go              dream -> the gated dream book API -> interpretation
│   └── render.go              Horoscope / interpretation -> plain text for a terminal
└── internal/seznam
    ├── login.go               orchestrates a browser login, waits for the cookie
    ├── browser.go             finds and starts a browser on a throwaway profile
    ├── cdp.go                 DevTools protocol: Chrome and everything based on it
    ├── bidi.go                WebDriver BiDi: Firefox and its forks
    ├── ws.go                  a small WebSocket client, because both speak nothing else
    ├── defaultbrowser.go      parsing the system's idea of a default browser
    ├── browser_paths_*.go     where browsers live, per operating system
    ├── session.go             the stored session: load, save, forget
    └── account.go             login.horoskopy.cz: whose session is this, and revoke it
```

`main.go` depends on both packages; neither depends on the other, and nothing depends on
`main.go`. `internal/horoskopy` is transport- and CLI-agnostic: it returns values, and
rendering is a separate step. `internal/seznam` knows nothing about horoscopes — it produces a
cookie, and `internal/horoskopy` is handed that cookie as a plain string.

## Data flow

```
"ryby" "zitra"
      │
      ├─ ParseSign / ParsePeriod      lower-case, trim, strip diacritics, match a known slug
      │
      ├─ documentUID                  ryby + zitra -> article 558
      │
      ├─ GET api-web.horoskopy.cz/v1/documents/558
      │
      ├─ verify slug                  response slug must be "…-ryby-zitra"
      │
      ├─ toSections                   content blocks -> headed sections
      │
      └─ Render                       -> text on stdout
```

## The dream book, and why the login looks like this

Horoscope articles are public. The dream book is not: it runs a language model per request, so
Seznam meters it per account.

```
horoskopycli snar "Zdálo se mi…"
      │
      ├─ load ~/.config/horoskopycli/session.json   (or $HOROSKOPYCLI_DS)
      │
      ├─ POST api-web.horoskopy.cz/v1/dream-book    Cookie: ds=…
      │       {"dream": "…"}
      │
      └─ RenderDream                                 -> text on stdout
```

The credential is a single cookie, `ds`, scoped to `horoskopy.cz` and valid for about a year.
Nothing else is needed: no `Origin`, no `Referer`, no second cookie. `login` exists only to
obtain it.

**Seznam OAuth 2.0 does not help here.** Seznam runs a real OAuth service, and it is friendly to
a CLI — `localhost` redirect URIs are allowed and PKCE is supported. But its tokens are only
accepted by `login.seznam.cz/api/v1/user`; the horoskopy.cz API authenticates with the
first-party SSO cookie and takes no bearer token. An OAuth flow would prove who the user is to
nobody but ourselves.

A browser page cannot be used as a proxy either: `api-web.horoskopy.cz` answers a cross-origin
preflight with `access-control-allow-credentials: false` and no allowed origin, and `ds` is
`SameSite=Lax`, so a page on `localhost` could neither send the cookie nor read the answer.

That leaves reading the cookie out of a browser, which is what `login` does:

```
horoskopycli login
      │
      ├─ find a browser                     --browser, else the system default, else PATH,
      │                                     else the usual macOS and Windows locations
      │
      ├─ pick a free port                   and start the browser on a throwaway profile
      │                                     listening there
      │
      ├─ wait for that port to answer       Chromium: GET /json/version names its socket
      │                                     Firefox:  a connection is answer enough
      │
      ├─ ask for the cookie jar every second  until `ds` for horoskopy.cz appears
      │   └─ open horoskopy.cz once           if Seznam interrupted the return trip: visiting
      │                                       the site is what hands the session over
      │
      ├─ GET login.horoskopy.cz/api/v1/user/badge   confirm the cookie works, get the account
      │
      └─ save {cookie, account, expires}    ~/.config/horoskopycli/session.json, mode 0600
```

The browser is asked to close itself and then given time to go, rather than being killed
outright. A killed browser leaves helper processes behind that write the profile directory back
out after it has been deleted — with the login cookie in it. That was observed with Chrome.

Teardown therefore waits for the browser's remote control port to stop answering before
deleting the profile, not for the process it started: with a snap, a launcher script or `open`,
the process this CLI spawned exits at once while the real browser runs on. That was observed
with Firefox. A kill and a couple of retries follow, in case the port outlives everything.

### Two protocols, one loop

Chrome and its relatives speak the DevTools protocol; Firefox speaks WebDriver BiDi and has no
DevTools endpoint at all. The two are close enough in shape — a command in, an answer with the
same id out — that only the vocabulary differs:

| | Chromium (`cdp.go`) | Firefox (`bidi.go`) |
|---|---|---|
| endpoint | `<profile>/DevToolsActivePort` | announced on stderr, plus `/session` |
| session | none needed | `session.new` first |
| read cookies | `Storage.getCookies` | `storage.getCookies` |
| open a page | `Target.createTarget` | `browsingContext.create`, then `browsingContext.navigate` |
| shut down | `Browser.close` | `browser.close` |
| cookie value | a plain string | `{type: "string", value: …}` |
| expiry field | `expires` | `expiry` |

Both satisfy `cookieSource`, so `waitForSession` — the loop that actually waits for the human —
is written once and knows nothing about either protocol.

Two traps worth recording, both of which fail quietly rather than loudly:

- BiDi's `storage.getCookies` domain filter matches **exactly**, so asking it for
  `horoskopy.cz` returns nothing at all, because the cookie is stored under `.horoskopy.cz`.
  The whole jar is read and filtered in Go instead, which is what the Chromium path does anyway.
- `browsingContext.create` takes **no address**. Passing one is not an error; it is ignored, and
  the result is a blank tab and a login that waits for a page nobody opened. Navigating is a
  second command.

The login page is opened this way, over the protocol, rather than passed on the command line.
A browser that is already running can take a command-line URL for itself, and then the person
signs in to a window this CLI cannot read — which looks exactly like a hang.

Commands are bounded by a deadline on the socket. Without one, a browser that stops answering
hangs the CLI for good: the login loop only checks its own deadline between commands, never
during one.

### Finding the browser a person actually uses

`horoskopycli login --debug` prints what the browser is holding on each poll — how many cookies
and which domains carry a Seznam session — which is the quickest way to tell "no window opened"
apart from "signed in somewhere this CLI cannot see".

`login` opens the browser the user would expect, not whichever one happens to be first in PATH:

- **Linux** — `xdg-settings get default-web-browser` names a `.desktop` file; its `Exec` line
  names the binary. Only the `[Desktop Entry]` section counts, since the actions further down
  the file have `Exec` lines of their own.
- **macOS** — LaunchServices records the https handler in a binary plist, which `plutil` can
  convert to JSON; the bundle identifier is mapped to the executable inside the application.
- **Windows** — the registry holds a `ProgId` for https under `UrlAssociations`, and that key's
  `shell\open\command` holds the quoted path to the executable.

If the default browser is one this CLI cannot drive — Safari, or something exotic — the search
falls through to PATH and then to the usual install locations. Only Linux is tested against a
real system; the other two are written from their documented behaviour and fall back safely.

### Starting a browser people can actually see

Two platform habits make "just run the executable" wrong.

On **macOS** a browser is an application bundle, and running the binary inside it while another
instance of the same bundle is already running — which it usually is — gives a process with no
window. It starts, it answers on its remote control port, it even loads pages; there is simply
nothing on screen to type into, so the login waits forever for a human who was never given a
chance. `open -n -a <bundle> --args …` is the documented way to ask for a genuinely separate
instance, and the one that comes with a window. The executable is still tried afterwards, for a
browser installed outside a bundle.

Because `open` returns immediately and hands the browser its own stdio, the endpoint cannot be
read from the process any more. So the port is chosen here — `net.Listen` on :0, note the
number, close it again — and the browser is told to use it. That also removed both of the
previous ways of learning the endpoint, one per engine, and replaced them with waiting for the
port to answer.

### Snap and the profile that cannot be read

A snap-packaged browser gets a private `/tmp`, and cannot see hidden directories in the home
directory either. A profile in `os.MkdirTemp("")` is therefore invisible to it: Ubuntu's Firefox
starts, finds nothing, and never announces its remote agent. Verified on Ubuntu with snap
Firefox 154, where `/tmp/…` and `~/.cache/…` both fail and `~/snap/firefox/common/…` works, so
that is where the throwaway profile goes when the browser looks like a snap.

`logout` is not just a file deletion: it POSTs to `login.horoskopy.cz/logout`, which invalidates
the session at Seznam, so a copied cookie stops working too. The service is particular about
that request — a GET is answered with 403 and a POST without a JSON body with 400.

### Why a hand-written WebSocket client

Both protocols speak WebSocket and nothing else. `chromedp` would pull five modules in for one
command, and would not help with Firefox at all, so `ws.go` implements the little of RFC 6455
that is needed: one masked text frame out, one message in, continuation and control frames
handled.

The subtle part is the handshake: the browser often sends its first frame in the same packet as
the upgrade response, so the buffered reader used for the headers has to be the one the
connection keeps reading from. Starting a fresh reader loses those bytes and hangs forever.
`TestWSDialKeepsBytesArrivingWithTheHandshake` covers exactly that.

### What is stored, and what it is worth

`ds` is Seznam's single sign-on cookie: the same value works across `seznam.cz`, mail included.
It is not scoped to horoscopes and cannot be narrowed. That is why the session file is `0600`,
why `login` verifies before storing, and why `logout` revokes rather than forgets.

## Why the JSON API and not the page

The tool originally scraped `www.horoskopy.cz` with an XPath expression for `#content-detail`.
The 2026 redesign moved the site onto Seznam's IMA.js stack: the page is rendered client-side,
that element no longer exists, and the remaining class names are build-time hashes (`c_bn`,
`g_e4`) that change on every deploy. Scraping it again would break again.

The site itself renders from `https://api-web.horoskopy.cz`, which serves clean structured JSON
and no markup. Reading it directly is both simpler and considerably more stable. It is an
undocumented API and could change without notice, hence the slug check described below.

## Article IDs

horoskopy.cz keeps one evergreen article per sign and period — 48 in total — and rewrites the
body in place as the horoscope changes. The IDs are therefore stable and laid out in blocks of
twelve consecutive numbers, one block per period, in zodiac order starting at Beran:

| Period  | Beran | … | Ryby |
|---------|-------|---|------|
| `dnes`  | 535   | … | 546  |
| `zitra` | 547   | … | 558  |
| `mesic` | 559   | … | 570  |
| `rok`   | 571   | … | 582  |

So `documentUID` is `periodBaseUID[period] + <index of sign in signs>`. The order of the `signs`
slice in `catalog.go` is load-bearing.

This table is hardcoded because the API supports no filtering: `slug`, `where`, `uid`,
`sections`, `q` and `search` are all accepted and all ignored, each returning the same
unfiltered document listing. There is no way to look an article up by name.

To make a stale table loud rather than silent, `Client.Fetch` compares the `slug` the API echoes
back against the slug it expected and returns `ErrUnexpectedArticle` on a mismatch. Without that
check, a renumbering would print the wrong sign's horoscope and look perfectly fine.

`TestDocumentUID` pins the boundary IDs of every block, so the table is also asserted in CI.

## Errors

| Error                  | Meaning                                              | CLI behaviour                  |
|------------------------|------------------------------------------------------|--------------------------------|
| `ErrUnknownSign`       | input is not a zodiac sign                            | usage text, exit 1             |
| `ErrUnknownPeriod`     | input is not a supported period                       | usage text, exit 1             |
| `ErrUnavailable`       | horoskopy.cz unreachable or non-200                   | apology ASCII art, exit 1      |
| `ErrUnexpectedArticle` | API returned a different article than requested       | error message, exit 1          |
| `ErrEmptyArticle`      | article exists but carries no text                    | error message, exit 1          |
| `ErrDreamTooShort/Long`| dream outside the 3–2000 characters the API accepts    | error message, exit 1, no call |
| `ErrUnauthorized`      | the dream book refused the session                    | "run `horoskopycli login`"     |
| `ErrNoSession`         | no session stored yet                                 | "run `horoskopycli login`"     |
| `ErrSessionExpired`    | the login service says the cookie is signed out       | error message, exit 1          |
| `ErrNoBrowser`         | no browser the CLI can drive                          | error message, exit 1          |
| `ErrLoginTimeout`      | the user did not finish signing in                    | error message, exit 1          |

Only `ErrUnavailable` prints the apology; bad user input is not an outage.

## Testing

`internal/horoskopy` is tested against `httptest` servers, with a recorded API response in
`internal/horoskopy/testdata/ryby-dnes.json`. `main.go` is tested through `app.run`, whose
dependencies are interfaces, so every command is exercised without network access, a browser or
a session file.

`internal/seznam` is tested the same way: the login service against `httptest`, the session
store against a temporary configuration directory, and the browser side against a stub that
speaks the DevTools protocol over an in-memory pipe. `waitForSession` takes its poll interval as
an argument so the tests do not wait a second per poll.

No test reaches the real horoskopy.cz, Seznam, or a real browser. The one thing that cannot be
covered this way is starting an actual browser; `findBrowser`, `waitForEndpoint`,
`waitForAnnouncement`, `readEndpoint`, `browserArgs`, `snapProfileDir` and `waitOrKill` are
tested individually instead, and the platform-specific parsers (`.desktop` files, the macOS
handler list, `reg query` output) have tests of their own that run everywhere.

Both browser paths were also exercised by hand against a live Seznam account before merging:
Chromium on Linux/ARM64 and Firefox 154 on Ubuntu, each through login, an interpreted dream and
a logout that leaves the cookie rejected.
