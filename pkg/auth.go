package pkg

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"
)

type Auth struct {
	username string
	password string
	realm    string
}

func InitAuth(username, password, realm string) *Auth {
	auth := Auth{
		username: username,
		password: password,
		realm:    realm,
	}

	if auth.IsDisabled() {
		log.Print("authenticator disabled")
	}
	return &auth
}

// Copied from https://stackoverflow.com/a/39591234
func (auth *Auth) BasicAuth(nextHandler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var authenticated bool
		username, password, ok := r.BasicAuth()
		if ok {
			authenticated = auth.Authenticated(username, password)
		} else {
			// no credentials
			authenticated = auth.IsDisabled()
		}
		if !authenticated {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s"`, auth.realm))
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintln(w, "Unauthorized")
			return
		}

		nextHandler(w, r)
	}
}

// Returns true if the credentials are correct
func (auth *Auth) Authenticated(username, password string) bool {
	// return true if authentication is disabled
	if auth.IsDisabled() {
		return true
	}

	return username == auth.username && password == auth.password
}

// Just like the Authenticated function - except it expects the argument to be
// in the following format - Base64(username:password)
// Returns true if the credentials are correct
func (auth *Auth) Base64Authenticated(s string) bool {
	if auth.IsDisabled() {
		return true
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		log.Printf("error trying to decode Base64 string: %v", err)
		return false
	}
	decoded := string(data)
	colon := strings.Index(decoded, ":")
	if colon == -1 {
		return false
	}
	username := decoded[:colon]
	password := decoded[colon+1:]

	return username == auth.username && password == auth.password
}

func (auth *Auth) IsDisabled() bool {
	return auth.username == "" || auth.password == ""
}
