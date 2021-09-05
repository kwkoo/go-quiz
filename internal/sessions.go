package internal

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kwkoo/go-quiz/internal/api"
	"github.com/kwkoo/go-quiz/internal/common"
	"github.com/kwkoo/go-quiz/internal/messaging"
	"github.com/kwkoo/go-quiz/internal/shutdown"
)

const reaperInterval = 60

type Sessions struct {
	msghub         *messaging.MessageHub
	mutex          sync.RWMutex
	all            map[string]*common.Session
	clientids      map[uint64]*common.Session
	engine         *PersistenceEngine
	auth           *api.Auth
	sessionTimeout int
}

func InitSessions(msghub *messaging.MessageHub, engine *PersistenceEngine, auth *api.Auth, sessionTimeout int) *Sessions {
	log.Printf("session timeout set to %d seconds", sessionTimeout)

	sessions := Sessions{
		msghub:         msghub,
		all:            make(map[string]*common.Session),
		clientids:      make(map[uint64]*common.Session),
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
		sessions.updateClientForSession(key, 0)
	}

	// session reaper
	go func() {
		shutdownChan := shutdown.GetShutdownChan()
		timeout := time.After(reaperInterval * time.Second)
		for {
			select {
			case <-shutdownChan:
				log.Printf("shutting down session reaper")
				shutdown.NotifyShutdownComplete()
				return
			case <-timeout:
				log.Print("running session reaper")
				sessions.expireSessions()
				timeout = time.After(reaperInterval * time.Second)
			default:
				time.Sleep(3 * time.Second)
			}
		}
	}()

	return &sessions
}

func (s *Sessions) Run() {
	shutdownChan := shutdown.GetShutdownChan()
	fromClients := s.msghub.GetTopic(messaging.IncomingMessageTopic)
	sessionsHub := s.msghub.GetTopic(messaging.SessionsTopic)

	for {
		select {
		case msg, ok := <-fromClients:
			if !ok {
				log.Printf("received empty message from %s", messaging.IncomingMessageTopic)
				continue
			}
			switch m := msg.(type) {
			case *ClientCommand:
				s.processClientCommand(m)
			default:
				log.Printf("unrecognized message type %T received on %s topic", msg, messaging.IncomingMessageTopic)
			}
		case msg, ok := <-sessionsHub:
			if !ok {
				log.Printf("received empty message from %s", messaging.SessionsTopic)
				continue
			}
			switch m := msg.(type) {
			case ErrorToSessionMessage:
				s.processErrorToSessionMessage(m)
			case BindGameToSessionMessage:
				s.processBindGameToSessionMessage(m)
			case SessionToScreenMessage:
				s.processSessionToScreenMessage(m)
			case SetSessionScreenMessage:
				s.processSetSessionScreenMessage(m)
			case SessionMessage:
				s.processSessionMessage(m)
			case SetSessionGamePinMessage:
				s.processSetSessionGamePinMessage(m)
			case DeregisterGameFromSessionsMessage:
				s.processDeregisterGameFromSessionsMessage(m)
			/*
				case SetSessionIDForClientMessage:
					s.processSetSessionIDForClientMessage(m)
			*/
			case ExtendSessionExpiryMessage:
				s.processExtendSessionExpiryMessage(m)
			case DeleteSessionMessage:
				s.processDeleteSessionMessage(m)
			case DeregisterClientMessage:
				s.processDeregisterClientMessage(m)
			default:
				log.Printf("unrecognized message type %T received on %s topic", msg, messaging.SessionsTopic)
			}
		case <-shutdownChan:
			log.Print("shutting down sessions handler")
			shutdown.NotifyShutdownComplete()
			return
		}
	}
}

func (s *Sessions) processDeregisterClientMessage(msg DeregisterClientMessage) {
	log.Printf("session deregister client %d", msg.clientid)
	session, ok := s.clientids[msg.clientid]
	if ok {
		//session.Client = 0
		s.updateClientForSession(session.Id, 0)
	}

	delete(s.clientids, msg.clientid)
}

func (s *Sessions) processDeleteSessionMessage(msg DeleteSessionMessage) {
	s.deleteSession(msg.sessionid)
}

/*
func (s *Sessions) processSetSessionIDForClientMessage(msg SetSessionIDForClientMessage) {
	s.updateClientForSession(msg.sessionid, msg.client)
}
*/

func (s *Sessions) processDeregisterGameFromSessionsMessage(msg DeregisterGameFromSessionsMessage) {
	for _, sessionid := range msg.sessions {
		s.deregisterGameFromSession(sessionid)
	}
}

func (s *Sessions) processSetSessionGamePinMessage(msg SetSessionGamePinMessage) {
	s.setSessionGamePin(msg.sessionid, msg.pin)
}

func (s *Sessions) processSessionMessage(msg SessionMessage) {
	sess := s.GetSession(msg.sessionid)
	if sess == nil {
		// session doesn't exist
		log.Printf("session %s does not exist", msg.sessionid)
		return
	}
	s.msghub.Send(messaging.ClientHubTopic, ClientMessage{
		client:  sess.Client,
		message: msg.message,
	})
}

func (s *Sessions) processSetSessionScreenMessage(msg SetSessionScreenMessage) {
	s.setSessionScreen(msg.sessionid, msg.nextscreen)
}

func (s *Sessions) processSessionToScreenMessage(msg SessionToScreenMessage) {
	session := s.GetSession(msg.sessionid)
	if session == nil {
		// session doesn't exist
		log.Printf("session %s does not exist", msg.sessionid)
		return
	}

	// session is valid from this point on

	// ensure that session is admin if trying to access host screens
	if strings.HasPrefix(msg.nextscreen, "host") && !session.Admin {
		msg.nextscreen = "authenticate-user"
	}

	switch msg.nextscreen {

	case "host-select-quiz":
		s.msghub.Send(messaging.QuizzesTopic, SendQuizzesToClientMessage{
			client:    session.Client,
			sessionid: session.Id,
		})

	case "host-game-lobby":
		s.msghub.Send(messaging.GamesTopic, SendGameMetadataMessage{
			client:    session.Client,
			sessionid: session.Id,
			pin:       session.Gamepin,
		})

	case "host-show-question":
		s.msghub.Send(messaging.GamesTopic, HostShowQuestionMessage{
			client:    session.Client,
			sessionid: session.Id,
			pin:       session.Gamepin,
		})

	case "host-show-game-results":
		s.msghub.Send(messaging.GamesTopic, HostShowGameResultsMessage{
			client:    session.Client,
			sessionid: session.Id,
			pin:       session.Gamepin,
		})

		// end of switch
	}

	s.setSessionScreen(session.Id, msg.nextscreen)

	s.msghub.Send(messaging.ClientHubTopic, ClientMessage{
		client:  session.Client,
		message: "screen " + msg.nextscreen,
	})
}

func (s *Sessions) processBindGameToSessionMessage(msg BindGameToSessionMessage) {
	s.registerSessionInGame(msg.sessionid, msg.name, msg.pin)
}

func (s *Sessions) processErrorToSessionMessage(msg ErrorToSessionMessage) {
	if msg.nextscreen != "" {
		s.setSessionScreen(msg.sessionid, msg.nextscreen)
	}

	client := s.getClientForSession(msg.sessionid)
	if client == 0 {
		// session is not bound to a client
		log.Printf("session %s does not have a client", msg.sessionid)
		return
	}

	s.msghub.Send(messaging.ClientHubTopic, ClientErrorMessage{
		client:     client,
		sessionid:  msg.sessionid,
		message:    msg.message,
		nextscreen: msg.nextscreen,
	})
}

func (s *Sessions) processExtendSessionExpiryMessage(msg ExtendSessionExpiryMessage) {
	s.extendSessionExpiry(msg.sessionid)
}

func (s *Sessions) processClientCommand(m *ClientCommand) {
	session, ok := s.clientids[m.client]
	if !ok {
		// client hasn't identified themselves yet
		if m.cmd == "session" {
			if len(m.arg) == 0 || len(m.arg) > 64 {
				s.msghub.Send(messaging.ClientHubTopic, ClientErrorMessage{
					client:     m.client,
					sessionid:  "",
					message:    "invalid session ID",
					nextscreen: "entrance",
				})
				return
			}

			clientid := m.client
			sessionid := m.arg

			session := s.GetSession(sessionid)
			if session == nil {
				session = s.newSession(sessionid, m.client, "entrance")
			} else {
				if session.Client != 0 {
					s.msghub.Send(messaging.ClientHubTopic, ClientErrorMessage{
						client:     m.client,
						sessionid:  "",
						message:    "you have another active session - disconnect that session before reconnecting",
						nextscreen: "",
					})

					return
				}
				s.updateClientForSession(session.Id, clientid)
			}
			s.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
				sessionid:  sessionid,
				nextscreen: session.Screen,
			})
			return
		}
		s.msghub.Send(messaging.ClientHubTopic, ClientMessage{
			client:  m.client,
			message: "register-session",
		})
		return
	}

	sessionid := ""
	if session != nil {
		sessionid = session.Id
	}

	client := m.client

	if session == nil {
		/*
			s.msghub.Send(messaging.ClientHubTopic, SetSessionIDForClientMessage{
				client:    client,
				sessionid: "",
			})
		*/
		s.msghub.Send(messaging.ClientHubTopic, ClientErrorMessage{
			client:     m.client,
			sessionid:  "",
			message:    "session does not exist",
			nextscreen: "",
		})

		return
	}

	// session is valid from this point on

	switch m.cmd {

	case "admin-login":
		if s.authenticateAdmin(sessionid, m.arg) {
			s.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
				sessionid:  sessionid,
				nextscreen: "host-select-quiz",
			})

			return
		}

		// invalid credentials
		s.msghub.Send(messaging.ClientHubTopic, ClientMessage{
			client:  client,
			message: "invalid-credentials",
		})
		return

	case "join-game":
		pinfo := struct {
			Pin  int    `json:"pin"`
			Name string `json:"name"`
		}{}
		dec := json.NewDecoder(strings.NewReader(m.arg))
		if err := dec.Decode(&pinfo); err != nil {
			s.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "could not decode json: " + err.Error(),
				nextscreen: "entrance",
			})
			return
		}
		if len(pinfo.Name) == 0 {
			s.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "name is missing",
				nextscreen: "entrance",
			})
			return
		}

		s.msghub.Send(messaging.GamesTopic, AddPlayerToGameMessage{
			sessionid: sessionid,
			name:      pinfo.Name,
			pin:       pinfo.Pin,
		})

		return

	case "query-display-choices":
		// player may have been disconnected - now they need to know how many
		// answers to enable
		if session.Gamepin < 0 {
			s.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "could not get game pin for this session",
				nextscreen: "entrance",
			})
			return
		}
		s.msghub.Send(messaging.GamesTopic, QueryDisplayChoicesMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return

	case "query-player-results":
		// player may have been disconnected - now they need to know about
		// their results
		if session.Gamepin < 0 {
			s.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "could not get game pin for this session",
				nextscreen: "entrance",
			})
			return
		}

		s.msghub.Send(messaging.GamesTopic, QueryPlayerResultsMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return

	case "answer":
		playerAnswer, err := strconv.Atoi(m.arg)
		if err != nil {
			s.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "could not parse answer",
				nextscreen: "",
			})
			return
		}

		if session.Gamepin < 0 {
			s.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "could not get game pin for this session",
				nextscreen: "entrance",
			})
			return
		}

		s.msghub.Send(messaging.GamesTopic, RegisterAnswerMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
			answer:    playerAnswer,
		})
		return

	case "host-back-to-start":
		s.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
			sessionid:  sessionid,
			nextscreen: "entrance",
		})
		return

	case "cancel-game":
		s.msghub.Send(messaging.GamesTopic, CancelGameMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return

	case "host-game":
		s.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
			sessionid:  sessionid,
			nextscreen: "host-select-quiz",
		})
		return

	case "host-game-lobby":
		quizid, err := strconv.Atoi(m.arg)
		if err != nil {
			s.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    "expected int argument",
				nextscreen: "host-select-quiz",
			})
			return
		}

		s.msghub.Send(messaging.GamesTopic, HostGameLobbyMessage{
			client:    client,
			sessionid: sessionid,
			quizid:    quizid,
		})
		return

	case "start-game":
		s.msghub.Send(messaging.GamesTopic, StartGameMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return

	case "show-results":
		s.msghub.Send(messaging.GamesTopic, ShowResultsMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return

	case "query-host-results":
		s.msghub.Send(messaging.GamesTopic, QueryHostResultsMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return

	case "next-question":
		s.msghub.Send(messaging.GamesTopic, NextQuestionMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return

	case "delete-game":
		s.msghub.Send(messaging.GamesTopic, DeleteGameMessage{
			client:    client,
			sessionid: sessionid,
			pin:       session.Gamepin,
		})
		return

	default:
		s.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    "invalid command",
			nextscreen: "",
		})
		return
	}
}

func (s *Sessions) newSession(id string, clientid uint64, screen string) *common.Session {
	session := &common.Session{
		Id:     id,
		Client: clientid,
		Screen: screen,
		Expiry: time.Now().Add(time.Duration(s.sessionTimeout) * time.Second),
	}

	s.mutex.Lock()
	s.all[id] = session
	s.clientids[clientid] = session
	s.mutex.Unlock()

	s.persist(session)

	return session
}

func (s *Sessions) extendSessionExpiry(id string) {
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

func (s *Sessions) persist(session *common.Session) {
	s.mutex.Lock()
	session.Expiry = time.Now().Add(time.Duration(s.sessionTimeout) * time.Second)
	s.mutex.Unlock()

	if s.engine == nil {
		return
	}

	data, err := session.Marshal()
	if err != nil {
		log.Printf("error encoding session %s to JSON: %v", session.Id, err)
		return
	}

	if err := s.engine.Set(fmt.Sprintf("session:%s", session.Id), data, s.sessionTimeout); err != nil {
		log.Printf("error persisting session %s to redis: %v", session.Id, err)
	}
}

// called by REST API
func (s *Sessions) GetAll() []common.Session {
	all := []common.Session{}
	s.mutex.RLock()
	for _, v := range s.all {
		all = append(all, v.Copy())
	}
	s.mutex.RUnlock()
	return all
}

func (s *Sessions) deleteSession(id string) {
	s.mutex.Lock()
	delete(s.all, id)
	s.mutex.Unlock()

	s.engine.Delete(fmt.Sprintf("session:%s", id))
}

func (s *Sessions) getClientForSession(id string) uint64 {
	session := s.GetSession(id)

	if session == nil {
		return 0
	}

	return session.Client
}

func (s *Sessions) updateClientForSession(id string, newclientid uint64) {
	if id == "" {
		return
	}
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	oldclientid := session.Client
	if oldclientid != 0 {
		delete(s.clientids, oldclientid)
	}
	session.Client = newclientid
	if newclientid == 0 {
		delete(s.clientids, newclientid)
	} else {
		s.clientids[newclientid] = session
	}
	s.mutex.Unlock()
	s.persist(session)
}

// also called by REST API
func (s *Sessions) GetSession(id string) *common.Session {
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

	decoded, err := common.UnmarshalSession(data)
	if err != nil {
		log.Printf("error decoding session from redis: %v", err)
		return nil
	}

	s.mutex.Lock()
	s.all[id] = decoded
	if decoded.Client != 0 {
		s.clientids[decoded.Client] = decoded
	}
	s.mutex.Unlock()
	return decoded
}

func (s *Sessions) registerSessionInGame(id, name string, pin int) {
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

func (s *Sessions) deregisterGameFromSession(id string) {
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

func (s *Sessions) setSessionScreen(id, screen string) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Screen = screen
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) setSessionGamePin(id string, pin int) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Gamepin = pin
	s.mutex.Unlock()
	s.persist(session)
}

// Credentials is in the basic auth format (base64 encoding of
// username:password).
// Returns true if user is authenticated.
func (s *Sessions) authenticateAdmin(id, credentials string) bool {
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
