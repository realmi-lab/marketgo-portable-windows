package main

import (
	"context"
	"log/slog"
	"net"
	"time"
)

func openBrowserWhenReady(ctx context.Context, address, target string, logger *slog.Logger) {
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			if err := openBrowser(target); err != nil && logger != nil {
				logger.Warn("browser could not be opened automatically", "error", err, "url", target)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			if logger != nil {
				logger.Warn("browser open timed out waiting for local server", "address", address)
			}
			return
		case <-ticker.C:
		}
	}
}
