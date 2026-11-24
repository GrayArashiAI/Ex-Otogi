package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/qq"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/modules/utility"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/TundraWork/Ex-Otogi/web"
)

// driver is the lifecycle every platform driver provides.
type driver interface {
	Start(ctx context.Context) error
	Drain(ctx context.Context) error
	Stop(ctx context.Context) error
	Done() <-chan struct{}
	Err() error
	Ready() bool
}

// run opens the event log, starts the management API, the kernel, and the
// drivers, waits for ctx to end (a signal or POST /api/shutdown) or a driver
// to stop, then shuts down within the configured timeout. It returns the
// first failure, or nil after a requested stop.
func run(ctx context.Context, cfg Config, log *slog.Logger, level *slog.LevelVar) (failure error) {
	ctx, requestShutdown := context.WithCancel(ctx)
	defer requestShutdown()
	started := time.Now()

	events, err := management.OpenEventLog(ctx, filepath.Join(cfg.DataDir, "eventlog.sqlite"), log)
	if err != nil {
		return err
	}
	defer func() {
		if err := events.Close(); err != nil && failure == nil {
			failure = err
		}
	}()
	process := func(state string, err error) {
		payload := map[string]string{"state": state}
		outcome := "ok"
		if err != nil {
			payload["error"], outcome = err.Error(), "error"
		}
		events.Record(context.Background(), otogi.Record{At: time.Now(), Kind: "process.state", Component: "process", Outcome: outcome, Payload: payload})
	}
	storage, err := modulestore.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := storage.Close(); err != nil && failure == nil {
			failure = err
		}
	}()
	// Modules get views of the runtime built below; status and commands are
	// only called once the process runs.
	var status func(context.Context) utility.Status
	var k *kernel.Kernel
	sys := newSystem(level, events, log)
	settings := &moduleSettings{store: storage, kinds: moduleKinds, recorder: events, log: log, system: sys, env: moduleEnv{
		operators: sys.operatorList,
		llm:       sys.provider,
		status:    func(ctx context.Context) utility.Status { return status(ctx) },
		commands:  func() []utility.CommandInfo { return commandsOf(k.Descriptors()) },
	}}
	if err := settings.loadSections(ctx, cfg); err != nil {
		return err
	}
	// The pruner starts once the retention is loaded.
	var background sync.WaitGroup
	pruneCtx, stopPruning := context.WithCancel(context.Background())
	background.Go(func() { events.RunPruner(pruneCtx, sys.retention) })
	defer func() {
		stopPruning()
		background.Wait()
	}()
	modules, err := settings.enabled(ctx)
	if err != nil {
		return err
	}
	routes := router{}
	kernelConfig := cfg.Kernel.kernel()
	kernelConfig.Recorder, kernelConfig.Storage = events, storage
	middleware := []otogi.Middleware{namesMiddleware{events: events, log: log}, kernel.CommandParser(&sys.prefixes)}
	k, err = kernel.New(kernelConfig, modules, middleware, routes, log)
	if err != nil {
		return err
	}
	settings.kernel = k
	var drivers []driver
	var statuses []func(context.Context) management.DriverStatus
	var telegramDrivers []*telegram.Driver
	for _, a := range cfg.Telegram {
		d := telegram.New(telegram.Config{
			AppID: a.AppID, AppHash: a.AppHash, AccountKey: a.Account,
			SessionPath: a.Session, DatabasePath: a.Database, UploadThreads: a.UploadThreads,
			Kernel: k, Logger: log, Recorder: events,
		})
		routes[destination{telegram.Platform, otogi.AccountID(a.Account)}] = d
		drivers = append(drivers, d)
		statuses = append(statuses, func(ctx context.Context) management.DriverStatus {
			s := d.Status(ctx)
			return management.DriverStatus{Platform: string(s.Platform), Account: s.Account, Ready: s.Ready, Error: s.Error,
				PendingInbox: s.PendingInbox, FailedInbox: s.FailedInbox, Gaps: s.Gaps, Details: s}
		})
		telegramDrivers = append(telegramDrivers, d)
	}
	var qqDrivers []*qq.Driver
	for _, a := range cfg.QQ {
		d := qq.New(qq.Config{
			AccountKey: a.Account, AppID: a.AppID, AppSecret: a.AppSecret, DatabasePath: a.Database,
			Kernel: k, Logger: log, Recorder: events,
		})
		routes[destination{qq.Platform, otogi.AccountID(a.Account)}] = d
		drivers = append(drivers, d)
		statuses = append(statuses, func(ctx context.Context) management.DriverStatus {
			s := d.Status(ctx)
			return management.DriverStatus{Platform: string(s.Platform), Account: s.Account, Ready: s.Ready, Error: s.Error,
				PendingInbox: s.PendingInbox, FailedInbox: s.FailedInbox, Gaps: s.Gaps, Details: s}
		})
		qqDrivers = append(qqDrivers, d)
	}
	status = func(ctx context.Context) utility.Status {
		stats, health := k.Stats(), events.Health(ctx)
		s := utility.Status{
			Version: version(), Uptime: time.Since(started), Ready: allReady(drivers),
			Kernel:   utility.KernelStatus{State: stats.State, InFlight: stats.InFlight, MaxInFlight: stats.MaxInFlight, Workers: stats.Workers},
			EventLog: utility.EventLogStatus{OK: health.OK, LastError: health.LastError},
		}
		for _, d := range telegramDrivers {
			ds := d.Status(ctx)
			s.Drivers = append(s.Drivers, utility.DriverStatus{
				Name: "telegram/" + ds.Account, Ready: ds.Ready, Error: ds.Error,
				Detail: fmt.Sprintf("%d pending, %d failed inbox rows, %d gaps", ds.PendingInbox, ds.FailedInbox, ds.Gaps),
			})
		}
		for _, d := range qqDrivers {
			ds := d.Status(ctx)
			s.Drivers = append(s.Drivers, utility.DriverStatus{
				Name: "qq/" + ds.Account, Ready: ds.Ready, Error: ds.Error,
				Detail: fmt.Sprintf("%d pending, %d failed inbox rows, %d gaps", ds.PendingInbox, ds.FailedInbox, ds.Gaps),
			})
		}
		return s
	}

	if addr := *cfg.Management.Addr; addr != "" {
		var key []byte
		if cfg.Management.PublicURL != "" {
			if key, err = management.LoadCookieKey(filepath.Join(cfg.DataDir, "panel.key")); err != nil {
				return err
			}
		}
		o := cfg.Management.OIDC
		server, err := management.StartServer(management.ServerConfig{
			PublicURL: cfg.Management.PublicURL, CookieKey: key, Panel: web.Panel(),
			OIDC: management.OIDC{Issuer: o.Issuer, ClientID: o.ClientID, ClientSecret: o.ClientSecret, Allow: o.Allow},
			Addr: addr, Token: cfg.Management.Token, Events: events, Logger: log,
			Shutdown: requestShutdown, Modules: newModuleAPI(settings), System: newSystemAPI(settings), Secrets: newModuleAPI(settings), Media: routes.FetchMedia, Version: version(),
			Ready: func() bool { return allReady(drivers) },
			Running: func() []management.Running {
				out := []management.Running{}
				for _, r := range k.Running() {
					out = append(out, management.Running{
						Module: string(r.Module), Origin: r.Origin, Event: string(r.Event),
						Platform: string(r.Platform), Account: string(r.Account), Conversation: string(r.Conversation), Started: r.Started,
					})
				}
				return out
			},
			Status: func(ctx context.Context) management.Status {
				stats := k.Stats()
				s := management.Status{
					Version: version(), StartedAt: started, Ready: allReady(drivers), Drivers: []management.DriverStatus{},
					Kernel:   management.KernelStatus{State: stats.State, InFlight: stats.InFlight, MaxInFlight: stats.MaxInFlight, Workers: stats.Workers},
					EventLog: management.EventLogStatus{Health: events.Health(ctx), RetentionMS: sys.retention().Milliseconds()},
				}
				for _, status := range statuses {
					d := status(ctx)
					d.LastEvent = events.LastReceived(ctx, d.Platform, d.Account)
					s.Drivers = append(s.Drivers, d)
				}
				return s
			},
		})
		if err != nil {
			return err
		}
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Stop(stopCtx); err != nil && failure == nil {
				failure = err
			}
		}()
	}

	if err := k.Start(ctx); err != nil {
		return err
	}
	var running []driver
	for _, d := range drivers {
		if err := d.Start(ctx); err != nil {
			failure = err
			break
		}
		running = append(running, d)
	}
	if failure == nil {
		log.Info("otogi: running", "modules", len(modules), "drivers", len(running))
		process("running", nil)
		failure = waitForStop(ctx, running)
	}
	if ctx.Err() != nil && errors.Is(failure, ctx.Err()) {
		failure = nil // a stop was requested during startup
	}
	process("stopping", failure)
	if err := shutdown(cfg, k, running, log); err != nil && failure == nil {
		failure = err
	}
	process("stopped", failure)
	return failure
}

func allReady(drivers []driver) bool {
	for _, d := range drivers {
		if !d.Ready() {
			return false
		}
	}
	return true
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := info.Main.Version
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			v += " " + s.Value
		}
	}
	return v
}

// waitForStop returns nil when ctx ends, or a driver's error when one stops.
func waitForStop(ctx context.Context, drivers []driver) error {
	stopped := make(chan driver, len(drivers))
	for _, d := range drivers {
		go func() {
			select {
			case <-d.Done():
				stopped <- d
			case <-ctx.Done():
			}
		}()
	}
	select {
	case <-ctx.Done():
		return nil
	case d := <-stopped:
		if err := d.Err(); err != nil {
			return err
		}
		return errors.New("a driver stopped unexpectedly")
	}
}

// shutdown drains drivers (no new dispatch), lets the kernel finish in-flight
// events, then stops drivers and the kernel, all within ShutdownTimeout.
func shutdown(cfg Config, k *kernel.Kernel, drivers []driver, log *slog.Logger) error {
	log.Info("otogi: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	var errs []error
	for _, d := range drivers {
		errs = append(errs, d.Drain(ctx))
	}
	if err := k.Drain(ctx); err != nil {
		errs = append(errs, fmt.Errorf("drain kernel: %w", err))
	}
	for _, d := range drivers {
		errs = append(errs, d.Stop(ctx))
	}
	errs = append(errs, k.Stop(ctx))
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Info("otogi: stopped")
	return nil
}
