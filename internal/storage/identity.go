package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/broton07/inventory_service/internal/identity"
)

// lookupIdentifiers — кому принадлежат признаки из снимка и какие сильные
// признаки уже записаны за найденными машинами.
//
// Два запроса вместо одного соединения намеренно: первый отвечает на вопрос
// «знаем ли мы эти признаки», второй — «что мы знаем про найденные машины».
// Второй не выполняется вовсе, если первый ничего не нашёл, а это самый
// частый случай для нового парка.
func lookupIdentifiers(ctx context.Context, tx pgx.Tx, ids []identity.Identifier) (identity.Known, error) {
	known := identity.Known{
		Owner:  map[identity.Identifier]identity.HostID{},
		Strong: map[identity.HostID][]identity.Identifier{},
	}
	if len(ids) == 0 {
		return known, nil
	}

	kinds := make([]string, len(ids))
	values := make([]string, len(ids))
	for i, id := range ids {
		kinds[i] = string(id.Kind)
		values[i] = id.Value
	}

	rows, err := tx.Query(ctx, `
		SELECT kind, value, host_id
		  FROM host_identifiers
		 WHERE (kind, value) IN (SELECT * FROM unnest($1::text[], $2::text[]))`,
		kinds, values)
	if err != nil {
		return known, fmt.Errorf("поиск признаков: %w", err)
	}

	var hosts []uuid.UUID
	seen := map[uuid.UUID]bool{}

	for rows.Next() {
		var (
			kind, value string
			hostID      uuid.UUID
		)
		if err := rows.Scan(&kind, &value, &hostID); err != nil {
			rows.Close()
			return known, err
		}
		known.Owner[identity.Identifier{Kind: identity.Kind(kind), Value: value}] =
			identity.HostID(hostID.String())
		if !seen[hostID] {
			seen[hostID] = true
			hosts = append(hosts, hostID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return known, err
	}

	if len(hosts) == 0 {
		return known, nil
	}

	strongRows, err := tx.Query(ctx, `
		SELECT host_id, kind, value
		  FROM host_identifiers
		 WHERE host_id = ANY($1) AND kind <> 'mac'`, hosts)
	if err != nil {
		return known, fmt.Errorf("сильные признаки машин: %w", err)
	}
	defer strongRows.Close()

	for strongRows.Next() {
		var (
			hostID      uuid.UUID
			kind, value string
		)
		if err := strongRows.Scan(&hostID, &kind, &value); err != nil {
			return known, err
		}
		h := identity.HostID(hostID.String())
		known.Strong[h] = append(known.Strong[h],
			identity.Identifier{Kind: identity.Kind(kind), Value: value})
	}

	return known, strongRows.Err()
}

// attachIdentifiers закрепляет признаки за машиной.
//
// ON CONFLICT DO NOTHING, а не DO UPDATE: если признак уже принадлежит другой
// машине, отбирать его нельзя. Такой случай — противоречие, и решение по нему
// принимает identity.Decide до этого места; сюда попадают только признаки,
// которые ничьи.
func attachIdentifiers(
	ctx context.Context, tx pgx.Tx, hostID uuid.UUID, ids []identity.Identifier, now time.Time,
) error {
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `
			INSERT INTO host_identifiers (kind, value, host_id, first_seen_at, last_seen_at)
			VALUES ($1, $2, $3, $4, $4)
			ON CONFLICT (kind, value) DO UPDATE
			   SET last_seen_at = $4
			 WHERE host_identifiers.host_id = $3`,
			string(id.Kind), id.Value, hostID, now); err != nil {
			return fmt.Errorf("закрепление признака %s=%s: %w", id.Kind, id.Value, err)
		}
	}
	return nil
}

// createHost заводит машину.
func createHost(ctx context.Context, tx pgx.Tx, agentMark, hostname string, now time.Time) (uuid.UUID, error) {
	id := uuid.New()
	_, err := tx.Exec(ctx, `
		INSERT INTO hosts (id, agent_id, hostname, first_seen_at, last_report_at)
		VALUES ($1, $2, $3, $4, $4)`, id, agentMark, hostname, now)
	if err != nil {
		return uuid.Nil, fmt.Errorf("создание машины: %w", err)
	}
	return id, nil
}

// HostTouch — что стало с машиной, когда её отчёт приняли.
type HostTouch struct {
	// Returned — до этого отчёта машина числилась молчащей или выбывшей.
	Returned bool

	// FirstSeen — когда машину завели. Едет в ленту: потребитель показывает
	// человеку «в учёте с марта», и брать это ему больше неоткуда.
	FirstSeen time.Time
}

// touchHost двигает отметку «последний отчёт» и запоминает имя.
//
// Двигается и на повторе: агент выходил на связь — это факт, независимый от
// того, изменилось ли что-нибудь.
// Возвращает признак «машина вернулась»: до этого отчёта она числилась
// молчащей. Он нужен ленте — о возвращении надо рассказать так же, как о
// молчании, иначе машина, замолчавшая в отпуске владельца, останется у всех
// потребителей молчащей навсегда.
func touchHost(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, agentMark, hostname string, now time.Time) (HostTouch, error) {
	// silent_since сбрасывается здесь же: машина вышла на связь, и следующее
	// молчание должно снова стать событием. Без сброса машина, замолчавшая
	// однажды, больше никогда бы об этом не сообщила.
	//
	// retired_at сбрасывается по той же причине, но случай реже и неприятнее:
	// машину вывели из учёта, а она отчитывается — значит вывели по ошибке
	// либо агента на ней не сняли. Оставить отметку значило бы, что служба
	// считает машину выбывшей, а все потребители получают от неё свежие
	// сведения и считают живой. Отчёт — это доказательство, и оно сильнее
	// прежнего решения человека.
	// Прежнее значение silent_since читается подзапросом в FROM, а не
	// общим выражением в WITH: RETURNING отдаёт то, что стало, а нужно то,
	// что было.
	//
	// Через WITH это не работает, хотя и компилируется и проходит все тесты:
	// общее выражение блокирует ту же строку, которую меняет UPDATE, и
	// скалярный подзапрос в RETURNING получает пустой результат вместо
	// значения. Наружу это выходило как «cannot scan NULL into *bool» на
	// каждом отчёте существующей машины.
	var out HostTouch
	err := tx.QueryRow(ctx, `
		UPDATE hosts h
		   SET last_report_at = GREATEST(h.last_report_at, $2),
		       agent_id       = COALESCE(NULLIF($3, ''), h.agent_id),
		       hostname       = COALESCE(NULLIF($4, ''), h.hostname),
		       silent_since   = NULL,
		       retired_at     = NULL
		  FROM (SELECT id, silent_since, retired_at FROM hosts WHERE id = $1 FOR UPDATE) было
		 WHERE h.id = было.id
		 RETURNING было.silent_since IS NOT NULL OR было.retired_at IS NOT NULL,
		           h.first_seen_at`,
		hostID, now, agentMark, hostname).Scan(&out.Returned, &out.FirstSeen)
	if err != nil {
		return HostTouch{}, fmt.Errorf("обновление машины: %w", err)
	}
	return out, nil
}

// UnresolvedReport — отчёт, ожидающий разбора человеком.
type UnresolvedReport struct {
	ID         uuid.UUID
	ReceivedAt time.Time
	Reason     string
	Candidates []string
	Hostname   string
	Serial     string
	AgentID    string

	// Attempts и FirstSeenAt — сколько раз машина приходила, пока её не
	// разобрали, и когда пришла впервые. Разница между «пришла один раз
	// вчера» и «приходит каждые четыре часа третью неделю» — это разница
	// между случайностью и тем, чем надо заняться сегодня.
	Attempts    int
	FirstSeenAt time.Time
}

// saveUnresolved откладывает отчёт до разбора.
func saveUnresolved(
	ctx context.Context, tx pgx.Tx,
	agentUUID *uuid.UUID, reason string, candidates []identity.HostID,
	payload []byte, now time.Time,
) (uuid.UUID, int, error) {
	var attempts int

	list := make([]string, len(candidates))
	for i, c := range candidates {
		list[i] = string(c)
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		return uuid.Nil, 0, err
	}

	// Одна запись на машину, а не одна на отчёт.
	//
	// Неопознанная машина шлёт отчёты дальше — каждые четыре часа, — и без
	// схлопывания очередь разбора за неделю перестаёт быть списком дел.
	// Обновляется свежий снимок и счётчик: человеку нужно знать, что машина
	// приходила двадцать раз, а не двадцать её отчётов подряд.
	id := uuid.New()
	err = tx.QueryRow(ctx, `
		INSERT INTO unresolved_reports
		       (id, agent_uuid, received_at, first_seen_at, reason, candidates, payload)
		VALUES ($1, $2, $3, $3, $4, $5, $6)
		ON CONFLICT (agent_uuid) WHERE resolved_at IS NULL AND agent_uuid IS NOT NULL
		DO UPDATE SET received_at = EXCLUDED.received_at,
		              reason      = EXCLUDED.reason,
		              candidates  = EXCLUDED.candidates,
		              payload     = EXCLUDED.payload,
		              attempts    = unresolved_reports.attempts + 1
		RETURNING id, attempts`,
		id, agentUUID, now, reason, encoded, payload).Scan(&id, &attempts)
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("сохранение отчёта в очередь разбора: %w", err)
	}
	return id, attempts, nil
}

// ListUnresolved — очередь разбора, старые сверху: они ждут дольше всех.
func (p *Pool) ListUnresolved(ctx context.Context, limit int) ([]UnresolvedReport, error) {
	rows, err := p.Query(ctx, `
		SELECT id, received_at, reason, candidates,
		       COALESCE(payload -> 'machine' ->> 'hostname', ''),
		       COALESCE(payload -> 'machine' ->> 'serial', ''),
		       COALESCE(payload ->> 'agent_id', ''),
		       attempts, COALESCE(first_seen_at, received_at)
		  FROM unresolved_reports
		 WHERE resolved_at IS NULL
		 ORDER BY COALESCE(first_seen_at, received_at)
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("очередь разбора: %w", err)
	}
	defer rows.Close()

	var out []UnresolvedReport
	for rows.Next() {
		var (
			r          UnresolvedReport
			candidates []byte
		)
		if err := rows.Scan(&r.ID, &r.ReceivedAt, &r.Reason, &candidates,
			&r.Hostname, &r.Serial, &r.AgentID, &r.Attempts, &r.FirstSeenAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(candidates, &r.Candidates)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolveUnresolved привязывает отложенный отчёт к машине.
//
// Сам отчёт при этом НЕ применяется к данным машины: он уже устарел, а
// следующий придёт через несколько часов и придёт уже опознанным — признаки
// из него закреплены здесь. Применять старый снимок задним числом значило бы
// откатывать карточку к состоянию на момент, когда её никто не смотрел.
func (p *Pool) ResolveUnresolved(ctx context.Context, reportID, hostID uuid.UUID, by string, now time.Time) error {
	return pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		var payload []byte
		err := tx.QueryRow(ctx, `
			SELECT payload FROM unresolved_reports
			 WHERE id = $1 AND resolved_at IS NULL
			 FOR UPDATE`, reportID).Scan(&payload)
		if err != nil {
			return fmt.Errorf("отчёт не найден или уже разобран: %w", err)
		}

		_, ids, err := признакиОтчёта(payload)
		if err != nil {
			return err
		}

		if err := attachIdentifiers(ctx, tx, hostID, ids, now); err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE unresolved_reports
			   SET resolved_at = $2, resolved_host_id = $3, resolved_by = $4
			 WHERE id = $1`, reportID, now, hostID, by)
		return err
	})
}

// отложенныйОтчёт — то, что нужно от снимка при разборе вручную.
type отложенныйОтчёт struct {
	AgentID string `json:"agent_id"`
	Machine struct {
		Hostname string `json:"hostname"`
		Serial   string `json:"serial"`
		UUID     string `json:"uuid"`
	} `json:"machine"`
	Interfaces []struct {
		MAC     string `json:"mac"`
		Virtual bool   `json:"virtual"`
	} `json:"interfaces"`
}

// признакиОтчёта достаёт из отложенного снимка всё, чем машину можно будет
// находить дальше.
//
// Слабые признаки здесь берутся наравне с сильными, и это не противоречит
// инварианту I8: там речь о том, что АВТОМАТИКА не заводит машину по слабому
// признаку. Здесь решение принял человек — он и отвечает за то, что это та
// самая машина.
func признакиОтчёта(payload []byte) (отложенныйОтчёт, []identity.Identifier, error) {
	var snap отложенныйОтчёт
	if err := json.Unmarshal(payload, &snap); err != nil {
		return snap, nil, fmt.Errorf("разбор отложенного отчёта: %w", err)
	}

	var ids []identity.Identifier
	if snap.AgentID != "" {
		ids = append(ids, identity.Identifier{Kind: identity.KindAgent, Value: snap.AgentID})
	}
	if snap.Machine.Serial != "" {
		ids = append(ids, identity.Identifier{Kind: identity.KindSerial, Value: snap.Machine.Serial})
	}
	if snap.Machine.UUID != "" {
		ids = append(ids, identity.Identifier{Kind: identity.KindUUID, Value: snap.Machine.UUID})
	}
	for _, iface := range snap.Interfaces {
		if !iface.Virtual && iface.MAC != "" {
			ids = append(ids, identity.Identifier{Kind: identity.KindMAC, Value: iface.MAC})
		}
	}

	return snap, ids, nil
}

// AcceptUnresolvedAsNew заводит машину по отложенному отчёту.
//
// Без этой команды очередь разбора наполовину бесполезна. Отчёт машины, у
// которой нет ни заводского номера, ни SMBIOS UUID — а это весь сборный парк,
// — попадает в разбор при первом же появлении, и связать его НЕ С ЧЕМ:
// машины в базе ещё нет. Человек видит запись, которую нельзя ни принять, ни
// отклонить.
//
// Здесь он говорит «да, это новая машина», и дальше она опознаётся сама: её
// признаки, включая отметку агента, закрепляются за заведённой записью.
func (p *Pool) AcceptUnresolvedAsNew(
	ctx context.Context, reportID uuid.UUID, by string, now time.Time,
) (uuid.UUID, error) {
	var hostID uuid.UUID

	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		var payload []byte
		err := tx.QueryRow(ctx, `
			SELECT payload FROM unresolved_reports
			 WHERE id = $1 AND resolved_at IS NULL
			 FOR UPDATE`, reportID).Scan(&payload)
		if err != nil {
			return fmt.Errorf("отчёт не найден или уже разобран: %w", err)
		}

		snap, ids, err := признакиОтчёта(payload)
		if err != nil {
			return err
		}

		hostID, err = createHost(ctx, tx, snap.AgentID, snap.Machine.Hostname, now)
		if err != nil {
			return err
		}

		if err := attachIdentifiers(ctx, tx, hostID, ids, now); err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE unresolved_reports
			   SET resolved_at = $2, resolved_host_id = $3, resolved_by = $4
			 WHERE id = $1`, reportID, now, hostID, by)
		return err
	})

	if err != nil {
		return uuid.Nil, err
	}
	return hostID, nil
}

// FindUnresolvedByPrefix находит отложенный отчёт по началу идентификатора.
//
// Очередь разбора печатает восемь символов: полный UUID в таблице из шести
// колонок читать невозможно. Но связывать человек должен именно тот отчёт,
// который видит, — значит, восьми символов должно хватать и команде.
//
// Неоднозначность — ошибка, а не выбор первого попавшегося: связать не тот
// отчёт значит закрепить признаки одной машины за другой.
func (p *Pool) FindUnresolvedByPrefix(ctx context.Context, prefix string) (uuid.UUID, error) {
	rows, err := p.Query(ctx, `
		SELECT id FROM unresolved_reports
		 WHERE resolved_at IS NULL AND id::text LIKE $1 || '%'
		 LIMIT 2`, prefix)
	if err != nil {
		return uuid.Nil, fmt.Errorf("поиск отчёта: %w", err)
	}
	defer rows.Close()

	var found []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return uuid.Nil, err
		}
		found = append(found, id)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}

	switch len(found) {
	case 0:
		return uuid.Nil, fmt.Errorf("%w: отчёт %s не найден в очереди разбора", ErrNotFound, prefix)
	case 1:
		return found[0], nil
	default:
		return uuid.Nil, fmt.Errorf("под %s подходит несколько отчётов: укажите больше символов", prefix)
	}
}

// DismissUnresolved закрывает отложенный отчёт, не заводя машину.
//
// Оператор может ответить очереди разбора тремя способами, а не двумя: «это
// вот эта машина», «это новая машина» и «это вообще не машина парка». Без
// третьего ответа отчёт от чужого ноутбука, тестового контейнера или машины,
// которую уже списали, остаётся в очереди навсегда — а очередь, в которой
// навсегда что-то лежит, перестают открывать.
//
// Отчёт не удаляется: остаётся отметка, кто и почему его закрыл. Через месяц
// на вопрос «а куда делась та машина из очереди» есть ответ.
func (p *Pool) DismissUnresolved(ctx context.Context, reportID uuid.UUID, by, reason string, now time.Time) error {
	tag, err := p.Exec(ctx, `
		UPDATE unresolved_reports
		   SET resolved_at = $2,
		       resolved_by = $3,
		       reason      = reason || ' | закрыт без записи: ' || $4
		 WHERE id = $1 AND resolved_at IS NULL`, reportID, now, by, reason)
	if err != nil {
		return fmt.Errorf("закрытие отчёта: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DismissAllUnresolved закрывает всю очередь разбора разом.
//
// Нужно ровно в одном случае: очередь наполнена следами наладки, и разбирать
// её по одной записи — работа без смысла. В обычной жизни закрывают по одному
// и с причиной.
func (p *Pool) DismissAllUnresolved(ctx context.Context, by, reason string, now time.Time) (int64, error) {
	tag, err := p.Exec(ctx, `
		UPDATE unresolved_reports
		   SET resolved_at = $1,
		       resolved_by = $2,
		       reason      = reason || ' | закрыт без записи: ' || $3
		 WHERE resolved_at IS NULL`, now, by, reason)
	if err != nil {
		return 0, fmt.Errorf("закрытие очереди разбора: %w", err)
	}
	return tag.RowsAffected(), nil
}
