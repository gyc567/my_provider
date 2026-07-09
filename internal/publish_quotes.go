// Package internal holds provider-side business logic that interacts with the t-0 Network.
package internal

import (
	"context"
	"log"
	"time"

	"my-provider/internal/quote"
)

// PublishQuotes starts a background goroutine that periodically publishes
// the current quote snapshots from storage to the t-0 network.
func PublishQuotes(ctx context.Context, publisher *quote.Publisher) {
	const (
		publishInterval = 5 * time.Second
		minInterval     = 1 * time.Second
	)

	ticker := time.NewTicker(publishInterval)
	defer ticker.Stop()

	var lastAttempt time.Time

	publish := func() {
		if err := publisher.Publish(ctx); err != nil {
			log.Printf("Error publishing quotes: %s (will retry next tick)\n", err.Error())
			return
		}
		lastAttempt = time.Now()
	}

	// publish once at startup so we don't wait publishInterval before the first attempt
	publish()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if elapsed := time.Since(lastAttempt); elapsed < minInterval {
				continue
			}
			publish()
		}
	}
}
