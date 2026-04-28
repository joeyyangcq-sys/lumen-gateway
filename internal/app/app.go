package app

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joey/lumen-gateway/internal/config"
	"github.com/joey/lumen-gateway/internal/gateway"
)

const defaultConfigPath = "configs/lumen.yaml"

func Run(args []string) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	configPath := flags.String("config", defaultConfigPath, "path to config file")
	testConfig := flags.Bool("test", false, "test config and exit")

	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	options, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	if *testConfig {
		fmt.Printf("config tested successfully: %s\n", *configPath)
		return nil
	}

	gw, err := gateway.New(options)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- gw.Run()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		slog.Info("shutdown signal received", "signal", sig.String())
		return gw.Shutdown()
	case err := <-errCh:
		return err
	}
}
