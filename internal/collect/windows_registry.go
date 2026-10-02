//go:build windows

package collect

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Мониторы и установленные программы берутся из реестра, а не из WMI.
//
// Для программ это принципиально: класс Win32_Product, которым их обычно
// достают, при каждом обращении запускает проверку целостности КАЖДОГО
// установленного пакета MSI. На рабочей станции это минуты нагрузки и записи
// в журнал установки — цена, которой инвентаризация не стоит. Реестр отвечает
// мгновенно и содержит ровно то же.
//
// Для мониторов — потому что EDID лежит в реестре двоичным блоком с
// зафиксированной раскладкой, а WMI отдаёт те же сведения массивами
// шестнадцатибитных чисел, разбор которых зависит от версии библиотеки COM.

// путиУдаления — три места, где Windows держит список установленного.
//
// Их именно три: 64-разрядные программы, 32-разрядные на 64-разрядной
// системе и установленные для текущего пользователя.
type веткаУдаления struct {
	корень registry.Key
	путь   string
}

// Хвосты путей, одинаковые во всех ветках.
const (
	путь64 = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	путь32 = `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`
)

var путиУдаления = []веткаУдаления{
	{registry.LOCAL_MACHINE, путь64},
	{registry.LOCAL_MACHINE, путь32},
	// Ветка текущего пользователя нужна для запуска руками: администратор
	// проверяет машину из-под своей учётной записи.
	{registry.CURRENT_USER, путь64},
}

// веткиПользователей — списки установленного у каждого, кто сейчас в системе.
//
// Найдено на живой машине сразу после установки службой. Служба работает от
// имени системы, и «текущий пользователь» для неё — не человек за машиной: из
// отчёта пропали Chrome, VS Code, Postman и Python, то есть всё, что ставится
// «только для меня». А ставится так изрядная часть того, что в учёте и
// интересно.
//
// Читаются только загруженные ветки — пользователи, вошедшие в систему.
// Подгружать NTUSER.DAT тех, кто вышел, агент не будет: это правка чужого
// профиля ради инвентаризации.
func веткиПользователей() []веткаУдаления {
	корень, err := registry.OpenKey(registry.USERS, "", registry.READ)
	if err != nil {
		return nil
	}
	defer func() { _ = корень.Close() }()

	sids, err := корень.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}

	var out []веткаУдаления
	for _, sid := range sids {
		// S-1-5-21-… — настоящие учётные записи. Всё остальное — системные
		// (SYSTEM, LOCAL SERVICE) и вспомогательные ветки классов.
		if !strings.HasPrefix(sid, "S-1-5-21-") || strings.HasSuffix(sid, "_Classes") {
			continue
		}
		out = append(out,
			веткаУдаления{registry.USERS, sid + `\` + путь64},
			веткаУдаления{registry.USERS, sid + `\` + путь32},
		)
	}
	return out
}

// installedSoftware читает список установленных программ.
func installedSoftware() ([]RawProgram, error) {
	видели := map[string]bool{}
	var out []RawProgram
	var последняя error

	// Копия, а не сам срез: append к общему дописал бы ветки пользователей в
	// него самого, и со второго сбора список рос бы на каждом проходе.
	ветки := append(append([]веткаУдаления{}, путиУдаления...), веткиПользователей()...)

	for _, место := range ветки {
		ключ, err := registry.OpenKey(место.корень, место.путь, registry.READ)
		if err != nil {
			// Ветки WOW6432Node нет на 32-разрядной системе, ветки
			// пользователя — у службы. Это норма, а не сбой.
			continue
		}

		имена, err := ключ.ReadSubKeyNames(-1)
		if err != nil {
			последняя = err
			_ = ключ.Close()
			continue
		}

		for _, имя := range имена {
			p, ok := программа(место.корень, место.путь+`\`+имя)
			if !ok {
				continue
			}
			// Одна и та же программа попадается и в 64-, и в 32-разрядной
			// ветке. В отчёте она должна быть одна.
			ключСравнения := p.Name + "\x00" + p.Version
			if видели[ключСравнения] {
				continue
			}
			видели[ключСравнения] = true
			out = append(out, p)
		}

		_ = ключ.Close()
	}

	if len(out) == 0 && последняя != nil {
		return nil, последняя
	}
	return out, nil
}

// программа читает одну запись списка установленного.
func программа(корень registry.Key, путь string) (RawProgram, bool) {
	ключ, err := registry.OpenKey(корень, путь, registry.QUERY_VALUE)
	if err != nil {
		return RawProgram{}, false
	}
	defer func() { _ = ключ.Close() }()

	name, _, err := ключ.GetStringValue("DisplayName")
	if err != nil || strings.TrimSpace(name) == "" {
		// Записи без отображаемого имени — служебные: их не показывает и
		// сама панель управления.
		return RawProgram{}, false
	}

	// Обновления системы и компоненты платформы — это тысячи записей, из
	// которых учёту не нужна ни одна.
	if v, _, err := ключ.GetIntegerValue("SystemComponent"); err == nil && v == 1 {
		return RawProgram{}, false
	}
	if _, _, err := ключ.GetStringValue("ParentKeyName"); err == nil {
		return RawProgram{}, false
	}

	p := RawProgram{Name: name}
	p.Version, _, _ = ключ.GetStringValue("DisplayVersion")
	p.Publisher, _, _ = ключ.GetStringValue("Publisher")
	p.InstallDate, _, _ = ключ.GetStringValue("InstallDate")

	return p, true
}

// monitorsFromRegistry читает EDID мониторов, перечисленных в active.
//
// Список подключённых приходит снаружи, из WMI, и это важно: реестр хранит
// ВСЕ мониторы, когда-либо подключавшиеся к машине. На живой машине их
// оказалось семь при одном подключённом — шесть призраков, каждый из которых
// выглядел бы в учёте как замена монитора.
func monitorsFromRegistry(active map[string]bool) ([]RawMonitor, error) {
	корень, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Enum\DISPLAY`, registry.READ)
	if err != nil {
		return nil, err
	}
	defer func() { _ = корень.Close() }()

	модели, err := корень.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}

	var out []RawMonitor
	видели := map[string]bool{}

	for _, модель := range модели {
		устройство, err := registry.OpenKey(корень, модель, registry.READ)
		if err != nil {
			continue
		}

		экземпляры, err := устройство.ReadSubKeyNames(-1)
		_ = устройство.Close()
		if err != nil {
			continue
		}

		for _, экземпляр := range путиЭкземпляров(модель, экземпляры) {
			if !active[normalizeMonitorInstance(экземпляр)] {
				continue
			}

			m, ok := монитор(корень, экземпляр)
			if !ok {
				continue
			}
			// Один монитор, переставленный в другой разъём, оставляет в
			// реестре две записи. В отчёте он должен быть один.
			ключ := m.Manufacturer + "\x00" + m.Model + "\x00" + m.Serial
			if видели[ключ] {
				continue
			}
			видели[ключ] = true
			out = append(out, m)
		}
	}

	return out, nil
}

// путиЭкземпляров собирает полные пути к экземплярам одной модели.
func путиЭкземпляров(модель string, имена []string) []string {
	out := make([]string, 0, len(имена))
	for _, имя := range имена {
		out = append(out, модель+`\`+имя)
	}
	return out
}

// монитор читает EDID одного экземпляра.
func монитор(корень registry.Key, путь string) (RawMonitor, bool) {
	параметры, err := registry.OpenKey(корень, путь+`\Device Parameters`, registry.QUERY_VALUE)
	if err != nil {
		return RawMonitor{}, false
	}
	defer func() { _ = параметры.Close() }()

	edid, _, err := параметры.GetBinaryValue("EDID")
	if err != nil {
		return RawMonitor{}, false
	}

	// В реестре под теми же именами лежит мусор от давно отключённых
	// устройств; принимать его — значит завести монитор-призрак.
	m, err := parseEDID(edid)
	if err != nil {
		return RawMonitor{}, false
	}

	return m, true
}
