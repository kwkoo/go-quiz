package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/kwkoo/go-quiz/internal/common"
	"github.com/kwkoo/go-quiz/internal/messaging"
)

type Quizzes struct {
	all    map[int]common.Quiz
	mutex  sync.RWMutex
	engine *PersistenceEngine
	msghub *messaging.MessageHub
}

func InitQuizzes(msghub *messaging.MessageHub, engine *PersistenceEngine) (*Quizzes, error) {
	if engine == nil {
		log.Print("initializing quizzes with no persistence engine")
		return &Quizzes{all: make(map[int]common.Quiz)}, nil
	}

	keys, err := engine.GetKeys("quiz")
	if err != nil {
		return nil, fmt.Errorf("could not retrieve keys from redis: %v", err)
	}

	all := make(map[int]common.Quiz)

	for _, key := range keys {
		data, err := engine.Get(key)
		if err != nil {
			log.Print(err.Error())
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		var quiz common.Quiz
		if err := dec.Decode(&quiz); err != nil {
			log.Printf("error parsing JSON from redis for key %s: %v", key, err)
			continue
		}
		all[quiz.Id] = quiz
	}

	log.Printf("ingested %d quizzes", len(all))
	return &Quizzes{
		all:    all,
		engine: engine,
		msghub: msghub,
	}, nil
}

func (q *Quizzes) Run() {
	shutdownChan := q.msghub.GetShutdownChan()
	topic := q.msghub.GetTopic(messaging.QuizzesTopic)
	for {
		select {
		case <-shutdownChan:
			q.msghub.NotifyShutdownComplete()
			return
		case msg, ok := <-topic:
			if !ok {
				log.Print("received empty message from quizzes")
				continue
			}
			if q.processSendQuizzesToClientMessage(msg) {
				continue
			}
			if q.processLookupQuizForGameMessage(msg) {
				continue
			}
			if q.processDeleteQuizMessage(msg) {
				continue
			}
		}
	}
}

func (q *Quizzes) processDeleteQuizMessage(message interface{}) bool {
	msg, ok := message.(DeleteQuizMessage)
	if !ok {
		return false
	}
	q.delete(msg.quizid)
	return true
}

func (q *Quizzes) processLookupQuizForGameMessage(message interface{}) bool {
	msg, ok := message.(LookupQuizForGameMessage)
	if !ok {
		return false
	}

	quiz, err := q.Get(msg.quizid)
	if err != nil {
		q.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error getting quiz in new game: " + err.Error(),
			nextscreen: "host-select-quiz",
		})
		return true
	}

	q.msghub.Send(messaging.GamesTopic, SetQuizForGameMessage{
		pin:  msg.pin,
		quiz: quiz,
	})

	q.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
		sessionid:  msg.sessionid,
		nextscreen: "host-game-lobby",
	})

	return true
}

func (q *Quizzes) processSendQuizzesToClientMessage(message interface{}) bool {
	msg, ok := message.(SendQuizzesToClientMessage)
	if !ok {
		return false
	}

	type quizMeta struct {
		Id   int    `json:"id"`
		Name string `json:"name"`
	}
	ml := []quizMeta{}
	for _, quiz := range q.GetQuizzes() {
		ml = append(ml, quizMeta{
			Id:   quiz.Id,
			Name: quiz.Name,
		})
	}

	encoded, err := common.ConvertToJSON(&ml)
	if err != nil {
		q.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    fmt.Sprintf("error encoding json: %v", err),
			nextscreen: "host-select-quiz",
		})
		return true
	}
	q.msghub.Send(messaging.ClientHubTopic, ClientMessage{
		client:  msg.client,
		message: "all-quizzes " + encoded,
	})
	return true
}

// called by REST API
func (q *Quizzes) GetQuizzes() []common.Quiz {
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	ids := make([]int, len(q.all))

	i := 0
	for k := range q.all {
		ids[i] = k
		i++
	}
	sort.Ints(ids)

	r := make([]common.Quiz, len(ids))
	for i, id := range ids {
		r[i] = q.all[id]
	}
	return r
}

// called by REST API
func (q *Quizzes) Get(id int) (common.Quiz, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	quiz, ok := q.all[id]
	if !ok {
		return common.Quiz{}, fmt.Errorf("could not find quiz with id %d", id)
	}
	return quiz, nil
}

func (q *Quizzes) delete(id int) {
	q.mutex.Lock()
	delete(q.all, id)
	q.mutex.Unlock()

	if q.engine != nil {
		q.engine.Delete(fmt.Sprintf("quiz:%d", id))
	}
}

// called by REST API
func (q *Quizzes) Add(quiz common.Quiz) error {
	var err error
	quiz.Id, err = q.nextID()
	if err != nil {
		return err
	}

	if q.engine != nil {
		encoded, err := quiz.Marshal()
		if err != nil {
			return fmt.Errorf("error converting quiz to JSON: %v", err)
		}
		if err := q.engine.Set(fmt.Sprintf("quiz:%d", quiz.Id), encoded, 0); err != nil {
			return fmt.Errorf("error persisting quiz to redis: %v", err)
		}
	}

	q.mutex.Lock()
	q.all[quiz.Id] = quiz
	q.mutex.Unlock()
	return nil
}

// called by REST API
func (q *Quizzes) Update(quiz common.Quiz) error {
	q.mutex.Lock()
	q.all[quiz.Id] = quiz
	q.mutex.Unlock()

	if q.engine != nil {
		encoded, err := quiz.Marshal()
		if err != nil {
			return fmt.Errorf("error converting quiz to JSON: %v", err)
		}
		if err := q.engine.Set(fmt.Sprintf("quiz:%d", quiz.Id), encoded, 0); err != nil {
			return fmt.Errorf("error persisting quiz to redis: %v", err)
		}
	}
	return nil
}

func (q *Quizzes) nextID() (int, error) {
	if q.engine == nil {
		q.mutex.RLock()
		defer q.mutex.RUnlock()
		highest := 0
		for key := range q.all {
			if key > highest {
				highest = key
			}
		}
		return highest + 1, nil
	}
	id, err := q.engine.Incr("quizid")
	if err != nil {
		return 0, fmt.Errorf("error generating quiz ID from persistent store: %v", err)
	}
	return id, nil
}
