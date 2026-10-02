// Команда migrate накатывает миграции базы.
//
// Отдельной командой, а не при старте службы (инвариант I12): три экземпляра
// службы, стартующие одновременно, накатывали бы миграции наперегонки, а
// откатить уже применённое изменение схемы посреди рабочего дня нельзя.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/broton07/inventory_service/internal/config"
	"github.com/broton07/inventory_service/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "настройки:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dsn := storage.DSN(cfg.PostgresHost, cfg.PostgresPort, cfg.PostgresUser, cfg.PostgresPass, cfg.PostgresDB)

	if err := storage.Migrate(ctx, dsn); err != nil {
		fmt.Fprintln(os.Stderr, "миграции:", err)
		os.Exit(1)
	}

	version, err := storage.Version(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "версия схемы:", err)
		os.Exit(1)
	}

	fmt.Printf("схема на версии %d\n", version)
}
