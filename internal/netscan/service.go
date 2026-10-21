package netscan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/broton07/inventory_service/internal/publish"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

// ErrInvalid — снимок устройства не прошёл проверку контрактом.
var ErrInvalid = errors.New("снимок устройства негоден")

// Store — то, что приёму снимков нужно от базы.
type Store interface {
	SaveNetworkDevice(
		ctx context.Context,
		dev storage.NetworkDeviceRecord,
		now time.Time,
		build func(storage.SavedNetworkDevice) ([]storage.OutboxMessage, error),
	) (storage.SavedNetworkDevice, error)
	MarkNetworkFailure(ctx context.Context, ip, reason string, now time.Time) error
	MarkNetworkSilent(
		ctx context.Context,
		before time.Time,
		build func(storage.SilentNetworkDevice) (storage.OutboxMessage, error),
	) ([]storage.SilentNetworkDevice, error)
}

// Service принимает снимки устройств — от живого опроса и из файлов.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService создаёт приём. now подменяется в тестах.
func NewService(store Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}
}

// Result — что произошло со снимком.
type Result struct {
	DeviceID  string
	FirstSeen bool
	// Changed — снимок отличается от прежнего, и в ленту ушло новое состояние.
	Changed bool
	// Returned — устройство числилось молчащим и снова ответило.
	Returned bool
}

// Accept проверяет снимок и записывает его вместе с сообщением в ленту.
//
// Сообщение — только при фактическом расхождении (I9). Опрос раз в полчаса
// при неизменной сети не должен гнать в ленту сотню одинаковых состояний.
func (s *Service) Accept(ctx context.Context, dev contract.NetworkDevice) (Result, error) {
	now := s.now().UTC()

	if err := dev.Validate(now); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	payload, err := json.Marshal(dev)
	if err != nil {
		return Result{}, fmt.Errorf("сериализация снимка: %w", err)
	}
	hash, err := Fingerprint(dev)
	if err != nil {
		return Result{}, err
	}

	record := storage.NetworkDeviceRecord{
		DeviceID:     dev.DeviceID,
		ManagementIP: dev.ManagementIP,
		SysName:      dev.SysName,
		CollectedAt:  dev.CollectedAt,
		Payload:      payload,
		Hash:         hash,
	}

	saved, err := s.store.SaveNetworkDevice(ctx, record, now,
		func(saved storage.SavedNetworkDevice) ([]storage.OutboxMessage, error) {
			// Возвращение на связь публикуется и без изменений в снимке:
			// пометку о молчании у потребителей снимает только новое
			// сообщение.
			if !saved.Changed && !saved.Returned {
				return nil, nil
			}
			state := contract.NetworkDeviceState{
				SchemaVersion: contract.SchemaVersion,
				DeviceID:      dev.DeviceID,
				FirstSeenAt:   saved.FirstSeenAt,
				LastPolledAt:  now,
				Device:        dev,
			}
			body, err := json.Marshal(state)
			if err != nil {
				return nil, fmt.Errorf("сериализация состояния: %w", err)
			}
			return []storage.OutboxMessage{{
				Topic:   publish.TopicNetwork,
				Key:     dev.DeviceID, // как и у машин: порядок по устройству
				Payload: body,
			}}, nil
		})
	if err != nil {
		return Result{}, err
	}

	return Result{
		DeviceID: dev.DeviceID, FirstSeen: saved.FirstSeen,
		Changed: saved.Changed, Returned: saved.Returned,
	}, nil
}

// Failed отмечает, что устройство по адресу не ответило.
func (s *Service) Failed(ctx context.Context, ip string, reason error) error {
	return s.store.MarkNetworkFailure(ctx, ip, reason.Error(), s.now().UTC())
}

// MarkSilent публикует молчание устройств, не отвечающих дольше threshold.
//
// Порог больше периода опроса нарочно: коммутатор, перезагрузившийся во
// время одного прохода, молчащим не считается.
func (s *Service) MarkSilent(ctx context.Context, threshold time.Duration) ([]storage.SilentNetworkDevice, error) {
	now := s.now().UTC()
	return s.store.MarkNetworkSilent(ctx, now.Add(-threshold),
		func(d storage.SilentNetworkDevice) (storage.OutboxMessage, error) {
			var dev contract.NetworkDevice
			if err := json.Unmarshal(d.Payload, &dev); err != nil {
				return storage.OutboxMessage{}, fmt.Errorf("снимок устройства %s: %w", d.DeviceID, err)
			}
			since := d.SilentSince.UTC()
			state := contract.NetworkDeviceState{
				SchemaVersion: contract.SchemaVersion,
				DeviceID:      d.DeviceID,
				FirstSeenAt:   d.FirstSeenAt,
				LastPolledAt:  d.LastPolledAt,
				SilentSince:   &since,
				Device:        dev,
			}
			body, err := json.Marshal(state)
			if err != nil {
				return storage.OutboxMessage{}, fmt.Errorf("сериализация состояния: %w", err)
			}
			return storage.OutboxMessage{Topic: publish.TopicNetwork, Key: d.DeviceID, Payload: body}, nil
		})
}

// Fingerprint — отпечаток содержимого снимка без времени сбора.
//
// Время меняется при каждом опросе, а изменением сети не является; с ним в
// отпечатке каждый опрос считался бы расхождением.
func Fingerprint(dev contract.NetworkDevice) (string, error) {
	dev.CollectedAt = time.Time{}
	body, err := json.Marshal(dev)
	if err != nil {
		return "", fmt.Errorf("отпечаток снимка: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// ------------------------------------------------------------------
// Периодический опрос
// ------------------------------------------------------------------

// Dialer готовит обход устройства по адресу.
type Dialer func(target string) (Walker, error)

// TargetList — список опроса, меняющийся на ходу, и журнал попыток.
//
// Список перечитывается каждый проход: внешняя система прислала новый — со
// следующего прохода опрашивается он, без перезапуска службы.
type TargetList interface {
	ListNetworkTargets(ctx context.Context) ([]storage.NetworkTargetRow, error)
	RecordNetworkAttempt(ctx context.Context, ip, deviceID, errText string, now time.Time) error
}

// Runner опрашивает список устройств по расписанию.
type Runner struct {
	service  *Service
	dial     Dialer
	targets  []string
	list     TargetList
	interval time.Duration
	timeout  time.Duration
	silence  time.Duration
	log      *slog.Logger
	now      func() time.Time
	kick     chan struct{}
}

// RunnerOptions — настройки опроса.
type RunnerOptions struct {
	// Targets — адреса из настроек, опрашиваются всегда.
	Targets []string
	// List — адреса из базы; nil — только Targets.
	List     TargetList
	Interval time.Duration
	// Timeout — срок на одно устройство целиком, все обходы вместе (I11).
	Timeout time.Duration
	// Silence — сколько устройство должно не отвечать, чтобы считаться
	// молчащим. По умолчанию — четыре периода опроса.
	Silence time.Duration
	Logger  *slog.Logger
	Now     func() time.Time
}

// NewRunner создаёт периодический опрос.
func NewRunner(service *Service, dial Dialer, opts RunnerOptions) *Runner {
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Minute
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.Silence <= 0 {
		opts.Silence = 4 * opts.Interval
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Runner{
		service: service, dial: dial, targets: opts.Targets, list: opts.List,
		interval: opts.Interval, timeout: opts.Timeout, silence: opts.Silence,
		log: opts.Logger.With("component", "netscan"), now: opts.Now,
		kick: make(chan struct{}, 1),
	}
}

// Interval — период опроса.
func (r *Runner) Interval() time.Duration { return r.interval }

// Kick просит пройти по списку сейчас, не дожидаясь периода.
//
// Возвращает false, если просьба уже стоит в очереди: десять нажатий кнопки
// — один внеочередной проход, а не десять подряд.
func (r *Runner) Kick() bool {
	select {
	case r.kick <- struct{}{}:
		return true
	default:
		return false
	}
}

// Run крутит опрос до отмены контекста.
func (r *Runner) Run(ctx context.Context) {
	r.log.Info("опрос сетевых устройств запущен",
		"targets", len(r.targets), "from_db", r.list != nil, "interval", r.interval)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		r.Tick(ctx)

		select {
		case <-ctx.Done():
			r.log.Info("опрос сетевых устройств остановлен")
			return
		case <-ticker.C:
		case <-r.kick:
			r.log.Info("внеочередной проход опроса")
		}
	}
}

// current — что опрашивать в этом проходе: настройки и база, без повторов.
func (r *Runner) current(ctx context.Context) []string {
	seen := make(map[string]bool, len(r.targets))
	out := make([]string, 0, len(r.targets))
	for _, t := range r.targets {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if r.list == nil {
		return out
	}
	rows, err := r.list.ListNetworkTargets(ctx)
	if err != nil {
		// База недоступна — опрашивается хотя бы то, что в настройках.
		r.log.Error("список адресов опроса не прочитан", "err", err)
		return out
	}
	for _, row := range rows {
		if !seen[row.IP] {
			seen[row.IP] = true
			out = append(out, row.IP)
		}
	}
	return out
}

// record пишет итог попытки в список; ошибка записи опрос не останавливает.
func (r *Runner) record(ctx context.Context, ip, deviceID string, failure error) {
	if r.list == nil {
		return
	}
	text := ""
	if failure != nil {
		text = failure.Error()
	}
	if err := r.list.RecordNetworkAttempt(ctx, ip, deviceID, text, r.now().UTC()); err != nil {
		r.log.Error("итог опроса не записан", "target", ip, "err", err)
	}
}

// TickStats — итог одного прохода.
type TickStats struct {
	Polled, Changed, Failed int
	// Silent — сколько устройств в этом проходе признано молчащими.
	Silent int
}

// Tick — один проход по всем устройствам.
//
// По одному, а не параллельно: сотня коммутаторов за полчаса — это
// несколько запросов в секунду, а одновременный GETBULK по всей сети нагружает
// процессоры коммутаторов, у которых он и так слабый. Недоступное устройство
// не останавливает проход: оно пишется в журнал, остальные опрашиваются.
func (r *Runner) Tick(ctx context.Context) TickStats {
	var stats TickStats

	for _, target := range r.current(ctx) {
		if ctx.Err() != nil {
			return stats
		}
		result, err := r.pollOne(ctx, target)
		if err != nil {
			stats.Failed++
			r.log.Warn("устройство не опрошено", "target", target, "err", err)
			if ferr := r.service.Failed(ctx, target, err); ferr != nil {
				r.log.Error("не удалось отметить неудачный опрос", "target", target, "err", ferr)
			}
			r.record(ctx, target, "", err)
			continue
		}
		r.record(ctx, target, result.DeviceID, nil)
		stats.Polled++
		if result.Changed {
			stats.Changed++
		}
	}

	silent, err := r.service.MarkSilent(ctx, r.silence)
	if err != nil {
		r.log.Error("проверка молчащих устройств не удалась", "err", err)
	}
	stats.Silent = len(silent)
	for _, d := range silent {
		r.log.Warn("устройство не отвечает дольше порога",
			"target", d.ManagementIP, "since", d.SilentSince, "err", d.LastError)
	}

	r.log.Info("проход опроса завершён",
		"polled", stats.Polled, "changed", stats.Changed, "failed", stats.Failed,
		"silent", stats.Silent)
	return stats
}

func (r *Runner) pollOne(ctx context.Context, target string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	walker, err := r.dial(target)
	if err != nil {
		return Result{}, err
	}
	dev, err := Collect(ctx, walker, target, r.now())
	if err != nil {
		return Result{}, err
	}
	return r.service.Accept(ctx, dev)
}
