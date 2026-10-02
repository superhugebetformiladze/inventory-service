// Команда simulator изображает парк машин.
//
// Нужна не для красоты демонстрации: агент появится только на седьмом этапе, и
// без симулятора всё до него пришлось бы проверять на живых рабочих станциях —
// то есть не проверять вовсе. Он же потом станет инструментом нагрузочного
// прогона: утренний всплеск в тысячу отчётов воспроизводится здесь, а не
// ожиданием девяти утра.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/pkg/contract"
)

func main() {
	var (
		addr    = flag.String("addr", "http://localhost:8090", "адрес службы")
		hosts   = flag.Int("hosts", 3, "сколько машин изображать")
		rounds  = flag.Int("rounds", 1, "сколько отчётов с каждой машины; 0 — бесконечно")
		pause   = flag.Duration("pause", 0, "пауза между отчётами одной машины")
		jitter  = flag.Duration("jitter", 0, "случайный разброс старта машин")
		seed    = flag.Int64("seed", 1, "зерно генератора: одно зерно — один и тот же парк")
		change  = flag.Float64("change", 0, "доля отчётов, в которых что-нибудь меняется (0..1)")
		token   = flag.String("token", "", "токен регистрации: каждая машина обменяет его на свой ключ")
		verbose = flag.Bool("v", false, "печатать каждый отчёт")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Зерно фиксировано по умолчанию: парк должен быть одинаковым от запуска к
	// запуску, иначе «у меня воспроизвелось» ничего не значит. Криптостойкий
	// генератор здесь был бы прямо вреден — он лишил бы прогон повторяемости.
	//nolint:gosec // G404: воспроизводимость важнее непредсказуемости
	rng := rand.New(rand.NewSource(*seed))
	park := make([]contract.Snapshot, *hosts)
	for i := range park {
		park[i] = машина(rng, i)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	var wg sync.WaitGroup
	var mu sync.Mutex
	sent, failed := 0, 0

	for i := range park {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			if *jitter > 0 {
				select {
				case <-time.After(time.Duration(rng.Int63n(int64(*jitter)))):
				case <-ctx.Done():
					return
				}
			}

			snap := park[idx]

			key, err := зарегистрировать(ctx, client, *addr, *token, snap)
			if err != nil {
				mu.Lock()
				failed++
				fmt.Fprintf(os.Stderr, "%s: регистрация: %v\n", snap.Machine.Hostname, err)
				mu.Unlock()
				return
			}

			for round := 0; *rounds == 0 || round < *rounds; round++ {
				if ctx.Err() != nil {
					return
				}

				snap.CollectedAt = time.Now().UTC()
				if *change > 0 && rng.Float64() < *change {
					изменить(&snap, rng)
				}

				err := отправить(ctx, client, *addr, key, snap)

				mu.Lock()
				if err != nil {
					failed++
					fmt.Fprintf(os.Stderr, "%s: %v\n", snap.Machine.Hostname, err)
				} else {
					sent++
					if *verbose {
						fmt.Printf("%s отправлен\n", snap.Machine.Hostname)
					}
				}
				mu.Unlock()

				if *pause > 0 {
					select {
					case <-time.After(*pause):
					case <-ctx.Done():
						return
					}
				}
			}
		}(i)
	}

	wg.Wait()
	fmt.Printf("отправлено %d, не удалось %d\n", sent, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// зарегистрировать обменивает токен раскатки на персональный ключ машины.
//
// Симулятор проходит тот же путь, что настоящий агент: без ключа отчёт не
// примут, и проверять приём в обход регистрации значило бы проверять не то,
// что работает в жизни.
func зарегистрировать(ctx context.Context, client *http.Client, addr, token string, snap contract.Snapshot) (string, error) {
	if token == "" {
		return "", fmt.Errorf("не задан токен регистрации (-token)")
	}

	body, err := json.Marshal(map[string]string{
		"enrollment_token": token,
		"agent_id":         snap.AgentID,
		"hostname":         snap.Machine.Hostname,
		"agent_version":    snap.AgentVersion,
	})
	if err != nil {
		return "", err
	}

	// Повторы, как у настоящего агента.
	//
	// Симулятор существует ради того, чтобы вести себя как агент; без повторов
	// он проверяет не нагрузку на службу, а собственную нетерпеливость. Так и
	// вышло на первом прогоне: тысяча машин, служба отвечает «слишком часто»,
	// и симулятор сдаётся с первого раза — а агент бы повторил.
	var (
		resp   *http.Response
		answer struct {
			AgentKey string `json:"agent_key"`
			Error    string `json:"error"`
		}
	)

	for попытка := 0; ; попытка++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr+"/v1/enroll", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)
		if err != nil {
			return "", err
		}

		answer.AgentKey, answer.Error = "", ""
		_ = json.NewDecoder(resp.Body).Decode(&answer)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests || попытка >= 4 {
			break
		}

		//nolint:gosec // G404: разброс паузы не секрет
		пауза := time.Duration(попытка+1)*2*time.Second + time.Duration(rand.Int63n(int64(time.Second)))
		select {
		case <-time.After(пауза):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("служба ответила %d: %s", resp.StatusCode, answer.Error)
	}
	if answer.AgentKey == "" {
		return "", fmt.Errorf("служба не выдала ключ")
	}
	return answer.AgentKey, nil
}

func отправить(ctx context.Context, client *http.Client, addr, key string, snap contract.Snapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr+"/v1/reports", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		var answer map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&answer)
		return fmt.Errorf("служба ответила %d: %v", resp.StatusCode, answer["error"])
	}
	return nil
}

var (
	процессоры = []string{"Intel Core i5-12400", "Intel Core i3-10100", "AMD Ryzen 5 5600G", "Intel Core i7-8700"}
	системы    = []string{"Windows 10 Pro", "Windows 11 Pro", "Windows 7 Professional"}
	объёмы     = []int64{4, 8, 16, 32}
)

func машина(rng *rand.Rand, idx int) contract.Snapshot {
	const гб = int64(1) << 30

	return contract.Snapshot{
		SchemaVersion: contract.SchemaVersion,
		AgentID:       uuid.NewSHA1(uuid.NameSpaceDNS, fmt.Appendf(nil, "sim-%d", idx)).String(),
		AgentVersion:  "sim-0.1.0",
		Origin:        contract.OriginNetwork,
		Machine: contract.Machine{
			Hostname:     fmt.Sprintf("SIM-%03d", idx+1),
			Domain:       "zavod.local",
			Serial:       fmt.Sprintf("SIM%06d", rng.Intn(1_000_000)),
			Manufacturer: "Dell Inc.",
			Model:        "OptiPlex 3080",
			Chassis:      "desktop",
		},
		OS:  contract.OS{Name: системы[rng.Intn(len(системы))], Version: "10.0.19045", Architecture: "x64"},
		CPU: contract.CPU{Model: процессоры[rng.Intn(len(процессоры))], Cores: 4, Threads: 8, MHz: 2900},
		Memory: contract.Memory{
			TotalBytes: объёмы[rng.Intn(len(объёмы))] * гб,
		},
		Disks: []contract.Disk{{
			Model:     "Samsung SSD 870 EVO",
			Kind:      contract.DiskSSD,
			SizeBytes: 240 * гб,
			Interface: "SATA",
			Health:    "OK",
		}},
		Interfaces: []contract.Interface{{
			Name:       "Ethernet",
			MAC:        адрес(rng),
			IPv4:       []string{fmt.Sprintf("10.0.%d.%d", rng.Intn(30), 10+rng.Intn(200))},
			Gateway:    "10.0.0.1",
			DHCP:       true,
			SpeedMbits: 1000,
		}},
	}
}

// изменить вносит правдоподобное изменение: добавили память или поменяли адрес.
// Нужно для проверки диффа на пятом этапе — сейчас просто делает поток
// отчётов не полностью одинаковым.
func изменить(snap *contract.Snapshot, rng *rand.Rand) {
	const гб = int64(1) << 30

	switch rng.Intn(2) {
	case 0:
		snap.Memory.TotalBytes += 8 * гб
	case 1:
		if len(snap.Interfaces) > 0 {
			snap.Interfaces[0].IPv4 = []string{fmt.Sprintf("10.0.%d.%d", rng.Intn(30), 10+rng.Intn(200))}
		}
	}
}

func адрес(rng *rand.Rand) string {
	b := make([]byte, 6)
	_, _ = rng.Read(b) // из *rand.Rand читается без ошибок
	b[0] &^= 1         // не групповой адрес
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}
