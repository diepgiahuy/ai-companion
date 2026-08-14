package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"companion-server/internal/pipeline"
)

type namedCloser struct {
	name   string
	closer io.Closer
}

func providerComponentClosers(components pipeline.Components) []namedCloser {
	candidates := []struct {
		name  string
		value any
	}{
		{name: "asr", value: components.ASR},
		{name: "agent", value: components.Agent},
		{name: "tts", value: components.TTS},
	}
	out := make([]namedCloser, 0, len(candidates))
	for _, candidate := range candidates {
		if closer, ok := candidate.value.(io.Closer); ok && closer != nil {
			out = append(out, namedCloser{name: candidate.name, closer: closer})
		}
	}
	return out
}

func closeRuntimeResources(ctx context.Context, logger *slog.Logger, resources ...namedCloser) error {
	if logger == nil {
		logger = slog.Default()
	}
	for _, resource := range resources {
		if resource.closer == nil {
			continue
		}
		done := make(chan error, 1)
		go func(c io.Closer) { done <- c.Close() }(resource.closer)
		select {
		case err := <-done:
			if err != nil {
				logger.Warn("runtime resource close failed", "resource", resource.name, "error", err)
				return fmt.Errorf("close %s: %w", resource.name, err)
			}
		case <-ctx.Done():
			logger.Warn("runtime resource close exceeded shutdown bound", "resource", resource.name)
			return fmt.Errorf("close %s: %w", resource.name, ctx.Err())
		}
	}
	return nil
}

func closeRuntimeResourcesBounded(logger *slog.Logger, timeout time.Duration, resources ...namedCloser) error {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return closeRuntimeResources(ctx, logger, resources...)
}
