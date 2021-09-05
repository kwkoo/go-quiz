// Copied from https://github.com/gorilla/websocket/blob/master/examples/chat/hub.go
// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package internal

import (
	"log"
	"math"
	"sync"

	"github.com/kwkoo/go-quiz/internal/api"
	"github.com/kwkoo/go-quiz/internal/common"
	"github.com/kwkoo/go-quiz/internal/messaging"
	"github.com/kwkoo/go-quiz/internal/shutdown"
)

// Hub maintains the set of active clients and broadcasts messages to the
// clients.
type Hub struct {
	// For generation of client IDs
	nextclientid uint64
	clientmux    sync.Mutex

	// Registered clients.
	clients   map[*Client]bool
	clientids map[uint64]*Client

	// Inbound messages from the clients.
	incomingcommands chan *ClientCommand

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client

	msghub *messaging.MessageHub

	sessions *Sessions

	quizzes *Quizzes

	games *Games

	persistenceengine *PersistenceEngine
}

func NewHub(msghub *messaging.MessageHub, redisHost, redisPassword string, auth *api.Auth, sessionTimeout int) *Hub {
	persistenceEngine := InitRedis(redisHost, redisPassword)
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
		incomingcommands:  make(chan *ClientCommand),
		register:          make(chan *Client),
		unregister:        make(chan *Client),
		clients:           make(map[*Client]bool),
		clientids:         make(map[uint64]*Client),
		msghub:            msghub,
		sessions:          sessions,
		quizzes:           quizzes,
		games:             games,
		persistenceengine: persistenceEngine,
	}
}

func (h *Hub) ClosePersistenceEngine() {
	h.persistenceengine.Close()
}

func (h *Hub) Run() {
	shutdownChan := shutdown.GetShutdownChan()
	clientHub := h.msghub.GetTopic(messaging.ClientHubTopic)

	for {
		select {
		case <-shutdownChan:
			log.Print("websockethub received shutdown signal, exiting")
			shutdown.NotifyShutdownComplete()
			return

		case client := <-h.register:
			clientid := h.generateClientID()
			client.clientid = clientid
			h.clients[client] = true
			h.clientids[clientid] = client

		case client := <-h.unregister:
			h.deregisterClient(client)

		case message := <-h.incomingcommands:
			log.Printf("incoming command: %s, arg: %s", message.cmd, message.arg)
			h.processMessage(message)

		case msg, ok := <-clientHub:
			if !ok {
				log.Printf("received empty message from %s", messaging.ClientHubTopic)
				continue
			}
			switch m := msg.(type) {
			case ClientMessage:
				h.processClientMessage(m)
			case ClientErrorMessage:
				h.processClientErrorMessage(m)
			default:
				log.Printf("unrecognized message type %T received on %s topic", msg, messaging.ClientHubTopic)
			}
		}
	}
}

func (h *Hub) deregisterClient(client *Client) {
	if client == nil {
		return
	}

	delete(h.clients, client)
	delete(h.clientids, client.clientid)
	close(client.send)

	h.msghub.Send(messaging.SessionsTopic, DeregisterClientMessage{
		clientid: client.clientid,
	})

	/*
		if client.sessionid != "" {
			log.Printf("cleaned up client for session %s", client.sessionid)

			h.msghub.Send(messaging.SessionsTopic, SetSessionIDForClientMessage{
				sessionid: client.sessionid,
				client:    0,
			})
		}
	*/
}

func (h *Hub) processClientMessage(msg ClientMessage) {
	c, ok := h.clientids[msg.client]
	if !ok {
		return
	}

	h.sendMessageToClient(c, msg.message)
}

func (h *Hub) processClientErrorMessage(msg ClientErrorMessage) {
	c, ok := h.clientids[msg.client]
	if !ok {
		return
	}

	h.errorMessageToClient(c, msg.message, msg.nextscreen)
}

func (h *Hub) processMessage(m *ClientCommand) {
	log.Printf("cmd=%s, arg=%s", m.cmd, m.arg)

	h.msghub.Send(messaging.IncomingMessageTopic, m)
}

// this is only called from the REST API
func (h *Hub) SendClientsToScreen(sessionids []string, screen string) {
	for _, id := range sessionids {
		h.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
			sessionid:  id,
			nextscreen: screen,
		})
	}
}

// this is only called from the REST API
func (h *Hub) RemoveGameFromSessions(sessionids []string) {
	h.msghub.Send(messaging.SessionsTopic, DeregisterGameFromSessionsMessage{
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
	encoded, err := common.ConvertToJSON(data)
	if err != nil {
		log.Printf("error converting payload for error message: %v", err)
		return
	}
	h.sendMessageToClient(c, "error "+encoded)
}

func (h *Hub) generateClientID() uint64 {
	h.clientmux.Lock()
	defer h.clientmux.Unlock()

	if h.nextclientid == math.MaxUint64 {
		h.nextclientid = 0
	}
	h.nextclientid++
	return h.nextclientid
}

// used by the REST API
func (h *Hub) GetQuizzes() []common.Quiz {
	return h.quizzes.GetQuizzes()
}

// used by the REST API
func (h *Hub) GetQuiz(id int) (common.Quiz, error) {
	return h.quizzes.Get(id)
}

// used by the REST API
func (h *Hub) DeleteQuiz(id int) {
	h.msghub.Send(messaging.QuizzesTopic, DeleteQuizMessage{quizid: id})
}

// used by the REST API
func (h *Hub) AddQuiz(q common.Quiz) error {
	return h.quizzes.Add(q)
}

// used by the REST API
func (h *Hub) UpdateQuiz(q common.Quiz) error {
	return h.quizzes.Update(q)
}

// used by the REST API
func (h *Hub) ExtendSessionExpiry(id string) {
	h.msghub.Send(messaging.SessionsTopic, ExtendSessionExpiryMessage{
		sessionid: id,
	})
}

// used by the REST API
func (h *Hub) GetSessions() []common.Session {
	return h.sessions.GetAll()
}

// used by the REST API
func (h *Hub) GetSession(id string) *common.Session {
	return h.sessions.GetSession(id)
}

// used by the REST API
func (h *Hub) DeleteSession(id string) {
	h.msghub.Send(messaging.SessionsTopic, DeleteSessionMessage{
		sessionid: id,
	})
}

// used by the REST API
func (h *Hub) GetGames() []common.Game {
	return h.games.GetAll()
}

// used by the REST API
func (h *Hub) GetGame(id int) (common.Game, error) {
	return h.games.Get(id)
}

// used by the REST API
func (h *Hub) DeleteGame(id int) {
	h.msghub.Send(messaging.GamesTopic, DeleteGameByPin{pin: id})
}

// used by the REST API
func (h *Hub) UpdateGame(g common.Game) {
	h.msghub.Send(messaging.GamesTopic, g)
}
