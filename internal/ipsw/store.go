package ipsw

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const rfc3339 = time.RFC3339Nano

type store struct {
	db *sql.DB
}

const cols = `id, version, build, device, source_url, file_path, status,
	size, sha256, error, created_at, updated_at`

func scan(s interface{ Scan(...any) error }) (IPSW, error) {
	var it IPSW
	var createdAt, updatedAt string
	if err := s.Scan(
		&it.ID, &it.Version, &it.Build, &it.Device, &it.SourceURL, &it.FilePath,
		&it.Status, &it.Size, &it.SHA256, &it.Error, &createdAt, &updatedAt,
	); err != nil {
		return IPSW{}, err
	}
	var err error
	if it.CreatedAt, err = time.Parse(rfc3339, createdAt); err != nil {
		return IPSW{}, fmt.Errorf("parse created_at: %w", err)
	}
	if it.UpdatedAt, err = time.Parse(rfc3339, updatedAt); err != nil {
		return IPSW{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return it, nil
}

func (st *store) insert(it IPSW) error {
	_, err := st.db.Exec(
		`INSERT INTO ipsws (`+cols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.ID, it.Version, it.Build, it.Device, it.SourceURL, it.FilePath,
		it.Status, it.Size, it.SHA256, it.Error,
		it.CreatedAt.Format(rfc3339), it.UpdatedAt.Format(rfc3339),
	)
	if err != nil {
		return fmt.Errorf("insert ipsw: %w", err)
	}
	return nil
}

func (st *store) get(id string) (IPSW, error) {
	row := st.db.QueryRow(`SELECT `+cols+` FROM ipsws WHERE id = ?`, id)
	it, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return IPSW{}, ErrNotFound
	}
	if err != nil {
		return IPSW{}, fmt.Errorf("get ipsw: %w", err)
	}
	return it, nil
}

func (st *store) list() ([]IPSW, error) {
	rows, err := st.db.Query(`SELECT ` + cols + ` FROM ipsws ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list ipsws: %w", err)
	}
	defer rows.Close()
	out := make([]IPSW, 0)
	for rows.Next() {
		it, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (st *store) delete(id string) error {
	res, err := st.db.Exec(`DELETE FROM ipsws WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete ipsw: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// update applies mut to the entry and persists it, bumping updated_at.
func (st *store) update(id string, mut func(*IPSW)) error {
	it, err := st.get(id)
	if err != nil {
		return err
	}
	mut(&it)
	it.UpdatedAt = time.Now()
	_, err = st.db.Exec(
		`UPDATE ipsws SET version=?, build=?, device=?, source_url=?, file_path=?,
		 status=?, size=?, sha256=?, error=?, updated_at=? WHERE id=?`,
		it.Version, it.Build, it.Device, it.SourceURL, it.FilePath, it.Status,
		it.Size, it.SHA256, it.Error, it.UpdatedAt.Format(rfc3339), id,
	)
	if err != nil {
		return fmt.Errorf("update ipsw: %w", err)
	}
	return nil
}
