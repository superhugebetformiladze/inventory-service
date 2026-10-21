// Команда agentctl — управление агентами и токенами регистрации.
//
// Отдельной командой, а не маршрутом службы: выдача токена раскатки и отзыв
// ключа — действия администратора, а не агента, и открывать их тем же портом,
// которым слушают пятьсот рабочих станций, незачем.
//
//	agentctl token   -label "раскатка сентябрь" -days 14 -uses 600
//	agentctl list
//	agentctl revoke  -agent <отметка> -reason "машина списана"
//	agentctl import  -dir /mnt/флешка
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/agents"
	"github.com/broton07/inventory_service/internal/commands"
	"github.com/broton07/inventory_service/internal/config"
	"github.com/broton07/inventory_service/internal/httpapi"
	"github.com/broton07/inventory_service/internal/retention"
	"github.com/broton07/inventory_service/internal/retire"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := открытьБазу(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer pool.Close()

	switch os.Args[1] {
	case "token":
		err = создатьТокен(ctx, pool, os.Args[2:])
	case "list":
		err = списокАгентов(ctx, pool, os.Args[2:])
	case "revoke":
		err = отозвать(ctx, pool, os.Args[2:])
	case "pending":
		err = очередьРазбора(ctx, pool, os.Args[2:])
	case "link":
		err = связать(ctx, pool, os.Args[2:])
	case "collect":
		err = собратьСейчас(ctx, pool, os.Args[2:])
	case "commands":
		err = списокЗаданий(ctx, pool, os.Args[2:])
	case "import":
		err = принятьФайлы(ctx, pool, os.Args[2:])
	case "cleanup":
		err = почистить(ctx, pool, os.Args[2:])
	case "retire":
		err = вывестиИзУчёта(ctx, pool, os.Args[2:])
	case "dismiss":
		err = закрытьОтчёт(ctx, pool, os.Args[2:])
	case "netscan-import":
		err = принятьОбходы(ctx, pool, os.Args[2:])
	case "netscan-poll":
		err = опроситьУстройство(ctx, pool, os.Args[2:])
	case "netscan-list":
		err = списокУстройств(ctx, pool, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `agentctl — управление агентами

  token    создать токен регистрации
  list     список агентов
  revoke   отозвать ключ одной машины
  pending  очередь разбора: машины, которые не удалось опознать
  link     разобрать отложенный отчёт: связать с машиной или завести новую
  collect  поставить задание «собрать сейчас»
  commands список заданий и их состояние
  import   принять отчёты с машин без сети: agentctl import -dir /mnt/флешка
  cleanup  разово удалить устаревшее (служба делает это сама раз в сутки)
  retire   вывести машину из учёта: agentctl retire -host <id> | -name <имя>
  dismiss  закрыть отложенный отчёт, не заводя машину: -report <id> | -all

  netscan-import  принять снимки коммутаторов из файлов: -dir <каталог>
  netscan-poll    опросить коммутатор сейчас: -target <адрес> [-dry]
  netscan-list    опрошенные сетевые устройства`)
}

func открытьБазу(ctx context.Context) (*storage.Pool, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("настройки: %w", err)
	}
	return storage.Open(ctx, storage.DSN(
		cfg.PostgresHost, cfg.PostgresPort, cfg.PostgresUser, cfg.PostgresPass, cfg.PostgresDB))
}

func создатьТокен(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	label := fs.String("label", "", "для чего этот токен: видно в списке")
	days := fs.Int("days", 14, "срок действия в днях")
	uses := fs.Int("uses", 0, "сколько машин можно зарегистрировать; 0 — без ограничения")
	if err := fs.Parse(args); err != nil {
		return err
	}

	secret, err := agents.NewEnrollmentToken()
	if err != nil {
		return err
	}

	var usesLeft *int
	if *uses > 0 {
		usesLeft = uses
	}

	token, err := pool.CreateEnrollmentToken(ctx, agents.Hash(secret), *label,
		time.Now().UTC().AddDate(0, 0, *days), usesLeft)
	if err != nil {
		return err
	}

	fmt.Println(secret)
	fmt.Fprintf(os.Stderr, `
Токен создан. Он показан ОДИН РАЗ — в базе лежит только его хеш.
  действует до: %s
  использований: %s
Положите его в установщик агента; для отзыва одной машины отзывается её ключ,
а не этот токен.
`, token.ExpiresAt.Format("2006-01-02 15:04 MST"), ограничение(usesLeft))

	return nil
}

func ограничение(uses *int) string {
	if uses == nil {
		return "без ограничения"
	}
	return fmt.Sprintf("%d", *uses)
}

func списокАгентов(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	limit := fs.Int("limit", 50, "сколько показать")
	if err := fs.Parse(args); err != nil {
		return err
	}

	list, err := pool.ListAgents(ctx, *limit)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("агентов пока нет")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	// Ошибки записи в tabwriter по одной не проверяются: буфер сбрасывается
	// один раз, и Flush ниже сообщит обо всём сразу.
	_, _ = fmt.Fprintln(w, "ОТМЕТКА\tИМЯ\tВЕРСИЯ\tКЛЮЧ\tНА СВЯЗИ\tСОСТОЯНИЕ")
	for _, a := range list {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s…\t%s\t%s\n",
			сокращённо(a.AgentID), пусто(a.Hostname), пусто(a.AgentVersion),
			a.KeyPrefix, наСвязи(a.LastSeenAt), состояние(a))
	}
	return w.Flush()
}

func отозвать(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("revoke", flag.ExitOnError)
	agentID := fs.String("agent", "", "отметка агента")
	reason := fs.String("reason", "", "почему отозван: видно в списке")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *agentID == "" {
		return fmt.Errorf("не указана отметка агента (-agent)")
	}

	if err := pool.RevokeAgent(ctx, *agentID, *reason, time.Now().UTC()); err != nil {
		return err
	}

	fmt.Printf("ключ агента %s отозван; следующий его отчёт будет отклонён\n", *agentID)
	return nil
}

func очередьРазбора(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("pending", flag.ExitOnError)
	limit := fs.Int("limit", 50, "сколько показать")
	if err := fs.Parse(args); err != nil {
		return err
	}

	list, err := pool.ListUnresolved(ctx, *limit)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("очередь разбора пуста")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ОТЧЁТ\tИМЯ\tЗАВ. НОМЕР\tЖДЁТ С\tОТЧЁТОВ\tПРИЧИНА")
	for _, r := range list {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			r.ID.String()[:8], пусто(r.Hostname), пусто(r.Serial),
			наСвязи(&r.FirstSeenAt), r.Attempts, r.Reason)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, `
Разобрать:
  agentctl link -report <ОТЧЁТ> -host <идентификатор машины>   это уже известная машина
  agentctl link -report <ОТЧЁТ> -new                           это новая машина`)
	return nil
}

func связать(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	report := fs.String("report", "", "идентификатор отложенного отчёта")
	host := fs.String("host", "", "идентификатор машины, к которой он относится")
	isNew := fs.Bool("new", false, "это новая машина: завести её")
	by := fs.String("by", "администратор", "кто разобрал")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *report == "" {
		return fmt.Errorf("нужен -report")
	}
	if (*host == "") == !*isNew {
		return fmt.Errorf("нужен либо -host с машиной, либо -new, но не оба сразу")
	}

	// Достаточно того, что напечатала очередь разбора: человек связывает
	// именно тот отчёт, который видит на экране.
	reportID, err := uuid.Parse(*report)
	if err != nil {
		if reportID, err = pool.FindUnresolvedByPrefix(ctx, *report); err != nil {
			return err
		}
	}

	// Машины, к которой можно привязать отчёт, может ещё не существовать: у
	// сборного компьютера нет ни заводского номера, ни SMBIOS UUID, и
	// автоматика такую машину не заводит (I8). Тогда решение принимает
	// человек — и это ровно то, ради чего очередь разбора заведена.
	if *isNew {
		hostID, err := pool.AcceptUnresolvedAsNew(ctx, reportID, *by, time.Now().UTC())
		if err != nil {
			return err
		}
		fmt.Printf(`машина заведена: %s
её признаки, включая отметку агента, закреплены за ней,
и следующий отчёт опознается сам
`, hostID)
		return nil
	}

	hostID, err := uuid.Parse(*host)
	if err != nil {
		return fmt.Errorf("идентификатор машины: %w", err)
	}

	if err := pool.ResolveUnresolved(ctx, reportID, hostID, *by, time.Now().UTC()); err != nil {
		return err
	}

	fmt.Println(`отчёт связан с машиной; признаки из него закреплены за ней,
и следующий отчёт этой машины опознается сам`)
	return nil
}

func собратьСейчас(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("collect", flag.ExitOnError)
	host := fs.String("host", "", "имя компьютера или его идентификатор")
	agentMark := fs.String("agent", "", "отметка агента, если имя неизвестно")
	by := fs.String("by", "администратор", "кто просит")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *host == "" && *agentMark == "" {
		return fmt.Errorf("нужен -host или -agent")
	}

	var (
		hostID    *uuid.UUID
		agentUUID uuid.UUID
		err       error
	)

	switch {
	case *agentMark != "":
		agentUUID, err = pool.AgentByMark(ctx, *agentMark)
	default:
		// Человек знает имя компьютера, а не идентификаторы. Разрешаем и то,
		// и другое: имя привычнее, идентификатор надёжнее.
		id, parseErr := uuid.Parse(*host)
		if parseErr != nil {
			id, err = pool.HostByName(ctx, *host)
			if err != nil {
				return fmt.Errorf("машина %q не найдена: %w", *host, err)
			}
		}
		hostID = &id
		agentUUID, err = pool.AgentOfHost(ctx, id)
		if err != nil {
			return fmt.Errorf("у машины нет действующего агента: %w", err)
		}
	}
	if err != nil {
		return err
	}

	service := commands.New(pool, httpapi.CommandTTL, time.Now)
	id, err := service.Create(ctx, agentUUID, hostID, contract.CommandCollectNow, nil, *by)
	if err != nil {
		return err
	}

	fmt.Printf("задание %s поставлено; агент заберёт его при ближайшем опросе\n", id)
	return nil
}

func списокЗаданий(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("commands", flag.ExitOnError)
	limit := fs.Int("limit", 30, "сколько показать")
	if err := fs.Parse(args); err != nil {
		return err
	}

	list, err := pool.ListCommands(ctx, *limit)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("заданий пока не было")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ЗАДАНИЕ\tВИД\tМАШИНА\tПОСТАВЛЕНО\tПОПЫТОК\tСОСТОЯНИЕ")
	for _, c := range list {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			c.ID.String()[:8], c.Kind, пусто(c.Hostname),
			наСвязи(&c.CreatedAt), c.Attempts, состояниеЗадания(c))
	}
	return w.Flush()
}

func состояниеЗадания(c storage.CommandRow) string {
	switch {
	case c.DoneAt != nil && c.OK != nil && *c.OK:
		return "выполнено"
	case c.DoneAt != nil:
		return "не удалось: " + c.Error
	case c.ExpiresAt.Before(time.Now()):
		return "просрочено"
	case c.TakenAt != nil:
		return "выдано агенту"
	default:
		return "ждёт опроса"
	}
}

func сокращённо(s string) string {
	if len(s) > 20 {
		return s[:20] + "…"
	}
	return s
}

func пусто(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func наСвязи(t *time.Time) string {
	if t == nil {
		return "никогда"
	}
	d := time.Since(*t).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "только что"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
	}
}

func состояние(a storage.Agent) string {
	if a.Revoked() {
		if a.RevokeReason != "" {
			return "отозван: " + a.RevokeReason
		}
		return "отозван"
	}
	return "действует"
}

// почистить — разовый запуск чистки.
//
// Служба чистит за собой сама раз в сутки; эта команда нужна там, где ждать
// сутки не хочется: после нагрузочного прогона, перед снятием копии базы,
// при разборе «почему база такая большая».
func почистить(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	snapshots := fs.Duration("snapshots", retention.DefaultSnapshots, "срок хранения снимков")
	outbox := fs.Duration("outbox", retention.DefaultOutbox, "срок хранения опубликованных сообщений")
	commands := fs.Duration("commands", retention.DefaultCommands, "срок хранения заданий")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Свой срок: чистка миллиона строк не укладывается в общий тридцатисекундный
	// таймаут команды.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()

	cleaned, err := pool.Cleanup(ctx, *snapshots, *outbox, *commands)
	if err != nil {
		return err
	}

	fmt.Printf("удалено: снимков %d, сообщений %d, заданий %d, разобранных отчётов %d\n",
		cleaned.Snapshots, cleaned.Outbox, cleaned.Commands, cleaned.Unresolved)
	fmt.Println("последний снимок каждой машины оставлен независимо от срока")
	return nil
}

// вывестиИзУчёта отправляет надгробие по машине: в службе она помечается
// выбывшей, всем потребителям уходит сообщение.
//
// Отдельно принимается список идентификаторов файлом. Это не удобство:
// машины, удалённые из базы в обход службы, остаются в уплотняемом топике
// навсегда, и вернуть их оттуда можно только надгробием по каждому ключу.
// Разбирать такое по одной машине руками никто не станет.
func вывестиИзУчёта(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("retire", flag.ExitOnError)
	host := fs.String("host", "", "идентификатор машины")
	name := fs.String("name", "", "имя машины вместо идентификатора")
	file := fs.String("file", "", "файл со списком идентификаторов, по одному в строке")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ids, err := идентификаторыМашин(ctx, pool, *host, *name, *file)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()

	now := time.Now().UTC()
	var известных, надгробий int
	for _, id := range ids {
		r, err := retire.Host(ctx, pool, id, now)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if r.Known {
			известных++
			if len(ids) == 1 {
				fmt.Printf("машина %s (%s) выведена из учёта\n", пусто(r.Hostname), id)
			}
		} else {
			надгробий++
			if len(ids) == 1 {
				fmt.Printf("машины %s в службе нет — отправлено надгробие в ленту\n", id)
			}
		}
	}

	if len(ids) > 1 {
		fmt.Printf("выведено из учёта: %d, надгробий по отсутствующим: %d\n", известных, надгробий)
	}
	return nil
}

// идентификаторыМашин собирает список из того, что дал оператор.
func идентификаторыМашин(
	ctx context.Context, pool *storage.Pool, host, name, file string,
) ([]uuid.UUID, error) {
	switch {
	case file != "":
		if host != "" || name != "" {
			return nil, fmt.Errorf("-file не совмещается с -host и -name")
		}
		return изФайла(file)
	case name != "":
		if host != "" {
			return nil, fmt.Errorf("укажите либо -host, либо -name")
		}
		id, err := pool.HostByName(ctx, name)
		if err != nil {
			return nil, err
		}
		return []uuid.UUID{id}, nil
	case host != "":
		id, err := uuid.Parse(host)
		if err != nil {
			return nil, fmt.Errorf("идентификатор машины: %w", err)
		}
		return []uuid.UUID{id}, nil
	default:
		return nil, fmt.Errorf("укажите -host, -name или -file")
	}
}

// изФайла читает идентификаторы построчно, пропуская пустые строки и
// примечания. Строка, которая не разбирается, останавливает всю работу:
// «вывести из учёта половину списка, а про остальных сообщить в конце» — это
// операция, которую потом невозможно повторить безопасно.
func изФайла(path string) ([]uuid.UUID, error) {
	data, err := os.ReadFile(path) //nolint:gosec // файл называет администратор, это и есть смысл команды
	if err != nil {
		return nil, fmt.Errorf("список машин: %w", err)
	}

	var ids []uuid.UUID
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, err := uuid.Parse(line)
		if err != nil {
			return nil, fmt.Errorf("%s, строка %d: %w", path, n+1, err)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s: ни одного идентификатора", path)
	}
	return ids, nil
}

// закрытьОтчёт убирает запись из очереди разбора, не создавая машину.
//
// Третий ответ очереди разбора, которого не хватало: кроме «это вот эта
// машина» и «это новая машина» бывает «это вообще не машина парка» — чужой
// ноутбук в гостевой сети, тестовый контейнер, машина, которую списали
// полгода назад. Без него такой отчёт остаётся в очереди навсегда, а очередь,
// в которой навсегда что-то лежит, перестают открывать.
func закрытьОтчёт(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("dismiss", flag.ExitOnError)
	report := fs.String("report", "", "идентификатор отложенного отчёта")
	all := fs.Bool("all", false, "закрыть всю очередь разбора")
	reason := fs.String("reason", "", "почему закрыт")
	by := fs.String("by", "администратор", "кто закрыл")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*report == "") == !*all {
		return fmt.Errorf("нужен либо -report с отчётом, либо -all, но не оба сразу")
	}
	if *reason == "" {
		return fmt.Errorf("нужна причина: -reason «чужой ноутбук», «след наладки»")
	}

	now := time.Now().UTC()

	if *all {
		n, err := pool.DismissAllUnresolved(ctx, *by, *reason, now)
		if err != nil {
			return err
		}
		fmt.Printf("закрыто отчётов: %d\n", n)
		return nil
	}

	// Достаточно того, что напечатала очередь разбора: человек закрывает
	// именно тот отчёт, который видит на экране.
	reportID, err := uuid.Parse(*report)
	if err != nil {
		if reportID, err = pool.FindUnresolvedByPrefix(ctx, *report); err != nil {
			return err
		}
	}

	if err := pool.DismissUnresolved(ctx, reportID, *by, *reason, now); err != nil {
		return err
	}

	fmt.Printf("отчёт %s закрыт без заведения машины\n", reportID)
	return nil
}
