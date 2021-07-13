package pkg

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	signalChan   chan os.Signal
	shutdownChan chan struct{} // this channel will be closed when the relevant signals are received
	shutdownWG   sync.WaitGroup
)

func InitShutdownHandler() {
	shutdownChan = make(chan struct{})

	signalChan = make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	signal.Notify(signalChan, syscall.SIGTERM)
}

func WaitForShutdown() {
	<-signalChan
	log.Print("received signal - shutting down gracefully...")

	// we received a signal - proceed to call all registered listeners
	close(shutdownChan)
	shutdownWG.Wait()
	log.Print("All shutdown listeners are done")
}

func GetShutdownArtifacts() (chan struct{}, *sync.WaitGroup) {
	return shutdownChan, &shutdownWG
}
