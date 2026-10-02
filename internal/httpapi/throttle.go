package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// throttle — ограничитель частоты по адресу обратившегося.
//
// Нужен ровно одному маршруту — регистрации. Токен раскатки лежит в скрипте
// групповой политики, то есть в месте, куда заглядывает весь домен; без
// ограничения его можно подбирать со скоростью сети. С ограничением подбор
// 192-битного секрета становится бессмысленным окончательно.
//
// Своя реализация вместо библиотеки: сорок строк против ещё одной
// зависимости в проекте, где их пока четыре.
type throttle struct {
	mu      sync.Mutex
	seen    map[string]*bucket
	rate    time.Duration // за сколько восстанавливается ОДНА попытка
	burst   int           // сколько попыток можно потратить залпом
	cleanup time.Time
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// newThrottle — ограничитель «не больше burst попыток за window».
//
// Именно так, а не «burst залпом и одна попытка за window»: второе читается из
// подписи ровно так же, и разница обнаруживается только под нагрузкой. Она и
// обнаружилась — на нагрузочном прогоне, где после залпа восстанавливалась
// одна попытка в минуту, и девятьсот машин из тысячи не зарегистрировались.
//
// Скорость восстановления считается здесь, чтобы вызывающему не приходилось
// делить самому и ошибаться в том же месте.
func newThrottle(burst int, window time.Duration) *throttle {
	if burst < 1 {
		burst = 1
	}

	return &throttle{
		seen:  make(map[string]*bucket),
		rate:  window / time.Duration(burst),
		burst: burst,
	}
}

// allow — можно ли пропустить очередную попытку с этого адреса.
func (t *throttle) allow(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.collect(now)

	b, ok := t.seen[key]
	if !ok {
		b = &bucket{tokens: float64(t.burst), lastSeen: now}
		t.seen[key] = b
	}

	b.tokens += now.Sub(b.lastSeen).Seconds() / t.rate.Seconds()
	if b.tokens > float64(t.burst) {
		b.tokens = float64(t.burst)
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// collect выбрасывает давно не появлявшиеся адреса.
//
// Без уборки карта растёт на каждый новый адрес и живёт вечно: это медленная
// утечка памяти, которую заметят через месяц работы.
func (t *throttle) collect(now time.Time) {
	if now.Before(t.cleanup) {
		return
	}
	t.cleanup = now.Add(10 * time.Minute)

	stale := now.Add(-30 * time.Minute)
	for k, b := range t.seen {
		if b.lastSeen.Before(stale) {
			delete(t.seen, k)
		}
	}
}

// clientIP — адрес обратившегося.
//
// Заголовки вроде X-Forwarded-For намеренно не читаются: их подделывает кто
// угодно, и ограничитель, доверяющий им, обходится одной строкой в запросе.
// Когда перед службой появится обратный прокси, доверенный источник адреса
// придётся задать явно.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
