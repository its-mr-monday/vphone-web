package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a job lookup fails.
var ErrNotFound = errors.New("job not found")

const rfc3339 = time.RFC3339Nano

type store struct {
	db *sql.DB
}

const jobColumns = `id, vm_id, ipsw_id, type, label, status, exit_code,
	error, output, created_at, started_at, finished_at`

func scanJob(s interface{ Scan(...any) error }) (Job, error) {
	var (
		j                                Job
		vmID, ipswID                     sql.NullString
		exitCode                         sql.NullInt64
		createdAt                        string
		startedAt, finishedAt            sql.NullString
	)
	if err := s.Scan(
		&j.ID, &vmID, &ipswID, &j.Type, &j.Label, &j.Status, &exitCode,
		&j.Error, &j.Output, &createdAt, &startedAt, &finishedAt,
	); err != nil {
		return Job{}, err
	}
	j.VMID = vmID.String
	j.IPSWID = ipswID.String
	if exitCode.Valid {
		code := int(exitCode.Int64)
		j.ExitCode = &code
	}
	var err error
	if j.CreatedAt, err = time.Parse(rfc3339, createdAt); err != nil {
		return Job{}, fmt.Errorf("parse created_at: %w", err)
	}
	if startedAt.Valid {
		t, err := time.Parse(rfc3339, startedAt.String)
		if err != nil {
			return Job{}, fmt.Errorf("parse started_at: %w", err)
		}
		j.StartedAt = &t
	}
	if finishedAt.Valid {
		t, err := time.Parse(rfc3339, finishedAt.String)
		if err != nil {
			return Job{}, fmt.Errorf("parse finished_at: %w", err)
		}
		j.FinishedAt = &t
	}
	return j, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (st *store) insert(j Job) error {
	_, err := st.db.Exec(
		`INSERT INTO jobs (`+jobColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, nullStr(j.VMID), nullStr(j.IPSWID), j.Type, j.Label, j.Status,
		nil, j.Error, j.Output, j.CreatedAt.Format(rfc3339), nil, nil,
	)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}
	return nil
}

func (st *store) get(id string) (Job, error) {
	row := st.db.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("get job: %w", err)
	}
	return j, nil
}

// list returns recent jobs, newest first, optionally filtered by vm_id.
func (st *store) list(vmID string, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if vmID != "" {
		rows, err = st.db.Query(
			`SELECT `+jobColumns+` FROM jobs WHERE vm_id = ? ORDER BY created_at DESC LIMIT ?`,
			vmID, limit)
	} else {
		rows, err = st.db.Query(
			`SELECT `+jobColumns+` FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	out := make([]Job, 0)
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (st *store) markStarted(id string, at time.Time) error {
	_, err := st.db.Exec(
		`UPDATE jobs SET status = ?, started_at = ? WHERE id = ?`,
		StatusRunning, at.Format(rfc3339), id)
	return err
}

func (st *store) markTerminal(id string, status Status, exitCode *int, errMsg, output string, at time.Time) error {
	var code any
	if exitCode != nil {
		code = *exitCode
	}
	_, err := st.db.Exec(
		`UPDATE jobs SET status = ?, exit_code = ?, error = ?, output = ?, finished_at = ? WHERE id = ?`,
		status, code, errMsg, output, at.Format(rfc3339), id)
	return err
}

func (st *store) setStatus(id string, status Status) error {
	_, err := st.db.Exec(`UPDATE jobs SET status = ? WHERE id = ?`, status, id)
	return err
}

// reconcileStale marks any PENDING/RUNNING jobs as FAILED on startup, since
// their in-memory run closures did not survive the restart.
func (st *store) reconcileStale() (int, error) {
	res, err := st.db.Exec(
		`UPDATE jobs SET status = ?, error = ?, finished_at = ?
		 WHERE status IN (?, ?)`,
		StatusFailed, "interrupted by server restart", time.Now().Format(rfc3339),
		StatusPending, StatusRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
