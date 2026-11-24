// Command otogi runs the Ex-Otogi bot. See docs/runbooks for operations.
//
//	otogi [-config otogi.yaml] [run]        run until SIGINT or SIGTERM
//	otogi [-config otogi.yaml] validate     check the config file
//	otogi [-config otogi.yaml] login ACCOUNT  log a Telegram account in
//	otogi smoke                             offline smoke test
//	otogi openapi                           print the management API's OpenAPI document
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram"
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
	case "openapi":
		var doc []byte
		if doc, err = management.OpenAPI(); err == nil {
			_, err = fmt.Printf("%s\n", doc)
		}
	case "run", "validate", "login":
		err = withConfig(ctx, *configPath, command, args)
	default:
		err = fmt.Errorf("unknown command %q (want run, validate, login, smoke, or openapi)", command)
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
		fmt.Printf("%s: OK (%d Telegram account(s), %d QQ bot(s))\n", path, len(cfg.Telegram), len(cfg.QQ))
		return nil
	case "login":
		if len(args) != 1 {
			return errors.New("usage: otogi login ACCOUNT")
		}
		return login(ctx, cfg, args[0])
	default:
		level := new(slog.LevelVar)
		return run(ctx, cfg, newLogger(cfg.Log.Format, level), level)
	}
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

// newLogger returns the process log. Its level is the general settings',
// which set it when they are loaded and each time they are saved.
func newLogger(format string, level *slog.LevelVar) *slog.Logger {
	options := &slog.HandlerOptions{Level: level}
	if format == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, options))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, options))
}

func logLevel(name string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(name)); err != nil {
		return 0, fmt.Errorf("%q: want debug, info, warn, or error", name)
	}
	return level, nil
}
