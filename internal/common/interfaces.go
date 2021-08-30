package common

type ShutdownInformer interface {
	GetShutdownChan() chan struct{}
	NotifyShutdownComplete()
}
