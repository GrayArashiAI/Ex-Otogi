// Command otogi runs the Ex-Otogi bot. See docs/runbooks for operations.
//
//	otogi [-config otogi.yaml] [run]        run until SIGINT or SIGTERM
//	otogi [-config otogi.yaml] validate     check the config file
//	otogi [-config otogi.yaml] login ACCOUNT  log a Telegram account in
//	otogi [-config otogi.yaml] passkey enroll  print a link that registers a panel passkey
//	otogi smoke                             offline smoke test
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management/panel"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func main() { os.Exit(mainExit()) }

func mainExit() int {
	flags := flag.NewFlagSet("otogi", flag.ContinueOnError)
	configPath := flags.String("config", "otogi.yaml", "path to the config file")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return 2
	}
	command, args := "run", flags.Args()
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop() // restore default handling: a second signal ends the process
	}()

	var err error
	switch command {
	case "smoke":
		err = smoke(ctx, os.Stdout)
	case "run", "validate", "login", "passkey":
		err = withConfig(ctx, *configPath, command, args)
	default:
		err = fmt.Errorf("unknown command %q (want run, validate, login, passkey, or smoke)", command)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "otogi:", err)
		return 1
	}
	return 0
}

func withConfig(ctx context.Context, path, command string, args []string) error {
	cfg, err := loadConfig(path)
	if err != nil {
		return err
	}
	switch command {
	case "validate":
		providers, err := buildProviders(cfg.LLM, otogi.NopRecorder{}, slog.New(slog.DiscardHandler))
		if err != nil {
			return err
		}
		fmt.Printf("%s: OK (%d Telegram account(s), %d QQ bot(s), %d LLM provider(s))\n", path, len(cfg.Telegram), len(cfg.QQ), len(providers))
		return nil
	case "login":
		if len(args) != 1 {
			return errors.New("usage: otogi login ACCOUNT")
		}
		return login(ctx, cfg, args[0])
	case "passkey":
		if len(args) != 1 || args[0] != "enroll" {
			return errors.New("usage: otogi passkey enroll")
		}
		return enroll(ctx, cfg, os.Stdout)
	default:
		log, err := newLogger(cfg.Log)
		if err != nil {
			return err
		}
		return run(ctx, cfg, log)
	}
}

// enroll prints a one-time link that registers a passkey for the panel,
// valid for 10 minutes. Run it as the service's user: it writes the panel's
// database.
func enroll(ctx context.Context, cfg Config, out io.Writer) error {
	if cfg.Management.PublicURL == "" {
		return errors.New("passkey enroll: set management.public_url, where the panel is served")
	}
	store, err := panel.Open(ctx, filepath.Join(cfg.DataDir, "panel.sqlite"))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	token, err := store.NewEnrollment(ctx, 10*time.Minute)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Open this link within 10 minutes to register a passkey; it works once:\n%s/enroll#%s\n", cfg.Management.PublicURL, token)
	return err
}

func login(ctx context.Context, cfg Config, account string) error {
	for _, a := range cfg.Telegram {
		if a.Account == account {
			who, err := telegram.Login(ctx, telegram.SessionConfig{AppID: a.AppID, AppHash: a.AppHash, SessionPath: a.Session}, newTerminal())
			if err != nil {
				return err
			}
			fmt.Printf("Logged in %s as %s\n", account, who)
			return nil
		}
	}
	return fmt.Errorf("no Telegram account %q in the config", account)
}

func newLogger(cfg LogConfig) (*slog.Logger, error) {
	level, err := logLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	options := &slog.HandlerOptions{Level: level}
	if cfg.Format == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, options)), nil
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, options)), nil
}

func logLevel(name string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(name)); err != nil {
		return 0, fmt.Errorf("log.level %q: want debug, info, warn, or error", name)
	}
	return level, nil
}
