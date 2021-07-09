// Copied from https://github.com/gorilla/websocket/blob/master/examples/chat/client.go
// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte

	sessionid string
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))

		c.hub.incomingcommands <- NewClientCommand(c, message)
	}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued chat messages to the current websocket message.
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) sendMessage(s string) {
	c.send <- []byte(s)
}

func (c *Client) errorMessage(s string) {
	c.sendMessage("error " + s)
}

func (c *Client) screen(s string) {
	session := c.hub.sessions.GetSession(c.sessionid)
	if session == nil {
		c.errorMessage("session does not exist anymore")
		return
	}
	switch s {
	case "select-quiz":
		type meta struct {
			Id   int    `json:"id"`
			Name string `json:"name"`
		}
		ml := []meta{}
		for _, q := range c.hub.quizzes.GetQuizzes() {
			ml = append(ml, meta{
				Id:   q.Id,
				Name: q.Name,
			})
		}

		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		if err := enc.Encode(&ml); err != nil {
			c.errorMessage(fmt.Sprintf("error encoding json: %v", err))
			return
		}
		c.sendMessage("all-quizzes " + b.String())

	case "game-lobby":
		// send over game object with lobby-game-metadata
		game, err := c.hub.games.Get(session.gamepin)
		if err != nil {
			c.errorMessage(fmt.Sprintf("could not retrieve game %d", session.gamepin))
			return
		}

		gameMetadata := struct {
			Pin     int      `json:"pin"`
			Host    string   `json:"host"`
			Players []string `json:"players"`
		}{
			Pin:  game.Pin,
			Host: game.Host, // todo: set to name
		}
		playerids := []string{}
		for k := range game.Players {
			playerids = append(playerids, k)
		}
		gameMetadata.Players = c.hub.sessions.ConvertSessionIdsToNames(playerids)

		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		if err := enc.Encode(&gameMetadata); err != nil {
			c.errorMessage("JSON encoding error: " + err.Error())
			return
		}
		c.sendMessage("lobby-game-metadata " + b.String())

	case "show-question":
		session := c.hub.sessions.GetSession(c.sessionid)
		if session == nil {
			c.errorMessage("could not get session")
			return
		}
		questionIndex, secondsLeft, quizQuestion, err := c.hub.games.GetCurrentQuestion(session.gamepin)
		if err != nil {
			c.errorMessage("error retrieving question: " + err.Error())
			return
		}
		hostQuestion := struct {
			QuestionIndex int      `json:"questionindex"`
			TimeLeft      int      `json:"timeleft"`
			Question      string   `json:"question"`
			Answers       []string `json:"answers"`
		}{
			QuestionIndex: questionIndex,
			TimeLeft:      secondsLeft,
			Question:      quizQuestion.Question,
			Answers:       quizQuestion.Answers,
		}

		encoded, err := convertToJSON(&hostQuestion)
		if err != nil {
			c.errorMessage("error converting question to JSON: " + err.Error())
			return
		}
		c.sendMessage("show-question " + encoded)

		// The logic for answer-question is in the hub
		//case "answer-question":

	}

	c.hub.sessions.UpdateScreenForSession(c.sessionid, s)
	c.sendMessage("screen " + s)
}

func convertToJSON(input interface{}) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(input); err != nil {
		return "", err
	}
	return b.String(), nil
}

// ServeWs handles websocket requests from the peer.
func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	client := &Client{hub: hub, conn: conn, send: make(chan []byte, 256)}
	client.hub.register <- client

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
}
