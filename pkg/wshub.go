// Copied from https://github.com/gorilla/websocket/blob/master/examples/chat/hub.go
// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"encoding/json"
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
				session = h.sessions.NewSession(m.client.sessionid, m.client, "enter-identity")
			} else {
				h.sessions.UpdateClientForSession(m.client.sessionid, m.client)
			}
			m.client.screen(session.screen)
			return
		}
		m.client.errorMessage("client does not have a session")
		return
	}

	switch m.cmd {
	case "host-game":
		m.client.screen("select-quiz")
	case "game-lobby":
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
		m.client.screen("game-lobby")

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
		h.sessions.SetSessionName(m.client.sessionid, pinfo.Name)
		if err := h.games.AddPlayerToGame(m.client.sessionid, pinfo.Pin); err != nil {
			m.client.errorMessage("could not add player to game: " + err.Error())
			return
		}
		m.client.screen("wait-for-game-start")

		// inform game host of new player
		playerids := h.games.GetPlayersForGame(pinfo.Pin)
		playernames := h.sessions.ConvertSessionIdsToNames(playerids)
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		if err := enc.Encode(&playernames); err != nil {
			log.Printf("error encoding player names: %v", err)
			return
		}
		h.sendMessageToGameHost(pinfo.Pin, "participants-list "+b.String())

	case "start-game":
		session := h.sessions.GetSession(m.client.sessionid)
		if session == nil {
			m.client.errorMessage("session does not exist")
			return
		}
		game, err := h.games.Get(session.gamepin)
		if err != nil {
			m.client.errorMessage("error retrieving game: " + err.Error())
			return
		}
		if game.Host != m.client.sessionid {
			m.client.errorMessage("you are not the host")
			return
		}
		gameState, err := h.games.NextState(session.gamepin)
		if err != nil {
			m.client.errorMessage("error starting game: " + err.Error())
			return
		}
		if gameState != QuestionInProgress {
			m.client.errorMessage(fmt.Sprintf("game was not in an expected state: %d", gameState))
			return
		}
		m.client.screen("show-question")
		h.sendGamePlayersToAnswerQuestionScreen(session.gamepin)

	default:
		m.client.errorMessage("invalid command")
	}
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
		client.sendMessage(fmt.Sprintf("answer %d", answerCount))
		h.sessions.UpdateScreenForSession(pid, "answer-question")
		client.sendMessage("screen answer-question")
	}
}

/*
func (h *Hub) sendMessageToGamePlayers(pin int, message string) {
	playerids := h.games.GetPlayersForGame(pin)
	for _, pid := range playerids {
		client := h.sessions.GetClientForSession(pid)
		if client == nil {
			continue
		}
		client.sendMessage(message)
	}
}

func (h *Hub) sendGamePlayersToScreen(pin int, screen string) {
	playerids := h.games.GetPlayersForGame(pin)
	for _, pid := range playerids {
		client := h.sessions.GetClientForSession(pid)
		if client == nil {
			continue
		}
		client.screen(screen)
	}
}
*/
