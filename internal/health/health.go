// Пакет health отвечает на вопрос «что со службой не так» одним списком.
//
// Те же четыре проверки, что и в тревогах Prometheus, но вычисленные самой
// службой и отданные по запросу. Нужно это не вместо мониторинга, а рядом с
// ним: мониторинг разворачивают не везде и не сразу, а человек, который
// открыл экран инвентаризации и видит устаревшие данные, должен узнать
// причину там же, а не идти за ней в другую систему.
//
// Пороги повторяют deploy/alerts.yml намеренно и держатся здесь же рядом:
// два места с разными числами — это две разные правды об одной службе.
package health

import (
	"context"
	"fmt"
	"time"

	"github.com/broton07/inventory_service/internal/storage"
)

// Уровень важности.
const (
	LevelInfo     = "info"
	LevelWarning  = "warning"
	LevelCritical = "critical"
)

// Пороги. Те же, что в deploy/alerts.yml.
const (
	// ЛагЛенты — сколько сообщение может ждать публикации, прежде чем это
	// станет неполадкой. Пять минут: релей работает раз в секунду, и всё,
	// что старше, означает не занятость, а поломку.
	ЛагЛенты = 5 * time.Minute

	// ТишинаПриёма — сколько парк может молчать целиком. Полчаса: пятьсот
	// машин отчитываются круглосуточно, и полная тишина означает сломанный
	// приём, а не то, что все ушли домой.
	ТишинаПриёма = 30 * time.Minute

	// ДоляМолчащих — какая часть парка может не выходить на связь. В долях,
	// а не в штуках: пять молчащих из пятисот — это отпуска и ремонт, пять
	// из десяти — сломанная раскатка.
	ДоляМолчащих = 0.2

	// ОчередьРазбора — сколько машин могут ждать человека, прежде чем это
	// перестанет быть текущей работой и станет забытой очередью.
	ОчередьРазбора = 50
)

// Problem — одна неполадка словами человека.
type Problem struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Summary string `json:"summary"`

	// Advice — что с этим делать. Тревога без ответа на вопрос «и что
	// теперь» через месяц перестаёт читаться вместе с полезными.
	Advice string `json:"advice"`
}

// Report — состояние службы одним ответом.
type Report struct {
	CheckedAt time.Time `json:"checked_at"`

	Hosts       int64 `json:"hosts"`
	HostsSilent int64 `json:"hosts_silent"`
	Unresolved  int64 `json:"unresolved"`

	OutboxPending    int64   `json:"outbox_pending"`
	OutboxLagSeconds float64 `json:"outbox_lag_seconds"`

	// OldestReportSeconds — сколько прошло с отчёта самой давно
	// отчитывавшейся живой машины. Показатель парка.
	OldestReportSeconds float64 `json:"oldest_report_seconds"`

	// LastReportSeconds — сколько прошло с последнего отчёта вообще.
	// Показатель приёма.
	LastReportSeconds float64 `json:"last_report_seconds"`

	Problems []Problem `json:"problems"`
}

// Source — то, что нужно от хранилища.
type Source interface {
	Snapshot(ctx context.Context) (storage.Stats, error)
}

// Check собирает состояние службы и список неполадок.
func Check(ctx context.Context, src Source, now time.Time) (Report, error) {
	stats, err := src.Snapshot(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("состояние службы: %w", err)
	}

	r := Report{
		CheckedAt:           now,
		Hosts:               stats.Hosts,
		HostsSilent:         stats.HostsSilent,
		Unresolved:          stats.Unresolved,
		OutboxPending:       stats.OutboxPending,
		OutboxLagSeconds:    stats.OutboxLag.Seconds(),
		OldestReportSeconds: stats.OldestReport.Seconds(),
		LastReportSeconds:   stats.LastReport.Seconds(),
		Problems:            []Problem{},
	}

	if stats.OutboxLag > ЛагЛенты {
		r.Problems = append(r.Problems, Problem{
			Code:    "лента-отстаёт",
			Level:   LevelCritical,
			Summary: fmt.Sprintf("Сообщения не уходят в ленту %s", коротко(stats.OutboxLag)),
			Advice: "Служба принимает отчёты, но не публикует их: до потребителей " +
				"ничего не доходит. Обычная причина — недоступная лента.",
		})
	}

	// Приём отчётов прекратился.
	//
	// Считается по ПОСЛЕДНЕМУ отчёту, а не по самому старому. Разница
	// принципиальная, и первая версия этой проверки её не делала: она брала
	// самую давно отчитывавшуюся машину и на живой службе объявляла «ни
	// одного отчёта четыре дня» в тот момент, когда отчёты шли каждые
	// несколько минут. Пока отчитывается хоть кто-то, приём работает,
	// сколько бы ни молчали остальные — а молчащих считает своя проверка.
	//
	// Пустой парк неполадкой не считается: службу только что подняли.
	if stats.Hosts > 0 && stats.LastReport > ТишинаПриёма {
		r.Problems = append(r.Problems, Problem{
			Code:    "отчёты-не-приходят",
			Level:   LevelWarning,
			Summary: fmt.Sprintf("Ни одного отчёта уже %s", коротко(stats.LastReport)),
			Advice: "Машины отчитываются круглосуточно, и полная тишина означает " +
				"сломанный приём, а не то, что все ушли домой.",
		})
	}

	if stats.Hosts > 0 && float64(stats.HostsSilent)/float64(stats.Hosts) > ДоляМолчащих {
		r.Problems = append(r.Problems, Problem{
			Code:  "парк-замолчал",
			Level: LevelWarning,
			Summary: fmt.Sprintf("Не выходят на связь %d машин из %d",
				stats.HostsSilent, stats.Hosts),
			Advice: "Несколько машин из сотни — это отпуска и ремонт. Пятая часть " +
				"парка — это чаще всего сломанная раскатка или недоступная служба.",
		})
	}

	if stats.Unresolved > ОчередьРазбора {
		r.Problems = append(r.Problems, Problem{
			Code:    "очередь-разбора",
			Level:   LevelInfo,
			Summary: fmt.Sprintf("%d машин ждут разбора человеком", stats.Unresolved),
			Advice: "Их сведения в учёт не попадают, пока их не связали с " +
				"карточками или не закрыли как чужие.",
		})
	}

	return r, nil
}

// коротко переводит длительность в слова: «6 ч», а не «6h13m52.4s».
func коротко(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d сек.", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d мин.", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн.", int(d.Hours()/24))
	}
}
