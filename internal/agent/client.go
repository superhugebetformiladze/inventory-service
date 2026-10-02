package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

// Ошибки, по которым агент решает, повторять ли попытку.
//
// Различать их обязательно. Отчёт, отвергнутый как негодный, при повторе
// будет отвергнут снова: агент, который этого не понимает, крутит его в
// очереди до конца времён и не отправляет всё остальное.
var (
	// ErrTemporary — служба недоступна или ответила ошибкой на своей
	// стороне. Повторять стоит.
	ErrTemporary = errors.New("временная неполадка")

	// ErrRejected — служба поняла запрос и отказала. Повторять бессмысленно.
	ErrRejected = errors.New("служба отказала")

	// ErrKeyDenied — ключ машины не признан: отозван или машину
	// перерегистрировали. Чинится только человеком.
	ErrKeyDenied = errors.New("ключ машины не признан")
)

// Client — обращения агента к службе.
type Client struct {
	BaseURL string
	Key     string
	Version string
	HTTP    *http.Client
}

// NewClient создаёт клиента с разумными сроками ожидания.
func NewClient(baseURL, key, version string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Key:     key,
		Version: version,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// EnrollResult — что вернула регистрация.
type EnrollResult struct {
	AgentKey  string `json:"agent_key"`
	AgentUUID string `json:"agent_uuid"`
	Rotated   bool   `json:"rotated"`
}

// Enroll обменивает токен раскатки на персональный ключ машины.
func (c *Client) Enroll(ctx context.Context, token string, s State, hostname string) (EnrollResult, error) {
	pub, err := s.PublicKey()
	if err != nil {
		return EnrollResult{}, err
	}

	body := map[string]string{
		"enrollment_token": token,
		"agent_id":         s.AgentID,
		"hostname":         hostname,
		"agent_version":    c.Version,
		// Открытый ключ отдаётся сразу, хотя нужен он только для отчётов
		// файлом: ноутбук может уехать надолго уже завтра, а взять ключ
		// задним числом будет неоткуда — служба к машине не ходит.
		"public_key": base64.StdEncoding.EncodeToString(pub),
	}

	var result EnrollResult
	if err := c.call(ctx, http.MethodPost, "/v1/enroll", body, &result, false); err != nil {
		return EnrollResult{}, err
	}
	if result.AgentKey == "" {
		return EnrollResult{}, fmt.Errorf("%w: служба не выдала ключ", ErrRejected)
	}
	return result, nil
}

// Send отправляет отчёт.
func (c *Client) Send(ctx context.Context, snap contract.Snapshot) error {
	return c.call(ctx, http.MethodPost, "/v1/reports", snap, nil, true)
}

// Commands забирает задания, которые для агента поставил человек.
func (c *Client) Commands(ctx context.Context) ([]contract.Command, error) {
	var answer struct {
		Commands []contract.Command `json:"commands"`
	}
	if err := c.call(ctx, http.MethodGet, "/v1/commands", nil, &answer, true); err != nil {
		return nil, err
	}

	// Проверка на СТОРОНЕ АГЕНТА, тем же кодом контракта, которым проверяет
	// служба. Она не лишняя: агент не должен доверять тому, что служба
	// прислала правильное, иначе взломанная служба становится пультом
	// управления парком (инвариант I6).
	out := make([]contract.Command, 0, len(answer.Commands))
	for _, cmd := range answer.Commands {
		if err := cmd.Validate(); err != nil {
			return out, fmt.Errorf("%w: задание %s не принято: %w", ErrRejected, cmd.ID, err)
		}
		out = append(out, cmd)
	}
	return out, nil
}

// Finish сообщает, чем кончилось задание.
func (c *Client) Finish(ctx context.Context, id string, result contract.CommandResult) error {
	return c.call(ctx, http.MethodPost, "/v1/commands/"+id+"/result", result, nil, true)
}

// maxAnswer — сколько агент готов прочитать из ответа службы.
//
// Ответы у службы короткие; предел стоит, чтобы неверно настроенный прокси,
// отдающий вместо ответа страницу на сотню мегабайт, не съел память рабочей
// станции.
const maxAnswer = 1 << 20

// call — один запрос к службе.
//
// Здесь же превращение кода ответа в одну из трёх ошибок: от этого зависит,
// повторит агент попытку или бросит.
func (c *Client) call(ctx context.Context, method, path string, in, out any, auth bool) error {
	var тело io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		тело = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, тело)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	req.Header.Set("User-Agent", "inventory-agent/"+c.Version)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Сюда попадает всё: нет сети, не разрешается имя, оборвалось
		// соединение, вышло время. Для агента это один случай — повторить
		// позже.
		return fmt.Errorf("%w: %w", ErrTemporary, err)
	}
	defer func() { _ = resp.Body.Close() }()

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return fmt.Errorf("%w: чтение ответа: %w", ErrTemporary, err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return статус(resp.StatusCode, b)
	}

	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("%w: ответ службы не разбирается: %w", ErrTemporary, err)
		}
	}
	return nil
}

// статус переводит код ответа в решение «повторять или нет».
func статус(code int, body []byte) error {
	var answer struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &answer)

	причина := strings.TrimSpace(answer.Error)
	if причина == "" {
		причина = http.StatusText(code)
	}

	switch {
	case code == http.StatusUnauthorized, code == http.StatusForbidden:
		// Ключ отозвали или машину перерегистрировали. Повторы не помогут и
		// только добавят службе нагрузки от машины, которой отказано.
		return fmt.Errorf("%w: %s", ErrKeyDenied, причина)

	case code == http.StatusTooManyRequests, code == http.StatusRequestTimeout:
		// Служба просит подождать — это ровно то, что агент умеет.
		return fmt.Errorf("%w: %s", ErrTemporary, причина)

	case code >= http.StatusInternalServerError:
		return fmt.Errorf("%w: служба ответила %d: %s", ErrTemporary, code, причина)

	default:
		// Прочие 4xx: служба поняла запрос и отказала. Тот же отчёт при
		// повторе будет отвергнут снова.
		return fmt.Errorf("%w: служба ответила %d: %s", ErrRejected, code, причина)
	}
}
