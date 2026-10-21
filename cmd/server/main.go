// Команда server — служба сбора сведений о рабочих станциях.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/broton07/inventory_service/internal/agents"
	"github.com/broton07/inventory_service/internal/commands"
	"github.com/broton07/inventory_service/internal/config"
	"github.com/broton07/inventory_service/internal/httpapi"
	"github.com/broton07/inventory_service/internal/ingest"
	"github.com/broton07/inventory_service/internal/metrics"
	"github.com/broton07/inventory_service/internal/netscan"
	"github.com/broton07/inventory_service/internal/outbox"
	"github.com/broton07/inventory_service/internal/publish"
	"github.com/broton07/inventory_service/internal/retention"
	"github.com/broton07/inventory_service/internal/silence"
	"github.com/broton07/inventory_service/internal/storage"
)

func main() {
	if err := run(); err != nil {
		slog.Error("служба остановлена с ошибкой", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("не удалось прочитать настройки", "err", err)
		return err
	}

	setupLogger(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dsn := storage.DSN(cfg.PostgresHost, cfg.PostgresPort, cfg.PostgresUser, cfg.PostgresPass, cfg.PostgresDB)

	pool, err := storage.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	if version, err := storage.Version(ctx, dsn); err != nil {
		slog.Warn("не удалось узнать версию схемы", "err", err)
	} else {
		slog.Info("схема базы", "version", version)
	}

	publisher, err := publish.NewKafka(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer publisher.Close()

	relay := outbox.New(pool, publisher, outbox.Options{
		Interval:  cfg.RelayInterval,
		BatchSize: cfg.RelayBatchSize,
		Logger:    slog.Default(),
	})

	// Опрос сетевых устройств — только если задана строка доступа SNMP. Без
	// неё служба работает как раньше: снимки устройств приходят файлами.
	// Адреса — из настроек и из списка, который присылает внешняя система.
	var (
		scanner     *netscan.Runner
		scannerDeps httpapi.Scanner
	)
	if cfg.SNMPCommunity != "" {
		dial, cleanup, err := netscan.NewDialer(cfg.NetscanWalker, netscan.SNMPOptions{
			Community: cfg.SNMPCommunity,
			Timeout:   cfg.SNMPTimeout,
			Retries:   cfg.SNMPRetries,
		})
		if err != nil {
			return fmt.Errorf("опрос сетевых устройств: %w", err)
		}
		defer cleanup()

		// Адреса из настроек — в тот же список, что и присланные: так у них
		// тоже виден итог последней попытки.
		var fromConfig []storage.NetworkTarget
		for _, ip := range cfg.NetscanTargets {
			fromConfig = append(fromConfig, storage.NetworkTarget{IP: ip})
		}
		if _, err := pool.ReplaceNetworkTargets(ctx, "config", fromConfig); err != nil {
			slog.Warn("адреса из настроек не записаны в список опроса", "err", err)
		}

		scanner = netscan.NewRunner(netscan.NewService(pool, time.Now), dial,
			netscan.RunnerOptions{
				Targets:  cfg.NetscanTargets,
				List:     pool,
				Interval: cfg.NetscanInterval,
				Timeout:  cfg.NetscanTimeout,
				Silence:  cfg.NetscanSilence,
				Logger:   slog.Default(),
			})
		// Не присваивать nil-указатель интерфейсу: «опрос выключен» маршруты
		// узнают по nil интерфейса.
		scannerDeps = scanner
		slog.Info("опрос сетевых устройств включён", "walker", cfg.NetscanWalker)
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.Router(httpapi.Deps{
			Checks:    checks(pool, publisher),
			Acceptor:  ingest.New(pool, time.Now),
			Auth:      agents.New(pool, time.Now),
			Commander: commands.New(pool, httpapi.CommandTTL, time.Now),

			// Административный вход поднимается только с ключом: без него
			// маршрутов нет вовсе (см. httpapi.Router).
			Admin:      commands.New(pool, httpapi.CommandTTL, time.Now),
			AdminToken: cfg.AdminToken,

			// Состояние службы словами — для экрана инвентаризации в
			// учётной системе: показатели в /metrics читает Prometheus, а
			// человеку нужен ответ на вопрос «почему данные устарели».
			Doctor: pool,

			// Список опроса коммутаторов: учёт присылает свои.
			NetTargets: pool,
			Scanner:    scannerDeps,

			// Предел частоты регистраций рассчитан на раскатку: см.
			// httpapi.DefaultEnrollBurst.
			EnrollBurst: cfg.EnrollBurst,

			Logger: slog.Default(),
			Now:    time.Now,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	watcher := silence.New(pool, silence.Options{
		Threshold: cfg.SilenceThreshold,
		Interval:  cfg.SilenceInterval,
		Logger:    slog.Default(),
	})

	// Показатели, которые нельзя посчитать приращением: длина очереди
	// публикации, число молчащих машин, возраст самого старого отчёта.
	collector := &metrics.Collector{Source: pool, Logger: slog.Default()}

	cleaner := retention.New(pool, retention.Options{
		Snapshots: cfg.RetentionSnapshots,
		Outbox:    cfg.RetentionOutbox,
		Commands:  cfg.RetentionCommands,
		Interval:  cfg.RetentionInterval,
		Logger:    slog.Default(),
	})

	var wg sync.WaitGroup
	if scanner != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scanner.Run(ctx)
		}()
	}
	wg.Add(4)
	go func() {
		defer wg.Done()
		relay.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		watcher.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		collector.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		cleaner.Run(ctx)
	}()

	errc := make(chan error, 1)
	go func() {
		slog.Info("служба слушает", "addr", cfg.HTTPAddr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		slog.Info("получен сигнал остановки, доигрываем запросы", "timeout", cfg.ShutdownTimeout)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}

	// Дожидаемся релея: он мог быть посреди транзакции, и обрыв на полуслове
	// оставил бы сообщения заблокированными до истечения таймаута базы.
	wg.Wait()

	slog.Info("служба остановлена")
	return nil
}

// checks — проверки готовности настоящими клиентами, а не набором TCP.
// Открытый порт не означает, что база принимает запросы, а лента жива.
func checks(pool *storage.Pool, publisher *publish.Kafka) []httpapi.Check {
	return []httpapi.Check{
		{Name: "postgres", Func: pool.Ping},
		{Name: "kafka", Func: publisher.Ping},
	}
}

func setupLogger(cfg config.Config) {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	// JSON и в разработке тоже: если формат журнала разный, то разбор
	// продуктивного журнала оказывается первым разом, когда его читают.
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler).With("service", "inventory"))
}
