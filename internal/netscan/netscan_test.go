package netscan

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/broton07/inventory_service/internal/publish"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

var момент = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

func обход(t *testing.T, имя string) *FileWalker {
	t.Helper()
	f, err := os.Open("testdata/" + имя)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	w, err := ParseWalk(f)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func собрать(t *testing.T, имя, адрес string) contract.NetworkDevice {
	t.Helper()
	dev, err := Collect(context.Background(), обход(t, имя), адрес, момент)
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.Validate(момент); err != nil {
		t.Fatalf("снимок не проходит контракт: %v", err)
	}
	return dev
}

func порт(t *testing.T, dev contract.NetworkDevice, номер int) contract.NetworkPort {
	t.Helper()
	for _, p := range dev.Ports {
		if p.Number == номер {
			return p
		}
	}
	t.Fatalf("порта %d нет", номер)
	return contract.NetworkPort{}
}

// ------------------------------------------------------------------
// Разбор файла
// ------------------------------------------------------------------

func TestРазборОбходаПонимаетВидыЗначений(t *testing.T) {
	w, err := ParseWalk(strings.NewReader(`.1.3.6.1.2.1.1.5.0 = Hex-STRING: 73 77
2D 31
.1.3.6.1.2.1.2.2.1.8.1 = INTEGER: up(1)
.1.3.6.1.2.1.1.3.0 = Timeticks: (42) 0:00:00.42
.1.3.6.1.2.1.1.2.0 = OID: .1.3.6.1.4.1.171
.1.3.6.1.2.1.4.22.1.2 = No Such Object available on this agent at this OID
`))
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]Variable{}
	_ = w.Walk(context.Background(), "1.3", func(v Variable) error {
		got[v.OID] = v
		return nil
	})

	if s := got["1.3.6.1.2.1.1.5.0"].String(); s != "sw-1" {
		t.Errorf("Hex-STRING с переносом: %q", s)
	}
	if n := got["1.3.6.1.2.1.2.2.1.8.1"].Int; n != 1 {
		t.Errorf("INTEGER up(1): %d", n)
	}
	if n := got["1.3.6.1.2.1.1.3.0"].Int; n != 42 {
		t.Errorf("Timeticks: %d", n)
	}
	if s := got["1.3.6.1.2.1.1.2.0"].String(); s != "1.3.6.1.4.1.171" {
		t.Errorf("OID: %q", s)
	}
	if _, ok := got["1.3.6.1.2.1.4.22.1.2"]; ok {
		t.Error("«No Such Object» не должно попадать в обход")
	}
}

func TestOIDСравниваютсяЧислами(t *testing.T) {
	if !lessOID("1.3.6.9", "1.3.6.10") {
		t.Error("1.3.6.9 должно идти раньше 1.3.6.10")
	}
}

// ------------------------------------------------------------------
// Коммутатор доступа
// ------------------------------------------------------------------

func TestКоммутаторДоступа(t *testing.T) {
	dev := собрать(t, "192.168.4.172.walk", "192.168.4.172")

	if dev.DeviceID != "88:76:b9:c8:89:80" || dev.ChassisMAC != dev.DeviceID {
		t.Errorf("идентификатор — MAC шасси: %q / %q", dev.DeviceID, dev.ChassisMAC)
	}
	if dev.SysName != "sw-412" {
		t.Errorf("имя из Hex-STRING: %q", dev.SysName)
	}

	// Интерфейс VLAN (тип 136) — не порт.
	if len(dev.Ports) != 6 {
		t.Fatalf("портов %d, ожидалось 6", len(dev.Ports))
	}
	if dev.Ports[0].Number != 1 || dev.Ports[5].Number != 26 {
		t.Errorf("порты по номеру: первый %d, последний %d", dev.Ports[0].Number, dev.Ports[5].Number)
	}

	p1 := порт(t, dev, 1)
	if len(p1.MACs) != 1 || p1.MACs[0] != "70:85:c2:11:22:33" {
		t.Errorf("порт 1: %v — собственный MAC (self) не должен попадать", p1.MACs)
	}
	if !p1.OperUp || p1.SpeedMbps != 1000 {
		t.Errorf("порт 1: включён %v, скорость %d", p1.OperUp, p1.SpeedMbps)
	}

	p2 := порт(t, dev, 2)
	if len(p2.MACs) != 2 || p2.MACs[0] != "00:15:65:aa:bb:cc" {
		t.Errorf("порт 2 — телефон и компьютер за ним: %v", p2.MACs)
	}
	if p2.PVID != 20 || p2.Alias != "бухгалтерия" {
		t.Errorf("порт 2: PVID %d, подпись %q", p2.PVID, p2.Alias)
	}

	if p3 := порт(t, dev, 3); p3.OperUp || !p3.AdminUp {
		t.Errorf("порт 3 включён администратором, но линка нет: %+v", p3)
	}
	if p4 := порт(t, dev, 4); p4.AdminUp {
		t.Error("порт 4 выключен администратором")
	}

	p25 := порт(t, dev, 25)
	if p25.Neighbor == nil {
		t.Fatal("на порту 25 — ядро по LLDP")
	}
	if n := *p25.Neighbor; n.ChassisID != "00:11:22:33:44:55" || n.PortID != "1/0/5" || n.SysName != "core" {
		t.Errorf("сосед: %+v", n)
	}
	if p25.Alias != "к ядру" {
		t.Errorf("подпись из Hex-STRING в UTF-8: %q", p25.Alias)
	}

	if p26 := порт(t, dev, 26); len(p26.MACs) != 4 {
		t.Errorf("порт 26 — неуправляемый коммутатор с четырьмя устройствами: %v", p26.MACs)
	}

	if len(dev.ARP) != 0 {
		t.Errorf("у коммутатора доступа ARP нет: %v", dev.ARP)
	}
}

// ------------------------------------------------------------------
// Ядро
// ------------------------------------------------------------------

func TestЯдроОтдаётARP(t *testing.T) {
	dev := собрать(t, "192.168.4.1.walk", "192.168.4.1")

	if dev.DeviceID != "00:11:22:33:44:55" {
		t.Errorf("идентификатор: %q", dev.DeviceID)
	}

	// Погашенная запись (invalid) не попадает.
	if len(dev.ARP) != 3 {
		t.Fatalf("ARP: %v", dev.ARP)
	}
	if dev.ARP[0].IP != "192.168.4.50" || dev.ARP[0].MAC != "00:80:77:01:02:03" {
		t.Errorf("первая запись ARP: %+v", dev.ARP[0])
	}

	p5 := порт(t, dev, 5)
	if p5.Neighbor == nil || p5.Neighbor.ChassisID != "88:76:b9:c8:89:80" || p5.Neighbor.PortID != "25" {
		t.Errorf("сосед на порту 5: %+v", p5.Neighbor)
	}
	if p5.Neighbor != nil && p5.Neighbor.SysName != "sw-412" {
		t.Errorf("имя соседа из Hex-STRING: %q", p5.Neighbor.SysName)
	}
}

func TestМолчащееУстройство(t *testing.T) {
	_, err := Collect(context.Background(), &FileWalker{}, "192.168.4.2", момент)
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("ожидалось ErrUnreachable, получено %v", err)
	}
}

// ------------------------------------------------------------------
// Приём
// ------------------------------------------------------------------

type памятьУстройств struct {
	hash   map[string]string
	первое map[string]time.Time
	outbox []storage.OutboxMessage

	// Молчание: адрес → начало серии неудач, устройство → пометка.
	адрес   map[string]string // device_id → management_ip
	снимок  map[string][]byte
	неудачи map[string]time.Time
	молчит  map[string]bool
}

func (s *памятьУстройств) MarkNetworkFailure(_ context.Context, ip, _ string, now time.Time) error {
	if s.неудачи == nil {
		s.неудачи = map[string]time.Time{}
	}
	for _, addr := range s.адрес {
		if addr == ip {
			if _, ok := s.неудачи[ip]; !ok {
				s.неудачи[ip] = now
			}
		}
	}
	return nil
}

func (s *памятьУстройств) MarkNetworkSilent(
	_ context.Context, before time.Time,
	build func(storage.SilentNetworkDevice) (storage.OutboxMessage, error),
) ([]storage.SilentNetworkDevice, error) {
	if s.молчит == nil {
		s.молчит = map[string]bool{}
	}
	var found []storage.SilentNetworkDevice
	for id, ip := range s.адрес {
		since, failing := s.неудачи[ip]
		if !failing || s.молчит[id] || since.After(before) {
			continue
		}
		d := storage.SilentNetworkDevice{
			DeviceID: id, ManagementIP: ip, SilentSince: since, Payload: s.снимок[id],
		}
		msg, err := build(d)
		if err != nil {
			return nil, err
		}
		s.outbox = append(s.outbox, msg)
		s.молчит[id] = true
		found = append(found, d)
	}
	return found, nil
}

func (s *памятьУстройств) SaveNetworkDevice(
	_ context.Context, rec storage.NetworkDeviceRecord, now time.Time,
	build func(storage.SavedNetworkDevice) ([]storage.OutboxMessage, error),
) (storage.SavedNetworkDevice, error) {
	if s.hash == nil {
		s.hash, s.первое = map[string]string{}, map[string]time.Time{}
	}
	old, known := s.hash[rec.DeviceID]
	if !known {
		s.первое[rec.DeviceID] = now
	}
	saved := storage.SavedNetworkDevice{
		DeviceID: rec.DeviceID, FirstSeen: !known, FirstSeenAt: s.первое[rec.DeviceID],
		Changed:  !known || old != rec.Hash,
		Returned: s.молчит[rec.DeviceID],
	}
	s.hash[rec.DeviceID] = rec.Hash
	if s.адрес == nil {
		s.адрес, s.снимок = map[string]string{}, map[string][]byte{}
	}
	s.адрес[rec.DeviceID] = rec.ManagementIP
	s.снимок[rec.DeviceID] = rec.Payload
	delete(s.неудачи, rec.ManagementIP)
	delete(s.молчит, rec.DeviceID)

	msgs, err := build(saved)
	if err != nil {
		return storage.SavedNetworkDevice{}, err
	}
	s.outbox = append(s.outbox, msgs...)
	return saved, nil
}

func TestПриёмПубликуетТолькоИзменения(t *testing.T) {
	store := &памятьУстройств{}
	svc := NewService(store, func() time.Time { return момент })
	dev := собрать(t, "192.168.4.172.walk", "192.168.4.172")

	res, err := svc.Accept(context.Background(), dev)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FirstSeen || len(store.outbox) != 1 {
		t.Fatalf("первый приём: %+v, сообщений %d", res, len(store.outbox))
	}
	if m := store.outbox[0]; m.Topic != publish.TopicNetwork || m.Key != dev.DeviceID {
		t.Errorf("сообщение: топик %s, ключ %s", m.Topic, m.Key)
	}

	// Тот же снимок позже — время сбора другое, сеть та же (I9).
	again := dev
	again.CollectedAt = момент.Add(-time.Minute)
	if _, err := svc.Accept(context.Background(), again); err != nil {
		t.Fatal(err)
	}
	if len(store.outbox) != 1 {
		t.Errorf("неизменившееся устройство ушло в ленту ещё раз: %d", len(store.outbox))
	}

	// Компьютер переехал на другой порт — изменение.
	moved := dev
	moved.Ports = append([]contract.NetworkPort(nil), dev.Ports...)
	moved.Ports[0].MACs = nil
	if _, err := svc.Accept(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	if len(store.outbox) != 2 {
		t.Errorf("изменение не опубликовано: %d", len(store.outbox))
	}
}

func TestПриёмОтклоняетНегодныйСнимок(t *testing.T) {
	svc := NewService(&памятьУстройств{}, func() time.Time { return момент })
	dev := собрать(t, "192.168.4.172.walk", "192.168.4.172")
	dev.Ports[0].MACs = []string{"не адрес"}

	if _, err := svc.Accept(context.Background(), dev); !errors.Is(err, ErrInvalid) {
		t.Errorf("ожидалось ErrInvalid, получено %v", err)
	}
}

// ------------------------------------------------------------------
// Опрос по расписанию
// ------------------------------------------------------------------

func TestПроходНеОстанавливаетсяНаНедоступном(t *testing.T) {
	store := &памятьУстройств{}
	runner := NewRunner(NewService(store, func() time.Time { return момент }),
		func(target string) (Walker, error) {
			if target == "192.168.4.172" {
				return обход(t, "192.168.4.172.walk"), nil
			}
			return &FileWalker{}, nil // молчит
		},
		RunnerOptions{
			Targets: []string{"192.168.4.250", "192.168.4.172"},
			Now:     func() time.Time { return момент },
		})

	stats := runner.Tick(context.Background())
	if stats.Polled != 1 || stats.Failed != 1 || stats.Changed != 1 {
		t.Errorf("итог прохода: %+v", stats)
	}
}

func TestСтрокаДоступаБезПереводаСтроки(t *testing.T) {
	// .env, сохранённый в Windows, отдаёт значение с «\r» на конце.
	w, err := NewLiveWalker("192.168.8.10", SNMPOptions{Community: "secret\r"})
	if err != nil {
		t.Fatal(err)
	}
	if w.opts.Community != "secret" {
		t.Errorf("строка доступа: %q", w.opts.Community)
	}
	if _, err := NewLiveWalker("192.168.8.10", SNMPOptions{Community: " \r\n"}); err == nil {
		t.Error("строка из одних пробелов — не строка доступа")
	}
}

func TestМолчаниеПубликуетсяОдинРазИСнимаетсяОтветом(t *testing.T) {
	store := &памятьУстройств{}
	сейчас := момент
	часы := func() time.Time { return сейчас }
	молчит := false

	runner := NewRunner(NewService(store, часы),
		func(string) (Walker, error) {
			if молчит {
				return &FileWalker{}, nil
			}
			return обход(t, "192.168.4.172.walk"), nil
		},
		RunnerOptions{
			Targets:  []string{"192.168.4.172"},
			Interval: 30 * time.Minute, Silence: 2 * time.Hour, Now: часы,
		})

	runner.Tick(context.Background()) // ответил — первое состояние
	if len(store.outbox) != 1 {
		t.Fatalf("первое состояние: %d сообщений", len(store.outbox))
	}

	молчит = true
	сейчас = сейчас.Add(time.Hour)
	if st := runner.Tick(context.Background()); st.Failed != 1 || st.Silent != 0 {
		t.Errorf("час молчания — ещё не молчит: %+v", st)
	}

	сейчас = сейчас.Add(2 * time.Hour)
	if st := runner.Tick(context.Background()); st.Silent != 1 {
		t.Errorf("три часа молчания — молчит: %+v", st)
	}
	if len(store.outbox) != 2 || !strings.Contains(string(store.outbox[1].Payload), `"silent_since"`) {
		t.Fatalf("о молчании должно уйти одно сообщение с silent_since: %d", len(store.outbox))
	}

	сейчас = сейчас.Add(time.Hour)
	if st := runner.Tick(context.Background()); st.Silent != 0 {
		t.Errorf("о молчании сообщают один раз: %+v", st)
	}

	// Ответило — снимок тот же, но пометку надо снять новым сообщением.
	молчит = false
	сейчас = сейчас.Add(30 * time.Minute)
	runner.Tick(context.Background())
	if len(store.outbox) != 3 || strings.Contains(string(store.outbox[2].Payload), `"silent_since"`) {
		t.Errorf("возвращение должно уйти сообщением без silent_since: %d", len(store.outbox))
	}
}
