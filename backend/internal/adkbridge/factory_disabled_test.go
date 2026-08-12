//go:build !adk

package adkbridge

import (
	"errors"
	"testing"
)

func TestDisabledBuildFailsClosed(t *testing.T) {
	if Enabled() {
		t.Fatal("ADK must report disabled without the adk build tag")
	}
	agent, err := New(Config{})
	if agent != nil || !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("New()=(%v, %v), want nil, ErrNotBuilt", agent, err)
	}
}
