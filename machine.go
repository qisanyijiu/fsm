package fsm

import (
	"strings"
	"sync"
)

type Machine struct {
	current         string
	transitions     map[eKey]string
	callbacks       map[cKey]Callback
	transition      func()
	transitionerObj transitioner
	stateMu         sync.RWMutex
	eventMu         sync.Mutex
}

type EventDesc struct {
	Name string
	Src  []string
	Dst  string
}

type Callback func(event *Event)
type Events []EventDesc
type Callbacks map[string]Callback

func NewMachine(initialState string, events []EventDesc, callbacks Callbacks) *Machine {
	m := &Machine{
		current:         initialState,
		transitionerObj: &transitionerStruct{},
		transitions:     make(map[eKey]string),
		callbacks:       make(map[cKey]Callback),
	}

	// 构建状态迁移字典
	allEvents := make(map[string]bool)
	allStatus := make(map[string]bool)
	for _, e := range events {
		for _, src := range e.Src {
			m.transitions[eKey{e.Name, src}] = e.Dst
			allStatus[src] = true
			allStatus[e.Dst] = true
		}
		allEvents[e.Name] = true
	}


	// 注册所有回调函数
	for name, fn := range callbacks {
		var target string
		var callbackType int
		switch {
		case strings.HasPrefix(name, "before_"):
			target = strings.Trim(name, "before_")
			if target == "event" {
				callbackType = callbackBeforeEvent
			} else if _, ok := allEvents[target]; ok {
				callbackType = callbackBeforeEvent
			}
		case strings.HasPrefix(name, "leave_"):
			target = strings.Trim(name, "leave_")
			if target == "state" {
				callbackType = callbackLeaveState
			} else if _, ok := allStatus[target]; ok {
				callbackType = callbackLeaveState
			}
		case strings.HasPrefix(name, "enter_"):
			target = strings.Trim(name, "enter_")
			if target == "state" {
				callbackType = callbackEnterState
			} else if _, ok := allStatus[target]; ok {
				callbackType = callbackEnterState
			}
		case strings.HasPrefix(name, "after_"):
			target = strings.Trim(name, "after_")
			if target == "event" {
				callbackType = callbackAfterEvent
			} else if _, ok := allEvents[target]; ok {
				callbackType = callbackAfterEvent
			}
		default:
			target = name
			if _, ok := allStatus[target]; ok {
				callbackType = callbackEnterState
			} else if _, ok := allEvents[target]; ok {
				callbackType = callbackAfterEvent
			}
		}
		if callbackType != callbackNone {
			m.callbacks[cKey{target: target, callbackType: callbackType}] = fn
		}
	}
	return m
}

func (m *Machine) Current() string {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.current
}

func (m *Machine) Is(state string) bool {
	return state == m.Current()
}

func (m *Machine) SetState(state string) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.current = state
	return
}

/**
Can: 返回当前状态下event可否执行
*/
func (m *Machine) Can(event string) bool {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	_, ok := m.transitions[eKey{event: event, src: m.current}]
	return ok && (m.transition == nil)
}

/**
AvailableTransitions: 返回当前状态下可以执行的转移
*/
func (m *Machine) AvailableTransitions() []string {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	var transitions []string
	for key := range m.transitions {
		if key.src == m.current {
			transitions = append(transitions, key.event)
		}
	}
	return transitions
}

/**
Cannot: 返回当前状态下event可否执行
*/
func (m *Machine) Cannot(event string) bool {
	return !m.Can(event)
}

func (m *Machine) Event(event string, args ...interface{}) error {
	m.eventMu.Lock()
	defer m.eventMu.Unlock()

	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	if m.transition != nil {
		return InTransitionError{event}
	}

	dst, ok := m.transitions[eKey{event, m.current}]
	if !ok {
		for ekey := range m.transitions {
			if ekey.event == event {
				return InvalidEventError{
					Event: event,
					State: m.current,
				}
			}
			return UnknownEventError{event}
		}
	}

	e := &Event{m, event, m.current, dst, nil, args, false, false}
	// 执行所有回调函数
	err := m.beforeEventCallbacks(e)
	if err != nil {
		return err
	}

	if m.current == dst {
		m.afterEventCallbacks(e)
		return NoTransitionError{e.Err}
	}

	// Setup the transition, call it later.
	m.transition = func() {
		m.stateMu.Lock()
		m.current = dst
		m.stateMu.Unlock()

		m.enterStateCallbacks(e)
		m.afterEventCallbacks(e)
	}

	if err = m.leaveStateCallbacks(e); err != nil {
		if _, ok := err.(CanceledError); ok {
			m.transition = nil
		}
		return err
	}

	// 执行转移
	m.stateMu.RUnlock()
	defer m.stateMu.RLock()
	err = m.doTransition()
	if err != nil {
		return InternalError{}
	}

	return e.Err
}

func (m *Machine) beforeEventCallbacks(e *Event) error {
	if fn, ok := m.callbacks[cKey{
		target:       m.current,
		callbackType: callbackBeforeEvent,
	}]; ok {
		fn(e)
		if e.canceled {
			return CanceledError{e.Err}
		}
	}
	if fn, ok := m.callbacks[cKey{
		target:       "",
		callbackType: callbackBeforeEvent,
	}]; ok {
		fn(e)
		if e.canceled {
			return CanceledError{e.Err}
		}
	}
	return nil
}

func (m *Machine) leaveStateCallbacks(e *Event) error {
	if fn, ok := m.callbacks[cKey{m.current, callbackLeaveState}]; ok {
		fn(e)
		if e.canceled {
			return CanceledError{e.Err}
		} else if e.async {
			return AsyncError{e.Err}
		}
	}
	if fn, ok := m.callbacks[cKey{"", callbackLeaveState}]; ok {
		fn(e)
		if e.canceled {
			return CanceledError{e.Err}
		} else if e.async {
			return AsyncError{e.Err}
		}
	}
	return nil
}

func (m *Machine) enterStateCallbacks(e *Event)  {
	if fn, ok := m.callbacks[cKey{m.current, callbackEnterState}]; ok {
		fn(e)
	}
	if fn, ok := m.callbacks[cKey{"", callbackEnterState}]; ok {
		fn(e)
	}
}

func (m *Machine) afterEventCallbacks(e *Event)  {
	if fn, ok := m.callbacks[cKey{e.Event, callbackAfterEvent}]; ok {
		fn(e)
	}
	if fn, ok := m.callbacks[cKey{"", callbackAfterEvent}]; ok {
		fn(e)
	}
}

func (m *Machine)doTransition() error {
	return m.transitionerObj.transition(m)
}

const (
	callbackNone int = iota
	callbackBeforeEvent
	callbackLeaveState
	callbackEnterState
	callbackAfterEvent
)

type cKey struct {
	target       string
	callbackType int
}
