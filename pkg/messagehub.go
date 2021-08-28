package pkg

import (
	"log"
	"sync"
)

const chanSize = 20

// topics
const (
	incomingMessageTopic = "from-clients"
	clientHubTopic       = "client-hub"
	sessionsTopic        = "sessions-hub"
	gamesTopic           = "games-hub"
	quizzesTopic         = "quizzes"
)

type MessageHub struct {
	wg           sync.WaitGroup
	mux          sync.Mutex
	chans        map[string](chan interface{})
	shutdownChan chan struct{}
}

func InitMessageHub() *MessageHub {
	return &MessageHub{
		chans:        make(map[string]chan interface{}),
		shutdownChan: make(chan struct{}),
	}
}

func (mh *MessageHub) Send(topicname string, msg interface{}) {
	topic := mh.GetTopic(topicname)
	topic <- msg
}

func (mh *MessageHub) GetShutdownChan() chan struct{} {
	mh.wg.Add(1)
	return mh.shutdownChan
}

func (mh *MessageHub) NotifyShutdownComplete() {
	mh.wg.Done()
}

func (mh *MessageHub) Shutdown() {
	close(mh.shutdownChan)
	mh.wg.Wait()
}

func (mh *MessageHub) GetTopic(name string) chan interface{} {
	mh.mux.Lock()
	defer mh.mux.Unlock()
	topic, ok := mh.chans[name]
	if ok {
		return topic
	}
	topic = make(chan interface{}, chanSize)
	mh.chans[name] = topic
	log.Printf("created topic %s", name)
	return topic
}
