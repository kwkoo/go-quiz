package pkg

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
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
	signalChan   chan os.Signal
}

func InitMessageHub() *MessageHub {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	signal.Notify(signalChan, syscall.SIGTERM)

	return &MessageHub{
		chans:        make(map[string]chan interface{}),
		shutdownChan: make(chan struct{}),
		signalChan:   signalChan,
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

func (mh *MessageHub) WaitForShutdown() {
	<-mh.signalChan
	log.Print("received signal - shutting down gracefully...")

	// we received a signal - proceed to call all registered listeners
	close(mh.shutdownChan)
	mh.wg.Wait()
	log.Print("all shutdown listeners are done, closing all channels...")
	for _, c := range mh.chans {
		close(c)
	}
	log.Print("shutdown complete")
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
