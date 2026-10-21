package contract

import (
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// NetworkDevice — снимок управляемого сетевого устройства, собранный по SNMP.
//
// Только факты, которые устройство сообщает о себе: порты, их состояние,
// какие MAC-адреса оно видит за каждым портом, кто сосед по LLDP, ARP-таблица.
// Выводов здесь нет — «магистральный порт», «за портом неуправляемый
// коммутатор», «на порту принтер» делает потребитель (инвариант I1): служба не
// знает ни принтеров, ни карточек, ни того, что потребителю считать важным.
type NetworkDevice struct {
	SchemaVersion int `json:"schema_version"`

	// DeviceID — устойчивый идентификатор устройства: MAC-адрес шасси.
	// Адрес управления меняют при переделке сети, MAC шасси — нет. Если
	// устройство не отдало ни один из адресов шасси, идентификатор —
	// "ip:<адрес управления>", и это видно в контракте, а не спрятано.
	DeviceID string `json:"device_id"`

	// ManagementIP — адрес, по которому устройство опрошено.
	ManagementIP string `json:"management_ip"`

	ChassisMAC  string `json:"chassis_mac,omitempty"`
	SysName     string `json:"sys_name,omitempty"`
	SysDescr    string `json:"sys_descr,omitempty"`
	SysObjectID string `json:"sys_object_id,omitempty"`

	CollectedAt time.Time `json:"collected_at"`

	// Ports — физические порты по возрастанию номера.
	Ports []NetworkPort `json:"ports"`

	// ARP — пары IP ↔ MAC из ARP-таблицы. Непуста у устройства, которое
	// маршрутизирует (ядро сети): только оно знает, какой IP у какого MAC,
	// и только по ней находятся устройства, о которых известен один IP.
	ARP []ARPEntry `json:"arp,omitempty"`
}

// NetworkPort — один физический порт.
type NetworkPort struct {
	// IfIndex — индекс интерфейса в IF-MIB.
	IfIndex int `json:"if_index"`

	// Number — номер порта моста (BRIDGE-MIB), он же номер на лицевой
	// панели у коммутаторов доступа. 0 — интерфейс не входит в мост.
	Number int `json:"number"`

	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// Alias — подпись, которую порту дал администратор («к 412 кабинету»).
	Alias string `json:"alias,omitempty"`

	AdminUp   bool `json:"admin_up"`
	OperUp    bool `json:"oper_up"`
	SpeedMbps int  `json:"speed_mbps,omitempty"`

	// PVID — VLAN, в который попадает нетегированный трафик порта.
	PVID int `json:"pvid,omitempty"`

	// MACs — адреса, выученные коммутатором за этим портом, по возрастанию.
	// Свои адреса устройства (status self) сюда не входят.
	MACs []string `json:"macs,omitempty"`

	// Neighbor — сосед по LLDP, если он себя объявил.
	Neighbor *LLDPNeighbor `json:"neighbor,omitempty"`
}

// LLDPNeighbor — устройство на том конце кабеля, как оно себя назвало.
type LLDPNeighbor struct {
	// ChassisID — идентификатор шасси соседа. Если сосед объявил его
	// MAC-адресом — нормализованный MAC, иначе строка как есть.
	ChassisID string `json:"chassis_id"`

	// PortID — порт соседа, как он его назвал: номер, имя или MAC.
	PortID          string `json:"port_id,omitempty"`
	PortDescription string `json:"port_description,omitempty"`
	SysName         string `json:"sys_name,omitempty"`
}

// ARPEntry — одна строка ARP-таблицы.
type ARPEntry struct {
	IP  string `json:"ip"`
	MAC string `json:"mac"`
}

// NetworkDeviceState — сообщение в уплотняемом топике inventory.network.v1.
//
// Ключ сообщения — DeviceID. Как и состояние машины, это последняя известная
// картина устройства: потребитель, поднятый с нуля, читает топик целиком и
// получает всю сеть, не спрашивая службу ни о чём.
type NetworkDeviceState struct {
	SchemaVersion int    `json:"schema_version"`
	DeviceID      string `json:"device_id"`

	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastPolledAt time.Time `json:"last_polled_at"`

	// SilentSince — с какого момента устройство не отвечает на опрос. Пусто —
	// отвечает. Публикуется, когда молчание длится дольше порога, и снимается
	// новым сообщением, когда устройство снова ответило: иначе неизменная
	// сеть и мёртвый коммутатор в ленте выглядели бы одинаково.
	SilentSince *time.Time `json:"silent_since,omitempty"`

	Device NetworkDevice `json:"device"`
}

// Validate проверяет снимок устройства до записи.
//
// Строго по тем же причинам, что и снимок машины: снимок может прийти не
// только от опроса, но и из файла, снятого на чужом компьютере, — и мусор в
// нём не должен дойти до ленты.
func (d NetworkDevice) Validate(now time.Time) error {
	var errs []error

	if d.SchemaVersion != SchemaVersion {
		errs = append(errs, fmt.Errorf("schema_version %d, ожидается %d", d.SchemaVersion, SchemaVersion))
	}
	if d.DeviceID == "" {
		errs = append(errs, errors.New("device_id пуст"))
	}
	if _, err := netip.ParseAddr(d.ManagementIP); err != nil {
		errs = append(errs, fmt.Errorf("management_ip %q не адрес", d.ManagementIP))
	}
	if d.CollectedAt.IsZero() {
		errs = append(errs, errors.New("collected_at не задан"))
	} else if d.CollectedAt.After(now.Add(MaxClockSkew)) {
		errs = append(errs, fmt.Errorf("collected_at %s в будущем", d.CollectedAt.Format(time.RFC3339)))
	}

	seen := make(map[int]bool, len(d.Ports))
	for i, p := range d.Ports {
		if p.IfIndex <= 0 {
			errs = append(errs, fmt.Errorf("ports[%d]: if_index %d", i, p.IfIndex))
		}
		if seen[p.IfIndex] {
			errs = append(errs, fmt.Errorf("ports[%d]: if_index %d повторяется", i, p.IfIndex))
		}
		seen[p.IfIndex] = true
		for _, mac := range p.MACs {
			if !canonicalMAC(mac) {
				errs = append(errs, fmt.Errorf("ports[%d]: MAC %q не в каноническом виде", i, mac))
			}
		}
	}

	for i, a := range d.ARP {
		if _, err := netip.ParseAddr(a.IP); err != nil {
			errs = append(errs, fmt.Errorf("arp[%d]: %q не адрес", i, a.IP))
		}
		if !canonicalMAC(a.MAC) {
			errs = append(errs, fmt.Errorf("arp[%d]: MAC %q не в каноническом виде", i, a.MAC))
		}
	}

	return errors.Join(errs...)
}

// canonicalMAC — адрес уже в том виде, в каком его сравнивают: aa:bb:cc:dd:ee:ff.
func canonicalMAC(mac string) bool {
	return len(mac) == 17 && NormalizeMAC(mac) == mac
}
