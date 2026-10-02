package collect

import (
	"context"
	"net"
	"os"
	"strings"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

// Collect снимает всё, что агент знает о своей машине.
//
// Ошибки не возвращаются наверх намеренно: частичный сбор — это норма, а не
// сбой. WMI на старой Windows регулярно отдаёт не всё, и отказ от снимка
// из-за нечитаемого S.M.A.R.T. означал бы, что машина вообще не попадёт в
// учёт. Что не собралось — перечислено в Snapshot.Failures.
func Collect(ctx context.Context, mark, version string, origin contract.Origin) contract.Snapshot {
	return Assemble(gather(ctx), mark, version, origin, time.Now())
}

// stdlibNICs — сетевые интерфейсы средствами самого Go.
//
// Это нижняя граница: работает на любой платформе и не зависит ни от WMI, ни
// от разбора файлов. Всё, чего здесь нет — шлюз, DNS, признак DHCP, скорость
// линка, — платформенный сбор добавляет сверху. Если он не сработал, у
// службы всё равно останутся имя и адрес карты, а этого хватает для
// опознания.
func stdlibNICs() ([]RawNIC, error) {
	list, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	out := make([]RawNIC, 0, len(list))
	for _, i := range list {
		mac := i.HardwareAddr.String()
		if mac == "" {
			// Интерфейс без адреса опознанию бесполезен, а места в отчёте
			// занимает столько же.
			continue
		}

		nic := RawNIC{Name: i.Name, MAC: mac}

		addrs, err := i.Addrs()
		if err != nil {
			// Интерфейс есть, адреса не прочитались — сам интерфейс всё
			// равно нужен: по адресу карты машину опознают.
			out = append(out, nic)
			continue
		}

		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil || ip.IsLinkLocalUnicast() {
				// Ссылочно-локальные адреса выдаёт себе сама система, когда
				// не смогла получить настоящий. В учёте это шум: они меняются
				// от включения к включению.
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				nic.IPv4 = append(nic.IPv4, v4.String())
			} else {
				nic.IPv6 = append(nic.IPv6, ip.String())
			}
		}

		out = append(out, nic)
	}

	return out, nil
}

// hostname — имя машины, как её видит система.
func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	// Короткое имя, без доменного хвоста: домен для этого есть отдельное
	// поле, а «PC-214-01.zavod.local» в карточке никто не ищет.
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	return name
}
