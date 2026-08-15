package main

import (
	"testing"
	"time"
)

func TestLoadJobConfigDefaultsAndRejectsUnsafeTiming(t *testing.T) {
	for _, name := range []string{
		"COMPANION_RIVER_RETENTION_INTERVAL", "COMPANION_RIVER_JOB_TIMEOUT",
		"COMPANION_RIVER_SOFT_STOP_TIMEOUT", "COMPANION_RIVER_RESCUE_AFTER",
		"COMPANION_RIVER_RUN_ON_START",
	} {
		t.Setenv(name, "")
	}
	config, err := loadJobConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.RetentionInterval != 6*time.Hour || !config.RunOnStart || config.JobTimeout != 10*time.Minute || config.RescueAfter != 20*time.Minute {
		t.Fatalf("config=%+v", config)
	}
	t.Setenv("COMPANION_RIVER_JOB_TIMEOUT", "2m")
	t.Setenv("COMPANION_RIVER_RESCUE_AFTER", "1m")
	if _, err := loadJobConfig(); err == nil {
		t.Fatal("rescue-before-timeout configuration was accepted")
	}
}
