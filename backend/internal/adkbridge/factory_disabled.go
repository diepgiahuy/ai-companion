//go:build !adk

package adkbridge

import "companion-server/internal/pipeline"

// New deliberately fails closed when ADK was not compiled into the binary.
// The caller can still select the legacy runtime explicitly.
func New(Config) (pipeline.Agent, error) {
	return nil, ErrNotBuilt
}

func Enabled() bool { return false }
