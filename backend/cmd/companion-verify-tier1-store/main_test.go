package main

import (
	"strings"
	"testing"
)

func TestRunRequiresPostgresAndOutput(t *testing.T) {
	err := run(nil)
	if err == nil || !strings.Contains(err.Error(), "--postgres and --output are required") {
		t.Fatalf("argument error = %v", err)
	}
}
