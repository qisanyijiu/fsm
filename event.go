package fsm

type Event struct {
	Machine  *Machine
	Event    string
	Src      string
	Dst      string
	Err      error
	Args     []interface{}
	canceled bool
	async    bool
}

func (e *Event) Cancel(err ...error) {
	e.canceled = true
	if len(err) > 0 {
		e.Err = err[0]
	}
}

func (e *Event) Async() {
	e.async = true
}

type eKey struct {
	event string
	src   string
}
