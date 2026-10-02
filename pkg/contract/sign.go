package contract

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SignatureAlgorithm — чем подписан отчёт, принесённый файлом.
//
// Ed25519, а не HMAC на ключе агента, по одной причине: служба хранит только
// ХЕШ ключа (см. agents.Hash) и проверить HMAC им не может. Пришлось бы либо
// держать ключи машин в открытом виде — и тогда одна утечка базы даёт право
// писать в учёт от имени любой машины, — либо не проверять подпись вовсе.
//
// С парой ключей у службы лежит только открытый ключ. Он не секрет: подделать
// им отчёт нельзя, и утечка базы ничего не даёт.
const SignatureAlgorithm = "ed25519"

// ErrBadSignature — подпись не сходится с открытым ключом машины.
var ErrBadSignature = errors.New("подпись не сходится")

// SignedSnapshot — отчёт с машины без сети, перенесённый файлом.
//
// Snapshot лежит сырыми байтами намеренно: подпись считается по ним, а не по
// разобранной и заново собранной структуре. Пересборка меняет порядок полей и
// формат чисел — и честная подпись переставала бы сходиться.
type SignedSnapshot struct {
	SchemaVersion int    `json:"schema_version"`
	AgentID       string `json:"agent_id"`
	Algorithm     string `json:"algorithm"`

	// Signature — подпись поля Snapshot, base64.
	Signature string `json:"signature"`

	Snapshot json.RawMessage `json:"snapshot"`
}

// SignSnapshot подписывает снимок закрытым ключом машины.
func SignSnapshot(snap Snapshot, priv ed25519.PrivateKey) (SignedSnapshot, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return SignedSnapshot{}, errors.New("закрытый ключ машины неверной длины")
	}

	body, err := json.Marshal(snap)
	if err != nil {
		return SignedSnapshot{}, fmt.Errorf("сериализация снимка: %w", err)
	}

	return SignedSnapshot{
		SchemaVersion: SchemaVersion,
		AgentID:       snap.AgentID,
		Algorithm:     SignatureAlgorithm,
		Signature:     base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body)),
		Snapshot:      body,
	}, nil
}

// Verify проверяет подпись и возвращает снимок.
//
// Разбор идёт ТОЛЬКО после проверки подписи: разбирать содержимое, которое
// ещё не признано подлинным, — значит подставлять разборщик неизвестно чему.
func (s SignedSnapshot) Verify(pub ed25519.PublicKey) (Snapshot, error) {
	if s.Algorithm != SignatureAlgorithm {
		return Snapshot{}, fmt.Errorf("неизвестный способ подписи %q", s.Algorithm)
	}
	if len(pub) != ed25519.PublicKeySize {
		return Snapshot{}, errors.New("у машины нет открытого ключа")
	}

	sig, err := base64.StdEncoding.DecodeString(s.Signature)
	if err != nil {
		return Snapshot{}, fmt.Errorf("подпись не разбирается: %w", err)
	}
	if !ed25519.Verify(pub, s.Snapshot, sig) {
		return Snapshot{}, ErrBadSignature
	}

	var snap Snapshot
	if err := json.Unmarshal(s.Snapshot, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("снимок не разбирается: %w", err)
	}

	// Отметка агента снаружи конверта не проверялась подписью — сходиться она
	// обязана с той, что внутри, иначе файл можно подсунуть от чужого имени, и
	// импорт станет искать не тот открытый ключ.
	if snap.AgentID != s.AgentID {
		return Snapshot{}, errors.New("отметка агента в конверте не совпадает с отметкой в снимке")
	}
	if snap.Origin != OriginOffline {
		// Сетевой отчёт, положенный в файл, — это способ переиграть заново
		// то, что уже принято. Автономный путь принимает только то, что для
		// него и снималось.
		return Snapshot{}, fmt.Errorf("файлом принимается только отчёт с origin=%s", OriginOffline)
	}

	return snap, nil
}

// SignedClaim — заявка на регистрацию машины, у которой нет сети.
//
// Тот же обмен, что и по сети, только в одну сторону: машина не может
// спросить службу, поэтому кладёт в файл всё нужное сразу — отметку, открытый
// ключ и хеш своего ключа доступа.
//
// Право зарегистрироваться даёт токен раскатки, ровно как по сети. Подпись
// нужна для другого: без неё открытый ключ в заявке можно подменить по дороге
// и потом слать поддельные отчёты, уже не имея токена.
type SignedClaim struct {
	SchemaVersion int    `json:"schema_version"`
	AgentID       string `json:"agent_id"`
	Hostname      string `json:"hostname"`
	AgentVersion  string `json:"agent_version"`

	// EnrollmentToken — тот же токен раскатки, что и по сети.
	EnrollmentToken string `json:"enrollment_token"`

	// KeyHash — hex-хеш ключа доступа, который машина сгенерировала себе сама.
	//
	// Именно хеш: служба и по сети хранит только его. Так машина, оказавшись
	// однажды в сети, воспользуется уже имеющимся ключом, и повторная
	// регистрация не понадобится.
	KeyHash string `json:"key_hash"`

	// KeyPrefix — начало ключа доступа, по которому человек узнаёт его в
	// списке агентов. По сети служба берёт его из самого ключа, здесь ключа
	// у неё нет.
	KeyPrefix string `json:"key_prefix"`

	// PublicKey — открытый ключ машины, base64.
	PublicKey string `json:"public_key"`

	CreatedAt time.Time `json:"created_at"`

	// Signature — подпись заявки закрытым ключом машины, base64. Заполняется
	// последней и в подписываемые байты не входит.
	Signature string `json:"signature"`
}

// SignClaim подписывает заявку закрытым ключом машины.
func SignClaim(claim SignedClaim, priv ed25519.PrivateKey) (SignedClaim, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return SignedClaim{}, errors.New("закрытый ключ машины неверной длины")
	}

	claim.SchemaVersion = SchemaVersion
	claim.PublicKey = base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	claim.Signature = ""

	body, err := json.Marshal(claim)
	if err != nil {
		return SignedClaim{}, fmt.Errorf("сериализация заявки: %w", err)
	}

	claim.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body))
	return claim, nil
}

// Verify проверяет, что заявку подписали тем ключом, который в ней объявлен.
//
// Это проверка целостности, а не права: право даёт токен раскатки, и его
// служба проверяет отдельно. Здесь ловится подмена открытого ключа по дороге.
func (c SignedClaim) Verify() (ed25519.PublicKey, error) {
	pub, err := c.PublicKeyBytes()
	if err != nil {
		return nil, err
	}

	sig, err := base64.StdEncoding.DecodeString(c.Signature)
	if err != nil {
		return nil, fmt.Errorf("подпись не разбирается: %w", err)
	}

	unsigned := c
	unsigned.Signature = ""
	body, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("сериализация заявки: %w", err)
	}

	if !ed25519.Verify(pub, body, sig) {
		return nil, ErrBadSignature
	}
	return pub, nil
}

// PublicKeyBytes разбирает открытый ключ из заявки.
func (c SignedClaim) PublicKeyBytes() (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(c.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("открытый ключ не разбирается: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("открытый ключ длиной %d байт вместо %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
