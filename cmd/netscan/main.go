// Command netscan — опрос коммутаторов с машины, у которой есть к ним доступ.
//
// Служба может стоять там, откуда сеть управления не видна. Тогда опрос
// запускают отсюда — с ноутбука администратора или сервера в нужном сегменте —
// и приносят папку со снимками службе:
//
//	netscan -targets switches.txt -out D:\обходы
//	make netscan-import DIR=D:/обходы            (на машине службы)
//
// Базы и ленты здесь нет: только опрос и файлы. Логика сбора — та же, что у
// периодического опроса службы (internal/netscan), поэтому снимки не
// отличаются ничем, кроме того, откуда их сняли.
//
// Строка доступа берётся из переменной SNMP_COMMUNITY или из -community.
// Флаг удобнее, но остаётся в истории команд — переменная предпочтительнее.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/broton07/inventory_service/internal/netscan"
)

func main() {
	targets := flag.String("targets", "", "файл со списком адресов, по одному в строке (# — комментарий)")
	list := flag.String("ip", "", "адреса через запятую — вместо файла")
	out := flag.String("out", "", "куда положить снимки")
	community := flag.String("community", "", "строка доступа SNMP; лучше задать SNMP_COMMUNITY")
	timeout := flag.Duration("timeout", 5*time.Second, "таймаут одного запроса")
	perDevice := flag.Duration("device-timeout", 2*time.Minute, "срок на одно устройство целиком")
	flag.Parse()

	if err := run(*targets, *list, *out, *community, *timeout, *perDevice); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(targetsFile, list, out, community string, timeout, perDevice time.Duration) error {
	if out == "" {
		return errors.New("нужен -out: каталог для снимков")
	}
	if community == "" {
		community = os.Getenv("SNMP_COMMUNITY")
	}
	if community == "" {
		return errors.New("не задана строка доступа: SNMP_COMMUNITY или -community")
	}

	addrs, err := readTargets(targetsFile, list)
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return errors.New("нет адресов: -targets <файл> или -ip <адреса>")
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("каталог снимков: %w", err)
	}

	failed := 0
	for _, addr := range addrs {
		if err := pollOne(addr, out, community, timeout, perDevice); err != nil {
			failed++
			fmt.Printf("✗ %s: %v\n", addr, err)
		}
	}

	fmt.Printf("\nопрошено: %d, не ответили: %d\n", len(addrs)-failed, failed)
	return nil
}

func pollOne(addr, out, community string, timeout, perDevice time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), perDevice)
	defer cancel()

	walker, err := netscan.NewLiveWalker(addr, netscan.SNMPOptions{
		Community: community, Timeout: timeout, Retries: 1,
	})
	if err != nil {
		return err
	}
	dev, err := netscan.Collect(ctx, walker, addr, time.Now())
	if err != nil {
		return err
	}

	body, err := json.MarshalIndent(dev, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(out, addr+".json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}

	macs := 0
	for _, p := range dev.Ports {
		macs += len(p.MACs)
	}
	fmt.Printf("✓ %s %s — портов %d, MAC %d, ARP %d\n", addr, dev.SysName, len(dev.Ports), macs, len(dev.ARP))
	return nil
}

func readTargets(file, list string) ([]string, error) {
	var out []string
	for _, a := range strings.Split(list, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	if file == "" {
		return out, nil
	}

	f, err := os.Open(file) //nolint:gosec // файл указал сам оператор
	if err != nil {
		return nil, fmt.Errorf("список адресов: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out, scanner.Err()
}
