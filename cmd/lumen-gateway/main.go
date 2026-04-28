package main

import (
	"log/slog"
	"os"

	"github.com/joey/lumen-gateway/internal/app"
)

func main() {
	if err := app.Run(os.Args); err != nil {
		slog.Error("failed to run lumen gateway", "error", err)
		os.Exit(1)
	}
}
