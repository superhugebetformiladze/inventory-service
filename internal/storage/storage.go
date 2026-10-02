// Package storage — доступ к базе службы.
//
// Запросы написаны на SQL и живут рядом с кодом, который их использует.
// Генератор (sqlc) в плане есть, но на четырёх запросах он окупается хуже, чем
// стоит: лишний шаг кодогенерации в сборке и в CI ради экономии тридцати
// строк. Как только запросов станет полтора десятка — вернуться к нему.
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/broton07/inventory_service/migrations"
)

// DSN собирает строку подключения.
func DSN(host string, port int, user, password, db string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", user, password, host, port, db)
}

// Pool — пул соединений с базой.
type Pool struct {
	*pgxpool.Pool
}

// Open открывает пул и проверяет, что база отвечает.
//
// Проверка здесь, а не при первом запросе: служба, стартовавшая с неверным
// паролем и упавшая на первом отчёте агента, выглядит как проблема агента.
func Open(ctx context.Context, dsn string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("разбор строки подключения: %w", err)
	}

	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("создание пула: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("база не отвечает: %w", err)
	}

	return &Pool{pool}, nil
}

// Migrate накатывает миграции.
//
// Отдельной командой, а не при старте службы (инвариант I12): три экземпляра
// службы, стартующие одновременно, накатывали бы миграции наперегонки, а
// откатить уже применённое изменение схемы посреди рабочего дня нельзя.
func Migrate(ctx context.Context, dsn string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("разбор строки подключения: %w", err)
	}

	db := stdlib.OpenDB(*cfg)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("диалект: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("накат миграций: %w", err)
	}
	return nil
}

// Version — номер последней применённой миграции. Нужен, чтобы служба могла
// сказать в журнале, на какой схеме она работает.
func Version(ctx context.Context, dsn string) (int64, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return 0, err
	}

	db := stdlib.OpenDB(*cfg)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return 0, err
	}
	return goose.GetDBVersionContext(ctx, db)
}
