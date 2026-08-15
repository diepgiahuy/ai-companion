package main

import (
	"fmt"
	"time"

	"companion-server/internal/jobs"
)

func loadJobConfig() (jobs.Config, error) {
	retentionInterval, err := databaseDuration("COMPANION_RIVER_RETENTION_INTERVAL", 6*time.Hour)
	if err != nil {
		return jobs.Config{}, err
	}
	jobTimeout, err := databaseDuration("COMPANION_RIVER_JOB_TIMEOUT", 10*time.Minute)
	if err != nil {
		return jobs.Config{}, err
	}
	softStopTimeout, err := databaseDuration("COMPANION_RIVER_SOFT_STOP_TIMEOUT", 8*time.Second)
	if err != nil {
		return jobs.Config{}, err
	}
	rescueAfter, err := databaseDuration("COMPANION_RIVER_RESCUE_AFTER", 20*time.Minute)
	if err != nil {
		return jobs.Config{}, err
	}
	runOnStart, err := databaseBool("COMPANION_RIVER_RUN_ON_START", true)
	if err != nil {
		return jobs.Config{}, err
	}
	if rescueAfter < jobTimeout {
		return jobs.Config{}, fmt.Errorf("COMPANION_RIVER_RESCUE_AFTER must be >= COMPANION_RIVER_JOB_TIMEOUT")
	}
	return jobs.Config{
		RetentionInterval: retentionInterval,
		JobTimeout:        jobTimeout,
		SoftStopTimeout:   softStopTimeout,
		RescueAfter:       rescueAfter,
		RunOnStart:        runOnStart,
	}, nil
}
