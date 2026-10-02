// Package agents — регистрация агентов и проверка их ключей.
package agents

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// Префиксы отличают одно от другого в журналах и в чужих руках: увидев
// «enr_...» в скрипте установки, человек понимает, что это токен раскатки, а
// не ключ машины.
const (
	EnrollmentTokenPrefix = "enr_"
	AgentKeyPrefix        = "inv_"
)

// Длина случайной части. 32 байта — это 256 бит: подобрать перебором нельзя
// ни при каком терпении, а хранить и передавать всё ещё удобно.
const (
	enrollmentTokenBytes = 24
	agentKeyBytes        = 32
)

// DisplayPrefixLen — сколько символов ключа показывать человеку.
const DisplayPrefixLen = 12

// NewEnrollmentToken выдаёт новый токен регистрации.
func NewEnrollmentToken() (string, error) {
	return newSecret(EnrollmentTokenPrefix, enrollmentTokenBytes)
}

// NewAgentKey выдаёт новый персональный ключ агента.
func NewAgentKey() (string, error) {
	return newSecret(AgentKeyPrefix, agentKeyBytes)
}

func newSecret(prefix string, n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("генератор случайных чисел: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// Hash — то, что попадает в базу вместо самого секрета.
//
// SHA-256 без соли и без замедления — и это осознанно, а не упущение.
// bcrypt и argon2 нужны там, где секрет выбирает человек: они делают перебор
// словаря дорогим. Здесь секрет — 256 случайных бит, перебирать его нечем, а
// медленный хеш стоил бы десятков миллисекунд на КАЖДОМ отчёте от пятисот
// машин и лишил бы возможности искать агента по ключу одним запросом.
func Hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// DisplayPrefix — начало ключа для журнала и списка агентов.
//
// Показывать нужно ровно столько, чтобы человек узнал ключ среди других, и
// ровно настолько мало, чтобы это не помогло его подобрать.
func DisplayPrefix(secret string) string {
	if len(secret) <= DisplayPrefixLen {
		return secret
	}
	return secret[:DisplayPrefixLen]
}

// LooksLikeAgentKey — грубая проверка формы до похода в базу.
//
// Смысл не в безопасности, а в том, чтобы мусор и чужие заголовки не
// превращались в запрос к базе на каждый вызов.
func LooksLikeAgentKey(s string) bool {
	return strings.HasPrefix(s, AgentKeyPrefix) && len(s) > len(AgentKeyPrefix)+16
}

// LooksLikeEnrollmentToken — то же для токена регистрации.
func LooksLikeEnrollmentToken(s string) bool {
	return strings.HasPrefix(s, EnrollmentTokenPrefix) && len(s) > len(EnrollmentTokenPrefix)+16
}
