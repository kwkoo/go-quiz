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
	log.Printf("cmd=%s,arg=%s", m.cmd, m.arg)

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
				h.sessions.UpdateClientForSession(m.client.sessionid, m.client)
			}
			h.sendClientToScreen(m.client, session.Screen)
			return
		}
		h.sendMessageToClient(m.client, "reregistersession")
		return
	}

	switch m.cmd {

	case "adminlogin":
		if h.sessions.AuthenticateAdmin(m.client.sessionid, m.arg) {
			h.sendClientToScreen(m.client, "hostselectquiz")
			return
		}

		// invalid credentials
		h.sendMessageToClient(m.client, "invalidcredentials")
		return

	case "join-game":
		pinfo := struct {
			Pin  int    `json:"pin"`
			Name string `json:"name"`
		}{}
		dec := json.NewDecoder(strings.NewReader(m.arg))
		if err := dec.Decode(&pinfo); err != nil {
			h.errorMessageToClient(m.client, "could not decode json: "+err.Error(), "entrance")
			return
		}
		if len(pinfo.Name) == 0 {
			h.errorMessageToClient(m.client, "name is missing", "entrance")
			return
		}
		if err := h.games.AddPlayerToGame(m.client.sessionid, pinfo.Pin); err != nil {
			h.errorMessageToClient(m.client, "could not add player to game: "+err.Error(), "entrance")
			return
		}
		h.sessions.RegisterSessionInGame(m.client.sessionid, pinfo.Name, pinfo.Pin)
		h.sendClientToScreen(m.client, "waitforgamestart")

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
			h.errorMessageToClient(m.client, "could not get game pin for this session", "entrance")
			return
		}
		currentQuestion, err := h.games.GetCurrentQuestion(pin)
		if err != nil {
			h.sessions.SetSessionGamePin(m.client.sessionid, -1)
			if _, ok := err.(*NoSuchGameError); ok {
				h.errorMessageToClient(m.client, err.Error(), "entrance")
				return
			}

			h.errorMessageToClient(m.client, "error retrieving current question: "+err.Error(), "")
			return
		}
		h.sendMessageToClient(m.client, fmt.Sprintf("display-choices %d", len(currentQuestion.Answers)))

	case "query-player-results":
		// player may have been disconnected - now they need to know about
		// their results
		pin := h.sessions.GetGamePinForSession(m.client.sessionid)
		if pin < 0 {
			h.errorMessageToClient(m.client, "could not get game pin for this session", "entrance")
			return
		}

		game, err := h.games.Get(pin)
		if err != nil {
			h.sessions.SetSessionGamePin(m.client.sessionid, -1)
			if _, ok := err.(*NoSuchGameError); ok {
				h.errorMessageToClient(m.client, err.Error(), "entrance")
				return
			}

			h.errorMessageToClient(m.client, "error fetching game: "+err.Error(), "entrance")
			return
		}

		_, correct := game.CorrectPlayers[m.client.sessionid]
		score, ok := game.Players[m.client.sessionid]
		if !ok {
			h.sessions.SetSessionGamePin(m.client.sessionid, -1)
			h.errorMessageToClient(m.client, "you do not have a score in this game", "entrance")
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
			h.errorMessageToClient(m.client, "could not parse answer", "")
			return
		}
		pin := h.sessions.GetGamePinForSession(m.client.sessionid)
		if pin < 0 {
			h.errorMessageToClient(m.client, "could not get game pin for this session", "entrance")
			return
		}
		answersUpdate, err := h.games.RegisterAnswer(pin, m.client.sessionid, playerAnswer)
		if err != nil {
			if _, ok := err.(*NoSuchGameError); ok {
				h.sessions.SetSessionGamePin(m.client.sessionid, -1)
				h.errorMessageToClient(m.client, err.Error(), "entrance")
				return
			}
			if errState, ok := err.(*UnexpectedStateError); ok {
				switch errState.CurrentState {
				case GameNotStarted:
					h.sendClientToScreen(m.client, "waitforgamestart")
				case ShowResults:
					h.sendClientToScreen(m.client, "displayplayerresults")
				default:
					h.sendClientToScreen(m.client, "entrance")
				}
				return
			}

			h.errorMessageToClient(m.client, "error registering answer: "+err.Error(), "")
			return
		}

		// send this player to wait for question to end screen
		h.sendClientToScreen(m.client, "waitforquestionend")

		encoded, err := convertToJSON(&answersUpdate)
		if err != nil {
			log.Printf("error converting players-answered payload to JSON: %v", err)
			return
		}
		h.sendMessageToGameHost(pin, "players-answered "+encoded)

	case "host-back-to-start":
		h.sendClientToScreen(m.client, "entrance")

	case "cancel-game":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			h.errorMessageToClient(m.client, err.Error(), "entrance")
			return
		}
		players := game.getPlayers()
		players = append(players, game.Host)
		h.RemoveGameFromSessions(players)
		h.SendClientsToScreen(players, "entrance")
		h.games.Delete(game.Pin)

	case "host-game":
		h.sendClientToScreen(m.client, "hostselectquiz")

	case "hostgamelobby":
		// create new game
		pin, err := h.games.Add(m.client.sessionid)
		if err != nil {
			h.errorMessageToClient(m.client, "could not add game: "+err.Error(), "hostselectquiz")
			log.Printf("could not add game: " + err.Error())
			return
		}
		h.sessions.SetSessionGamePin(m.client.sessionid, pin)
		quizid, err := strconv.Atoi(m.arg)
		if err != nil {
			h.errorMessageToClient(m.client, "expected int argument", "hostselectquiz")
			return
		}
		quiz, err := h.quizzes.Get(quizid)
		if err != nil {
			h.errorMessageToClient(m.client, "error getting quiz in new game: "+err.Error(), "hostselectquiz")
			return
		}
		h.games.SetGameQuiz(pin, quiz)
		h.sendClientToScreen(m.client, "hostgamelobby")

	case "start-game":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			h.errorMessageToClient(m.client, err.Error(), "entrance")
			return
		}
		gameState, err := h.games.NextState(game.Pin)
		if err != nil {
			h.errorMessageToClient(m.client, "error starting game: "+err.Error(), "hostselectquiz")
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
				h.sendClientToScreen(m.client, "hostselectquiz")
				return
			}
			h.errorMessageToClient(m.client, fmt.Sprintf("game was not in an expected state: %d", gameState), "")
			return
		}
		h.sendClientToScreen(m.client, "hostshowquestion")
		h.sendGamePlayersToAnswerQuestionScreen(game.Pin)

	case "show-results":
		pin, err := h.sendQuestionResultsToHost(m)
		if err != nil {
			h.errorMessageToClient(m.client, "error sending question results: "+err.Error(), "")
			return
		}
		h.sendClientToScreen(m.client, "hostshowresults")
		h.informGamePlayersOfResults(pin)

	case "query-host-results":
		if _, err := h.sendQuestionResultsToHost(m); err != nil {
			h.errorMessageToClient(m.client, "error sending question results: "+err.Error(), "")
			return
		}

	case "next-question":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			h.errorMessageToClient(m.client, err.Error(), "entrance")
			return
		}

		gameState, err := h.games.NextState(game.Pin)
		if err != nil {
			if _, ok := err.(*NoSuchGameError); ok {
				h.sessions.SetSessionGamePin(m.client.sessionid, -1)
				h.errorMessageToClient(m.client, err.Error(), "entrance")
			}
			h.errorMessageToClient(m.client, "error setting game to next state: "+err.Error(), "hostselectquiz")
			return
		}
		if gameState == QuestionInProgress {
			h.sendClientToScreen(m.client, "hostshowquestion")
			h.sendGamePlayersToAnswerQuestionScreen(game.Pin)
			return
		}

		// assume that game has ended
		h.sendClientToScreen(m.client, "hostshowgameresults")

		players := game.getPlayers()
		h.RemoveGameFromSessions(players)
		h.SendClientsToScreen(players, "entrance")

	case "delete-game":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			h.errorMessageToClient(m.client, err.Error(), "entrance")
			return
		}
		h.games.Delete(game.Pin)
		h.sessions.SetSessionGamePin(m.client.sessionid, -1)
		h.sendClientToScreen(m.client, "hostselectquiz")

	default:
		h.errorMessageToClient(m.client, "invalid command", "")
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

	// this is an ugly hack - go through the top scorers and replace the
	// session IDs with names
	for i, ps := range results.TopScorers {
		name := h.sessions.GetNameForSession(ps.Id)
		if name != "" {
			results.TopScorers[i].Id = name
		}
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
		h.sessions.UpdateScreenForSession(pid, "answerquestion")
		client := h.sessions.GetClientForSession(pid)
		if client == nil {
			continue
		}
		h.sendMessageToClient(client, fmt.Sprintf("display-choices %d", answerCount))
		h.sendMessageToClient(client, "screen answerquestion")
	}
}

// We are doing all this in the hub for performance reasons - if we did this
// in the client, we would have to keep fetching the game  for each client.
//
// Also sends game players to displayplayerresults screen.
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
		h.sessions.UpdateScreenForSession(pid, "displayplayerresults")

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
		h.sendMessageToClient(client, "screen displayplayerresults")
	}
}

func (h *Hub) SendClientsToScreen(sessionids []string, screen string) {
	for _, id := range sessionids {
		client := h.sessions.GetClientForSession(id)
		if client == nil {
			continue
		}
		h.sendClientToScreen(client, screen)
	}
}

func (h *Hub) RemoveGameFromSessions(sessionids []string) {
	for _, id := range sessionids {
		h.sessions.DeregisterGameFromSession(id)
	}
}

func (h *Hub) sendClientToScreen(c *Client, s string) {
	session := h.sessions.GetSession(c.sessionid)
	if session == nil {
		h.errorMessageToClient(c, "session does not exist anymore", "entrance")
		return
	}

	// ensure that session is admin if trying to access host screens
	if strings.HasPrefix(s, "host") && !session.Admin {
		s = "authenticateuser"
	}

	switch s {
	case "hostselectquiz":
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
			h.errorMessageToClient(c, fmt.Sprintf("error encoding json: %v", err), "hostselectquiz")
			return
		}
		h.sendMessageToClient(c, "all-quizzes "+encoded)

	case "hostgamelobby":
		// send over game object with lobby-game-metadata
		game, err := h.games.Get(session.Gamepin)
		if err != nil {
			h.errorMessageToClient(c, fmt.Sprintf("could not retrieve game %d", session.Gamepin), "entrance")
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
			h.errorMessageToClient(c, "error converting lobby-game-metadata payload to JSON: "+err.Error(), "")
			return
		}
		h.sendMessageToClient(c, "lobby-game-metadata "+encoded)

	case "hostshowquestion":
		session := h.sessions.GetSession(c.sessionid)
		if session == nil {
			c.sessionid = ""
			h.errorMessageToClient(c, "could not get session", "entrance")
			return
		}

		currentQuestion, err := h.games.GetCurrentQuestion(session.Gamepin)
		if err != nil {
			// if the host disconnected while the question was live, and if
			// the game state has now changed, we may need to move the host to
			// the relevant screen
			unexpectedState, ok := err.(*UnexpectedStateError)
			if ok && unexpectedState.CurrentState == ShowResults {
				h.processMessage(&ClientCommand{
					client: c,
					cmd:    "show-results",
					arg:    "",
				})
				return
			}

			h.errorMessageToClient(c, "error retrieving question: "+err.Error(), "")
			return
		}

		encoded, err := convertToJSON(&currentQuestion)
		if err != nil {
			h.errorMessageToClient(c, "error converting question to JSON: "+err.Error(), "")
			return
		}
		h.sendMessageToClient(c, "hostshowquestion "+encoded)

		// The logic for answerquestion is in the hub
		//case "answerquestion":

	case "hostshowgameresults":
		session := h.sessions.GetSession(c.sessionid)
		if session == nil {
			c.sessionid = ""
			h.errorMessageToClient(c, "could not get session", "entrance")
			return
		}

		winners, err := h.games.GetWinners(session.Gamepin)
		if err != nil {
			h.errorMessageToClient(c, "error retrieving game winners: "+err.Error(), "")
			return
		}
		type FriendlyScore struct {
			Name  string `json:"name"`
			Score int    `json:"score"`
		}
		fl := []FriendlyScore{}
		for _, w := range winners {
			session := h.sessions.GetSession(w.Id)
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
			h.errorMessageToClient(c, "error converting show-winners payload to JSON: "+err.Error(), "")
			return
		}
		log.Printf("winners for game %d: %s", session.Gamepin, encoded)
		h.sendMessageToClient(c, "show-winners "+encoded)

		// end of switch
	}

	h.sessions.UpdateScreenForSession(c.sessionid, s)
	h.sendMessageToClient(c, "screen "+s)
}

func (h *Hub) sendMessageToClient(c *Client, s string) {
	select {
	case c.send <- []byte(s):
	default:
		close(c.send)
		delete(h.clients, c)
	}
}

func (h *Hub) errorMessageToClient(c *Client, message, nextscreen string) {
	if nextscreen != "" {
		h.sessions.SetSessionScreen(c.sessionid, "entrance")
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
