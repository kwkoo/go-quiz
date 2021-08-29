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
	persistenceEngine := InitRedis(redisHost, redisPassword, msghub)
	quizzes, err := InitQuizzes(msghub, persistenceEngine)
	if err != nil {
		log.Fatal(err)
	}

	sessions := InitSessions(msghub, persistenceEngine, auth, sessionTimeout)
	games := InitGames(msghub, persistenceEngine)

	go func() {
		quizzes.Run()
	}()

	go func() {
		sessions.Run()
	}()

	go func() {
		games.Run()
	}()

	return &Hub{
		incomingcommands: make(chan *ClientCommand),
		register:         make(chan *Client),
		unregister:       make(chan *Client),
		clients:          make(map[*Client]bool),
		msghub:           msghub,
		sessions:         sessions,
		quizzes:          quizzes,
		games:            games,
	}
}

func (h *Hub) Run() {
	shutdownChan := h.msghub.GetShutdownChan()
	clientHub := h.msghub.GetTopic(clientHubTopic)

	for {
		select {
		case <-shutdownChan:
			log.Print("websockethub received shutdown signal, exiting")
			h.msghub.NotifyShutdownComplete()
			return

		case client := <-h.register:
			h.clients[client] = true

		case client := <-h.unregister:
			h.deregisterClient(client)

		case message := <-h.incomingcommands:
			log.Printf("incoming command: %s, arg: %s", message.cmd, message.arg)
			h.processMessage(message)

		case msg, ok := <-clientHub:
			if !ok {
				log.Print("received empty message from client-hub")
				continue
			}
			if h.processClientMessage(msg) {
				continue
			}
			if h.processClientErrorMessage(msg) {
				continue
			}
			if h.processSetSessionIDForClientMessage(msg) {
				continue
			}
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

		h.msghub.Send(sessionsTopic, SetSessionIDForClientMessage{
			sessionid: client.sessionid,
			client:    nil,
		})
	}
}

func (h *Hub) processSetSessionIDForClientMessage(message interface{}) bool {
	msg, ok := message.(SetSessionIDForClientMessage)
	if !ok {
		return false
	}

	msg.client.sessionid = msg.sessionid
	return true
}

func (h *Hub) processClientMessage(message interface{}) bool {
	msg, ok := message.(ClientMessage)
	if !ok {
		return false
	}
	h.sendMessageToClient(msg.client, msg.message)
	return true
}

func (h *Hub) processClientErrorMessage(message interface{}) bool {
	msg, ok := message.(ClientErrorMessage)
	if !ok {
		return false
	}
	h.errorMessageToClient(msg.client, msg.message, msg.nextscreen)
	return true
}

func (h *Hub) processMessage(m *ClientCommand) {
	log.Printf("cmd=%s, arg=%s", m.cmd, m.arg)

	h.msghub.Send(incomingMessageTopic, m)
}

// this is only called from the REST API
func (h *Hub) SendClientsToScreen(sessionids []string, screen string) {
	for _, id := range sessionids {
		h.msghub.Send(sessionsTopic, SessionToScreenMessage{
			sessionid:  id,
			nextscreen: screen,
		})
	}
}

// this is only called from the REST API
func (h *Hub) RemoveGameFromSessions(sessionids []string) {
	h.msghub.Send(sessionsTopic, DeregisterGameFromSessionsMessage{
		sessions: sessionids,
	})
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
