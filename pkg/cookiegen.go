package pkg

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
	cookie, err := r.Cookie(cookieKey)
	if err != nil {
		id, _ := uuid.NewRandom()
		cookie = &http.Cookie{
			Name:  cookieKey,
			Value: id.String(),
			Path:  "/",
		}
		http.SetCookie(w, cookie)
		log.Printf("generated new cookie %s", cookie)
	} else {
		log.Printf("got cookie %s", cookie)
	}
	s.next(w, r)
}
