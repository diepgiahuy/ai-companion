//go:build !adk

package adkbridge

import "companion-server/internal/pipeline"

// New deliberately fails closed when ADK was not compiled into the binary.
// Product builds must include the adk tag; there is no alternate agent runtime.
func New(Config) (pipeline.Agent, error) {
	return nil, ErrNotBuilt
}

// NewProvider is the product composition entrypoint when provider protocol
// selection is enabled. A binary without the adk tag still fails closed.
func NewProvider(Config) (pipeline.Agent, error) {
	return nil, ErrNotBuilt
}

func Enabled() bool { return false }
