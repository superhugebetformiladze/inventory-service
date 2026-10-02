package collect

import "strings"

// запросНаработки — WQL-запрос счётчика надёжности одного физического диска.
//
// Через связь с диском, а не перечислением класса счётчиков. Проверено на
// живой машине 2026-09-15: перечисление MSFT_StorageReliabilityCounter от
// администратора прошло без ошибки и не вернуло ни одной строки, а штатная
// Get-StorageReliabilityCounter ходит именно через связь
// MSFT_PhysicalDiskToStorageReliabilityCounter.
//
// Идентификатор диска — строка с кавычками и обратными косыми чертами, и в
// пути объекта WQL обе экранируются обратной косой чертой. Ошибка
// экранирования даёт не пустой ответ, а синтаксическую ошибку запроса, поэтому
// экранирование живёт здесь, где его проверяет тест.
func запросНаработки(objectID string) string {
	экранированный := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(objectID)
	return `ASSOCIATORS OF {MSFT_PhysicalDisk.ObjectId="` + экранированный +
		`"} WHERE AssocClass = MSFT_PhysicalDiskToStorageReliabilityCounter`
}

// ключСчётчика и ключиДиска сопоставляют счётчик с диском: сперва по диску, у
// которого счётчик спрашивали, потом по номеру устройства.
func ключСчётчика(rc RawReliability) string {
	if id := strings.TrimSpace(rc.DiskObjectId); id != "" {
		return "obj:" + id
	}
	return "dev:" + strings.TrimSpace(rc.DeviceId)
}

func ключиДиска(p RawPhysicalDisk) []string {
	var out []string
	if id := strings.TrimSpace(p.ObjectId); id != "" {
		out = append(out, "obj:"+id)
	}
	return append(out, "dev:"+strings.TrimSpace(p.DeviceId))
}

// наработкаДиска — наработка диска по сопоставленным счётчикам; ноль — «не знаем».
func наработкаДиска(часы map[string]uint32, p RawPhysicalDisk) uint32 {
	for _, ключ := range ключиДиска(p) {
		if h, есть := часы[ключ]; есть {
			return h
		}
	}
	return 0
}
