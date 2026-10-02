package collect

import (
	"strconv"
	"strings"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

// parseCIMDate разбирает время в формате CIM DATETIME.
//
// WMI отдаёт даты строкой вида 20230115103000.000000+180, где хвост — сдвиг
// от UTC в МИНУТАХ, а не в часах. Часовые пояса с получасовым сдвигом (Индия,
// Иран, Непал) при делении на 60 разъезжаются, поэтому разбор здесь свой, а
// не через time.Parse с готовым шаблоном.
func parseCIMDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 14 {
		return time.Time{}, false
	}

	основа, err := time.Parse("20060102150405", s[:14])
	if err != nil {
		return time.Time{}, false
	}

	// Сдвиг может отсутствовать или быть звёздочками — так WMI сообщает
	// «время без пояса». Считаем такое временем машины, то есть UTC: врать
	// на несколько часов хуже, чем не знать пояса.
	знак := strings.LastIndexAny(s, "+-")
	if знак < 14 || знак+1 >= len(s) {
		return основа.UTC(), true
	}

	минуты, err := strconv.Atoi(s[знак+1:])
	if err != nil {
		return основа.UTC(), true
	}
	if s[знак] == '-' {
		минуты = -минуты
	}

	return основа.Add(-time.Duration(минуты) * time.Minute).UTC(), true
}

// memoryTypeName переводит числовой код типа памяти в название.
//
// Кодов два набора: SMBIOSMemoryType (Windows 10 и новее) и MemoryType
// (старые системы), и в них одно и то же число означает разное. Поэтому
// таблицы раздельные, а не одна общая.
func memoryTypeName(smbios, legacy uint16) string {
	по := map[uint16]string{
		20: "DDR", 21: "DDR2", 22: "DDR2 FB-DIMM",
		24: "DDR3", 26: "DDR4", 34: "DDR5",
	}
	if name, ok := по[smbios]; ok {
		return name
	}

	старые := map[uint16]string{
		17: "SDRAM", 18: "SDRAM-II", 19: "DDR", 20: "DDR",
		21: "DDR2", 22: "DDR2 FB-DIMM", 24: "DDR3",
	}
	return старые[legacy]
}

// chassisKind переводит коды корпуса в слово, понятное человеку.
//
// От него зависит, ждать ли машину в сети постоянно: ноутбук, не выходивший
// на связь неделю, — это норма, а неделя молчания настольной машины —
// повод спросить.
func chassisKind(codes []int32) string {
	for _, c := range codes {
		switch c {
		case 8, 9, 10, 14: // Portable, Laptop, Notebook, Sub Notebook
			return "laptop"
		case 30, 31, 32: // Tablet, Convertible, Detachable
			return "tablet"
		case 23, 28: // Rack Mount, Blade
			return "server"
		case 3, 4, 5, 6, 7, 13, 15, 16, 17, 24: // настольные корпуса
			return "desktop"
		}
	}
	return ""
}

// Коды MSFT_PhysicalDisk. Именами, а не числами по месту: «MediaType == 4»
// в условии не читается.
const (
	mediaHDD  = 3
	mediaSSD  = 4
	mediaSCM  = 5
	healthOK  = 0
	healthWrn = 1
	healthBad = 2
)

// Виды шин из MSFT_PhysicalDisk.
//
// Нужны не для полноты: Win32_DiskDrive называет обычные диски SATA словом
// "IDE" — проверено на живой машине, где два диска SATA пришли с
// InterfaceType = IDE. В карточке «IDE» означает интерфейс, которого нет на
// материнских платах уже пятнадцать лет.
var busNames = map[uint16]string{
	3:  "ATA",
	7:  "USB",
	8:  "RAID",
	9:  "iSCSI",
	10: "SAS",
	11: "SATA",
	17: "NVMe",
}

const (
	busUSB  = 7
	busNVMe = 17
)

// diskKind определяет вид накопителя.
//
// Достоверный ответ даёт только MSFT_PhysicalDisk, которого нет на Windows 7.
// Там остаётся модель: производители пишут в неё «SSD» и «NVMe» достаточно
// последовательно, чтобы это работало, но угадывание помечено как угадывание —
// вызывающий обязан знать, что ответ ненадёжен.
func diskKind(media, bus uint16, known bool, model string) contract.DiskKind {
	if known {
		if bus == busNVMe {
			return contract.DiskNVMe
		}
		switch media {
		case mediaSSD, mediaSCM:
			return contract.DiskSSD
		case mediaHDD:
			return contract.DiskHDD
		}
	}

	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "nvme"):
		return contract.DiskNVMe
	case strings.Contains(lower, "ssd"), strings.Contains(lower, "solid state"):
		return contract.DiskSSD
	}
	return contract.DiskUnknown
}

// diskInterface — по чему подключён накопитель.
func diskInterface(bus uint16, known bool, wmi string) string {
	if known {
		if name, ok := busNames[bus]; ok {
			return name
		}
	}
	return strings.TrimSpace(wmi)
}

// diskHealth сводит состояние диска к одной строке.
//
// Пустая строка означает «не знаем», а не «здоров»: диск, чей S.M.A.R.T. не
// прочитался, не должен выглядеть исправным.
func diskHealth(status string, health uint16, known bool) string {
	if s := strings.TrimSpace(status); s != "" {
		return s
	}
	if !known {
		return ""
	}
	switch health {
	case healthOK:
		return "OK"
	case healthWrn:
		return "Warning"
	case healthBad:
		return "Unhealthy"
	}
	return ""
}

// virtualOUI — начала адресов, которые выдают себе гипервизоры.
//
// Имя адаптера виртуальной машины не выдаёт: на живой машине адаптер
// VirtualBox называется «Ethernet 2», и отличить его от настоящей карты можно
// только по адресу. А отличать надо: опознание по такому адресу привяжет
// машину к тому, что исчезнет вместе с удалённым VirtualBox.
var virtualOUI = []string{
	"00:05:69", "00:0c:29", "00:1c:14", "00:50:56", // VMware
	"00:15:5d",             // Hyper-V
	"08:00:27", "0a:00:27", // VirtualBox
	"52:54:00", // QEMU/KVM
	"00:16:3e", // Xen
	"02:42",    // Docker
}

// virtualByMAC — принадлежит ли адрес гипервизору.
func virtualByMAC(mac string) bool {
	mac = contract.NormalizeMAC(mac)
	for _, prefix := range virtualOUI {
		if strings.HasPrefix(mac, prefix) {
			return true
		}
	}
	return false
}

// virtualByName — виртуален ли интерфейс, если система не сказала прямо.
//
// Нужно там, где нет WMI: у виртуальных адаптеров адреса меняются от запуска
// к запуску, и опознание по ним давало бы новую машину при каждом включении
// VPN.
func virtualByName(name string) bool {
	lower := strings.ToLower(name)
	for _, признак := range []string{
		"loopback", "vethernet", "hyper-v", "virtualbox", "vmware", "vmnet",
		"vpn", "tap-", "tun", "wireguard", "openvpn", "teredo", "isatap",
		"bluetooth", "docker", "veth", "wsl",
	} {
		if strings.Contains(lower, признак) {
			return true
		}
	}
	return false
}

// installDate разбирает дату установки программы из реестра: 20230115.
func installDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if len(s) != 8 {
		return nil
	}
	t, err := time.Parse("20060102", s)
	if err != nil {
		return nil
	}
	return &t
}

// normalizeMonitorInstance приводит путь экземпляра монитора к общему виду.
//
// Один и тот же монитор WMI называет
// "DISPLAY\AOC2270\4&9c24ace&0&UID65793_0", а реестр —
// "AOC2270\4&9c24ace&0&UID65793". Приставка, хвост и регистр отличаются, и
// без приведения списки не сходятся ни в одной записи.
func normalizeMonitorInstance(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, `DISPLAY\`)

	// Хвост "_0" WMI добавляет к каждому экземпляру.
	if i := strings.LastIndexByte(s, '_'); i > 0 {
		if _, err := strconv.Atoi(s[i+1:]); err == nil {
			s = s[:i]
		}
	}
	return s
}
