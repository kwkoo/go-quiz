package pkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

const reaperInterval = 60

type Session struct {
	Id      string    `json:"id"`
	Client  *Client   `json:"client"`
	Screen  string    `json:"screen"`
	Gamepin int       `json:"gamepin"`
	Name    string    `json:"name"`
	Admin   bool      `json:"admin"`
	Expiry  time.Time `json:"expiry"`
}

func unmarshalSession(b []byte) (*Session, error) {
	var session Session
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&session); err != nil {
		return nil, fmt.Errorf("error unmarshaling bytes to session: %v", err)
	}
	return &session, nil
}

func (s Session) marshal() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(&s); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (s *Session) copy() Session {
	return Session{
		Id:      s.Id,
		Client:  s.Client,
		Screen:  s.Screen,
		Gamepin: s.Gamepin,
		Name:    s.Name,
		Admin:   s.Admin,
		Expiry:  s.Expiry,
	}
}

type Sessions struct {
	msghub         *MessageHub
	mutex          sync.RWMutex
	all            map[string]*Session
	engine         *PersistenceEngine
	auth           *Auth
	sessionTimeout int
}

func InitSessions(msghub *MessageHub, engine *PersistenceEngine, auth *Auth, sessionTimeout int) *Sessions {
	log.Printf("session timeout set to %d seconds", sessionTimeout)

	sessions := Sessions{
		msghub:         msghub,
		all:            make(map[string]*Session),
		engine:         engine,
		auth:           auth,
		sessionTimeout: sessionTimeout,
	}

	keys, err := engine.GetKeys("session")
	if err != nil {
		log.Printf("error retrieving session keys from persistent store: %v", err)
		return &sessions
	}

	log.Printf("persistent store contains %d sessions - clearing clients from all sessions...", len(keys))
	for _, key := range keys {
		key = key[len("session:"):]
		sessions.UpdateClientForSession(key, nil)
	}

	go func(shutdownChan chan struct{}) {
		for {
			select {
			case <-shutdownChan:
				log.Printf("shutting down session reaper")
				msghub.NotifyShutdownComplete()
				return
			case <-time.After(reaperInterval * time.Second):
				sessions.expireSessions()
			}
		}
	}(msghub.GetShutdownChan())

	return &sessions
}

func (s *Sessions) Run() {
	shutdownChan := s.msghub.GetShutdownChan()
	fromClients := s.msghub.GetTopic(incomingMessageTopic)
	sessionsHub := s.msghub.GetTopic(sessionsTopic)

	for {
		select {
		case msg, ok := <-fromClients:
			if !ok {
				log.Print("received empty message from from-clients")
				continue
			}
			if s.processClientCommand(msg) {
				continue
			}
		case msg, ok := <-sessionsHub:
			if !ok {
				log.Print("received empty message from sessions-hub")
				continue
			}
			if s.processErrorToSessionMessage(msg) {
				continue
			}
			if s.processBindGameToSessionMessage(msg) {
				continue
			}
			if s.processSessionToScreenMessage(msg) {
				continue
			}
			if s.processSetSessionScreenMessage(msg) {
				continue
			}
			if s.processSessionMessage(msg) {
				continue
			}
			if s.processSetSessionGamePinMessage(msg) {
				continue
			}
			if s.processDeregisterGameFromSessionsMessage(msg) {
				continue
			}
			if s.processSetSessionIDForClientMessage(msg) {
				continue
			}
		case <-shutdownChan:
			s.msghub.NotifyShutdownComplete()
			return
		}
	}
}

func (s *Sessions) processSetSessionIDForClientMessage(message interface{}) bool {
	msg, ok := message.(SetSessionIDForClientMessage)
	if !ok {
		return false
	}

	s.UpdateClientForSession(msg.sessionid, msg.client)
	return true
}

func (s *Sessions) processDeregisterGameFromSessionsMessage(message interface{}) bool {
	msg, ok := message.(DeregisterGameFromSessionsMessage)
	if !ok {
		return false
	}
	for _, sessionid := range msg.sessions {
		s.DeregisterGameFromSession(sessionid)
	}
	return true
}

func (s *Sessions) processSetSessionGamePinMessage(message interface{}) bool {
	msg, ok := message.(SetSessionGamePinMessage)
	if !ok {
		return false
	}
	s.SetSessionGamePin(msg.sessionid, msg.pin)
	return true
}

func (s *Sessions) processSessionMessage(message interface{}) bool {
	msg, ok := message.(SessionMessage)
	if !ok {
		return false
	}

	session := s.GetSession(msg.sessionid)
	if session == nil {
		// session doesn't exist
		return true
	}
	s.msghub.Send(clientHubTopic, ClientMessage{
		client:  session.Client,
		message: msg.message,
	})
	return true
}

func (s *Sessions) processSetSessionScreenMessage(message interface{}) bool {
	msg, ok := message.(SetSessionScreenMessage)
	if !ok {
		return false
	}
	s.SetSessionScreen(msg.sessionid, msg.nextscreen)

	return true
}

// returns true if argument is SessionToScreenMessage
func (s *Sessions) processSessionToScreenMessage(message interface{}) bool {
	msg, ok := message.(SessionToScreenMessage)
	if !ok {
		return false
	}

	session := s.GetSession(msg.sessionid)
	if session == nil {
		// session doesn't exist
		log.Print("*** session doesn't exist")
		return true
	}

	// session is valid from this point on

	// ensure that session is admin if trying to access host screens
	if strings.HasPrefix(msg.nextscreen, "host") && !session.Admin {
		msg.nextscreen = "authenticate-user"
	}

	switch msg.nextscreen {

	case "host-select-quiz":
		s.msghub.Send(quizzesTopic, SendQuizzesToClientMessage{
			client:    session.Client,
			sessionid: session.Id,
		})

	case "host-game-lobby":
		s.msghub.Send(gamesTopic, SendGameMetadataMessage{
			client:    session.Client,
			sessionid: session.Id,
			pin:       session.Gamepin,
		})

	case "host-show-question":
		s.msghub.Send(gamesTopic, HostShowQuestionMessage{
			client:    session.Client,
			sessionid: session.Id,
			pin:       session.Gamepin,
		})

	case "host-show-game-results":
		s.msghub.Send(gamesTopic, HostShowGameResultsMessage{
			client:    session.Client,
			sessionid: session.Id,
			pin:       session.Gamepin,
		})

		// end of switch
	}

	s.SetSessionScreen(session.Id, msg.nextscreen)

	s.msghub.Send(clientHubTopic, ClientMessage{
		client:  session.Client,
		message: "screen " + msg.nextscreen,
	})

	return true
}

// returns true if argument is BindGameToSessionMessage
func (s *Sessions) processBindGameToSessionMessage(message interface{}) bool {
	msg, ok := message.(BindGameToSessionMessage)
	if !ok {
		return false
	}

	s.RegisterSessionInGame(msg.sessionid, msg.name, msg.pin)
	return true
}

// returns true if argument is ErrorToSessionMessage
func (s *Sessions) processErrorToSessionMessage(message interface{}) bool {
	msg, ok := message.(ErrorToSessionMessage)
	if !ok {
		return false
	}

	if msg.nextscreen != "" {
		s.SetSessionScreen(msg.sessionid, msg.nextscreen)
	}

	client := s.GetClientForSession(msg.sessionid)
	if client == nil {
		// session is not bound to a client
		return true
	}

	s.msghub.Send(clientHubTopic, ClientErrorMessage{
		client:     client,
		sessionid:  msg.sessionid,
		message:    msg.message,
		nextscreen: msg.nextscreen,
	})
	return true
}

// returns true if argument is *ClientCommand
func (s *Sessions) processClientCommand(msg interface{}) bool {
	m, ok := msg.(*ClientCommand)
	if !ok {
		return false
	}

	if m.client.sessionid == "" {
		// client hasn't identified themselves yet
		if m.cmd == "session" {
			if len(m.arg) == 0 || len(m.arg) > 64 {
				s.msghub.Send(clientHubTopic, ClientErrorMessage{
					client:     m.client,
					sessionid:  "",
					message:    "invalid session ID",
					nextscreen: "entrance",
				})
				return true
			}

			client := m.client
			sessionid := m.arg
			s.msghub.Send(clientHubTopic, SetSessionIDForClientMessage{
				client:    client,
				sessionid: sessionid,
			})

			session := s.GetSession(sessionid)
			if session == nil {
				session = s.NewSession(sessionid, m.client, "entrance")
			} else {
				if session.Client != nil {
					s.msghub.Send(clientHubTopic, ClientErrorMessage{
						client:     m.client,
						sessionid:  "",
						message:    "you have another active session - disconnect that session before reconnecting",
						nextscreen: "",
					})

					s.msghub.Send(clientHubTopic, SetSessionIDForClientMessage{
						client:    client,
						sessionid: "",
					})

					return true
				}
				s.UpdateClientForSession(session.Id, client)
			}
			s.msghub.Send(sessionsTopic, SessionToScreenMessage{
				sessionid:  sessionid,
				nextscreen: session.Screen,
			})
			return true
		}
		s.msghub.Send(clientHubTopic, ClientMessage{
			client:  m.client,
			message: "register-session",
		})
		return true
	}

	client := m.client
	sessionid := client.sessionid
	session := s.GetSession(sessionid)

	if session == nil {
		s.msghub.Send(clientHubTopic, SetSessionIDForClientMessage{
			client:    client,
			sessionid: "",
		})
		s.msghub.Send(clientHubTopic, ClientErrorMessage{
			client:     m.client,
			sessionid:  "",
			message:    "session does not exist",
			nextscreen: "",
		})

		return true
	}

	// session is valid from this point on

	switch m.cmd {

	case "admin-login":
		if s.AuthenticateAdmin(sessionid, m.arg) {
			s.msghub.Send(sessionsTopic, SessionToScreenMessage{
				sessionid:  sessionid,
				nextscreen: "host-select-quiz",
			})

			return true
		}

		// invalid credentials
		s.msghub.Send(clientHubTopic, ClientMessage{
			client:  client,
			message: "invalid-credentials",
		})
		return true

	case "join-game":
		pinfo := struct {
			Pin  int    `json:"pin"`
			Name string `json:"name"`
		}{}
		dec := json.NewDecoder(strings.NewReader(m.arg))
		if err := dec.Decode(&pinfo); err != nil {
			s.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "could not decode json: " + err.Error(),
				nextscreen: "entrance",
			})
			return true
		}
		if len(pinfo.Name) == 0 {
			s.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "name is missing",
				nextscreen: "entrance",
			})
			return true
		}

		s.msghub.Send(gamesTopic, AddPlayerToGameMessage{
			sessionid: sessionid,
			name:      pinfo.Name,
			pin:       pinfo.Pin,
		})

		return true

	case "query-display-choices":
		// player may have been disconnected - now they need to know how many
		// answers to enable
		if session.Gamepin < 0 {
			s.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "could not get game pin for this session",
				nextscreen: "entrance",
			})
			return true
		}
		s.msghub.Send(gamesTopic, QueryDisplayChoicesMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return true

	case "query-player-results":
		// player may have been disconnected - now they need to know about
		// their results
		if session.Gamepin < 0 {
			s.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "could not get game pin for this session",
				nextscreen: "entrance",
			})
			return true
		}

		s.msghub.Send(gamesTopic, QueryPlayerResultsMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return true

	case "answer":
		playerAnswer, err := strconv.Atoi(m.arg)
		if err != nil {
			s.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "could not parse answer",
				nextscreen: "",
			})
			return true
		}

		if session.Gamepin < 0 {
			s.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "could not get game pin for this session",
				nextscreen: "entrance",
			})
			return true
		}

		s.msghub.Send(gamesTopic, RegisterAnswerMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
			answer:    playerAnswer,
		})
		return true

	case "host-back-to-start":
		s.msghub.Send(sessionsTopic, SessionToScreenMessage{
			sessionid:  sessionid,
			nextscreen: "entrance",
		})
		return true

	case "cancel-game":
		s.msghub.Send(gamesTopic, CancelGameMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return true

	case "host-game":
		s.msghub.Send(sessionsTopic, SessionToScreenMessage{
			sessionid:  sessionid,
			nextscreen: "host-select-quiz",
		})
		return true

	case "host-game-lobby":
		quizid, err := strconv.Atoi(m.arg)
		if err != nil {
			s.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "expected int argument",
				nextscreen: "host-select-quiz",
			})
			return true
		}

		s.msghub.Send(gamesTopic, HostGameLobbyMessage{
			client:    client,
			sessionid: sessionid,
			quizid:    quizid,
		})
		return true

	case "start-game":
		s.msghub.Send(gamesTopic, StartGameMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return true

	case "show-results":
		s.msghub.Send(gamesTopic, ShowResultsMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return true

	case "query-host-results":
		s.msghub.Send(gamesTopic, QueryHostResultsMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return true

	case "next-question":
		s.msghub.Send(gamesTopic, NextQuestionMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return true

	case "delete-game":
		s.msghub.Send(gamesTopic, DeleteGameMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return true

	default:
		s.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    "invalid command",
			nextscreen: "",
		})
		return true
	}
	return true
}

func (s *Sessions) NewSession(id string, client *Client, screen string) *Session {
	session := &Session{
		Id:     id,
		Client: client,
		Screen: screen,
		Expiry: time.Now().Add(time.Duration(s.sessionTimeout) * time.Second),
	}

	s.mutex.Lock()
	s.all[id] = session
	s.mutex.Unlock()

	s.persist(session)

	_, ok := s.all[id]
	log.Printf("*** created session %s %v", id, ok)

	return session
}

func (s *Sessions) ExtendSessionExpiry(id string) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.persist(session)
}

func (s *Sessions) expireSessions() {
	now := time.Now()
	s.mutex.Lock()
	for id, session := range s.all {
		if now.After(session.Expiry) {
			delete(s.all, id)
			log.Printf("expiring session %s", id)
		}
	}
	s.mutex.Unlock()
}

func (s *Sessions) persist(session *Session) {
	s.mutex.Lock()
	session.Expiry = time.Now().Add(time.Duration(s.sessionTimeout) * time.Second)
	s.mutex.Unlock()

	if s.engine == nil {
		return
	}

	data, err := session.marshal()
	if err != nil {
		log.Printf("error encoding session %s to JSON: %v", session.Id, err)
		return
	}

	if err := s.engine.Set(fmt.Sprintf("session:%s", session.Id), data, s.sessionTimeout); err != nil {
		log.Printf("error persisting session %s to redis: %v", session.Id, err)
	}
}

func (s *Sessions) getAll() []Session {
	all := []Session{}
	s.mutex.RLock()
	for _, v := range s.all {
		all = append(all, v.copy())
	}
	s.mutex.RUnlock()
	return all
}

func (s *Sessions) DeleteSession(id string) {
	s.mutex.Lock()
	delete(s.all, id)
	s.mutex.Unlock()

	s.engine.Delete(fmt.Sprintf("session:%s", id))
}

func (s *Sessions) GetScreenForSession(id string) string {
	session := s.GetSession(id)

	if session == nil {
		return ""
	}

	return session.Screen
}

func (s *Sessions) GetGamePinForSession(id string) int {
	session := s.GetSession(id)

	if session == nil {
		return -1
	}

	return session.Gamepin
}

func (s *Sessions) GetNameForSession(id string) string {
	session := s.GetSession(id)

	if session == nil {
		return ""
	}

	return session.Name
}

func (s *Sessions) GetClientForSession(id string) *Client {
	session := s.GetSession(id)

	if session == nil {
		return nil
	}

	return session.Client
}

func (s *Sessions) UpdateClientForSession(id string, newclient *Client) {
	if id == "" {
		return
	}
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Client = newclient
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) GetSession(id string) *Session {
	s.mutex.RLock()
	session, ok := s.all[id]
	s.mutex.RUnlock()

	if ok {
		return session
	}

	if s.engine == nil {
		return nil
	}

	// session doesn't exist in memory - check if it's available in the
	// storage engine
	key := fmt.Sprintf("session:%s", id)
	data, err := s.engine.Get(key)
	if err != nil {
		return nil
	}

	decoded, err := unmarshalSession(data)
	if err != nil {
		log.Printf("error decoding session from redis: %v", err)
		return nil
	}

	s.mutex.Lock()
	s.all[id] = decoded
	s.mutex.Unlock()
	return decoded
}

func (s *Sessions) RegisterSessionInGame(id, name string, pin int) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Name = name
	session.Gamepin = pin
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) DeregisterGameFromSession(id string) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Gamepin = -1
	session.Screen = "entrance"
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) SetSessionScreen(id, screen string) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Screen = screen
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) SetSessionGamePin(id string, pin int) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Gamepin = pin
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) ConvertSessionIdsToNames(ids []string) []string {
	names := []string{}

	for _, id := range ids {
		session := s.GetSession(id)
		if session == nil {
			continue
		}
		names = append(names, session.Name)
	}

	return names
}

func (s *Sessions) SessionIsAdmin(id string) bool {
	session := s.GetSession(id)
	if session == nil {
		return false
	}
	return session.Admin
}

// Credentials is in the basic auth format (base64 encoding of
// username:password).
// Returns true if user is authenticated.
func (s *Sessions) AuthenticateAdmin(id, credentials string) bool {
	session := s.GetSession(id)
	if session.Admin {
		return true
	}
	if s.auth.Base64Authenticated(credentials) {
		s.mutex.Lock()
		session.Admin = true
		s.mutex.Unlock()
		s.persist(session)
		return true
	}
	return false
}
