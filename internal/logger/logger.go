package logger

import (
	"sync"

	"go.uber.org/zap"
)

var Log *zap.Logger
var m sync.Mutex

func InitLogger() {
	m.Lock()
	defer m.Unlock()

	Log = newZapLogger()
}
