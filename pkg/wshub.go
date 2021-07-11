// Copied from https://github.com/gorilla/websocket/blob/master/examples/chat/hub.go
// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
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

func NewHub() *Hub {
	quizzes, err := InitQuizzes()
	if err != nil {
		log.Fatal(err)
	}

	return &Hub{
		incomingcommands: make(chan *ClientCommand),
		register:         make(chan *Client),
		unregister:       make(chan *Client),
		clients:          make(map[*Client]bool),
		sessions:         InitSessions(),
		quizzes:          quizzes,
		games:            InitGames(),
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
			//for client := range h.clients {
			//	select {
			//	case client.send <- message:
			//	default:
			//		close(client.send)
			//		delete(h.clients, client)
			//	}
			//}
		}
	}
}

func (h *Hub) processMessage(m *ClientCommand) {
	// client hasn't identified themselves yet
	if len(m.client.sessionid) == 0 {
		if m.cmd == "session" {
			if len(m.arg) == 0 {
				m.client.errorMessage("invalid session ID")
				return
			}
			m.client.sessionid = m.arg
			session := h.sessions.GetSession(m.client.sessionid)
			if session == nil {
				session = h.sessions.NewSession(m.client.sessionid, m.client, "entrance")
			} else {
				if session.client != nil {
					m.client.sessionid = ""
					m.client.errorMessage("you have another active session - disconnect that session before reconnecting")
					return
				}
				h.sessions.UpdateClientForSession(m.client.sessionid, m.client)
			}
			m.client.screen(session.screen)
			return
		}
		m.client.errorMessage("client does not have a session")
		return
	}

	switch m.cmd {

	case "join-game":
		pinfo := struct {
			Pin  int    `json:"pin"`
			Name string `json:"name"`
		}{}
		dec := json.NewDecoder(strings.NewReader(m.arg))
		if err := dec.Decode(&pinfo); err != nil {
			m.client.errorMessage("could not decode json: " + err.Error())
			return
		}
		if len(pinfo.Name) == 0 {
			m.client.errorMessage("name is missing")
			return
		}
		if err := h.games.AddPlayerToGame(m.client.sessionid, pinfo.Pin); err != nil {
			m.client.errorMessage("could not add player to game: " + err.Error())
			return
		}
		h.sessions.RegisterSessionInGame(m.client.sessionid, pinfo.Name, pinfo.Pin)
		m.client.screen("waitforgamestart")

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
			m.client.errorMessage("could not get game pin for this session")
			return
		}
		currentQuestion, err := h.games.GetCurrentQuestion(pin)
		if err != nil {
			m.client.errorMessage("error retrieving current question: " + err.Error())
			return
		}
		m.client.sendMessage(fmt.Sprintf("display-choices %d", len(currentQuestion.Answers)))

	case "query-player-results":
		// player may have been disconnected - now they need to know about
		// their results
		pin := h.sessions.GetGamePinForSession(m.client.sessionid)
		if pin < 0 {
			m.client.errorMessage("could not get game pin for this session")
			return
		}

		game, err := h.games.Get(pin)
		if err != nil {
			m.client.errorMessage("error fetching game: " + err.Error())
			return
		}

		_, correct := game.CorrectPlayers[m.client.sessionid]
		score, ok := game.Players[m.client.sessionid]
		if !ok {
			m.client.errorMessage("you do not have a score in this game")
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
		m.client.sendMessage("player-results " + encoded)

	case "answer":
		playerAnswer, err := strconv.Atoi(m.arg)
		if err != nil {
			m.client.errorMessage("could not parse answer")
			return
		}
		pin := h.sessions.GetGamePinForSession(m.client.sessionid)
		if pin < 0 {
			m.client.errorMessage("could not get game pin for this session")
			return
		}
		answersUpdate, err := h.games.RegisterAnswer(pin, m.client.sessionid, playerAnswer)
		if err != nil {
			m.client.errorMessage("error registering answer: " + err.Error())
			return
		}

		// send this player to wait for question to end screen
		m.client.screen("waitforquestionend")

		encoded, err := convertToJSON(&answersUpdate)
		if err != nil {
			log.Printf("error converting players-answered payload to JSON: %v", err)
			return
		}
		h.sendMessageToGameHost(pin, "players-answered "+encoded)

	case "host-back-to-start":
		m.client.screen("entrance")

	case "cancel-game":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			m.client.errorMessage(err.Error())
			return
		}
		players := game.getPlayers()
		players = append(players, game.Host)
		h.removeGameFromSessions(players)
		h.sendClientsToScreen(players, "entrance")
		h.games.Delete(game.Pin)

	case "host-game":
		m.client.screen("hostselectquiz")

	case "hostgamelobby":
		// create new game
		pin, err := h.games.Add(m.client.sessionid)
		if err != nil {
			m.client.errorMessage("could not add game: " + err.Error())
			return
		}
		h.sessions.SetSessionGamePin(m.client.sessionid, pin)
		quizid, err := strconv.Atoi(m.arg)
		if err != nil {
			m.client.errorMessage("expected int argument")
			return
		}
		quiz, err := h.quizzes.Get(quizid)
		if err != nil {
			m.client.errorMessage("error setting quiz in new game: " + err.Error())
			return
		}
		h.games.SetGameQuiz(pin, quiz)
		m.client.screen("hostgamelobby")

	case "start-game":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			m.client.errorMessage(err.Error())
			return
		}
		gameState, err := h.games.NextState(game.Pin)
		if err != nil {
			m.client.errorMessage("error starting game: " + err.Error())
			return
		}
		if gameState != QuestionInProgress {
			m.client.errorMessage(fmt.Sprintf("game was not in an expected state: %d", gameState))
			return
		}
		m.client.screen("hostshowquestion")
		h.sendGamePlayersToAnswerQuestionScreen(game.Pin)

	case "show-results":
		pin, err := h.sendQuestionResultsToHost(m)
		if err != nil {
			m.client.errorMessage("error sending question results: " + err.Error())
			return
		}
		m.client.screen("hostshowresults")
		h.informGamePlayersOfResults(pin)

	case "query-host-results":
		if _, err := h.sendQuestionResultsToHost(m); err != nil {
			m.client.errorMessage("error sending question results: " + err.Error())
			return
		}

	case "next-question":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			m.client.errorMessage(err.Error())
			return
		}

		gameState, err := h.games.NextState(game.Pin)
		if err != nil {
			m.client.errorMessage("error setting game to next state: " + err.Error())
			return
		}
		if gameState == QuestionInProgress {
			m.client.screen("hostshowquestion")
			h.sendGamePlayersToAnswerQuestionScreen(game.Pin)
			return
		}

		// assume that game has ended
		m.client.screen("hostshowgameresults")

		players := game.getPlayers()
		h.removeGameFromSessions(players)
		h.sendClientsToScreen(players, "entrance")

	case "delete-game":
		game, err := h.ensureUserIsGameHost(m)
		if err != nil {
			m.client.errorMessage(err.Error())
			return
		}
		h.games.Delete(game.Pin)
		m.client.screen("hostselectquiz")

	default:
		m.client.errorMessage("invalid command")
	}
}

func (h *Hub) ensureUserIsGameHost(m *ClientCommand) (Game, error) {
	session := h.sessions.GetSession(m.client.sessionid)
	if session == nil {
		return Game{}, errors.New("session does not exist")
	}
	game, err := h.games.Get(session.gamepin)
	if err != nil {
		return Game{}, errors.New("error retrieving game: " + err.Error())
	}
	if game.Host != m.client.sessionid {
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
	encoded, err := convertToJSON(&results)
	if err != nil {
		return -1, fmt.Errorf("error converting question results payload to JSON: %v", err)
	}
	m.client.sendMessage("question-results " + encoded)

	return game.Pin, nil
}

func (h *Hub) sendMessageToGameHost(pin int, message string) {
	hostid := h.games.GetHostForGame(pin)
	if len(hostid) == 0 {
		log.Printf("game %d does not have a host", pin)
		return
	}
	hostclient := h.sessions.GetClientForSession(hostid)
	if hostclient == nil {
		// host has probably disconnected
		return
	}
	hostclient.sendMessage(message)
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
		client := h.sessions.GetClientForSession(pid)
		if client == nil {
			continue
		}
		client.sendMessage(fmt.Sprintf("display-choices %d", answerCount))
		h.sessions.UpdateScreenForSession(pid, "answerquestion")
		client.sendMessage("screen answerquestion")
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
		client := h.sessions.GetClientForSession(pid)
		if client == nil {
			continue
		}
		_, playerCorrect := game.CorrectPlayers[pid]
		playerResults.Correct = playerCorrect
		playerResults.Score = score

		encoded, err := convertToJSON(&playerResults)
		if err != nil {
			log.Printf("error converting player-results payload to JSON: %v", err)
			continue
		}
		client.sendMessage("player-results " + encoded)
		h.sessions.UpdateScreenForSession(pid, "displayplayerresults")
		client.sendMessage("screen displayplayerresults")
	}
}

func (h *Hub) sendClientsToScreen(sessionids []string, screen string) {
	for _, id := range sessionids {
		client := h.sessions.GetClientForSession(id)
		if client == nil {
			continue
		}
		client.screen(screen)
	}
}

func (h *Hub) removeGameFromSessions(sessionids []string) {
	for _, id := range sessionids {
		h.sessions.DeregisterGameFromSession(id)
	}
}
