package agent

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

// Значения по умолчанию для того, что не задано в настройках.
const (
	// DefaultInterval — как часто снимаются сведения.
	//
	// Четыре часа: оборудование меняется реже, чем раз в день, а установленные
	// программы — реже, чем раз в час. Чаще — это нагрузка ради нагрузки;
	// реже — и замену диска увидят через неделю.
	DefaultInterval = 4 * time.Hour

	// DefaultPollInterval — как часто агент спрашивает про задания.
	//
	// Отсюда берётся время отклика кнопки «Обновить сейчас»: человек нажал —
	// машина отзовётся в течение минуты.
	DefaultPollInterval = time.Minute

	// DefaultTick — шаг главного цикла.
	DefaultTick = 10 * time.Second

	// DefaultHTTPTimeout — сколько ждать ответа службы.
	DefaultHTTPTimeout = 30 * time.Second

	// CollectTimeout — сколько отводится на сам сбор.
	//
	// WMI на нездоровой машине умеет висеть бесконечно, и без предела агент
	// однажды просто перестал бы отчитываться, ничего об этом не сообщив.
	CollectTimeout = 2 * time.Minute

	// MaxSpoolAge — насколько старый отчёт ещё имеет смысл досылать.
	MaxSpoolAge = 30 * 24 * time.Hour
)

// Пределы повторов при недоступной службе.
const (
	minBackoff = time.Minute
	maxBackoff = 30 * time.Minute
)

// Config — настройки агента.
type Config struct {
	Server       string
	DataDir      string
	Interval     time.Duration
	PollInterval time.Duration
	Tick         time.Duration
	HTTPTimeout  time.Duration
	Version      string
}

// Collector — сбор сведений о машине.
//
// Интерфейсом, а не прямым вызовом: настоящий сбор идёт через WMI и
// проверяется только на живой Windows. Подменяемый сбор позволяет прогнать
// весь остальной агент — расписание, буфер, повторы, задания — обычными
// тестами.
type Collector func(ctx context.Context, mark, version string, origin contract.Origin) contract.Snapshot

// Agent — сам агент: расписание, отправка, буфер и задания.
type Agent struct {
	cfg     Config
	state   State
	client  *Client
	spool   *Spool
	collect Collector
	log     *slog.Logger
	now     func() time.Time
	rng     *rand.Rand

	// statePath — куда писать состояние, если его поменяет задание.
	statePath string

	nextCollect time.Time
	nextPoll    time.Time
	nextRetry   time.Time
	failures    int
}

// New собирает агента. Состояние уже должно быть загружено и
// зарегистрировано: без ключа отчёты не примут.
func New(cfg Config, state State, statePath string, spool *Spool, collect Collector, log *slog.Logger) *Agent {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.Tick <= 0 {
		cfg.Tick = DefaultTick
	}
	// Шаг цикла не может быть грубее периода опроса: иначе настройка «спрашивать
	// про задания раз в пять секунд» тихо превращается в «раз в десять» —
	// человек её задал, а она не действует.
	if cfg.Tick > cfg.PollInterval {
		cfg.Tick = cfg.PollInterval
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = DefaultHTTPTimeout
	}

	return &Agent{
		cfg:       cfg,
		state:     state,
		statePath: statePath,
		client:    NewClient(cfg.Server, state.AgentKey, cfg.Version, cfg.HTTPTimeout),
		spool:     spool,
		collect:   collect,
		log:       log,
		now:       time.Now,
		//nolint:gosec // G404: разброс расписания не секрет, криптостойкость здесь не нужна
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Run крутит главный цикл, пока не отменят контекст.
func (a *Agent) Run(ctx context.Context) error {
	now := a.now()

	// Первый сбор — не сразу.
	//
	// Пятьсот машин включаются утром в одни и те же десять минут, и сбор
	// «сразу при старте» превратил бы это в залп из пятисот отчётов по
	// свежезагруженной службе. Разброс размазывает залп.
	a.nextCollect = now.Add(a.startDelay())
	a.nextPoll = now.Add(a.jitter(a.cfg.PollInterval))

	// Сроки — строками, а не как есть: slog печатает time.Duration числом
	// наносекунд, и в журнале вместо «4h0m0s» стоит 14400000000000. Журнал
	// читает человек, у которого что-то не работает.
	a.log.Info("агент запущен",
		"server", a.cfg.Server,
		"interval", a.interval().String(),
		"first_collect_in", a.nextCollect.Sub(now).Round(time.Second).String(),
		"spooled", a.spool.Len())

	ticker := time.NewTicker(a.cfg.Tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.log.Info("агент остановлен")
			return nil
		case <-ticker.C:
			a.Tick(ctx, a.now())
		}
	}
}

// Tick — один проход цикла: делается то, чему подошёл срок.
//
// Вынесен отдельно от Run намеренно: так расписание, буфер и повторы
// проверяются тестами без ожидания настоящих четырёх часов.
func (a *Agent) Tick(ctx context.Context, now time.Time) {
	if !a.nextPoll.IsZero() && !now.Before(a.nextPoll) {
		a.nextPoll = now.Add(a.jitter(a.cfg.PollInterval))
		a.poll(ctx)
	}

	if !a.nextCollect.IsZero() && !now.Before(a.nextCollect) {
		a.nextCollect = now.Add(a.jitter(a.interval()))
		a.CollectAndSend(ctx, now)
	}

	// Повтор досылки — отдельно от расписания сбора: снимать заново нечего,
	// нужно лишь отправить накопленное.
	if !a.nextRetry.IsZero() && !now.Before(a.nextRetry) {
		a.nextRetry = time.Time{}
		a.drain(ctx, now)
	}
}

// CollectAndSend снимает сведения и пытается их отправить.
func (a *Agent) CollectAndSend(ctx context.Context, now time.Time) {
	// У сбора свой срок, не связанный со сроком отправки: висящий WMI не
	// должен выглядеть как недоступная служба.
	собрать, cancel := context.WithTimeout(ctx, CollectTimeout)
	snap := a.collect(собрать, a.state.AgentID, a.cfg.Version, contract.OriginNetwork)
	cancel()

	if len(snap.Failures) > 0 {
		// Не ошибка агента: частичный сбор — норма. Но в журнале это должно
		// быть видно, иначе «почему у машины нет дисков» не отвечается.
		a.log.Warn("собрано не всё", "failures", snap.Failures)
	}

	// Сначала накопленное, потом свежее: служба сравнивает снимки по времени
	// сбора, и отправить свежий раньше старых значит заставить её отбросить
	// старые как устаревшие.
	a.drain(ctx, now)

	if err := a.send(ctx, snap); err != nil {
		a.spoolIt(snap, err, now)
		return
	}
	a.success()
}

// send отправляет один отчёт, различая исходы.
func (a *Agent) send(ctx context.Context, snap contract.Snapshot) error {
	err := a.client.Send(ctx, snap)
	switch {
	case err == nil:
		a.log.Info("отчёт отправлен", "collected_at", snap.CollectedAt)
		return nil

	case errors.Is(err, ErrKeyDenied):
		// Копить отчёты бессмысленно: ключ не примут и завтра. Чинится
		// только человеком — перерегистрацией машины.
		a.log.Error("ключ машины не признан службой: нужна повторная регистрация", "err", err)
		return err

	case errors.Is(err, ErrRejected):
		// Отчёт негоден. Повтор ничего не изменит, и складывать его в
		// очередь — значит забить её навсегда.
		a.log.Error("служба отвергла отчёт", "err", err)
		return err

	default:
		return err
	}
}

// spoolIt откладывает отчёт, если его есть смысл дослать.
func (a *Agent) spoolIt(snap contract.Snapshot, err error, now time.Time) {
	if errors.Is(err, ErrRejected) || errors.Is(err, ErrKeyDenied) {
		// Отвергнутое не откладывается: повтор будет отвергнут так же.
		return
	}

	if err := a.spool.Add(snap); err != nil {
		// Диск полон или каталог недоступен. Терять нечего — отчёт всё
		// равно не отправлен, — но знать об этом надо.
		a.log.Error("не удалось отложить отчёт", "err", err)
		return
	}

	a.failure(now)
	a.log.Warn("служба недоступна, отчёт отложен",
		"spooled", a.spool.Len(), "next_try", a.nextRetry.Format(time.RFC3339))
}

// drain досылает накопленное, старое первым.
func (a *Agent) drain(ctx context.Context, now time.Time) {
	// Слишком старые не отправляются вовсе: служба их не примет, а агент
	// считал бы отказ неполадкой связи и повторял вечно.
	if dropped, err := a.spool.DropOlderThan(now, MaxSpoolAge); err == nil && dropped > 0 {
		a.log.Warn("выброшены устаревшие отчёты", "count", dropped)
	}

	files, err := a.spool.Files()
	if err != nil {
		a.log.Error("очередь отчётов недоступна", "err", err)
		return
	}
	if len(files) == 0 {
		return
	}

	отправлено := 0
	for _, f := range files {
		if ctx.Err() != nil {
			return
		}

		snap, err := a.spool.Read(f)
		if err != nil {
			// Испорченный файл выбрасывается: отправить его нельзя, а
			// занимать очередь он будет вечно.
			a.log.Error("отчёт в очереди испорчен, выброшен", "file", f, "err", err)
			_ = a.spool.Remove(f)
			continue
		}

		if err := a.send(ctx, snap); err != nil {
			if errors.Is(err, ErrRejected) {
				_ = a.spool.Remove(f)
				continue
			}
			// Связи снова нет — остальное подождёт следующего раза. Дальше
			// по списку идти незачем: результат будет тот же.
			a.failure(now)
			return
		}

		_ = a.spool.Remove(f)
		отправлено++
	}

	if отправлено > 0 {
		a.log.Info("отложенные отчёты доставлены", "count", отправлено)
		a.success()
	}
}

// poll забирает задания и выполняет их.
func (a *Agent) poll(ctx context.Context) {
	list, err := a.client.Commands(ctx)
	if err != nil {
		// Опрос заданий — самое частое обращение к службе, и писать в журнал
		// каждую минуту при недоступной службе значит забить журнал. Уровень
		// отладки: связь видна по отправке отчётов.
		a.log.Debug("опрос заданий не удался", "err", err)
		return
	}

	for _, cmd := range list {
		if ctx.Err() != nil {
			return
		}
		a.execute(ctx, cmd)
	}
}

// execute выполняет одно задание и отчитывается о результате.
func (a *Agent) execute(ctx context.Context, cmd contract.Command) {
	a.log.Info("получено задание", "id", cmd.ID, "kind", cmd.Kind)

	err := a.do(ctx, cmd)

	result := contract.CommandResult{SchemaVersion: contract.SchemaVersion, OK: err == nil}
	if err != nil {
		result.Error = err.Error()
		a.log.Error("задание не выполнено", "id", cmd.ID, "err", err)
	}

	if err := a.client.Finish(ctx, cmd.ID, result); err != nil {
		// Связь пропала между выполнением и отчётом. Задание вернётся в
		// очередь по истечении аренды, и человек увидит вторую попытку — это
		// лучше, чем считать выполненным то, о чём никто не узнал.
		a.log.Warn("не удалось сообщить о выполнении задания", "id", cmd.ID, "err", err)
	}
}

// do — то место, где закрытый список видов заданий превращается в действие.
//
// Списка два, и они обязаны совпадать: contract.KnownCommandKinds и этот
// switch. Задание, не попавшее ни в одну ветку, не выполняется — не
// «выполняется как умеем», а именно не выполняется (инвариант I6).
func (a *Agent) do(ctx context.Context, cmd contract.Command) error {
	switch cmd.Kind {
	case contract.CommandCollectNow:
		a.CollectAndSend(ctx, a.now())
		// Следующий сбор по расписанию отодвигается: собирать заново через
		// минуту после внеочередного сбора незачем.
		a.nextCollect = a.now().Add(a.jitter(a.interval()))
		return nil

	case contract.CommandSetInterval:
		seconds, ok := cmd.IntervalSeconds()
		if !ok {
			return errors.New("в задании нет периода сбора")
		}
		return a.setInterval(seconds)

	default:
		// Сюда попасть нельзя: задания проверены контрактом при получении.
		// Но если в contract однажды добавят вид, а сюда добавить забудут,
		// отказ лучше молчаливого пропуска.
		return errors.New("агент не умеет выполнять задания такого вида")
	}
}

// setInterval меняет период сбора и запоминает его.
//
// Запоминает обязательно: иначе после первой же перезагрузки машина вернётся
// к прежнему периоду, а человек будет считать, что настройка применена.
func (a *Agent) setInterval(seconds int) error {
	a.state.IntervalSeconds = seconds
	if err := SaveState(a.statePath, a.state); err != nil {
		return err
	}

	a.nextCollect = a.now().Add(a.jitter(a.interval()))
	a.log.Info("период сбора изменён", "interval", a.interval().String())
	return nil
}

// interval — действующий период сбора.
func (a *Agent) interval() time.Duration {
	return a.state.Interval(a.cfg.Interval)
}

// startDelay — задержка первого сбора после запуска.
//
// Не дольше пяти минут: машину включают в том числе ради того, чтобы
// посмотреть, что с ней, и отчёт должен появиться в разумный срок, а не через
// полпериода.
func (a *Agent) startDelay() time.Duration {
	предел := a.interval()
	if предел > 5*time.Minute {
		предел = 5 * time.Minute
	}
	return time.Duration(a.rng.Int63n(int64(предел) + 1))
}

// jitter — разброс вокруг срока, плюс-минус десятая часть.
//
// Без него пятьсот агентов, запущенных групповой политикой в одну минуту, так
// и ходили бы в службу строем: раз в четыре часа все разом.
func (a *Agent) jitter(d time.Duration) time.Duration {
	разброс := int64(d) / 10
	if разброс <= 0 {
		return d
	}
	return d + time.Duration(a.rng.Int63n(2*разброс)-разброс)
}

// failure отмечает неудачу связи и назначает следующую попытку.
//
// Задержка растёт: служба, лежащая под нагрузкой, не должна получать от
// пятисот агентов по попытке в минуту — это добивает её ровно тогда, когда
// она пытается подняться.
func (a *Agent) failure(now time.Time) {
	a.failures++

	// Сдвиг ограничен восемью: без предела 1<<63 переполняет знак и даёт
	// отрицательную задержку, то есть повтор без всякой паузы.
	задержка := minBackoff << min(a.failures-1, 8)
	if задержка > maxBackoff {
		задержка = maxBackoff
	}

	a.nextRetry = now.Add(a.jitter(задержка))
}

// success сбрасывает счётчик неудач.
func (a *Agent) success() {
	a.failures = 0
	a.nextRetry = time.Time{}
}
