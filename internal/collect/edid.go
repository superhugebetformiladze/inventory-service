package collect

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// EDID — блок сведений, который монитор сообщает о себе видеокарте.
//
// Разбирается он здесь, а не берётся из WMI, по двум причинам. Класс
// WmiMonitorID отдаёт названия массивами шестнадцатибитных чисел, и работа с
// ними через COM зависит от версии библиотеки. А сам EDID — 128 байт с
// зафиксированной раскладкой, одинаковой на всех версиях Windows: его разбор
// проверяется обычным тестом с настоящим блоком монитора.
const edidLength = 128

var edidHeader = []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

// errNotEDID — блок не похож на EDID. В реестре под этим именем лежит и
// мусор от давно отключённых устройств.
var errNotEDID = errors.New("блок не похож на EDID")

// parseEDID достаёт из блока то, что нужно учёту.
func parseEDID(b []byte) (RawMonitor, error) {
	if len(b) < edidLength {
		return RawMonitor{}, errNotEDID
	}
	for i, want := range edidHeader {
		if b[i] != want {
			return RawMonitor{}, errNotEDID
		}
	}

	m := RawMonitor{
		Manufacturer: manufacturerCode(binary.BigEndian.Uint16(b[8:10])),
		Model:        fmt.Sprintf("%04X", binary.LittleEndian.Uint16(b[10:12])),
	}

	// Год: в EDID он хранится смещением от 1990-го. Ноль означает, что год
	// не указан, а не 1990-й.
	if b[17] != 0 {
		m.Year = 1990 + int(b[17])
	}

	// Заводской номер бывает в двух местах сразу: числом в заголовке и
	// строкой в описателе. Строка точнее — числовой вариант у части
	// производителей одинаков для всей партии.
	if serial := binary.LittleEndian.Uint32(b[12:16]); serial != 0 {
		m.Serial = fmt.Sprintf("%d", serial)
	}

	// Размер экрана в сантиметрах. Ноль — монитор проекционный или размер не
	// сообщён; диагональ тогда не считаем, а не пишем ноль дюймов.
	if ширина, высота := int(b[21]), int(b[22]); ширина > 0 && высота > 0 {
		диагональСм := math.Hypot(float64(ширина), float64(высота))
		m.DiagonalInch = int(math.Round(диагональСм / 2.54))
	}

	// Четыре описателя по 18 байт. Нужные — с текстом: название модели и
	// заводской номер.
	for offset := 54; offset+18 <= edidLength; offset += 18 {
		d := b[offset : offset+18]
		if d[0] != 0 || d[1] != 0 || d[2] != 0 {
			continue // это тайминг, а не текст
		}

		switch d[3] {
		case 0xfc:
			if name := descriptorText(d[5:]); name != "" {
				m.Model = name
			}
		case 0xff:
			if serial := descriptorText(d[5:]); serial != "" {
				m.Serial = serial
			}
		}
	}

	return m, nil
}

// manufacturerCode разворачивает три пятибитные буквы кода производителя.
//
// EDID хранит их упакованными в два байта: 'A' это 1, 'Z' это 26. Именно
// поэтому в списках мониторов встречается «DEL» вместо «Dell» — это и есть
// весь код, других сведений о производителе в блоке нет.
func manufacturerCode(packed uint16) string {
	var b strings.Builder
	for _, shift := range []uint{10, 5, 0} {
		c := byte((packed>>shift)&0x1f) + 'A' - 1
		if c < 'A' || c > 'Z' {
			return ""
		}
		b.WriteByte(c)
	}
	return b.String()
}

// descriptorText достаёт строку из описателя.
//
// Строка заканчивается переводом строки и добивается пробелами — так
// устроен формат, и без обрезки в учёт попадут названия с хвостом пробелов.
func descriptorText(b []byte) string {
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}
