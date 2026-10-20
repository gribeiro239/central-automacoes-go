package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"central-automacoes/internal/config"
	"central-automacoes/internal/domain"
	"central-automacoes/internal/executor"
	"central-automacoes/internal/httpapi"
	"central-automacoes/internal/memory"
	"central-automacoes/internal/postgres"
	"central-automacoes/internal/scheduler"
	"central-automacoes/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("aplicação encerrada", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	root, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	setup, cancelSetup := context.WithTimeout(root, 15*time.Second)
	defer cancelSetup()
	var store domain.Store
	var pg *postgres.Store
	if cfg.Storage == "postgres" {
		pg, err = postgres.Open(setup, cfg.DatabaseURL)
		if err != nil {
			return errors.New("não foi possível conectar ao PostgreSQL; verifique DATABASE_URL e disponibilidade do banco")
		}
		defer pg.Close()
		if err = pg.Migrate(setup); err != nil {
			return fmt.Errorf("falha nas migrações: %w", err)
		}
		store = pg
	} else {
		store = memory.New()
		logger.Warn("modo em memória: dados são perdidos ao encerrar")
	}

	if len(os.Args) > 1 {
		if len(os.Args) != 2 || os.Args[1] != "migrate" {
			return errors.New("uso: central [migrate]")
		}
		if pg == nil {
			return errors.New("migrate exige STORAGE=postgres")
		}
		logger.Info("migrações aplicadas")
		return nil
	}

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("não foi possível abrir HTTP_ADDR: %w", err)
	}
	defer listener.Close()
	claims, stopClaims := context.WithCancel(context.Background())
	defer stopClaims()
	active, cancelActive := context.WithCancel(context.Background())
	defer cancelActive()
	pool := &worker.Pool{Store: store, Executor: executor.NewHTTP(), Logger: logger, Concurrency: cfg.Workers, PollInterval: cfg.PollInterval, RetryBase: cfg.RetryBase}
	pool.Start(claims, active)
	srv := &http.Server{Handler: httpapi.New(store, cfg.Storage, logger), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- srv.Serve(listener) }()
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		(&scheduler.Scheduler{Store: store, Logger: logger, Interval: cfg.SchedulerInterval}).Run(claims)
	}()
	logger.Info("central iniciada", "address", listener.Addr().String(), "storage", cfg.Storage, "workers", cfg.Workers)
	select {
	case <-root.Done():
		logger.Info("encerramento solicitado")
	case err = <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	stopClaims()
	// One grace deadline covers both in-flight API handlers and background work.
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); <-schedulerDone }()
	go func() { defer wg.Done(); _ = srv.Shutdown(shutdown) }()
	go func() { defer wg.Done(); pool.Wait() }()
	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-shutdown.Done():
		logger.Info("prazo de encerramento atingido; cancelando chamadas ativas")
		cancelActive()
		_ = srv.Close()
		// Each worker persists cancellation with a separate five-second DB timeout.
		select {
		case <-drained:
		case <-time.After(6 * time.Second):
			logger.Warn("trabalho pendente será recuperado após vencer a reserva")
		}
	}
	logger.Info("central encerrada")
	return err
}
