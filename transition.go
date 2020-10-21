package fsm

type transitioner interface {
	transition(machine *Machine) error
}

type transitionerStruct struct{}

func (t transitionerStruct) transition(m *Machine) error {
	if m.transition == nil {
		return NotInTransitionError{}
	}
	m.transition()
	m.transition = nil
	return nil
}
