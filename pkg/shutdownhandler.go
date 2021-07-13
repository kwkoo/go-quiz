package pkg

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

var shutdownListeners []func()

func InitShutdownHandler() {
	shutdownListeners = []func(){}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGTERM)
	go func() {
		<-c
		log.Print("received signal - shutting down gracefully...")

		// we received a signal - proceed to call all registered listeners
		for _, f := range shutdownListeners {
			f()
		}

		os.Exit(0)
	}()
}

func RegisterShutdownHandler(f func()) {
	shutdownListeners = append(shutdownListeners, f)
}
