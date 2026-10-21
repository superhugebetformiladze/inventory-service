// Package httpapi — HTTP-слой службы: маршруты и обработчики.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// DefaultEnrollBurst — сколько регистраций в минуту принимать с одного адреса.
//
// Шестьсот, то есть десять в секунду. Число выбрано не «на глаз», а из того,
// от чего ограничитель на самом деле защищает.
//
// От подбора токена он не защищает: токен — 192 случайных бита, его не
// подберут ни за какое время. От злоупотребления УТЕКШИМ токеном тоже не он:
// это делает счётчик использований — «токен на шестьсот машин», и шестьсот
// первая не зарегистрируется, с какой бы скоростью её ни просили.
//
// Остаётся одно: не дать одному источнику завалить службу запросами к
// маршруту, который работает без ключа. Десять в секунду — на порядок больше
// любого настоящего залпа раскатки и всё ещё предел.
//
// Проверено нагрузочным прогоном дважды: при десяти в минуту тысяча машин
// давала девятьсот девяносто отказов, при шестидесяти — девятьсот сорок.
const DefaultEnrollBurst = 600

// Check — проверка одной зависимости для /ready.
type Check struct {
	Name string
	Func func(ctx context.Context) error
}

// DialCheck — проверка, что до адреса можно достучаться по TCP.
//
// Годится там, где настоящего клиента ещё нет. Где он есть — проверять надо
// им: открытый порт не означает, что база принимает запросы.
func DialCheck(name, addr string) Check {
	return Check{
		Name: name,
		Func: func(ctx context.Context) error {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return conn.Close()
		},
	}
}

// Deps — то, что нужно маршрутам.
type Deps struct {
	Checks    []Check
	Acceptor  Acceptor
	Auth      Authenticator
	Commander Commander

	// Admin и AdminToken — административный вход. Пустой ключ означает, что
	// таких маршрутов нет вовсе: открытый вход, через который можно поставить
	// задание любой машине парка, не должен получаться из-за незаданной
	// переменной.
	Admin      Administrator
	AdminToken string

	// Doctor — состояние самой службы для административного экрана. Не
	// обязателен: без него маршрут просто не заводится.
	Doctor Doctor

	// EnrollBurst — сколько регистраций подряд принимать с одного адреса.
	//
	// Значение по умолчанию рассчитано на раскатку, а не на одиночную
	// установку: групповая политика поднимает агента на сотнях машин за
	// минуты, и если служба стоит за обратным прокси, все они приходят с
	// одного адреса.
	EnrollBurst int

	// NetTargets — список опроса сетевых устройств; Scanner — сам опрос,
	// nil, когда он выключен (нет строки доступа SNMP).
	NetTargets NetworkTargets
	Scanner    Scanner

	Logger *slog.Logger

	// Now подменяется в тестах: ограничитель частоты живёт по часам.
	Now func() time.Time
}

// Router собирает маршруты службы.
func Router(deps Deps) *http.ServeMux {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /ready", ready(deps.Checks))

	// Показатели отдаются без ключа, и это осознанно: служба живёт в
	// локальной сети, наружу не смотрит, а снимать их будет система
	// наблюдения, у которой ключей нет. Секретов в них нет — только счётчики.
	mux.Handle("GET /metrics", promhttp.Handler())

	if deps.Auth == nil {
		// Без проверки ключей маршрутов агента нет вовсе. Открытый приём
		// отчётов — это возможность записать в учёт что угодно от имени
		// любой машины; такой режим не должен получаться случайно, из-за
		// недособранной зависимости.
		return mux
	}

	// Ограничитель заведён на всю службу, а не на запрос: у каждого запроса
	// свой был бы всегда полон и не ограничивал ничего.
	//
	// Настоящая защита от злоупотребления токеном — не частота, а счётчик его
	// использований: администратор говорит «этот токен на шестьсот машин», и
	// шестьсот первая не зарегистрируется, с какой бы скоростью её ни просили.
	// Ограничитель здесь защищает не токен, а саму службу от того, чтобы её
	// заваливали запросами.
	//
	// Проверено нагрузочным прогоном: при пределе в десять регистраций в
	// минуту тысяча машин раскатки превращается в десять зарегистрированных и
	// девятьсот девяносто отказов.
	burst := deps.EnrollBurst
	if burst <= 0 {
		burst = DefaultEnrollBurst
	}
	limiter := newThrottle(burst, time.Minute)
	mux.HandleFunc("POST /v1/enroll", enroll(deps.Auth, limiter, deps.Logger, deps.Now))

	if deps.Acceptor != nil {
		mux.HandleFunc("POST /v1/reports",
			requireAgent(deps.Auth, deps.Logger, reports(deps.Acceptor, deps.Auth, deps.Logger)))
	}

	if deps.Commander != nil {
		mux.HandleFunc("GET /v1/commands",
			requireAgent(deps.Auth, deps.Logger, commands(deps.Commander, deps.Logger)))
		mux.HandleFunc("POST /v1/commands/{id}/result",
			requireAgent(deps.Auth, deps.Logger, commandResult(deps.Commander, deps.Logger)))
	}

	if deps.Admin != nil && deps.AdminToken != "" {
		mux.HandleFunc("POST /v1/admin/hosts/{id}/collect",
			requireAdmin(deps.AdminToken, deps.Logger,
				adminCollect(deps.Admin, deps.Logger)))
		mux.HandleFunc("GET /v1/admin/hosts/{id}",
			requireAdmin(deps.AdminToken, deps.Logger,
				adminFacts(deps.Admin, deps.Logger)))
	}

	if deps.Doctor != nil && deps.AdminToken != "" {
		mux.HandleFunc("GET /v1/admin/health",
			requireAdmin(deps.AdminToken, deps.Logger,
				adminHealth(deps.Doctor, deps.Logger, deps.Now)))
	}

	if deps.NetTargets != nil && deps.AdminToken != "" {
		mux.HandleFunc("GET /v1/admin/network/targets",
			requireAdmin(deps.AdminToken, deps.Logger,
				adminListTargets(deps.NetTargets, deps.Scanner, deps.Logger)))
		mux.HandleFunc("PUT /v1/admin/network/targets",
			requireAdmin(deps.AdminToken, deps.Logger,
				adminReplaceTargets(deps.NetTargets, deps.Scanner, deps.Logger)))
		mux.HandleFunc("POST /v1/admin/network/poll",
			requireAdmin(deps.AdminToken, deps.Logger,
				adminPollNow(deps.Scanner, deps.Logger)))
	}

	return mux
}

// health отвечает, пока процесс жив. Ничего не проверяет намеренно: если
// сюда подмешать зависимости, перезапуск базы начнёт убивать саму службу.
func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ready отвечает, готова ли служба принимать нагрузку.
func ready(checks []Check) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Таймаут обязателен (I11): проверка готовности, висящая на
		// неотвечающей зависимости, сама становится отказом.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		results := make(map[string]string, len(checks))
		code := http.StatusOK

		for _, c := range checks {
			if err := c.Func(ctx); err != nil {
				results[c.Name] = "недоступен: " + err.Error()
				code = http.StatusServiceUnavailable
				continue
			}
			results[c.Name] = "ok"
		}

		status := "ready"
		if code != http.StatusOK {
			status = "not ready"
		}

		writeJSON(w, code, map[string]any{"status": status, "checks": results})
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
