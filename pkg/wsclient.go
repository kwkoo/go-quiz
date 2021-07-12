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
	case "hostselectquiz":
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

		encoded, err := convertToJSON(&ml)
		if err != nil {
			c.errorMessage(fmt.Sprintf("error encoding json: %v", err))
			return
		}
		c.sendMessage("all-quizzes " + encoded)

	case "hostgamelobby":
		// send over game object with lobby-game-metadata
		game, err := c.hub.games.Get(session.Gamepin)
		if err != nil {
			c.errorMessage(fmt.Sprintf("could not retrieve game %d", session.Gamepin))
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
		gameMetadata.Players = c.hub.sessions.ConvertSessionIdsToNames(playerids)

		encoded, err := convertToJSON(&gameMetadata)
		if err != nil {
			c.errorMessage("error converting lobby-game-metadata payload to JSON: " + err.Error())
			return
		}
		c.sendMessage("lobby-game-metadata " + encoded)

	case "hostshowquestion":
		session := c.hub.sessions.GetSession(c.sessionid)
		if session == nil {
			c.errorMessage("could not get session")
			return
		}

		currentQuestion, err := c.hub.games.GetCurrentQuestion(session.Gamepin)
		if err != nil {
			// if the host disconnected while the question was live, and if
			// the game state has now changed, we may need to move the host to
			// the relevant screen
			unexpectedState, ok := err.(*UnexpectedStateError)
			if ok && unexpectedState.CurrentState == ShowResults {
				c.hub.processMessage(&ClientCommand{
					client: c,
					cmd:    "show-results",
					arg:    "",
				})
				return
			}

			c.errorMessage("error retrieving question: " + err.Error())
			return
		}

		encoded, err := convertToJSON(&currentQuestion)
		if err != nil {
			c.errorMessage("error converting question to JSON: " + err.Error())
			return
		}
		c.sendMessage("hostshowquestion " + encoded)

		// The logic for answerquestion is in the hub
		//case "answerquestion":

	case "hostshowgameresults":
		session := c.hub.sessions.GetSession(c.sessionid)
		if session == nil {
			c.errorMessage("could not get session")
			return
		}

		winners, err := c.hub.games.GetWinners(session.Gamepin)
		if err != nil {
			c.errorMessage("error retrieving game winners: " + err.Error())
			return
		}
		type FriendlyScore struct {
			Name  string `json:"name"`
			Score int    `json:"score"`
		}
		fl := []FriendlyScore{}
		for _, w := range winners {
			session := c.hub.sessions.GetSession(w.Sessionid)
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
			c.errorMessage("error converting show-winners payload to JSON: " + err.Error())
			return
		}
		log.Printf("winners for game %d: %s", session.Gamepin, encoded)
		c.sendMessage("show-winners " + encoded)

		// end of switch
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
