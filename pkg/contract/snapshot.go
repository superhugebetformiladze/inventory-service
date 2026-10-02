package contract

import (
	"strings"
	"time"
)

// SchemaVersion — версия контракта снимка. Растёт только при несовместимом
// изменении; добавление поля версию не меняет (см. doc.go).
const SchemaVersion = 1

// Snapshot — всё, что агент рассказал о своей машине за один сбор.
//
// Служба не доверяет полям, влияющим на опознание, слепо: Machine.Serial и
// адреса сетевых карт машина может сообщить какие угодно. Проверка ключа
// агента — отдельно (I7), опознание — отдельно (I8).
type Snapshot struct {
	SchemaVersion int `json:"schema_version"`

	// AgentID — отметка агента: идентификатор, который агент записал у себя
	// при первом запуске и с тех пор не меняет. Первое и самое надёжное
	// звено цепочки опознания: переименование машины и замена диска его не
	// трогают.
	AgentID string `json:"agent_id"`

	// AgentVersion — версия программы-агента. Нужна не для красоты: после
	// первого же обновления в парке будут одновременно жить две-три версии,
	// и «почему у этих машин нет поля X» отвечается только этим полем.
	AgentVersion string `json:"agent_version"`

	// CollectedAt — когда сняли, в UTC. Именно по нему отбрасываются
	// устаревшие снимки: доставка не гарантирует порядок, и отчёт,
	// пришедший позже, может быть снят раньше.
	CollectedAt time.Time `json:"collected_at"`

	// Origin — как отчёт добрался до службы.
	Origin Origin `json:"origin"`

	Machine    Machine     `json:"machine"`
	OS         OS          `json:"os"`
	CPU        CPU         `json:"cpu"`
	Memory     Memory      `json:"memory"`
	Disks      []Disk      `json:"disks"`
	Interfaces []Interface `json:"interfaces"`

	Monitors []Monitor `json:"monitors,omitempty"`
	Software []Program `json:"software,omitempty"`

	// Failures — что собрать не удалось.
	//
	// Без этого поля снимок без дисков неотличим от машины без дисков, и
	// служба честно запишет «диски исчезли», а потребитель — «диск вынули».
	// Частичный сбор — это норма, а не сбой: WMI на старой Windows регулярно
	// отдаёт не всё.
	Failures []Failure `json:"failures,omitempty"`
}

// Origin — путь, которым отчёт попал в службу.
type Origin string

const (
	// OriginNetwork — агент отправил сам по сети. Обычный путь.
	OriginNetwork Origin = "network"

	// OriginOffline — отчёт снят на машине без сети, перенесён файлом и
	// загружен человеком. Для 500–1000 машин часть парка иначе не
	// охватывается вовсе (docs/PLAN.md §10).
	OriginOffline Origin = "offline"
)

// Machine — то, что отличает эту машину от остальных.
type Machine struct {
	// Hostname — имя компьютера. Слабый признак: его меняют.
	Hostname string `json:"hostname"`

	// Domain — домен или рабочая группа.
	Domain string `json:"domain,omitempty"`

	// Serial — заводской номер из BIOS. Второе звено цепочки опознания.
	// На сборных машинах пустой или буквально "To Be Filled By O.E.M." —
	// такие значения служба обязана считать отсутствующими.
	Serial string `json:"serial,omitempty"`

	// UUID — идентификатор из SMBIOS. Стабильнее серийного номера, но у
	// клонированных виртуальных машин совпадает, поэтому не первое звено.
	UUID string `json:"uuid,omitempty"`

	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`

	// Motherboard — материнская плата: в учётной системе для неё заведён
	// отдельный атрибут, и люди им пользуются.
	Motherboard string `json:"motherboard,omitempty"`

	// Chassis — desktop, laptop, virtual и подобное. От него зависит,
	// ожидать ли машину в сети постоянно.
	Chassis string `json:"chassis,omitempty"`
}

// OS — операционная система.
type OS struct {
	Name         string     `json:"name"`
	Version      string     `json:"version,omitempty"`
	Build        string     `json:"build,omitempty"`
	Architecture string     `json:"architecture,omitempty"`
	InstalledAt  *time.Time `json:"installed_at,omitempty"`
	LastBootAt   *time.Time `json:"last_boot_at,omitempty"`
}

// CPU — процессор.
type CPU struct {
	Model string `json:"model"`

	// Cores — физические ядра, Threads — логические. Разделены потому, что
	// «4 ядра» в учёте у разных людей означает разное.
	Cores   int `json:"cores,omitempty"`
	Threads int `json:"threads,omitempty"`

	MHz int `json:"mhz,omitempty"`
}

// Memory — оперативная память.
type Memory struct {
	// TotalBytes — суммарный объём. Именно байты, а не гигабайты: см. doc.go.
	TotalBytes int64 `json:"total_bytes"`

	// Modules — планки по отдельности. Нужны для ответа на вопрос «куда
	// можно доставить память, не выбрасывая имеющуюся».
	Modules []MemoryModule `json:"modules,omitempty"`
}

// MemoryModule — одна планка.
type MemoryModule struct {
	SizeBytes    int64  `json:"size_bytes"`
	Type         string `json:"type,omitempty"` // DDR3, DDR4, ...
	MHz          int    `json:"mhz,omitempty"`
	Slot         string `json:"slot,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Serial       string `json:"serial,omitempty"`
}

// DiskKind — вид накопителя.
type DiskKind string

const (
	DiskHDD     DiskKind = "hdd"
	DiskSSD     DiskKind = "ssd"
	DiskNVMe    DiskKind = "nvme"
	DiskUnknown DiskKind = "unknown"
)

// Disk — накопитель.
type Disk struct {
	Model     string   `json:"model,omitempty"`
	Serial    string   `json:"serial,omitempty"`
	Kind      DiskKind `json:"kind"`
	SizeBytes int64    `json:"size_bytes"`
	Interface string   `json:"interface,omitempty"` // SATA, NVMe, USB

	// Health — состояние по S.M.A.R.T., если удалось прочитать. Пустое
	// значение означает «не знаем», а не «здоров».
	Health string `json:"health,omitempty"`

	// PowerOnHours — наработка. Единственный признак, по которому диск,
	// который скоро умрёт, виден заранее.
	PowerOnHours int `json:"power_on_hours,omitempty"`
}

// Interface — сетевой интерфейс.
type Interface struct {
	Name string `json:"name"`

	// MAC — в каноническом виде: строчными, через двоеточие. Приводится
	// NormalizeMAC на стороне агента, но служба всё равно нормализует
	// повторно: опознание сравнивает строки, и «AA-BB» против «aa:bb»
	// означало бы дубликат хоста.
	MAC string `json:"mac,omitempty"`

	IPv4       []string `json:"ipv4,omitempty"`
	IPv6       []string `json:"ipv6,omitempty"`
	Gateway    string   `json:"gateway,omitempty"`
	DNS        []string `json:"dns,omitempty"`
	DHCP       bool     `json:"dhcp,omitempty"`
	Virtual    bool     `json:"virtual,omitempty"` // VPN, Hyper-V, loopback
	SpeedMbits int      `json:"speed_mbits,omitempty"`
}

// Monitor — подключённый монитор. В учётной системе для мониторов есть
// отдельный тип объектов, поэтому данные пригодятся не только в карточке
// компьютера.
type Monitor struct {
	Model        string `json:"model,omitempty"`
	Serial       string `json:"serial,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	DiagonalInch int    `json:"diagonal_inch,omitempty"`
	Year         int    `json:"year,omitempty"`
}

// Program — установленная программа.
type Program struct {
	Name        string     `json:"name"`
	Version     string     `json:"version,omitempty"`
	Publisher   string     `json:"publisher,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
}

// Failure — то, что собрать не удалось.
type Failure struct {
	// Section — что именно: disks, software, monitors.
	Section string `json:"section"`
	Reason  string `json:"reason"`
}

// TotalDiskBytes — суммарный объём накопителей.
//
// Внешние диски исключены намеренно: флешка, забытая в машине на момент
// сбора, не должна выглядеть как выросший объём хранилища и порождать
// событие об изменении.
func (s Snapshot) TotalDiskBytes() int64 {
	var total int64
	for _, d := range s.Disks {
		if strings.EqualFold(d.Interface, "USB") {
			continue
		}
		total += d.SizeBytes
	}
	return total
}

// PhysicalInterfaces — только настоящие сетевые карты.
//
// Виртуальные интерфейсы (VPN, Hyper-V, loopback) отброшены: их адреса
// меняются от запуска к запуску, и опознание по ним давало бы новый хост
// при каждом подключении к VPN.
func (s Snapshot) PhysicalInterfaces() []Interface {
	out := make([]Interface, 0, len(s.Interfaces))
	for _, i := range s.Interfaces {
		if i.Virtual || i.MAC == "" {
			continue
		}
		out = append(out, i)
	}
	return out
}

// NormalizeMAC приводит адрес к каноническому виду: строчные буквы, через
// двоеточие. Пустую строку возвращает как есть.
//
// Один и тот же адрес WMI отдаёт как "AA-BB-CC-DD-EE-FF", а ip как
// "aa:bb:cc:dd:ee:ff". Без приведения опознание сочло бы это двумя машинами.
func NormalizeMAC(mac string) string {
	if mac == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(17)

	n := 0
	for _, r := range strings.ToLower(mac) {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			if n > 0 && n%2 == 0 {
				b.WriteByte(':')
			}
			b.WriteRune(r)
			n++
		}
	}

	// Не шестнадцатеричный мусор возвращаем как есть: молча превращать
	// непонятное в пустую строку — значит терять след при разборе.
	if n != 12 {
		return strings.ToLower(strings.TrimSpace(mac))
	}
	return b.String()
}
