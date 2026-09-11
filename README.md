# Horoskopy.cz CLI

[![Go](https://github.com/kozaktomas/horoskopycli/actions/workflows/go.yaml/badge.svg)](https://github.com/kozaktomas/horoskopycli/actions/workflows/go.yaml)

Read your [horoskopy.cz](https://www.horoskopy.cz) horoscope without leaving the terminal.

# How to install

### From source code:

```bash
go install github.com/kozaktomas/horoskopycli@latest
```

### From releases

Download the newest version from [Releases](https://github.com/kozaktomas/horoskopycli/releases) and put the binary to
your $PATH.

# Usage

```bash
horoskopycli <sign> [period]
```

The period is optional and defaults to `dnes`.

```bash
horoskopycli byk          # today
horoskopycli ryby zitra   # tomorrow
horoskopycli lev mesic    # this month
horoskopycli stir rok     # this year
```

**Signs:** `beran`, `byk`, `blizenci`, `rak`, `lev`, `panna`, `vahy`, `stir`, `strelec`, `kozoroh`, `vodnar`, `ryby`

**Periods:** `dnes`, `zitra`, `mesic`, `rok`

Czech diacritics and capitalisation are accepted too, so `horoskopycli Býk zítra` works just as well.

Running `horoskopycli` with no arguments prints this list.

### Example output

```
Ryby zítra
==========

Blízkost se často buduje prostřednictvím obyčejných okamžiků a pozornosti. ...

=> Láska a přátelství
Malé gesto vám připomene, proč jste si právě tohoto člověka pustili k sobě. ...

=> Peníze a práce
Malý pracovní přešlap vás zaskočí víc, než by měl. ...

https://www.horoskopy.cz/clanek/horoskop-znameni-zverokruhu-ryby-zitra-558
```

# Dream book (snář)

horoskopy.cz can also interpret a dream with an AI model. Unlike the horoscopes, that part is
not public: Seznam meters it per account, so the CLI has to be signed in.

```bash
horoskopycli login                       # opens a browser, sign in as usual
horoskopycli snar "Zdálo se mi, že létám nad mořem."
echo "Zdálo se mi o starém domě." | horoskopycli snar
horoskopycli logout                      # ends the session, at Seznam too
```

`login` opens your default browser on a throwaway profile, waits for you to sign in to Seznam,
keeps the session cookie it is given and deletes the profile again. Your password never passes
through the CLI. The session lasts about a year, so this is a once-a-year chore.

**Firefox and Chrome both work**, as do Brave, Edge, Vivaldi, Opera, Chromium and the Firefox
forks — whichever of them is your default browser is the one that opens. Safari cannot be
driven and is skipped. Because the profile is a fresh one, the window that opens has none of
your logins in it, which is also why nothing of the login is left behind afterwards.

Other ways to sign in:

```bash
horoskopycli login --browser /path/to/browser   # pick one explicitly
horoskopycli login --paste                      # no supported browser: paste the cookie
horoskopycli login --debug                      # say what the browser is holding, when it sticks
export HOROSKOPYCLI_DS=<cookie>                 # for scripts and CI
```

⚠️ **The stored cookie is your whole Seznam account**, not just horoscopes — the same session
that opens your mailbox. It is written to `~/.config/horoskopycli/session.json` with mode
`0600`; treat that file like a password, and run `horoskopycli logout` when you are done with a
machine, which invalidates the session at Seznam rather than merely forgetting it here.

### Development

Make changes and run `make` and make it pass.

See [docs/architecture.md](docs/architecture.md) for how the tool talks to horoskopy.cz — worth reading before
changing anything in `internal/horoskopy` or `internal/seznam`.

###### Run tests
```bash
make test
```

###### Fix lint
```bash
make lint-fix
```
