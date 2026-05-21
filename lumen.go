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

	publicbalancer "github.com/joey/lumen-gateway/balancer"
	"github.com/joey/lumen-gateway/internal/adminapi"
	internalbalancer "github.com/joey/lumen-gateway/internal/balancer"
	"github.com/joey/lumen-gateway/internal/balancer/roundrobin"
	"github.com/joey/lumen-gateway/internal/bootstrap"
	"github.com/joey/lumen-gateway/internal/config"
	"github.com/joey/lumen-gateway/internal/controlplane"
	"github.com/joey/lumen-gateway/internal/gateway"
	internallogging "github.com/joey/lumen-gateway/internal/logging"
	internalplugin "github.com/joey/lumen-gateway/internal/plugin"
	"github.com/joey/lumen-gateway/internal/plugin/builtin"
	"github.com/joey/lumen-gateway/internal/provider"
	publicplugin "github.com/joey/lumen-gateway/plugin"
	"github.com/urfave/cli/v2"
)

const defaultShutdownTimeout = 5 * time.Second

// WithPlugins adds one or more plugin registrars that run after the built-in
// plugins are loaded. Each registrar receives the shared *plugin.Registry and
// calls plugin.RegisterTypedContext (or r.Register) to add custom plugins.
//
// Multiple calls to WithPlugins are cumulative — all registrars run in order.
//
// Example:
//
//	lumen.Run(lumen.WithPlugins(myplugins.Register))
//
// where myplugins.Register has the signature:
//
//	func Register(r *plugin.Registry) error
func WithPlugins(registrars ...func(*publicplugin.Registry) error) Option {
	return func(o *options) {
		o.pluginRegs = append(o.pluginRegs, registrars...)
	}
}

// WithBalancerType registers a custom load-balancer type identified by kind.
// The kind string must match the balancer.type field in the upstream config.
// Multiple calls with different kinds are cumulative.
//
// The built-in "round_robin" type is always available as a fallback.
//
// Example:
//
//	lumen.Run(
//	    lumen.WithBalancerType("least_conn", leastconn.Factory),
//	)
//
// Factory signature:
//
//	func(endpoints []balancer.Endpoint, params any) (balancer.Balancer, error)
func WithBalancerType(kind string, factory func([]publicbalancer.Endpoint, any) (publicbalancer.Balancer, error)) Option {
	return func(o *options) {
		if o.balancerTypes == nil {
			o.balancerTypes = make(map[string]func([]internalbalancer.Endpoint, any) (internalbalancer.Balancer, error))
		}
		o.balancerTypes[kind] = factory
	}
}

// WithCompilerOptions passes low-level gateway.CompilerOption values directly
// to the runtime compiler. Prefer WithPlugins and WithBalancerType for common
// use-cases; use this only when you need to replace the proxy factory or
// supply a fully custom registry factory.
func WithCompilerOptions(opts ...gateway.CompilerOption) Option {
	return func(o *options) {
		o.compilerOpts = append(o.compilerOpts, opts...)
	}
}

// buildCompilerOpts converts the accumulated plugin registrars and balancer
// type registrations into gateway.CompilerOption values, appended to any
// explicitly provided WithCompilerOptions.
func (o *options) buildCompilerOpts() []gateway.CompilerOption {
	var extra []gateway.CompilerOption

	if len(o.pluginRegs) > 0 {
		regs := o.pluginRegs
		extra = append(extra, gateway.WithRegistryFactory(func() (*internalplugin.Registry, error) {
			r := internalplugin.NewRegistry()
			if err := builtin.Register(r); err != nil {
				return nil, err
			}
			for _, reg := range regs {
				if err := reg(r); err != nil {
					return nil, fmt.Errorf("plugin registrar failed: %w", err)
				}
			}
			return r, nil
		}))
	}

	if len(o.balancerTypes) > 0 {
		custom := o.balancerTypes
		extra = append(extra, gateway.WithBalancerFactory(func(opts config.UpstreamOptions, endpoints []internalbalancer.Endpoint) (internalbalancer.Balancer, error) {
			if factory, ok := custom[opts.Balancer.Type]; ok {
				return factory(endpoints, opts.Balancer.Params)
			}
			switch opts.Balancer.Type {
			case "", "round_robin":
				return roundrobin.New(endpoints, opts.Balancer.Params)
			default:
				return nil, fmt.Errorf("balancer type %q is not supported", opts.Balancer.Type)
			}
		}))
	}

	return append(extra, o.compilerOpts...)
}

type options struct {
	version       string
	build         string
	flags         []cli.Flag
	init          func(bootstrap.Options) error
	compilerOpts  []gateway.CompilerOption
	pluginRegs    []func(*internalplugin.Registry) error
	balancerTypes map[string]func([]internalbalancer.Endpoint, any) (internalbalancer.Balancer, error)
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
			if err := internallogging.Configure(initial.Logging); err != nil {
				return err
			}
			slog.Info("loaded gateway configuration",
				"config", configPath,
				"source", boot.Gateway.Source,
				"servers", len(initial.Servers),
				"routes", len(initial.Routes),
				"services", len(initial.Services),
				"upstreams", len(initial.Upstreams),
			)

			compiler := gateway.NewRuntimeCompiler(opt.buildCompilerOpts()...)
			if adminHandler != nil {
				if registry, regErr := compiler.BuildRegistry(); regErr == nil {
					defs := registry.Definitions()
					catalog := make([]adminapi.PluginCatalogEntry, 0, len(defs))
					for _, def := range defs {
						scopes := make([]string, len(def.Metadata().Scopes))
						for i, s := range def.Metadata().Scopes {
							scopes[i] = string(s)
						}
						catalog = append(catalog, adminapi.PluginCatalogEntry{
							Name:   def.Metadata().Name,
							Scopes: scopes,
						})
					}
					adminHandler.SetPluginCatalog(catalog)
				} else {
					slog.Warn("failed to build plugin registry for catalog", "error", regErr)
				}
			}

			gw, err := gateway.NewWithCompiler(initial, compiler, adminHandler)
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
						return
					}
					if err := internallogging.Configure(next.Options.Logging); err != nil {
						slog.Warn("skip invalid logging configuration", "error", err)
						return
					}
					slog.Info("reloaded gateway configuration",
						"routes", len(next.Options.Routes),
						"services", len(next.Options.Services),
						"upstreams", len(next.Options.Upstreams),
					)
				})
			}()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			defer signal.Stop(sigCh)

			select {
			case sig := <-sigCh:
				slog.Info("shutdown signal received", "signal", sig.String())
				cancel()
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
				defer shutdownCancel()
				return gw.ShutdownContext(shutdownCtx)
			case err := <-errCh:
				if errors.Is(err, context.Canceled) {
					return nil
				}
				cancel()
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
				_ = gw.ShutdownContext(shutdownCtx)
				shutdownCancel()
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
					&cli.StringSliceFlag{Name: "prune-kind", Usage: "Restrict prune to specific resource kinds (routes, services, upstreams, plugin_configs, global_rules)"},
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
					pruneKinds, err := parseKinds(ctx.StringSlice("prune-kind"))
					if err != nil {
						return err
					}
					result, err := controlplane.ApplyBundleWithOptions(ctx.Context, svc, bundle, controlplane.ApplyOptions{
						Prune:      ctx.Bool("prune"),
						PruneKinds: pruneKinds,
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
					&cli.StringSliceFlag{Name: "kind", Usage: "Export only specific resource kinds (routes, services, upstreams, plugin_configs, global_rules)"},
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
					kinds, err := parseKinds(ctx.StringSlice("kind"))
					if err != nil {
						return err
					}
					bundle, err := controlplane.ExportBundleWithOptions(ctx.Context, svc, controlplane.ExportOptions{
						EtcdPrefix:   boot.Etcd.Prefix,
						IncludeKinds: kinds,
						IncludeMeta:  true,
					})
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
					&cli.StringSliceFlag{Name: "prune-kind", Usage: "Restrict prune to specific resource kinds (routes, services, upstreams, plugin_configs, global_rules)"},
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
						pruneKinds, err := parseKinds(ctx.StringSlice("prune-kind"))
						if err != nil {
							return err
						}
						bundle, err := controlplane.LoadBundleFile(ctx.String("file"))
						if err != nil {
							return err
						}
						result, err := controlplane.ApplyBundleWithOptions(ctx.Context, svc, bundle, controlplane.ApplyOptions{
							Prune:      ctx.Bool("prune"),
							PruneKinds: pruneKinds,
						})
						if err != nil {
							return err
						}
						fmt.Printf("synced bundle from %s\n", ctx.String("file"))
						printApplyResult(result)
						return nil
					}

					pruneKinds, err := parseKinds(ctx.StringSlice("prune-kind"))
					if err != nil {
						return err
					}
					runCtx, cancel := signal.NotifyContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
					defer cancel()
					fmt.Printf("watching bundle %s\n", ctx.String("file"))
					return controlplane.SyncBundleFile(runCtx, svc, ctx.String("file"), controlplane.SyncOptions{
						PollInterval: ctx.Duration("interval"),
						Prune:        ctx.Bool("prune"),
						PruneKinds:   pruneKinds,
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

func parseKinds(values []string) ([]controlplane.ResourceKind, error) {
	if len(values) == 0 {
		return nil, nil
	}
	kinds := make([]controlplane.ResourceKind, 0, len(values))
	for _, value := range values {
		kind, ok := controlplane.ParseKind(value)
		if !ok {
			return nil, fmt.Errorf("unsupported resource kind: %s", value)
		}
		kinds = append(kinds, kind)
	}
	return kinds, nil
}
