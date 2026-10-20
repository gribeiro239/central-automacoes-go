package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr          string
	Storage           string
	DatabaseURL       string
	Workers           int
	PollInterval      time.Duration
	RetryBase         time.Duration
	ShutdownTimeout   time.Duration
	SchedulerInterval time.Duration
}

func Load() (Config, error) { return Parse(os.Getenv) }

func Parse(get func(string) string) (Config, error) {
	value := func(k, d string) string {
		if v := get(k); v != "" {
			return v
		}
		return d
	}
	c := Config{HTTPAddr: value("HTTP_ADDR", "127.0.0.1:18081"), Storage: value("STORAGE", "memory"), DatabaseURL: get("DATABASE_URL")}
	host, port, err := net.SplitHostPort(c.HTTPAddr)
	if err != nil {
		return c, fmt.Errorf("HTTP_ADDR inválido")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return c, fmt.Errorf("HTTP_ADDR deve usar IP de loopback: API local sem autenticação")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return c, fmt.Errorf("porta de HTTP_ADDR inválida")
	}
	if c.Storage != "memory" && c.Storage != "postgres" {
		return c, fmt.Errorf("STORAGE deve ser memory ou postgres")
	}
	if c.Storage == "postgres" && c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL é obrigatória para STORAGE=postgres")
	}
	c.Workers, err = strconv.Atoi(value("WORKERS", "4"))
	if err != nil || c.Workers < 1 || c.Workers > 64 {
		return c, fmt.Errorf("WORKERS deve estar entre 1 e 64")
	}
	durations := []struct {
		k, d     string
		dst      *time.Duration
		min, max time.Duration
	}{
		{"POLL_INTERVAL", "250ms", &c.PollInterval, 10 * time.Millisecond, time.Minute},
		{"RETRY_BASE", "5s", &c.RetryBase, 10 * time.Millisecond, time.Hour},
		{"SHUTDOWN_TIMEOUT", "20s", &c.ShutdownTimeout, time.Second, 5 * time.Minute},
		{"SCHEDULER_INTERVAL", "500ms", &c.SchedulerInterval, 10 * time.Millisecond, time.Minute},
	}
	for _, v := range durations {
		*v.dst, err = time.ParseDuration(value(v.k, v.d))
		if err != nil || *v.dst < v.min || *v.dst > v.max {
			return c, fmt.Errorf("%s deve estar entre %s e %s", v.k, v.min, v.max)
		}
	}
	return c, nil
}
