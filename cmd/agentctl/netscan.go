package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/broton07/inventory_service/internal/netscan"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

// принятьОбходы — снимки сетевых устройств из файлов.
//
//	agentctl netscan-import -dir /mnt/обходы
//
// Пока у службы нет сетевого доступа к коммутаторам, снимки снимают с машины,
// у которой доступ есть, и приносят файлами. Два вида:
//
//   - <адрес>.walk — вывод `snmpwalk -On -Ox <адрес> …` (scripts/snmp-walks.*);
//     имя файла — адрес управления устройства;
//   - <что угодно>.json — готовый снимок, записанный `netscan -out` с той же
//     машины.
//
// Повторный приём безвреден: неизменившееся устройство в ленту не уходит
// (I9), а снимок старше записанного не затирает свежий.
func принятьОбходы(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("netscan-import", flag.ExitOnError)
	dir := fs.String("dir", "", "каталог с файлами .walk и .json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("нужен -dir")
	}

	entries, err := os.ReadDir(*dir)
	if err != nil {
		return fmt.Errorf("чтение каталога: %w", err)
	}

	приём := netscan.NewService(pool, time.Now)
	принято, отказано := 0, 0

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		путь := filepath.Join(*dir, e.Name())

		dev, err := прочитатьСнимок(ctx, путь)
		if errors.Is(err, errНеОбход) {
			continue
		}
		if err != nil {
			отказано++
			fmt.Printf("✗ %s: %v\n", e.Name(), err)
			continue
		}

		res, err := приём.Accept(ctx, dev)
		if err != nil {
			отказано++
			fmt.Printf("✗ %s: %v\n", e.Name(), err)
			continue
		}
		принято++
		fmt.Printf("✓ %s: %s %s — %s\n", e.Name(), dev.ManagementIP, dev.SysName, описание(dev, res))
	}

	fmt.Printf("\nпринято: %d, отказано: %d\n", принято, отказано)
	if отказано > 0 {
		return fmt.Errorf("не принято файлов: %d", отказано)
	}
	return nil
}

var errНеОбход = errors.New("не файл обхода")

func прочитатьСнимок(ctx context.Context, путь string) (contract.NetworkDevice, error) {
	switch strings.ToLower(filepath.Ext(путь)) {
	case ".walk":
		адрес := strings.TrimSuffix(filepath.Base(путь), filepath.Ext(путь))
		if _, err := netip.ParseAddr(адрес); err != nil {
			return contract.NetworkDevice{}, fmt.Errorf("имя файла %q должно быть адресом устройства", адрес)
		}
		f, err := os.Open(путь) //nolint:gosec // путь — из каталога, который указал оператор
		if err != nil {
			return contract.NetworkDevice{}, err
		}
		defer func() { _ = f.Close() }()

		walker, err := netscan.ParseWalk(f)
		if err != nil {
			return contract.NetworkDevice{}, err
		}
		info, err := f.Stat()
		if err != nil {
			return contract.NetworkDevice{}, err
		}
		// Время сбора — время файла: обход могли снять неделю назад, и
		// выдавать его за сегодняшний значит затереть им свежий снимок.
		return netscan.Collect(ctx, walker, адрес, info.ModTime())
	case ".json":
		body, err := os.ReadFile(путь) //nolint:gosec // путь — из каталога, который указал оператор
		if err != nil {
			return contract.NetworkDevice{}, err
		}
		var dev contract.NetworkDevice
		if err := json.Unmarshal(body, &dev); err != nil {
			return contract.NetworkDevice{}, fmt.Errorf("разбор снимка: %w", err)
		}
		return dev, nil
	default:
		return contract.NetworkDevice{}, errНеОбход
	}
}

// опроситьУстройство — разовый живой опрос.
//
//	agentctl netscan-poll -target 192.168.4.172 [-dry]
//
// Первое, что запускают, когда появится доступ к сети: ответит ли коммутатор
// и что служба из него вытащит. С -dry снимок печатается и не записывается.
func опроситьУстройство(ctx context.Context, pool *storage.Pool, args []string) error {
	fs := flag.NewFlagSet("netscan-poll", flag.ExitOnError)
	target := fs.String("target", "", "адрес управления устройства")
	dry := fs.Bool("dry", false, "только показать снимок, не записывать")
	how := fs.String("walker", envOr("NETSCAN_WALKER", netscan.WalkerSnmpwalk),
		"чем опрашивать: snmpwalk (net-snmp) или gosnmp (встроенная библиотека)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" {
		return errors.New("нужен -target")
	}

	dial, cleanup, err := netscan.NewDialer(*how, netscan.SNMPOptions{
		Community: os.Getenv("SNMP_COMMUNITY"),
		Timeout:   5 * time.Second,
		Retries:   1,
	})
	if err != nil {
		return fmt.Errorf("%w (задайте SNMP_COMMUNITY в .env)", err)
	}
	defer cleanup()
	walker, err := dial(*target)
	if err != nil {
		return err
	}

	dev, err := netscan.Collect(ctx, walker, *target, time.Now())
	if err != nil {
		return err
	}

	if *dry {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(dev)
	}

	res, err := netscan.NewService(pool, time.Now).Accept(ctx, dev)
	if err != nil {
		return err
	}
	fmt.Printf("%s %s — %s\n", dev.ManagementIP, dev.SysName, описание(dev, res))
	return nil
}

// списокУстройств — что служба знает о сети.
func списокУстройств(ctx context.Context, pool *storage.Pool, _ []string) error {
	rows, err := pool.ListNetworkDevices(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("опрошенных устройств нет")
		return nil
	}
	sort.Slice(rows, func(i, j int) bool {
		a, _ := netip.ParseAddr(rows[i].ManagementIP)
		b, _ := netip.ParseAddr(rows[j].ManagementIP)
		return a.Less(b)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "АДРЕС\tИМЯ\tПОРТОВ\tОПРОШЕНО\tИЗМЕНЕНО\tИДЕНТИФИКАТОР")
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", r.ManagementIP, r.SysName, r.Ports,
			r.LastPolledAt.Local().Format("02.01 15:04"), r.ChangedAt.Local().Format("02.01 15:04"),
			r.DeviceID)
	}
	return w.Flush()
}

func описание(dev contract.NetworkDevice, res netscan.Result) string {
	macs, соседи, вверх := 0, 0, 0
	for _, p := range dev.Ports {
		macs += len(p.MACs)
		if p.Neighbor != nil {
			соседи++
		}
		if p.OperUp {
			вверх++
		}
	}
	состояние := "без изменений"
	switch {
	case res.FirstSeen:
		состояние = "новое устройство"
	case res.Changed:
		состояние = "изменилось"
	}
	return fmt.Sprintf("портов %d (включено %d), MAC %d, соседей LLDP %d, ARP %d; %s",
		len(dev.Ports), вверх, macs, соседи, len(dev.ARP), состояние)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
