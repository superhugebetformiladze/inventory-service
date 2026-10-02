//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/broton07/inventory_service/internal/agent"
)

// командаInstall ставит агента службой.
//
//	agent install -server http://inventory:8080 -token enr_...
//
// Одной командой намеренно: раскатка по парку идёт скриптом групповой
// политики, и каждый лишний шаг в нём — это машины, на которых шаг не
// выполнился.
func командаInstall(args []string) error {
	f, err := флаги("install", args)
	if err != nil {
		return err
	}

	cfg, token, err := настройки(f)
	if err != nil {
		return err
	}
	if cfg.Server == "" {
		return errors.New("не задан адрес службы: -server")
	}

	// Настройки пишутся до создания службы: служба стартует сразу и должна
	// найти адрес и токен на месте.
	if err := agent.SaveConfig(cfg.DataDir, cfg, token); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("не удалось определить путь к программе: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("диспетчер служб недоступен (нужны права администратора): %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	if s, err := m.OpenService(ServiceName); err == nil {
		_ = s.Close()
		return fmt.Errorf("служба %s уже установлена", ServiceName)
	}

	s, err := m.CreateService(ServiceName, exe, mgr.Config{
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
		StartType:   mgr.StartAutomatic,

		// Отложенный автозапуск: пятьсот машин включаются утром в одни и те
		// же десять минут, и агент не должен соревноваться за диск с
		// системой, антивирусом и входом пользователя. Разброс расписания
		// внутри агента решает ту же задачу дальше по времени.
		DelayedAutoStart: true,
	}, "run")
	if err != nil {
		return fmt.Errorf("создание службы: %w", err)
	}
	defer func() { _ = s.Close() }()

	// Перезапуск при сбое. Без него любая неожиданная ошибка означает
	// машину, молча выпавшую из учёта до следующего обхода руками.
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Minute},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Minute},
	}, uint32((24 * time.Hour).Seconds())); err != nil {
		fmt.Fprintln(os.Stderr, "предупреждение: не удалось задать перезапуск при сбое:", err)
	}

	if err := s.Start("run"); err != nil {
		return fmt.Errorf("служба создана, но не запустилась: %w", err)
	}

	fmt.Printf("служба %s установлена и запущена\n", ServiceName)
	fmt.Printf("журнал: %s\n", cfg.LogPath())
	return nil
}

// командаUninstall снимает службу.
func командаUninstall(args []string) error {
	f, err := флаги("uninstall", args)
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("диспетчер служб недоступен (нужны права администратора): %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("служба %s не установлена", ServiceName)
	}
	defer func() { _ = s.Close() }()

	if status, err := s.Control(svc.Stop); err == nil {
		// Служба может быть в середине отправки отчёта. Ждать бесконечно
		// незачем, но и рвать на полуслове тоже.
		срок := time.Now().Add(30 * time.Second)
		for status.State != svc.Stopped && time.Now().Before(срок) {
			time.Sleep(300 * time.Millisecond)
			if status, err = s.Query(); err != nil {
				break
			}
		}
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("удаление службы: %w", err)
	}

	fmt.Printf("служба %s снята\n", ServiceName)

	// Каталог данных НЕ удаляется, и это осознанно: в нём лежит отметка
	// агента. Снятая и поставленная заново служба должна остаться той же
	// машиной в учёте, а не завести вторую карточку.
	fmt.Printf("каталог данных оставлен: %s\n", *f.data)
	fmt.Println("в нём отметка агента — удалять её значит завести машине вторую карточку")
	return nil
}
