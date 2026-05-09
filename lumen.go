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
	"time"

	"github.com/joey/lumen-gateway/internal/adminapi"
	"github.com/joey/lumen-gateway/internal/bootstrap"
	"github.com/joey/lumen-gateway/internal/controlplane"
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
		Commands: []*cli.Command{
			adminCommand(),
		},
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

			adminHandler, err := adminapi.New(boot)
			if err != nil {
				return err
			}
			if adminHandler != nil {
				defer adminHandler.Close()
			}

			runCtx, cancel := context.WithCancel(context.Background())
			defer cancel()

			initial, err := source.Load(runCtx)
			if err != nil {
				return err
			}

			gw, err := gateway.New(initial, adminHandler)
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
						slog.Warn("skip invalid control-plane update", "error", next.Err)
						return
					}
					if err := gw.Reload(next.Options); err != nil {
						slog.Warn("skip invalid gateway reload", "error", err)
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

func adminCommand() *cli.Command {
	return &cli.Command{
		Name:  "admin",
		Usage: "Control-plane file and resource operations",
		Subcommands: []*cli.Command{
			{
				Name:  "import",
				Usage: "Import an APISIX-style bundle file into etcd",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Required: true, Usage: "Bundle file path"},
					&cli.BoolFlag{Name: "prune", Usage: "Delete managed resources missing from the bundle for included kinds"},
				},
				Action: func(ctx *cli.Context) error {
					boot, svc, err := loadControlPlaneService(ctx.String("config"))
					if err != nil {
						return err
					}
					defer svc.Close()
					if boot.Gateway.Source != "etcd_apisix" {
						return errors.New("admin import requires gateway.source=etcd_apisix")
					}
					bundle, err := controlplane.LoadBundleFile(ctx.String("file"))
					if err != nil {
						return err
					}
					result, err := controlplane.ApplyBundleWithOptions(ctx.Context, svc, bundle, controlplane.ApplyOptions{
						Prune: ctx.Bool("prune"),
					})
					if err != nil {
						return err
					}
					fmt.Printf("imported bundle from %s\n", ctx.String("file"))
					printApplyResult(result)
					return nil
				},
			},
			{
				Name:  "export",
				Usage: "Export current APISIX resources from etcd to a bundle file",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "out", Aliases: []string{"o"}, Required: true, Usage: "Output bundle file path"},
				},
				Action: func(ctx *cli.Context) error {
					boot, svc, err := loadControlPlaneService(ctx.String("config"))
					if err != nil {
						return err
					}
					defer svc.Close()
					if boot.Gateway.Source != "etcd_apisix" {
						return errors.New("admin export requires gateway.source=etcd_apisix")
					}
					bundle, err := controlplane.ExportBundle(ctx.Context, svc)
					if err != nil {
						return err
					}
					if err := controlplane.WriteBundleFile(ctx.String("out"), bundle); err != nil {
						return err
					}
					fmt.Printf("exported bundle to %s\n", ctx.String("out"))
					return nil
				},
			},
			{
				Name:  "sync",
				Usage: "Apply a bundle file once or keep syncing it into etcd",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Required: true, Usage: "Bundle file path"},
					&cli.BoolFlag{Name: "watch", Usage: "Keep polling the file and reapply on change"},
					&cli.BoolFlag{Name: "prune", Usage: "Delete managed resources missing from the bundle for included kinds"},
					&cli.DurationFlag{Name: "interval", Value: time.Second, Usage: "Poll interval when --watch is enabled"},
				},
				Action: func(ctx *cli.Context) error {
					boot, svc, err := loadControlPlaneService(ctx.String("config"))
					if err != nil {
						return err
					}
					defer svc.Close()
					if boot.Gateway.Source != "etcd_apisix" {
						return errors.New("admin sync requires gateway.source=etcd_apisix")
					}
					if !ctx.Bool("watch") {
						bundle, err := controlplane.LoadBundleFile(ctx.String("file"))
						if err != nil {
							return err
						}
						result, err := controlplane.ApplyBundleWithOptions(ctx.Context, svc, bundle, controlplane.ApplyOptions{
							Prune: ctx.Bool("prune"),
						})
						if err != nil {
							return err
						}
						fmt.Printf("synced bundle from %s\n", ctx.String("file"))
						printApplyResult(result)
						return nil
					}

					runCtx, cancel := signal.NotifyContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
					defer cancel()
					fmt.Printf("watching bundle %s\n", ctx.String("file"))
					return controlplane.SyncBundleFile(runCtx, svc, ctx.String("file"), controlplane.SyncOptions{
						PollInterval: ctx.Duration("interval"),
						Prune:        ctx.Bool("prune"),
						OnApply:      printApplyResult,
					})
				},
			},
		},
	}
}

func loadControlPlaneService(configPath string) (bootstrap.Options, *controlplane.Service, error) {
	boot, err := bootstrap.Load(configPath)
	if err != nil {
		return bootstrap.Options{}, nil, err
	}
	store, err := controlplane.NewEtcdStore(boot)
	if err != nil {
		return bootstrap.Options{}, nil, err
	}
	return boot, controlplane.New(store), nil
}

func printApplyResult(result controlplane.ApplyResult) {
	for _, kind := range controlplane.SupportedKinds() {
		if count := result.Counts[kind]; count > 0 {
			fmt.Printf("%s=%d\n", kind, count)
		}
	}
}
