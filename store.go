package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// db es la unica conexion a SQLite. El servicio es un solo binario con un
// archivo al lado: respaldar es copiar ese archivo.
var db *sql.DB

var (
	ErrNotFound   = errors.New("no encontrado")
	ErrEmailTaken = errors.New("ese correo ya tiene cuenta")
)

type User struct {
	ID        int64
	Email     string
	PassHash  string
	CreatedAt int64
}

type Code struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"-"`
	Slug      string `json:"slug"`
	Kind      string `json:"kind"` // "static" | "dynamic"
	Name      string `json:"name"`
	Data      string `json:"data"`
	Design    string `json:"-"`
	Active    bool   `json:"active"`
	Scans     int64  `json:"scans"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

func openStore(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creando carpeta de datos: %w", err)
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	// SQLite escribe de a un proceso: una sola conexion evita los "database is locked".
	d.SetMaxOpenConns(1)
	d.SetConnMaxLifetime(time.Hour)
	if err := d.Ping(); err != nil {
		return err
	}
	db = d
	return migrate()
}

func migrate() error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS users (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	email      TEXT NOT NULL UNIQUE,
	pass_hash  TEXT NOT NULL,
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS codes (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	slug       TEXT NOT NULL UNIQUE,
	kind       TEXT NOT NULL,
	name       TEXT NOT NULL DEFAULT '',
	data       TEXT NOT NULL,
	design     TEXT NOT NULL DEFAULT '{}',
	active     INTEGER NOT NULL DEFAULT 1,
	scans      INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_codes_user ON codes(user_id, created_at DESC);
`)
	return err
}

// ---------- usuarios ----------

func createUser(email, passHash string) (*User, error) {
	now := time.Now().Unix()
	res, err := db.Exec(`INSERT INTO users (email, pass_hash, created_at) VALUES (?, ?, ?)`,
		email, passHash, now)
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Email: email, PassHash: passHash, CreatedAt: now}, nil
}

func userByEmail(email string) (*User, error) {
	row := db.QueryRow(`SELECT id, email, pass_hash, created_at FROM users WHERE email = ?`, email)
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.PassHash, &u.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func userByID(id int64) (*User, error) {
	row := db.QueryRow(`SELECT id, email, pass_hash, created_at FROM users WHERE id = ?`, id)
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.PassHash, &u.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// ---------- sesiones ----------

func saveSession(tokenHash string, userID int64, expiresAt int64) error {
	_, err := db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash, userID, expiresAt)
	return err
}

func userIDBySession(tokenHash string) (int64, error) {
	var userID, expires int64
	err := db.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if time.Now().Unix() > expires {
		db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
		return 0, ErrNotFound
	}
	return userID, nil
}

func deleteSession(tokenHash string) {
	db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
}

func purgeExpiredSessions() {
	db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
}

// ---------- codigos ----------

func createCode(c *Code) error {
	now := time.Now().Unix()
	c.CreatedAt, c.UpdatedAt = now, now
	res, err := db.Exec(`INSERT INTO codes (user_id, slug, kind, name, data, design, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.UserID, c.Slug, c.Kind, c.Name, c.Data, c.Design, boolToInt(c.Active), now, now)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	return nil
}

func codesByUser(userID int64) ([]Code, error) {
	rows, err := db.Query(`SELECT id, user_id, slug, kind, name, data, design, active, scans, created_at, updated_at
		FROM codes WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Code{}
	for rows.Next() {
		var c Code
		var active int
		if err := rows.Scan(&c.ID, &c.UserID, &c.Slug, &c.Kind, &c.Name, &c.Data, &c.Design,
			&active, &c.Scans, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Active = active == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

func codeByID(userID, id int64) (*Code, error) {
	row := db.QueryRow(`SELECT id, user_id, slug, kind, name, data, design, active, scans, created_at, updated_at
		FROM codes WHERE id = ? AND user_id = ?`, id, userID)
	return scanCode(row)
}

func codeBySlug(slug string) (*Code, error) {
	row := db.QueryRow(`SELECT id, user_id, slug, kind, name, data, design, active, scans, created_at, updated_at
		FROM codes WHERE slug = ?`, slug)
	return scanCode(row)
}

func scanCode(row *sql.Row) (*Code, error) {
	var c Code
	var active int
	err := row.Scan(&c.ID, &c.UserID, &c.Slug, &c.Kind, &c.Name, &c.Data, &c.Design,
		&active, &c.Scans, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Active = active == 1
	return &c, nil
}

func updateCode(userID, id int64, name, data string, active bool) error {
	res, err := db.Exec(`UPDATE codes SET name = ?, data = ?, active = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		name, data, boolToInt(active), time.Now().Unix(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func deleteCode(userID, id int64) error {
	res, err := db.Exec(`DELETE FROM codes WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func addScan(id int64) {
	db.Exec(`UPDATE codes SET scans = scans + 1 WHERE id = ?`, id)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isUniqueErr(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}
