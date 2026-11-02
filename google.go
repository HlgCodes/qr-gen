

package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// googleCertsURL es el JWKS publico de Google.
const googleCertsURL = "https://www.googleapis.com/oauth2/v3/certs"

type googleJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type googleJWKS struct {
	Keys []googleJWK `json:"keys"`
}

var (
	googleCertsCache   googleJWKS
	googleCertsFetched time.Time
	googleCertsMu      sync.Mutex
)

func fetchGoogleCerts() (googleJWKS, error) {
	googleCertsMu.Lock()
	defer googleCertsMu.Unlock()

	if time.Since(googleCertsFetched) < time.Hour && len(googleCertsCache.Keys) > 0 {
		return googleCertsCache, nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(googleCertsURL)
	if err != nil {
		return googleJWKS{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return googleJWKS{}, fmt.Errorf("google certs: status %d", resp.StatusCode)
	}
	var jwks googleJWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return googleJWKS{}, err
	}
	googleCertsCache = jwks
	googleCertsFetched = time.Now()
	return jwks, nil
}

func jwkToRSA(k googleJWK) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}

// verifyGoogleIDToken verifica el JWT firmado por Google y devuelve el email.
func verifyGoogleIDToken(token, clientID string) (email, sub string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", errors.New("token invalido")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", err
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", err
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", err
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return "", "", err
	}
	if header.Alg != "RS256" {
		return "", "", errors.New("algoritmo no soportado")
	}

	jwks, err := fetchGoogleCerts()
	if err != nil {
		return "", "", err
	}
	var key *googleJWK
	for i := range jwks.Keys {
		if jwks.Keys[i].Kid == header.Kid {
			key = &jwks.Keys[i]
			break
		}
	}
	if key == nil {
		// refrescar cache por si rotaron
		googleCertsFetched = time.Time{}
		jwks, err = fetchGoogleCerts()
		if err != nil {
			return "", "", err
		}
		for i := range jwks.Keys {
			if jwks.Keys[i].Kid == header.Kid {
				key = &jwks.Keys[i]
				break
			}
		}
	}
	if key == nil {
		return "", "", errors.New("clave no encontrada")
	}

	pub, err := jwkToRSA(*key)
	if err != nil {
		return "", "", err
	}

	// verificar firma
	h := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sigBytes); err != nil {
		return "", "", errors.New("firma invalida")
	}

	var claims struct {
		Iss           string `json:"iss"`
		Aud           string `json:"aud"`
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Exp           int64  `json:"exp"`
		Iat           int64  `json:"iat"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", "", err
	}

	if claims.Iss != "https://accounts.google.com" && claims.Iss != "accounts.google.com" {
		return "", "", errors.New("emisor invalido")
	}
	if claims.Aud != clientID {
		return "", "", errors.New("audiencia invalida")
	}
	if !claims.EmailVerified {
		return "", "", errors.New("correo no verificado por Google")
	}
	if time.Now().Unix() > claims.Exp {
		return "", "", errors.New("token expirado")
	}
	if claims.Email == "" || claims.Sub == "" {
		return "", "", errors.New("claims incompletos")
	}
	return claims.Email, claims.Sub, nil
}

// handleGoogleLogin procesa el ID token de Google.
func handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	clientID := strings.TrimSpace(envOr("GOOGLE_CLIENT_ID", ""))
	if clientID == "" {
		writeErr(w, http.StatusServiceUnavailable, "Google no esta configurado en este servidor")
		return
	}

	var in struct {
		Credential string `json:"credential"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in); err != nil || in.Credential == "" {
		writeErr(w, http.StatusBadRequest, "falta el token de Google")
		return
	}

	email, sub, err := verifyGoogleIDToken(in.Credential, clientID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "no se pudo verificar tu cuenta de Google")
		return
	}
	email = strings.ToLower(strings.TrimSpace(email))

	// buscar usuario existente
	u, err := userByGoogleSub(sub)
	if err != nil {
		// intentar por email (cuenta creada con password)
		u2, err2 := userByEmail(email)
		if err2 == nil {
			// vincular google_sub a la cuenta existente
			db.Exec(`UPDATE users SET google_sub = ?, provider = 'google' WHERE id = ?`, sub, u2.ID)
			u2.GoogleSub = sub
			u2.Provider = "google"
			u = u2
		} else {
			// crear nuevo usuario sin password
			u, err = createUser(email, "", "google", sub)
			if err != nil {
				if errors.Is(err, ErrEmailTaken) {
					// carrera: reintentar por email
					u2, err2 := userByEmail(email)
					if err2 != nil {
						writeErr(w, http.StatusInternalServerError, "no se pudo crear la cuenta")
						return
					}
					u = u2
				} else {
					writeErr(w, http.StatusInternalServerError, "no se pudo crear la cuenta")
					return
				}
			}
		}
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

// handleConfig expone configuracion publica del frontend (client id de Google).
func handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"googleClientId": strings.TrimSpace(envOr("GOOGLE_CLIENT_ID", "")),
	})
}
