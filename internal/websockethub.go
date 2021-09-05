// Copied from https://github.com/gorilla/websocket/blob/master/examples/chat/hub.go
// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package internal

import (
	"log"
	"math"
	"sync"

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

	persistenceengine *PersistenceEngine
}

func NewHub(msghub *messaging.MessageHub, persistenceEngine *PersistenceEngine) *Hub {
	return &Hub{
		incomingcommands:  make(chan *ClientCommand),
		register:          make(chan *Client),
		unregister:        make(chan *Client),
		clients:           make(map[*Client]bool),
		clientids:         make(map[uint64]*Client),
		msghub:            msghub,
		persistenceengine: persistenceEngine,
	}
}

func (h *Hub) ClosePersistenceEngine() {
	h.persistenceengine.Close()
}

func (h *Hub) Run(shutdownChan chan struct{}) {
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
			case common.ClientMessage:
				h.processClientMessage(m)
			case common.ClientErrorMessage:
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

	h.msghub.Send(messaging.SessionsTopic, common.DeregisterClientMessage{
		Clientid: client.clientid,
	})
}

func (h *Hub) processClientMessage(msg common.ClientMessage) {
	c, ok := h.clientids[msg.Clientid]
	if !ok {
		return
	}

	h.sendMessageToClient(c, msg.Message)
}

func (h *Hub) processClientErrorMessage(msg common.ClientErrorMessage) {
	c, ok := h.clientids[msg.Clientid]
	if !ok {
		return
	}

	h.errorMessageToClient(c, msg.Message, msg.Nextscreen)
}

func (h *Hub) processMessage(m *ClientCommand) {
	log.Printf("cmd=%s, arg=%s", m.cmd, m.arg)

	h.msghub.Send(messaging.IncomingMessageTopic, m)
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
