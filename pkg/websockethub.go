// Copied from https://github.com/gorilla/websocket/blob/master/examples/chat/hub.go
// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
)

// Hub maintains the set of active clients and broadcasts messages to the
// clients.
type Hub struct {
	// Registered clients.
	clients map[*Client]bool

	// Inbound messages from the clients.
	incomingcommands chan *ClientCommand

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client

	sessions *Sessions

	quizzes *Quizzes

	games *Games
}

func NewHub(redisHost, redisPassword string, auth *Auth, sessionTimeout int) *Hub {
	persistenceEngine := InitRedis(redisHost, redisPassword, GetShutdownArtifacts())
	quizzes, err := InitQuizzes(persistenceEngine)
	if err != nil {
		log.Fatal(err)
	}

	return &Hub{
		incomingcommands: make(chan *ClientCommand),
		register:         make(chan *Client),
		unregister:       make(chan *Client),
		clients:          make(map[*Client]bool),
		sessions:         InitSessions(persistenceEngine, auth, sessionTimeout, GetShutdownArtifacts()),
		quizzes:          quizzes,
		games:            InitGames(persistenceEngine),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				if len(client.sessionid) > 0 {
					h.sessions.UpdateClientForSession(client.sessionid, nil)
				}
			}
		case message := <-h.incomingcommands:
			log.Printf("incoming command: %s, arg: %s", message.cmd, message.arg)
			h.processMessage(message)
		}
	}
}

func (h *Hub) processMessage(m *ClientCommand) {
	log.Printf("cmd=%s, arg=%s", m.cmd, m.arg)

	if len(m.client.sessionid) == 0 {
		// client hasn't identified themselves yet
		if m.cmd == "session" {
			if len(m.arg) == 0 || len(m.arg) > 64 {
				h.errorMessageToClient(m.client, "invalid session ID", "entrance")
				return
			}
			m.client.sessionid = m.arg
			session := h.sessions.GetSession(m.client.sessionid)
			if session == nil {
				session = h.sessions.NewSession(m.client.sessionid, m.client, "entrance")
			} else {
				if session.Client != nil {
					m.client.sessionid = ""
					h.errorMessageToClient(m.client, "you have another active session - disconnect that session before reconnecting", "")
					return
				}
				h.sessions.UpdateClientForSession(session.Id, m.client)
			}
			h.sendSessionToScreen(session.Id, session.Screen)
			return
		}
		h.sendMessageToClient(m.client, "register-session")
		return
	}

	session := h.sessions.GetSession(m.client.sessionid)
	if session == nil {
		m.client.sessionid = ""
		h.errorMessageToClient(m.client, "session does not exist", "")
		return
	}

	switch m.cmd {

	case "admin-login":
		if h.sessions.AuthenticateAdmin(session.Id, m.arg) {
			h.sendSessionToScreen(session.Id, "host-select-quiz")
			return
		}

		// invalid credentials
		h.sendMessageToClient(m.client, "invalid-credentials")
		return

	case "join-game":
		pinfo := struct {
			Pin  int    `json:"pin"`
			Name string `json:"name"`
		}{}
		dec := json.NewDecoder(strings.NewReader(m.arg))
		if err := dec.Decode(&pinfo); err != nil {
			h.errorMessageToSession(session.Id, "could not decode json: "+err.Error(), "entrance")
			return
		}
		if len(pinfo.Name) == 0 {
			h.errorMessageToSession(session.Id, "name is missing", "entrance")
			return
		}
		if err := h.games.AddPlayerToGame(session.Id, pinfo.Pin); err != nil {
			h.errorMessageToSession(session.Id, "could not add player to game: "+err.Error(), "entrance")
			return
		}
		h.sessions.RegisterSessionInGame(session.Id, pinfo.Name, pinfo.Pin)
		h.sendSessionToScreen(session.Id, "wait-for-game-start")

		// inform game host of new player
		playerids := h.games.GetPlayersForGame(pinfo.Pin)
		playernames := h.sessions.ConvertSessionIdsToNames(playerids)
		encoded, err := convertToJSON(&playernames)
		if err != nil {
			log.Printf("error encoding player names: %v", err)
			return
		}
		h.sendMessageToGameHost(pinfo.Pin, "participants-list "+encoded)

	case "query-display-choices":
		// player may have been disconnected - now they need to know how many
		// answers to enable
		pin := h.sessions.GetGamePinForSession(m.client.sessionid)
		if pin < 0 {
			h.errorMessageToSession(session.Id, "could not get game pin for this session", "entrance")
			return
		}
		currentQuestion, err := h.games.GetCurrentQuestion(pin)
		if err != nil {
			h.sessions.SetSessionGamePin(session.Id, -1)
			if _, ok := err.(*NoSuchGameError); ok {
				h.errorMessageToSession(session.Id, err.Error(), "entrance")
				return
			}

			h.errorMessageToSession(session.Id, "error retrieving current question: "+err.Error(), "")
			return
		}
		h.sendMessageToClient(m.client, fmt.Sprintf("display-choices %d", len(currentQuestion.Answers)))

	case "query-player-results":
		// player may have been disconnected - now they need to know about
		// their results
		pin := h.sessions.GetGamePinForSession(m.client.sessionid)
		if pin < 0 {
			h.errorMessageToSession(session.Id, "could not get game pin for this session", "entrance")
			return
		}

		game, err := h.games.Get(pin)
		if err != nil {
			h.sessions.SetSessionGamePin(session.Id, -1)
			if _, ok := err.(*NoSuchGameError); ok {
				h.errorMessageToSession(session.Id, err.Error(), "entrance")
				return
			}

			h.errorMessageToSession(session.Id, "error fetching game: "+err.Error(), "entrance")
			return
		}

		_, correct := game.CorrectPlayers[session.Id]
		score, ok := game.Players[session.Id]
		if !ok {
			h.sessions.SetSessionGamePin(session.Id, -1)
			h.errorMessageToSession(session.Id, "you do not have a score in this game", "entrance")
			return
		}

		playerResults := struct {
			Correct bool `json:"correct"`
			Score   int  `json:"score"`
		}{
			Correct: correct,
			Score:   score,
		}

		encoded, err := convertToJSON(&playerResults)
		if err != nil {
			log.Printf("error converting player-results payload to JSON: %v", err)
			return
		}
		h.sendMessageToClient(m.client, "player-results "+encoded)

	case "answer":
		playerAnswer, err := strconv.Atoi(m.arg)
		if err != nil {
			h.errorMessageToSession(session.Id, "could not parse answer", "")
			return
		}
		pin := h.sessions.GetGamePinForSession(m.client.sessionid)
		if pin < 0 {
			h.errorMessageToSession(session.Id, "could not get game pin for this session", "entrance")
			return
		}
		answersUpdate, err := h.games.RegisterAnswer(pin, session.Id, playerAnswer)
		if err != nil {
			if _, ok := err.(*NoSuchGameError); ok {
				h.sessions.SetSessionGamePin(session.Id, -1)
				h.errorMessageToSession(session.Id, err.Error(), "entrance")
				return
			}
			if errState, ok := err.(*UnexpectedStateError); ok {
				switch errState.CurrentState {
				case GameNotStarted:
					h.sendSessionToScreen(session.Id, "wait-for-game-start")
				case ShowResults:
					h.sendSessionToScreen(session.Id, "display-player-results")
				default:
					h.sendSessionToScreen(session.Id, "entrance")
				}
				return
			}

			h.errorMessageToSession(session.Id, "error registering answer: "+err.Error(), "")
			return
		}

		// send this player to wait for question to end screen
		h.sendSessionToScreen(session.Id, "wait-for-question-end")

		encoded, err := convertToJSON(&answersUpdate)
		if err != nil {
			log.Printf("error converting players-answered payload to JSON: %v", err)
			return
		}
		h.sendMessageToGameHost(pin, "players-answered "+encoded)

	case "host-back-to-start":
		h.sendSessionToScreen(session.Id, "entrance")

	case "cancel-game":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			h.errorMessageToSession(session.Id, err.Error(), "entrance")
			return
		}
		players := game.getPlayers()
		players = append(players, game.Host)
		h.RemoveGameFromSessions(players)
		h.SendClientsToScreen(players, "entrance")
		h.games.Delete(game.Pin)

	case "host-game":
		h.sendSessionToScreen(session.Id, "host-select-quiz")

	case "host-game-lobby":
		// create new game
		pin, err := h.games.Add(session.Id)
		if err != nil {
			h.errorMessageToSession(session.Id, "could not add game: "+err.Error(), "host-select-quiz")
			log.Printf("could not add game: " + err.Error())
			return
		}
		h.sessions.SetSessionGamePin(session.Id, pin)
		quizid, err := strconv.Atoi(m.arg)
		if err != nil {
			h.errorMessageToSession(session.Id, "expected int argument", "host-select-quiz")
			return
		}
		quiz, err := h.quizzes.Get(quizid)
		if err != nil {
			h.errorMessageToSession(session.Id, "error getting quiz in new game: "+err.Error(), "host-select-quiz")
			return
		}
		h.games.SetGameQuiz(pin, quiz)
		h.sendSessionToScreen(session.Id, "host-game-lobby")

	case "start-game":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			h.errorMessageToSession(session.Id, err.Error(), "entrance")
			return
		}
		gameState, err := h.games.NextState(game.Pin)
		if err != nil {
			h.errorMessageToSession(session.Id, "error starting game: "+err.Error(), "host-select-quiz")
			return
		}
		if gameState != QuestionInProgress {
			if gameState == ShowResults {
				h.processMessage(&ClientCommand{
					client: m.client,
					cmd:    "show-results",
					arg:    "",
				})
				return
			}
			if gameState == GameEnded {
				h.sendSessionToScreen(session.Id, "host-select-quiz")
				return
			}
			h.errorMessageToSession(session.Id, fmt.Sprintf("game was not in an expected state: %d", gameState), "")
			return
		}
		h.sendSessionToScreen(session.Id, "host-show-question")
		h.sendGamePlayersToAnswerQuestionScreen(game.Pin)

	case "show-results":
		pin, err := h.sendQuestionResultsToHost(m)
		if err != nil {
			h.errorMessageToSession(session.Id, "error sending question results: "+err.Error(), "")
			return
		}
		h.sendSessionToScreen(session.Id, "host-show-results")
		h.informGamePlayersOfResults(pin)

	case "query-host-results":
		if _, err := h.sendQuestionResultsToHost(m); err != nil {
			h.errorMessageToSession(session.Id, "error sending question results: "+err.Error(), "")
			return
		}

	case "next-question":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			h.errorMessageToSession(session.Id, err.Error(), "entrance")
			return
		}

		gameState, err := h.games.NextState(game.Pin)
		if err != nil {
			if _, ok := err.(*NoSuchGameError); ok {
				h.sessions.SetSessionGamePin(session.Id, -1)
				h.errorMessageToSession(session.Id, err.Error(), "entrance")
			}
			h.errorMessageToSession(session.Id, "error setting game to next state: "+err.Error(), "host-select-quiz")
			return
		}
		if gameState == QuestionInProgress {
			h.sendSessionToScreen(session.Id, "host-show-question")
			h.sendGamePlayersToAnswerQuestionScreen(game.Pin)
			return
		}

		// assume that game has ended
		h.sendSessionToScreen(session.Id, "host-show-game-results")

		players := game.getPlayers()
		h.RemoveGameFromSessions(players)
		h.SendClientsToScreen(players, "entrance")

	case "delete-game":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			h.errorMessageToSession(session.Id, err.Error(), "entrance")
			return
		}
		h.games.Delete(game.Pin)
		h.sessions.SetSessionGamePin(m.client.sessionid, -1)
		h.sendSessionToScreen(session.Id, "host-select-quiz")

	default:
		h.errorMessageToSession(session.Id, "invalid command", "")
	}
}

func (h *Hub) ensureUserIsGameHost(m *ClientCommand) (Game, error) {
	session := h.sessions.GetSession(m.client.sessionid)
	if session == nil {
		m.client.sessionid = ""
		return Game{}, errors.New("session does not exist")
	}
	game, err := h.games.Get(session.Gamepin)
	if err != nil {
		h.sessions.SetSessionGamePin(session.Id, -1)
		h.sessions.SetSessionScreen(session.Id, "entrance")
		return Game{}, errors.New("error retrieving game: " + err.Error())
	}
	if game.Host != m.client.sessionid {
		h.sessions.SetSessionGamePin(session.Id, -1)
		h.sessions.SetSessionScreen(session.Id, "entrance")
		return Game{}, errors.New("you are not the host")
	}

	return game, nil
}

// Returns game pin.
func (h *Hub) sendQuestionResultsToHost(m *ClientCommand) (int, error) {
	game, err := h.ensureUserIsGameHost(m)
	if err != nil {
		return -1, err
	}
	if err := h.games.ShowResults(game.Pin); err != nil {
		return -1, fmt.Errorf("error moving game to show results state: %v", err)
	}
	results, err := h.games.GetQuestionResults(game.Pin)
	if err != nil {
		return -1, fmt.Errorf("error getting question results: %v", err)
	}

	for i, ps := range results.TopScorers {
		name := h.sessions.GetNameForSession(ps.id)
		if name == "" {
			name = "unknown"
		}
		results.TopScorers[i].Name = name
	}

	encoded, err := convertToJSON(&results)
	if err != nil {
		return -1, fmt.Errorf("error converting question results payload to JSON: %v", err)
	}
	h.sendMessageToClient(m.client, "question-results "+encoded)

	return game.Pin, nil
}

func (h *Hub) sendMessageToGameHost(pin int, message string) {
	hostid := h.games.GetHostForGame(pin)
	if hostid == "" {
		log.Printf("game %d does not have a host", pin)
		return
	}
	hostclient := h.sessions.GetClientForSession(hostid)
	if hostclient == nil {
		// host has probably disconnected
		return
	}
	h.sendMessageToClient(hostclient, message)
}

// We are doing all this in the hub for performance reasons - if we did this
// in the client, we would have to keep fetching the game question for each
// client.
func (h *Hub) sendGamePlayersToAnswerQuestionScreen(pin int) {
	game, err := h.games.Get(pin)
	if err != nil {
		log.Printf("error retrieving game %d: %v", pin, err)
		return
	}
	question, err := game.Quiz.GetQuestion(game.QuestionIndex)
	if err != nil {
		log.Printf("error getting question: %v", err)
		return
	}
	answerCount := len(question.Answers)
	for pid := range game.Players {
		h.sessions.SetSessionScreen(pid, "answer-question")
		client := h.sessions.GetClientForSession(pid)
		if client == nil {
			continue
		}
		h.sendMessageToClient(client, fmt.Sprintf("display-choices %d", answerCount))
		h.sendMessageToClient(client, "screen answer-question")
	}
}

// We are doing all this in the hub for performance reasons - if we did this
// in the client, we would have to keep fetching the game  for each client.
//
// Also sends game players to display-player-results screen.
//
func (h *Hub) informGamePlayersOfResults(pin int) {
	game, err := h.games.Get(pin)
	if err != nil {
		log.Printf("error retrieving game %d: %v", pin, err)
		return
	}

	playerResults := struct {
		Correct bool `json:"correct"`
		Score   int  `json:"score"`
	}{}

	for pid, score := range game.Players {
		_, playerCorrect := game.CorrectPlayers[pid]
		playerResults.Correct = playerCorrect
		playerResults.Score = score

		// we're doing this here to set the state for disconnected players
		h.sessions.SetSessionScreen(pid, "display-player-results")

		client := h.sessions.GetClientForSession(pid)
		if client == nil {
			continue
		}

		encoded, err := convertToJSON(&playerResults)
		if err != nil {
			log.Printf("error converting player-results payload to JSON: %v", err)
			continue
		}
		h.sendMessageToClient(client, "player-results "+encoded)
		h.sendMessageToClient(client, "screen display-player-results")
	}
}

func (h *Hub) SendClientsToScreen(sessionids []string, screen string) {
	for _, id := range sessionids {
		h.sendSessionToScreen(id, screen)
	}
}

func (h *Hub) RemoveGameFromSessions(sessionids []string) {
	for _, id := range sessionids {
		h.sessions.DeregisterGameFromSession(id)
	}
}

func (h *Hub) sendSessionToScreen(id, s string) {
	session := h.sessions.GetSession(id)
	if session == nil {
		return
	}

	// ensure that session is admin if trying to access host screens
	if strings.HasPrefix(s, "host") && !session.Admin {
		s = "authenticate-user"
	}

	switch s {
	case "host-select-quiz":
		type meta struct {
			Id   int    `json:"id"`
			Name string `json:"name"`
		}
		ml := []meta{}
		for _, q := range h.quizzes.GetQuizzes() {
			ml = append(ml, meta{
				Id:   q.Id,
				Name: q.Name,
			})
		}

		encoded, err := convertToJSON(&ml)
		if err != nil {
			h.errorMessageToSession(id, fmt.Sprintf("error encoding json: %v", err), "host-select-quiz")
			return
		}
		h.sendMessageToClient(session.Client, "all-quizzes "+encoded)

	case "host-game-lobby":
		// send over game object with lobby-game-metadata
		game, err := h.games.Get(session.Gamepin)
		if err != nil {
			h.errorMessageToSession(id, fmt.Sprintf("could not retrieve game %d", session.Gamepin), "entrance")
			h.sessions.SetSessionScreen(session.Id, "entrance")
			return
		}

		gameMetadata := struct {
			Pin     int      `json:"pin"`
			Name    string   `json:"name"`
			Host    string   `json:"host"`
			Players []string `json:"players"`
		}{
			Pin:  game.Pin,
			Name: game.Quiz.Name,
			Host: game.Host,
		}
		playerids := []string{}
		for k := range game.Players {
			playerids = append(playerids, k)
		}
		gameMetadata.Players = h.sessions.ConvertSessionIdsToNames(playerids)

		encoded, err := convertToJSON(&gameMetadata)
		if err != nil {
			h.errorMessageToSession(id, "error converting lobby-game-metadata payload to JSON: "+err.Error(), "")
			return
		}
		h.sendMessageToClient(session.Client, "lobby-game-metadata "+encoded)

	case "host-show-question":
		currentQuestion, err := h.games.GetCurrentQuestion(session.Gamepin)
		if err != nil {
			// if the host disconnected while the question was live, and if
			// the game state has now changed, we may need to move the host to
			// the relevant screen
			unexpectedState, ok := err.(*UnexpectedStateError)
			if ok && unexpectedState.CurrentState == ShowResults {
				h.processMessage(&ClientCommand{
					client: session.Client,
					cmd:    "show-results",
					arg:    "",
				})
				return
			}

			h.errorMessageToSession(id, "error retrieving question: "+err.Error(), "")
			return
		}

		encoded, err := convertToJSON(&currentQuestion)
		if err != nil {
			h.errorMessageToSession(id, "error converting question to JSON: "+err.Error(), "")
			return
		}
		h.sendMessageToClient(session.Client, "host-show-question "+encoded)

		// The logic for answer-question is in the hub
		//case "answer-question":

	case "host-show-game-results":
		winners, err := h.games.GetWinners(session.Gamepin)
		if err != nil {
			h.errorMessageToSession(id, "error retrieving game winners: "+err.Error(), "")
			return
		}
		type FriendlyScore struct {
			Name  string `json:"name"`
			Score int    `json:"score"`
		}
		fl := []FriendlyScore{}
		for _, w := range winners {
			session := h.sessions.GetSession(w.id)
			if session == nil {
				// player session doesn't exist anymore
				continue
			}
			fl = append(fl, FriendlyScore{
				Name:  session.Name,
				Score: w.Score,
			})
		}
		encoded, err := convertToJSON(&fl)
		if err != nil {
			h.errorMessageToSession(id, "error converting show-winners payload to JSON: "+err.Error(), "")
			return
		}
		log.Printf("winners for game %d: %s", session.Gamepin, encoded)
		h.sendMessageToClient(session.Client, "show-winners "+encoded)

		// end of switch
	}

	h.sessions.SetSessionScreen(id, s)
	h.sendMessageToClient(session.Client, "screen "+s)
}

func (h *Hub) sendMessageToClient(c *Client, s string) {
	if c == nil {
		return
	}
	select {
	case c.send <- []byte(s):
	default:
		close(c.send)
		delete(h.clients, c)
	}
}

func (h *Hub) errorMessageToSession(id, message, nextscreen string) {
	session := h.sessions.GetSession(id)
	if session == nil {
		return
	}
	if nextscreen != "" {
		h.sessions.SetSessionScreen(id, nextscreen)
	}
	h.errorMessageToClient(session.Client, message, nextscreen)
}

func (h *Hub) errorMessageToClient(c *Client, message, nextscreen string) {
	if c == nil {
		return
	}

	data := struct {
		Message    string `json:"message"`
		NextScreen string `json:"nextscreen"`
	}{
		Message:    message,
		NextScreen: nextscreen,
	}
	encoded, err := convertToJSON(data)
	if err != nil {
		log.Printf("error converting payload for error message: %v", err)
		return
	}
	h.sendMessageToClient(c, "error "+encoded)
}

func convertToJSON(input interface{}) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(input); err != nil {
		return "", err
	}
	return b.String(), nil
}
