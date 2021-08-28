// Copied from https://github.com/gorilla/websocket/blob/master/examples/chat/hub.go
// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"encoding/json"
	"log"
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

	msghub *MessageHub

	sessions *Sessions

	quizzes *Quizzes

	games *Games
}

func NewHub(msghub *MessageHub, redisHost, redisPassword string, auth *Auth, sessionTimeout int) *Hub {
	persistenceEngine := InitRedis(redisHost, redisPassword, GetShutdownArtifacts())
	quizzes, err := InitQuizzes(msghub, persistenceEngine)
	if err != nil {
		log.Fatal(err)
	}

	return &Hub{
		incomingcommands: make(chan *ClientCommand),
		register:         make(chan *Client),
		unregister:       make(chan *Client),
		clients:          make(map[*Client]bool),
		msghub:           msghub,
		sessions:         InitSessions(msghub, persistenceEngine, auth, sessionTimeout, GetShutdownArtifacts()),
		quizzes:          quizzes,
		games:            InitGames(msghub, persistenceEngine),
	}
}

func (h *Hub) Run() { // todo: accept shutdownChan as arg
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			h.deregisterClient(client)
		case message := <-h.incomingcommands:
			log.Printf("incoming command: %s, arg: %s", message.cmd, message.arg)
			h.processMessage(message)

			// todo: process messages from client-hub: SetSessionIDForClientMessage, ClientMessage
			// * ClientErrorMessage - send message to client

			// todo: wait on shutdownChan
		}
	}
}

func (h *Hub) deregisterClient(client *Client) {
	if client == nil {
		return
	}
	delete(h.clients, client)
	close(client.send)
	if client.sessionid != "" {
		log.Printf("cleaned up client for session %s", client.sessionid)
		h.sessions.UpdateClientForSession(client.sessionid, nil)
	}
}

func (h *Hub) processMessage(m *ClientCommand) {
	log.Printf("cmd=%s, arg=%s", m.cmd, m.arg)

	h.msghub.Send(incomingMessageTopic, m)
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

// todo: code ported to session.processSessionToScreenMessage
// todo: to be removed
func (h *Hub) sendSessionToScreen(id, s string) {

}

func (h *Hub) sendMessageToClient(c *Client, s string) {
	if c == nil {
		return
	}
	select {
	case c.send <- []byte(s):
	default:
		h.deregisterClient(c)
	}
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
