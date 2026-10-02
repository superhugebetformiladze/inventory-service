package diff

import (
	"strings"
	"testing"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

const гб = int64(1) << 30

func снимок() contract.Snapshot {
	return contract.Snapshot{
		SchemaVersion: contract.SchemaVersion,
		AgentID:       "метка-1",
		AgentVersion:  "0.1.0",
		CollectedAt:   time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
		Origin:        contract.OriginNetwork,
		Machine: contract.Machine{
			Hostname: "PC-214-01", Serial: "7XKPL03",
			Model: "OptiPlex 3080", Motherboard: "Dell 0K240Y",
		},
		OS:  contract.OS{Name: "Windows 10 Pro", Version: "10.0.19045", Build: "19045.4291"},
		CPU: contract.CPU{Model: "Intel Core i5-12400", Cores: 6, Threads: 12},
		Memory: contract.Memory{
			TotalBytes: 8 * гб,
			Modules:    []contract.MemoryModule{{SizeBytes: 8 * гб, Type: "DDR4"}},
		},
		Disks: []contract.Disk{{
			Model: "Samsung SSD 870 EVO", Serial: "S5Y2NJ0T",
			Kind: contract.DiskSSD, SizeBytes: 240 * гб, Interface: "SATA", Health: "OK",
		}},
		Interfaces: []contract.Interface{{
			Name: "Ethernet", MAC: "a4:bb:6d:11:22:33", IPv4: []string{"10.0.14.87"},
		}},
	}
}

func поле(changes []contract.Change, path string) (contract.Change, bool) {
	for _, c := range changes {
		if c.Field == path {
			return c, true
		}
	}
	return contract.Change{}, false
}

// ---------------------------------------------------------------------------
// Тишина там, где ничего не произошло
// ---------------------------------------------------------------------------

func TestОдинаковыеСнимкиНеДаютНиОдногоРасхождения(t *testing.T) {
	if got := Compare(снимок(), снимок()); len(got) != 0 {
		t.Fatalf("ожидалась тишина, получено %d расхождений: %+v", len(got), got)
	}
}

func TestВремяСбораИВерсияАгентаНеСчитаютсяИзменением(t *testing.T) {
	// Иначе каждый отчёт был бы изменением, и настоящая замена диска утонула
	// бы в этом потоке.
	было := снимок()
	стало := снимок()
	стало.CollectedAt = было.CollectedAt.Add(4 * time.Hour)
	стало.AgentVersion = "0.2.0"
	стало.OS.LastBootAt = &стало.CollectedAt

	if got := Compare(было, стало); len(got) != 0 {
		t.Fatalf("ожидалась тишина, получено: %+v", got)
	}
}

func TestНаработкаДискаНеСчитаетсяИзменением(t *testing.T) {
	// Она растёт при каждом отчёте: диск «менялся» бы каждые четыре часа на
	// всех машинах парка.
	было := снимок()
	стало := снимок()
	стало.Disks[0].PowerOnHours = было.Disks[0].PowerOnHours + 4

	if got := Compare(было, стало); len(got) != 0 {
		t.Fatalf("ожидалась тишина, получено: %+v", got)
	}
}

func TestЗабытаяФлешкаНеВыглядитПоявившимсяДиском(t *testing.T) {
	было := снимок()
	стало := снимок()
	стало.Disks = append(стало.Disks, contract.Disk{
		Model: "Kingston DataTraveler", SizeBytes: 32 * гб, Interface: "USB",
	})

	if got := Compare(было, стало); len(got) != 0 {
		t.Fatalf("ожидалась тишина, получено: %+v", got)
	}
}

func TestАдресVPNНеСчитаетсяИзменениемСети(t *testing.T) {
	// Он меняется от подключения к подключению: без этого «изменилось»
	// приходило бы на каждый вход в сеть из дома.
	было := снимок()
	стало := снимок()
	стало.Interfaces = append(стало.Interfaces, contract.Interface{
		Name: "VPN", MAC: "00:ff:aa:bb:cc:dd", IPv4: []string{"172.16.0.9"}, Virtual: true,
	})

	if got := Compare(было, стало); len(got) != 0 {
		t.Fatalf("ожидалась тишина, получено: %+v", got)
	}
}

func TestДособранныеСведенияНеСчитаютсяИзменением(t *testing.T) {
	// Агент обновился и научился читать материнскую плату. Это не событие про
	// железо, а событие про агента.
	было := снимок()
	было.Machine.Motherboard = ""
	стало := снимок()

	if got := Compare(было, стало); len(got) != 0 {
		t.Fatalf("появление значения на пустом месте — не изменение, получено: %+v", got)
	}
}

func TestПропущенныйСборПрограммНеВыглядитУдалением(t *testing.T) {
	// У агента не хватило прав на реестр. Считать это удалением всех программ
	// значило бы породить событие о сотне удалений на ровном месте.
	было := снимок()
	было.Software = []contract.Program{{Name: "1С:Предприятие", Version: "8.3"}}
	стало := снимок()
	стало.Software = nil
	стало.Failures = []contract.Failure{{Section: "software", Reason: "доступ запрещён"}}

	if got := Compare(было, стало); len(got) != 0 {
		t.Fatalf("ожидалась тишина, получено: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Расхождения, ради которых всё затевалось
// ---------------------------------------------------------------------------

func TestДобавлениеПамяти(t *testing.T) {
	было := снимок()
	стало := снимок()
	стало.Memory.TotalBytes = 16 * гб
	стало.Memory.Modules = []contract.MemoryModule{
		{SizeBytes: 8 * гб, Type: "DDR4"},
		{SizeBytes: 8 * гб, Type: "DDR4"},
	}

	changes := Compare(было, стало)

	c, ok := поле(changes, "memory.total_bytes")
	if !ok {
		t.Fatalf("изменение объёма памяти не найдено: %+v", changes)
	}
	if c.Old != "8 ГБ" || c.New != "16 ГБ" {
		t.Errorf("значения не для человека: было %q, стало %q", c.Old, c.New)
	}
}

func TestЗаменаДиска(t *testing.T) {
	было := снимок()
	стало := снимок()
	стало.Disks = []contract.Disk{{
		Model: "WDC WD10EZEX", Serial: "WD-99", Kind: contract.DiskHDD,
		SizeBytes: 1000 * гб, Interface: "SATA",
	}}

	changes := Compare(было, стало)

	if _, ok := поле(changes, "disks"); !ok {
		t.Fatalf("замена диска не замечена: %+v", changes)
	}
	c, ok := поле(changes, "disks.total_bytes")
	if !ok {
		t.Fatalf("изменение объёма не замечено: %+v", changes)
	}
	if !strings.Contains(c.Old, "240") || !strings.Contains(c.New, "1000") {
		t.Errorf("объёмы прочитать нельзя: было %q, стало %q", c.Old, c.New)
	}
}

func TestУхудшениеСостоянияДиска(t *testing.T) {
	// Единственное, ради чего стоит будить человека до того, как диск умер.
	было := снимок()
	стало := снимок()
	стало.Disks[0].Health = "Pred Fail"

	c, ok := поле(Compare(было, стало), "disks.health")
	if !ok {
		t.Fatal("ухудшение состояния диска не замечено")
	}
	if !strings.Contains(c.New, "Pred Fail") {
		t.Errorf("в новом значении нет состояния: %q", c.New)
	}
	if !strings.Contains(c.New, "Samsung") {
		t.Errorf("непонятно, о каком диске речь: %q", c.New)
	}
}

func TestПереименованиеМашины(t *testing.T) {
	было := снимок()
	стало := снимок()
	стало.Machine.Hostname = "БУХГАЛТЕРИЯ-2"

	c, ok := поле(Compare(было, стало), "machine.hostname")
	if !ok {
		t.Fatal("переименование не замечено")
	}
	if c.Old != "PC-214-01" || c.New != "БУХГАЛТЕРИЯ-2" {
		t.Errorf("было %q, стало %q", c.Old, c.New)
	}
}

func TestСменаАдреса(t *testing.T) {
	было := снимок()
	стало := снимок()
	стало.Interfaces[0].IPv4 = []string{"10.0.14.99"}

	c, ok := поле(Compare(было, стало), "interfaces.ipv4")
	if !ok {
		t.Fatal("смена адреса не замечена")
	}
	if c.Old != "10.0.14.87" || c.New != "10.0.14.99" {
		t.Errorf("было %q, стало %q", c.Old, c.New)
	}
}

func TestОбновлениеСистемы(t *testing.T) {
	было := снимок()
	стало := снимок()
	стало.OS.Build = "19045.5011"

	if _, ok := поле(Compare(было, стало), "os.build"); !ok {
		t.Fatal("обновление сборки системы не замечено")
	}
}

func TestУстановкаИУдалениеПрограмм(t *testing.T) {
	было := снимок()
	было.Software = []contract.Program{
		{Name: "1С:Предприятие", Version: "8.3"},
		{Name: "Старая программа", Version: "1.0"},
	}
	стало := снимок()
	стало.Software = []contract.Program{
		{Name: "1С:Предприятие", Version: "8.3"},
		{Name: "Новая программа", Version: "2.0"},
	}

	changes := Compare(было, стало)

	установлено, ok := поле(changes, "software.installed")
	if !ok {
		t.Fatalf("установка не замечена: %+v", changes)
	}
	if !strings.Contains(установлено.New, "Новая программа") {
		t.Errorf("не названа установленная программа: %q", установлено.New)
	}
	if _, ok := поле(changes, "software.removed"); !ok {
		t.Error("удаление не замечено")
	}
}

func TestДлинныйСписокПрограммСворачиваетсяВСчёт(t *testing.T) {
	// После обновления системы список меняется на десятки позиций;
	// перечислять их все — значит сделать историю машины нечитаемой.
	было := снимок()
	было.Software = []contract.Program{{Name: "Базовая", Version: "1.0"}}

	стало := снимок()
	стало.Software = []contract.Program{{Name: "Базовая", Version: "1.0"}}
	for i := range 30 {
		стало.Software = append(стало.Software, contract.Program{
			Name: "Программа", Version: string(rune('a' + i)),
		})
	}

	c, ok := поле(Compare(было, стало), "software.installed")
	if !ok {
		t.Fatal("установка не замечена")
	}
	if !strings.Contains(c.New, "и ещё") {
		t.Errorf("длинный список не свёрнут: %q", c.New)
	}
}

// ---------------------------------------------------------------------------
// Отпечаток
// ---------------------------------------------------------------------------

func TestОтпечатокОдинаковыхСнимковСовпадает(t *testing.T) {
	// Два отдельно собранных снимка одной машины должны дать один отпечаток:
	// на этом держится весь быстрый путь «ничего не менялось».
	первый := снимок()
	второй := снимок()

	if string(Fingerprint(первый)) != string(Fingerprint(второй)) {
		t.Fatal("отпечаток не воспроизводится")
	}
}

func TestОтпечатокНеЗависитОтВремениСбора(t *testing.T) {
	// Иначе быстрый путь «точно ничего не менялось» не срабатывал бы никогда,
	// и каждый отчёт стоил бы полного сличения снимков.
	было := снимок()
	стало := снимок()
	стало.CollectedAt = было.CollectedAt.Add(4 * time.Hour)
	стало.Disks[0].PowerOnHours += 4

	if string(Fingerprint(было)) != string(Fingerprint(стало)) {
		t.Fatal("отпечаток изменился от того, что меняется само по себе")
	}
}

func TestОтпечатокЗамечаетУхудшениеДиска(t *testing.T) {
	// Найдено живой проверкой на пятом этапе: состояние по S.M.A.R.T. не
	// входило в отпечаток, быстрый путь отвечал «ничего не менялось», и
	// сличение снимков до состояния диска не доходило вовсе. Человек узнал бы
	// о сыплющемся диске, только когда тот умер.
	//
	// Правило, которое отсюда следует: отпечаток обязан покрывать ВСЁ, что
	// смотрит Compare. Иначе быстрый путь глушит расхождение молча.
	было := снимок()
	стало := снимок()
	стало.Disks[0].Health = "Pred Fail"

	if string(Fingerprint(было)) == string(Fingerprint(стало)) {
		t.Fatal("отпечаток не заметил ухудшения состояния диска")
	}
	if _, ok := поле(Compare(было, стало), "disks.health"); !ok {
		t.Fatal("сличение не заметило ухудшения состояния диска")
	}
}

func TestОтпечатокМеняетсяВместеСЖелезом(t *testing.T) {
	было := снимок()
	стало := снимок()
	стало.Memory.TotalBytes = 16 * гб

	if string(Fingerprint(было)) == string(Fingerprint(стало)) {
		t.Fatal("отпечаток не заметил добавления памяти")
	}
}

func TestОтпечатокНеЗависитОтПорядкаДисков(t *testing.T) {
	// WMI отдаёт устройства в произвольном порядке; без сортировки машина
	// «менялась» бы через раз.
	было := снимок()
	было.Disks = append(было.Disks, contract.Disk{
		Model: "WDC WD10EZEX", Kind: contract.DiskHDD, SizeBytes: 1000 * гб, Interface: "SATA",
	})

	стало := снимок()
	стало.Disks = []contract.Disk{
		{Model: "WDC WD10EZEX", Kind: contract.DiskHDD, SizeBytes: 1000 * гб, Interface: "SATA"},
		было.Disks[0],
	}

	if string(Fingerprint(было)) != string(Fingerprint(стало)) {
		t.Fatal("отпечаток зависит от порядка дисков в снимке")
	}
}

func TestОбъёмыЧитаемыЧеловеком(t *testing.T) {
	cases := map[int64]string{
		0:              "0",
		512:            "512 Б",
		8 * гб:         "8 ГБ",
		240 * гб:       "240 ГБ",
		1000 * гб:      "1000 ГБ",
		int64(1) << 40: "1 ТБ",
		(3 * гб) / 2:   "1.5 ГБ",
	}
	for n, want := range cases {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, ожидалось %q", n, got, want)
		}
	}
}
