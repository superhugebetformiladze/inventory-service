//go:build windows

package collect

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/yusufpapurcu/wmi"
)

// Сбор на Windows.
//
// Здесь только запросы к системе: всё, что превращает ответы в снимок, лежит
// в assemble.go и parse.go и проверяется тестами на любой машине. Причина
// такая: этот файл нельзя прогнать нигде, кроме живой Windows, и держать в
// нём хоть какую-то логику — значит не проверять её вовсе.
//
// Три версии Windows в парке ведут себя по-разному, и это не мелочь:
// MSFT_PhysicalDisk появился в Windows 8, SMBIOSMemoryType — в Windows 10.
// Поэтому клиент WMI настроен не считать отсутствующее свойство ошибкой:
// иначе на Windows 7 не собиралось бы вообще ничего.
var клиентWMI = &wmi.Client{
	AllowMissingFields: true,
	NonePtrZero:        true,
	PtrNil:             true,
}

func gather(ctx context.Context) Raw {
	// Запас на случай, если сбор не уложится в срок: имя машины и адреса
	// карт есть и без WMI, а по ним машину уже можно опознать. Пустой снимок
	// вместо опоздавшего был бы хуже — служба сочла бы, что оборудование
	// исчезло.
	запасной := Raw{}
	запасной.System.Name = hostname()
	if nics, err := stdlibNICs(); err == nil {
		запасной.NICs = nics
	}

	готово := make(chan Raw, 1)
	go func() {
		// COM привязан к потоку операционной системы, а горутина Go по
		// потокам кочует. Без этой привязки часть запросов к WMI отвечает
		// ошибкой инициализации на ровном месте.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		готово <- gatherWMI()
	}()

	select {
	case r := <-готово:
		return r
	case <-ctx.Done():
		// Горутина остаётся висеть на неотвечающем WMI: прервать вызов COM
		// снаружи нечем. Она завершится сама, когда WMI ответит, и результат
		// уйдёт в буфер канала, откуда его никто не заберёт.
		запасной.Fail("all", errors.New("сбор не уложился в отведённое время"))
		return запасной
	}
}

func gatherWMI() Raw {
	var r Raw

	r.Fail("system", one(`SELECT * FROM Win32_ComputerSystem`, &r.System))
	r.Fail("bios", one(`SELECT * FROM Win32_BIOS`, &r.BIOS))
	r.Fail("product", one(`SELECT * FROM Win32_ComputerSystemProduct`, &r.Product))
	r.Fail("board", one(`SELECT * FROM Win32_BaseBoard`, &r.Board))
	r.Fail("chassis", one(`SELECT * FROM Win32_SystemEnclosure`, &r.Chassis))
	r.Fail("os", one(`SELECT * FROM Win32_OperatingSystem`, &r.OS))

	r.Fail("cpu", клиентWMI.Query(`SELECT * FROM Win32_Processor`, &r.CPUs))
	r.Fail("memory", клиентWMI.Query(`SELECT * FROM Win32_PhysicalMemory`, &r.Memory))
	r.Fail("disks", клиентWMI.Query(`SELECT * FROM Win32_DiskDrive`, &r.Disks))

	// Имя машины из WMI бывает пустым на машинах вне домена — своё имя
	// система знает и без WMI.
	if strings.TrimSpace(r.System.Name) == "" {
		r.System.Name = hostname()
	}

	// MSFT_PhysicalDisk живёт в отдельном пространстве имён и появился в
	// Windows 8. Его отсутствие — не сбой, а Windows 7: вид накопителя тогда
	// определяется по модели. Записывать это в неудачи не надо, иначе у
	// трети парка в каждом отчёте будет ложная тревога.
	if err := клиентWMI.Query(`SELECT * FROM MSFT_PhysicalDisk`, &r.Physical,
		nil, `root\Microsoft\Windows\Storage`); err != nil {
		r.Physical = nil
	}

	// Наработка — через связь каждого физического диска с его счётчиком
	// надёжности, как это делает сама Windows (см. запросНаработки). На
	// Windows 7 физических дисков нет, и цикл просто не выполняется. Отказ в
	// доступе — настоящая неудача: разовый запуск не от администратора; он
	// одинаков для всех дисков, поэтому записывается один раз.
	for _, диск := range r.Physical {
		if strings.TrimSpace(диск.ObjectId) == "" {
			continue
		}
		var счётчики []RawReliability
		if err := клиентWMI.Query(запросНаработки(диск.ObjectId), &счётчики,
			nil, `root\Microsoft\Windows\Storage`); err != nil {
			r.Fail("disk_hours", err)
			break
		}
		for _, rc := range счётчики {
			rc.DiskObjectId = диск.ObjectId
			r.Reliability = append(r.Reliability, rc)
		}
	}

	nics, err := networkAdapters()
	if err != nil {
		// Запасной путь: без шлюза и DNS, но с именами и адресами карт.
		// Опознание машины держится именно на них.
		nics, err = stdlibNICs()
	}
	r.Fail("interfaces", err)
	r.NICs = nics

	// Какие мониторы подключены СЕЙЧАС — спрашивается у WMI; чем они являются
	// — читается из EDID в реестре. Каждый источник отвечает на тот вопрос,
	// на который может ответить достоверно.
	active, err := activeMonitors()
	if err != nil {
		// Без списка подключённых мониторы не собираются вовсе. Взять из
		// реестра все — значит завести машине шесть мониторов-призраков и
		// показывать замену там, где её не было; лучше честно записать
		// неудачу.
		r.Fail("monitors", err)
	} else {
		monitors, err := monitorsFromRegistry(active)
		r.Fail("monitors", err)
		r.Monitors = monitors
	}

	software, err := installedSoftware()
	r.Fail("software", err)
	r.Software = software

	return r
}

// one выполняет запрос, от которого ожидается ровно одна строка.
func one[T any](query string, dst *T) error {
	var rows []T
	if err := клиентWMI.Query(query, &rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("запрос %q не вернул ни одной строки", query)
	}
	*dst = rows[0]
	return nil
}

// wmiMonitor — WmiMonitorID из пространства root\wmi.
//
// Из всего класса нужно одно поле: имя экземпляра. Названия и заводские
// номера он отдаёт массивами шестнадцатибитных чисел, и разбирать их через
// COM незачем — те же сведения лежат в EDID с зафиксированной раскладкой.
type wmiMonitor struct {
	InstanceName string
}

// activeMonitors — какие мониторы подключены прямо сейчас.
func activeMonitors() (map[string]bool, error) {
	var list []wmiMonitor
	if err := клиентWMI.Query(`SELECT InstanceName FROM WmiMonitorID`, &list,
		nil, `root\wmi`); err != nil {
		return nil, fmt.Errorf("список подключённых мониторов: %w", err)
	}

	active := make(map[string]bool, len(list))
	for _, m := range list {
		active[normalizeMonitorInstance(m.InstanceName)] = true
	}
	return active, nil
}
