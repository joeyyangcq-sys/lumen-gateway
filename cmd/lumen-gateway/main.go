package main

import (
	"log/slog"
	"os"

	lumen "github.com/joey/lumen-gateway"
)

func main() {
	if err := lumen.Run(); err != nil {
		slog.Error("failed to run lumen gateway", "error", err)
		os.Exit(1)
	}
}
