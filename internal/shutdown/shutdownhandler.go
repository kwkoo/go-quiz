package shutdown

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	wg  sync.WaitGroup
	ctx context.Context
)

func InitShutdownHandler() {
	ctx, _ = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func Context() context.Context {
	wg.Add(1)
	return ctx
}

func NotifyShutdownComplete() {
	wg.Done()
}

func WaitForShutdown() {
	wg.Wait()
}
