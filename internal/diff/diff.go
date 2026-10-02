// Package diff отвечает на вопрос «что именно изменилось между двумя снимками».
//
// Сравнивается НЕ всё подряд, а перечисленный здесь список полей. Это главное
// решение пакета: в снимке полно того, что меняется каждый раз само по себе —
// время последней загрузки, наработка диска в часах, свободное место. Сравнивай
// снимки целиком, и девятнадцать отчётов из двадцати давали бы «изменилось»,
// а настоящая замена диска утонула бы в этом потоке (инвариант I9).
//
// Список ниже — это ответ на вопрос «о чём стоит будить человека».
package diff

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/broton07/inventory_service/pkg/contract"
)

// MaxNamedSoftware — сколько установленных и удалённых программ называть
// поимённо.
//
// После обновления системы список программ меняется на десятки позиций;
// перечислять их все — значит сделать историю машины нечитаемой. Дальше идёт
// счёт.
const MaxNamedSoftware = 10

// Compare возвращает расхождения между снимками, от важного к мелкому.
//
// Пустой результат означает «ничего значимого не изменилось» — и это самый
// частый исход.
func Compare(was, is contract.Snapshot) []contract.Change {
	var out []contract.Change

	out = append(out, compareMachine(was, is)...)
	out = append(out, compareOS(was, is)...)
	out = append(out, compareCPU(was, is)...)
	out = append(out, compareMemory(was, is)...)
	out = append(out, compareDisks(was, is)...)
	out = append(out, compareInterfaces(was, is)...)
	out = append(out, compareSoftware(was, is)...)

	return out
}

// Fingerprint — отпечаток того, что сравнивается.
//
// Нужен для быстрого ответа «точно ничего не менялось»: сравнить два хеша
// дешевле, чем разбирать и сличать два снимка. Считается ровно по тем полям,
// которые сравнивает Compare, — иначе отпечаток менялся бы от времени
// загрузки, и быстрый путь не срабатывал бы никогда.
func Fingerprint(snap contract.Snapshot) []byte {
	payload, err := json.Marshal(comparable(snap))
	if err != nil {
		// Проекция состоит из строк, чисел и срезов — сериализация не может
		// не удаться. Если всё-таки не удалась, лучше отпечаток, который
		// никогда не совпадёт, чем ложное «не изменилось».
		return []byte(err.Error())
	}
	sum := sha256.Sum256(payload)
	return sum[:]
}

// comparable — проекция снимка на то, что вообще участвует в сравнении.
type comparableSnapshot struct {
	Machine    []string `json:"machine"`
	OS         []string `json:"os"`
	CPU        []string `json:"cpu"`
	Memory     []string `json:"memory"`
	Disks      []string `json:"disks"`
	Health     []string `json:"health"`
	Interfaces []string `json:"interfaces"`
	Software   []string `json:"software"`
}

func comparable(s contract.Snapshot) comparableSnapshot {
	return comparableSnapshot{
		Machine: []string{
			s.Machine.Hostname, s.Machine.Serial, s.Machine.UUID,
			s.Machine.Manufacturer, s.Machine.Model, s.Machine.Motherboard,
		},
		OS:     []string{s.OS.Name, s.OS.Version, s.OS.Build, s.OS.Architecture},
		CPU:    []string{s.CPU.Model, fmt.Sprint(s.CPU.Cores), fmt.Sprint(s.CPU.Threads)},
		Memory: append([]string{fmt.Sprint(s.Memory.TotalBytes)}, memoryKeys(s)...),
		Disks:  diskKeys(s),
		// Состояние по S.M.A.R.T. лежит отдельно от diskKeys намеренно: в
		// сравнении дисков оно не участвует (иначе «диск заменили» срабатывало
		// бы на ухудшении здоровья того же диска), но в отпечатке быть
		// обязано — иначе быстрый путь заглушит его изменение и человек
		// узнает о сыплющемся диске только когда тот умрёт.
		Health: healthKeys(s),
		// Только физические: адрес VPN-адаптера меняется от подключения к
		// подключению, и без этого отбора «изменилось» приходило бы на каждый
		// вход в сеть из дома.
		Interfaces: interfaceKeys(s),
		Software:   softwareKeys(s),
	}
}

// ---------------------------------------------------------------------------
// Машина, система, процессор
// ---------------------------------------------------------------------------

func compareMachine(was, is contract.Snapshot) []contract.Change {
	return compareFields([]field{
		{"machine.hostname", "Имя компьютера", was.Machine.Hostname, is.Machine.Hostname},
		{"machine.serial", "Заводской номер", was.Machine.Serial, is.Machine.Serial},
		{"machine.model", "Модель", was.Machine.Model, is.Machine.Model},
		{"machine.motherboard", "Материнская плата", was.Machine.Motherboard, is.Machine.Motherboard},
	})
}

func compareOS(was, is contract.Snapshot) []contract.Change {
	return compareFields([]field{
		{"os.name", "Операционная система", was.OS.Name, is.OS.Name},
		{"os.version", "Версия системы", was.OS.Version, is.OS.Version},
		{"os.build", "Сборка системы", was.OS.Build, is.OS.Build},
	})
}

func compareCPU(was, is contract.Snapshot) []contract.Change {
	return compareFields([]field{
		{"cpu.model", "Процессор", was.CPU.Model, is.CPU.Model},
		{"cpu.cores", "Ядер", число(was.CPU.Cores), число(is.CPU.Cores)},
	})
}

// ---------------------------------------------------------------------------
// Память
// ---------------------------------------------------------------------------

func compareMemory(was, is contract.Snapshot) []contract.Change {
	var out []contract.Change

	if was.Memory.TotalBytes != is.Memory.TotalBytes {
		out = append(out, contract.Change{
			Field: "memory.total_bytes",
			Old:   Bytes(was.Memory.TotalBytes),
			New:   Bytes(is.Memory.TotalBytes),
		})
	}

	// Планки по отдельности: суммарный объём может совпасть, а две планки по
	// 4 ГБ вместо одной на 8 — это другая машина для того, кто планирует
	// расширение.
	if added, removed := setDiff(memoryKeys(was), memoryKeys(is)); len(added)+len(removed) > 0 {
		out = append(out, contract.Change{
			Field: "memory.modules",
			Old:   строкой(memoryKeys(was)),
			New:   строкой(memoryKeys(is)),
		})
	}

	return out
}

func memoryKeys(s contract.Snapshot) []string {
	out := make([]string, 0, len(s.Memory.Modules))
	for _, m := range s.Memory.Modules {
		out = append(out, fmt.Sprintf("%s %s", Bytes(m.SizeBytes), m.Type))
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Диски
// ---------------------------------------------------------------------------

func compareDisks(was, is contract.Snapshot) []contract.Change {
	var out []contract.Change

	if added, removed := setDiff(diskKeys(was), diskKeys(is)); len(added)+len(removed) > 0 {
		out = append(out, contract.Change{
			Field: "disks",
			Old:   строкой(diskKeys(was)),
			New:   строкой(diskKeys(is)),
		})
	}

	if was.TotalDiskBytes() != is.TotalDiskBytes() {
		out = append(out, contract.Change{
			Field: "disks.total_bytes",
			Old:   Bytes(was.TotalDiskBytes()),
			New:   Bytes(is.TotalDiskBytes()),
		})
	}

	// Состояние по S.M.A.R.T. — единственное, ради чего стоит будить человека
	// до того, как диск умер.
	for _, d := range is.Disks {
		prev, ok := найтиДиск(was.Disks, d)
		if !ok || prev.Health == d.Health || d.Health == "" {
			continue
		}
		out = append(out, contract.Change{
			Field: "disks.health",
			Old:   fmt.Sprintf("%s: %s", описаниеДиска(prev), пустоеЗначение(prev.Health)),
			New:   fmt.Sprintf("%s: %s", описаниеДиска(d), d.Health),
		})
	}

	return out
}

// diskKeys — диск без того, что меняется само.
//
// Наработка в часах намеренно не входит: она растёт при каждом отчёте, и диск
// «менялся» бы каждые четыре часа на всех машинах парка.
func diskKeys(s contract.Snapshot) []string {
	out := make([]string, 0, len(s.Disks))
	for _, d := range s.Disks {
		if strings.EqualFold(d.Interface, "USB") {
			// Флешка, забытая в машине на момент сбора, не должна выглядеть
			// как появившийся диск.
			continue
		}
		out = append(out, описаниеДиска(d))
	}
	sort.Strings(out)
	return out
}

// healthKeys — состояние накопителей, привязанное к самим накопителям.
func healthKeys(s contract.Snapshot) []string {
	var out []string
	for _, d := range s.Disks {
		if strings.EqualFold(d.Interface, "USB") || d.Health == "" {
			continue
		}
		out = append(out, описаниеДиска(d)+": "+d.Health)
	}
	sort.Strings(out)
	return out
}

func описаниеДиска(d contract.Disk) string {
	name := d.Model
	if name == "" {
		name = "накопитель"
	}
	kind := strings.ToUpper(string(d.Kind))
	if d.Kind == "" || d.Kind == contract.DiskUnknown {
		kind = ""
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s %s", kind, Bytes(d.SizeBytes), name))
}

func найтиДиск(disks []contract.Disk, want contract.Disk) (contract.Disk, bool) {
	for _, d := range disks {
		if d.Serial != "" && d.Serial == want.Serial {
			return d, true
		}
		if d.Serial == "" && d.Model == want.Model && d.SizeBytes == want.SizeBytes {
			return d, true
		}
	}
	return contract.Disk{}, false
}

// ---------------------------------------------------------------------------
// Сеть
// ---------------------------------------------------------------------------

func compareInterfaces(was, is contract.Snapshot) []contract.Change {
	var out []contract.Change

	if added, removed := setDiff(macKeys(was), macKeys(is)); len(added)+len(removed) > 0 {
		out = append(out, contract.Change{
			Field: "interfaces",
			Old:   строкой(macKeys(was)),
			New:   строкой(macKeys(is)),
		})
	}

	if былIP, сталIP := adresa(was), adresa(is); былIP != сталIP {
		out = append(out, contract.Change{
			Field: "interfaces.ipv4",
			Old:   пустоеЗначение(былIP),
			New:   пустоеЗначение(сталIP),
		})
	}

	return out
}

func interfaceKeys(s contract.Snapshot) []string {
	keys := macKeys(s)
	if ip := adresa(s); ip != "" {
		keys = append(keys, ip)
	}
	return keys
}

func macKeys(s contract.Snapshot) []string {
	var out []string
	for _, i := range s.PhysicalInterfaces() {
		out = append(out, contract.NormalizeMAC(i.MAC))
	}
	sort.Strings(out)
	return out
}

func adresa(s contract.Snapshot) string {
	var out []string
	for _, i := range s.PhysicalInterfaces() {
		out = append(out, i.IPv4...)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// ---------------------------------------------------------------------------
// Программы
// ---------------------------------------------------------------------------

func compareSoftware(was, is contract.Snapshot) []contract.Change {
	// Отсутствие списка — это «не собрали», а не «программ нет». Считать
	// пропуск сбора удалением всех программ значило бы порождать событие о
	// сотне удалений каждый раз, когда у агента не хватило прав.
	if len(was.Software) == 0 || len(is.Software) == 0 {
		return nil
	}

	added, removed := setDiff(softwareKeys(was), softwareKeys(is))
	if len(added)+len(removed) == 0 {
		return nil
	}

	var out []contract.Change
	if len(added) > 0 {
		out = append(out, contract.Change{
			Field: "software.installed",
			Old:   "",
			New:   список(added),
		})
	}
	if len(removed) > 0 {
		out = append(out, contract.Change{
			Field: "software.removed",
			Old:   список(removed),
			New:   "",
		})
	}
	return out
}

func softwareKeys(s contract.Snapshot) []string {
	out := make([]string, 0, len(s.Software))
	for _, p := range s.Software {
		out = append(out, strings.TrimSpace(p.Name+" "+p.Version))
	}
	sort.Strings(out)
	return out
}

// список перечисляет первые MaxNamedSoftware позиций, остальное — счётом.
func список(items []string) string {
	if len(items) <= MaxNamedSoftware {
		return strings.Join(items, "; ")
	}
	return fmt.Sprintf("%s и ещё %d",
		strings.Join(items[:MaxNamedSoftware], "; "), len(items)-MaxNamedSoftware)
}

// ---------------------------------------------------------------------------
// Мелкая механика
// ---------------------------------------------------------------------------

type field struct {
	path, _name string
	was, is     string
}

func compareFields(fields []field) []contract.Change {
	var out []contract.Change
	for _, f := range fields {
		if f.was == f.is {
			continue
		}
		// Появление значения там, где раньше было пусто, — это не изменение
		// железа, а дособранные сведения: агент обновился и научился читать
		// поле. Будить этим человека незачем.
		if f.was == "" {
			continue
		}
		out = append(out, contract.Change{
			Field: f.path,
			Old:   f.was,
			New:   пустоеЗначение(f.is),
		})
	}
	return out
}

func setDiff(was, is []string) (added, removed []string) {
	былоМного := map[string]int{}
	for _, v := range was {
		былоМного[v]++
	}
	for _, v := range is {
		if былоМного[v] > 0 {
			былоМного[v]--
			continue
		}
		added = append(added, v)
	}
	for v, n := range былоМного {
		for range n {
			removed = append(removed, v)
		}
	}
	sort.Strings(removed)
	return added, removed
}

// Bytes переводит байты в то, что человек прочитает в карточке.
func Bytes(n int64) string {
	const (
		кб = int64(1) << 10
		мб = int64(1) << 20
		гб = int64(1) << 30
		тб = int64(1) << 40
	)

	switch {
	case n == 0:
		return "0"
	case n >= тб:
		return дробью(float64(n)/float64(тб), "ТБ")
	case n >= гб:
		return дробью(float64(n)/float64(гб), "ГБ")
	case n >= мб:
		return дробью(float64(n)/float64(мб), "МБ")
	case n >= кб:
		return дробью(float64(n)/float64(кб), "КБ")
	default:
		return fmt.Sprintf("%d Б", n)
	}
}

func дробью(v float64, unit string) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d %s", int64(v), unit)
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}

func число(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

func строкой(items []string) string {
	if len(items) == 0 {
		return "—"
	}
	return strings.Join(items, "; ")
}

func пустоеЗначение(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
