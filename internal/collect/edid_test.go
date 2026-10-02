package collect

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Настоящий блок EDID монитора Dell U2412M — не собранный по описанию
// формата, а снятый с живого устройства. Собранный по описанию проверял бы
// разбор против того же понимания формата, которое в разборе и заложено.
const edidDellU2412M = "" +
	"00ffffffffffff0010ac47404c374e41" +
	"0f16010380342078eaad35a4544d9c26" +
	"0f5054a54b00714f8180a940d1c00101" +
	"010101010101283c80a070b023403020" +
	"360006442100001a000000ff00594d33" +
	"4e4431364e414e374c0a000000fc0044" +
	"454c4c2055323431324d0a20000000fd" +
	"00313d1e5311000a202020202020017a"

func TestРазборEDID(t *testing.T) {
	b, err := hex.DecodeString(edidDellU2412M)
	if err != nil {
		t.Fatalf("образец не разбирается: %v", err)
	}

	m, err := parseEDID(b)
	if err != nil {
		t.Fatalf("разбор EDID: %v", err)
	}

	проверить := func(что, получено, ожидалось string) {
		t.Helper()
		if получено != ожидалось {
			t.Errorf("%s: получено %q, ожидалось %q", что, получено, ожидалось)
		}
	}

	проверить("производитель", m.Manufacturer, "DEL")
	проверить("модель", m.Model, "DELL U2412M")
	проверить("заводской номер", m.Serial, "YM3ND16NAN7L")

	if m.Year != 2012 {
		t.Errorf("год выпуска: %d, ожидался 2012", m.Year)
	}
	// 52 на 32 сантиметра — это 24 дюйма по диагонали.
	if m.DiagonalInch != 24 {
		t.Errorf("диагональ: %d дюймов, ожидалось 24", m.DiagonalInch)
	}
}

func TestНеEDIDОтвергается(t *testing.T) {
	// В реестре под теми же именами лежит мусор от давно отключённых
	// устройств. Принять его — значит завести монитор-призрак.
	for _, случай := range []struct {
		имя  string
		блок []byte
	}{
		{"пусто", nil},
		{"короткий", make([]byte, 64)}, //nolint:mnd // половина блока
		{"нули", make([]byte, edidLength)},
	} {
		if _, err := parseEDID(случай.блок); err == nil {
			t.Errorf("%s: принят как EDID", случай.имя)
		}
	}
}

func TestСерийныйНомерИзОписателяВажнееЧисла(t *testing.T) {
	// В заголовке номер лежит числом, и у части производителей он одинаков
	// для всей партии. Строка из описателя — настоящий номер устройства.
	b, _ := hex.DecodeString(edidDellU2412M)

	m, err := parseEDID(b)
	if err != nil {
		t.Fatalf("разбор EDID: %v", err)
	}
	if strings.ContainsAny(m.Serial, "0123456789") && !strings.ContainsAny(m.Serial, "YM") {
		t.Errorf("взят номер из заголовка вместо описателя: %q", m.Serial)
	}
}
