package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/broton07/inventory_service/internal/agents"
	"github.com/broton07/inventory_service/pkg/contract"
)

// Автономный режим: машина без сети.
//
// В парке такие есть, и их не единицы. Для них весь путь разворачивается в
// одну сторону: агент кладёт файлы на флешку, человек несёт флешку к службе.
// Спросить службу машина не может ни о чём, поэтому в файл кладётся сразу
// всё, что понадобится на той стороне.
//
// Отчёт подписывается закрытым ключом машины. Без подписи файл, принесённый
// на флешке, — это способ записать в учёт что угодно от имени любой машины:
// достаточно поправить в нём имя и объём диска.

// Расширения файлов автономного обмена. Разные, потому что порядок обработки
// имеет значение: заявку надо принять до отчёта, иначе отчёт не от кого
// принимать.
const (
	ClaimSuffix  = ".claim.json"
	ReportSuffix = ".report.json"
)

// WriteClaim кладёт в каталог заявку на регистрацию машины.
//
// Ключ доступа машина придумывает себе сама, а службе отдаёт только его хеш —
// ровно так же, как при регистрации по сети: сам ключ службе не нужен, она
// хранит хеш. Поэтому машина, оказавшись однажды в сети, воспользуется уже
// имеющимся ключом, и второй раз регистрироваться не придётся.
func WriteClaim(dir string, state State, token, hostname, version string) (State, string, error) {
	if state.AgentKey == "" {
		key, err := agents.NewAgentKey()
		if err != nil {
			return state, "", err
		}
		state.AgentKey = key
		state.EnrolledAt = time.Now().UTC()
	}

	priv, err := state.PrivateKey()
	if err != nil {
		return state, "", err
	}

	сумма := sha256.Sum256([]byte(state.AgentKey))

	claim, err := contract.SignClaim(contract.SignedClaim{
		AgentID:         state.AgentID,
		Hostname:        hostname,
		AgentVersion:    version,
		EnrollmentToken: token,
		KeyHash:         hex.EncodeToString(сумма[:]),
		KeyPrefix:       agents.DisplayPrefix(state.AgentKey),
		CreatedAt:       time.Now().UTC(),
	}, priv)
	if err != nil {
		return state, "", err
	}

	// Заявку можно печатать с отступами: её подпись считается по заново
	// собранной структуре, а не по байтам файла.
	path, err := writeJSON(dir, safeName(hostname, state.AgentID)+ClaimSuffix, claim, true)
	return state, path, err
}

// WriteReport кладёт в каталог подписанный отчёт.
func WriteReport(dir string, state State, snap contract.Snapshot) (string, error) {
	priv, err := state.PrivateKey()
	if err != nil {
		return "", err
	}

	// Отчёт помечается как автономный ещё здесь, до подписи: пометка входит
	// в подписываемые байты, и подменить её по дороге нельзя.
	snap.Origin = contract.OriginOffline
	snap.AgentID = state.AgentID

	signed, err := contract.SignSnapshot(snap, priv)
	if err != nil {
		return "", err
	}

	// Время в имени файла: на одной флешке накапливаются отчёты с десятков
	// машин за несколько заходов, и разбирать их надо по порядку.
	name := fmt.Sprintf("%s-%s%s",
		safeName(snap.Machine.Hostname, state.AgentID),
		snap.CollectedAt.UTC().Format("20060102T150405Z"),
		ReportSuffix)

	// Без отступов, и это не мелочь: подпись считается по БАЙТАМ снимка, а
	// печать с отступами переформатировала бы уже подписанное — файл
	// перестал бы проходить проверку собственной подписью.
	return writeJSON(dir, name, signed, false)
}

func writeJSON(dir, name string, body any, indent bool) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("каталог для файлов: %w", err)
	}

	var (
		b   []byte
		err error
	)
	if indent {
		b, err = json.MarshalIndent(body, "", "  ")
	} else {
		b, err = json.Marshal(body)
	}
	if err != nil {
		return "", err
	}

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", fmt.Errorf("запись %s: %w", path, err)
	}
	return path, nil
}

// safeName делает из имени машины кусок имени файла.
//
// Имя компьютера задаёт человек, и попасть в него может что угодно, вплоть до
// косой черты и точек: без очистки такое имя увело бы запись файла в чужой
// каталог.
func safeName(hostname, fallback string) string {
	var b strings.Builder
	for _, r := range hostname {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}

	name := strings.Trim(b.String(), "_")
	if name == "" {
		// Отметка агента — всегда UUID, в имени файла безопасна.
		return fallback
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}
