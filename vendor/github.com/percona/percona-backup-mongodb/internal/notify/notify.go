package notify

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const EventNotFound = "Event not found"

var rwMutex sync.RWMutex
var events = make(map[interface{}][]chan interface{})

func Start(event interface{}) chan interface{} {
	outputChan := make(chan interface{})
	rwMutex.Lock()
	events[event] = append(events[event], outputChan)
	rwMutex.Unlock()
	return outputChan
}

// Stop observing the specified event on the provided output channel
func Stop(event interface{}, outputChan chan interface{}) error {
	rwMutex.Lock()
	defer rwMutex.Unlock()

	newArray := make([]chan interface{}, 0)
	outChans, ok := events[event]
	if !ok {
		return errors.New(EventNotFound)
	}
	for _, ch := range outChans {
		if ch != outputChan {
			newArray = append(newArray, ch)
		} else {
			close(ch)
		}
	}
	events[event] = newArray

	return nil
}

// Stop observing the specified event on all channels
func StopAll(event string) {
	rwMutex.Lock()
	defer rwMutex.Unlock()

	outChans, ok := events[event]
	if !ok {
		return
	}
	for _, ch := range outChans {
		close(ch)
	}
	delete(events, event)

	return
}

// Post a notification (arbitrary data) to the specified event
func Post(event interface{}, data interface{}) error {
	rwMutex.RLock()
	defer rwMutex.RUnlock()

	outChans, ok := events[event]
	if !ok {
		return fmt.Errorf("There are no listeners for this event: %q", event)
	}
	for _, outputChan := range outChans {
		select {
		case outputChan <- data:
		default:
		}
	}

	return nil
}

// Post a notification to the specified event using the provided timeout for
// any output channels that are blocking
func PostTimeout(event interface{}, data interface{}, timeout time.Duration) error {
	rwMutex.RLock()
	defer rwMutex.RUnlock()

	outChans, ok := events[event]
	if !ok {
		return fmt.Errorf("There are no listeners for this event: %q", event)
	}
	wg := sync.WaitGroup{}

	for _, outputChan := range outChans {
		wg.Add(1)
		go func(outChan chan interface{}) {
			select {
			case outChan <- data:
			case <-time.After(timeout):
			}
			wg.Done()
		}(outputChan)
	}
	wg.Wait()

	return nil
}
