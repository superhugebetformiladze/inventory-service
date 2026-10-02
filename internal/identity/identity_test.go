package identity

import (
	"testing"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

func снимок(agentID, serial, mac string) contract.Snapshot {
	return contract.Snapshot{
		SchemaVersion: contract.SchemaVersion,
		AgentID:       agentID,
		CollectedAt:   time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
		Origin:        contract.OriginNetwork,
		Machine:       contract.Machine{Hostname: "PC-01", Serial: serial},
		Interfaces:    []contract.Interface{{Name: "Ethernet", MAC: mac}},
	}
}

func база(owner map[Identifier]HostID, strong map[HostID][]Identifier) Known {
	if owner == nil {
		owner = map[Identifier]HostID{}
	}
	if strong == nil {
		strong = map[HostID][]Identifier{}
	}
	return Known{Owner: owner, Strong: strong}
}

// ---------------------------------------------------------------------------
// Четыре сценария, на которых такие системы обычно и ломаются
// ---------------------------------------------------------------------------

func TestСценарийПереименованиеМашины(t *testing.T) {
	// Человек переименовал компьютер. Отметка агента та же — карточка должна
	// остаться той же.
	было := снимок("метка-1", "7XKPL03", "a4:bb:6d:11:22:33")
	стало := снимок("метка-1", "7XKPL03", "a4:bb:6d:11:22:33")
	стало.Machine.Hostname = "БУХГАЛТЕРИЯ-2"

	known := база(map[Identifier]HostID{
		{KindAgent, "метка-1"}:         "хост-1",
		{KindSerial, "7XKPL03"}:        "хост-1",
		{KindMAC, "a4:bb:6d:11:22:33"}: "хост-1",
	}, map[HostID][]Identifier{
		"хост-1": {{KindSerial, "7XKPL03"}},
	})

	d := Decide(Identifiers(стало), known)

	if d.Verdict != VerdictMatched {
		t.Fatalf("переименование не должно менять опознание: %s (%s)", d.Verdict, d.Reason)
	}
	if d.Host != "хост-1" {
		t.Errorf("опознана машина %q вместо «хост-1»", d.Host)
	}
	_ = было
}

func TestСценарийЗаменаДискаОтметкаПотеряна(t *testing.T) {
	// Диск заменили, систему переставили: отметка агента новая. Но заводской
	// номер материнской платы прежний — машина должна найтись по нему, а
	// новая отметка запомниться.
	новый := снимок("метка-2-новая", "7XKPL03", "a4:bb:6d:11:22:33")

	known := база(map[Identifier]HostID{
		{KindAgent, "метка-1-старая"}: "хост-1",
		{KindSerial, "7XKPL03"}:       "хост-1",
	}, map[HostID][]Identifier{
		"хост-1": {{KindSerial, "7XKPL03"}},
	})

	d := Decide(Identifiers(новый), known)

	if d.Verdict != VerdictMatched {
		t.Fatalf("машина должна найтись по заводскому номеру: %s (%s)", d.Verdict, d.Reason)
	}
	if d.Host != "хост-1" {
		t.Fatalf("опознана машина %q вместо «хост-1»", d.Host)
	}

	var запомнили bool
	for _, id := range d.Learn {
		if id.Kind == KindAgent && id.Value == "метка-2-новая" {
			запомнили = true
		}
	}
	if !запомнили {
		t.Error("новая отметка агента должна запомниться, иначе следующий отчёт снова пойдёт длинным путём")
	}
}

func TestСценарийКлонВиртуальнойМашины(t *testing.T) {
	// Копию виртуальной машины сняли вместе с диском: отметка агента у неё та
	// же, что у исходной, а заводской признак свой. Слить их в одну машину
	// нельзя — данные обеих станут кашей.
	клон := снимок("метка-1", "КЛОН-0002", "aa:bb:cc:00:00:02")

	known := база(map[Identifier]HostID{
		{KindAgent, "метка-1"}:     "хост-1",
		{KindSerial, "ИСХОД-0001"}: "хост-1",
	}, map[HostID][]Identifier{
		"хост-1": {{KindSerial, "ИСХОД-0001"}},
	})

	d := Decide(Identifiers(клон), known)

	if d.Verdict != VerdictAmbiguous {
		t.Fatalf("клон должен уйти в разбор, получено: %s", d.Verdict)
	}
	if d.Reason == "" {
		t.Error("человеку нужна причина, а не просто «разберитесь»")
	}
	if len(d.Candidates) == 0 {
		t.Error("подсказка о том, с какой машиной спутали, обязательна")
	}
}

func TestСценарийНоваяМашина(t *testing.T) {
	// Собрали новый компьютер: ни один признак не известен. Заводского номера
	// достаточно, чтобы завести машину и потом уверенно её находить.
	новая := снимок("метка-новая", "NEW12345", "aa:bb:cc:dd:ee:ff")

	d := Decide(Identifiers(новая), база(nil, nil))

	if d.Verdict != VerdictNew {
		t.Fatalf("ожидалось заведение новой машины, получено: %s (%s)", d.Verdict, d.Reason)
	}
	if len(d.Learn) < 2 {
		t.Errorf("за новой машиной должны закрепиться все её признаки, получено %d", len(d.Learn))
	}
}

// ---------------------------------------------------------------------------
// Осторожность там, где признаков не хватает
// ---------------------------------------------------------------------------

func TestСборнаяМашинаБезЗаводскогоНомераУходитВРазбор(t *testing.T) {
	// Заводского номера нет (сборка), есть только отметка агента и адрес
	// карты. Завести машину по ним — значит завести дубликат при первой же
	// переустановке или замене карты.
	сборная := снимок("метка-новая", "To Be Filled By O.E.M.", "aa:bb:cc:dd:ee:01")

	d := Decide(Identifiers(сборная), база(nil, nil))

	if d.Verdict != VerdictAmbiguous {
		t.Fatalf("ожидался разбор, получено: %s", d.Verdict)
	}
}

func TestПризнакиУказываютНаРазныеМашины(t *testing.T) {
	// Так выглядит сетевая карта, переставленная из одной машины в другую.
	// Выбрать наугад значит испортить обе карточки.
	снимок := снимок("метка-1", "SER-1", "aa:bb:cc:00:00:09")

	known := база(map[Identifier]HostID{
		{KindAgent, "метка-1"}:         "хост-1",
		{KindMAC, "aa:bb:cc:00:00:09"}: "хост-2",
	}, nil)

	d := Decide(Identifiers(снимок), known)

	if d.Verdict != VerdictAmbiguous {
		t.Fatalf("ожидался разбор, получено: %s", d.Verdict)
	}
	if len(d.Candidates) != 2 {
		t.Errorf("в подсказке должны быть обе машины, получено %d", len(d.Candidates))
	}
}

func TestПустойСнимокНеОпознаётся(t *testing.T) {
	d := Decide(nil, база(nil, nil))
	if d.Verdict != VerdictAmbiguous {
		t.Fatalf("ожидался разбор, получено: %s", d.Verdict)
	}
}

// ---------------------------------------------------------------------------
// Сбор признаков из снимка
// ---------------------------------------------------------------------------

func TestВиртуальныеИнтерфейсыНеПопадаютВПризнаки(t *testing.T) {
	// Адрес VPN-адаптера меняется от подключения к подключению: опознание по
	// нему давало бы новую машину при каждом входе в сеть из дома.
	snap := снимок("метка-1", "SER-1", "aa:bb:cc:dd:ee:01")
	snap.Interfaces = append(snap.Interfaces, contract.Interface{
		Name: "VPN", MAC: "00:ff:aa:bb:cc:dd", Virtual: true,
	})

	for _, id := range Identifiers(snap) {
		if id.Kind == KindMAC && id.Value == "00:ff:aa:bb:cc:dd" {
			t.Fatal("адрес виртуального адаптера попал в признаки")
		}
	}
}

func TestЗаглушкаВместоЗаводскогоНомераНеПризнак(t *testing.T) {
	// Опознание по такой «строке» склеило бы в одну машину весь сборный парк.
	snap := снимок("метка-1", "Default string", "aa:bb:cc:dd:ee:01")

	for _, id := range Identifiers(snap) {
		if id.Kind == KindSerial {
			t.Fatalf("заглушка производителя принята за признак: %q", id.Value)
		}
	}
}

func TestАдресаПриводятсяКОдномуВиду(t *testing.T) {
	// WMI отдаёт «AA-BB-CC-DD-EE-01», ip — «aa:bb:cc:dd:ee:01». Без
	// приведения одна машина превратилась бы в две.
	дефис := Identifiers(снимок("м", "SER-1", "AA-BB-CC-DD-EE-01"))
	двоеточие := Identifiers(снимок("м", "SER-1", "aa:bb:cc:dd:ee:01"))

	if дефис[len(дефис)-1] != двоеточие[len(двоеточие)-1] {
		t.Errorf("один и тот же адрес дал разные признаки: %v против %v",
			дефис[len(дефис)-1], двоеточие[len(двоеточие)-1])
	}
}

func TestПорядокПризнаковПоНадёжности(t *testing.T) {
	snap := снимок("метка", "SER-1", "aa:bb:cc:dd:ee:01")
	snap.Machine.UUID = "4c4c4544-0058-4b10-8050-b4c04f303733"

	ids := Identifiers(snap)
	ожидание := []Kind{KindAgent, KindSerial, KindUUID, KindMAC}

	if len(ids) != len(ожидание) {
		t.Fatalf("признаков %d, ожидалось %d: %v", len(ids), len(ожидание), ids)
	}
	for i, kind := range ожидание {
		if ids[i].Kind != kind {
			t.Errorf("на позиции %d признак %s, ожидался %s", i, ids[i].Kind, kind)
		}
	}
}

func TestАдресКартыНеСильныйПризнак(t *testing.T) {
	// Карту переставляют из машины в машину; запись, заведённая по одному
	// лишь адресу, завтра окажется чужой.
	if (Identifier{KindMAC, "aa:bb:cc:dd:ee:01"}).Strong() {
		t.Error("адрес сетевой карты не должен считаться надёжным признаком")
	}
	if !(Identifier{KindSerial, "SER-1"}).Strong() {
		t.Error("заводской номер должен считаться надёжным")
	}
}
