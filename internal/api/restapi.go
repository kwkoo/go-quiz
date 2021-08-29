package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/kwkoo/go-quiz/internal/common"
)

type QuizApp interface {
	GetQuizzes() []common.Quiz
	GetQuiz(int) (common.Quiz, error)
	DeleteQuiz(int)
	AddQuiz(common.Quiz) (common.Quiz, error)
	UpdateQuiz(common.Quiz) error
	ExtendSessionExpiry(string)
	GetSessions() []common.Session
	GetSession(string) *common.Session
	DeleteSession(string)
	GetGames() []common.Game
	GetGame(int) (common.Game, error)
	DeleteGame(int)
	UpdateGame(common.Game) error
	RemoveGameFromSessions([]string)
	SendClientsToScreen([]string, string)
}

type RestApi struct {
	hub QuizApp
}

func InitRestApi(hub QuizApp) *RestApi {
	return &RestApi{hub: hub}
}

func (api *RestApi) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/quiz") {
		api.Quiz(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/session") {
		api.Session(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/extendsession/") {
		api.ExtendSession(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/game") {
		api.Game(w, r)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

func (api *RestApi) Quiz(w http.ResponseWriter, r *http.Request) {
	// export
	if r.Method == http.MethodGet {
		last := lastPart(r.URL.Path)
		id, err := strconv.Atoi(last)
		if err != nil {
			allQuizzes := api.hub.GetQuizzes()
			w.Header().Add("Content-Type", "application/json")
			enc := json.NewEncoder(w)
			if err := enc.Encode(allQuizzes); err != nil {
				log.Printf("error encoding slice of quizzes to JSON: %v", err)
				return
			}
			return
		}

		quiz, err := api.hub.GetQuiz(id)
		if err != nil {
			streamResponse(w, false, fmt.Sprintf("quiz %d does not exist", id))
			return
		}

		w.Header().Add("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		if err := enc.Encode(quiz); err != nil {
			streamResponse(w, false, fmt.Sprintf("error encoding quiz to JSON: %v", err))
			return
		}
		return
	}

	if r.Method == http.MethodDelete {
		last := lastPart(r.URL.Path)
		id, err := strconv.Atoi(last)
		if err != nil {
			streamResponse(w, false, fmt.Sprintf("invalid id %s: %v", last, err))
			return
		}
		api.hub.DeleteQuiz(id)
		streamResponse(w, true, "")
		return
	}

	// import
	defer r.Body.Close()

	// check to see if it's bulk import
	if strings.HasSuffix(r.URL.Path, "/bulk") {
		toImport, err := common.UnmarshalQuizzes(r.Body)
		if err != nil {
			streamResponse(w, false, fmt.Sprintf("error parsing JSON: %v", err))
			return
		}
		for _, q := range toImport {
			if _, err := api.hub.AddQuiz(q); err != nil {
				streamResponse(w, false, fmt.Sprintf("error adding quiz: %v", err))
				continue
			}
		}
		streamResponse(w, true, "")
		return
	}

	// we're importing a single quiz
	toImport, err := common.UnmarshalQuiz(r.Body)
	if err != nil {
		streamResponse(w, false, fmt.Sprintf("error parsing JSON: %v", err))
		return
	}

	if toImport.Id == 0 {
		// no ID, so treat this as an add operation
		if _, err := api.hub.AddQuiz(toImport); err != nil {
			streamResponse(w, false, fmt.Sprintf("error adding quiz: %v", err))
			return
		}
		streamResponse(w, true, "")
		return
	}

	// update
	if err := api.hub.UpdateQuiz(toImport); err != nil {
		streamResponse(w, false, fmt.Sprintf("error updating quiz: %v", err))
		return
	}
	streamResponse(w, true, "")
}

func (api *RestApi) ExtendSession(w http.ResponseWriter, r *http.Request) {
	id := lastPart(r.URL.Path)
	if len(id) == 0 {
		streamResponse(w, false, "invalid session id")
		return
	}
	api.hub.ExtendSessionExpiry(id)
	streamResponse(w, true, "")
}

func (api *RestApi) Session(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if strings.HasSuffix(r.URL.Path, "/session") {
			// get all sessions
			all := api.hub.GetSessions()
			w.Header().Add("Content-Type", "application/json")
			enc := json.NewEncoder(w)
			if err := enc.Encode(all); err != nil {
				log.Printf("error encoding slice of quizzes to JSON: %v", err)
			}
			return
		}

		id := lastPart(r.URL.Path)
		if len(id) == 0 {
			streamResponse(w, false, "invalid session id")
			return
		}
		sessions := api.hub.GetSession(id)
		if sessions == nil {
			streamResponse(w, false, fmt.Sprintf("invalid session id %s", id))
			return
		}
		enc := json.NewEncoder(w)
		if err := enc.Encode(sessions); err != nil {
			log.Printf("error encoding session %s: %v", id, err)
			return
		}
		return
	}

	if r.Method == http.MethodDelete {
		id := lastPart(r.URL.Path)
		if len(id) == 0 {
			streamResponse(w, false, "invalid session id")
			return
		}
		api.hub.DeleteSession(id)
		streamResponse(w, true, "")
		return
	}

	http.Error(w, "unsupported method", http.StatusNotImplemented)
}

func (api *RestApi) Game(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if strings.HasSuffix(r.URL.Path, "/game") {
			// get all games
			all := api.hub.GetGames()
			w.Header().Add("Content-Type", "application/json")
			enc := json.NewEncoder(w)
			if err := enc.Encode(all); err != nil {
				log.Printf("error encoding slice of games to JSON: %v", err)
			}
			return
		}

		last := lastPart(r.URL.Path)
		if len(last) == 0 {
			streamResponse(w, false, "invalid game id")
			return
		}
		pin, err := strconv.Atoi(last)
		if err != nil {
			streamResponse(w, false, fmt.Sprintf("invalid game id %s: %v", last, err))
			return
		}
		game, err := api.hub.GetGame(pin)
		if err != nil {
			streamResponse(w, false, fmt.Sprintf("error getting game %d: %v", pin, err))
			return
		}
		enc := json.NewEncoder(w)
		if err := enc.Encode(&game); err != nil {
			log.Printf("error encoding game to JSON: %v", err)
			return
		}
		return
	}

	if r.Method == http.MethodDelete {
		last := lastPart(r.URL.Path)
		if len(last) == 0 {
			streamResponse(w, false, "invalid game id")
			return
		}
		pin, err := strconv.Atoi(last)
		if err != nil {
			streamResponse(w, false, fmt.Sprintf("invalid game id %s: %v", last, err))
			return
		}

		game, err := api.hub.GetGame(pin)
		if err != nil {
			streamResponse(w, false, fmt.Sprintf("could not get game with pin %d: %v", pin, err))
			return
		}

		// remove players and host from game
		players := append(game.GetPlayers(), game.Host)
		api.hub.RemoveGameFromSessions(players)
		api.hub.SendClientsToScreen(players, "entrance")

		api.hub.DeleteGame(pin)
		streamResponse(w, true, "")
		return
	}

	if r.Method == http.MethodPut {
		defer r.Body.Close()
		dec := json.NewDecoder(r.Body)
		var game common.Game
		if err := dec.Decode(&game); err != nil {
			streamResponse(w, false, fmt.Sprintf("error decoding game JSON: %v", err))
			return
		}
		if err := api.hub.UpdateGame(game); err != nil {
			streamResponse(w, false, fmt.Sprintf("error updating game: %v", err))
			return
		}
		streamResponse(w, true, "")
		return
	}

	http.Error(w, "unsupported method", http.StatusNotImplemented)
}

// returns the part beyond the last slash in the URL
func lastPart(s string) string {
	last := strings.LastIndex(s, "/")
	if last == -1 {
		return s
	}
	return s[last+1:]
}

func streamResponse(w io.Writer, success bool, errMsg string) {
	resp := struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}{
		Success: success,
		Error:   errMsg,
	}
	json.NewEncoder(w).Encode(&resp)
}
