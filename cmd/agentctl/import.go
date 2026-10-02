package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/broton07/inventory_service/internal/agent"
	"github.com/broton07/inventory_service/internal/agents"
	"github.com/broton07/inventory_service/internal/ingest"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

// принятьФайлы — приём отчётов с машин, у которых нет сети.
//
//	agentctl import -dir /mnt/флешка
//
// Пачкой, а не по одному: человек обходит с флешкой десяток машин за раз, и
// разбирать потом каждый файл отдельной командой — это гарантия того, что
// половину забудут.
//
// Повторный приём тех же файлов безвреден: снимок с тем же временем сбора
// служба уже видела и запишет его как повтор (инвариант I3). Поэтому файлы
// никуда не перемещаются и ничего не удаляется — флешку приносят и уносят, а
// не отдают навсегда.
func принятьФайлы(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	dir := fs.String("dir", "", "каталог с файлами, снятыми с машин без сети")
	file := fs.String("file", "", "один файл")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" && *file == "" {
		return errors.New("нужен -dir или -file")
	}

	файлы, err := собратьФайлы(*dir, *file)
	if err != nil {
		return err
	}
	if len(файлы) == 0 {
		fmt.Println("файлов для приёма не найдено")
		return nil
	}

	регистрация := agents.New(pool, time.Now)
	приём := ingest.New(pool, time.Now)

	принято, отказано := 0, 0

	for _, путь := range файлы {
		var err error
		switch {
		case strings.HasSuffix(путь, agent.ClaimSuffix):
			err = приняьЗаявку(ctx, регистрация, путь)
		case strings.HasSuffix(путь, agent.ReportSuffix):
			err = принятьОтчёт(ctx, регистрация, приём, путь)
		default:
			continue
		}

		if err != nil {
			// Одна испорченная флешка не должна останавливать приём
			// остальных: человек принёс их все разом и второй раз не пойдёт.
			fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(путь), err)
			отказано++
			continue
		}
		принято++
	}

	fmt.Printf("\nпринято %d, отказано %d\n", принято, отказано)
	if отказано > 0 {
		return errors.New("часть файлов принять не удалось")
	}
	return nil
}

// собратьФайлы находит, что принимать, и раскладывает в нужном порядке.
//
// Заявки первыми: отчёт от машины, которой ещё нет в базе, принять не от
// кого — проверять его подпись будет нечем.
func собратьФайлы(dir, file string) ([]string, error) {
	var файлы []string

	if file != "" {
		файлы = append(файлы, file)
	}

	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("чтение каталога: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			имя := e.Name()
			if strings.HasSuffix(имя, agent.ClaimSuffix) || strings.HasSuffix(имя, agent.ReportSuffix) {
				файлы = append(файлы, filepath.Join(dir, имя))
			}
		}
	}

	sort.Slice(файлы, func(i, j int) bool {
		iЗаявка := strings.HasSuffix(файлы[i], agent.ClaimSuffix)
		jЗаявка := strings.HasSuffix(файлы[j], agent.ClaimSuffix)
		if iЗаявка != jЗаявка {
			return iЗаявка
		}
		// Отчёты — по имени, а в имени время сбора: служба отбрасывает
		// снимок, пришедший позже более свежего.
		return файлы[i] < файлы[j]
	})

	return файлы, nil
}

// приняьЗаявку регистрирует машину по заявке с флешки.
func приняьЗаявку(ctx context.Context, регистрация *agents.Service, путь string) error {
	b, err := os.ReadFile(путь) //nolint:gosec // файл называет администратор, это и есть смысл команды
	if err != nil {
		return err
	}

	var claim contract.SignedClaim
	if err := json.Unmarshal(b, &claim); err != nil {
		return fmt.Errorf("заявка не разбирается: %w", err)
	}

	agentRow, err := регистрация.EnrollOffline(ctx, claim)
	if err != nil {
		return err
	}

	fmt.Printf("заявка  %-20s зарегистрирована, отметка %s\n",
		claim.Hostname, agentRow.AgentID)
	return nil
}

// принятьОтчёт проверяет подпись и отдаёт снимок обычному приёму.
func принятьОтчёт(
	ctx context.Context, регистрация *agents.Service, приём *ingest.Service, путь string,
) error {
	b, err := os.ReadFile(путь) //nolint:gosec // файл называет администратор, это и есть смысл команды
	if err != nil {
		return err
	}

	var signed contract.SignedSnapshot
	if err := json.Unmarshal(b, &signed); err != nil {
		return fmt.Errorf("отчёт не разбирается: %w", err)
	}

	// Открытый ключ берётся по отметке из конверта, и он единственное, чему
	// здесь верят. Всё остальное содержимое файла подтверждается подписью
	// или не принимается вовсе.
	agentRow, err := регистрация.ByMark(ctx, signed.AgentID)
	if err != nil {
		return err
	}

	snap, err := signed.Verify(agentRow.PublicKey)
	if err != nil {
		return fmt.Errorf("файл не принят: %w", err)
	}

	// Дальше — тот же путь, что у сетевого отчёта: те же проверки, то же
	// опознание, та же запись в ленту. Иначе автономный отчёт давал бы в
	// учёте не то же самое, что сетевой.
	result, err := приём.Accept(ctx, snap, &agentRow.ID)
	if err != nil {
		return err
	}

	switch {
	case result.Unresolved:
		fmt.Printf("отчёт   %-20s принят, но машину опознать не удалось: %s\n",
			snap.Machine.Hostname, result.Reason)
	case result.Duplicate:
		fmt.Printf("отчёт   %-20s уже принимался, повтор\n", snap.Machine.Hostname)
	default:
		fmt.Printf("отчёт   %-20s принят, машина %s\n", snap.Machine.Hostname, result.HostID)
	}
	return nil
}
