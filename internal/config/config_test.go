package config_test

import (
	"testing"
	"time"

	"central-automacoes/internal/config"
)

func parse(values map[string]string) (config.Config, error) {
	return config.Parse(func(key string) string { return values[key] })
}

func TestDefaultsAndExplicitPostgres(t *testing.T) {
	c, err := parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != "127.0.0.1:18081" || c.Storage != "memory" || c.Workers != 4 || c.PollInterval != 250*time.Millisecond || c.RetryBase != 5*time.Second || c.ShutdownTimeout != 20*time.Second || c.SchedulerInterval != 500*time.Millisecond {
		t.Fatalf("defaults: %+v", c)
	}
	c, err = parse(map[string]string{"HTTP_ADDR": "[::1]:18082", "STORAGE": "postgres", "DATABASE_URL": "postgres://test@127.0.0.1/test", "WORKERS": "2", "POLL_INTERVAL": "20ms", "RETRY_BASE": "30ms", "SHUTDOWN_TIMEOUT": "1s"})
	if err != nil || c.Storage != "postgres" || c.Workers != 2 || c.PollInterval != 20*time.Millisecond || c.RetryBase != 30*time.Millisecond || c.ShutdownTimeout != time.Second {
		t.Fatalf("explicit config=%+v err=%v", c, err)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	cases := []map[string]string{
		{"HTTP_ADDR": "bad"}, {"HTTP_ADDR": "0.0.0.0:18081"}, {"HTTP_ADDR": "192.168.1.2:18081"}, {"HTTP_ADDR": "localhost:18081"}, {"HTTP_ADDR": "127.0.0.1:0"}, {"HTTP_ADDR": "127.0.0.1:65536"}, {"HTTP_ADDR": "127.0.0.1:nope"},
		{"STORAGE": "other"}, {"STORAGE": "postgres"},
		{"WORKERS": "0"}, {"WORKERS": "65"}, {"WORKERS": "bad"},
		{"POLL_INTERVAL": "bad"}, {"POLL_INTERVAL": "1ms"}, {"POLL_INTERVAL": "2m"},
		{"SCHEDULER_INTERVAL": "bad"}, {"SCHEDULER_INTERVAL": "1ms"}, {"SCHEDULER_INTERVAL": "2m"},
		{"RETRY_BASE": "0s"}, {"RETRY_BASE": "2h"}, {"SHUTDOWN_TIMEOUT": "0s"}, {"SHUTDOWN_TIMEOUT": "6m"},
	}
	for _, values := range cases {
		t.Run(name(values), func(t *testing.T) {
			if c, err := parse(values); err == nil {
				t.Fatalf("accepted invalid configuration %+v => %+v", values, c)
			}
		})
	}
}

func name(values map[string]string) string {
	for k, v := range values {
		return k + "=" + v
	}
	return "empty"
}
