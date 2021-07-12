package pkg

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

type RestApi struct {
	hub *Hub
}

func InitRestApi(hub *Hub) *RestApi {
	return &RestApi{hub: hub}
}

func (api *RestApi) Quizzes(w http.ResponseWriter, r *http.Request) {
	// export
	if r.Method == http.MethodGet {
		allQuizzes := api.hub.quizzes.GetQuizzes()
		w.Header().Add("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		if err := enc.Encode(allQuizzes); err != nil {
			log.Printf("error encoding slice of quizzes to JSON: %v", err)
			return
		}
		return
	}

	// import
	toImport, err := UnmarshalQuizzes(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("error parsing JSON: %v", err), http.StatusInternalServerError)
		return
	}
	for _, q := range toImport {
		if _, err := api.hub.quizzes.Add(q); err != nil {
			log.Printf("error adding quiz: %v", err)
			continue
		}
	}
	fmt.Fprintf(w, "OK")
}
