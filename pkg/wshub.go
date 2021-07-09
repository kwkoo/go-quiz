// Copied from https://github.com/gorilla/websocket/blob/master/examples/chat/hub.go
// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"encoding/json"
	"log"
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
		game, err := h.games.Add(m.client.sessionid)
		if err != nil {
			m.client.errorMessage("could not add game: " + err.Error())
			return
		}
		h.sessions.SetSessionGamePin(m.client.sessionid, game.Pin)
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
