// Package metrics — счётчики службы для наблюдения снаружи.
//
// Отвечают на четыре вопроса, которые задают, когда что-то не так:
//
//	сколько отчётов приняли и сколько отвергли — работает ли приём вообще
//	сколько сообщений ждёт публикации  — успевает ли релей за нагрузкой
//	сколько машин молчит               — не отвалилась ли часть парка
//	сколько времени заняло             — где именно стало медленно
//
// Метрики намеренно скупые. Счётчик, который никто не смотрит, стоит времени
// на каждом запросе и создаёт ощущение наблюдаемости там, где её нет.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Приём отчётов.
var (
	// ReportsTotal — принятые отчёты по исходу.
	//
	// Исход меткой, а не отдельным счётчиком на каждый: «принят», «повтор» и
	// «неопознан» — это три состояния одного процесса, и смотреть на них надо
	// вместе. Отдельные счётчики пришлось бы складывать глазами.
	ReportsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "inventory_reports_total",
		Help: "Принятые отчёты по исходу: accepted, duplicate, unresolved, rejected",
	}, []string{"outcome"})

	// ReportDuration — сколько занял приём одного отчёта.
	//
	// Гистограмма, а не среднее: среднее время приёма скрывает ровно то, ради
	// чего его смотрят. Утренний всплеск — это хвост распределения, а не
	// сдвиг середины.
	ReportDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "inventory_report_seconds",
		Help:    "Время приёма одного отчёта",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	})
)

// Лента.
var (
	// OutboxPending — сколько сообщений ждёт публикации.
	//
	// Главная метрика службы. Растущая очередь означает, что лента недоступна
	// или релей не успевает; при этом отчёты продолжают приниматься, и
	// снаружи всё выглядит здоровым — до тех пор, пока потребитель не
	// заметит, что данные отстали на сутки.
	OutboxPending = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "inventory_outbox_pending",
		Help: "Сообщений в исходящей очереди, ещё не опубликованных",
	})

	// OutboxLagSeconds — возраст самого старого неопубликованного сообщения.
	//
	// Именно то, что в условиях приёмки названо «лагом потребителя». Число
	// сообщений само по себе ничего не говорит: сто сообщений, накопившихся
	// за секунду, — это норма, а одно, лежащее час, — уже неполадка.
	OutboxLagSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "inventory_outbox_lag_seconds",
		Help: "Возраст самого старого неопубликованного сообщения",
	})

	// PublishedTotal — опубликованные сообщения по топикам.
	PublishedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "inventory_published_total",
		Help: "Опубликованные в ленту сообщения",
	}, []string{"topic"})

	// PublishFailuresTotal — неудачные попытки публикации.
	PublishFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "inventory_publish_failures_total",
		Help: "Неудачные попытки публикации в ленту",
	})
)

// Парк.
var (
	// HostsTotal — машин в реестре службы.
	HostsTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "inventory_hosts_total",
		Help: "Машин в реестре службы",
	})

	// HostsSilent — машин, не выходивших на связь дольше порога.
	HostsSilent = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "inventory_hosts_silent",
		Help: "Машин, не выходивших на связь дольше порога",
	})

	// OldestReportSeconds — возраст самого старого отчёта по парку.
	//
	// «Возраст последнего отчёта по парку» из условий этапа. Отвечает на
	// вопрос «насколько устарела картина в целом», а не «кто именно молчит».
	OldestReportSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "inventory_oldest_report_seconds",
		Help: "Возраст самого старого отчёта среди действующих машин",
	})

	// UnresolvedTotal — машин в очереди разбора.
	UnresolvedTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "inventory_unresolved_total",
		Help: "Отчётов в очереди разбора",
	})
)

// Задания.
var (
	// CommandsPending — заданий ждёт агентов.
	CommandsPending = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "inventory_commands_pending",
		Help: "Заданий, ожидающих выдачи агентам",
	})
)
