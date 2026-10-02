package contract

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func образецСнимка() Snapshot {
	return Snapshot{
		SchemaVersion: SchemaVersion,
		AgentID:       "6f1e0a2c-7b4d-4a9e-9f31-0c2d5e8a1b77",
		AgentVersion:  "0.1.0",
		CollectedAt:   time.Date(2026, 9, 2, 8, 30, 0, 0, time.UTC),
		Origin:        OriginNetwork,
		Machine: Machine{
			Hostname:     "PC-214-01",
			Domain:       "zavod.local",
			Serial:       "7XKPL03",
			Manufacturer: "Dell Inc.",
			Model:        "OptiPlex 3080",
			Motherboard:  "Dell 0K240Y",
			Chassis:      "desktop",
		},
		OS:  OS{Name: "Windows 10 Pro", Version: "10.0.19045", Architecture: "x64"},
		CPU: CPU{Model: "Intel Core i5-12400", Cores: 6, Threads: 12, MHz: 2500},
		Memory: Memory{
			TotalBytes: 17179869184,
			Modules: []MemoryModule{
				{SizeBytes: 8589934592, Type: "DDR4", MHz: 3200, Slot: "DIMM_A1"},
				{SizeBytes: 8589934592, Type: "DDR4", MHz: 3200, Slot: "DIMM_B1"},
			},
		},
		Disks: []Disk{
			{Model: "Samsung SSD 870 EVO", Kind: DiskSSD, SizeBytes: 512110190592, Interface: "SATA", Health: "OK"},
			{Model: "WDC WD10EZEX", Kind: DiskHDD, SizeBytes: 1000204886016, Interface: "SATA", PowerOnHours: 14322},
		},
		Interfaces: []Interface{
			{Name: "Ethernet", MAC: "a4:bb:6d:11:22:33", IPv4: []string{"10.0.14.87"}, Gateway: "10.0.14.1", DHCP: true, SpeedMbits: 1000},
			{Name: "VPN", MAC: "00:ff:aa:bb:cc:dd", IPv4: []string{"172.16.0.9"}, Virtual: true},
		},
	}
}

func TestСнимокПереживаетОборотЧерезJSON(t *testing.T) {
	original := образецСнимка()

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("не сериализуется: %v", err)
	}

	var back Snapshot
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("не разбирается: %v", err)
	}

	if back.Machine.Serial != original.Machine.Serial {
		t.Errorf("серийный номер потерян: %q", back.Machine.Serial)
	}
	if back.Memory.TotalBytes != original.Memory.TotalBytes {
		t.Errorf("объём памяти потерян: %d", back.Memory.TotalBytes)
	}
	if len(back.Disks) != 2 || back.Disks[0].Kind != DiskSSD {
		t.Errorf("диски разобраны неверно: %+v", back.Disks)
	}
	if !back.CollectedAt.Equal(original.CollectedAt) {
		t.Errorf("время сбора потеряно: %v", back.CollectedAt)
	}
}

func TestНезнакомыеПоляИгнорируютсяАНеЛомаютРазбор(t *testing.T) {
	// Инвариант I5 держится именно на этом: агент на пятистах машинах
	// обновляется неделями, и всё это время в ленте лежат сообщения обеих
	// версий. Читатель, падающий на новом поле, останавливает всю обработку.
	raw := []byte(`{
		"schema_version": 1,
		"agent_id": "abc",
		"origin": "network",
		"collected_at": "2026-09-02T08:30:00Z",
		"machine": {"hostname": "PC-1", "tpm_version": "2.0"},
		"battery": {"health": 87},
		"disks": []
	}`)

	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("разбор упал на незнакомых полях: %v", err)
	}
	if s.Machine.Hostname != "PC-1" {
		t.Errorf("знакомые поля должны разобраться: %q", s.Machine.Hostname)
	}
}

func TestСуммарныйОбъёмДисковБезВнешних(t *testing.T) {
	// Флешка, забытая в машине на момент сбора, не должна выглядеть как
	// выросшее хранилище и порождать событие об изменении.
	s := Snapshot{Disks: []Disk{
		{SizeBytes: 500, Interface: "SATA"},
		{SizeBytes: 1000, Interface: "NVMe"},
		{SizeBytes: 64, Interface: "USB"},
		{SizeBytes: 32, Interface: "usb"},
	}}

	if got := s.TotalDiskBytes(); got != 1500 {
		t.Errorf("ожидалось 1500, получено %d", got)
	}
}

func TestВиртуальныеИнтерфейсыНеУчаствуютВОпознании(t *testing.T) {
	// Адрес VPN-адаптера меняется от подключения к подключению; опознание по
	// нему давало бы новую машину при каждом входе в сеть из дома.
	s := образецСнимка()

	phys := s.PhysicalInterfaces()
	if len(phys) != 1 {
		t.Fatalf("ожидался один физический интерфейс, получено %d: %+v", len(phys), phys)
	}
	if phys[0].Name != "Ethernet" {
		t.Errorf("отобран не тот интерфейс: %s", phys[0].Name)
	}
}

func TestИнтерфейсБезАдресаНеСчитаетсяФизическим(t *testing.T) {
	s := Snapshot{Interfaces: []Interface{{Name: "Ethernet 2"}}}
	if len(s.PhysicalInterfaces()) != 0 {
		t.Error("интерфейс без MAC не годится для опознания")
	}
}

func TestПриведениеАдресаСетевойКарты(t *testing.T) {
	// WMI отдаёт "AA-BB-CC-DD-EE-FF", ip — "aa:bb:cc:dd:ee:ff". Без
	// приведения опознание сочло бы это двумя машинами.
	cases := map[string]string{
		"A4-BB-6D-11-22-33": "a4:bb:6d:11:22:33",
		"a4:bb:6d:11:22:33": "a4:bb:6d:11:22:33",
		"A4BB6D112233":      "a4:bb:6d:11:22:33",
		"a4.bb.6d.11.22.33": "a4:bb:6d:11:22:33",
		"":                  "",
	}

	for in, want := range cases {
		if got := NormalizeMAC(in); got != want {
			t.Errorf("NormalizeMAC(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestНепонятныйАдресНеПревращаетсяВПустоту(t *testing.T) {
	// Молча превращать непонятное в пустую строку — значит терять след при
	// разборе: в журнале будет «нет адреса» вместо настоящего значения.
	got := NormalizeMAC("не адрес вовсе")
	if got == "" {
		t.Error("непонятное значение не должно исчезать")
	}
	if !strings.Contains(got, "не адрес") {
		t.Errorf("исходное значение должно сохраниться, получено %q", got)
	}
}
