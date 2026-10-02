//go:build !windows

package collect

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Сбор на не-Windows.
//
// Рабочие станции в парке — Windows, и этот файл существует не для них. Он
// нужен, чтобы агента можно было целиком прогнать в контейнере: расписание,
// буфер отчётов, повторы, опрос заданий — всё это к платформе не привязано, а
// без работающего сбора проверить их было бы не на чем.
//
// Поэтому здесь ровно столько, чтобы снимок проходил проверку контрактом, и
// ни строкой больше: /proc и /sys разбираются грубо и без запасных путей.
func gather(_ context.Context) Raw {
	var r Raw

	r.System.Name = hostname()
	r.System.Manufacturer = dmi("sys_vendor")
	r.System.Model = dmi("product_name")
	r.System.Workgroup = "linux"
	r.BIOS.SerialNumber = dmi("product_serial")
	r.Product.UUID = dmi("product_uuid")
	r.Board.Manufacturer = dmi("board_vendor")
	r.Board.Product = dmi("board_name")

	if code, err := strconv.ParseInt(dmi("chassis_type"), 10, 32); err == nil {
		r.Chassis.ChassisTypes = []int32{int32(code)}
	}

	release := keyValueFile("/etc/os-release", "=")
	r.OS.Caption = strings.Trim(release["PRETTY_NAME"], `"`)
	r.OS.Version = strings.Trim(release["VERSION_ID"], `"`)
	r.OS.OSArchitecture = archName()

	cpu := keyValueFile("/proc/cpuinfo", ":")
	ядра, _ := strconv.Atoi(cpu["cpu cores"])
	потоки := countLines("/proc/cpuinfo", "processor")
	r.CPUs = []RawProcessor{{
		Name:                      cpu["model name"],
		NumberOfCores:             счётЯдер(max(ядра, потоки)),
		NumberOfLogicalProcessors: счётЯдер(потоки),
	}}

	// MemTotal в килобайтах — единственная единица, в которой /proc отвечает.
	mem := strings.Fields(keyValueFile("/proc/meminfo", ":")["MemTotal"])
	if len(mem) > 0 {
		if кб, err := strconv.ParseUint(mem[0], 10, 64); err == nil {
			r.System.TotalPhysicalMemory = кб * 1024
		}
	}

	r.Disks = blockDevices()

	nics, err := stdlibNICs()
	r.Fail("interfaces", err)
	r.NICs = nics

	return r
}

// счётЯдер приводит разобранное число к типу, в котором его ждёт сборка.
func счётЯдер(n int) uint32 {
	if n < 0 || n > 4096 {
		return 0
	}
	return uint32(n)
}

// dmi читает одно поле из сведений о материнской плате.
//
// Часть полей доступна только root; отсутствие — не ошибка, а обычное
// состояние, поэтому неудача превращается в пустую строку молча.
func dmi(name string) string {
	b, err := os.ReadFile(filepath.Join("/sys/class/dmi/id", name)) //nolint:gosec // путь склеен из константы и имени поля
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// blockDevices — накопители из /sys/block.
func blockDevices() []RawDiskDrive {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}

	var out []RawDiskDrive
	for i, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
			continue
		}

		// Размер в /sys всегда в секторах по 512 байт, независимо от
		// настоящего размера сектора устройства.
		секторы, err := strconv.ParseUint(sysFile("/sys/block/"+name+"/size"), 10, 64)
		if err != nil || секторы == 0 {
			continue
		}

		диск := RawDiskDrive{
			Index:  uint32(i),
			Model:  sysFile("/sys/block/" + name + "/device/model"),
			Size:   секторы * 512,
			Status: "OK",
		}
		if sysFile("/sys/block/"+name+"/queue/rotational") == "0" {
			диск.Model += " SSD"
		}
		out = append(out, диск)
	}
	return out
}

func sysFile(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // читаются только пути, собранные здесь же из /sys
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// keyValueFile разбирает файл вида «ключ<разделитель>значение».
func keyValueFile(path, sep string) map[string]string {
	out := map[string]string{}

	f, err := os.Open(path) //nolint:gosec // читаются только /proc и /etc/os-release
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), sep)
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		// Первое значение, а не последнее: в /proc/cpuinfo одинаковые ключи
		// повторяются для каждого ядра.
		if _, есть := out[key]; !есть {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

func countLines(path, prefix string) int {
	f, err := os.Open(path) //nolint:gosec // читаются только пути, заданные в этом файле
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()

	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), prefix) {
			n++
		}
	}
	return n
}

// archName — разрядность системы в том же виде, в каком её называет WMI.
func archName() string {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return "64-bit"
	case "386", "arm":
		return "32-bit"
	}
	return runtime.GOARCH
}
