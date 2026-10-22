package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"minipress/internal/config"
	"minipress/internal/database"
	"minipress/internal/handlers"
	"minipress/internal/repository"
	"minipress/internal/service"
	"minipress/internal/worker"
	"minipress/web"
)

func main() {
	cleanupNow := flag.Bool("cleanup-now", false, "run background cleanup once and exit")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, *cleanupNow); err != nil {
		log.Error("exiting with error", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, cleanupNow bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := database.Migrate(db); err != nil {
		return err
	}

	store, err := service.NewImageStore(cfg.UploadsDir)
	if err != nil {
		return err
	}
	pages := repository.NewPageRepo(db)
	images := repository.NewImageRepo(db)

	scheduler := &worker.Scheduler{
		Hour:   cfg.CleanupHour,
		Minute: cfg.CleanupMinute,
		Clock:  worker.RealClock,
		Log:    log,
		Jobs: []worker.Job{
			worker.NewDraftCleanupJob(pages, cfg.DraftMinAge, worker.RealClock),
			worker.NewImageCleanupJob(images, store, cfg.ImageGrace, worker.RealClock, log),
		},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cleanupNow {
		scheduler.RunAll(ctx, log)
		return nil
	}

	app, err := handlers.New(pages, images, store, web.Templates(), log, handlers.Options{TrustProxy: cfg.TrustProxy})
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           app.Routes(web.Static()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	go scheduler.Run(ctx)

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("server started", "url", "http://localhost:"+cfg.Port)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
