package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

func TestАвтономныйОтчётПодписанИПроверяется(t *testing.T) {
	dir := t.TempDir()
	state, _ := NewState()

	path, err := WriteReport(dir, state, снимок(сейчас))
	if err != nil {
		t.Fatalf("запись отчёта: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение файла: %v", err)
	}

	var signed contract.SignedSnapshot
	if err := json.Unmarshal(b, &signed); err != nil {
		t.Fatalf("файл не разбирается: %v", err)
	}

	pub, _ := state.PublicKey()
	snap, err := signed.Verify(pub)
	if err != nil {
		t.Fatalf("подпись не сходится: %v", err)
	}

	if snap.Origin != contract.OriginOffline {
		t.Errorf("отчёт не помечен как автономный: %q", snap.Origin)
	}
	if snap.AgentID != state.AgentID {
		t.Errorf("отметка агента: %q", snap.AgentID)
	}
}

func TestПравкаФайлаНаФлешкеЛомаетПодпись(t *testing.T) {
	// Ради этого подпись и нужна: файл проходит через руки, и поправленный
	// объём диска не должен доезжать до учёта как настоящий.
	dir := t.TempDir()
	state, _ := NewState()

	path, err := WriteReport(dir, state, снимок(сейчас))
	if err != nil {
		t.Fatalf("запись отчёта: %v", err)
	}

	b, _ := os.ReadFile(path)
	испорчен := strings.Replace(string(b), "PC-TEST", "PC-ЧУЖОЙ", 1)
	if испорчен == string(b) {
		t.Fatal("подготовка теста не сработала: имя машины не найдено в файле")
	}
	if err := os.WriteFile(path, []byte(испорчен), 0o600); err != nil {
		t.Fatalf("правка файла: %v", err)
	}

	var signed contract.SignedSnapshot
	if err := json.Unmarshal([]byte(испорчен), &signed); err != nil {
		t.Fatalf("файл не разбирается: %v", err)
	}

	pub, _ := state.PublicKey()
	if _, err := signed.Verify(pub); err == nil {
		t.Fatal("правка прошла проверку подписи")
	}
}

func TestЗаявкаНесётХешКлючаАНеСамКлюч(t *testing.T) {
	// Ключ доступа машина придумывает себе сама; службе уезжает только хеш —
	// ровно так же, как при регистрации по сети. Сам ключ на флешке не
	// нужен, а попав туда, стал бы работающим ключом в чужих руках.
	dir := t.TempDir()
	state, _ := NewState()

	state, path, err := WriteClaim(dir, state, "enr_токенраскатки123456", "PC-TEST", "тест")
	if err != nil {
		t.Fatalf("запись заявки: %v", err)
	}
	if state.AgentKey == "" {
		t.Fatal("ключ доступа не сгенерирован")
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	if strings.Contains(string(b), state.AgentKey) {
		t.Fatal("ключ доступа целиком попал в файл на флешке")
	}

	var claim contract.SignedClaim
	if err := json.Unmarshal(b, &claim); err != nil {
		t.Fatalf("заявка не разбирается: %v", err)
	}
	if _, err := claim.Verify(); err != nil {
		t.Errorf("заявка не проверяется своей же подписью: %v", err)
	}
}

func TestИмяМашиныНеУводитЗаписьВЧужойКаталог(t *testing.T) {
	// Имя компьютера задаёт человек, и попасть в него может что угодно.
	dir := t.TempDir()
	state, _ := NewState()

	snap := снимок(сейчас)
	snap.Machine.Hostname = `..\..\windows\system32\evil`

	path, err := WriteReport(dir, state, snap)
	if err != nil {
		t.Fatalf("запись отчёта: %v", err)
	}

	if filepath.Dir(path) != dir {
		t.Fatalf("файл записан мимо каталога: %s", path)
	}
}

func TestИмяФайлаОтчётаНесётВремя(t *testing.T) {
	// На одной флешке накапливаются отчёты с десятков машин за несколько
	// заходов, и разбирать их надо по порядку.
	dir := t.TempDir()
	state, _ := NewState()

	раньше := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	позже := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

	первый, _ := WriteReport(dir, state, снимок(раньше))
	второй, _ := WriteReport(dir, state, снимок(позже))

	if filepath.Base(первый) >= filepath.Base(второй) {
		t.Errorf("сортировка по имени не совпадает с сортировкой по времени: %s, %s",
			filepath.Base(первый), filepath.Base(второй))
	}
	if !strings.HasSuffix(первый, ReportSuffix) {
		t.Errorf("отчёт назван не как отчёт: %s", первый)
	}
}
