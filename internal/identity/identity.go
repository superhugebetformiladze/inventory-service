// Package identity отвечает на вопрос «эта машина уже известна или новая».
//
// Звучит как мелочь, но именно здесь такие системы обычно и ломаются.
// Компьютер переименовывают. Диск меняют. Сетевую карту меняют. Виртуальную
// машину копируют вместе со всем содержимым. Если считать, что «имя
// компьютера — это и есть компьютер», то через полгода в базе два «ПК-341» и
// ни одного правильного.
//
// Решение принимается ЧИСТОЙ функцией: на входе признаки из снимка и то, что
// о них знает база, на выходе — что делать. Так четыре сценария, на которых
// всё ломается, проверяются тестами без базы вообще.
package identity

import (
	"github.com/broton07/inventory_service/pkg/contract"
)

// Kind — вид признака. Порядок объявления — порядок надёжности.
type Kind string

const (
	// KindAgent — отметка агента: записана при первом запуске и больше не
	// меняется. Переименование машины и замена диска её не трогают.
	KindAgent Kind = "agent"

	// KindSerial — заводской номер из BIOS. Переживает переустановку системы,
	// но у сборных машин его нет.
	KindSerial Kind = "serial"

	// KindUUID — идентификатор SMBIOS. Стабильнее серийного номера, но у
	// клонированных виртуальных машин совпадает с исходной.
	KindUUID Kind = "uuid"

	// KindMAC — адрес сетевой карты. Самый слабый: карту меняют, а в
	// докерах и виртуалках адреса и вовсе назначаются заново.
	KindMAC Kind = "mac"
)

// Identifier — один признак машины.
type Identifier struct {
	Kind  Kind
	Value string
}

// Strong — признак, по которому можно заводить машину не боясь дубликата.
//
// Адрес сетевой карты сюда не входит: карту переставляют из машины в машину, и
// новая запись, заведённая по одному лишь адресу, завтра окажется чужой.
func (i Identifier) Strong() bool {
	return i.Kind == KindAgent || i.Kind == KindSerial || i.Kind == KindUUID
}

// Identifiers вытаскивает из снимка все признаки, годные для опознания, в
// порядке убывания надёжности.
func Identifiers(snap contract.Snapshot) []Identifier {
	var ids []Identifier

	if snap.AgentID != "" {
		ids = append(ids, Identifier{KindAgent, snap.AgentID})
	}
	if serial := contract.UsableSerial(snap.Machine.Serial); serial != "" {
		ids = append(ids, Identifier{KindSerial, serial})
	}
	if u := contract.UsableSerial(snap.Machine.UUID); u != "" {
		ids = append(ids, Identifier{KindUUID, u})
	}

	// Только физические интерфейсы: адрес VPN-адаптера меняется от
	// подключения к подключению, и опознание по нему давало бы новую машину
	// при каждом входе в сеть из дома.
	for _, iface := range snap.PhysicalInterfaces() {
		if mac := contract.NormalizeMAC(iface.MAC); mac != "" {
			ids = append(ids, Identifier{KindMAC, mac})
		}
	}

	return ids
}

// Verdict — что делать с отчётом.
type Verdict string

const (
	// VerdictMatched — машина опознана.
	VerdictMatched Verdict = "matched"

	// VerdictNew — машины нет, но признаков достаточно, чтобы завести её и
	// потом уверенно находить.
	VerdictNew Verdict = "new"

	// VerdictAmbiguous — признаки противоречивы или слишком слабы. Запись не
	// создаётся и не переписывается: разбирается человеком.
	VerdictAmbiguous Verdict = "ambiguous"
)

// HostID — идентификатор машины в службе. Строкой, а не uuid.UUID: пакету
// незачем знать, как база выдаёт идентификаторы.
type HostID string

// Decision — решение по одному отчёту.
type Decision struct {
	Verdict Verdict

	// Host заполнен при VerdictMatched.
	Host HostID

	// Learn — признаки, которых у машины ещё нет и которые надо запомнить.
	// Так машина, опознанная по заводскому номеру после переустановки,
	// получает новую отметку агента и дальше находится по ней.
	Learn []Identifier

	// Reason заполнен при VerdictAmbiguous — его увидит человек в очереди
	// разбора, поэтому он на русском и по делу.
	Reason string

	// Candidates — машины, между которыми не удалось выбрать. Подсказка тому,
	// кто будет разбирать.
	Candidates []HostID
}

// Known — то, что база знает о признаках из снимка.
type Known struct {
	// Owner — какой машине принадлежит признак. Признак принадлежит не более
	// чем одной машине: это ограничение базы, а не соглашение.
	Owner map[Identifier]HostID

	// Strong — сильные признаки, уже записанные за машиной. Нужны, чтобы
	// заметить подмену: под известной отметкой приходит другой заводской
	// номер.
	Strong map[HostID][]Identifier
}

// Decide принимает решение по отчёту.
func Decide(ids []Identifier, known Known) Decision {
	if len(ids) == 0 {
		return Decision{
			Verdict: VerdictAmbiguous,
			Reason:  "в отчёте нет ни одного признака машины",
		}
	}

	owners := distinctOwners(ids, known.Owner)

	switch len(owners) {
	case 0:
		return decideUnknown(ids)
	case 1:
		return decideMatched(ids, owners[0], known)
	default:
		// Так выглядит переставленная сетевая карта и склейка двух карточек в
		// одну. Выбрать наугад значит испортить обе.
		return Decision{
			Verdict:    VerdictAmbiguous,
			Reason:     "признаки указывают на разные машины",
			Candidates: owners,
		}
	}
}

// decideUnknown — ни один признак не известен базе.
func decideUnknown(ids []Identifier) Decision {
	if !hasStrong(ids) {
		// Только имя и адрес карты. Завести машину по ним — значит завести
		// дубликат при первой же замене карты; поэтому в разбор.
		return Decision{
			Verdict: VerdictAmbiguous,
			Reason:  "нет ни одного надёжного признака: только сетевой адрес",
		}
	}

	return Decision{Verdict: VerdictNew, Learn: ids}
}

// decideMatched — все известные признаки указывают на одну машину.
func decideMatched(ids []Identifier, host HostID, known Known) Decision {
	// Подмена заводских признаков под известной отметкой.
	//
	// Так выглядит клон виртуальной машины: копия несёт отметку агента
	// исходной, но собственный серийный номер. Так же выглядит и замена
	// материнской платы — и это правильно, что оба случая идут к человеку:
	// в первом случае машины две, во втором одна, и различить их снаружи
	// нельзя.
	if conflict, ok := strongConflict(ids, host, known); ok {
		return Decision{
			Verdict: VerdictAmbiguous,
			Reason: "под известной отметкой пришёл другой заводской признак (" +
				string(conflict.Kind) + "): клон машины или замена платы",
			Candidates: []HostID{host},
		}
	}

	var learn []Identifier
	for _, id := range ids {
		if _, ok := known.Owner[id]; !ok {
			learn = append(learn, id)
		}
	}

	return Decision{Verdict: VerdictMatched, Host: host, Learn: learn}
}

// strongConflict — есть ли в снимке сильный признак того же вида, что уже
// записан за машиной, но с другим значением.
func strongConflict(ids []Identifier, host HostID, known Known) (Identifier, bool) {
	registered := known.Strong[host]

	for _, id := range ids {
		if !id.Strong() || id.Kind == KindAgent {
			// Отметка агента здесь не сравнивается: она как раз и есть то,
			// по чему машину нашли, а вторая отметка на одной машине —
			// нормальный итог переустановки.
			continue
		}
		for _, r := range registered {
			if r.Kind == id.Kind && r.Value != id.Value {
				return id, true
			}
		}
	}
	return Identifier{}, false
}

func hasStrong(ids []Identifier) bool {
	for _, id := range ids {
		if id.Strong() && id.Kind != KindAgent {
			// Одной отметки агента мало: её выдаёт себе сам агент, и
			// переустановленная система принесёт новую. Машина, заведённая
			// только по ней, при переустановке превратится в дубликат.
			return true
		}
	}
	return false
}

func distinctOwners(ids []Identifier, owner map[Identifier]HostID) []HostID {
	seen := map[HostID]bool{}
	var out []HostID

	for _, id := range ids {
		host, ok := owner[id]
		if !ok || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	return out
}
