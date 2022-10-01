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
	"github.com/kwkoo/go-quiz/internal/messaging"
)

type RestApi struct {
	hub messaging.MessageHub
}

func InitRestApi(hub messaging.MessageHub) *RestApi {
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
			allQuizzes := api.getQuizzes()
			w.Header().Add("Content-Type", "application/json")
			enc := json.NewEncoder(w)
			if err := enc.Encode(allQuizzes); err != nil {
				log.Printf("error encoding slice of quizzes to JSON: %v", err)
				return
			}
			return
		}

		quiz, err := api.getQuiz(id)
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
		api.deleteQuiz(id)
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
			if err := api.addQuiz(q); err != nil {
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
		if err := api.addQuiz(toImport); err != nil {
			streamResponse(w, false, fmt.Sprintf("error adding quiz: %v", err))
			return
		}
		streamResponse(w, true, "")
		return
	}

	// update
	api.updateQuiz(toImport)
	streamResponse(w, true, "")
}

func (api *RestApi) ExtendSession(w http.ResponseWriter, r *http.Request) {
	id := lastPart(r.URL.Path)
	if len(id) == 0 {
		streamResponse(w, false, "invalid session id")
		return
	}
	api.extendSessionExpiry(id)
	streamResponse(w, true, "")
}

func (api *RestApi) Session(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if strings.HasSuffix(r.URL.Path, "/session") {
			// get all sessions
			all := api.getSessions()
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
		sessions := api.getSession(id)
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
		api.deleteSession(id)
		streamResponse(w, true, "")
		return
	}

	http.Error(w, "unsupported method", http.StatusNotImplemented)
}

func (api *RestApi) Game(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if strings.HasSuffix(r.URL.Path, "/game") {
			// get all games
			all := api.getGames()
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
		game, err := api.getGame(pin)
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

		game, err := api.getGame(pin)
		if err != nil {
			streamResponse(w, false, fmt.Sprintf("could not get game with pin %d: %v", pin, err))
			return
		}

		// remove players and host from game
		players := append(game.GetPlayers(), game.Host)
		api.removeGameFromSessions(players)
		api.sendClientsToScreen(players, "entrance")

		api.deleteGame(pin)
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
		api.updateGame(game)
		streamResponse(w, true, "")
		return
	}

	http.Error(w, "unsupported method", http.StatusNotImplemented)
}

func (api *RestApi) getQuizzes() []common.Quiz {
	c := make(chan []common.Quiz)
	api.hub.Send(messaging.QuizzesTopic, &common.GetQuizzesMessage{
		Result: c,
	})
	return <-c
}

func (api *RestApi) getQuiz(id int) (common.Quiz, error) {
	c := make(chan common.GetQuizResult)
	api.hub.Send(messaging.QuizzesTopic, &common.GetQuizMessage{
		Quizid: id,
		Result: c,
	})
	result := <-c
	return result.Quiz, result.Error
}

func (api *RestApi) deleteQuiz(id int) {
	api.hub.Send(messaging.QuizzesTopic, common.DeleteQuizMessage{Quizid: id})
}

func (api *RestApi) addQuiz(q common.Quiz) error {
	c := make(chan error)
	api.hub.Send(messaging.QuizzesTopic, &common.AddQuizMessage{
		Quiz:   q,
		Result: c,
	})
	return <-c
}

// used by the REST API
func (api *RestApi) updateQuiz(q common.Quiz) error {
	c := make(chan error)
	api.hub.Send(messaging.QuizzesTopic, &common.UpdateQuizMessage{
		Quiz:   q,
		Result: c,
	})
	return <-c
}

// used by the REST API
func (api *RestApi) extendSessionExpiry(id string) {
	api.hub.Send(messaging.SessionsTopic, common.ExtendSessionExpiryMessage{
		Sessionid: id,
	})
}

// used by the REST API
func (api *RestApi) getSessions() []common.Session {
	c := make(chan []common.Session)
	api.hub.Send(messaging.SessionsTopic, &common.GetSessionsMessage{
		Result: c,
	})
	return <-c
}

// used by the REST API
func (api *RestApi) getSession(id string) *common.Session {
	c := make(chan *common.Session)
	api.hub.Send(messaging.SessionsTopic, &common.GetSessionMessage{
		Sessionid: id,
		Result:    c,
	})
	return <-c
}

// used by the REST API
func (api *RestApi) deleteSession(id string) {
	api.hub.Send(messaging.SessionsTopic, common.DeleteSessionMessage{
		Sessionid: id,
	})
}

// used by the REST API
func (api *RestApi) getGames() []common.Game {
	c := make(chan []common.Game)
	api.hub.Send(messaging.GamesTopic, &common.GetGamesMessage{
		Result: c,
	})
	return <-c
}

// used by the REST API
func (api *RestApi) getGame(id int) (common.Game, error) {
	c := make(chan common.GetGameResult)
	api.hub.Send(messaging.GamesTopic, &common.GetGameMessage{
		Pin:    id,
		Result: c,
	})
	result := <-c
	return result.Game, result.Error
}

// used by the REST API
func (api *RestApi) deleteGame(id int) {
	api.hub.Send(messaging.GamesTopic, common.DeleteGameByPin{Pin: id})
}

// used by the REST API
func (api *RestApi) updateGame(g common.Game) {
	api.hub.Send(messaging.GamesTopic, g)
}

func (api *RestApi) removeGameFromSessions(sessionids []string) {
	api.hub.Send(messaging.SessionsTopic, common.DeregisterGameFromSessionsMessage{
		Sessions: sessionids,
	})
}

func (api *RestApi) sendClientsToScreen(sessionids []string, screen string) {
	for _, id := range sessionids {
		api.hub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
			Sessionid:  id,
			Nextscreen: screen,
		})
	}
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
