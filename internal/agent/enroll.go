package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"
)

// ErrNotEnrolled — машина ещё не зарегистрирована, а токена раскатки нет.
var ErrNotEnrolled = errors.New("машина не зарегистрирована и токен раскатки не задан")

// EnsureEnrolled доводит машину до состояния «есть ключ».
//
// Возвращает состояние, которое уже сохранено на диске. Сохранение сразу же,
// а не потом, принципиально: ключ выдаётся службой ОДИН раз, и агент, упавший
// между получением и записью, потерял бы его навсегда — понадобилась бы
// повторная регистрация руками.
func EnsureEnrolled(
	ctx context.Context, cfg Config, statePath string, state State, token, hostname string,
	log *slog.Logger,
) (State, error) {
	if state.Enrolled() {
		return state, nil
	}
	if token == "" {
		return state, ErrNotEnrolled
	}

	client := NewClient(cfg.Server, "", cfg.Version, cfg.HTTPTimeout)

	result, err := регистрироваться(ctx, client, token, state, hostname, log)
	if err != nil {
		return state, fmt.Errorf("регистрация: %w", err)
	}

	state.AgentKey = result.AgentKey
	state.EnrolledAt = time.Now().UTC()

	if err := SaveState(statePath, state); err != nil {
		// Ключ получен, но не сохранён. Молчать нельзя: следующий запуск
		// зарегистрируется заново, старый ключ останется в базе висеть, и
		// откуда взялись два агента на одну машину, никто не поймёт.
		return state, fmt.Errorf("ключ получен, но не сохранён: %w", err)
	}

	log.Info("машина зарегистрирована",
		"agent_id", state.AgentID, "rotated", result.Rotated)
	return state, nil
}

// Сколько раз пробовать зарегистрироваться и с какими паузами.
//
// Найдено нагрузочным прогоном: раскатка групповой политикой поднимает агента
// на сотнях машин одновременно, служба отвечает «слишком часто», и без
// повторов установка на большинстве машин заканчивается ничем. Формально
// служба Windows перезапустится по действиям при сбое — через минуту, пять и
// пятнадцать, — но выглядеть это будет как сломанная раскатка.
//
// Паузы растут и разъезжаются случайным сдвигом: сотня агентов, повторивших
// ровно через десять секунд, снова придёт залпом.
var паузыРегистрации = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	45 * time.Second,
	2 * time.Minute,
}

func регистрироваться(
	ctx context.Context, client *Client, token string, state State, hostname string,
	log *slog.Logger,
) (EnrollResult, error) {
	var last error

	for попытка := 0; ; попытка++ {
		result, err := client.Enroll(ctx, token, state, hostname)
		if err == nil {
			return result, nil
		}
		last = err

		// Повторяем только временные неудачи. Отказ службы («токен не принят»)
		// повтором не лечится, и настойчивость здесь означала бы только шум в
		// журнале и лишнюю нагрузку.
		if !errors.Is(err, ErrTemporary) || попытка >= len(паузыРегистрации) {
			return EnrollResult{}, last
		}

		пауза := паузыРегистрации[попытка] + разброс(паузыРегистрации[попытка])
		log.Warn("регистрация не удалась, повторим",
			"err", err, "через", пауза.Round(time.Second).String())

		select {
		case <-time.After(пауза):
		case <-ctx.Done():
			return EnrollResult{}, ctx.Err()
		}
	}
}

// разброс — случайная добавка до десятой части паузы.
func разброс(d time.Duration) time.Duration {
	часть := int64(d) / 10
	if часть <= 0 {
		return 0
	}
	//nolint:gosec // G404: разброс расписания не секрет, криптостойкость не нужна
	return time.Duration(rand.Int63n(часть))
}
