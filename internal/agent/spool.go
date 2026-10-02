package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

// MaxSpooled — сколько отчётов агент хранит, пока служба недоступна.
//
// Не «сколько влезет»: машина, простоявшая полгода без сети, иначе завалит
// службу сотнями отчётов разом. Двести при обычном периоде в четыре часа —
// это месяц молчания, дольше которого сведения всё равно теряют смысл.
const MaxSpooled = 200

// Spool — очередь отчётов на диске.
//
// Ради неё стоило заводить агента вообще: без буфера отчёт машины, у которой
// в момент сбора не было сети, просто пропадал бы, а ноутбуки — это как раз
// те машины, чьи сведения устаревают быстрее всего.
type Spool struct {
	Dir string
	Max int
}

// Add кладёт отчёт в очередь.
func (s *Spool) Add(snap contract.Snapshot) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("каталог очереди: %w", err)
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}

	// Имя файла — время сбора: очередь разбирается в том же порядке, в каком
	// снималась, простой сортировкой по имени. Отметка времени в UTC и с
	// ведущими нулями, иначе сортировка по имени перестанет совпадать с
	// сортировкой по времени.
	name := snap.CollectedAt.UTC().Format("20060102T150405.000000000Z") + ".json"
	path := filepath.Join(s.Dir, name)

	temp := path + ".tmp"
	if err := os.WriteFile(temp, b, 0o600); err != nil {
		return fmt.Errorf("запись отчёта в очередь: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("замена отчёта в очереди: %w", err)
	}

	return s.trim()
}

// Files — очередь по порядку, старые первыми.
func (s *Spool) Files() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("чтение очереди: %w", err)
	}

	var out []string
	for _, e := range entries {
		// Недописанные файлы пропускаются: в них может лежать половина
		// отчёта, и отправлять её незачем.
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, filepath.Join(s.Dir, e.Name()))
	}

	sort.Strings(out)
	return out, nil
}

// Len — сколько отчётов ждёт отправки.
func (s *Spool) Len() int {
	files, err := s.Files()
	if err != nil {
		return 0
	}
	return len(files)
}

// Read читает один отчёт из очереди.
func (s *Spool) Read(path string) (contract.Snapshot, error) {
	b, err := os.ReadFile(path) //nolint:gosec // читается только то, что вернул Files из своего каталога
	if err != nil {
		return contract.Snapshot{}, err
	}

	var snap contract.Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return contract.Snapshot{}, fmt.Errorf("отчёт в очереди испорчен (%s): %w", path, err)
	}
	return snap, nil
}

// Remove убирает отправленный отчёт.
func (s *Spool) Remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DropOlderThan выбрасывает отчёты, которые служба всё равно не примет.
//
// Смысл не в экономии места, а в том, чтобы агент не отправлял вечно то, что
// заведомо будет отвергнуто, и не считал это неполадкой связи.
func (s *Spool) DropOlderThan(now time.Time, age time.Duration) (int, error) {
	files, err := s.Files()
	if err != nil {
		return 0, err
	}

	dropped := 0
	for _, f := range files {
		snap, err := s.Read(f)
		// Испорченный файл тоже выбрасывается: отправить его нельзя, а
		// занимать очередь он будет вечно.
		if err != nil || now.Sub(snap.CollectedAt) > age {
			if err := s.Remove(f); err == nil {
				dropped++
			}
		}
	}
	return dropped, nil
}

// trim держит очередь в пределах Max, выбрасывая самые старые.
//
// Старые, а не новые: свежие сведения о машине нужнее прошлогодних, и если
// чем-то жертвовать, то прошлым.
func (s *Spool) trim() error {
	limit := s.Max
	if limit <= 0 {
		limit = MaxSpooled
	}

	files, err := s.Files()
	if err != nil {
		return err
	}
	if len(files) <= limit {
		return nil
	}

	for _, f := range files[:len(files)-limit] {
		if err := s.Remove(f); err != nil {
			return err
		}
	}
	return nil
}
