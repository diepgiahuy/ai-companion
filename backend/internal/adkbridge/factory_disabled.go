//go:build !adk

package adkbridge

import "companion-server/internal/pipeline"

func New(Config) (pipeline.Agent, error) { return nil, ErrNotBuilt }
func NewProvider(Config) (pipeline.Agent, error) { return nil, ErrNotBuilt }
func Enabled() bool { return false }
