package config

import (
	"fmt"
	"strings"
	"time"
)

const (
	defaultWorkerConcurrency             = 8
	defaultWorkerScanInterval            = time.Second
	defaultWorkerLeaseDuration           = 30 * time.Second
	defaultWorkerRenewInterval           = 10 * time.Second
	defaultWorkerNormalPollInterval      = 5 * time.Second
	defaultWorkerBackoffMin              = 2 * time.Second
	defaultWorkerBackoffMax              = 2 * time.Minute
	defaultWorkerDrainTimeout            = 20 * time.Second
	defaultWorkerIntegrationSyncInterval = 15 * time.Second
	defaultWorkerLegacyPollInterval      = 10 * time.Second
)

// WorkerConfig is the YAML/environment representation of the background
// worker settings. Durations remain strings here so malformed input can be
// rejected during startup instead of being silently rounded or defaulted by a
// consumer.
type WorkerConfig struct {
	Enabled                 bool   `yaml:"enabled"`
	Concurrency             int    `yaml:"concurrency"`
	ClaimBatchSize          int    `yaml:"claim_batch_size"`
	ScanInterval            string `yaml:"scan_interval"`
	LeaseDuration           string `yaml:"lease_duration"`
	RenewInterval           string `yaml:"renew_interval"`
	NormalPollInterval      string `yaml:"normal_poll_interval"`
	BackoffMin              string `yaml:"backoff_min"`
	BackoffMax              string `yaml:"backoff_max"`
	DrainTimeout            string `yaml:"drain_timeout"`
	IntegrationSyncInterval string `yaml:"integration_sync_interval"`
	LegacyPollInterval      string `yaml:"legacy_poll_interval"`
}

// WorkerRuntimeConfig contains only normalized and validated values. Callers
// should pass this snapshot into long-lived components rather than repeatedly
// consulting the mutable package global.
type WorkerRuntimeConfig struct {
	Enabled                 bool
	Concurrency             int
	ClaimBatchSize          int
	ScanInterval            time.Duration
	LeaseDuration           time.Duration
	RenewInterval           time.Duration
	NormalPollInterval      time.Duration
	BackoffMin              time.Duration
	BackoffMax              time.Duration
	DrainTimeout            time.Duration
	IntegrationSyncInterval time.Duration
	LegacyPollInterval      time.Duration
}

func applyWorkerEnvironmentOverrides(worker *WorkerConfig) error {
	if err := overrideOptionalBool("ARES_WORKER_ENABLED", &worker.Enabled); err != nil {
		return err
	}
	if err := overrideOptionalInt("ARES_WORKER_CONCURRENCY", &worker.Concurrency); err != nil {
		return err
	}
	if err := overrideOptionalInt("ARES_WORKER_CLAIM_BATCH_SIZE", &worker.ClaimBatchSize); err != nil {
		return err
	}
	overrideString("ARES_WORKER_SCAN_INTERVAL", &worker.ScanInterval)
	overrideString("ARES_WORKER_LEASE_DURATION", &worker.LeaseDuration)
	overrideString("ARES_WORKER_RENEW_INTERVAL", &worker.RenewInterval)
	overrideString("ARES_WORKER_NORMAL_POLL_INTERVAL", &worker.NormalPollInterval)
	overrideString("ARES_WORKER_BACKOFF_MIN", &worker.BackoffMin)
	overrideString("ARES_WORKER_BACKOFF_MAX", &worker.BackoffMax)
	overrideString("ARES_WORKER_DRAIN_TIMEOUT", &worker.DrainTimeout)
	overrideString("ARES_WORKER_INTEGRATION_SYNC_INTERVAL", &worker.IntegrationSyncInterval)
	overrideString("ARES_WORKER_LEGACY_POLL_INTERVAL", &worker.LegacyPollInterval)
	return nil
}

func normalizeAndValidateWorkerConfig(worker *WorkerConfig) error {
	if worker == nil {
		return fmt.Errorf("worker config is nil")
	}
	if worker.Concurrency == 0 {
		worker.Concurrency = defaultWorkerConcurrency
	}
	if worker.Concurrency < 1 || worker.Concurrency > 64 {
		return fmt.Errorf("worker.concurrency must be between 1 and 64")
	}
	if worker.ClaimBatchSize == 0 {
		worker.ClaimBatchSize = min(defaultWorkerConcurrency, worker.Concurrency)
	}
	if worker.ClaimBatchSize < 1 || worker.ClaimBatchSize > 64 {
		return fmt.Errorf("worker.claim_batch_size must be between 1 and 64")
	}
	if worker.ClaimBatchSize > worker.Concurrency {
		return fmt.Errorf("worker.claim_batch_size must not exceed worker.concurrency")
	}

	setDurationDefault(&worker.ScanInterval, defaultWorkerScanInterval)
	setDurationDefault(&worker.LeaseDuration, defaultWorkerLeaseDuration)
	setDurationDefault(&worker.RenewInterval, defaultWorkerRenewInterval)
	setDurationDefault(&worker.NormalPollInterval, defaultWorkerNormalPollInterval)
	setDurationDefault(&worker.BackoffMin, defaultWorkerBackoffMin)
	setDurationDefault(&worker.BackoffMax, defaultWorkerBackoffMax)
	setDurationDefault(&worker.DrainTimeout, defaultWorkerDrainTimeout)
	setDurationDefault(&worker.IntegrationSyncInterval, defaultWorkerIntegrationSyncInterval)
	setDurationDefault(&worker.LegacyPollInterval, defaultWorkerLegacyPollInterval)

	checks := []struct {
		name    string
		value   string
		minimum time.Duration
		maximum time.Duration
	}{
		{"worker.scan_interval", worker.ScanInterval, 100 * time.Millisecond, time.Minute},
		{"worker.lease_duration", worker.LeaseDuration, 5 * time.Second, 10 * time.Minute},
		{"worker.renew_interval", worker.RenewInterval, time.Second, 3 * time.Minute},
		{"worker.normal_poll_interval", worker.NormalPollInterval, 100 * time.Millisecond, 10 * time.Minute},
		{"worker.backoff_min", worker.BackoffMin, 100 * time.Millisecond, time.Hour},
		{"worker.backoff_max", worker.BackoffMax, 100 * time.Millisecond, 24 * time.Hour},
		{"worker.drain_timeout", worker.DrainTimeout, time.Second, 30 * time.Minute},
		{"worker.integration_sync_interval", worker.IntegrationSyncInterval, time.Second, 10 * time.Minute},
		{"worker.legacy_poll_interval", worker.LegacyPollInterval, time.Second, 10 * time.Minute},
	}
	parsed := make(map[string]time.Duration, len(checks))
	for _, check := range checks {
		value, err := parseWorkerDuration(check.name, check.value, check.minimum, check.maximum)
		if err != nil {
			return err
		}
		parsed[check.name] = value
	}
	lease := parsed["worker.lease_duration"]
	renew := parsed["worker.renew_interval"]
	if renew > lease/3 {
		return fmt.Errorf("worker.renew_interval must not exceed one third of worker.lease_duration")
	}
	if parsed["worker.backoff_min"] > parsed["worker.backoff_max"] {
		return fmt.Errorf("worker.backoff_min must not exceed worker.backoff_max")
	}
	return nil
}

func parseWorkerDuration(name, raw string, minimum, maximum time.Duration) (time.Duration, error) {
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be a duration between %s and %s", name, minimum, maximum)
	}
	return value, nil
}

// WorkerSettings returns the normalized settings produced by Init. The
// defensive fallbacks only protect tests or embedders that replace Main
// directly; normal startup has already validated every field.
func WorkerSettings() WorkerRuntimeConfig {
	worker := WorkerConfig{Enabled: true}
	if Main != nil {
		worker = Main.Worker
	}
	if err := normalizeAndValidateWorkerConfig(&worker); err != nil {
		worker = WorkerConfig{Enabled: worker.Enabled}
		_ = normalizeAndValidateWorkerConfig(&worker)
	}
	parse := func(value string) time.Duration {
		parsed, _ := time.ParseDuration(value)
		return parsed
	}
	return WorkerRuntimeConfig{
		Enabled: worker.Enabled, Concurrency: worker.Concurrency, ClaimBatchSize: worker.ClaimBatchSize,
		ScanInterval: parse(worker.ScanInterval), LeaseDuration: parse(worker.LeaseDuration),
		RenewInterval: parse(worker.RenewInterval), NormalPollInterval: parse(worker.NormalPollInterval),
		BackoffMin: parse(worker.BackoffMin), BackoffMax: parse(worker.BackoffMax),
		DrainTimeout: parse(worker.DrainTimeout), IntegrationSyncInterval: parse(worker.IntegrationSyncInterval),
		LegacyPollInterval: parse(worker.LegacyPollInterval),
	}
}
