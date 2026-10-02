//go:build windows

package collect

import "strings"

// wmiAdapter — Win32_NetworkAdapter: само устройство.
type wmiAdapter struct {
	Index uint32

	// NetConnectionID — то имя, которое человек видит в «Сетевых
	// подключениях»: «Ethernet», «Wi-Fi». Name — длинное название модели
	// адаптера, и в карточке оно бесполезно.
	NetConnectionID string
	Name            string
	MACAddress      string

	// PhysicalAdapter — настоящая карта или виртуальная. Ответ системы
	// важнее догадки по имени.
	PhysicalAdapter bool

	// Speed — скорость линка в БИТАХ в секунду.
	Speed uint64
}

// wmiAdapterConfig — Win32_NetworkAdapterConfiguration: его настройки.
type wmiAdapterConfig struct {
	Index                uint32
	IPAddress            []string
	DefaultIPGateway     []string
	DNSServerSearchOrder []string
	DHCPEnabled          bool
}

// networkAdapters сводит устройство и его настройки в один список.
//
// Двумя запросами, а не одним: WMI держит их в разных классах, связанных
// номером адаптера. В Win32_NetworkAdapter нет адресов, в
// Win32_NetworkAdapterConfiguration нет ни имени, ни признака виртуальности.
func networkAdapters() ([]RawNIC, error) {
	var устройства []wmiAdapter
	if err := клиентWMI.Query(
		`SELECT * FROM Win32_NetworkAdapter WHERE MACAddress IS NOT NULL`, &устройства); err != nil {
		return nil, err
	}

	var настройки []wmiAdapterConfig
	if err := клиентWMI.Query(
		`SELECT * FROM Win32_NetworkAdapterConfiguration`, &настройки); err != nil {
		return nil, err
	}

	поИндексу := make(map[uint32]wmiAdapterConfig, len(настройки))
	for _, c := range настройки {
		поИндексу[c.Index] = c
	}

	out := make([]RawNIC, 0, len(устройства))
	for _, a := range устройства {
		имя := strings.TrimSpace(a.NetConnectionID)
		if имя == "" {
			имя = strings.TrimSpace(a.Name)
		}

		nic := RawNIC{
			Name:          имя,
			MAC:           a.MACAddress,
			SpeedMbits:    int(a.Speed / 1_000_000),
			Physical:      a.PhysicalAdapter,
			PhysicalKnown: true,
		}

		c := поИндексу[a.Index]
		nic.DHCP = c.DHCPEnabled
		nic.DNS = c.DNSServerSearchOrder
		if len(c.DefaultIPGateway) > 0 {
			nic.Gateway = c.DefaultIPGateway[0]
		}

		// WMI отдаёт адреса одним списком вперемешку. Разделяет их наличие
		// двоеточия: в IPv4 его не бывает.
		for _, ip := range c.IPAddress {
			if strings.Contains(ip, ":") {
				nic.IPv6 = append(nic.IPv6, ip)
			} else {
				nic.IPv4 = append(nic.IPv4, ip)
			}
		}

		out = append(out, nic)
	}

	return out, nil
}
