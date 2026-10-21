package netscan

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

// ErrUnreachable — устройство не ответило даже на системную группу.
var ErrUnreachable = errors.New("устройство не отвечает")

// Типы интерфейсов IF-MIB, которые считаются физическими портами, даже если
// не входят в мост: ethernetCsmacd, fastEther, fastEtherFX, gigabitEthernet.
var physicalTypes = map[int64]bool{6: true, 62: true, 69: true, 117: true}

// Состояния записи FDB: learned(3) и mgmt(5) — адрес за портом; self(4) —
// собственный адрес коммутатора, invalid(2) — запись уже не действует.
var fdbKept = map[int64]bool{3: true, 5: true}

// Подтипы LLDP: chassis/port id как macAddress.
const (
	lldpChassisMAC = 4
	lldpPortMAC    = 3
)

// collector копит то, что пришло из разных обходов, и в конце собирает снимок.
type collector struct {
	sysName, sysDescr, sysObjectID string

	ifType  map[int]int64
	ifDescr map[int]string
	ifName  map[int]string
	ifAlias map[int]string
	ifAdmin map[int]int64
	ifOper  map[int]int64
	ifSpeed map[int]int64

	bridgeAddr   []byte
	bridgeToIf   map[int]int
	pvid         map[int]int
	fdb          map[int]map[string]bool // номер порта моста → множество MAC
	fdbStatus    map[string]int64        // хвост индекса → статус
	fdbPortByKey map[string]int          // хвост индекса → порт
	fdbMACByKey  map[string]string       // хвост индекса → MAC

	lldpLocSubtype int64
	lldpLocID      []byte
	lldp           map[int]*contract.LLDPNeighbor // локальный номер порта → сосед
	lldpChassisSub map[string]int64
	lldpPortSub    map[string]int64

	arp     map[string]string // IP → MAC
	arpType map[string]int64
}

func newCollector() *collector {
	return &collector{
		ifType: map[int]int64{}, ifDescr: map[int]string{}, ifName: map[int]string{},
		ifAlias: map[int]string{}, ifAdmin: map[int]int64{}, ifOper: map[int]int64{},
		ifSpeed: map[int]int64{}, bridgeToIf: map[int]int{}, pvid: map[int]int{},
		fdb: map[int]map[string]bool{}, fdbStatus: map[string]int64{},
		fdbPortByKey: map[string]int{}, fdbMACByKey: map[string]string{},
		lldp: map[int]*contract.LLDPNeighbor{}, lldpChassisSub: map[string]int64{},
		lldpPortSub: map[string]int64{}, arp: map[string]string{}, arpType: map[string]int64{},
	}
}

// Collect опрашивает устройство и собирает его снимок.
//
// Обязательна только системная группа: не ответило на неё — устройство
// недоступно. Остальное — по возможности: у коммутатора без LLDP просто не
// будет соседей, у коммутатора доступа — ARP-таблицы. Ошибка посреди
// необязательного обхода возвращается: снимок без половины портов хуже, чем
// никакого, — потребитель решил бы, что устройства отключили.
func Collect(ctx context.Context, w Walker, managementIP string, now time.Time) (contract.NetworkDevice, error) {
	c := newCollector()

	if err := w.Walk(ctx, oidSystem, c.system); err != nil {
		return contract.NetworkDevice{}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	if c.sysName == "" && c.sysDescr == "" && c.sysObjectID == "" {
		return contract.NetworkDevice{}, fmt.Errorf("%w: системная группа пуста", ErrUnreachable)
	}

	walks := []struct {
		root string
		fn   func(Variable) error
	}{
		{oidIfType, c.intByIf(c.ifType)},
		{oidIfDescr, c.textByIf(c.ifDescr)},
		{oidIfAdmin, c.intByIf(c.ifAdmin)},
		{oidIfOper, c.intByIf(c.ifOper)},
		{oidIfName, c.textByIf(c.ifName)},
		{oidIfAlias, c.textByIf(c.ifAlias)},
		{oidIfSpeedH, c.intByIf(c.ifSpeed)},
		{oidBridgeAddress, c.bridgeAddress},
		{oidBasePortIfIdx, c.basePort},
		{oidDot1qFdbPort, c.fdbPort(oidDot1qFdbPort)},
		{oidDot1qFdbStatus, c.fdbStat(oidDot1qFdbStatus)},
		{oidDot1qPvid, c.pvidOf},
		{oidLldpLocChassisSubtype, c.lldpLocSub},
		{oidLldpLocChassisID, c.lldpLoc},
		{oidLldpRemTable, c.lldpRem},
		{oidArpPhys, c.arpPhys},
		{oidArpType, c.arpKind},
	}
	for _, step := range walks {
		if err := w.Walk(ctx, step.root, step.fn); err != nil {
			return contract.NetworkDevice{}, fmt.Errorf("обход %s: %w", step.root, err)
		}
	}

	// Q-BRIDGE (с VLAN) есть почти у всех управляемых. Если его нет — старая
	// таблица BRIDGE-MIB без VLAN.
	if len(c.fdbPortByKey) == 0 {
		for _, step := range []struct {
			root string
			fn   func(Variable) error
		}{
			{oidDot1dFdbPort, c.fdbPort(oidDot1dFdbPort)},
			{oidDot1dFdbStatus, c.fdbStat(oidDot1dFdbStatus)},
		} {
			if err := w.Walk(ctx, step.root, step.fn); err != nil {
				return contract.NetworkDevice{}, fmt.Errorf("обход %s: %w", step.root, err)
			}
		}
	}

	return c.build(managementIP, now), nil
}

// ------------------------------------------------------------------
// Обработчики обходов
// ------------------------------------------------------------------

func (c *collector) system(v Variable) error {
	switch v.OID {
	case oidSystem + ".1.0":
		c.sysDescr = v.String()
	case oidSystem + ".2.0":
		c.sysObjectID = v.String()
	case oidSystem + ".5.0":
		c.sysName = v.String()
	}
	return nil
}

// ifIndexOf — индекс интерфейса из хвоста OID таблицы интерфейсов.
func ifIndexOf(v Variable, root string) (int, bool) {
	tail, ok := suffix(v.OID, root)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(tail)
	return n, err == nil && n > 0
}

func (c *collector) intByIf(target map[int]int64) func(Variable) error {
	return func(v Variable) error {
		root := v.OID[:strings.LastIndex(v.OID, ".")]
		if idx, ok := ifIndexOf(v, root); ok && v.Kind == KindInt {
			target[idx] = v.Int
		}
		return nil
	}
}

func (c *collector) textByIf(target map[int]string) func(Variable) error {
	return func(v Variable) error {
		root := v.OID[:strings.LastIndex(v.OID, ".")]
		if idx, ok := ifIndexOf(v, root); ok {
			target[idx] = v.String()
		}
		return nil
	}
}

func (c *collector) bridgeAddress(v Variable) error {
	if v.Kind == KindBytes && len(v.Bytes) == 6 {
		c.bridgeAddr = v.Bytes
	}
	return nil
}

func (c *collector) basePort(v Variable) error {
	tail, ok := suffix(v.OID, oidBasePortIfIdx)
	if !ok {
		return nil
	}
	port, err := strconv.Atoi(tail)
	if err == nil && port > 0 && v.Kind == KindInt && v.Int > 0 {
		c.bridgeToIf[port] = int(v.Int)
	}
	return nil
}

// fdbKey разбирает хвост индекса FDB. У Q-BRIDGE он «fdbId.m1…m6», у
// BRIDGE-MIB — «m1…m6». MAC — всегда последние шесть чисел.
func fdbMAC(tail string) (string, bool) {
	nums, ok := parseIndex(tail)
	if !ok || len(nums) < 6 {
		return "", false
	}
	b := make([]byte, 6)
	for i, n := range nums[len(nums)-6:] {
		if n > 255 {
			return "", false
		}
		b[i] = byte(n) //nolint:gosec // n проверено на 0..255 строкой выше
	}
	return macFromBytes(b), true
}

func (c *collector) fdbPort(root string) func(Variable) error {
	return func(v Variable) error {
		tail, ok := suffix(v.OID, root)
		if !ok || v.Kind != KindInt || v.Int <= 0 {
			return nil
		}
		mac, ok := fdbMAC(tail)
		if !ok {
			return nil
		}
		c.fdbPortByKey[tail] = int(v.Int)
		c.fdbMACByKey[tail] = mac
		return nil
	}
}

func (c *collector) fdbStat(root string) func(Variable) error {
	return func(v Variable) error {
		if tail, ok := suffix(v.OID, root); ok && v.Kind == KindInt {
			c.fdbStatus[tail] = v.Int
		}
		return nil
	}
}

func (c *collector) pvidOf(v Variable) error {
	tail, ok := suffix(v.OID, oidDot1qPvid)
	if !ok || v.Kind != KindInt {
		return nil
	}
	if port, err := strconv.Atoi(tail); err == nil && port > 0 {
		c.pvid[port] = int(v.Int)
	}
	return nil
}

func (c *collector) lldpLocSub(v Variable) error {
	if v.Kind == KindInt {
		c.lldpLocSubtype = v.Int
	}
	return nil
}

func (c *collector) lldpLoc(v Variable) error {
	if v.Kind == KindBytes {
		c.lldpLocID = v.Bytes
	}
	return nil
}

// lldpRem — таблица соседей: столбец.timeMark.localPortNum.remIndex.
func (c *collector) lldpRem(v Variable) error {
	tail, ok := suffix(v.OID, oidLldpRemTable)
	if !ok {
		return nil
	}
	nums, ok := parseIndex(tail)
	if !ok || len(nums) != 4 {
		return nil
	}
	column, localPort := nums[0], nums[2]
	key := strings.Join(strings.Split(tail, ".")[1:], ".")

	n := c.lldp[localPort]
	if n == nil {
		n = &contract.LLDPNeighbor{}
		c.lldp[localPort] = n
	}

	switch column {
	case 4:
		c.lldpChassisSub[key] = v.Int
	case 5:
		n.ChassisID = lldpID(v, c.lldpChassisSub[key] == lldpChassisMAC)
	case 6:
		c.lldpPortSub[key] = v.Int
	case 7:
		n.PortID = lldpID(v, c.lldpPortSub[key] == lldpPortMAC)
	case 8:
		n.PortDescription = v.String()
	case 9:
		n.SysName = v.String()
	}
	return nil
}

// lldpID — идентификатор из LLDP: MAC, если так объявлен, иначе текст.
//
// Подтип приходит раньше значения (столбец 4 раньше 5), поэтому известен к
// моменту разбора. Шестибайтовое значение без подтипа тоже считается MAC:
// печатать его текстом значит получить шесть случайных символов.
func lldpID(v Variable, isMAC bool) string {
	if v.Kind == KindBytes && (isMAC || (len(v.Bytes) == 6 && !printable(v.Bytes))) {
		return macFromBytes(v.Bytes)
	}
	return v.String()
}

func printable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// arpPhys — ipNetToMediaPhysAddress: ifIndex.a.b.c.d → MAC.
func (c *collector) arpPhys(v Variable) error {
	tail, ok := suffix(v.OID, oidArpPhys)
	if !ok || v.Kind != KindBytes || len(v.Bytes) != 6 {
		return nil
	}
	if ip, ok := arpIP(tail); ok {
		c.arp[ip] = macFromBytes(v.Bytes)
	}
	return nil
}

func (c *collector) arpKind(v Variable) error {
	tail, ok := suffix(v.OID, oidArpType)
	if !ok || v.Kind != KindInt {
		return nil
	}
	if ip, ok := arpIP(tail); ok {
		c.arpType[ip] = v.Int
	}
	return nil
}

func arpIP(tail string) (string, bool) {
	nums, ok := parseIndex(tail)
	if !ok || len(nums) != 5 {
		return "", false
	}
	b := [4]byte{}
	for i, n := range nums[1:] {
		if n > 255 {
			return "", false
		}
		b[i] = byte(n) //nolint:gosec // n проверено на 0..255 строкой выше
	}
	return netip.AddrFrom4(b).String(), true
}

func macFromBytes(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02x", x)
	}
	return contract.NormalizeMAC(strings.Join(parts, ":"))
}

// ------------------------------------------------------------------
// Сборка снимка
// ------------------------------------------------------------------

func (c *collector) build(managementIP string, now time.Time) contract.NetworkDevice {
	dev := contract.NetworkDevice{
		SchemaVersion: contract.SchemaVersion,
		ManagementIP:  managementIP,
		SysName:       c.sysName,
		SysDescr:      c.sysDescr,
		SysObjectID:   c.sysObjectID,
		CollectedAt:   now.UTC(),
	}

	switch {
	case len(c.bridgeAddr) == 6:
		dev.ChassisMAC = macFromBytes(c.bridgeAddr)
	case c.lldpLocSubtype == lldpChassisMAC && len(c.lldpLocID) == 6:
		dev.ChassisMAC = macFromBytes(c.lldpLocID)
	}
	dev.DeviceID = dev.ChassisMAC
	if dev.DeviceID == "" {
		dev.DeviceID = "ip:" + managementIP
	}

	// Обратная карта: интерфейс → номер порта моста.
	ifToBridge := make(map[int]int, len(c.bridgeToIf))
	for port, ifIndex := range c.bridgeToIf {
		ifToBridge[ifIndex] = port
	}

	// MAC-адреса по портам моста.
	for key, port := range c.fdbPortByKey {
		if status, known := c.fdbStatus[key]; known && !fdbKept[status] {
			continue
		}
		if c.fdb[port] == nil {
			c.fdb[port] = map[string]bool{}
		}
		c.fdb[port][c.fdbMACByKey[key]] = true
	}

	// Порты: всё, что входит в мост, и физические интерфейсы вне его.
	indexes := map[int]bool{}
	for ifIndex := range ifToBridge {
		indexes[ifIndex] = true
	}
	for ifIndex, t := range c.ifType {
		if physicalTypes[t] {
			indexes[ifIndex] = true
		}
	}

	for ifIndex := range indexes {
		number := ifToBridge[ifIndex]
		port := contract.NetworkPort{
			IfIndex:     ifIndex,
			Number:      number,
			Name:        c.ifName[ifIndex],
			Description: c.ifDescr[ifIndex],
			Alias:       c.ifAlias[ifIndex],
			AdminUp:     c.ifAdmin[ifIndex] == 1,
			OperUp:      c.ifOper[ifIndex] == 1,
			SpeedMbps:   int(c.ifSpeed[ifIndex]),
		}
		if number > 0 {
			port.PVID = c.pvid[number]
			port.MACs = sortedKeys(c.fdb[number])
		}

		// Локальный номер порта LLDP — номер порта моста, а если моста нет —
		// индекс интерфейса.
		if n, ok := c.lldp[number]; ok && number > 0 && n.ChassisID != "" {
			port.Neighbor = n
		} else if n, ok := c.lldp[ifIndex]; ok && number == 0 && n.ChassisID != "" {
			port.Neighbor = n
		}

		dev.Ports = append(dev.Ports, port)
	}

	sort.Slice(dev.Ports, func(i, j int) bool {
		a, b := dev.Ports[i], dev.Ports[j]
		if (a.Number == 0) != (b.Number == 0) {
			return a.Number != 0
		}
		if a.Number != b.Number {
			return a.Number < b.Number
		}
		return a.IfIndex < b.IfIndex
	})

	for ip, mac := range c.arp {
		// invalid(2) — запись погашена; other(1), dynamic(3), static(4) — живые.
		if c.arpType[ip] == 2 {
			continue
		}
		dev.ARP = append(dev.ARP, contract.ARPEntry{IP: ip, MAC: mac})
	}
	sort.Slice(dev.ARP, func(i, j int) bool {
		a, _ := netip.ParseAddr(dev.ARP[i].IP)
		b, _ := netip.ParseAddr(dev.ARP[j].IP)
		return a.Less(b)
	})

	return dev
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
