// Command horoskopycli prints the horoskopy.cz horoscope for a zodiac sign and
// interprets dreams with the site's dream book.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kozaktomas/horoskopycli/internal/horoskopy"
	"github.com/kozaktomas/horoskopycli/internal/seznam"
)

// errTooManyArguments is returned when more than a sign and a period are given.
var errTooManyArguments = errors.New("too many arguments")

// Taken from https://github.com/NaAbAsD/this_is_fine, thanks!
const sorryMessage = `
Ouch, horoskopy.cz is probably down but I'm here for you! 🤗

     ..
    ...
     .    ..                .
      ..  _ .      .       ..
     .   |_| .  .. ..    .  .
    ..  -___-_. .   .. ..   ..
  ..   /      )      ..      .
 .____/| (0) (0)_()    ..     ..
/|   | |   ^____)      ..      ..
||   |_|    \_//     Uɔ....   .. ..
||    || |    |    ========.  ..  ..
||    || |    |      ||     ..   .
||     \\_\   |\     ||   ...    .
=========||====||    ||  ..       .
  || ||   \Ɔ || \Ɔ   ||   ..    ..
  || ||      ||      ||  .     ..
-------------------------------------
            This is fine.
`

// Subcommands. Everything else is read as a zodiac sign.
const (
	cmdLogin  = "login"
	cmdLogout = "logout"
	cmdDream  = "snar"
)

// fetcher downloads a horoscope. It is satisfied by horoskopy.Client and
// replaced by a fake in tests.
type fetcher interface {
	Fetch(ctx context.Context, sign horoskopy.Sign, period horoskopy.Period) (horoskopy.Horoscope, error)
}

// interpreter reads a dream. It is satisfied by horoskopy.Client.
type interpreter interface {
	InterpretDream(ctx context.Context, cookie, dream string) (string, error)
}

// sessionStore keeps the Seznam session between runs.
type sessionStore interface {
	Load() (seznam.Session, error)
	Save(session seznam.Session) error
	Forget() error
	Path() (string, error)
}

// accountService answers who a session belongs to and can end it.
type accountService interface {
	Account(ctx context.Context, cookie string) (string, error)
	Revoke(ctx context.Context, cookie string) error
}

// loginFunc opens a browser and returns the session the user signed in with.
type loginFunc func(ctx context.Context, opts seznam.LoginOptions) (seznam.Session, error)

// app holds everything the commands need, so tests can replace the network,
// the browser and the session file with fakes.
type app struct {
	horoscopes   fetcher
	dreams       interpreter
	sessions     sessionStore
	accounts     accountService
	browserLogin loginFunc
	in           io.Reader
	out          io.Writer
}

// main wires up the real client and turns a failure into a non-zero exit code.
func main() {
	client := horoskopy.NewClient()
	cli := &app{
		horoscopes:   client,
		dreams:       client,
		sessions:     seznamStore{},
		accounts:     seznam.NewAPI(),
		browserLogin: seznam.Login,
		in:           os.Stdin,
		out:          os.Stdout,
	}

	if err := cli.run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "horoskopycli: %s\n", err)
		os.Exit(1)
	}
}

// run dispatches the command line: a subcommand if one is named, a horoscope
// lookup otherwise.
//
// Called without arguments it prints usage and succeeds, so that a bare
// invocation is not an error.
func (a *app) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.out, usage())

		return nil
	}

	switch args[0] {
	case cmdLogin:
		return a.login(ctx, args[1:])
	case cmdLogout:
		return a.logout(ctx)
	case cmdDream:
		return a.dream(ctx, args[1:])
	default:
		return a.horoscope(ctx, args)
	}
}

// horoscope fetches the requested horoscope and writes it out. Invalid input
// returns an error describing the problem; an unreachable horoskopy.cz
// additionally writes an apology.
func (a *app) horoscope(ctx context.Context, args []string) error {
	sign, period, err := parseArgs(args)
	if err != nil {
		return fmt.Errorf("%w\n\n%s", err, usage())
	}

	horoscope, err := a.horoscopes.Fetch(ctx, sign, period)
	if err != nil {
		if errors.Is(err, horoskopy.ErrUnavailable) {
			fmt.Fprint(a.out, sorryMessage)
		}

		return err
	}

	fmt.Fprint(a.out, horoskopy.Render(horoscope))

	return nil
}

// dream sends a dream to the dream book and prints its interpretation. The
// dream is taken from the arguments, or read from standard input when none are
// given.
func (a *app) dream(ctx context.Context, args []string) error {
	dream, err := a.readDream(args)
	if err != nil {
		return err
	}

	session, err := a.sessions.Load()
	if err != nil {
		return loginHint(err)
	}

	interpretation, err := a.dreams.InterpretDream(ctx, session.Cookie, dream)
	if err != nil {
		if errors.Is(err, horoskopy.ErrUnavailable) {
			fmt.Fprint(a.out, sorryMessage)
		}

		return loginHint(err)
	}

	fmt.Fprint(a.out, horoskopy.RenderDream(interpretation))

	return nil
}

// readDream assembles the dream from the arguments, falling back to standard
// input so that a long dream can be piped in.
func (a *app) readDream(args []string) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}

	dream, err := io.ReadAll(a.in)
	if err != nil {
		return "", fmt.Errorf("could not read the dream: %w", err)
	}

	return string(dream), nil
}

// loginHint turns a missing or rejected session into an error that says what
// to do about it.
func loginHint(err error) error {
	switch {
	case errors.Is(err, seznam.ErrNoSession):
		return fmt.Errorf("%w: run `horoskopycli login` first", err)
	case errors.Is(err, horoskopy.ErrUnauthorized):
		return fmt.Errorf("%w: run `horoskopycli login` again", err)
	default:
		return err
	}
}

// loginArgs are the parsed options of the login command.
type loginArgs struct {
	opts  seznam.LoginOptions
	paste bool
}

// login signs in to Seznam and stores the session. Without flags it opens a
// browser; with --paste it takes a cookie the user copied out of one.
func (a *app) login(ctx context.Context, args []string) error {
	parsed, err := parseLoginArgs(args)
	if err != nil {
		return err
	}

	session, err := a.newSession(ctx, parsed)
	if err != nil {
		return err
	}

	account, err := a.accounts.Account(ctx, session.Cookie)
	if err != nil {
		return fmt.Errorf("the session does not work: %w", err)
	}
	session.Account = account

	if err := a.sessions.Save(session); err != nil {
		return err
	}

	path, err := a.sessions.Path()
	if err != nil {
		return err
	}

	fmt.Fprintf(a.out, "Logged in as %s%s.\nSession stored in %s\n", account, expiryNote(session.Expires), path)

	return nil
}

// newSession obtains a session either from a browser or from the terminal.
func (a *app) newSession(ctx context.Context, parsed loginArgs) (seznam.Session, error) {
	if parsed.paste {
		return a.pasteSession()
	}

	parsed.opts.Progress = a.out

	return a.browserLogin(ctx, parsed.opts)
}

// pasteSession asks for a cookie copied out of a browser, for people whose
// browser this CLI cannot drive.
func (a *app) pasteSession() (seznam.Session, error) {
	fmt.Fprintf(a.out, "Log in at https://www.horoskopy.cz, then copy the value of the `%s` cookie.\n",
		seznam.CookieName)
	fmt.Fprint(a.out, "Cookie: ")

	scanner := bufio.NewScanner(a.in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return seznam.Session{}, fmt.Errorf("could not read the cookie: %w", err)
		}

		return seznam.Session{}, errors.New("no cookie given")
	}

	return seznam.Session{Cookie: cleanCookie(scanner.Text())}, nil
}

// cleanCookie accepts both a bare cookie value and the `ds=value` form that
// browsers copy to the clipboard.
func cleanCookie(pasted string) string {
	pasted = strings.TrimSpace(pasted)
	pasted = strings.TrimSuffix(pasted, ";")

	return strings.TrimPrefix(pasted, seznam.CookieName+"=")
}

// logout revokes the session at Seznam and forgets it locally.
func (a *app) logout(ctx context.Context) error {
	session, err := a.sessions.Load()
	if errors.Is(err, seznam.ErrNoSession) {
		fmt.Fprintln(a.out, "Not logged in.")

		return nil
	}
	if err != nil {
		return err
	}

	revokeErr := a.accounts.Revoke(ctx, session.Cookie)

	if err := a.sessions.Forget(); err != nil {
		return err
	}

	if revokeErr != nil {
		fmt.Fprintf(a.out, "Session forgotten, but Seznam could not be told: %s\n", revokeErr)

		return nil
	}

	fmt.Fprintln(a.out, "Logged out, the session is no longer valid.")

	return nil
}

// parseLoginArgs reads the flags of the login command.
func parseLoginArgs(args []string) (loginArgs, error) {
	flags := flag.NewFlagSet(cmdLogin, flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	browser := flags.String("browser", "", "browser executable to run the login in")
	paste := flags.Bool("paste", false, "paste a cookie from another browser instead")
	timeout := flags.Duration("timeout", seznam.DefaultLoginTimeout, "how long to wait for the login")
	debug := flags.Bool("debug", false, "report what the browser is holding while waiting")

	if err := flags.Parse(args); err != nil {
		return loginArgs{}, fmt.Errorf("login: %w\n\n%s", err, usage())
	}

	return loginArgs{
		opts:  seznam.LoginOptions{Browser: *browser, Timeout: *timeout, Debug: *debug},
		paste: *paste,
	}, nil
}

// expiryNote renders the validity of a session for the login message, or
// nothing when the session does not say.
func expiryNote(expires time.Time) string {
	if expires.IsZero() {
		return ""
	}

	return ", valid until " + expires.Format(time.DateOnly)
}

// parseArgs reads the zodiac sign and the optional period from the command
// line. The period defaults to today when omitted.
func parseArgs(args []string) (horoskopy.Sign, horoskopy.Period, error) {
	if len(args) > 2 {
		return "", "", fmt.Errorf("%w: expected a sign and an optional period, got %d", errTooManyArguments, len(args))
	}

	sign, err := horoskopy.ParseSign(args[0])
	if err != nil {
		return "", "", err
	}

	if len(args) == 1 {
		return sign, horoskopy.DefaultPeriod, nil
	}

	period, err := horoskopy.ParsePeriod(args[1])
	if err != nil {
		return "", "", err
	}

	return sign, period, nil
}

// usage returns the help text listing the accepted signs, periods and commands.
func usage() string {
	return fmt.Sprintf(`Usage: horoskopycli <sign> [period]
       horoskopycli snar [dream]
       horoskopycli login [--paste] [--browser <path>] [--debug]
       horoskopycli logout

Signs:   %s
Periods: %s (default: %s)

Examples:
  horoskopycli byk
  horoskopycli ryby zitra
  horoskopycli lev rok
  horoskopycli snar "Zdálo se mi, že létám nad mořem."

The dream book needs a Seznam account: `+"`horoskopycli login`"+` opens a browser,
signs you in and keeps the session cookie in %s.
Set %s to pass the cookie in instead.
`,
		strings.Join(horoskopy.SignSlugs(), ", "),
		strings.Join(horoskopy.PeriodSlugs(), ", "),
		horoskopy.DefaultPeriod,
		sessionLocation(),
		seznam.EnvCookie,
	)
}

// sessionLocation names the session file for the usage text, falling back to a
// description when the configuration directory cannot be determined.
func sessionLocation() string {
	path, err := seznam.SessionPath()
	if err != nil {
		return "your configuration directory"
	}

	return path
}

// seznamStore is the real session store, backed by the file internal/seznam
// keeps in the user's configuration directory.
type seznamStore struct{}

// Load returns the stored session.
func (seznamStore) Load() (seznam.Session, error) {
	return seznam.Load()
}

// Save writes the session to disk.
func (seznamStore) Save(session seznam.Session) error {
	return session.Save()
}

// Forget removes the stored session.
func (seznamStore) Forget() error {
	return seznam.Forget()
}

// Path returns the file the session is kept in.
func (seznamStore) Path() (string, error) {
	return seznam.SessionPath()
}
