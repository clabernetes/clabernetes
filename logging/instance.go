package logging

import (
	"fmt"
	"sync"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

// Instance is a logging instance managed by the Manager.
type Instance interface { //nolint: interfacebloat
	Debug(f string)
	Debugf(f string, a ...any)
	Info(f string)
	Infof(f string, a ...any)
	Warn(f string)
	Warnf(f string, a ...any)
	Critical(f string)
	Criticalf(f string, a ...any)
	Fatal(f string)
	Fatalf(f string, a ...any)
	// Write implements io.Writer so that an instance can be used most places. Messages received
	// via Write will always have the current formatter applied, and all messages will be queued
	// for egress unless this logging instance's level is Disabled.
	Write(p []byte) (n int, err error)
	GetName() string
	GetLevel() string
}

type instance struct {
	lock      sync.Mutex
	name      string
	level     string
	formatter Formatter
	c         chan string
	done      chan any
}

func (i *instance) GetName() string {
	return i.name
}

func (i *instance) GetLevel() string {
	return i.level
}

// Debug accepts a Debug level log message with no formatting.
func (i *instance) Debug(f string) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if !i.shouldLog(clabernetesconstants.Debug) {
		return
	}

	i.enqueue(i.formatter(i, clabernetesconstants.Debug, f))
}

// Debugf accepts a Debug level log message normal fmt.Sprintf type formatting.
func (i *instance) Debugf(f string, a ...any) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if !i.shouldLog(clabernetesconstants.Debug) {
		return
	}

	i.enqueue(i.formatter(i, clabernetesconstants.Debug, fmt.Sprintf(f, a...)))
}

// Info accepts an Info level log message with no formatting.
func (i *instance) Info(f string) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if !i.shouldLog(clabernetesconstants.Info) {
		return
	}

	i.enqueue(i.formatter(i, clabernetesconstants.Info, f))
}

// Infof accepts an Info level log message normal fmt.Sprintf type formatting.
func (i *instance) Infof(f string, a ...any) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if !i.shouldLog(clabernetesconstants.Info) {
		return
	}

	i.enqueue(i.formatter(i, clabernetesconstants.Info, fmt.Sprintf(f, a...)))
}

// Warn accepts a Warn level log message with no formatting.
func (i *instance) Warn(f string) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if !i.shouldLog(clabernetesconstants.Warn) {
		return
	}

	i.enqueue(i.formatter(i, clabernetesconstants.Warn, f))
}

// Warnf accepts a Warn level log message normal fmt.Sprintf type formatting.
func (i *instance) Warnf(f string, a ...any) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if !i.shouldLog(clabernetesconstants.Warn) {
		return
	}

	i.enqueue(i.formatter(i, clabernetesconstants.Warn, fmt.Sprintf(f, a...)))
}

// Critical accepts a Critical level log message with no formatting.
func (i *instance) Critical(f string) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if !i.shouldLog(clabernetesconstants.Critical) {
		return
	}

	i.enqueue(i.formatter(i, clabernetesconstants.Critical, f))
}

// Criticalf accepts a Critical level log message normal fmt.Sprintf type formatting.
func (i *instance) Criticalf(f string, a ...any) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if !i.shouldLog(clabernetesconstants.Critical) {
		return
	}

	i.enqueue(i.formatter(i, clabernetesconstants.Critical, fmt.Sprintf(f, a...)))
}

// Fatal accepts a Fatal level log message with no formatting. After emitting the message the log
// manager is flushed and the program is crashed via the calbernetesutil.Panic function.
func (i *instance) Fatal(f string) {
	i.lock.Lock()
	defer i.lock.Unlock()

	formattedMsg := i.formatter(i, clabernetesconstants.Fatal, f)

	i.enqueue(formattedMsg)

	GetManager().Flush()

	clabernetesutil.Panic(formattedMsg)
}

// Fatalf accepts a Fatal level log message normal fmt.Sprintf type formatting. After emitting the
// message the log manager is flushed and the program is crashed via the calbernetesutil.Panic
// function.
func (i *instance) Fatalf(f string, a ...any) {
	i.lock.Lock()
	defer i.lock.Unlock()

	formattedMsg := i.formatter(i, clabernetesconstants.Fatal, fmt.Sprintf(f, a...))

	i.enqueue(formattedMsg)

	GetManager().Flush()

	clabernetesutil.Panic(formattedMsg)
}

// Write allows a logging instance to be used as an io.Writer. Messages received via this method
// will always be logged at informational unless the logger level is Disabled.
func (i *instance) Write(p []byte) (n int, err error) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if i.level == clabernetesconstants.Disabled {
		return 0, nil
	}

	i.enqueue(i.formatter(i, clabernetesconstants.Info, string(p)))

	return len(p), nil
}

func (i *instance) enqueue(m string) {
	i.c <- m
}

func (i *instance) shouldLog(l string) bool {
	switch i.level {
	case clabernetesconstants.Disabled:
		return false
	case clabernetesconstants.Debug:
		return true
	case clabernetesconstants.Info:
		switch l {
		case clabernetesconstants.Info, clabernetesconstants.Warn, clabernetesconstants.Critical:
			return true
		default:
			return false
		}
	case clabernetesconstants.Warn:
		switch l {
		case clabernetesconstants.Warn, clabernetesconstants.Critical:
			return true
		default:
			return false
		}
	case clabernetesconstants.Critical:
		if l == clabernetesconstants.Critical {
			return true
		}
	}

	return false
}
