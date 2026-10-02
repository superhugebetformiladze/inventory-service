package contract

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func пара(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("генерация пары ключей: %v", err)
	}
	return pub, priv
}

func снимокДляПодписи() Snapshot {
	return Snapshot{
		SchemaVersion: SchemaVersion,
		AgentID:       "6f1c0f1e-0000-4000-8000-000000000001",
		AgentVersion:  "0.7.0",
		CollectedAt:   time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
		Origin:        OriginOffline,
		Machine:       Machine{Hostname: "PC-OFFLINE", Serial: "ABC12345"},
	}
}

func TestПодписанныйОтчётПроверяется(t *testing.T) {
	pub, priv := пара(t)

	signed, err := SignSnapshot(снимокДляПодписи(), priv)
	if err != nil {
		t.Fatalf("подпись: %v", err)
	}

	got, err := signed.Verify(pub)
	if err != nil {
		t.Fatalf("проверка: %v", err)
	}
	if got.Machine.Hostname != "PC-OFFLINE" {
		t.Errorf("снимок разобран неверно: %q", got.Machine.Hostname)
	}
}

func TestПравкаОтчётаЛомаетПодпись(t *testing.T) {
	// Ради этого подпись и заведена: файл на флешке проходит через руки, и
	// поправленный объём диска не должен доезжать до учёта как настоящий.
	pub, priv := пара(t)

	signed, _ := SignSnapshot(снимокДляПодписи(), priv)

	var снимок map[string]any
	if err := json.Unmarshal(signed.Snapshot, &снимок); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	снимок["machine"] = map[string]any{"hostname": "PC-ЧУЖОЙ"}
	signed.Snapshot, _ = json.Marshal(снимок)

	if _, err := signed.Verify(pub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("правка прошла проверку: %v", err)
	}
}

func TestЧужойКлючНеПодходит(t *testing.T) {
	_, priv := пара(t)
	чужой, _ := пара(t)

	signed, _ := SignSnapshot(снимокДляПодписи(), priv)

	if _, err := signed.Verify(чужой); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("подошёл чужой ключ: %v", err)
	}
}

func TestСетевойОтчётФайломНеПринимается(t *testing.T) {
	// Иначе перехваченный сетевой отчёт можно переиграть заново файлом.
	pub, priv := пара(t)

	snap := снимокДляПодписи()
	snap.Origin = OriginNetwork

	signed, _ := SignSnapshot(snap, priv)

	if _, err := signed.Verify(pub); err == nil {
		t.Fatal("сетевой отчёт принят файлом")
	}
}

func TestПодменаОтметкиВКонвертеЗамечена(t *testing.T) {
	// Отметка снаружи конверта подписью не покрыта: по ней импорт ищет
	// открытый ключ. Расхождение с подписанным снимком — попытка подсунуть
	// отчёт от чужого имени.
	pub, priv := пара(t)

	signed, _ := SignSnapshot(снимокДляПодписи(), priv)
	signed.AgentID = "чужая-отметка"

	if _, err := signed.Verify(pub); err == nil {
		t.Fatal("подмена отметки прошла")
	}
}

func TestЗаявкаПроверяетсяСвоимКлючом(t *testing.T) {
	pub, priv := пара(t)

	claim, err := SignClaim(SignedClaim{
		AgentID:         "6f1c0f1e-0000-4000-8000-000000000001",
		Hostname:        "PC-OFFLINE",
		EnrollmentToken: "enr_нечто",
		KeyHash:         "0f0f",
		CreatedAt:       time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
	}, priv)
	if err != nil {
		t.Fatalf("подпись заявки: %v", err)
	}

	got, err := claim.Verify()
	if err != nil {
		t.Fatalf("проверка заявки: %v", err)
	}
	if !got.Equal(pub) {
		t.Error("вернулся не тот открытый ключ")
	}
}

func TestПодменаОткрытогоКлючаВЗаявкеЗамечена(t *testing.T) {
	// Без этого открытый ключ в заявке подменяется по дороге, и дальше
	// поддельные отчёты принимаются уже без всякого токена.
	_, priv := пара(t)
	чужойПуб, _ := пара(t)

	claim, _ := SignClaim(SignedClaim{AgentID: "отметка", Hostname: "PC"}, priv)
	claim.PublicKey = base64.StdEncoding.EncodeToString(чужойПуб)

	if _, err := claim.Verify(); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("подмена открытого ключа прошла: %v", err)
	}
}
