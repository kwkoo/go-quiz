package internal

import (
	"log"
	"net/http"

	"github.com/google/uuid"
)

const cookieKey = "quizsession"

type CookieGenerator struct {
	next func(w http.ResponseWriter, r *http.Request)
}

func InitCookieGenerator(next func(w http.ResponseWriter, r *http.Request)) *CookieGenerator {
	return &CookieGenerator{next: next}
}

func (s CookieGenerator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// copied from https://medium.com/wesionary-team/cookies-and-session-management-using-cookies-in-go-7801f935a1c8
	if _, err := r.Cookie(cookieKey); err != nil {
		id, _ := uuid.NewRandom()
		cookie := &http.Cookie{
			Name:  cookieKey,
			Value: id.String(),
			Path:  "/",
		}
		log.Printf("cookie not found - generating new cookie %s", id)
		http.SetCookie(w, cookie)
	}
	s.next(w, r)
}
