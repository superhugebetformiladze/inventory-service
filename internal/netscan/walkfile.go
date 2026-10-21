package netscan

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FileWalker — обход, снятый заранее командой snmpwalk.
//
// Нужен, пока у службы нет сетевого доступа к коммутаторам: обход снимают с
// машины, у которой доступ есть, и отдают службе файлом. Логика сбора при
// этом та же, что у живого опроса, — проверяется она на настоящих ответах
// настоящих коммутаторов, а не на выдуманных.
//
// Ожидаемый вид — вывод `snmpwalk -On -Ox`: числовые OID и строки октетов
// шестнадцатеричными байтами. Без -Ox snmpwalk печатает «читаемые» строки
// текстом, и MAC-адрес, случайно состоящий из печатных байтов, приходит
// мусором. Такие строки тоже принимаются, но MAC-адресам в них верить нельзя.
type FileWalker struct {
	vars []Variable
}

// lineStart — начало новой переменной: «.1.3.6… = ТИП: значение».
var lineStart = regexp.MustCompile(`^\.?(\d+(?:\.\d+)*)\s+=\s+(.*)$`)

// ParseWalk читает вывод snmpwalk.
func ParseWalk(r io.Reader) (*FileWalker, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var (
		vars    []Variable
		oid     string
		value   strings.Builder
		lineNum int
	)

	flush := func() error {
		if oid == "" {
			return nil
		}
		v, err := parseValue(oid, value.String())
		if err != nil {
			return fmt.Errorf("строка %d: %w", lineNum, err)
		}
		vars = append(vars, v)
		oid = ""
		value.Reset()
		return nil
	}

	for scanner.Scan() {
		lineNum++
		line := strings.TrimRight(scanner.Text(), "\r")

		if m := lineStart.FindStringSubmatch(line); m != nil {
			if err := flush(); err != nil {
				return nil, err
			}
			oid = m[1]
			value.WriteString(m[2])
			continue
		}

		// Продолжение предыдущего значения: длинные Hex-STRING snmpwalk
		// переносит на следующую строку.
		if oid != "" {
			value.WriteByte('\n')
			value.WriteString(line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("чтение обхода: %w", err)
	}
	if err := flush(); err != nil {
		return nil, err
	}

	sort.SliceStable(vars, func(i, j int) bool { return lessOID(vars[i].OID, vars[j].OID) })
	return &FileWalker{vars: vars}, nil
}

// Len — сколько переменных в файле.
func (w *FileWalker) Len() int { return len(w.vars) }

// Walk отдаёт переменные поддерева по порядку.
func (w *FileWalker) Walk(ctx context.Context, root string, fn func(Variable) error) error {
	for _, v := range w.vars {
		if err := ctx.Err(); err != nil {
			return err
		}
		if v.OID != root && !strings.HasPrefix(v.OID, root+".") {
			continue
		}
		if v.Kind == KindNull {
			continue
		}
		if err := fn(v); err != nil {
			return err
		}
	}
	return nil
}

// parseValue разбирает «ТИП: значение».
func parseValue(oid, raw string) (Variable, error) {
	v := Variable{OID: oid}

	kind, rest, found := strings.Cut(raw, ":")
	if !found {
		// «No Such Object available on this agent at this OID» и подобное.
		v.Kind = KindNull
		return v, nil
	}
	kind = strings.TrimSpace(kind)
	rest = strings.TrimSpace(rest)

	switch kind {
	case "Hex-STRING":
		b, err := hex.DecodeString(strings.Join(strings.Fields(rest), ""))
		if err != nil {
			return v, fmt.Errorf("%s: Hex-STRING не разбирается: %w", oid, err)
		}
		v.Kind, v.Bytes = KindBytes, b
	case "STRING":
		v.Kind, v.Bytes = KindBytes, []byte(unquote(rest))
	case "INTEGER", "Gauge32", "Counter32", "Counter64", "UInteger32", "Unsigned32":
		n, err := parseInt(rest)
		if err != nil {
			return v, fmt.Errorf("%s: %w", oid, err)
		}
		v.Kind, v.Int = KindInt, n
	case "Timeticks":
		// «(12345) 0:02:03.45» — число в скобках.
		n, err := parseInt(rest)
		if err != nil {
			return v, fmt.Errorf("%s: %w", oid, err)
		}
		v.Kind, v.Int = KindInt, n
	case "OID":
		v.Kind, v.Text = KindOID, strings.TrimPrefix(rest, ".")
	case "IpAddress", "Network Address":
		v.Kind, v.Text = KindIP, rest
	default:
		v.Kind = KindNull
	}
	return v, nil
}

// intInParens — «up(1)», «(12345) 0:02:03.45»: число в скобках главнее имени.
var intInParens = regexp.MustCompile(`\((-?\d+)\)`)

func parseInt(s string) (int64, error) {
	if m := intInParens.FindStringSubmatch(s); m != nil {
		return strconv.ParseInt(m[1], 10, 64)
	}
	field := strings.Fields(s)
	if len(field) == 0 {
		return 0, fmt.Errorf("пустое число")
	}
	return strconv.ParseInt(field[0], 10, 64)
}

func unquote(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return s[1 : len(s)-1]
	}
	return s
}

// lessOID сравнивает OID по числам, а не строкой: 1.10 идёт после 1.9.
func lessOID(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, errA := strconv.Atoi(pa[i])
		nb, errB := strconv.Atoi(pb[i])
		if errA != nil || errB != nil {
			if pa[i] != pb[i] {
				return pa[i] < pb[i]
			}
			continue
		}
		if na != nb {
			return na < nb
		}
	}
	return len(pa) < len(pb)
}
