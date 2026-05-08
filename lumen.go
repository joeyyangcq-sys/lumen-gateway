package lumen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/joey/lumen-gateway/internal/bootstrap"
	"github.com/joey/lumen-gateway/internal/gateway"
	"github.com/joey/lumen-gateway/internal/provider"
	"github.com/urfave/cli/v2"
)

type options struct {
	version string
	build   string
	flags   []cli.Flag
	init    func(bootstrap.Options) error
}

type Option func(*options)

func WithVersion(version string) Option {
	return func(o *options) {
		o.version = version
	}
}

func WithFlags(flags ...cli.Flag) Option {
	return func(o *options) {
		o.flags = append(o.flags, flags...)
	}
}

func WithInit(fn func(bootstrap.Options) error) Option {
	return func(o *options) {
		o.init = fn
	}
}

func Run(opts ...Option) error {
	opt := &options{
		version: "0.0.0",
		build:   "unknown",
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				opt.build = setting.Value
				break
			}
		}
	}

	for _, apply := range opts {
		apply(opt)
	}

	cli.VersionPrinter = func(ctx *cli.Context) {
		fmt.Printf("version=%s\n", ctx.App.Version)
		fmt.Printf("build=%s\n", opt.build)
	}

	app := &cli.App{
		Version: opt.version,
		Flags: append([]cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Value:   "configs/bootstrap.yaml",
				Usage:   "Bootstrap config path (etcd endpoints, listen address, etc.)",
			},
			&cli.BoolFlag{
				Name:    "test",
				Aliases: []string{"t"},
				Value:   false,
				Usage:   "Test bootstrap config and then exit",
			},
		}, opt.flags...),
		Action: func(ctx *cli.Context) error {
			configPath := ctx.String("config")
			boot, err := bootstrap.Load(configPath)
			if err != nil {
				return err
			}

			if ctx.Bool("test") {
				fmt.Printf("bootstrap config tested successfully: %s\n", configPath)
				return nil
			}

			if opt.init != nil {
				if err := opt.init(boot); err != nil {
					return err
				}
			}

			source, err := provider.NewSource(boot)
			if err != nil {
				return err
			}
			defer source.Close()

			runCtx, cancel := context.WithCancel(context.Background())
			defer cancel()

			initial, err := source.Load(runCtx)
			if err != nil {
				return err
			}

			gw, err := gateway.New(initial)
			if err != nil {
				return err
			}

			errCh := make(chan error, 2)
			go func() {
				errCh <- gw.Run()
			}()
			go func() {
				errCh <- source.Watch(runCtx, func(next provider.Update) {
					if next.Err != nil {
						errCh <- next.Err
						return
					}
					if err := gw.Reload(next.Options); err != nil {
						errCh <- err
					}
				})
			}()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			defer signal.Stop(sigCh)

			select {
			case sig := <-sigCh:
				slog.Info("shutdown signal received", "signal", sig.String())
				cancel()
				return gw.Shutdown()
			case err := <-errCh:
				if errors.Is(err, context.Canceled) {
					return nil
				}
				cancel()
				_ = gw.Shutdown()
				return err
			}
		},
	}

	return app.Run(os.Args)
}
