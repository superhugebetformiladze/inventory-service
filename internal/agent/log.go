package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// MaxLogBytes — до какого размера растёт журнал агента, прежде чем его
// сменят.
//
// Пять мегабайт: журнал на рабочей станции никто не читает годами, и агент,
// незаметно съевший гигабайт диска на машине с забитым SSD, — это претензия
// не к дисковой квоте, а к агенту.
const MaxLogBytes = 5 << 20

// logWriter — файл журнала, который сам себя подрезает.
//
// Своё, а не готовая библиотека ротации: нужны один файл, один предыдущий и
// предел размера. Зависимость ради пятидесяти строк — это ещё один пакет,
// который придётся обновлять на пятистах машинах.
type logWriter struct {
	path string
	max  int64

	mu   sync.Mutex
	file *os.File
	size int64
}

// OpenLog открывает журнал агента.
func OpenLog(path string) (*logWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("каталог журнала: %w", err)
	}

	w := &logWriter{path: path, max: MaxLogBytes}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *logWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("журнал %s: %w", w.path, err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}

	w.file = f
	w.size = info.Size()
	return nil
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size+int64(len(p)) > w.max {
		if err := w.rotate(); err != nil {
			// Смена не удалась — писать всё равно надо: потерять запись о
			// сбое из-за неполадки с файлом журнала было бы обидно.
			_ = err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate сдвигает журнал: текущий становится предыдущим.
//
// Предыдущий ровно один. Больше не нужно: журнал агента читают, когда с
// машиной что-то не так, и смотрят последние дни, а не историю за год.
func (w *logWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	// Прежний .1 затирается: os.Rename на Windows не заменяет существующий
	// файл, и без удаления смена перестала бы работать со второго раза.
	_ = os.Remove(w.path + ".1")
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		return err
	}
	return w.open()
}

// Close закрывает журнал.
func (w *logWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
