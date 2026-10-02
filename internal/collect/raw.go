// Package collect — сбор сведений о машине, на которой запущен агент.
//
// Пакет разделён надвое намеренно. Запросы к системе (WMI, реестр, /proc)
// живут в файлах с ограничением по платформе и проверяются только на живой
// машине. Всё, что превращает сырые ответы в снимок контракта, — обычные
// функции без обращений к системе: они собираются и проверяются где угодно,
// включая контейнер с Linux, в котором идёт разработка.
//
// Разделение не косметическое. Ошибка в разборе даты WMI или в определении
// вида накопителя — это тихо неверная карточка у сотен машин, и ловить её
// вручную на трёх версиях Windows пришлось бы каждым изменением.
package collect

import "github.com/broton07/inventory_service/pkg/contract"

// Raw — сырые ответы системы, ещё не превращённые в снимок.
//
// Имена полей внутри структур повторяют имена свойств WMI: библиотека
// сопоставляет их по имени, и переименование поля здесь тихо оставит его
// пустым.
type Raw struct {
	System   RawSystem
	BIOS     RawBIOS
	Product  RawProduct
	Board    RawBoard
	Chassis  RawChassis
	OS       RawOS
	CPUs     []RawProcessor
	Memory   []RawMemoryModule
	Disks    []RawDiskDrive
	Physical []RawPhysicalDisk

	// Reliability — счётчики надёжности физических дисков, отдельным списком.
	// Сопоставляются с дисками по DeviceId в assemble.go, а не здесь: этот
	// файл описывает ответы системы, а связывать их — логика, и её место там,
	// где она проверяется тестами.
	Reliability []RawReliability

	NICs     []RawNIC
	Monitors []RawMonitor
	Software []RawProgram

	// Failures — что собрать не удалось. Пустой раздел без записи о неудаче
	// неотличим от машины, у которой этого действительно нет, и служба
	// честно запишет «диски исчезли».
	Failures []contract.Failure
}

// Fail добавляет запись о неудавшемся разделе.
func (r *Raw) Fail(section string, err error) {
	if err == nil {
		return
	}
	r.Failures = append(r.Failures, contract.Failure{Section: section, Reason: err.Error()})
}

// RawSystem — Win32_ComputerSystem.
type RawSystem struct {
	Name                string
	Domain              string
	Workgroup           string
	PartOfDomain        bool
	Manufacturer        string
	Model               string
	TotalPhysicalMemory uint64
}

// RawBIOS — Win32_BIOS.
type RawBIOS struct {
	SerialNumber string
}

// RawProduct — Win32_ComputerSystemProduct: отсюда берётся UUID из SMBIOS.
type RawProduct struct {
	UUID string
}

// RawBoard — Win32_BaseBoard.
type RawBoard struct {
	Manufacturer string
	Product      string
}

// RawChassis — Win32_SystemEnclosure.
type RawChassis struct {
	ChassisTypes []int32
}

// RawOS — Win32_OperatingSystem.
type RawOS struct {
	Caption        string
	Version        string
	BuildNumber    string
	OSArchitecture string
	InstallDate    string
	LastBootUpTime string
}

// RawProcessor — Win32_Processor.
type RawProcessor struct {
	Name                      string
	NumberOfCores             uint32
	NumberOfLogicalProcessors uint32
	MaxClockSpeed             uint32
}

// RawMemoryModule — Win32_PhysicalMemory.
type RawMemoryModule struct {
	Capacity      uint64
	Speed         uint32
	DeviceLocator string
	Manufacturer  string
	SerialNumber  string

	// SMBIOSMemoryType появился в Windows 10; на более старых заполнен
	// только MemoryType, и коды в них разные.
	SMBIOSMemoryType uint16
	MemoryType       uint16
}

// RawDiskDrive — Win32_DiskDrive. Есть на всех версиях Windows, включая 7.
type RawDiskDrive struct {
	Index         uint32
	Model         string
	SerialNumber  string
	InterfaceType string
	MediaType     string
	Size          uint64

	// Status — тот самый «OK» или «Pred Fail», ради которого раздел о
	// здоровье диска вообще нужен.
	Status string
}

// RawPhysicalDisk — MSFT_PhysicalDisk из пространства Storage.
//
// Появился в Windows 8: только он честно отвечает, SSD перед нами или HDD.
// На Windows 7 его нет, и вид накопителя приходится угадывать по модели.
type RawPhysicalDisk struct {
	DeviceId     string
	ObjectId     string
	FriendlyName string
	SerialNumber string
	MediaType    uint16
	BusType      uint16
	Size         uint64
	HealthStatus uint16
}

// RawReliability — MSFT_StorageReliabilityCounter: наработка диска.
//
// Класс доступен только администратору. Служба агента работает от SYSTEM и
// получает его всегда; разовый запуск обычным пользователем — нет, и тогда
// причина уходит в failures, а наработка в снимке остаётся пустой: «не
// знаем», а не «ноль часов».
type RawReliability struct {
	DeviceId     string
	PowerOnHours uint32

	// DiskObjectId — ObjectId физического диска, у которого счётчик
	// спрашивали. Не поле WMI: проставляется при опросе, и по нему счётчик
	// сопоставляется с диском без догадок о формате номеров устройств.
	DiskObjectId string
}

// RawNIC — сетевой интерфейс, уже сведённый из нескольких источников.
type RawNIC struct {
	Name       string
	MAC        string
	IPv4       []string
	IPv6       []string
	Gateway    string
	DNS        []string
	DHCP       bool
	SpeedMbits int

	// Physical — известно ли достоверно, что адаптер настоящий. Если нет,
	// виртуальность определяется по имени.
	Physical      bool
	PhysicalKnown bool
}

// RawMonitor — монитор, разобранный из EDID.
type RawMonitor struct {
	Manufacturer string
	Model        string
	Serial       string
	Year         int
	DiagonalInch int
}

// RawProgram — запись из списка установленных программ.
type RawProgram struct {
	Name        string
	Version     string
	Publisher   string
	InstallDate string
}
