package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a user or session lookup fails.
var ErrNotFound = errors.New("not found")

const rfc3339 = time.RFC3339Nano

type store struct{ db *sql.DB }

const userCols = `id, username, email, role, provider, password_hash, disabled,
	must_change, created_at, updated_at`

func scanUser(s interface{ Scan(...any) error }) (User, error) {
	var (
		u                    User
		disabled, mustChange int
		createdAt, updatedAt string
	)
	if err := s.Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.Provider,
		&u.PasswordHash, &disabled, &mustChange, &createdAt, &updatedAt); err != nil {
		return User{}, err
	}
	u.Disabled = disabled != 0
	u.MustChange = mustChange != 0
	u.CreatedAt, _ = time.Parse(rfc3339, createdAt)
	u.UpdatedAt, _ = time.Parse(rfc3339, updatedAt)
	return u, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (st *store) insertUser(u User) error {
	_, err := st.db.Exec(`INSERT INTO users (`+userCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Username, u.Email, u.Role, u.Provider, u.PasswordHash,
		b2i(u.Disabled), b2i(u.MustChange), u.CreatedAt.Format(rfc3339), u.UpdatedAt.Format(rfc3339))
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (st *store) userByName(username string) (User, error) {
	row := st.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username = ?`, username)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (st *store) userByID(id string) (User, error) {
	row := st.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (st *store) listUsers() ([]User, error) {
	rows, err := st.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (st *store) countUsers() (int, error) {
	var n int
	err := st.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (st *store) updateUser(u User) error {
	u.UpdatedAt = time.Now()
	_, err := st.db.Exec(
		`UPDATE users SET email=?, role=?, provider=?, password_hash=?, disabled=?,
		 must_change=?, updated_at=? WHERE id=?`,
		u.Email, u.Role, u.Provider, u.PasswordHash, b2i(u.Disabled),
		b2i(u.MustChange), u.UpdatedAt.Format(rfc3339), u.ID)
	return err
}

func (st *store) deleteUser(id string) error {
	res, err := st.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, _ = st.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id)
	return nil
}

// --- sessions ---

func (st *store) insertSession(s Session, userAgent, ip string) error {
	_, err := st.db.Exec(
		`INSERT INTO sessions (token, user_id, created_at, expires_at, user_agent, ip)
		 VALUES (?,?,?,?,?,?)`,
		s.Token, s.UserID, s.CreatedAt.Format(rfc3339), s.ExpiresAt.Format(rfc3339), userAgent, ip)
	return err
}

func (st *store) sessionByToken(token string) (Session, error) {
	row := st.db.QueryRow(`SELECT token, user_id, created_at, expires_at FROM sessions WHERE token = ?`, token)
	var s Session
	var createdAt, expiresAt string
	if err := row.Scan(&s.Token, &s.UserID, &createdAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	s.CreatedAt, _ = time.Parse(rfc3339, createdAt)
	s.ExpiresAt, _ = time.Parse(rfc3339, expiresAt)
	return s, nil
}

func (st *store) deleteSession(token string) error {
	_, err := st.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (st *store) touchSession(token string, expires time.Time) error {
	_, err := st.db.Exec(`UPDATE sessions SET expires_at = ? WHERE token = ?`, expires.Format(rfc3339), token)
	return err
}

func (st *store) purgeExpired() {
	_, _ = st.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Format(rfc3339))
}
