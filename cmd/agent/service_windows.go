//go:build windows

package main

import (
	"context"

	"golang.org/x/sys/windows/svc"

	"github.com/broton07/inventory_service/internal/agent"
)

// Имя службы и то, что человек увидит в списке служб.
const (
	ServiceName        = "InventoryAgent"
	ServiceDisplayName = "Инвентаризация: сбор сведений о компьютере"
	ServiceDescription = "Собирает сведения об оборудовании и программах " +
		"этой машины и отправляет их в службу учёта. Ничего не выполняет по " +
		"указанию сервера, кроме внеочередного сбора и смены расписания."
)

// служебныйЗапуск — запущены ли мы диспетчером служб.
//
// Отличить это от запуска руками можно только спросив систему: аргументы
// командной строки у службы те же, что задал установщик.
func служебныйЗапуск() bool {
	is, err := svc.IsWindowsService()
	return err == nil && is
}

// windowsService — обвязка, через которую диспетчер служб разговаривает с
// агентом.
type windowsService struct{}

// Execute — то, что диспетчер вызывает при запуске службы.
func (windowsService) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const принимаем = svc.AcceptStop | svc.AcceptShutdown

	s <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	готово := make(chan error, 1)
	go func() { готово <- работа(ctx) }()

	s <- svc.Status{State: svc.Running, Accepts: принимаем}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				// Отдельное состояние «останавливаемся»: агент может быть в
				// середине отправки отчёта, и диспетчер должен подождать, а
				// не счесть службу зависшей.
				s <- svc.Status{State: svc.StopPending}
				cancel()
				<-готово
				return false, 0
			}
		case err := <-готово:
			if err != nil {
				// Код возврата отличен от нуля: по нему система применяет
				// действия при сбое и перезапускает службу.
				return false, 1
			}
			return false, 0
		}
	}
}

// работа — то же самое, что делает «agent run», но без разбора аргументов.
func работа(ctx context.Context) error {
	cfg, token, err := agent.LoadConfig(agent.DefaultDataDir(), Version)
	if err != nil {
		return err
	}

	log, закрыть, err := журнал(cfg, false)
	if err != nil {
		return err
	}
	defer закрыть()

	state, err := подготовить(ctx, cfg, token, log)
	if err != nil {
		log.Error("агент не смог подготовиться к работе", "err", err)
		return err
	}

	return собранный(cfg, state, log).Run(ctx)
}

func runAsService() error {
	return svc.Run(ServiceName, windowsService{})
}
