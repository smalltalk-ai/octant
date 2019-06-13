package event

import (
	"bytes"
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/heptio/developer-dash/internal/clustereye"
	"github.com/heptio/developer-dash/internal/log"
)

//go:generate mockgen -destination=./fake/mock_streamer.go -package=fake github.com/heptio/developer-dash/internal/event Streamer

const (
	// DefaultScheduleDelay is how long events should delay before generating.
	DefaultScheduleDelay = 5 * time.Second
)

type Streamer interface {
	Stream(ctx context.Context, ch <-chan clustereye.Event)
}

func Stream(ctx context.Context, streamer Streamer, generators []clustereye.Generator, requestPath, contentPath string) error {
	if streamer == nil {
		return errors.New("unable to stream because streamer is nil")
	}

	logger := log.From(ctx).With("component", "event-stream")

	var generatorNames []string
	for _, generator := range generators {
		generatorNames = append(generatorNames, generator.Name())
	}

	logger.With("generator-names", generatorNames).Debugf("preparing to stream generators")

	// setup generators
	eventCh := make(chan clustereye.Event, 1)

	var wg sync.WaitGroup
	for _, generator := range generators {
		wg.Add(1)
		go func(g clustereye.Generator) {
			runGenerator(ctx, eventCh, g, requestPath, contentPath)
			wg.Done()
		}(generator)
	}

	streamer.Stream(ctx, eventCh)
	wg.Wait()

	logger.Debugf("shutting down stream")
	close(eventCh)

	return nil
}

func runGenerator(ctx context.Context, ch chan<- clustereye.Event, generator clustereye.Generator, requestPath, contentPath string) {
	logger := log.From(ctx)

	timer := time.NewTimer(0)
	isRunning := true

	eventCache := make(map[clustereye.EventType][]byte)

	for isRunning {
		select {
		case <-ctx.Done():
			isRunning = false
			logger.
				With("generator", generator.Name()).
				Debugf("generator shutting down")
		case <-timer.C:
			now := time.Now()

			event, err := generator.Event(ctx)
			if err != nil {
				if nfe, ok := err.(notFound); ok && nfe.NotFound() {
					logger.With(
						"path", contentPath,
						"requestPath", requestPath,
					).Errorf("content not found")
					isRunning = false

					ch <- clustereye.Event{
						Type: clustereye.EventTypeObjectNotFound,
						Data: []byte(notFoundRedirectPath(requestPath)),
					}
					break
				}

				// This could be one time error, or it could be a huge failure.
				// Either way, log, and move on. If this becomes a problem,
				// a circuit breaker or some other pattern could be employed here.
				logger.
					WithErr(err).
					Errorf("event generator error")

			} else {
				previous, ok := eventCache[event.Type]

				if !ok || !bytes.Equal(previous, event.Data) {
					logger.With(
						"elapsed", time.Since(now),
						"generator", generator.Name(),
						"contentPath", contentPath,
					).Debugf("event generated")
					ch <- event

					eventCache[event.Type] = event.Data
				}

			}

			nextTick := generator.ScheduleDelay()
			if nextTick == 0 {
				isRunning = false
			} else {
				timer.Reset(nextTick)
			}
		}
	}
}

func notFoundRedirectPath(requestPath string) string {
	parts := strings.Split(requestPath, "/")
	if len(parts) < 5 {
		return ""
	}
	return path.Join(append([]string{"/"}, parts[3:len(parts)-2]...)...)
}
