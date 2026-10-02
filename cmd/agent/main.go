// Команда agent — программа, которая живёт на рабочей станции.
//
//	agent run                        обычная работа: сбор по расписанию
//	agent once                       снять и отправить один раз
//	agent once   -out КАТАЛОГ        снять в подписанный файл: машина без сети
//	agent enroll -token enr_...      зарегистрировать машину
//	agent enroll -token enr_... -out КАТАЛОГ   заявка файлом: машина без сети
//	agent install / uninstall        служба Windows
//	agent status                     что агент знает о себе
//
// Один exe на все случаи: раскатывать по парку два файла вместо одного —
// это вдвое больше поводов раскатать не то.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/broton07/inventory_service/internal/agent"
	"github.com/broton07/inventory_service/internal/collect"
	"github.com/broton07/inventory_service/pkg/contract"
)

// Version — версия агента. Попадает в каждый отчёт: после первого же
// обновления в парке живут две-три версии, и на вопрос «почему у этих машин
// нет поля X» отвечает только она.
const Version = "0.9.0"

func main() {
	// Служба Windows запускается без аргументов и разговаривает с системой
	// по своему протоколу. Проверить это надо ДО разбора аргументов: иначе
	// служба увидит пустую команду и напечатает справку в никуда.
	if служебныйЗапуск() {
		if err := runAsService(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// Кодовая страница возвращается до выхода, а не через defer: defer не
	// срабатывает при os.Exit, и консоль осталась бы переключённой уже после
	// того, как программа завершилась.
	вернуть := настроитьКонсоль()
	code := выполнить()
	вернуть()

	os.Exit(code)
}

// выполнить разбирает команду и возвращает код возврата.
func выполнить() int {
	if len(os.Args) < 2 {
		usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "run":
		err = командаRun(ctx, os.Args[2:])
	case "once":
		err = командаOnce(ctx, os.Args[2:])
	case "enroll":
		err = командаEnroll(ctx, os.Args[2:])
	case "status":
		err = командаStatus(os.Args[2:])
	case "install":
		err = командаInstall(os.Args[2:])
	case "uninstall":
		err = командаUninstall(os.Args[2:])
	case "version":
		fmt.Println(Version)
	default:
		usage()
		return 2
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintf(os.Stderr, `agent %s — сбор сведений о рабочей станции

  run        работа по расписанию
  once       снять и отправить один раз; с -out — подписанный файл для флешки
  enroll     зарегистрировать машину; с -out — заявка файлом
  status     что агент знает о себе
  install    поставить службой Windows
  uninstall  снять службу
  version    версия

Каталог данных по умолчанию: %s
`, Version, agent.DefaultDataDir())
}

// общиеФлаги — то, что нужно всем командам.
type общиеФлаги struct {
	fs       *flag.FlagSet
	data     *string
	server   *string
	token    *string
	out      *string
	interval *time.Duration
	poll     *time.Duration
	verbose  *bool
}

func флаги(имя string, args []string) (общиеФлаги, error) {
	fs := flag.NewFlagSet(имя, flag.ExitOnError)
	f := общиеФлаги{
		fs:       fs,
		data:     fs.String("data", agent.DefaultDataDir(), "каталог данных агента"),
		server:   fs.String("server", "", "адрес службы, например http://inventory:8080"),
		token:    fs.String("token", "", "токен раскатки для регистрации"),
		out:      fs.String("out", "", "каталог для файлов машины без сети"),
		interval: fs.Duration("interval", 0, "период сбора"),
		poll:     fs.Duration("poll", 0, "как часто спрашивать про задания"),
		verbose:  fs.Bool("v", false, "писать журнал в консоль"),
	}
	return f, fs.Parse(args)
}

// настройки собирает конфигурацию: файл, поверх него флаги.
//
// Именно в таком порядке: файл кладёт установщик один раз на пятьсот машин, а
// флаг набирает человек, который прямо сейчас разбирается с одной конкретной.
func настройки(f общиеФлаги) (agent.Config, string, error) {
	cfg, token, err := agent.LoadConfig(*f.data, Version)
	if err != nil {
		return cfg, "", err
	}
	if *f.server != "" {
		cfg.Server = *f.server
	}
	if *f.token != "" {
		token = *f.token
	}
	if *f.interval > 0 {
		cfg.Interval = *f.interval
	}
	if *f.poll > 0 {
		cfg.PollInterval = *f.poll
	}
	return cfg, token, nil
}

// журнал — свой файл, и по желанию консоль.
//
// Свой, а не журнал событий Windows: агент ставится и на машины, где к
// журналу событий у человека, который чинит, доступа нет, а к файлу рядом с
// программой — есть.
func журнал(cfg agent.Config, консоль bool) (*slog.Logger, func(), error) {
	w, err := agent.OpenLog(cfg.LogPath())
	if err != nil {
		return nil, nil, err
	}

	var out io.Writer = w
	if консоль {
		out = io.MultiWriter(w, os.Stderr)
	}

	log := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return log, func() { _ = w.Close() }, nil
}

// подготовить доводит агента до рабочего состояния: настройки, состояние,
// регистрация.
func подготовить(ctx context.Context, cfg agent.Config, token string, log *slog.Logger) (agent.State, error) {
	if cfg.Server == "" {
		return agent.State{}, errors.New("не задан адрес службы: -server или config.json")
	}

	state, есть, err := agent.LoadState(cfg.StatePath())
	if err != nil {
		return state, err
	}
	if !есть {
		if state, err = agent.NewState(); err != nil {
			return state, err
		}
		// Отметка сохраняется ДО регистрации: агент, упавший между
		// генерацией и записью, при следующем запуске придумал бы вторую
		// отметку и завёл в учёте вторую карточку той же машины.
		if err := agent.SaveState(cfg.StatePath(), state); err != nil {
			return state, err
		}
		log.Info("заведена отметка агента", "agent_id", state.AgentID)
	}

	былНезарегистрирован := !state.Enrolled()

	state, err = agent.EnsureEnrolled(ctx, cfg, cfg.StatePath(), state, token, имяМашины(), log)
	if err != nil {
		return state, err
	}

	// Токен раскатки стирается сразу после регистрации: он действует на все
	// машины сразу, и пятьсот его копий по рабочим станциям — это пятьсот
	// мест, откуда его можно унести.
	if былНезарегистрирован && token != "" {
		if err := agent.SaveConfig(cfg.DataDir, cfg, ""); err != nil {
			log.Warn("токен раскатки не удалось стереть из настроек", "err", err)
		} else {
			log.Info("токен раскатки стёрт из настроек")
		}
	}

	return state, nil
}

func командаRun(ctx context.Context, args []string) error {
	f, err := флаги("run", args)
	if err != nil {
		return err
	}

	cfg, token, err := настройки(f)
	if err != nil {
		return err
	}

	log, закрыть, err := журнал(cfg, *f.verbose)
	if err != nil {
		return err
	}
	defer закрыть()

	state, err := подготовить(ctx, cfg, token, log)
	if err != nil {
		return err
	}

	return собранный(cfg, state, log).Run(ctx)
}

func командаOnce(ctx context.Context, args []string) error {
	f, err := флаги("once", args)
	if err != nil {
		return err
	}

	cfg, token, err := настройки(f)
	if err != nil {
		return err
	}

	// Машина без сети: снять в файл и отдать человеку с флешкой.
	if *f.out != "" {
		return автономныйОтчёт(ctx, cfg, token, *f.out)
	}

	log, закрыть, err := журнал(cfg, true)
	if err != nil {
		return err
	}
	defer закрыть()

	state, err := подготовить(ctx, cfg, token, log)
	if err != nil {
		return err
	}

	a := собранный(cfg, state, log)
	a.CollectAndSend(ctx, time.Now())
	return nil
}

// автономныйОтчёт снимает сведения в подписанный файл.
func автономныйОтчёт(ctx context.Context, cfg agent.Config, token, out string) error {
	state, есть, err := agent.LoadState(cfg.StatePath())
	if err != nil {
		return err
	}
	if !есть {
		if state, err = agent.NewState(); err != nil {
			return err
		}
	}

	// Машина без сети зарегистрироваться не может — заявка кладётся тем же
	// заходом. Ключ доступа она придумывает себе сама, службе уезжает только
	// его хеш: тот же порядок, что и по сети.
	if !state.Enrolled() {
		if token == "" {
			return errors.New(
				"машина не зарегистрирована: нужен -token с токеном раскатки")
		}

		state, путь, err := agent.WriteClaim(out, state, token, имяМашины(), Version)
		if err != nil {
			return err
		}
		if err := agent.SaveState(cfg.StatePath(), state); err != nil {
			return err
		}
		fmt.Println("заявка на регистрацию:", путь)
	}

	собрать, cancel := context.WithTimeout(ctx, agent.CollectTimeout)
	defer cancel()

	snap := collect.Collect(собрать, state.AgentID, Version, contract.OriginOffline)

	путь, err := agent.WriteReport(out, state, snap)
	if err != nil {
		return err
	}

	fmt.Println("отчёт:", путь)
	if len(snap.Failures) > 0 {
		fmt.Fprintln(os.Stderr, "собрано не всё:")
		for _, f := range snap.Failures {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Section, f.Reason)
		}
	}
	return nil
}

func командаEnroll(ctx context.Context, args []string) error {
	f, err := флаги("enroll", args)
	if err != nil {
		return err
	}

	cfg, token, err := настройки(f)
	if err != nil {
		return err
	}
	if token == "" {
		return errors.New("нужен -token с токеном раскатки")
	}

	// Заявка файлом: машина без сети.
	if *f.out != "" {
		state, есть, err := agent.LoadState(cfg.StatePath())
		if err != nil {
			return err
		}
		if !есть {
			if state, err = agent.NewState(); err != nil {
				return err
			}
		}

		state, путь, err := agent.WriteClaim(*f.out, state, token, имяМашины(), Version)
		if err != nil {
			return err
		}
		if err := agent.SaveState(cfg.StatePath(), state); err != nil {
			return err
		}

		fmt.Println("заявка на регистрацию:", путь)
		return nil
	}

	log, закрыть, err := журнал(cfg, true)
	if err != nil {
		return err
	}
	defer закрыть()

	// Настройки записываются до регистрации: адрес службы, переданный
	// флагом, должен пережить перезагрузку — иначе служба Windows после
	// перезапуска не будет знать, куда ходить.
	if err := agent.SaveConfig(cfg.DataDir, cfg, token); err != nil {
		return err
	}

	state, err := подготовить(ctx, cfg, token, log)
	if err != nil {
		return err
	}

	fmt.Println("машина зарегистрирована, отметка:", state.AgentID)
	return nil
}

func командаStatus(args []string) error {
	f, err := флаги("status", args)
	if err != nil {
		return err
	}

	cfg, token, err := настройки(f)
	if err != nil {
		return err
	}

	state, есть, err := agent.LoadState(cfg.StatePath())
	if err != nil {
		return err
	}

	spool := &agent.Spool{Dir: cfg.SpoolPath()}

	fmt.Printf("версия агента       %s\n", Version)
	fmt.Printf("каталог данных      %s\n", cfg.DataDir)
	fmt.Printf("адрес службы        %s\n", или(cfg.Server, "не задан"))
	if !есть {
		fmt.Println("состояние           агент ещё не запускался")
		return nil
	}

	fmt.Printf("отметка агента      %s\n", state.AgentID)
	fmt.Printf("регистрация         %s\n", регистрация(state))
	fmt.Printf("период сбора        %s\n", state.Interval(cfg.Interval))
	fmt.Printf("отчётов в очереди   %d\n", spool.Len())
	if token != "" {
		// Видно, что установщик оставил токен: он должен был стереться
		// после первой же удачной регистрации.
		fmt.Println("внимание            в настройках лежит неиспользованный токен раскатки")
	}
	return nil
}

func регистрация(s agent.State) string {
	if !s.Enrolled() {
		return "не зарегистрирована"
	}
	return "есть ключ, с " + s.EnrolledAt.Local().Format("2006-01-02 15:04")
}

func или(v, запасное string) string {
	if v == "" {
		return запасное
	}
	return v
}

// собранный складывает агента из частей.
func собранный(cfg agent.Config, state agent.State, log *slog.Logger) *agent.Agent {
	return agent.New(cfg, state, cfg.StatePath(),
		&agent.Spool{Dir: cfg.SpoolPath(), Max: agent.MaxSpooled},
		collect.Collect, log)
}

// имяМашины — имя, под которым машина известна сети.
func имяМашины() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
