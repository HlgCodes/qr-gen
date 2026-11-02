package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	sessionCookie = "qr_sesion"
	sessionTTL    = 30 * 24 * time.Hour
	pbkdf2Iter    = 600_000
)

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[a-zA-Z]{2,}$`)

// ---------- contraseñas ----------

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s",
		pbkdf2Iter,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func checkPassword(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	var iter int
	if _, err := fmt.Sscanf(parts[1], "%d", &iter); err != nil || iter < 1 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---------- sesiones ----------

func newSessionToken() (token, tokenHash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
		MaxAge:   -1,
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// currentUser devuelve el usuario de la cookie de sesión, o nil.
func currentUser(r *http.Request) *User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	userID, err := userIDBySession(hashToken(c.Value))
	if err != nil {
		return nil
	}
	u, err := userByID(userID)
	if err != nil {
		return nil
	}
	return u
}

// ---------- handlers ----------

type credenciales struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func handleRegistro(w http.ResponseWriter, r *http.Request) {
	var in credenciales
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "datos invalidos")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !emailRe.MatchString(email) || len(email) > 254 {
		writeErr(w, http.StatusBadRequest, "escribe un correo valido")
		return
	}
	if len(in.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "la contraseña necesita al menos 8 caracteres")
		return
	}
	if len(in.Password) > 200 {
		writeErr(w, http.StatusBadRequest, "contraseña demasiado larga")
		return
	}

	hash, err := hashPassword(in.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudo crear la cuenta")
		return
	}
	u, err := createUser(email, hash, "password", "")
	if err != nil {
		if errors.Is(err, ErrEmailTaken) {
			writeErr(w, http.StatusConflict, "ese correo ya tiene cuenta")
			return
		}
		writeErr(w, http.StatusInternalServerError, "no se pudo crear la cuenta")
		return
	}

	token, tokenHash, err := newSessionToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudo iniciar la sesión")
		return
	}
	if err := saveSession(tokenHash, u.ID, time.Now().Add(sessionTTL).Unix()); err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudo iniciar la sesión")
		return
	}
	setSessionCookie(w, r, token)
	writeJSON(w, http.StatusCreated, map[string]any{"email": u.Email})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var in credenciales
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "datos invalidos")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	u, err := userByEmail(email)
	if err != nil || !checkPassword(u.PassHash, in.Password) {
		// mismo mensaje en los dos casos: no revelamos si el correo existe
		writeErr(w, http.StatusUnauthorized, "correo o contraseña incorrectos")
		return
	}

	token, tokenHash, err := newSessionToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudo iniciar la sesión")
		return
	}
	if err := saveSession(tokenHash, u.ID, time.Now().Add(sessionTTL).Unix()); err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudo iniciar la sesión")
		return
	}
	setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{"email": u.Email})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		deleteSession(hashToken(c.Value))
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func handleMe(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeJSON(w, http.StatusOK, map[string]any{"user": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{"email": u.Email, "createdAt": u.CreatedAt},
	})
}
