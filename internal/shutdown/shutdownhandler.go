package shutdown

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	wg           sync.WaitGroup
	shutdownChan chan struct{}
	signalChan   chan os.Signal
)

func InitShutdownHandler() {
	shutdownChan = make(chan struct{})
	signalChan = make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	signal.Notify(signalChan, syscall.SIGTERM)
}

func GetShutdownChan() chan struct{} {
	wg.Add(1)
	return shutdownChan
}

func NotifyShutdownComplete() {
	wg.Done()
}

func WaitForShutdown() {
	<-signalChan
	log.Print("received signal - shutting down gracefully...")

	// we received a signal - proceed to call all registered listeners
	close(shutdownChan)
	wg.Wait()
}
