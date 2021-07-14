package pkg

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

type ShutdownArtifacts struct {
	Ch chan struct{}  // this channel will be closed when the relevant signals are received
	Wg sync.WaitGroup // shutdown handlers will Add to this WaitGroup - the process will Wait on this WaitGroup before exiting
}

var (
	signalChan        chan os.Signal
	shutdownArtifacts ShutdownArtifacts
)

func InitShutdownHandler() {
	shutdownArtifacts.Ch = make(chan struct{})

	signalChan = make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	signal.Notify(signalChan, syscall.SIGTERM)
}

func WaitForShutdown() {
	<-signalChan
	log.Print("received signal - shutting down gracefully...")

	// we received a signal - proceed to call all registered listeners
	close(shutdownArtifacts.Ch)
	shutdownArtifacts.Wg.Wait()
	log.Print("All shutdown listeners are done")
}

func GetShutdownArtifacts() *ShutdownArtifacts {
	return &shutdownArtifacts
}
