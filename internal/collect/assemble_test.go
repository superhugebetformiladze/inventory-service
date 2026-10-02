package collect

import (
	"strings"
	"testing"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

var времяСбора = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

// сырое — ответы системы с настольной машины, каких в парке большинство.
func сырое() Raw {
	const гб = uint64(1) << 30

	return Raw{
		System: RawSystem{
			Name: "PC-214-01", Domain: "zavod.local", PartOfDomain: true,
			Manufacturer: "Dell Inc.", Model: "OptiPlex 3080",
			TotalPhysicalMemory: 16 * гб,
		},
		BIOS:    RawBIOS{SerialNumber: "7QK2M63"},
		Product: RawProduct{UUID: "4C4C4544-0051-4B10-8032-B4C04F4D3633"},
		Board:   RawBoard{Manufacturer: "Dell Inc.", Product: "0WMJ54"},
		Chassis: RawChassis{ChassisTypes: []int32{3}},
		OS: RawOS{
			Caption: "Microsoft Windows 10 Pro", Version: "10.0.19045",
			BuildNumber: "19045", OSArchitecture: "64-bit",
			InstallDate: "20230115103000.000000+180",
		},
		CPUs: []RawProcessor{{
			Name:          "Intel(R) Core(TM) i5-10500 CPU @ 3.10GHz",
			NumberOfCores: 6, NumberOfLogicalProcessors: 12, MaxClockSpeed: 3100,
		}},
		Memory: []RawMemoryModule{
			{Capacity: 8 * гб, Speed: 2666, DeviceLocator: "DIMM1", SMBIOSMemoryType: 26},
			{Capacity: 8 * гб, Speed: 2666, DeviceLocator: "DIMM2", SMBIOSMemoryType: 26},
		},
		Disks: []RawDiskDrive{{
			Index: 0, Model: "SK hynix BC511 NVMe", SerialNumber: "AJ01N654",
			InterfaceType: "SCSI", Size: 256 * гб, Status: "OK",
		}},
		Physical: []RawPhysicalDisk{{
			DeviceId: "0", FriendlyName: "SK hynix BC511",
			MediaType: mediaSSD, BusType: busNVMe, Size: 256 * гб, HealthStatus: healthOK,
		}},
		Reliability: []RawReliability{{DeviceId: "0", PowerOnHours: 1200}},
		NICs: []RawNIC{{
			Name: "Ethernet", MAC: "F0-1F-AF-2B-3C-4D", IPv4: []string{"10.0.14.31"},
			Gateway: "10.0.14.1", DHCP: true, SpeedMbits: 1000,
			Physical: true, PhysicalKnown: true,
		}},
	}
}

func TestСнимокСобираетсяЦеликом(t *testing.T) {
	snap := Assemble(сырое(), "отметка-1", "0.7.0", contract.OriginNetwork, времяСбора)

	if err := snap.Validate(времяСбора); err != nil {
		t.Fatalf("собранный снимок не проходит проверку контрактом: %v", err)
	}

	if snap.Machine.Hostname != "PC-214-01" {
		t.Errorf("имя машины: %q", snap.Machine.Hostname)
	}
	if snap.Machine.Motherboard != "Dell Inc. 0WMJ54" {
		t.Errorf("плата: %q", snap.Machine.Motherboard)
	}
	if snap.CPU.Cores != 6 || snap.CPU.Threads != 12 {
		t.Errorf("процессор: %d ядер, %d потоков", snap.CPU.Cores, snap.CPU.Threads)
	}
	if snap.Memory.TotalBytes != 16<<30 {
		t.Errorf("память: %d байт", snap.Memory.TotalBytes)
	}
	if len(snap.Disks) != 1 || snap.Disks[0].Kind != contract.DiskNVMe {
		t.Errorf("диски: %+v", snap.Disks)
	}
	if snap.OS.InstalledAt == nil {
		t.Error("дата установки системы потеряна")
	}
}

func TestАдресКартыПриводитсяКОдномуВиду(t *testing.T) {
	// WMI отдаёт адрес через дефис, Linux — через двоеточие. Без приведения
	// опознание сочло бы это двумя машинами.
	snap := Assemble(сырое(), "отметка-1", "0.7.0", contract.OriginNetwork, времяСбора)

	if got := snap.Interfaces[0].MAC; got != "f0:1f:af:2b:3c:4d" {
		t.Errorf("адрес карты: %q", got)
	}
}

func TestЗаглушкаВUUIDОтбрасывается(t *testing.T) {
	// Дешёвые платы пишут в SMBIOS UUID все единицы целыми партиями.
	// Опознание по такому значению склеило бы в одну машину весь парк.
	r := сырое()
	r.Product.UUID = "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF"

	snap := Assemble(r, "отметка-1", "0.7.0", contract.OriginNetwork, времяСбора)

	if snap.Machine.UUID != "" {
		t.Errorf("заглушка доехала до службы: %q", snap.Machine.UUID)
	}
}

func TestПорядокСпискаНеПлаваетМеждуОтчётами(t *testing.T) {
	// Инвариант I9: событие только при фактическом расхождении. Система не
	// гарантирует порядок ни дисков, ни программ, и без сортировки каждый
	// второй отчёт выглядел бы как замена оборудования.
	r := сырое()
	r.Disks = append(r.Disks, RawDiskDrive{Index: 1, Model: "ST1000DM003", Size: 1 << 40})
	r.Software = []RawProgram{
		{Name: "Яндекс Браузер", Version: "24.1"},
		{Name: "7-Zip", Version: "23.01"},
		{Name: "Google Chrome", Version: "120.0"},
	}

	первый := Assemble(r, "отметка-1", "0.7.0", contract.OriginNetwork, времяСбора)

	// Тот же набор, пришедший от системы в другом порядке.
	r.Disks[0], r.Disks[1] = r.Disks[1], r.Disks[0]
	r.Software[0], r.Software[2] = r.Software[2], r.Software[0]

	второй := Assemble(r, "отметка-1", "0.7.0", contract.OriginNetwork, времяСбора)

	for i := range первый.Disks {
		if первый.Disks[i].Model != второй.Disks[i].Model {
			t.Fatalf("диски разъехались: %q против %q",
				первый.Disks[i].Model, второй.Disks[i].Model)
		}
	}
	for i := range первый.Software {
		if первый.Software[i].Name != второй.Software[i].Name {
			t.Fatalf("программы разъехались: %q против %q",
				первый.Software[i].Name, второй.Software[i].Name)
		}
	}
}

func TestНеудачаРазделаПопадаетВСнимок(t *testing.T) {
	// Пустой раздел без записи о неудаче неотличим от машины, у которой
	// этого действительно нет, и служба честно запишет «диски исчезли».
	r := сырое()
	r.Disks = nil
	r.Fail("disks", errТест)

	snap := Assemble(r, "отметка-1", "0.7.0", contract.OriginNetwork, времяСбора)

	if len(snap.Failures) != 1 || snap.Failures[0].Section != "disks" {
		t.Fatalf("неудача не записана: %+v", snap.Failures)
	}
	if err := snap.Validate(времяСбора); err != nil {
		t.Errorf("частичный снимок должен оставаться годным: %v", err)
	}
}

func TestОбъёмПамятиБерётсяИзПланокЕслиСистемаМолчит(t *testing.T) {
	r := сырое()
	r.System.TotalPhysicalMemory = 0

	snap := Assemble(r, "отметка-1", "0.7.0", contract.OriginNetwork, времяСбора)

	if snap.Memory.TotalBytes != 16<<30 {
		t.Errorf("объём памяти: %d байт, ожидалось 16 ГБ", snap.Memory.TotalBytes)
	}
}

var errТест = ошибка("WMI не ответил")

type ошибка string

func (о ошибка) Error() string { return string(о) }

func TestКоммутаторHyperVНеСчитаетсяНастоящейКартой(t *testing.T) {
	// Проверено на живой машине: Win32_NetworkAdapter.PhysicalAdapter
	// отвечает «настоящая карта» для коммутаторов Hyper-V — они привязаны к
	// физической. Довериться этому ответу значит опознавать машину по адресу,
	// который меняется при переустановке WSL.
	r := сырое()
	r.NICs = []RawNIC{
		{Name: "vEthernet (WSL)", MAC: "00:15:5d:99:c7:b8", Physical: true, PhysicalKnown: true},
		{Name: "vEthernet (Default Switch)", MAC: "00:15:5d:ae:15:8a", Physical: true, PhysicalKnown: true},
		{Name: "Ethernet", MAC: "f0:1f:af:2b:3c:4d", Physical: true, PhysicalKnown: true},
		{Name: "WAN Miniport (IP)", MAC: "14:a5:20:52:41:53", Physical: false, PhysicalKnown: true},
	}

	snap := Assemble(r, "отметка-1", "0.7.0", contract.OriginNetwork, времяСбора)

	физические := snap.PhysicalInterfaces()
	if len(физические) != 1 {
		t.Fatalf("настоящих карт: %d, ожидалась одна", len(физические))
	}
	if физические[0].Name != "Ethernet" {
		t.Errorf("настоящей картой признана %q", физические[0].Name)
	}
}

func TestДискВДисковомПространствеНеТеряется(t *testing.T) {
	// Снято с живой машины с тремя дисками. Win32_DiskDrive показывает два из
	// них, а вместо третьего — «Microsoft Storage Space Device»: диск,
	// отданный в дисковое пространство, система из этого списка убирает и
	// подставляет вместо него виртуальный том.
	//
	// То есть по одному Win32_DiskDrive нельзя ни увидеть третий диск, ни не
	// посчитать лишнего: один пропадает, а вместо него считается том,
	// собранный из него же.
	const гб = uint64(1_000_000_000)

	r := сырое()
	r.Disks = []RawDiskDrive{
		{Index: 1, Model: "WDC WD10EZEX-08WN4A1", Size: 1000204886016, Status: "OK"},
		{Index: 3, Model: "Microsoft Storage Space Device", Size: 930 * гб, Status: "OK"},
		{Index: 0, Model: "WDC WDS240G2G0A-00JH30", Size: 240057409536, Status: "OK"},
	}
	r.Physical = []RawPhysicalDisk{
		{DeviceId: "1", FriendlyName: "WDC WD10EZEX-08WN4A1", SerialNumber: "WCC4M3EX4PPZ",
			MediaType: mediaHDD, BusType: 11, Size: 1000204886016},
		{DeviceId: "0", FriendlyName: "WDC WDS240G2G0A-00JH30", SerialNumber: "1948GR441009",
			MediaType: mediaSSD, BusType: 11, Size: 240057409536},
		{DeviceId: "2", FriendlyName: "WDC WD10EZEX-22MFCA0", SerialNumber: "WD-WCC6Y0TRNC0N",
			MediaType: mediaHDD, BusType: 11, Size: 1000204886016},
	}

	snap := Assemble(r, "отметка-1", "0.7.0", contract.OriginNetwork, времяСбора)

	if len(snap.Disks) != 3 {
		t.Fatalf("дисков в снимке: %d, ожидалось три", len(snap.Disks))
	}
	for _, d := range snap.Disks {
		if strings.Contains(d.Model, "Storage Space") {
			t.Errorf("виртуальный том попал в снимок как диск: %q", d.Model)
		}
		if d.Serial == "" {
			t.Errorf("у диска %q нет заводского номера", d.Model)
		}
	}

	// Два по терабайту и один на 240 ГБ.
	const ожидалось = 2*1000204886016 + 240057409536
	if got := snap.TotalDiskBytes(); got != ожидалось {
		t.Errorf("суммарный объём %d байт, ожидалось %d", got, ожидалось)
	}
}

func TestНаWindows7ДискиНеОтбрасываются(t *testing.T) {
	// MSFT_PhysicalDisk появился в Windows 8. Там, где его нет, отбрасывать
	// «не найденные среди физических» значило бы отбросить все диски сразу.
	r := сырое()
	r.Physical = nil

	snap := Assemble(r, "отметка-1", "0.7.0", contract.OriginNetwork, времяСбора)

	if len(snap.Disks) != 1 {
		t.Fatalf("дисков в снимке: %d, ожидался один", len(snap.Disks))
	}
}

func TestНаработкаПопадаетНаСвойДиск(t *testing.T) {
	const гб = uint64(1) << 30

	// Наработка — единственный признак, по которому видно, что диск отработал
	// своё. Перепутать диски значит пообещать замену исправному.
	физические := []RawPhysicalDisk{
		{DeviceId: "0", FriendlyName: "WDC WDS240G2G0A", MediaType: mediaSSD, Size: 240 * гб},
		{DeviceId: "1", FriendlyName: "WDC WD10EZEX", Size: 1000 * гб},
		{DeviceId: "2", FriendlyName: "WDC WD10EZEX", Size: 1000 * гб},
	}
	счётчики := []RawReliability{
		{DeviceId: "2", PowerOnHours: 31000},
		{DeviceId: "0", PowerOnHours: 14322},
	}

	диски := physicalDisks(физические, счётчики)

	if диски[0].PowerOnHours != 14322 {
		t.Errorf("диск 0: наработка %d, ожидалось 14322", диски[0].PowerOnHours)
	}
	if диски[2].PowerOnHours != 31000 {
		t.Errorf("диск 2: наработка %d, ожидалось 31000", диски[2].PowerOnHours)
	}
	// У диска 1 счётчика нет — например, отказ в доступе к нему. Ноль в
	// снимок не попадает: это «не знаем», а не «новый диск».
	if диски[1].PowerOnHours != 0 {
		t.Errorf("диск 1 без счётчика получил наработку %d", диски[1].PowerOnHours)
	}
}

func TestБезСчётчиковДискиСобираютсяКакРаньше(t *testing.T) {
	const гб = uint64(1) << 30

	// Разовый запуск не от администратора: счётчиков нет вовсе. Сами диски
	// от этого пропадать не должны — наработка дополняет снимок, а не
	// условие его сборки.
	диски := physicalDisks([]RawPhysicalDisk{{DeviceId: "0", FriendlyName: "SK hynix BC511", Size: 256 * гб}}, nil)

	if len(диски) != 1 || диски[0].Model != "SK hynix BC511" || диски[0].PowerOnHours != 0 {
		t.Errorf("диски без счётчиков: %+v", диски)
	}
}

func причинаНаработки(r Raw) string {
	f, есть := наработкаНеСобрана(r)
	if !есть {
		return ""
	}
	return f.Reason
}

func TestПричинаПустойНаработкиНазываетсяСнимком(t *testing.T) {
	диск := []RawPhysicalDisk{{DeviceId: "0"}, {DeviceId: "1"}}

	случаи := []struct {
		имя  string
		r    Raw
		ждём string
	}{
		{"счётчиков нет вовсе", Raw{Physical: диск}, "не вернула ни одного счётчика"},
		{"номера не совпали", Raw{Physical: диск, Reliability: []RawReliability{{DeviceId: "{a1}", PowerOnHours: 900}}},
			"номера счётчиков [{a1}], номера дисков [0, 1]"},
		{"совпали, но без часов", Raw{Physical: диск, Reliability: []RawReliability{{DeviceId: "0"}, {DeviceId: "1"}}},
			"наработку в часах диски не сообщают"},
	}
	for _, c := range случаи {
		if got := причинаНаработки(c.r); !strings.Contains(got, c.ждём) {
			t.Errorf("%s: причина %q, ожидалось содержащее %q", c.имя, got, c.ждём)
		}
	}
}

func TestНаработкаБезЛишнихПричин(t *testing.T) {
	диск := []RawPhysicalDisk{{DeviceId: "0"}, {DeviceId: "1"}}

	случаи := []struct {
		имя string
		r   Raw
	}{
		// Отказ в доступе уже записан опросом — второй записи о том же не нужно.
		{"причина уже записана", Raw{Physical: диск,
			Failures: []contract.Failure{{Section: "disk_hours", Reason: "Отказано в доступе"}}}},
		// Windows 7: физических дисков нет, и отсутствие счётчиков — не неудача.
		{"Windows 7", Raw{}},
		// Наработка есть хотя бы у одного диска — сбор работает.
		{"часть дисков с наработкой", Raw{Physical: диск,
			Reliability: []RawReliability{{DeviceId: "0", PowerOnHours: 14322}, {DeviceId: "1"}}}},
	}
	for _, c := range случаи {
		if got := причинаНаработки(c.r); got != "" {
			t.Errorf("%s: лишняя причина %q", c.имя, got)
		}
	}
}

func TestПричинаНеДублируетсяПриПовторнойСборке(t *testing.T) {
	// Снимок из одного Raw собирается и для отправки, и для файла: причина
	// не должна копиться от сборки к сборке.
	r := Raw{Physical: []RawPhysicalDisk{{DeviceId: "0"}}, Failures: make([]contract.Failure, 0, 8)}

	первый := сбоиСбора(r)
	второй := сбоиСбора(r)

	if len(первый) != 1 || len(второй) != 1 || len(r.Failures) != 0 {
		t.Errorf("причины: первый %d, второй %d, в Raw %d", len(первый), len(второй), len(r.Failures))
	}
}

func TestЗапросНаработкиЭкранируетИдентификаторДиска(t *testing.T) {
	// Настоящий ObjectId диска содержит и обратные косые черты, и кавычки.
	// Неэкранированные, они дают не пустой ответ, а синтаксическую ошибку WQL.
	запрос := запросНаработки("a\\b\"c")

	ждём := `ObjectId="a\\b\"c"}`
	if !strings.Contains(запрос, ждём) {
		t.Errorf("запрос %q не содержит %q", запрос, ждём)
	}
	if !strings.HasPrefix(запрос, "ASSOCIATORS OF {MSFT_PhysicalDisk.") ||
		!strings.HasSuffix(запрос, "AssocClass = MSFT_PhysicalDiskToStorageReliabilityCounter") {
		t.Errorf("запрос построен не через связь с диском: %q", запрос)
	}
}

func TestНаработкаСопоставляетсяПоДискуУКоторогоСпрашивали(t *testing.T) {
	// Номер устройства в счётчике может быть записан не так, как у диска. Когда
	// известно, у какого диска счётчик спрашивали, номер не нужен вовсе.
	диски := []RawPhysicalDisk{
		{DeviceId: "0", ObjectId: "{obj-ssd}", FriendlyName: "WDC WDS240G2G0A"},
		{DeviceId: "1", ObjectId: "{obj-hdd}", FriendlyName: "WDC WD10EZEX"},
	}
	счётчики := []RawReliability{
		{DeviceId: "{другой-формат-1}", PowerOnHours: 31000, DiskObjectId: "{obj-hdd}"},
		{DeviceId: "{другой-формат-0}", PowerOnHours: 14322, DiskObjectId: "{obj-ssd}"},
	}

	got := physicalDisks(диски, счётчики)

	if got[0].PowerOnHours != 14322 || got[1].PowerOnHours != 31000 {
		t.Errorf("наработка: SSD %d, HDD %d", got[0].PowerOnHours, got[1].PowerOnHours)
	}
	if причина := причинаНаработки(Raw{Physical: диски, Reliability: счётчики}); причина != "" {
		t.Errorf("при сопоставленных счётчиках лишняя причина: %q", причина)
	}
}
