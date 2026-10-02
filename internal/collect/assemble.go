package collect

import (
	"math"
	"sort"
	"strings"
	"time"

	"fmt"
	"github.com/broton07/inventory_service/pkg/contract"
)

// bytesOf переводит объём, который система сообщает беззнаковым числом, в
// знаковый тип контракта.
//
// Проверка не формальность ради линтера: WMI на неисправном оборудовании
// отдаёт в поле размера все единицы, и без потолка диск получил бы
// отрицательный объём — а отрицательный объём в учёте выглядит как ошибка
// человека, а не как сбойная плата.
func bytesOf(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// placeholderUUID — то, что попадает в поле SMBIOS UUID вместо номера.
//
// Дешёвые материнские платы пишут туда все единицы или все нули целыми
// партиями. Опознание по такому значению склеило бы в одну машину весь парк
// на одинаковых платах, поэтому оно отбрасывается здесь, на машине, а не
// доезжает до службы.
var placeholderUUID = map[string]bool{
	"ffffffff-ffff-ffff-ffff-ffffffffffff": true,
	"00000000-0000-0000-0000-000000000000": true,
	"03000200-0400-0500-0006-000700080009": true, // встречается у клонов ВМ
}

// Assemble превращает сырые ответы системы в снимок контракта.
//
// Функция чистая: ничего не спрашивает у системы и не смотрит на часы. Всё,
// что зависит от платформы, уже лежит в Raw, а всё, что зависит от времени,
// приходит аргументом. Поэтому она проверяется тестами на любой машине,
// включая контейнер, в котором идёт разработка.
func Assemble(r Raw, mark, version string, origin contract.Origin, now time.Time) contract.Snapshot {
	return contract.Snapshot{
		SchemaVersion: contract.SchemaVersion,
		AgentID:       mark,
		AgentVersion:  version,
		CollectedAt:   now.UTC(),
		Origin:        origin,
		Machine:       machine(r),
		OS:            operatingSystem(r.OS),
		CPU:           processor(r.CPUs),
		Memory:        memory(r),
		Disks:         disks(r),
		Interfaces:    interfaces(r.NICs),
		Monitors:      monitors(r.Monitors),
		Software:      software(r.Software),
		Failures:      сбоиСбора(r),
	}
}

// сбоиСбора — неудачи опроса и то, что выясняется только при сборке снимка.
//
// Копия, а не r.Failures напрямую: append к срезу с запасом ёмкости дописал
// бы в чужой массив, и снимок, собранный дважды из одного Raw, получил бы
// причину дважды.
func сбоиСбора(r Raw) []contract.Failure {
	out := append([]contract.Failure(nil), r.Failures...)
	if f, есть := наработкаНеСобрана(r); есть {
		out = append(out, f)
	}
	return out
}

// наработкаНеСобрана объясняет, почему у дисков нет наработки, если опрос сам
// этого не записал.
//
// Заведено после проверки на живой машине: запуск с правами администратора
// прошёл без единой неудачи, а наработки не оказалось ни у одного диска — и
// отличить «диски её не сообщают» от «счётчики не сопоставились с дисками»
// было нечем. Теперь снимок называет причину сам, и её видно на любой машине
// парка без отдельной диагностики.
func наработкаНеСобрана(r Raw) (contract.Failure, bool) {
	// Windows 7 и сбор вне Windows: физических дисков нет, спрашивать нечего.
	if len(r.Physical) == 0 {
		return contract.Failure{}, false
	}
	// Причина уже записана при опросе — чаще всего отказ в доступе.
	for _, f := range r.Failures {
		if f.Section == "disk_hours" {
			return contract.Failure{}, false
		}
	}

	неудача := func(причина string) (contract.Failure, bool) {
		return contract.Failure{Section: "disk_hours", Reason: причина}, true
	}

	if len(r.Reliability) == 0 {
		return неудача("Windows не вернула ни одного счётчика надёжности ни для одного диска: " +
			"диски или драйвер контроллера наработку не сообщают")
	}

	ключи := make(map[string]bool, 2*len(r.Physical))
	номераДисков := make([]string, 0, len(r.Physical))
	for _, p := range r.Physical {
		for _, к := range ключиДиска(p) {
			ключи[к] = true
		}
		номераДисков = append(номераДисков, strings.TrimSpace(p.DeviceId))
	}

	совпало, сЧасами := 0, 0
	номераСчётчиков := make([]string, 0, len(r.Reliability))
	for _, rc := range r.Reliability {
		номераСчётчиков = append(номераСчётчиков, strings.TrimSpace(rc.DeviceId))
		if ключи[ключСчётчика(rc)] {
			совпало++
			if rc.PowerOnHours > 0 {
				сЧасами++
			}
		}
	}

	switch {
	case совпало == 0:
		sort.Strings(номераДисков)
		sort.Strings(номераСчётчиков)
		return неудача(fmt.Sprintf(
			"счётчиков надёжности %d, но ни один не сопоставился с дисками: "+
				"номера счётчиков [%s], номера дисков [%s]",
			len(r.Reliability), strings.Join(номераСчётчиков, ", "), strings.Join(номераДисков, ", ")))
	case сЧасами == 0:
		return неудача("счётчики надёжности есть, но наработку в часах диски не сообщают")
	}
	// Часть дисков с наработкой, часть без — не неудача: у дисков без неё
	// поле просто пустое, это «не знаем».
	return contract.Failure{}, false
}

func machine(r Raw) contract.Machine {
	m := contract.Machine{
		Hostname:     strings.TrimSpace(r.System.Name),
		Manufacturer: strings.TrimSpace(r.System.Manufacturer),
		Model:        strings.TrimSpace(r.System.Model),
		Serial:       strings.TrimSpace(r.BIOS.SerialNumber),
		Chassis:      chassisKind(r.Chassis.ChassisTypes),
	}

	// Домен или рабочая группа — одно поле: для учёта разницы нет, а два
	// поля пришлось бы объяснять каждому потребителю ленты.
	if r.System.PartOfDomain {
		m.Domain = strings.TrimSpace(r.System.Domain)
	} else {
		m.Domain = strings.TrimSpace(r.System.Workgroup)
	}

	if u := strings.ToLower(strings.TrimSpace(r.Product.UUID)); u != "" && !placeholderUUID[u] {
		m.UUID = u
	}

	// Плата — «производитель модель» одной строкой: в учётной системе под неё
	// заведён один атрибут, и разбирать её обратно там нечем.
	m.Motherboard = strings.TrimSpace(
		strings.TrimSpace(r.Board.Manufacturer) + " " + strings.TrimSpace(r.Board.Product))

	return m
}

func operatingSystem(o RawOS) contract.OS {
	os := contract.OS{
		Name:         strings.TrimSpace(o.Caption),
		Version:      strings.TrimSpace(o.Version),
		Build:        strings.TrimSpace(o.BuildNumber),
		Architecture: strings.TrimSpace(o.OSArchitecture),
	}
	if t, ok := parseCIMDate(o.InstallDate); ok {
		os.InstalledAt = &t
	}
	if t, ok := parseCIMDate(o.LastBootUpTime); ok {
		os.LastBootAt = &t
	}
	return os
}

// processor сводит процессоры в один.
//
// Двухпроцессорные машины в парке рабочих станций редкость, но если такая
// есть, «4 ядра» вместо восьми — это неверная карточка. Ядра складываются,
// модель берётся от первого.
func processor(cpus []RawProcessor) contract.CPU {
	var cpu contract.CPU
	for i, c := range cpus {
		if i == 0 {
			cpu.Model = strings.TrimSpace(c.Name)
			cpu.MHz = int(c.MaxClockSpeed)
		}
		cpu.Cores += int(c.NumberOfCores)
		cpu.Threads += int(c.NumberOfLogicalProcessors)
	}
	return cpu
}

func memory(r Raw) contract.Memory {
	m := contract.Memory{TotalBytes: bytesOf(r.System.TotalPhysicalMemory)}

	var сумма int64
	for _, mod := range r.Memory {
		m.Modules = append(m.Modules, contract.MemoryModule{
			SizeBytes:    bytesOf(mod.Capacity),
			Type:         memoryTypeName(mod.SMBIOSMemoryType, mod.MemoryType),
			MHz:          int(mod.Speed),
			Slot:         strings.TrimSpace(mod.DeviceLocator),
			Manufacturer: strings.TrimSpace(mod.Manufacturer),
			Serial:       strings.TrimSpace(mod.SerialNumber),
		})
		сумма += bytesOf(mod.Capacity)
	}

	// Win32_ComputerSystem занижает объём на 32-битных системах и молчит на
	// части виртуальных машин. Планки в таком случае ближе к правде.
	if m.TotalBytes == 0 {
		m.TotalBytes = сумма
	}

	return m
}

// disks — накопители машины.
//
// Источников два, и главный из них — MSFT_PhysicalDisk: он перечисляет именно
// ФИЗИЧЕСКИЕ диски и знает о них всё, включая вид, шину, заводской номер и
// состояние. Win32_DiskDrive остаётся запасным: этого класса не было до
// Windows 8, а машины на Windows 7 в парке есть.
//
// Почему главный именно он — выяснилось на живой машине с тремя дисками.
// Win32_DiskDrive показывал два из них и вместо третьего — «Microsoft Storage
// Space Device»: диск, отданный в дисковое пространство, система из этого
// списка убирает и подставляет вместо него виртуальный том. То есть по
// Win32_DiskDrive нельзя ни узнать, что диск есть, ни не посчитать лишнего:
// один диск пропадал, а вместо него считался том, собранный из него же.
func disks(r Raw) []contract.Disk {
	out := physicalDisks(r.Physical, r.Reliability)
	if len(out) == 0 {
		out = legacyDisks(r.Disks)
	}

	// Порядок дисков система не гарантирует, а сравнение снимков сравнивает
	// списки: переставленные местами диски выглядели бы заменой.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Serial < out[j].Serial
	})

	return out
}

func physicalDisks(list []RawPhysicalDisk, reliability []RawReliability) []contract.Disk {
	// Наработка по ключу диска — см. ключСчётчика.
	часы := make(map[string]uint32, len(reliability))
	for _, rc := range reliability {
		часы[ключСчётчика(rc)] = rc.PowerOnHours
	}

	out := make([]contract.Disk, 0, len(list))
	for _, p := range list {
		out = append(out, contract.Disk{
			Model:     strings.TrimSpace(p.FriendlyName),
			Serial:    strings.TrimSpace(p.SerialNumber),
			Kind:      diskKind(p.MediaType, p.BusType, true, p.FriendlyName),
			SizeBytes: bytesOf(p.Size),
			Interface: diskInterface(p.BusType, true, ""),
			Health:    diskHealth("", p.HealthStatus, true),
			// Нет счётчика — ноль, а ноль с omitempty в снимок не попадает:
			// «не знаем», а не «диск новый».
			PowerOnHours: int(наработкаДиска(часы, p)),
		})
	}
	return out
}

// legacyDisks — путь для Windows 7 и для сбора вне Windows.
//
// Здесь вид накопителя приходится угадывать по модели, а состояние берётся из
// текстового Status — того самого, где встречается «Pred Fail».
func legacyDisks(list []RawDiskDrive) []contract.Disk {
	out := make([]contract.Disk, 0, len(list))
	for _, d := range list {
		out = append(out, contract.Disk{
			Model:     strings.TrimSpace(d.Model),
			Serial:    strings.TrimSpace(d.SerialNumber),
			Kind:      diskKind(0, 0, false, d.Model),
			SizeBytes: bytesOf(d.Size),
			Interface: strings.TrimSpace(d.InterfaceType),
			Health:    diskHealth(d.Status, 0, false),
		})
	}
	return out
}

func interfaces(nics []RawNIC) []contract.Interface {
	out := make([]contract.Interface, 0, len(nics))
	for _, n := range nics {
		// Виртуальным считается интерфейс, на который указал хоть один
		// признак, а не тот, на который указали оба.
		//
		// Проверено на живой машине: Win32_NetworkAdapter.PhysicalAdapter
		// отвечает «настоящая карта» для коммутаторов Hyper-V — vEthernet
		// (WSL), vEthernet (Default Switch), — потому что они привязаны к
		// физической. Довериться этому ответу значит опознавать машину по
		// адресу, который меняется при переустановке WSL.
		virtual := virtualByName(n.Name) ||
			virtualByMAC(n.MAC) ||
			(n.PhysicalKnown && !n.Physical)

		out = append(out, contract.Interface{
			Name:       strings.TrimSpace(n.Name),
			MAC:        contract.NormalizeMAC(n.MAC),
			IPv4:       n.IPv4,
			IPv6:       n.IPv6,
			Gateway:    n.Gateway,
			DNS:        n.DNS,
			DHCP:       n.DHCP,
			Virtual:    virtual,
			SpeedMbits: n.SpeedMbits,
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].MAC < out[j].MAC })
	return out
}

func monitors(list []RawMonitor) []contract.Monitor {
	out := make([]contract.Monitor, 0, len(list))
	for _, m := range list {
		out = append(out, contract.Monitor{
			Model:        m.Model,
			Serial:       m.Serial,
			Manufacturer: m.Manufacturer,
			DiagonalInch: m.DiagonalInch,
			Year:         m.Year,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Manufacturer != out[j].Manufacturer {
			return out[i].Manufacturer < out[j].Manufacturer
		}
		return out[i].Serial < out[j].Serial
	})
	return out
}

// MaxSoftware — сколько программ попадает в отчёт.
//
// На машине с полным набором инженерного софта их бывает под тысячу, и отчёт
// вылезает за предел размера, который принимает служба. Пятьсот — с запасом
// больше, чем встречается в жизни, и вдвое меньше предела.
const MaxSoftware = 500

func software(list []RawProgram) []contract.Program {
	out := make([]contract.Program, 0, len(list))
	for _, p := range list {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		out = append(out, contract.Program{
			Name:        name,
			Version:     strings.TrimSpace(p.Version),
			Publisher:   strings.TrimSpace(p.Publisher),
			InstalledAt: installDate(p.InstallDate),
		})
	}

	// Порядок реестра случаен, а сравнение снимков сравнивает списки: без
	// сортировки каждый отчёт выглядел бы как изменившийся состав программ.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})

	if len(out) > MaxSoftware {
		out = out[:MaxSoftware]
	}
	return out
}
