package collect

import (
	"testing"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

func TestРазборДатыWMI(t *testing.T) {
	случаи := []struct {
		имя      string
		вход     string
		ожидание time.Time
		годится  bool
	}{
		{
			имя:      "московское время",
			вход:     "20230115103000.000000+180",
			ожидание: time.Date(2023, 1, 15, 7, 30, 0, 0, time.UTC),
			годится:  true,
		},
		{
			// Ради этого случая разбор здесь свой: сдвиг в WMI хранится в
			// МИНУТАХ, и деление на 60 в Индии, Иране и Непале даёт
			// получасовую ошибку.
			имя:      "получасовой пояс",
			вход:     "20230115103000.000000+330",
			ожидание: time.Date(2023, 1, 15, 5, 0, 0, 0, time.UTC),
			годится:  true,
		},
		{
			имя:      "западный сдвиг",
			вход:     "20230115103000.000000-300",
			ожидание: time.Date(2023, 1, 15, 15, 30, 0, 0, time.UTC),
			годится:  true,
		},
		{
			имя:      "без пояса",
			вход:     "20230115103000.000000+000",
			ожидание: time.Date(2023, 1, 15, 10, 30, 0, 0, time.UTC),
			годится:  true,
		},
		{имя: "пусто", вход: "", годится: false},
		{имя: "мусор", вход: "не дата", годится: false},
		{имя: "обрезанная", вход: "202301", годится: false},
	}

	for _, с := range случаи {
		t.Run(с.имя, func(t *testing.T) {
			got, ok := parseCIMDate(с.вход)
			if ok != с.годится {
				t.Fatalf("годность: %v, ожидалась %v", ok, с.годится)
			}
			if ok && !got.Equal(с.ожидание) {
				t.Errorf("получено %s, ожидалось %s", got, с.ожидание)
			}
		})
	}
}

func TestТипПамяти(t *testing.T) {
	// Два набора кодов: SMBIOSMemoryType на Windows 10 и MemoryType на
	// старых системах. Одно и то же число в них означает разное.
	if got := memoryTypeName(26, 0); got != "DDR4" {
		t.Errorf("SMBIOS 26: получено %q, ожидалось DDR4", got)
	}
	if got := memoryTypeName(34, 0); got != "DDR5" {
		t.Errorf("SMBIOS 34: получено %q, ожидалось DDR5", got)
	}
	if got := memoryTypeName(0, 24); got != "DDR3" {
		t.Errorf("старый код 24: получено %q, ожидалось DDR3", got)
	}
	if got := memoryTypeName(0, 0); got != "" {
		t.Errorf("неизвестный код: получено %q, ожидалась пустая строка", got)
	}
}

func TestВидКорпуса(t *testing.T) {
	// От вида корпуса зависит, ждать ли машину в сети постоянно: неделя
	// молчания ноутбука — норма, неделя молчания настольной машины — повод
	// спросить.
	случаи := map[string]struct {
		коды     []int32
		ожидание string
	}{
		"ноутбук":     {[]int32{10}, "laptop"},
		"настольный":  {[]int32{3}, "desktop"},
		"планшет":     {[]int32{30}, "tablet"},
		"стойка":      {[]int32{23}, "server"},
		"неизвестный": {[]int32{2}, ""},
		"пусто":       {nil, ""},
	}

	for имя, с := range случаи {
		if got := chassisKind(с.коды); got != с.ожидание {
			t.Errorf("%s: получено %q, ожидалось %q", имя, got, с.ожидание)
		}
	}
}

func TestВидНакопителя(t *testing.T) {
	// Достоверный ответ есть только на Windows 8 и новее. На Windows 7
	// остаётся модель, и угадывание по ней должно работать на том, что
	// производители действительно пишут.
	случаи := []struct {
		имя      string
		media    uint16
		bus      uint16
		известно bool
		модель   string
		ожидание contract.DiskKind
	}{
		{"система знает: SSD", mediaSSD, 8, true, "какая-то модель", contract.DiskSSD},
		{"система знает: HDD", mediaHDD, 8, true, "какая-то модель", contract.DiskHDD},
		{"шина NVMe важнее вида", mediaSSD, busNVMe, true, "", contract.DiskNVMe},
		{"Windows 7: SSD в модели", 0, 0, false, "Samsung SSD 870 EVO 250GB", contract.DiskSSD},
		{"Windows 7: NVMe в модели", 0, 0, false, "WDC PC SN730 NVMe", contract.DiskNVMe},
		{"Windows 7: обычный диск", 0, 0, false, "ST1000DM003-1SB102", contract.DiskUnknown},
	}

	for _, с := range случаи {
		got := diskKind(с.media, с.bus, с.известно, с.модель)
		if got != с.ожидание {
			t.Errorf("%s: получено %q, ожидалось %q", с.имя, got, с.ожидание)
		}
	}
}

func TestСостояниеДискаНеПридумывается(t *testing.T) {
	// Пустое значение означает «не знаем», а не «здоров»: диск, чей S.M.A.R.T.
	// не прочитался, не должен выглядеть исправным.
	if got := diskHealth("", 0, false); got != "" {
		t.Errorf("неизвестное состояние подменено на %q", got)
	}
	if got := diskHealth("Pred Fail", 0, true); got != "Pred Fail" {
		t.Errorf("предупреждение потеряно: %q", got)
	}
	if got := diskHealth("", healthBad, true); got != "Unhealthy" {
		t.Errorf("получено %q, ожидалось Unhealthy", got)
	}
}

func TestВиртуальныйИнтерфейсПоИмени(t *testing.T) {
	// У виртуальных адаптеров адреса меняются от запуска к запуску, и
	// опознание по ним давало бы новую машину при каждом включении VPN.
	виртуальные := []string{
		"vEthernet (Default Switch)", "VirtualBox Host-Only Network",
		"TAP-Windows Adapter V9", "Loopback Pseudo-Interface 1",
		"OpenVPN Wintun", "Bluetooth Network Connection",
	}
	for _, имя := range виртуальные {
		if !virtualByName(имя) {
			t.Errorf("%q не признан виртуальным", имя)
		}
	}

	настоящие := []string{"Ethernet", "Ethernet 2", "Realtek PCIe GbE Family Controller", "Wi-Fi"}
	for _, имя := range настоящие {
		if virtualByName(имя) {
			t.Errorf("%q ошибочно признан виртуальным", имя)
		}
	}
}

func TestПутьМонитораПриводитсяКОбщемуВиду(t *testing.T) {
	// WMI и реестр называют один и тот же монитор по-разному: приставка,
	// хвост и регистр. Без приведения списки не сходятся ни в одной записи, и
	// в отчёт не попадает ни один монитор.
	изWMI := normalizeMonitorInstance(`DISPLAY\AOC2270\4&9c24ace&0&UID65793_0`)
	изРеестра := normalizeMonitorInstance(`AOC2270\4&9c24ace&0&UID65793`)

	if изWMI != изРеестра {
		t.Errorf("не сошлись: %q и %q", изWMI, изРеестра)
	}
	if изWMI == "" {
		t.Error("путь схлопнулся в пустую строку")
	}
}

func TestШинаНакопителяНеОстаётсяIDE(t *testing.T) {
	// Проверено на живой машине: Win32_DiskDrive называет обычные диски SATA
	// словом IDE. В карточке это означает интерфейс, которого нет на
	// материнских платах пятнадцать лет.
	if got := diskInterface(11, true, "IDE"); got != "SATA" {
		t.Errorf("шина 11: получено %q, ожидалось SATA", got)
	}
	if got := diskInterface(busNVMe, true, "SCSI"); got != "NVMe" {
		t.Errorf("шина NVMe: получено %q", got)
	}
	// Windows 7: MSFT_PhysicalDisk нет, остаётся ответ Win32_DiskDrive.
	if got := diskInterface(0, false, "SATA"); got != "SATA" {
		t.Errorf("без MSFT_PhysicalDisk: получено %q", got)
	}
}

func TestАдресГипервизораНеСчитаетсяНастоящим(t *testing.T) {
	// Имя адаптера виртуальной машины не выдаёт: на живой машине адаптер
	// VirtualBox называется «Ethernet 2». Отличить его можно только по
	// адресу, а отличать надо — иначе машина окажется привязана к тому, что
	// исчезнет вместе с удалённым VirtualBox.
	виртуальные := []string{
		"0A-00-27-00-00-10", // VirtualBox, тот самый случай
		"00:15:5d:99:c7:b8", // Hyper-V
		"00:50:56:aa:bb:cc", // VMware
		"52:54:00:12:34:56", // QEMU
	}
	for _, mac := range виртуальные {
		if !virtualByMAC(mac) {
			t.Errorf("%q не признан адресом гипервизора", mac)
		}
	}

	настоящие := []string{"f8:32:e4:8b:a6:28", "a4:bb:6d:11:22:33", ""}
	for _, mac := range настоящие {
		if virtualByMAC(mac) {
			t.Errorf("%q ошибочно признан адресом гипервизора", mac)
		}
	}
}
