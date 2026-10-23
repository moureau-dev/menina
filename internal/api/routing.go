package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/moureau-dev/menina/internal/edge"
)

// SubscribeRouting keeps an edge server updated from the control plane. It
// reconnects with a fixed delay until the context is cancelled.
func SubscribeRouting(ctx context.Context, socket, tokenPath string, log *slog.Logger, server *edge.Server) {
	for ctx.Err() == nil {
		client, err := Dial(socket, tokenPath)
		if err != nil {
			log.Warn("control plane unavailable; retrying", "error", err)
			sleepOrDone(ctx, 2*time.Second)
			continue
		}
		if snap, err := client.Snapshot(ctx); err != nil {
			log.Warn("fetch routing snapshot failed", "error", err)
		} else if err := server.UpdateSnapshot(snap); err != nil {
			log.Error("rejected routing snapshot", "error", err)
		} else {
			log.Info("loaded routing snapshot", "version", snap.Version, "routes", len(snap.Routes))
		}
		err = client.Stream(ctx, func(s *edge.Snapshot) {
			if err := server.UpdateSnapshot(s); err != nil {
				log.Error("rejected routing snapshot update", "error", err)
				return
			}
			log.Info("routing snapshot updated", "version", s.Version, "routes", len(s.Routes))
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Warn("routing stream ended; reconnecting", "error", err)
		}
		sleepOrDone(ctx, 2*time.Second)
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
